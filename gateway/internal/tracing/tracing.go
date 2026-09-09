// Package tracing wires llmgw's request handling to OpenTelemetry, exporting
// spans via OTLP/HTTP to Jaeger (or any OTLP collector) when configured.
package tracing

import (
	"context"
	"log"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// ServiceName is the OTel service.name reported for llmgw spans.
const ServiceName = "llmgw"

// tracerName identifies the tracer used to create llmgw spans.
const tracerName = "github.com/miqui/kind-llm-gateway/gateway"

func init() {
	// Ensure W3C tracecontext propagation is used for both extraction of
	// incoming traceparent headers and injection into upstream requests.
	otel.SetTextMapPropagator(propagation.TraceContext{})
}

// InitTracing configures the global OTel TracerProvider to export spans to
// otlpEndpoint via OTLP/HTTP. If otlpEndpoint is empty, a no-op
// TracerProvider is installed so that Tracer().Start remains cheap and safe
// to call unconditionally. It returns a shutdown function that should be
// deferred by the caller.
func InitTracing(ctx context.Context, serviceName, otlpEndpoint string) (func(context.Context) error, error) {
	if otlpEndpoint == "" {
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(otlpEndpoint),
	)
	if err != nil {
		log.Printf("tracing: failed to configure OTLP exporter (continuing without tracing): %v", err)
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(serviceName),
	))
	if err != nil {
		res = resource.Default()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	)
	otel.SetTracerProvider(tp)

	return func(shutdownCtx context.Context) error {
		return tp.Shutdown(shutdownCtx)
	}, nil
}

// Tracer returns the tracer used for llmgw spans, sourced from whatever
// TracerProvider is currently installed globally (real or no-op).
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// Middleware wraps an http.Handler, starting a server span per request named
// "<METHOD> <route>". It extracts any incoming W3C traceparent header via the
// global propagator, so a client-supplied trace id is honored (the request
// becomes a child of the caller's span rather than starting a new trace).
func Middleware(route string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			spanName := r.Method + " " + route
			ctx, span := Tracer().Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AddEvent records a lightweight span event (e.g. "auth.ok", "budget.ok",
// "ratelimit.ok", "quota.reserved") on the span found in ctx, with optional
// attributes. It is a no-op if ctx carries no active span.
func AddEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span == nil {
		return
	}
	span.AddEvent(name, trace.WithAttributes(attrs...))
}

// StartUpstreamSpan starts a client span (e.g. "llm.upstream.chat" or
// "llm.upstream.embed") as a child of ctx's current span, and injects the
// resulting context's traceparent into the outgoing request headers so the
// upstream (or retrieval-svc) participates in the same trace.
func StartUpstreamSpan(ctx context.Context, name string, req *http.Request) (context.Context, trace.Span) {
	ctx, span := Tracer().Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient))
	if req != nil {
		otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	}
	return ctx, span
}

// InjectTraceparent injects the current trace context from ctx into the
// given outgoing request's headers, for calls that build their own child
// span elsewhere (or none) but still need to propagate.
func InjectTraceparent(ctx context.Context, req *http.Request) {
	if req == nil {
		return
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
}

// Now is exposed for tests that need a deterministic clock hook; currently
// unused by the package itself but kept for parity with other internal
// packages' test-seams conventions.
var Now = time.Now
