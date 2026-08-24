package backup

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/depado/buoy/internal/config"
	"github.com/depado/buoy/internal/docker"
	"github.com/depado/buoy/internal/notify"
	"github.com/depado/buoy/internal/registry"
	"github.com/depado/buoy/internal/restic"
	"github.com/depado/buoy/internal/telemetry"
	"github.com/depado/buoy/internal/types"
)

type Runner struct {
	docker            *docker.Client
	restic            *restic.Client
	repoReg           *registry.Registry
	resticConf        *config.ResticConf
	defaultSchedule   string
	defaultRetention  string
	ignoredIDs        *sync.Map
	notifier          *notify.Notifier
	logger            *slog.Logger
	execTimeout       time.Duration
	healthWaitTimeout time.Duration
	repoTimeout       time.Duration
	meters            telemetry.MeterSet
	tracer            trace.Tracer
}

type RunnerConfig struct {
	Docker            *docker.Client
	Restic            *restic.Client
	Registry          *registry.Registry
	ResticConf        *config.ResticConf
	DefaultSchedule   string
	DefaultRetention  string
	IgnoredIDs        *sync.Map
	Notifier          *notify.Notifier
	Logger            *slog.Logger
	ExecTimeout       time.Duration
	HealthWaitTimeout time.Duration
	RepoTimeout       time.Duration
	Meters            telemetry.MeterSet
	Tracer            trace.Tracer
}

func New(cfg *RunnerConfig) *Runner {
	return &Runner{
		docker:            cfg.Docker,
		restic:            cfg.Restic,
		repoReg:           cfg.Registry,
		resticConf:        cfg.ResticConf,
		defaultSchedule:   cfg.DefaultSchedule,
		defaultRetention:  cfg.DefaultRetention,
		ignoredIDs:        cfg.IgnoredIDs,
		notifier:          cfg.Notifier,
		logger:            cfg.Logger,
		execTimeout:       cfg.ExecTimeout,
		healthWaitTimeout: cfg.HealthWaitTimeout,
		repoTimeout:       cfg.RepoTimeout,
		meters:            cfg.Meters,
		tracer:            cfg.Tracer,
	}
}

func (r *Runner) parseConfig(labels map[string]string) types.BackupConfig {
	return types.ParseBackupConfig(labels, r.defaultSchedule, r.defaultRetention)
}

