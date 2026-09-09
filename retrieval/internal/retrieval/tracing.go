package retrieval

import (
	"context"
	"log"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// InitTracing configures the global OTel TracerProvider to export spans to
// otlpEndpoint via OTLP/HTTP, if otlpEndpoint is non-empty. Connection
// failures are non-fatal: the exporter is configured lazily and any
// export-time errors are logged, never crashing the service. If
// otlpEndpoint is empty, the global no-op TracerProvider is left in place
// and tracer().Start becomes a cheap no-op.
//
// It returns a shutdown function that should be deferred by the caller.
func InitTracing(otlpEndpoint string) func() {
	if otlpEndpoint == "" {
		return func() {}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(otlpEndpoint),
	)
	if err != nil {
		// Non-fatal: log and continue without tracing configured.
		log.Printf("retrieval: failed to configure OTLP exporter (continuing without tracing): %v", err)
		return func() {}
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName("retrieval-svc"),
	))
	if err != nil {
		res = resource.Default()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := tp.Shutdown(shutdownCtx); err != nil {
			log.Printf("retrieval: error shutting down tracer provider: %v", err)
		}
	}
}
