package telemetry

import (
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/depado/buoy/internal/version"
)

type MeterSet struct {
	BackupDuration    metric.Float64Histogram
	BackupMountCount  metric.Int64Histogram
	BackupsTotal      metric.Int64Counter
	ContainerStopDur  metric.Float64Histogram
	ContainerStartDur metric.Float64Histogram
	HookDuration      metric.Float64Histogram
	RetentionDuration metric.Float64Histogram
	CheckDuration     metric.Float64Histogram
}

const (
	instrumentName = "buoy"
)

func newMeter(factory func(metric.Meter) MeterSet) MeterSet {
	if factory == nil {
		return MeterSet{}
	}
	return factory(noop.NewMeterProvider().Meter(instrumentName))
}

func newMeterSet() MeterSet {
	return newMeter(buildMeterSet)
}

func (t *Telemetry) Meters() MeterSet {
	if t.mp == nil {
		return newMeterSet()
	}
	return buildMeterSet(t.mp.Meter(instrumentName,
		metric.WithInstrumentationVersion(version.Version),
	))
}

func buildMeterSet(m metric.Meter) MeterSet {
	backupDuration, err := m.Float64Histogram("buoy.backup.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of restic backup per mount"),
		metric.WithExplicitBucketBoundaries(1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.backup.duration", "error", err)
	}
	backupMountCount, err := m.Int64Histogram("buoy.backup.mounts",
		metric.WithUnit("{mount}"),
		metric.WithDescription("Number of backup-eligible mounts"),
		metric.WithExplicitBucketBoundaries(1, 2, 3, 5, 10, 20),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.backup.mounts", "error", err)
	}
	backupsTotal, err := m.Int64Counter("buoy.backups.total",
		metric.WithUnit("{backup}"),
		metric.WithDescription("Total number of backup operations"),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.backups.total", "error", err)
	}
	containerStopDur, err := m.Float64Histogram("buoy.container.stop.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of container stop operations"),
		metric.WithExplicitBucketBoundaries(0.1, 0.5, 1, 2, 5, 10, 30, 60, 120),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.container.stop.duration", "error", err)
	}
	containerStartDur, err := m.Float64Histogram("buoy.container.start.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of container start operations"),
		metric.WithExplicitBucketBoundaries(0.1, 0.5, 1, 2, 5, 10, 30, 60, 120),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.container.start.duration", "error", err)
	}
	hookDuration, err := m.Float64Histogram("buoy.hook.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of hook command execution"),
		metric.WithExplicitBucketBoundaries(0.1, 0.5, 1, 2, 5, 10, 30, 60, 120),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.hook.duration", "error", err)
	}
	retentionDuration, err := m.Float64Histogram("buoy.retention.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of retention operations"),
		metric.WithExplicitBucketBoundaries(1, 5, 10, 30, 60, 120, 300, 600, 1800),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.retention.duration", "error", err)
	}
	checkDuration, err := m.Float64Histogram("buoy.check.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of restic repository check operations"),
		metric.WithExplicitBucketBoundaries(1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600),
	)
	if err != nil {
		slog.Warn("failed to create metric", "name", "buoy.check.duration", "error", err)
	}

	return MeterSet{
		BackupDuration:    backupDuration,
		BackupMountCount:  backupMountCount,
		BackupsTotal:      backupsTotal,
		ContainerStopDur:  containerStopDur,
		ContainerStartDur: containerStartDur,
		HookDuration:      hookDuration,
		RetentionDuration: retentionDuration,
		CheckDuration:     checkDuration,
	}
}
