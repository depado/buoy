package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/depado/buoy/internal/config"
	"github.com/depado/buoy/internal/docker"
	"github.com/depado/buoy/internal/hook"
	"github.com/depado/buoy/internal/notify"
	"github.com/depado/buoy/internal/registry"
	"github.com/depado/buoy/internal/restic"
	"github.com/depado/buoy/internal/telemetry"
)

type Runner struct {
	docker            *docker.Client
	restic            *restic.Client
	hook              *hook.Executor
	repoReg           *registry.Registry
	resticConf        *config.ResticConf
	defaultSchedule   string
	defaultRetention  string
	ignoredIDs        *sync.Map
	notifier          *notify.Notifier
	logger            *slog.Logger
	execTimeout       time.Duration
	healthWaitTimeout time.Duration
	backupTimeout     time.Duration
	meters            telemetry.MeterSet
	tracers           telemetry.TracerSet
}

type RunnerConfig struct {
	Docker            *docker.Client
	Restic            *restic.Client
	Hook              *hook.Executor
	Registry          *registry.Registry
	ResticConf        *config.ResticConf
	DefaultSchedule   string
	DefaultRetention  string
	IgnoredIDs        *sync.Map
	Notifier          *notify.Notifier
	Logger            *slog.Logger
	ExecTimeout       time.Duration
	HealthWaitTimeout time.Duration
	BackupTimeout     time.Duration
	Meters            telemetry.MeterSet
	Tracers           telemetry.TracerSet
}

func New(cfg *RunnerConfig) *Runner {
	return &Runner{
		docker:            cfg.Docker,
		restic:            cfg.Restic,
		hook:              cfg.Hook,
		repoReg:           cfg.Registry,
		resticConf:        cfg.ResticConf,
		defaultSchedule:   cfg.DefaultSchedule,
		defaultRetention:  cfg.DefaultRetention,
		ignoredIDs:        cfg.IgnoredIDs,
		notifier:          cfg.Notifier,
		logger:            cfg.Logger,
		execTimeout:       cfg.ExecTimeout,
		healthWaitTimeout: cfg.HealthWaitTimeout,
		backupTimeout:     cfg.BackupTimeout,
		meters:            cfg.Meters,
		tracers:           cfg.Tracers,
	}
}

func (r *Runner) parseConfig(labels map[string]string) docker.BackupConfig {
	return docker.ParseBackupConfig(labels, r.defaultSchedule, r.defaultRetention)
}

func (r *Runner) Run(ctx context.Context, ctr *docker.Container) (runErr error) {
	ctx, span := r.tracers.Tracer.Start(ctx, "buoy.backup",
		trace.WithAttributes(
			attribute.String("container.name", ctr.Name),
			attribute.String("container.id", ctr.ID),
		),
	)
	defer func() {
		if runErr != nil {
			span.SetStatus(codes.Error, runErr.Error())
		}
		span.End()
	}()

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

	l.Info("backup started", "stop", cfg.StopBefore, "mounts", len(fresh.Mounts), "repos", len(repos))
	l.Debug("pre-hooks complete, proceeding with backup")
	wasRunning := false
	if cfg.StopBefore {
		r.ignore(fresh.ID)
		defer r.release(fresh.ID)
		var err error
		wasRunning, err = r.stopContainer(ctx, fresh, cfg, l)
		if err != nil {
			return err
		}
	}

	l.Debug("backing up mounts", "repos", len(repos))
	backupErr := r.backupMounts(ctx, fresh, cfg, repos, l)

	if wasRunning {
		r.startContainer(ctx, fresh, l)
	}

	l.Debug("running post-backup hooks")
	r.runPostHooks(ctx, fresh, cfg, l)

	var issues []string
	if backupErr != nil {
		issues = append(issues, fmt.Sprintf("Backup: %s", backupErr.Error()))
	} else {
		issues = append(issues, r.applyRetention(ctx, fresh, cfg, repos, l)...)
	}

	if len(issues) > 0 {
		l.Warn("backup completed with failures", "issues", len(issues))
		msg := strings.Join(issues, "\n")
		r.notifier.SendBackupError(ctr.Name, msg)
		if backupErr != nil {
			return backupErr
		}
		return nil
	}

	l.Info("backup complete")
	r.notifier.SendInfo(
		fmt.Sprintf("buoy backup complete: %s", ctr.Name),
		fmt.Sprintf("Backup completed for container %s", ctr.Name),
	)
	return nil
}

