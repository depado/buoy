package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/moby/moby/api/types/container"

	"github.com/depado/buoy/internal/docker"
	"github.com/depado/buoy/internal/hook"
	"github.com/depado/buoy/internal/restic"
)

// Runner orchestrates the full backup lifecycle for a container:
// pre-hooks → stop → backup → post-hooks → start → retention.
// For containers in a compose stack, the entire stack is stopped, every
// enabled container's volumes are backed up, and the stack is restarted.
type Runner struct {
	docker *docker.Client
	restic *restic.Client
	hook   *hook.Executor
	base   string
	logger *slog.Logger
}

// New creates a new Runner.
func New(d *docker.Client, r *restic.Client, h *hook.Executor, baseRepo string, logger *slog.Logger) *Runner {
	return &Runner{
		docker: d,
		restic: r,
		hook:   h,
		base:   baseRepo,
		logger: logger,
	}
}

// Run executes the backup lifecycle for the given container.
// If the container belongs to a compose stack, all enabled containers in the
// stack are backed up together.
func (r *Runner) Run(ctx context.Context, ctr *docker.Container) error {
	l := r.logger.With("container", ctr.Name, "project", ctr.ComposeProject, "service", ctr.ComposeService)

	fresh, err := r.docker.InspectContainer(ctx, ctr.ID)
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}

	if fresh.State != "running" {
		l.Info("skipping backup, container not running", "state", fresh.State)
		return nil
	}

	if fresh.ComposeProject != "" {
		return r.runStack(ctx, fresh, l)
	}
	return r.runContainer(ctx, fresh, l)
}

func (r *Runner) runStack(ctx context.Context, trigger *docker.Container, l *slog.Logger) error {
	project := trigger.ComposeProject
	l.Info("backing up compose stack", "project", project)

	summaries, err := r.docker.ListContainersByProject(ctx, project)
	if err != nil {
		return fmt.Errorf("list stack containers: %w", err)
	}

	ctrs := make([]*docker.Container, 0, len(summaries))
	for i := range summaries {
		ctr, err := r.docker.InspectContainer(ctx, summaries[i].ID)
		if err != nil {
			l.Warn("failed to inspect stack container", "id", summaries[i].ID, "error", err)
			continue
		}
		cfg := docker.ParseBackupConfig(ctr.Labels)
		if !cfg.Enabled {
			continue
		}
		if !hasBackupMount(ctr) {
			continue
		}
		ctrs = append(ctrs, ctr)
	}

	for _, ctr := range ctrs {
		cfg := docker.ParseBackupConfig(ctr.Labels)
		r.runPreHooks(ctx, ctr, cfg, l)
	}

	running := make(map[string]bool)
	for _, ctr := range ctrs {
		cfg := docker.ParseBackupConfig(ctr.Labels)
		if !cfg.StopBefore {
			continue
		}
		cl := l.With("container", ctr.Name)
		if err := r.docker.StopContainer(ctx, ctr.ID, cfg.StopTimeout); err != nil {
			cl.Warn("failed to stop container", "error", err)
			continue
		}
		if err := r.docker.ContainerWait(ctx, ctr.ID, container.WaitConditionNotRunning); err != nil {
			cl.Warn("failed to wait for container stop", "error", err)
		}
		running[ctr.ID] = true
	}

	for _, ctr := range ctrs {
		r.backupMounts(ctx, ctr, l)
	}

	for _, ctr := range ctrs {
		if !running[ctr.ID] {
			continue
		}
		if err := r.docker.StartContainer(ctx, ctr.ID); err != nil {
			l.With("container", ctr.Name).Warn("failed to start container", "error", err)
		}
	}

	for _, ctr := range ctrs {
		r.runPostHooks(ctx, ctr, docker.ParseBackupConfig(ctr.Labels), l)
	}

	for _, ctr := range ctrs {
		r.applyRetention(ctx, ctr, docker.ParseBackupConfig(ctr.Labels), l)
	}

	return nil
}

