package backup

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/depado/buoy/internal/hook"
	"github.com/depado/buoy/internal/types"
)

func (r *Runner) runPreHooks(ctx context.Context, ctr *types.Container, cfg types.BackupConfig, l *slog.Logger) {
	if cfg.HookPreCmd != "" {
		r.runHook(ctx, "buoy.hook.pre.host", ctr, "pre", "host",
			func(ctx context.Context) error { return hook.ExecOnHost(ctx, cfg.HookPreCmd, l) },
			l)
	}
	if cfg.HookPreExec != "" {
		r.runHook(ctx, "buoy.hook.pre.exec", ctr, "pre", "container",
			func(ctx context.Context) error {
				return hook.ExecInContainer(ctx, r.docker, ctr.ID, cfg.HookPreExec, l)
			},
			l)
	}
}

func (r *Runner) runPostHooks(ctx context.Context, ctr *types.Container, cfg types.BackupConfig, l *slog.Logger) {
	if cfg.HookPostExec != "" {
		r.runHook(ctx, "buoy.hook.post.exec", ctr, "post", "container",
			func(ctx context.Context) error {
				return hook.ExecInContainer(ctx, r.docker, ctr.ID, cfg.HookPostExec, l)
			},
			l)
	}
	if cfg.HookPostCmd != "" {
		r.runHook(ctx, "buoy.hook.post.host", ctr, "post", "host",
			func(ctx context.Context) error { return hook.ExecOnHost(ctx, cfg.HookPostCmd, l) },
			l)
	}
}

func (r *Runner) runHook(
	ctx context.Context,
	spanName string,
	ctr *types.Container,
	hookType, hookTarget string,
	fn func(context.Context) error,
	l *slog.Logger,
) {
	ctx, span := r.tracer.Start(ctx, spanName,
		trace.WithAttributes(
			attribute.String("hook.type", hookType),
			attribute.String("hook.target", hookTarget),
		),
	)
	defer span.End()

	execCtx, cancel := context.WithTimeout(ctx, r.execTimeout)
	defer cancel()

	target := "host command"
	if hookTarget == "container" {
		target = "exec command"
	}
	label := hookType + "-backup " + target

	ok := true
	start := time.Now()
	if err := fn(execCtx); err != nil {
		l.Warn(label+" failed", "error", err)
		ok = false
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
}
