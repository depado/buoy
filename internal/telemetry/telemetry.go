package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

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

	hostname, _ := os.Hostname()

	res, err := sdkresource.New(context.Background(),
		sdkresource.WithAttributes(
			semconv.ServiceName("buoy"),
			semconv.ServiceVersion(version.Version),
			semconv.HostName(hostname),
		),
		sdkresource.WithFromEnv(),
	)
	if err != nil {
		logger.Warn("failed to create otel resource", "error", err)
	}
	if res == nil {
		res = sdkresource.Empty()
	}

	useHTTP := os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL") == "http/protobuf"

	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.Warn("otel internal error", "error", err)
	}))

	t := &Telemetry{enabled: true}
	var joinErr error

	if err := t.setupTraces(context.Background(), res, useHTTP); err != nil {
		logger.Warn("failed to setup otel traces, continuing without", "error", err)
		joinErr = errors.Join(joinErr, err)
	}
	if err := t.setupMetrics(context.Background(), res, useHTTP); err != nil {
		logger.Warn("failed to setup otel metrics, continuing without", "error", err)
		joinErr = errors.Join(joinErr, err)
	}
	if err := t.setupLogs(context.Background(), res, useHTTP); err != nil {
		logger.Warn("failed to setup otel logs, continuing without", "error", err)
		joinErr = errors.Join(joinErr, err)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	logger.Info("telemetry initialized",
		"protocol", map[bool]string{true: "http", false: "grpc"}[useHTTP],
		"endpoint", endpoint,
	)
	return t, joinErr
}

func (t *Telemetry) setupTraces(ctx context.Context, res *sdkresource.Resource, useHTTP bool) error {
	var exp sdktrace.SpanExporter
	var err error
	if useHTTP {
		exp, err = otlptracehttp.New(ctx)
	} else {
		exp, err = otlptracegrpc.New(ctx)
	}
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

func (t *Telemetry) setupMetrics(ctx context.Context, res *sdkresource.Resource, useHTTP bool) error {
	if useHTTP {
		exp, err := otlpmetrichttp.New(ctx)
		if err != nil {
			return fmt.Errorf("create metric exporter: %w", err)
		}
		reader := sdkmetric.NewPeriodicReader(exp)
		mp := sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(reader),
			sdkmetric.WithResource(res),
		)
		otel.SetMeterProvider(mp)
		t.mp = mp
		runtime.Start(runtime.WithMeterProvider(mp)) //nolint:errcheck
		return nil
	}
	exp, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		return fmt.Errorf("create metric exporter: %w", err)
	}
	reader := sdkmetric.NewPeriodicReader(exp)
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)
	t.mp = mp
	runtime.Start(runtime.WithMeterProvider(mp)) //nolint:errcheck
	return nil
}

func (t *Telemetry) setupLogs(ctx context.Context, res *sdkresource.Resource, useHTTP bool) error {
	if useHTTP {
		exp, err := otlploghttp.New(ctx)
		if err != nil {
			return fmt.Errorf("create log exporter: %w", err)
		}
		t.lp = sdklog.NewLoggerProvider(
			sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
			sdklog.WithResource(res),
		)
		return nil
	}
	exp, err := otlploggrpc.New(ctx)
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
		errs = errors.Join(errs, t.mp.Shutdown(ctx))
	}
	if t.lp != nil {
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
