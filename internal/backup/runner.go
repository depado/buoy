package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/depado/buoy/internal/docker"
	"github.com/depado/buoy/internal/hook"
	"github.com/depado/buoy/internal/notify"
	"github.com/depado/buoy/internal/restic"
)

type Runner struct {
	docker            *docker.Client
	restic            *restic.Client
	hook              *hook.Executor
	repos             []string
	defaultSchedule   string
	defaultRetention  string
	ignoredIDs        *sync.Map
	notifier          *notify.Notifier
	logger            *slog.Logger
	execTimeout       time.Duration
	healthWaitTimeout time.Duration
}

func New(d *docker.Client, r *restic.Client, h *hook.Executor, repos []string, defaultSchedule, defaultRetention string, ignoredIDs *sync.Map, notifier *notify.Notifier, logger *slog.Logger, execTimeout, healthWaitTimeout time.Duration) *Runner {
	return &Runner{
		docker:            d,
		restic:            r,
		hook:              h,
		repos:             repos,
		defaultSchedule:   defaultSchedule,
		defaultRetention:  defaultRetention,
		ignoredIDs:        ignoredIDs,
		notifier:          notifier,
		logger:            logger,
		execTimeout:       execTimeout,
		healthWaitTimeout: healthWaitTimeout,
	}
}

func (r *Runner) parseConfig(labels map[string]string) docker.BackupConfig {
	return docker.ParseBackupConfig(labels, r.defaultSchedule, r.defaultRetention)
}

func (r *Runner) resolveRepos(ctr *docker.Container, cfg docker.BackupConfig) ([]string, error) {
	bases := r.repos
	if len(cfg.ReposOverride) > 0 {
		bases = cfg.ReposOverride
	}
	repos := make([]string, len(bases))
	for i, base := range bases {
		path := ctr.RepoPath(base)
		if isLocalPath(path) {
			abs, err := filepath.Abs(path)
			if err != nil {
				return nil, fmt.Errorf("repo path for base %q: %w", base, err)
			}
			path = abs
		}
		repos[i] = path
	}
	return repos, nil
}

// isLocalPath reports whether p is a local filesystem path rather than a
// remote backend URL. Local paths are absolute (/..., ./..., ../...) or
// have no colon-separated scheme prefix (s3:, b2:, sftp:, etc.).
func isLocalPath(p string) bool {
	if filepath.IsAbs(p) {
		return true
	}
	if strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") {
		return true
	}
	// Check for scheme colon: restic remote backends use <scheme>:<path>.
	// Colon can also appear in Windows paths (C:\) but buoy targets Linux
	// containers. s3:, b2:, sftp:, rest:, rclone:, gs:, azure: all match.
	idx := strings.Index(p, ":")
	if idx == -1 {
		return true
	}
	// Has a colon — could be remote or Windows. Treat as remote unless
	// it's clearly a Windows path (letter-colon-backslash).
	if idx == 1 && len(p) > 2 && p[2] == '\\' {
		return true // Windows absolute path e.g. C:\...
	}
	return false
}