func (r *Runner) Run(ctx context.Context, ctr *types.Container) (runErr error) {
	ctx, span := r.tracer.Start(ctx, "buoy.backup",
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
	warnUnusedMountFilters(l, fresh, cfg)

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
	if d, ok := ctx.Deadline(); ok {
		l.Info("backup budget", "deadline", d, "remaining", time.Until(d).Round(time.Second))
	}
	l.Debug("pre-hooks completed, proceeding with backup")
	wasRunning := false
	if cfg.StopBefore {
		r.ignore(fresh.ID)
		defer r.release(fresh.ID)
		var err error
		wasRunning, err = r.stopContainer(ctx, fresh, cfg, l)
		if err != nil {
			r.meters.ContainerBackups.Add(ctx, 1,
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

	r.meters.ContainerBackups.Add(ctx, 1,
		metric.WithAttributes(containerAttrs(fresh,
			attribute.Int("mounts", eligibleMounts),
			attribute.Bool("success", backupErr == nil),
		)...),
	)

	if wasRunning {
		r.startContainer(ctx, fresh, cfg, l)
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

// minRemainingBudget is the smallest remaining window before a new unit of
// work (repo prune/check, hook) is started; anything less is guaranteed to
// fail instantly, so it is skipped instead.
const minRemainingBudget = 10 * time.Second

func (r *Runner) effectivePassword(cfg types.BackupConfig, repoName string) string {
	if cfg.Password != "" {
		return cfg.Password
	}
	return r.resticConf.PasswordFor(repoName)
}

func (r *Runner) effectiveRepoTimeout(cfg types.BackupConfig, repoName string) time.Duration {
	if t, ok := cfg.RepoTimeouts[repoName]; ok && t > 0 {
		return t
	}
	if cfg.RepoTimeout > 0 {
		return cfg.RepoTimeout
	}
	if repo, ok := r.resticConf.Repos[repoName]; ok && repo.Timeout != "" {
		if d, err := time.ParseDuration(repo.Timeout); err == nil && d > 0 {
			return d
		}
	}
	return r.repoTimeout
}

// maintenanceRepoTimeout resolves the per-repo budget for prune/check runs:
// repo config timeout → daemon repo_timeout (0 = unbounded). Container
// labels don't apply here, since maintenance is repo-centric, not
// container-centric.
func (r *Runner) maintenanceRepoTimeout(repoName string) time.Duration {
	if repo, ok := r.resticConf.Repos[repoName]; ok && repo.Timeout != "" {
		if d, err := time.ParseDuration(repo.Timeout); err == nil && d > 0 {
			return d
		}
	}
	return r.repoTimeout
}

// maintenanceRepoCtx bounds one prune/check repo call by its per-repo budget,
// inherited from the maintenance window (the shorter wins). Returns an error
// when the window is too far gone to start another repo.
func (r *Runner) maintenanceRepoCtx(ctx context.Context, repoName string) (context.Context, context.CancelFunc, error) {
	if d, ok := ctx.Deadline(); ok && time.Until(d) < minRemainingBudget {
		return nil, nil, fmt.Errorf("maintenance window nearly exhausted (%s left)", time.Until(d).Round(time.Second))
	}
	budget := r.maintenanceRepoTimeout(repoName)
	if budget <= 0 {
		return ctx, func() {}, nil
	}
	repoCtx, cancel := context.WithTimeout(ctx, budget)
	return repoCtx, cancel, nil
}

func (r *Runner) effectiveHealthWaitTimeout(cfg types.BackupConfig) time.Duration {
	if cfg.HealthWaitTimeout > 0 {
		return cfg.HealthWaitTimeout
	}
	return r.healthWaitTimeout
}

// warnUnusedMountFilters logs a warning for include/exclude entries that
// match none of the container's mounts (they silently do nothing).
func warnUnusedMountFilters(l *slog.Logger, ctr *types.Container, cfg types.BackupConfig) {
	for _, ex := range cfg.Exclude {
		if !mountMatchesAny(ctr, func(m types.Mount) bool {
			return m.Name == ex || m.Source == ex || m.Destination == ex
		}) {
			l.Warn("buoy.exclude matches no mount, ignoring", "exclude", ex)
		}
	}
	for _, in := range cfg.Include {
		if !mountMatchesAny(ctr, func(m types.Mount) bool {
			return in.Key == m.Name || in.Key == m.Source || in.Key == m.Destination
		}) {
			l.Warn("buoy.include matches no mount, ignoring", "include", in.Key)
		}
	}
}

func mountMatchesAny(ctr *types.Container, match func(types.Mount) bool) bool {
	return slices.ContainsFunc(ctr.Mounts, match)
}

func (r *Runner) ignore(id string)  { r.ignoredIDs.Store(id, true) }
func (r *Runner) release(id string) { r.ignoredIDs.Delete(id) }

func countEligibleMounts(ctr *types.Container, cfg types.BackupConfig) int {
	n := 0
	for _, m := range ctr.Mounts {
		if m.Type == "tmpfs" {
			continue
		}
		if _, ok := types.MountMatches(m, cfg.Include, cfg.Exclude); ok {
			n++
		}
	}
	return n
}

func containerAttrs(ctr *types.Container, extra ...attribute.KeyValue) []attribute.KeyValue {
	base := make([]attribute.KeyValue, 0, 3+len(extra))
	base = append(base,
		attribute.String("container", ctr.Name),
		attribute.String("service", ctr.ComposeService),
		attribute.String("project", ctr.ComposeProject),
	)
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
