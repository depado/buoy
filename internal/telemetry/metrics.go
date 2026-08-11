package telemetry

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

type MeterSet struct {
	meter metric.Meter

	BackupDuration    metric.Float64Gauge
	BackupsTotal      metric.Int64Counter
	ContainerStopDur  metric.Float64Gauge
	ContainerStartDur metric.Float64Gauge
	HookDuration      metric.Float64Gauge
	RetentionDuration metric.Float64Gauge
	CheckDuration     metric.Float64Gauge
	StackDuration     metric.Float64Gauge

	ContainersActive metric.Int64ObservableGauge
	LastSuccess      metric.Int64ObservableGauge
}

type ActiveCallback func(context.Context) (int64, error)

type LastSuccessPoint struct {
	ContainerName string
	Project       string
	Service       string
	Timestamp     int64
}

type LastSuccessCallback func(context.Context) ([]LastSuccessPoint, error)

const instrumentName = "buoy"

func newMeter(m metric.Meter) MeterSet {
	if m == nil {
		m = noop.NewMeterProvider().Meter(instrumentName)
	}
	return buildMeterSet(m)
}

func newMeterSet() MeterSet {
	return newMeter(nil)
}

func (t *Telemetry) Meters() MeterSet {
	if t.mp == nil {
		return newMeterSet()
	}
	return newMeter(t.mp.Meter(instrumentName))
}

func (ms MeterSet) RegisterCallbacks(active ActiveCallback, lastSuccess LastSuccessCallback) {
	if ms.meter == nil {
		return
	}
	if ms.ContainersActive != nil && active != nil {
		_, _ = ms.meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
			n, err := active(ctx)
			if err != nil {
				return err
			}
			o.ObserveInt64(ms.ContainersActive, n)
			return nil
		}, ms.ContainersActive)
	}
	if ms.LastSuccess != nil && lastSuccess != nil {
		_, _ = ms.meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
			points, err := lastSuccess(ctx)
			if err != nil {
				return err
			}
			for _, p := range points {
				o.ObserveInt64(ms.LastSuccess, p.Timestamp,
					metric.WithAttributes(
						attribute.String("container", p.ContainerName),
						attribute.String("service", p.Service),
						attribute.String("project", p.Project),
					),
				)
			}
			return nil
		}, ms.LastSuccess)
	}
}

func buildMeterSet(m metric.Meter) MeterSet {
	ms := MeterSet{meter: m}

	backupDuration, err := m.Float64Gauge("buoy.backup.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of the last completed backup run per repo"),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.backup.duration", "error", err)
	}
	ms.BackupDuration = backupDuration

	backupsTotal, err := m.Int64Counter("buoy.backup.runs",
		metric.WithUnit("{run}"),
		metric.WithDescription("Total number of completed backup runs"),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.backup.runs", "error", err)
	}
	ms.BackupsTotal = backupsTotal

	containerStopDur, err := m.Float64Gauge("buoy.container.stop.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of the last container stop operation"),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.container.stop.duration", "error", err)
	}
	ms.ContainerStopDur = containerStopDur

	containerStartDur, err := m.Float64Gauge("buoy.container.start.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of the last container start operation"),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.container.start.duration", "error", err)
	}
	ms.ContainerStartDur = containerStartDur

	hookDuration, err := m.Float64Gauge("buoy.hook.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of the last hook command execution"),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.hook.duration", "error", err)
	}
	ms.HookDuration = hookDuration

	retentionDuration, err := m.Float64Gauge("buoy.retention.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of the last retention operation per repo"),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.retention.duration", "error", err)
	}
	ms.RetentionDuration = retentionDuration

	checkDuration, err := m.Float64Gauge("buoy.check.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of the last restic repository check per repo"),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.check.duration", "error", err)
	}
	ms.CheckDuration = checkDuration

	stackDuration, err := m.Float64Gauge("buoy.stack.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of the last compose stack backup cycle per project"),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.stack.duration", "error", err)
	}
	ms.StackDuration = stackDuration

	containersActive, err := m.Int64ObservableGauge("buoy.containers.active",
		metric.WithUnit("{container}"),
		metric.WithDescription("Number of containers currently discovered and scheduled"),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.containers.active", "error", err)
	}
	ms.ContainersActive = containersActive

	lastSuccess, err := m.Int64ObservableGauge("buoy.backup.last_success",
		metric.WithUnit("s"),
		metric.WithDescription("Unix timestamp of last successful backup per container"),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.backup.last_success", "error", err)
	}
	ms.LastSuccess = lastSuccess

	return ms
}