func (r *Runner) Run(ctx context.Context, ctr *docker.Container) error {
	l := r.logger.With(ctr.LogAttrs()...)

	fresh, err := r.docker.InspectContainer(ctx, ctr.ID)
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}

	if fresh.State != "running" {
		l.Debug("skipping, not running", "state", fresh.State)
		return nil
	}

	cfg := r.parseConfig(fresh.Labels)

	r.runPreHooks(ctx, fresh, cfg, l)

	l.Info("starting backup", "stop", cfg.StopBefore)
	wasRunning := false
	if cfg.StopBefore {
		r.ignore(fresh.ID)
		defer r.release(fresh.ID)
		l.Debug("stopping container")
		if err := r.docker.StopContainer(ctx, fresh.ID, cfg.StopTimeout); err != nil {
			return fmt.Errorf("stop container: %w", err)
		}
		if err := r.docker.ContainerWait(ctx, fresh.ID, container.WaitConditionNotRunning); err != nil {
			return fmt.Errorf("wait for stop: %w", err)
		}
		l.Info("container stopped")
		wasRunning = true
	}

	r.backupMounts(ctx, fresh, l)

	if wasRunning {
		l.Debug("starting container")
		if err := r.docker.StartContainer(ctx, fresh.ID); err != nil {
			return fmt.Errorf("start container: %w", err)
		}
		l.Info("container started")
	}

	if wasRunning {
		r.waitRunning(ctx, fresh)
	}

	r.runPostHooks(ctx, fresh, cfg, l)
	r.applyRetention(ctx, fresh, cfg, l)

	l.Info("backup complete")
	r.notifier.SendInfo(
		fmt.Sprintf("buoy backup complete: %s", ctr.Name),
		fmt.Sprintf("Backup completed for container %s", ctr.Name),
	)
	return nil
}

func (r *Runner) RunStackBatch(ctx context.Context, project string, batch []*docker.Container) error {
	l := r.logger.With("project", project)

	summaries, err := r.docker.ListContainersByProject(ctx, project)
	if err != nil {
		return fmt.Errorf("list stack containers: %w", err)
	}
	l.Debug("listed stack containers", "count", len(summaries))

	all := make([]*docker.Container, 0, len(summaries))
	byID := make(map[string]*docker.Container, len(summaries))
	for i := range summaries {
		ctr, err := r.docker.InspectContainer(ctx, summaries[i].ID)
		if err != nil {
			l.Warn("failed to inspect stack container", "id", summaries[i].ID, "error", err)
			continue
		}
		all = append(all, ctr)
		byID[ctr.ID] = ctr
	}

	fresh := make([]*docker.Container, 0, len(batch))
	for _, ctr := range batch {
		inspected, ok := byID[ctr.ID]
		if !ok {
			l.Warn("batch container not found in stack", "id", ctr.ID)
			continue
		}
		fresh = append(fresh, inspected)
	}

	fresh = deduplicateByService(fresh)

	var wg sync.WaitGroup
	for _, ctr := range fresh {
		wg.Add(1)
		go func(c *docker.Container) {
			defer wg.Done()
			cfg := r.parseConfig(c.Labels)
			r.runPreHooks(ctx, c, cfg, l)
		}(ctr)
	}
	wg.Wait()

	stopSvc := stopSet(fresh, all)
	l.Info("starting stack backup", "services", len(fresh), "stop_set", mapKeys(stopSvc))
	services := serviceContainers(all)
	deps := serviceDeps(all)
	stopOrder := orderForStopFromDeps(deps, func(svc string) {
		l.Warn("dependency cycle detected", "service", svc, "project", project)
	})
	wasStopped := make(map[string]bool)
	stopFailed := make(map[string]bool)
	ignoredInBatch := make(map[string]bool)

	for _, svc := range stopOrder {
		for _, ctr := range services[svc] {
			if !stopSvc[svc] {
				continue
			}
			sl := l.With(ctr.LogAttrs()...)
			r.ignore(ctr.ID)
			ignoredInBatch[ctr.ID] = true
			sl.Debug("stopping container")
			cfg := docker.ParseBackupConfig(ctr.Labels, "", "")
			if err := r.docker.StopContainer(ctx, ctr.ID, cfg.StopTimeout); err != nil {
				sl.Warn("failed to stop container", "error", err)
				stopFailed[ctr.ID] = true
				r.release(ctr.ID)
				delete(ignoredInBatch, ctr.ID)
				continue
			}
			if err := r.docker.ContainerWait(ctx, ctr.ID, container.WaitConditionNotRunning); err != nil {
				sl.Warn("failed to wait for container stop", "error", err)
			}
			sl.Info("container stopped")
			wasStopped[ctr.ID] = true
		}
	}

	for _, ctr := range fresh {
		if stopFailed[ctr.ID] {
			l.With("service", ctr.ComposeService).Warn("skipping backup, stop failed")
			continue
		}
		r.backupMounts(ctx, ctr, l.With("service", ctr.ComposeService))
	}

	startOrder := orderForStartFromDeps(deps, func(svc string) {
		l.Warn("dependency cycle detected", "service", svc, "project", project)
	})
	for _, svc := range startOrder {
		if err := r.waitForDeps(ctx, deps, all, svc, l); err != nil {
			l.Warn("failed waiting for dependencies", "service", svc, "error", err)
		}
		for _, ctr := range services[svc] {
			if !wasStopped[ctr.ID] {
				continue
			}
			cl := l.With(ctr.LogAttrs()...)
			cl.Debug("starting container")
			if err := r.docker.StartContainer(ctx, ctr.ID); err != nil {
				cl.Warn("failed to start container", "error", err)
			} else {
				cl.Info("container started")
			}
		}
	}

	for _, ctr := range all {
		if wasStopped[ctr.ID] {
			r.waitRunning(ctx, ctr)
		}
	}

	for _, ctr := range fresh {
		cfg := r.parseConfig(ctr.Labels)
		r.runPostHooks(ctx, ctr, cfg, l)
		r.applyRetention(ctx, ctr, cfg, l)
	}

	for id := range ignoredInBatch {
		r.release(id)
	}

	l.Info("stack backup complete")
	r.notifier.SendInfo(
		fmt.Sprintf("buoy stack backup complete: %s", project),
		fmt.Sprintf("Backup completed for project %s (%d services)", project, len(fresh)),
	)
	return nil
}