func (r *Runner) stopContainer(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) (stopped bool, stopErr error) {
	stopStart := time.Now()
	ctx, span := r.tracers.Tracer.Start(ctx, "buoy.container.stop",
		trace.WithAttributes(
			attribute.String("container.name", ctr.Name),
		),
	)
	defer func() {
		d := time.Since(stopStart).Seconds()
		result := "ok"
		if stopErr != nil {
			span.SetStatus(codes.Error, stopErr.Error())
			result = "timeout"
		}
		r.meters.ContainerStopDur.Record(ctx, d,
			metric.WithAttributes(attribute.String("result", result)),
		)
		span.End()
	}()

	l.Debug("stopping container")
	if err := r.docker.StopContainer(ctx, ctr.ID, cfg.StopTimeout); err != nil {
		return false, fmt.Errorf("stop container: %w", err)
	}
	l.Debug("waiting for container to stop", "timeout", cfg.StopTimeout)
	if err := r.docker.ContainerWait(ctx, ctr.ID, container.WaitConditionNotRunning); err != nil {
		l.Warn("container did not stop in time, aborting backup", "error", err)
		if startErr := r.docker.StartContainer(ctx, ctr.ID); startErr != nil {
			l.Error("failed to restart container after failed stop", "error", startErr)
		}
		return false, fmt.Errorf("wait for stop: %w", err)
	}
	l.Info("container stopped")
	return true, nil
}

func (r *Runner) startContainer(ctx context.Context, ctr *docker.Container, l *slog.Logger) {
	startTime := time.Now()
	result := "ok"
	ctx, span := r.tracers.Tracer.Start(ctx, "buoy.container.start",
		trace.WithAttributes(
			attribute.String("container.name", ctr.Name),
		),
	)
	defer func() {
		r.meters.ContainerStartDur.Record(ctx, time.Since(startTime).Seconds(),
			metric.WithAttributes(attribute.String("result", result)),
		)
		span.End()
	}()

	l.Debug("starting container")
	if err := r.docker.StartContainer(ctx, ctr.ID); err != nil {
		l.Error("start container failed", "error", err)
		span.SetStatus(codes.Error, err.Error())
		result = "fail"
		return
	}
	l.Info("container started")
	r.waitRunning(ctx, ctr, l)
}

func (r *Runner) runPreHooks(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) {
	if cfg.HookPreCmd != "" {
		ctx, span := r.tracers.Tracer.Start(ctx, "buoy.hook.pre.host")
		start := time.Now()
		status := "ok"
		l.Info("running pre-backup host command")
		if err := r.hook.ExecOnHost(ctx, cfg.HookPreCmd); err != nil {
			l.Warn("pre-backup host command failed", "error", err)
			status = "fail"
			span.SetStatus(codes.Error, err.Error())
		} else {
			l.Info("pre-backup host command completed")
		}
		r.meters.HookDuration.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(
				attribute.String("type", "pre"),
				attribute.String("target", "host"),
				attribute.String("status", status),
			),
		)
		span.End()
	}
	if cfg.HookPreExec != "" {
		execCtx, cancel := context.WithTimeout(ctx, r.execTimeout)
		defer cancel()
		ctx, span := r.tracers.Tracer.Start(execCtx, "buoy.hook.pre.exec")
		start := time.Now()
		status := "ok"
		l.Info("running pre-backup exec command")
		if err := r.hook.ExecInContainer(ctx, ctr.ID, cfg.HookPreExec); err != nil {
			l.Warn("pre-backup exec failed", "error", err)
			status = "fail"
			span.SetStatus(codes.Error, err.Error())
		} else {
			l.Info("pre-backup exec command completed")
		}
		r.meters.HookDuration.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(
				attribute.String("type", "pre"),
				attribute.String("target", "container"),
				attribute.String("status", status),
			),
		)
		span.End()
	}
}

func (r *Runner) runPostHooks(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) {
	if cfg.HookPostExec != "" {
		execCtx, cancel := context.WithTimeout(ctx, r.execTimeout)
		defer cancel()
		ctx, span := r.tracers.Tracer.Start(execCtx, "buoy.hook.post.exec")
		start := time.Now()
		status := "ok"
		l.Info("running post-backup exec command")
		if err := r.hook.ExecInContainer(ctx, ctr.ID, cfg.HookPostExec); err != nil {
			l.Warn("post-backup exec failed", "error", err)
			status = "fail"
			span.SetStatus(codes.Error, err.Error())
		} else {
			l.Info("post-backup exec command completed")
		}
		r.meters.HookDuration.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(
				attribute.String("type", "post"),
				attribute.String("target", "container"),
				attribute.String("status", status),
			),
		)
		span.End()
	}
	if cfg.HookPostCmd != "" {
		ctx, span := r.tracers.Tracer.Start(ctx, "buoy.hook.post.host")
		start := time.Now()
		status := "ok"
		l.Info("running post-backup host command")
		if err := r.hook.ExecOnHost(ctx, cfg.HookPostCmd); err != nil {
			l.Warn("post-backup host command failed", "error", err)
			status = "fail"
			span.SetStatus(codes.Error, err.Error())
		} else {
			l.Info("post-backup host command completed")
		}
		r.meters.HookDuration.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(
				attribute.String("type", "post"),
				attribute.String("target", "host"),
				attribute.String("status", status),
			),
		)
		span.End()
	}
}

