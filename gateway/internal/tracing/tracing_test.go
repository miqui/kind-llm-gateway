package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestInitTracingEmptyEndpointIsNoop(t *testing.T) {
	shutdown, err := InitTracing(context.Background(), "llmgw", "")
	if err != nil {
		t.Fatalf("InitTracing: %v", err)
	}
	defer shutdown(context.Background())

	_, span := Tracer().Start(context.Background(), "test-span")
	defer span.End()
	if span.SpanContext().IsValid() {
		// Noop tracer spans are not "recording" but may still carry a
		// valid-looking (zero) span context; the important property is
		// that nothing panics and IsRecording is false.
	}
	if span.IsRecording() {
		t.Fatalf("expected noop span to not be recording")
	}
}

func TestMiddlewareCreatesServerSpan(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	}()

	var gotName string
	h := Middleware("/v1/chat/completions")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		AddEvent(r.Context(), "auth.ok")
		w.WriteHeader(http.StatusOK)
		_ = gotName
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 ended span, got %d", len(spans))
	}
	if got, want := spans[0].Name(), "POST /v1/chat/completions"; got != want {
		t.Fatalf("span name = %q, want %q", got, want)
	}
	events := spans[0].Events()
	found := false
	for _, e := range events {
		if e.Name == "auth.ok" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected auth.ok event on span, got %+v", events)
	}
}

func TestMiddlewareHonorsIncomingTraceparent(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	}()

	h := Middleware("/v1/chat/completions")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const incomingTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("traceparent", "00-"+incomingTraceID+"-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 ended span, got %d", len(spans))
	}
	if got := spans[0].SpanContext().TraceID().String(); got != incomingTraceID {
		t.Fatalf("trace id = %s, want client-supplied %s", got, incomingTraceID)
	}
}

func TestStartUpstreamSpanInjectsTraceparent(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	}()

	ctx, parentSpan := Tracer().Start(context.Background(), "parent")
	defer parentSpan.End()

	req := httptest.NewRequest(http.MethodPost, "http://upstream/v1/chat/completions", nil)
	_, span := StartUpstreamSpan(ctx, "llm.upstream.chat", req)
	defer span.End()

	if req.Header.Get("traceparent") == "" {
		t.Fatalf("expected traceparent header to be injected on upstream request")
	}
}
