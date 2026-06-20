package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/depado/buoy/internal/docker"
	"github.com/depado/buoy/internal/hook"
	"github.com/depado/buoy/internal/notify"
	"github.com/depado/buoy/internal/registry"
	"github.com/depado/buoy/internal/restic"
)

type Runner struct {
	docker            *docker.Client
	restic            *restic.Client
	hook              *hook.Executor
	repoReg           *registry.Registry
	defaultSchedule   string
	defaultRetention  string
	ignoredIDs        *sync.Map
	notifier          *notify.Notifier
	logger            *slog.Logger
	execTimeout       time.Duration
	healthWaitTimeout time.Duration
	backupTimeout     time.Duration
}

// RunnerConfig holds the dependencies and configuration for a Runner.
type RunnerConfig struct {
	Docker            *docker.Client
	Restic            *restic.Client
	Hook              *hook.Executor
	Registry          *registry.Registry
	DefaultSchedule   string
	DefaultRetention  string
	IgnoredIDs        *sync.Map
	Notifier          *notify.Notifier
	Logger            *slog.Logger
	ExecTimeout       time.Duration
	HealthWaitTimeout time.Duration
	BackupTimeout     time.Duration
}

func New(cfg *RunnerConfig) *Runner {
	return &Runner{
		docker:            cfg.Docker,
		restic:            cfg.Restic,
		hook:              cfg.Hook,
		repoReg:           cfg.Registry,
		defaultSchedule:   cfg.DefaultSchedule,
		defaultRetention:  cfg.DefaultRetention,
		ignoredIDs:        cfg.IgnoredIDs,
		notifier:          cfg.Notifier,
		logger:            cfg.Logger,
		execTimeout:       cfg.ExecTimeout,
		healthWaitTimeout: cfg.HealthWaitTimeout,
		backupTimeout:     cfg.BackupTimeout,
	}
}

