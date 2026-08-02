package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

	"github.com/depado/buoy/internal/version"
)

type Telemetry struct {
	tp      *sdktrace.TracerProvider
	mp      *sdkmetric.MeterProvider
	lp      *sdklog.LoggerProvider
	enabled bool
}

func New(logger *slog.Logger) (*Telemetry, error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return &Telemetry{}, nil
	}

	res, err := sdkresource.New(context.Background(),
		sdkresource.WithFromEnv(),
		sdkresource.WithTelemetrySDK(),
		sdkresource.WithAttributes(
			semconv.ServiceName("buoy"),
			semconv.ServiceVersion(version.Version),
		),
	)
	if err != nil {
		logger.Warn("failed to create otel resource", "error", err)
	}
	if res == nil {
		res = sdkresource.Empty()
	}

	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.Warn("otel internal error", "error", err)
	}))

	t := &Telemetry{enabled: true}
	var joinErr error

	if err := t.setupTraces(context.Background(), res); err != nil {
		logger.Warn("failed to setup otel traces, continuing without", "error", err)
		joinErr = errors.Join(joinErr, err)
	}
	if err := t.setupMetrics(context.Background(), res); err != nil {
		logger.Warn("failed to setup otel metrics, continuing without", "error", err)
		joinErr = errors.Join(joinErr, err)
	}
	if err := t.setupLogs(context.Background(), res); err != nil {
		logger.Warn("failed to setup otel logs, continuing without", "error", err)
		joinErr = errors.Join(joinErr, err)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logger.Info("telemetry initialized", "endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	return t, joinErr
}

func (t *Telemetry) setupTraces(ctx context.Context, res *sdkresource.Resource) error {
	exp, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return fmt.Errorf("create trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	t.tp = tp
	return nil
}

func (t *Telemetry) setupMetrics(ctx context.Context, res *sdkresource.Resource) error {
	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return fmt.Errorf("create metric exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)
	t.mp = mp
	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		slog.Warn("runtime metrics collection failed to start", "error", err)
	}
	return nil
}

func (t *Telemetry) setupLogs(ctx context.Context, res *sdkresource.Resource) error {
	exp, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return fmt.Errorf("create log exporter: %w", err)
	}
	t.lp = sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
		sdklog.WithResource(res),
	)
	return nil
}

func (t *Telemetry) Shutdown(ctx context.Context) error {
	if !t.enabled {
		return nil
	}
	var errs error
	if t.tp != nil {
		errs = errors.Join(errs, t.tp.ForceFlush(ctx))
		errs = errors.Join(errs, t.tp.Shutdown(ctx))
	}
	if t.mp != nil {
		errs = errors.Join(errs, t.mp.ForceFlush(ctx))
		errs = errors.Join(errs, t.mp.Shutdown(ctx))
	}
	if t.lp != nil {
		errs = errors.Join(errs, t.lp.ForceFlush(ctx))
		errs = errors.Join(errs, t.lp.Shutdown(ctx))
	}
	return errs
}

func (t *Telemetry) LoggerHandler(baseHandler slog.Handler) slog.Handler {
	if t.lp == nil {
		return baseHandler
	}
	otelHandler := otelslog.NewHandler("buoy", otelslog.WithLoggerProvider(t.lp))
	return &multiHandler{handlers: []slog.Handler{baseHandler, otelHandler}}
}