type mountError struct {
	mount string
	repo  string
	err   error
}

func (r *Runner) backupMounts(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, repos []registry.RepoRef, logger *slog.Logger) error {
	mountCount := 0
	var failures []mountError

	for _, ref := range repos {
		ctx := restic.WithPassword(ctx, r.effectivePassword(cfg, ref.Name))

		if !r.ensureRepo(ctx, ref.URL, logger, &failures) {
			continue
		}

		l := logger.With("repo", ref.URL)
		repoOK := true
		repoMounts := 0

		for _, m := range ctr.Mounts {
			if m.Type == "tmpfs" {
				continue
			}

			matchedName, ok := docker.MountMatches(m, cfg.Include, cfg.Exclude)
			if !ok {
				continue
			}

			mountCount++
			repoMounts++
			if repoErr := r.backupSingleMount(ctx, ref.URL, ctr.Name, m, matchedName, cfg, l); repoErr != nil {
				failures = append(failures, *repoErr)
				repoOK = false
			}
		}
		if err := r.repoReg.MarkBackupComplete(ref.URL, repoOK); err != nil {
			logger.Warn("failed to persist backup status", "repo", ref.URL, "error", err)
		}
		r.meters.BackupMountCount.Record(ctx, int64(repoMounts),
			metric.WithAttributes(attribute.String("repo", ref.URL)),
		)
	}

	if mountCount == 0 {
		logger.Warn("container has no backup-eligible mounts", "mounts", len(ctr.Mounts), "include", len(cfg.Include), "exclude", len(cfg.Exclude))
	}

	return summarizeFailures(failures, mountCount, len(repos))
}

func (r *Runner) effectivePassword(cfg docker.BackupConfig, repoName string) string {
	if cfg.Password != "" {
		return cfg.Password
	}
	return r.resticConf.PasswordFor(repoName)
}

func (r *Runner) ensureRepo(ctx context.Context, repo string, logger *slog.Logger, failures *[]mountError) bool {
	l := logger.With("repo", repo)

	l.Debug("checking repo")
	exists, err := r.restic.RepoExists(ctx, repo)
	if err != nil {
		l.Warn("failed to check repo, skipping repo", "error", err)
		*failures = append(*failures, mountError{repo: repo, err: fmt.Errorf("repo check: %w", err)})
		if err := r.repoReg.MarkBackupComplete(repo, false); err != nil {
			l.Warn("failed to persist backup status", "error", err)
		}
		return false
	}
	if !exists {
		l.Debug("repo not found, initializing")
		if err := r.restic.Init(ctx, repo); err != nil {
			l.Warn("failed to init repo, skipping repo", "error", err)
			*failures = append(*failures, mountError{repo: repo, err: fmt.Errorf("repo init: %w", err)})
			if err := r.repoReg.MarkBackupComplete(repo, false); err != nil {
				l.Warn("failed to persist backup status", "error", err)
			}
			return false
		}
		l.Info("initialized repo")
	}

	if err := r.restic.Unlock(ctx, repo); err != nil {
		l.Warn("failed to unlock repo", "error", err)
	}
	return true
}

