// Package otel initialises the OpenTelemetry TracerProvider for distributed tracing.
package otel

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

// Setup initialises a TracerProvider and installs it as the global OTel provider.
// Returns a shutdown function that must be called on service exit.
//
// If OTEL_EXPORTER_OTLP_ENDPOINT is set, spans are exported via OTLP/HTTP
// (e.g. to a Jaeger or OpenTelemetry Collector sidecar).
// Otherwise falls back to stdout for local development.
func Setup(ctx context.Context, service, version string) (trace.Tracer, func(context.Context) error, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(service),
			semconv.ServiceVersion(version),
		),
	)
	if err != nil {
		return nil, nil, err
	}

	exp, err := buildExporter(ctx)
	if err != nil {
		return nil, nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	return tp.Tracer(service), tp.Shutdown, nil
}

func buildExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		return otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(endpoint),
			otlptracehttp.WithInsecure(),
		)
	}
	return stdouttrace.New(stdouttrace.WithPrettyPrint())
}
