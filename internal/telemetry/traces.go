package telemetry

import (
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func (t *Telemetry) Tracer() trace.Tracer {
	if t.tp == nil {
		return noop.NewTracerProvider().Tracer("buoy")
	}
	return t.tp.Tracer("buoy")
}
