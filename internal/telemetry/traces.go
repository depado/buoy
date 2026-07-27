package telemetry

import (
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type TracerSet struct {
	Tracer trace.Tracer
}

func newTracerSet() TracerSet {
	tp := noop.NewTracerProvider()
	return TracerSet{
		Tracer: tp.Tracer("buoy"),
	}
}

func (t *Telemetry) Tracers() TracerSet {
	if t.tp == nil {
		return newTracerSet()
	}
	return TracerSet{
		Tracer: t.tp.Tracer("buoy"),
	}
}
