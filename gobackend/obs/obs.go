package obs

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Shutdown func(ctx context.Context) error

func Init(serviceName string) (Shutdown, *slog.Logger) {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		serviceName = "gobackend"
	}

	logger := newJSONLogger(serviceName)
	slog.SetDefault(logger)
	SetAppInfo(serviceName)

	shutdownTrace, err := initTracing(serviceName)
	if err != nil {
		logger.Error("init tracing failed", "err", err)
	}

	return func(ctx context.Context) error {
		var out error
		if shutdownTrace != nil {
			if err := shutdownTrace(ctx); err != nil {
				out = errors.Join(out, err)
			}
		}
		return out
	}, logger
}

func newJSONLogger(service string) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:     level,
		AddSource: false,
	})
	return slog.New(h).With("service", service)
}

func initTracing(serviceName string) (Shutdown, error) {
	// If no OTLP endpoint configured, keep the global no-op tracer provider.
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exp),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

func WrapHTTP(serviceName string, next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
	}
	return MetricsMiddleware(otelhttp.NewHandler(next, serviceName))
}

func Tracer(name string) trace.Tracer {
	n := strings.TrimSpace(name)
	if n == "" {
		n = "gobackend"
	}
	return otel.Tracer(n)
}