func (r *Runner) runPreHooks(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) {
	if cfg.PreBackupCmd != "" {
		if err := r.hook.ExecOnHost(ctx, cfg.PreBackupCmd); err != nil {
			l.Warn("pre-backup host command failed", "error", err)
		}
	}
	if cfg.PreBackupExec != "" {
		execCtx, cancel := context.WithTimeout(ctx, r.execTimeout)
		defer cancel()
		if err := r.hook.ExecInContainer(execCtx, ctr.ID, cfg.PreBackupExec); err != nil {
			l.Warn("pre-backup exec failed", "error", err)
		}
	}
}

func (r *Runner) runPostHooks(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) {
	if cfg.PostBackupExec != "" {
		execCtx, cancel := context.WithTimeout(ctx, r.execTimeout)
		defer cancel()
		if err := r.hook.ExecInContainer(execCtx, ctr.ID, cfg.PostBackupExec); err != nil {
			l.Warn("post-backup exec failed", "error", err)
		}
	}
	if cfg.PostBackupCmd != "" {
		if err := r.hook.ExecOnHost(ctx, cfg.PostBackupCmd); err != nil {
			l.Warn("post-backup host command failed", "error", err)
		}
	}
}

func (r *Runner) backupMounts(ctx context.Context, ctr *docker.Container, l *slog.Logger) {
	cfg := r.parseConfig(ctr.Labels)

	repos, err := r.resolveRepos(ctr, cfg)
	if err != nil {
		l.Warn("failed to resolve repo paths, skipping backup", "error", err)
		return
	}

	if len(cfg.IncludeVolumes) > 0 && len(cfg.ExcludeVolumes) > 0 {
		l.Warn("both include-volumes and exclude-volumes set, exclude-volumes ignored")
	}
	if len(cfg.IncludeMounts) > 0 && len(cfg.ExcludeMounts) > 0 {
		l.Warn("both include-mounts and exclude-mounts set, exclude-mounts ignored")
	}

	for _, repo := range repos {
		repoL := l.With("repo", repo)

		exists, err := r.restic.RepoExists(ctx, repo)
		if err != nil {
			repoL.Warn("failed to check repo, skipping repo", "error", err)
			continue
		}
		if !exists {
			repoL.Debug("repo not found, initializing")
			if err := r.restic.Init(ctx, repo); err != nil {
				repoL.Warn("failed to init repo, skipping repo", "error", err)
				continue
			}
			repoL.Info("initialized repo")
		}

		if err := r.restic.Unlock(ctx, repo); err != nil {
			repoL.Warn("failed to unlock repo", "error", err)
		}

		for _, m := range ctr.Mounts {
			if m.Type == "tmpfs" {
				continue
			}
			if len(cfg.IncludeVolumes) > 0 {
				if !contains(cfg.IncludeVolumes, m.Name) {
					continue
				}
			} else if contains(cfg.ExcludeVolumes, m.Name) {
				continue
			}
			if len(cfg.IncludeMounts) > 0 {
				if !contains(cfg.IncludeMounts, m.Source) && !contains(cfg.IncludeMounts, m.Destination) {
					continue
				}
			} else if contains(cfg.ExcludeMounts, m.Source) || contains(cfg.ExcludeMounts, m.Destination) {
				continue
			}

			source := m.Source
			if _, err := os.Stat(source); os.IsNotExist(err) {
				repoL.Warn("mount source does not exist, skipping", "source", source, "type", m.Type)
				continue
			}

			mountTag := m.Name
			if mountTag == "" {
				mountTag = m.Destination
			}

			opts := restic.BackupOptions{
				Tags:     append(cfg.Tags, "mount:"+mountTag),
				Excludes: cfg.ExcludePatterns,
				Files:    cfg.Files,
				Hostname: ctr.Name,
				WorkDir:  source,
			}

			paths := []string{"."}
			if len(cfg.Files) == 0 {
				entries, err := os.ReadDir(source)
				if err != nil {
					repoL.Warn("failed to read source directory, skipping", "source", source, "error", err)
					continue
				}
				if len(entries) == 0 {
					repoL.Debug("source directory is empty, skipping", "source", source)
					continue
				}
				paths = make([]string, 0, len(entries))
				for _, e := range entries {
					paths = append(paths, e.Name())
				}
			}

			result, err := r.restic.Backup(ctx, repo, paths, opts)
			if err != nil {
				if result != nil {
					l.Error("backup completed with errors",
						"repo", repo,
						"mount", source,
						"snapshot", result.SnapshotID,
						"error", err,
					)
				} else {
					l.Error("backup failed", "repo", repo, "mount", source, "error", err)
				}
				r.notifier.SendBackupError(ctr.Name,
					fmt.Sprintf("Backup failed for mount %s on repo %s: %s", source, repo, err.Error()))
				continue
			}
			if result == nil {
				l.Error("backup produced no summary", "repo", repo, "mount", source)
				r.notifier.SendBackupError(ctr.Name,
					fmt.Sprintf("Backup produced no summary for mount %s on repo %s", source, repo))
				continue
			}
			l.Info("backup complete",
				"repo", repo,
				"mount", source,
				"snapshot", result.SnapshotID,
				slog.Duration("duration", time.Duration(result.TotalDuration*float64(time.Second))),
			)
		}
	}
}