func (r *Runner) backupSingleMount(
	ctx context.Context,
	repo string,
	hostname string,
	m docker.Mount,
	matchedName string,
	cfg docker.BackupConfig,
	l *slog.Logger,
) (mountErr *mountError) {
	source := m.Source
	if _, err := os.Stat(source); os.IsNotExist(err) {
		l.Warn("mount source does not exist, skipping", "source", source, "type", m.Type)
		return &mountError{mount: source, repo: repo, err: fmt.Errorf("mount source not found")}
	}

	mountTag := m.Name
	if mountTag == "" {
		mountTag = m.Destination
	}

	files, excludes, tags := cfg.ResolveMountBackup(matchedName)

	opts := restic.BackupOptions{
		Tags:     append(tags, "mount:"+mountTag),
		Excludes: excludes,
		Files:    files,
		Hostname: hostname,
		WorkDir:  source,
	}

	paths := []string{"."}
	if len(files) == 0 {
		entries, err := os.ReadDir(source)
		if err != nil {
			l.Warn("failed to read source directory, skipping", "source", source, "error", err)
			return &mountError{mount: source, repo: repo, err: fmt.Errorf("read dir: %w", err)}
		}
		if len(entries) == 0 {
			l.Debug("source directory is empty, skipping", "source", source)
			return nil
		}
		paths = make([]string, 0, len(entries))
		for _, e := range entries {
			paths = append(paths, e.Name())
		}
	}

	start := time.Now()

	ctx, span := r.tracers.Tracer.Start(ctx, "buoy.restic.backup",
		trace.WithAttributes(
			attribute.String("repo", repo),
			attribute.String("mount.source", source),
			attribute.String("mount.type", m.Type),
		),
	)
	defer span.End()

	result, err := r.restic.Backup(ctx, repo, paths, opts)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
	} else if result != nil {
		span.SetAttributes(attribute.String("snapshot.id", result.SnapshotID))
	}

	status := "success"
	if err != nil {
		status = "fail"
	}
	attrs := []attribute.KeyValue{
		attribute.String("repo", repo),
		attribute.String("mount", source),
		attribute.String("status", status),
	}
	r.meters.BackupDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attrs...))
	r.meters.BackupsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("repo", repo),
		attribute.String("status", status),
	))

	if err != nil {
		if result != nil {
			l.Error("backup completed with errors",
				"repo", repo, "mount", source,
				"snapshot", result.SnapshotID, "error", err,
			)
		} else {
			l.Error("backup failed", "repo", repo, "mount", source, "error", err)
		}
		return &mountError{mount: source, repo: repo, err: err}
	}
	if result == nil {
		l.Error("backup produced no summary", "repo", repo, "mount", source)
		return &mountError{mount: source, repo: repo, err: fmt.Errorf("no summary")}
	}
	l.Info("backup complete",
		"repo", repo, "mount", source,
		"snapshot", result.SnapshotID,
		slog.Duration("duration", time.Duration(result.TotalDuration*float64(time.Second))),
	)
	return nil
}

func summarizeFailures(failures []mountError, mountCount, repoCount int) error {
	if len(failures) == 0 {
		return nil
	}
	parts := make([]string, 0, len(failures))
	for _, f := range failures {
		msg := strings.TrimPrefix(f.err.Error(), "restic backup: ")
		if f.mount != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", f.mount, msg))
		} else {
			parts = append(parts, fmt.Sprintf("%s: %s", f.repo, msg))
		}
	}
	joined := strings.Join(parts, "; ")
	if mountCount > 0 && len(failures) == mountCount*repoCount {
		return fmt.Errorf("all %d mounts failed: %s", mountCount, joined)
	}
	return fmt.Errorf("%d failure(s): %s", len(failures), joined)
}

func (r *Runner) applyRetention(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, repos []registry.RepoRef, logger *slog.Logger) []string {
	logger.Debug("applying retention", "policy", cfg.Retention, "repos", len(repos))
	policy := cfg.Retention

	var issues []string
	for _, ref := range repos {
		ctx := restic.WithPassword(ctx, r.effectivePassword(cfg, ref.Name))

		l := logger.With("repo", ref.URL)

		start := time.Now()
		if err := r.restic.Forget(ctx, ref.URL, policy, ctr.Name); err != nil {
			l.Warn("forget failed", "error", err)
			issues = append(issues, fmt.Sprintf("forget on %s: %s", ref.URL, err.Error()))
		}
		if err := r.restic.Prune(ctx, ref.URL); err != nil {
			l.Warn("prune failed", "error", err)
			issues = append(issues, fmt.Sprintf("prune on %s: %s", ref.URL, err.Error()))
		}
		l.Info("retention complete", slog.Duration("duration", time.Since(start)))
		r.meters.RetentionDuration.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(attribute.String("repo", ref.URL)),
		)
	}
	return issues
}

func (r *Runner) waitRunning(ctx context.Context, ctr *docker.Container, l *slog.Logger) {
	l.Debug("waiting for container to reach running state", "timeout", r.healthWaitTimeout)
	ctx, cancel := context.WithTimeout(ctx, r.healthWaitTimeout)
	defer cancel()

	_ = r.waitForEvent(ctx, ctr,
		[]string{"start", "die"},
		func(c *docker.Container) (bool, error) {
			return c.State == "running" || c.State == "exited", nil
		})
}

func (r *Runner) ignore(id string)  { r.ignoredIDs.Store(id, true) }
func (r *Runner) release(id string) { r.ignoredIDs.Delete(id) }
