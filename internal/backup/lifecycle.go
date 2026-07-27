package backup

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/moby/moby/api/types/container"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/depado/buoy/internal/docker"
)

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

func (r *Runner) waitRunning(ctx context.Context, ctr *docker.Container, l *slog.Logger) {
	l.Debug("waiting for container to reach running state", "timeout", r.healthWaitTimeout)
	ctx, cancel := context.WithTimeout(ctx, r.healthWaitTimeout)
	defer cancel()

	if err := r.waitForEvent(ctx, ctr,
		[]string{"start", "die"},
		func(c *docker.Container) (bool, error) {
			return c.State == "running" || c.State == "exited", nil
		}); err != nil {
		l.Warn("container did not reach running state", "error", err)
	}
}