func (r *Runner) applyRetention(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) {
	repos, err := r.resolveRepos(ctr, cfg)
	if err != nil {
		l.Warn("failed to resolve repo paths, skipping retention", "error", err)
		return
	}

	policy := cfg.Retention

	for _, repo := range repos {
		if err := r.restic.Forget(ctx, repo, policy); err != nil {
			l.Warn("forget failed", "repo", repo, "error", err)
			r.notifier.SendBackupError(ctr.Name,
				fmt.Sprintf("Forget failed on repo %s: %s", repo, err.Error()))
		}
		if err := r.restic.Prune(ctx, repo); err != nil {
			l.Warn("prune failed", "repo", repo, "error", err)
			r.notifier.SendBackupError(ctr.Name,
				fmt.Sprintf("Prune failed on repo %s: %s", repo, err.Error()))
		}
	}
}

func (r *Runner) waitForDeps(ctx context.Context, deps map[string][]depInfo, ctrs []*docker.Container, serviceName string, l *slog.Logger) error {
	svcMap := serviceContainers(ctrs)

	for _, dep := range depConditionsFrom(deps, serviceName) {
		depCtrs := svcMap[dep.Name]
		if len(depCtrs) == 0 {
			continue
		}
		for _, ctr := range depCtrs {
			l.With("dependency", dep.Name, "condition", dep.Condition).Debug("waiting for dependency")
			if err := r.waitForCondition(ctx, ctr, dep.Condition); err != nil {
				return err
			}
			if dep.Condition == "service_healthy" {
				l.With("dependency", dep.Name).Info("container healthy")
			} else {
				l.With("dependency", dep.Name).Debug("dependency satisfied")
			}
		}
	}
	return nil
}