func (r *Runner) parseConfig(labels map[string]string) docker.BackupConfig {
	return docker.ParseBackupConfig(labels, r.defaultSchedule, r.defaultRetention)
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

	repos, err := r.repoReg.SyncContainer(fresh, cfg)
	if err != nil {
		l.Warn("failed to sync container repos", "error", err)
	}
	if len(repos) == 0 {
		return fmt.Errorf("no repos resolved for container %s", ctr.Name)
	}

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
		wasRunning = true
		if err := r.docker.ContainerWait(ctx, fresh.ID, container.WaitConditionNotRunning); err != nil {
			l.Warn("container wait for stop failed, proceeding anyway", "error", err)
		} else {
			l.Info("container stopped")
		}
	}

	backupErr := r.backupMounts(ctx, fresh, cfg, repos, l)

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
	if backupErr == nil {
		r.applyRetention(ctx, fresh, cfg, repos, l)
	}

	if backupErr != nil {
		l.Error("backup completed with failures", "error", backupErr)
		r.notifier.SendBackupError(ctr.Name, fmt.Sprintf("Backup failed: %s", backupErr.Error()))
		return backupErr
	}

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

	var containerCfg map[string]docker.BackupConfig
	var containerRepos map[string][]string
	containerCfg = make(map[string]docker.BackupConfig, len(fresh))
	containerRepos = make(map[string][]string, len(fresh))
	for _, ctr := range fresh {
		cfg := r.parseConfig(ctr.Labels)
		repos, err := r.repoReg.SyncContainer(ctr, cfg)
		if err != nil {
			l.Warn("failed to sync container repos", "service", ctr.ComposeService, "error", err)
		}
		containerCfg[ctr.ID] = cfg
		containerRepos[ctr.ID] = repos
	}

	var wg sync.WaitGroup
	for _, ctr := range fresh {
		wg.Add(1)
		go func(c *docker.Container) {
			defer wg.Done()
			cfg := containerCfg[c.ID]
			r.runPreHooks(ctx, c, cfg, l.With(c.LogAttrs()...))
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

	backupErrors := make(map[string]error)
	for _, ctr := range fresh {
		if stopFailed[ctr.ID] {
			l.With("service", ctr.ComposeService).Warn("skipping backup, stop failed")
			backupErrors[ctr.ComposeService] = fmt.Errorf("stop failed")
			continue
		}
		if err := r.backupMounts(ctx, ctr, containerCfg[ctr.ID], containerRepos[ctr.ID], l.With("service", ctr.ComposeService)); err != nil {
			l.Error("backup failed for service", "service", ctr.ComposeService, "error", err)
			r.notifier.SendBackupError(ctr.Name, fmt.Sprintf("Backup failed for service %s: %s", ctr.ComposeService, err.Error()))
			backupErrors[ctr.ComposeService] = err
		}
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
		cfg := containerCfg[ctr.ID]
		r.runPostHooks(ctx, ctr, cfg, l)
		if _, failed := backupErrors[ctr.ComposeService]; !failed {
			r.applyRetention(ctx, ctr, cfg, containerRepos[ctr.ID], l)
		}
	}

	for id := range ignoredInBatch {
		r.release(id)
	}

	l.Info("stack backup complete", "services", len(fresh)-len(backupErrors), "failed", len(backupErrors))
	if len(backupErrors) > 0 {
		r.notifier.SendBackupError(project, fmt.Sprintf("Stack backup had %d/%d failed services", len(backupErrors), len(fresh)))
		return fmt.Errorf("stack backup: %d/%d services failed", len(backupErrors), len(fresh))
	}
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

type mountError struct {
	mount string
	repo  string
	err   error
}

func (r *Runner) backupMounts(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, repos []string, logger *slog.Logger) error {
	if len(cfg.IncludeVolumes) > 0 && len(cfg.ExcludeVolumes) > 0 {
		logger.Warn("both include-volumes and exclude-volumes set, exclude-volumes ignored")
	}
	if len(cfg.IncludeMounts) > 0 && len(cfg.ExcludeMounts) > 0 {
		logger.Warn("both include-mounts and exclude-mounts set, exclude-mounts ignored")
	}

	mountCount := 0
	var failures []mountError

	for _, repo := range repos {
		l := logger.With("repo", repo)
		repoOK := true

		exists, err := r.restic.RepoExists(ctx, repo)
		if err != nil {
			l.Warn("failed to check repo, skipping repo", "error", err)
			failures = append(failures, mountError{repo: repo, err: fmt.Errorf("repo check: %w", err)})
			r.repoReg.MarkBackupComplete(repo, false) //nolint:errcheck
			continue
		}
		if !exists {
			l.Debug("repo not found, initializing")
			if err := r.restic.Init(ctx, repo); err != nil {
				l.Warn("failed to init repo, skipping repo", "error", err)
				failures = append(failures, mountError{repo: repo, err: fmt.Errorf("repo init: %w", err)})
				r.repoReg.MarkBackupComplete(repo, false) //nolint:errcheck
				continue
			}
			l.Info("initialized repo")
		}

		if err := r.restic.Unlock(ctx, repo); err != nil {
			l.Warn("failed to unlock repo", "error", err)
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

			mountCount++
			source := m.Source
			if _, err := os.Stat(source); os.IsNotExist(err) {
				l.Warn("mount source does not exist, skipping", "source", source, "type", m.Type)
				r.notifier.SendBackupError(ctr.Name,
					fmt.Sprintf("Mount source not found (backup skipped): %s (%s)", source, m.Type))
				failures = append(failures, mountError{mount: source, repo: repo, err: fmt.Errorf("mount source not found")})
				repoOK = false
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
					l.Warn("failed to read source directory, skipping", "source", source, "error", err)
					r.notifier.SendBackupError(ctr.Name,
						fmt.Sprintf("Failed to read mount source (backup skipped): %s (%v)", source, err))
					failures = append(failures, mountError{mount: source, repo: repo, err: fmt.Errorf("read dir: %w", err)})
					repoOK = false
					continue
				}
				if len(entries) == 0 {
					l.Debug("source directory is empty, skipping", "source", source)
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
				failures = append(failures, mountError{mount: source, repo: repo, err: err})
				repoOK = false
				continue
			}
			if result == nil {
				l.Error("backup produced no summary", "repo", repo, "mount", source)
				r.notifier.SendBackupError(ctr.Name,
					fmt.Sprintf("Backup produced no summary for mount %s on repo %s", source, repo))
				failures = append(failures, mountError{mount: source, repo: repo, err: fmt.Errorf("no summary")})
				repoOK = false
				continue
			}
			l.Info("backup complete",
				"repo", repo,
				"mount", source,
				"snapshot", result.SnapshotID,
				slog.Duration("duration", time.Duration(result.TotalDuration*float64(time.Second))),
			)
		}
		_ = r.repoReg.MarkBackupComplete(repo, repoOK)
	}

	if mountCount == 0 {
		logger.Warn("container has no backup-eligible mounts")
	}

	if mountCount > 0 && len(failures) == mountCount*len(repos) {
		return fmt.Errorf("all %d mounts failed across %d repos", mountCount, len(repos))
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d mount/repo failures (first: %s)", len(failures), failures[0].err)
	}
	return nil
}

func (r *Runner) applyRetention(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, repos []string, logger *slog.Logger) {
	logger.Debug("applying retention", "policy", cfg.Retention, "repos", len(repos))
	policy := cfg.Retention

	for _, repo := range repos {
		l := logger
		if ctr.ComposeService != "" {
			l = logger.With("service", ctr.ComposeService)
		}
		l = l.With("repo", repo)

		start := time.Now()
		if err := r.restic.Forget(ctx, repo, policy, ctr.Name); err != nil {
			l.Warn("forget failed", "error", err)
			r.notifier.SendBackupError(ctr.Name,
				fmt.Sprintf("Forget failed on repo %s: %s", repo, err.Error()))
		}
		if err := r.restic.Prune(ctx, repo); err != nil {
			l.Warn("prune failed", "error", err)
			r.notifier.SendBackupError(ctr.Name,
				fmt.Sprintf("Prune failed on repo %s: %s", repo, err.Error()))
		}
		l.Info("retention complete", slog.Duration("duration", time.Since(start)))
	}
}

func (r *Runner) waitForDeps(ctx context.Context, deps map[string][]depInfo, ctrs []*docker.Container, serviceName string, logger *slog.Logger) error {
	svcMap := serviceContainers(ctrs)

	for _, dep := range depConditionsFrom(deps, serviceName) {
		depCtrs := svcMap[dep.Name]
		if len(depCtrs) == 0 {
			continue
		}
		for _, ctr := range depCtrs {
			l := logger.With("dependency", dep.Name, "condition", dep.Condition)
			l.Debug("waiting for dependency")
			if err := r.waitForCondition(ctx, ctr, dep.Condition); err != nil {
				return err
			}
			if dep.Condition == ServiceHealthy {
				l.Info("container healthy")
			} else {
				l.Debug("dependency satisfied")
			}
		}
	}
	return nil
}

func (r *Runner) waitForCondition(ctx context.Context, ctr *docker.Container, condition DepCondition) error {
	ctx, cancel := context.WithTimeout(ctx, r.healthWaitTimeout)
	defer cancel()

	check := func() (bool, error) {
		fresh, err := r.docker.InspectContainer(ctx, ctr.ID)
		if err != nil {
			return false, err
		}

		switch condition {
		case ServiceHealthy:
			if fresh.Health == nil {
				return false, fmt.Errorf("%s has no healthcheck configured", ctr.Name)
			}
			if fresh.Health.Status == "unhealthy" {
				return false, fmt.Errorf("%s is unhealthy", ctr.Name)
			}
			return fresh.Health.Status == "healthy", nil
		case ServiceStarted, ServiceRunningOrHealthy:
			return fresh.State == "running", nil
		case ServiceCompletedSuccessfully:
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

	entries, err := r.repoReg.ListRepos(registry.ExcludeOrphaned())
	if err != nil {
		l.Error("check: failed to list repos from registry", "error", err)
		return
	}
	if len(entries) > 0 {
		r.checkRepoEntries(ctx, entries, l)
		return
	}

	containers, err := r.docker.ListBackupContainers(ctx)
	if err != nil {
		l.Error("check: failed to list containers", "error", err)
		return
	}

	seen := make(map[string]bool)
	for _, ctr := range containers {
		cfg := r.parseConfig(ctr.Labels)
		repos, err := r.repoReg.SyncContainer(&ctr, cfg)
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
				_ = r.repoReg.MarkCheckComplete(repo, false)
				r.notifier.SendBackupError("repository-check",
					fmt.Sprintf("Check failed for repo %s: %s", repo, err.Error()))
			} else {
				l.Info("check: repository ok", "repo", repo)
				_ = r.repoReg.MarkCheckComplete(repo, true)
			}
		}
	}
}

func (r *Runner) checkRepoEntries(ctx context.Context, entries []registry.RepoEntry, logger *slog.Logger) {
	seen := make(map[string]bool)
	for _, entry := range entries {
		if seen[entry.URL] {
			continue
		}
		seen[entry.URL] = true
		if err := r.restic.Check(ctx, entry.URL); err != nil {
			logger.Error("check: repository check failed", "repo", entry.URL, "error", err)
			_ = r.repoReg.MarkCheckComplete(entry.URL, false)
			r.notifier.SendBackupError("repository-check",
				fmt.Sprintf("Check failed for repo %s: %s", entry.URL, err.Error()))
		} else {
			logger.Info("check: repository ok", "repo", entry.URL)
			_ = r.repoReg.MarkCheckComplete(entry.URL, true)
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