func (r *Runner) runContainer(ctx context.Context, ctr *docker.Container, l *slog.Logger) error {
	cfg := docker.ParseBackupConfig(ctr.Labels)

	repo := cfg.RepoOverride
	if repo == "" {
		repo = ctr.RepoPath(r.base)
	}

	if err := r.restic.Unlock(ctx, repo); err != nil {
		l.Warn("failed to unlock repo", "error", err)
	}

	r.runPreHooks(ctx, ctr, cfg, l)

	wasRunning := false
	if cfg.StopBefore {
		if err := r.docker.StopContainer(ctx, ctr.ID, cfg.StopTimeout); err != nil {
			return fmt.Errorf("stop container: %w", err)
		}
		if err := r.docker.ContainerWait(ctx, ctr.ID, container.WaitConditionNotRunning); err != nil {
			return fmt.Errorf("wait for stop: %w", err)
		}
		wasRunning = true
	}

	r.backupMounts(ctx, ctr, l)

	if wasRunning {
		if err := r.docker.StartContainer(ctx, ctr.ID); err != nil {
			return fmt.Errorf("start container: %w", err)
		}
	}

	r.runPostHooks(ctx, ctr, cfg, l)

	r.applyRetention(ctx, ctr, cfg, l)

	return nil
}

func (r *Runner) runPreHooks(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) {
	if cfg.PreBackupCmd != "" {
		if err := r.hook.ExecOnHost(ctx, cfg.PreBackupCmd); err != nil {
			l.Warn("pre-backup host command failed", "container", ctr.Name, "error", err)
		}
	}
	if cfg.PreBackupExec != "" {
		if err := r.hook.ExecInContainer(ctx, ctr.ID, cfg.PreBackupExec); err != nil {
			l.Warn("pre-backup exec failed", "container", ctr.Name, "error", err)
		}
	}
}

func (r *Runner) runPostHooks(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) {
	if cfg.PostBackupExec != "" {
		if err := r.hook.ExecInContainer(ctx, ctr.ID, cfg.PostBackupExec); err != nil {
			l.Warn("post-backup exec failed", "container", ctr.Name, "error", err)
		}
	}
	if cfg.PostBackupCmd != "" {
		if err := r.hook.ExecOnHost(ctx, cfg.PostBackupCmd); err != nil {
			l.Warn("post-backup host command failed", "container", ctr.Name, "error", err)
		}
	}
}

func (r *Runner) backupMounts(ctx context.Context, ctr *docker.Container, l *slog.Logger) {
	cfg := docker.ParseBackupConfig(ctr.Labels)

	repo := cfg.RepoOverride
	if repo == "" {
		repo = ctr.RepoPath(r.base)
	}

	if err := r.restic.Unlock(ctx, repo); err != nil {
		l.Warn("failed to unlock repo", "container", ctr.Name, "error", err)
	}

	for _, m := range ctr.Mounts {
		if m.Type == "tmpfs" {
			continue
		}
		if contains(cfg.ExcludeVolumes, m.Name) {
			continue
		}

		source := m.Source
		if _, err := os.Stat(source); os.IsNotExist(err) {
			l.Warn("mount source does not exist, skipping", "container", ctr.Name, "source", source, "type", m.Type)
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

		result, err := r.restic.Backup(ctx, repo, []string{"."}, opts)
		if err != nil {
			l.Error("backup failed", "container", ctr.Name, "mount", source, "error", err)
			continue
		}
		l.Info("backup complete",
			"container", ctr.Name,
			"mount", source,
			"snapshot", result.SnapshotID,
			"duration", result.TotalDuration,
			"added_bytes", result.DataAdded,
		)
	}
}

func (r *Runner) applyRetention(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) {
	repo := cfg.RepoOverride
	if repo == "" {
		repo = ctr.RepoPath(r.base)
	}

	if err := r.restic.Forget(ctx, repo, restic.RetentionPolicy{
		KeepDaily:   cfg.Retention.KeepDaily,
		KeepWeekly:  cfg.Retention.KeepWeekly,
		KeepMonthly: cfg.Retention.KeepMonthly,
		KeepYearly:  cfg.Retention.KeepYearly,
		KeepWithin:  cfg.Retention.KeepWithin,
	}); err != nil {
		l.Warn("forget failed", "container", ctr.Name, "error", err)
	}
	if err := r.restic.Prune(ctx, repo); err != nil {
		l.Warn("prune failed", "container", ctr.Name, "error", err)
	}
}

func hasBackupMount(ctr *docker.Container) bool {
	for _, m := range ctr.Mounts {
		if m.Type != "tmpfs" {
			return true
		}
	}
	return false
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