func (r *Runner) waitForCondition(ctx context.Context, ctr *docker.Container, condition string) error {
	ctx, cancel := context.WithTimeout(ctx, r.healthWaitTimeout)
	defer cancel()

	check := func() (bool, error) {
		fresh, err := r.docker.InspectContainer(ctx, ctr.ID)
		if err != nil {
			return false, err
		}

		switch condition {
		case "service_healthy":
			if fresh.Health == nil {
				return false, fmt.Errorf("%s has no healthcheck configured", ctr.Name)
			}
			if fresh.Health.Status == "unhealthy" {
				return false, fmt.Errorf("%s is unhealthy", ctr.Name)
			}
			return fresh.Health.Status == "healthy", nil
		case "service_started", "service_running_or_healthy":
			return fresh.State == "running", nil
		case "service_completed_successfully":
			if fresh.State == "exited" {
				if fresh.ExitCode != 0 {
					return false, fmt.Errorf("%s exited with code %d", ctr.Name, fresh.ExitCode)
				}
				return true, nil
			}
			return false, nil
		default:
			return false, fmt.Errorf("unknown dependency condition: %s", condition)
		}
	}

	if done, err := check(); done || err != nil {
		return err
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}

		if done, err := check(); done || err != nil {
			return err
		}
	}
}

func (r *Runner) waitRunning(ctx context.Context, ctr *docker.Container) {
	ctx, cancel := context.WithTimeout(ctx, r.healthWaitTimeout)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fresh, err := r.docker.InspectContainer(ctx, ctr.ID)
			if err != nil || fresh.State == "running" || fresh.State == "exited" {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (r *Runner) ignore(id string)  { r.ignoredIDs.Store(id, true) }
func (r *Runner) release(id string) { r.ignoredIDs.Delete(id) }

func deduplicateByService(ctrs []*docker.Container) []*docker.Container {
	seen := make(map[string]bool)
	out := make([]*docker.Container, 0, len(ctrs))
	for _, c := range ctrs {
		svc := c.ComposeService
		if svc == "" {
			out = append(out, c)
			continue
		}
		if seen[svc] {
			continue
		}
		seen[svc] = true
		out = append(out, c)
	}
	return out
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func (r *Runner) CheckKnownRepos(ctx context.Context) {
	l := r.logger.With("component", "check")
	containers, err := r.docker.ListBackupContainers(ctx)
	if err != nil {
		l.Error("check: failed to list containers", "error", err)
		return
	}

	seen := make(map[string]bool)
	for _, ctr := range containers {
		cfg := r.parseConfig(ctr.Labels)
		repos, err := r.resolveRepos(&ctr, cfg)
		if err != nil {
			l.Warn("check: failed to resolve repos", "container", ctr.Name, "error", err)
			continue
		}
		for _, repo := range repos {
			if seen[repo] {
				continue
			}
			seen[repo] = true
			if err := r.restic.Check(ctx, repo); err != nil {
				l.Error("check: repository check failed", "repo", repo, "error", err)
				r.notifier.SendBackupError("repository-check",
					fmt.Sprintf("Check failed for repo %s: %s", repo, err.Error()))
			} else {
				l.Info("check: repository ok", "repo", repo)
			}
		}
	}
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
