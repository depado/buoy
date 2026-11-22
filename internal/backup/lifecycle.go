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

	"github.com/depado/buoy/internal/types"
)

func (r *Runner) stopContainer(ctx context.Context, ctr *types.Container, cfg types.BackupConfig, l *slog.Logger) (stopped bool, stopErr error) {
	stopStart := time.Now()
	// Record ownership before stopping, so a crash between stop and restart is
	// recoverable. The record is dropped again as soon as the real state is
	// known: immediately on a successful start, or right here when the
	// container turns out not to have stopped.
	if err := r.repoReg.MarkStopped(ctr); err != nil {
		l.Warn("failed to persist stop ownership", "error", err)
	}
	ctx, span := r.tracer.Start(ctx, "buoy.container.stop",
		trace.WithAttributes(containerAttrs(ctr)...),
	)
	recordCtx := context.WithoutCancel(ctx)
	defer func() {
		if stopErr != nil {
			r.clearStopRecordIfNotStopped(ctx, ctr, l)
		}
		d := time.Since(stopStart).Seconds()
		if stopErr != nil {
			span.RecordError(stopErr)
			span.SetStatus(codes.Error, stopErr.Error())
		}
		r.meters.ContainerStopDur.Record(recordCtx, d,
			metric.WithAttributes(containerAttrs(ctr, attribute.Bool("success", stopErr == nil))...),
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
			return false, fmt.Errorf("wait for stop: %w (restart: %v)", err, startErr)
		}
		return false, fmt.Errorf("wait for stop: %w", err)
	}
	l.Info("container stopped")
	return true, nil
}

// clearStopRecordIfNotStopped drops the stop ownership record when a failed
// stop attempt left the container running (or being restarted by Docker).
// buoy does not owe a restart in that case, and a stale record would make a
// later daemon start act on an outdated fact. A container that is actually
// down keeps the record for startup recovery.
func (r *Runner) clearStopRecordIfNotStopped(ctx context.Context, ctr *types.Container, l *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	fresh, err := r.docker.InspectContainer(ctx, ctr.ID)
	if err != nil {
		l.Warn("failed to inspect container after failed stop, keeping stop record", "error", err)
		return
	}
	switch fresh.State {
	case "running", "restarting":
		r.clearStopped(ctr.ID, l)
	default:
		l.Debug("container did not stop, keeping stop record for recovery", "state", fresh.State)
	}
}

func (r *Runner) startContainer(ctx context.Context, ctr *types.Container, cfg types.BackupConfig, l *slog.Logger) error {
	startTime := time.Now()
	startErr := error(nil)
	// Detach from the backup deadline: the container must be restarted even
	// if the backup timed out, otherwise it is left stopped. Bound the
	// restart with its own timeout instead.
	ctx = context.WithoutCancel(ctx)
	ctx, cancel := context.WithTimeout(ctx, r.effectiveHealthWaitTimeout(cfg))
	defer cancel()
	ctx, span := r.tracer.Start(ctx, "buoy.container.start",
		trace.WithAttributes(containerAttrs(ctr)...),
	)
	recordCtx := context.WithoutCancel(ctx)
	defer func() {
		r.meters.ContainerStartDur.Record(recordCtx, time.Since(startTime).Seconds(),
			metric.WithAttributes(containerAttrs(ctr, attribute.Bool("success", startErr == nil))...),
		)
		span.End()
	}()

	l.Debug("starting container")
	startErr = r.docker.StartContainer(ctx, ctr.ID)
	if startErr != nil {
		l.Error("start container failed", "error", startErr)
		span.RecordError(startErr)
		span.SetStatus(codes.Error, startErr.Error())
		return startErr
	}
	// The container is back: buoy no longer owes a restart, even if the
	// readiness wait below fails.
	r.clearStopped(ctr.ID, l)
	l.Info("container started")
	if startErr = r.waitRunning(ctx, ctr, cfg, l); startErr != nil {
		span.RecordError(startErr)
		span.SetStatus(codes.Error, startErr.Error())
		return startErr
	}
	return nil
}

func (r *Runner) waitRunning(ctx context.Context, ctr *types.Container, cfg types.BackupConfig, l *slog.Logger) error {
	l.Debug("waiting for container to be running", "timeout", r.effectiveHealthWaitTimeout(cfg))
	ctx, cancel := context.WithTimeout(ctx, r.effectiveHealthWaitTimeout(cfg))
	defer cancel()

	if err := r.waitForEvent(ctx, ctr,
		[]string{"start", "die"},
		func(c *types.Container) (bool, error) {
			if c.State == "running" {
				return true, nil
			}
			if c.State == "exited" {
				if c.ExitCode != 0 {
					return false, fmt.Errorf("%s exited with code %d", ctr.Name, c.ExitCode)
				}
				return true, nil
			}
			return false, nil
		}); err != nil {
		l.Warn("container did not reach running state", "error", err)
		return err
	}
	l.Info("container running")
	return nil
}
