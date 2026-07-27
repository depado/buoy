package backup

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"

	"github.com/depado/buoy/internal/docker"
)

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
