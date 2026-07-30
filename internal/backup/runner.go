package backup

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

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
		meters:            cfg.Meters,
		tracers:           cfg.Tracers,
	}
}

func (r *Runner) parseConfig(labels map[string]string) docker.BackupConfig {
	return docker.ParseBackupConfig(labels, r.defaultSchedule, r.defaultRetention)
}

func (r *Runner) Run(ctx context.Context, ctr *docker.Container) (runErr error) {
	ctx, span := r.tracers.Tracer.Start(ctx, "buoy.backup",
		trace.WithAttributes(containerAttrs(ctr)...),
	)
	defer func() {
		if runErr != nil {
			span.RecordError(runErr)
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

	eligibleMounts := countEligibleMounts(fresh, cfg)

	r.runPreHooks(ctx, fresh, cfg, l)

	l.Info("backup started", "stop", cfg.StopBefore, "mounts", len(fresh.Mounts), "repos", len(repos))
	l.Debug("pre-hooks completed, proceeding with backup")
	wasRunning := false
	if cfg.StopBefore {
		r.ignore(fresh.ID)
		defer r.release(fresh.ID)
		var err error
		wasRunning, err = r.stopContainer(ctx, fresh, cfg, l)
		if err != nil {
			r.meters.BackupsTotal.Add(ctx, 1,
				metric.WithAttributes(containerAttrs(fresh,
					attribute.Int("mounts", eligibleMounts),
					attribute.Bool("success", false),
				)...),
			)
			return err
		}
	}

	l.Debug("backing up mounts", "repos", len(repos))
	backupErr := r.backupMounts(ctx, fresh, cfg, repos, l)

	r.meters.BackupsTotal.Add(ctx, 1,
		metric.WithAttributes(containerAttrs(fresh,
			attribute.Int("mounts", eligibleMounts),
			attribute.Bool("success", backupErr == nil),
		)...),
	)

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

	l.Info("backup completed")
	r.notifier.SendInfo(
		fmt.Sprintf("buoy backup complete: %s", ctr.Name),
		fmt.Sprintf("Backup completed for container %s", ctr.Name),
	)
	return nil
}

func (r *Runner) effectivePassword(cfg docker.BackupConfig, repoName string) string {
	if cfg.Password != "" {
		return cfg.Password
	}
	return r.resticConf.PasswordFor(repoName)
}

func (r *Runner) ignore(id string)  { r.ignoredIDs.Store(id, true) }
func (r *Runner) release(id string) { r.ignoredIDs.Delete(id) }

func countEligibleMounts(ctr *docker.Container, cfg docker.BackupConfig) int {
	n := 0
	for _, m := range ctr.Mounts {
		if m.Type == "tmpfs" {
			continue
		}
		if _, ok := docker.MountMatches(m, cfg.Include, cfg.Exclude); ok {
			n++
		}
	}
	return n
}

func containerAttrs(ctr *docker.Container, extra ...attribute.KeyValue) []attribute.KeyValue {
	base := []attribute.KeyValue{
		attribute.String("container", ctr.Name),
		attribute.String("service", ctr.ComposeService),
		attribute.String("project", ctr.ComposeProject),
	}
	return append(base, extra...)
}

type mountError struct {
	mount string
	repo  string
	err   error
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
