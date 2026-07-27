package telemetry

import (
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

type MeterSet struct {
	BackupDuration    metric.Float64Histogram
	BackupMountCount  metric.Int64Histogram
	BackupsTotal      metric.Int64Counter
	ContainerStopDur  metric.Float64Histogram
	ContainerStartDur metric.Float64Histogram
	HookDuration      metric.Float64Histogram
	RetentionDuration metric.Float64Histogram
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
	return buildMeterSet(t.mp.Meter(instrumentName))
}

func buildMeterSet(m metric.Meter) MeterSet {
	backupDuration, _ := m.Float64Histogram("buoy.backup.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of restic backup per mount"),
	)
	backupMountCount, _ := m.Int64Histogram("buoy.backup.mounts",
		metric.WithUnit("{mount}"),
		metric.WithDescription("Number of backup-eligible mounts"),
	)
	backupsTotal, _ := m.Int64Counter("buoy.backups.total",
		metric.WithUnit("{backup}"),
		metric.WithDescription("Total number of backup operations"),
	)
	containerStopDur, _ := m.Float64Histogram("buoy.container.stop.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of container stop operations"),
	)
	containerStartDur, _ := m.Float64Histogram("buoy.container.start.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of container start operations"),
	)
	hookDuration, _ := m.Float64Histogram("buoy.hook.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of hook command execution"),
	)
	retentionDuration, _ := m.Float64Histogram("buoy.retention.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of retention operations"),
	)

	return MeterSet{
		BackupDuration:    backupDuration,
		BackupMountCount:  backupMountCount,
		BackupsTotal:      backupsTotal,
		ContainerStopDur:  containerStopDur,
		ContainerStartDur: containerStartDur,
		HookDuration:      hookDuration,
		RetentionDuration: retentionDuration,
	}
}
