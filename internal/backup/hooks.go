package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/depado/buoy/internal/hook"
	"github.com/depado/buoy/internal/types"
)

func (r *Runner) runPreHooks(ctx context.Context, ctr *types.Container, cfg types.BackupConfig, l *slog.Logger) error {
	var errs []error
	if cfg.HookPreCmd != "" {
		errs = append(errs, r.runHook(ctx, "buoy.hook.pre.host", ctr, "pre", "host",
			func(ctx context.Context) error { return hook.ExecOnHost(ctx, cfg.HookPreCmd, l) },
			l))
	}
	if cfg.HookPreExec != "" {
		errs = append(errs, r.runHook(ctx, "buoy.hook.pre.exec", ctr, "pre", "container",
			func(ctx context.Context) error {
				return hook.ExecInContainer(ctx, r.docker, ctr.ID, cfg.HookPreExec, l)
			},
			l))
	}
	return errors.Join(errs...)
}

func (r *Runner) runPostHooks(ctx context.Context, ctr *types.Container, cfg types.BackupConfig, l *slog.Logger) error {
	// Detach from the backup deadline: post-hooks must run even when the
	// budget is gone, since pre-hooks may have left state behind that
	// needs tearing down. They stay bounded by exec_timeout.
	ctx = context.WithoutCancel(ctx)

	var errs []error
	if cfg.HookPostExec != "" {
		errs = append(errs, r.runHook(ctx, "buoy.hook.post.exec", ctr, "post", "container",
			func(ctx context.Context) error {
				return hook.ExecInContainer(ctx, r.docker, ctr.ID, cfg.HookPostExec, l)
			},
			l))
	}
	if cfg.HookPostCmd != "" {
		errs = append(errs, r.runHook(ctx, "buoy.hook.post.host", ctr, "post", "host",
			func(ctx context.Context) error { return hook.ExecOnHost(ctx, cfg.HookPostCmd, l) },
			l))
	}
	return errors.Join(errs...)
}

func (r *Runner) runHook(
	ctx context.Context,
	spanName string,
	ctr *types.Container,
	hookType, hookTarget string,
	fn func(context.Context) error,
	l *slog.Logger,
) error {
	ctx, span := r.tracer.Start(ctx, spanName,
		trace.WithAttributes(
			attribute.String("hook.type", hookType),
			attribute.String("hook.target", hookTarget),
		),
	)
	defer span.End()

	// Fail fast: a hook starting when the backup budget is nearly gone is
	// guaranteed to die instantly, so skip it instead.
	if d, ok := ctx.Deadline(); ok && time.Until(d) < minRemainingBudget {
		l.Warn("skipping hook, backup budget nearly exhausted", "remaining", time.Until(d).Round(time.Second))
		return fmt.Errorf("%s skipped: backup budget nearly exhausted", hookType)
	}

	execCtx, cancel := context.WithTimeout(ctx, r.execTimeout)
	defer cancel()

	target := "host command"
	if hookTarget == "container" {
		target = "exec command"
	}
	label := hookType + "-backup " + target

	ok := true
	var hookErr error
	start := time.Now()
	if err := fn(execCtx); err != nil {
		l.Warn(label+" failed", "error", err)
		ok = false
		hookErr = fmt.Errorf("%s-backup %s: %w", hookType, target, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		l.Info(label + " completed")
	}

	r.meters.HookDuration.Record(ctx, time.Since(start).Seconds(),
		metric.WithAttributes(containerAttrs(ctr,
			attribute.String("type", hookType),
			attribute.String("target", hookTarget),
			attribute.Bool("success", ok),
		)...),
	)
	return hookErr
}
