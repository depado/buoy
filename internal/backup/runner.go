package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/depado/buoy/internal/docker"
	"github.com/depado/buoy/internal/hook"
	"github.com/depado/buoy/internal/restic"
)

type Runner struct {
	docker           *docker.Client
	restic           *restic.Client
	hook             *hook.Executor
	base             string
	defaultSchedule  string
	defaultRetention string
	ignoredIDs       *sync.Map
	logger           *slog.Logger
}

func New(d *docker.Client, r *restic.Client, h *hook.Executor, baseRepo, defaultSchedule, defaultRetention string, ignoredIDs *sync.Map, logger *slog.Logger) *Runner {
	return &Runner{
		docker:           d,
		restic:           r,
		hook:             h,
		base:             baseRepo,
		defaultSchedule:  defaultSchedule,
		defaultRetention: defaultRetention,
		ignoredIDs:       ignoredIDs,
		logger:           logger,
	}
}

func (r *Runner) parseConfig(labels map[string]string) docker.BackupConfig {
	return docker.ParseBackupConfig(labels, r.defaultSchedule, r.defaultRetention)
}

func (r *Runner) resolveRepo(ctr *docker.Container, cfg docker.BackupConfig) (string, error) {
	repo := cfg.RepoOverride
	if repo == "" {
		repo = ctr.RepoPath(r.base)
	}
	return filepath.Abs(repo)
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

	repo, err := r.resolveRepo(fresh, cfg)
	if err != nil {
		return fmt.Errorf("repo path: %w", err)
	}

	if err := r.restic.Unlock(ctx, repo); err != nil {
		l.Warn("failed to unlock repo", "error", err)
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
	for i := range summaries {
		ctr, err := r.docker.InspectContainer(ctx, summaries[i].ID)
		if err != nil {
			l.Warn("failed to inspect stack container", "id", summaries[i].ID, "error", err)
			continue
		}
		all = append(all, ctr)
	}

	fresh := make([]*docker.Container, 0, len(batch))
	for _, ctr := range batch {
		inspected, err := r.docker.InspectContainer(ctx, ctr.ID)
		if err != nil {
			l.Warn("failed to inspect batch container", "id", ctr.ID, "error", err)
			continue
		}
		fresh = append(fresh, inspected)
	}

	fresh = deduplicateByService(fresh)

	for _, ctr := range fresh {
		cfg := r.parseConfig(ctr.Labels)
		r.runPreHooks(ctx, ctr, cfg, l)
	}

	stopSvc := stopSet(fresh, all)
	l.Info("starting stack backup", "services", len(fresh), "stop_set", mapKeys(stopSvc))
	services := serviceContainers(all)
	stopOrder := orderForStop(all, func(svc string) {
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

	startOrder := orderForStart(all, func(svc string) {
		l.Warn("dependency cycle detected", "service", svc, "project", project)
	})
	for _, svc := range startOrder {
		if err := r.waitForDeps(ctx, all, svc, l); err != nil {
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
	return nil
}

func (r *Runner) runPreHooks(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) {
	if cfg.PreBackupCmd != "" {
		if err := r.hook.ExecOnHost(ctx, cfg.PreBackupCmd); err != nil {
			l.Warn("pre-backup host command failed", "error", err)
		}
	}
	if cfg.PreBackupExec != "" {
		if err := r.hook.ExecInContainer(ctx, ctr.ID, cfg.PreBackupExec); err != nil {
			l.Warn("pre-backup exec failed", "error", err)
		}
	}
}

func (r *Runner) runPostHooks(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) {
	if cfg.PostBackupExec != "" {
		if err := r.hook.ExecInContainer(ctx, ctr.ID, cfg.PostBackupExec); err != nil {
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

	repo, err := r.resolveRepo(ctr, cfg)
	if err != nil {
		l.Warn("failed to resolve repo path, skipping backup", "repo", repo, "error", err)
		return
	}

	exists, err := r.restic.RepoExists(ctx, repo)
	if err != nil {
		l.Warn("failed to check repo, skipping backup", "repo", repo, "error", err)
		return
	}
	if !exists {
		l.Debug("repo not found, initializing", "repo", repo)
		if err := r.restic.Init(ctx, repo); err != nil {
			l.Warn("failed to init repo, skipping backup", "repo", repo, "error", err)
			return
		}
		l.Info("initialized repo", "repo", repo)
	} else {
		l.Debug("repo already exists", "repo", repo)
	}

	if err := r.restic.Unlock(ctx, repo); err != nil {
		l.Warn("failed to unlock repo", "error", err)
	}

	if len(cfg.IncludeVolumes) > 0 && len(cfg.ExcludeVolumes) > 0 {
		l.Warn("both include-volumes and exclude-volumes set, exclude-volumes ignored")
	}
	if len(cfg.IncludeMounts) > 0 && len(cfg.ExcludeMounts) > 0 {
		l.Warn("both include-mounts and exclude-mounts set, exclude-mounts ignored")
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
			l.Warn("mount source does not exist, skipping", "source", source, "type", m.Type)
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
			l.Error("backup failed", "mount", source, "error", err)
			continue
		}
		if result == nil {
			l.Error("backup produced no summary", "mount", source)
			continue
		}
		l.Info("backup complete",
			"mount", source,
			"snapshot", result.SnapshotID,
			slog.Duration("duration", time.Duration(result.TotalDuration*float64(time.Second))),
		)
	}
}

func (r *Runner) applyRetention(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) {
	repo, err := r.resolveRepo(ctr, cfg)
	if err != nil {
		l.Warn("failed to resolve repo path, skipping retention", "repo", repo, "error", err)
		return
	}

	if err := r.restic.Forget(ctx, repo, restic.RetentionPolicy{
		KeepDaily:   cfg.Retention.KeepDaily,
		KeepWeekly:  cfg.Retention.KeepWeekly,
		KeepMonthly: cfg.Retention.KeepMonthly,
		KeepYearly:  cfg.Retention.KeepYearly,
		KeepWithin:  cfg.Retention.KeepWithin,
	}); err != nil {
		l.Warn("forget failed", "error", err)
	}
	if err := r.restic.Prune(ctx, repo); err != nil {
		l.Warn("prune failed", "error", err)
	}
}

func (r *Runner) waitForDeps(ctx context.Context, ctrs []*docker.Container, serviceName string, l *slog.Logger) error {
	deps := depConditions(ctrs, serviceName)
	svcMap := serviceContainers(ctrs)

	for _, dep := range deps {
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

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
