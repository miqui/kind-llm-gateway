package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

func spanContextFromContext(ctx context.Context) trace.SpanContext {
	return trace.SpanContextFromContext(ctx)
}

func withNoSleep(t *testing.T) {
	t.Helper()
	orig := sleep
	sleep = func(time.Duration) {}
	t.Cleanup(func() { sleep = orig })
}

func TestHandleRetrievalReturnsThreeDocs(t *testing.T) {
	withNoSleep(t)

	mux := NewMux()
	body, _ := json.Marshal(Request{Query: "hello world", TenantID: "team-a"})
	req := httptest.NewRequest(http.MethodPost, "/v1/retrieval", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Documents) != 3 {
		t.Fatalf("expected 3 documents (no keyword match falls back to full pool), got %d", len(resp.Documents))
	}
}

func TestHandleRetrievalKeywordMatch(t *testing.T) {
	withNoSleep(t)

	mux := NewMux()
	body, _ := json.Marshal(Request{Query: "embeddings", TenantID: "team-a"})
	req := httptest.NewRequest(http.MethodPost, "/v1/retrieval", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Documents) == 0 {
		t.Fatalf("expected at least one matched document")
	}
	found := false
	for _, d := range resp.Documents {
		if d.ID == "doc-3" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected doc-3 (mentions embeddings) to match query %q, got %+v", "embeddings", resp.Documents)
	}
}

func TestHandleRetrievalAppliesSimulatedLatency(t *testing.T) {
	orig := sleep
	var slept time.Duration
	sleep = func(d time.Duration) { slept = d }
	t.Cleanup(func() { sleep = orig })

	mux := NewMux()
	body, _ := json.Marshal(Request{Query: "x", TenantID: "team-a"})
	req := httptest.NewRequest(http.MethodPost, "/v1/retrieval", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if slept != simulatedLatency {
		t.Fatalf("expected simulated latency %v applied, got %v", simulatedLatency, slept)
	}
}

func TestHandleRetrievalPropagatesTraceparent(t *testing.T) {
	withNoSleep(t)

	mux := NewMux()
	body, _ := json.Marshal(Request{Query: "hi", TenantID: "team-a"})
	req := httptest.NewRequest(http.MethodPost, "/v1/retrieval", bytes.NewReader(body))
	// A valid W3C traceparent header: version-traceid-spanid-flags.
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// At minimum, extraction must not error and must produce a context
	// whose span context matches the incoming trace id (parent link, not
	// a new root).
	ctx := ExtractTraceContext(req.Context(), req.Header)
	sc := spanContextFromContext(ctx)
	if !sc.IsValid() {
		t.Fatalf("expected valid remote span context extracted from traceparent")
	}
	if sc.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("expected extracted trace id to match incoming traceparent, got %s", sc.TraceID().String())
	}
}

func TestHealthz(t *testing.T) {
	mux := NewMux()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Body.String(); got != "ok" {
		t.Fatalf("expected body 'ok', got %q", got)
	}
}
