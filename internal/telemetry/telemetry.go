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

	"github.com/depado/buoy/internal/config"
	"github.com/depado/buoy/internal/version"
)

type Telemetry struct {
	tp      *sdktrace.TracerProvider
	mp      *sdkmetric.MeterProvider
	lp      *sdklog.LoggerProvider
	enabled bool
}

func New(conf *config.Conf, logger *slog.Logger) (*Telemetry, error) {
	if !conf.Otel.Enabled {
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

	opts := exporterOpts(conf)

	t := &Telemetry{enabled: true}
	var joinErr error

	if err := t.setupTraces(context.Background(), res, conf.Otel.Protocol == "http", opts); err != nil {
		logger.Warn("failed to setup otel traces, continuing without", "error", err)
		joinErr = errors.Join(joinErr, err)
	}
	if err := t.setupMetrics(context.Background(), res, conf.Otel.Protocol == "http", opts); err != nil {
		logger.Warn("failed to setup otel metrics, continuing without", "error", err)
		joinErr = errors.Join(joinErr, err)
	}
	if err := t.setupLogs(context.Background(), res, conf.Otel.Protocol == "http", opts); err != nil {
		logger.Warn("failed to setup otel logs, continuing without", "error", err)
		joinErr = errors.Join(joinErr, err)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logger.Info("telemetry initialized",
		"protocol", conf.Otel.Protocol,
		"endpoint", conf.Otel.Endpoint,
	)
	return t, joinErr
}

// exporterOpts returns configured overrides. Most settings are read automatically
// by the OTel SDK from environment variables (OTEL_EXPORTER_OTLP_*).
func exporterOpts(conf *config.Conf) exporterOptions {
	return exporterOptions{
		endpoint: conf.Otel.Endpoint,
		insecure: conf.Otel.Insecure,
	}
}

type exporterOptions struct {
	endpoint string
	insecure bool
}

func (t *Telemetry) setupTraces(ctx context.Context, res *sdkresource.Resource, useHTTP bool, opts exporterOptions) error {
	var exp sdktrace.SpanExporter
	var err error
	if useHTTP {
		var o []otlptracehttp.Option
		if opts.endpoint != "" {
			o = append(o, otlptracehttp.WithEndpoint(opts.endpoint))
		}
		if opts.insecure {
			o = append(o, otlptracehttp.WithInsecure())
		}
		exp, err = otlptracehttp.New(ctx, o...)
	} else {
		var o []otlptracegrpc.Option
		if opts.endpoint != "" {
			o = append(o, otlptracegrpc.WithEndpoint(opts.endpoint))
		}
		if opts.insecure {
			o = append(o, otlptracegrpc.WithInsecure())
		}
		exp, err = otlptracegrpc.New(ctx, o...)
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

func (t *Telemetry) setupMetrics(ctx context.Context, res *sdkresource.Resource, useHTTP bool, opts exporterOptions) error {
	if useHTTP {
		var o []otlpmetrichttp.Option
		if opts.endpoint != "" {
			o = append(o, otlpmetrichttp.WithEndpoint(opts.endpoint))
		}
		if opts.insecure {
			o = append(o, otlpmetrichttp.WithInsecure())
		}
		exp, err := otlpmetrichttp.New(ctx, o...)
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
	var o []otlpmetricgrpc.Option
	if opts.endpoint != "" {
		o = append(o, otlpmetricgrpc.WithEndpoint(opts.endpoint))
	}
	if opts.insecure {
		o = append(o, otlpmetricgrpc.WithInsecure())
	}
	exp, err := otlpmetricgrpc.New(ctx, o...)
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

func (t *Telemetry) setupLogs(ctx context.Context, res *sdkresource.Resource, useHTTP bool, opts exporterOptions) error {
	if useHTTP {
		var o []otlploghttp.Option
		if opts.endpoint != "" {
			o = append(o, otlploghttp.WithEndpoint(opts.endpoint))
		}
		if opts.insecure {
			o = append(o, otlploghttp.WithInsecure())
		}
		exp, err := otlploghttp.New(ctx, o...)
		if err != nil {
			return fmt.Errorf("create log exporter: %w", err)
		}
		t.lp = sdklog.NewLoggerProvider(
			sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
			sdklog.WithResource(res),
		)
		return nil
	}
	var o []otlploggrpc.Option
	if opts.endpoint != "" {
		o = append(o, otlploggrpc.WithEndpoint(opts.endpoint))
	}
	if opts.insecure {
		o = append(o, otlploggrpc.WithInsecure())
	}
	exp, err := otlploggrpc.New(ctx, o...)
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
