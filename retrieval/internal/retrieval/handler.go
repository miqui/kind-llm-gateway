// Package retrieval implements the retrieval-svc stub (ADR-0006): a tiny
// HTTP service on the chat path that simulates a vector-search lookup and
// returns a small canned set of context documents.
package retrieval

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// simulatedLatency approximates the cost of a real vector-search lookup.
const simulatedLatency = 50 * time.Millisecond

// Request is the incoming retrieval request body.
type Request struct {
	Query    string `json:"query"`
	TenantID string `json:"tenant_id"`
}

// Document is a single canned context document.
type Document struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Response is the retrieval response body.
type Response struct {
	Documents []Document `json:"documents"`
}

// docPool is the static pool of canned documents returned by the stub.
// Naive keyword matching selects up to 3 documents whose text shares a
// word with the query; if nothing matches, all 3 are returned.
var docPool = []Document{
	{ID: "doc-1", Text: "The gateway enforces per-tenant token budgets before any upstream call is made."},
	{ID: "doc-2", Text: "llama.cpp serves both chat and embedding models behind the llmgw proxy."},
	{ID: "doc-3", Text: "Retrieval augmentation runs only on the chat completions path, never on embeddings."},
}

// clock is overridable in tests to avoid sleeping the full simulated
// latency.
var sleep = time.Sleep

// tracer is lazily set by InitTracing; it defaults to a no-op tracer via
// otel.Tracer, which is always safe to call even without a configured
// TracerProvider.
func tracer() trace.Tracer {
	return otel.Tracer("retrieval-svc")
}

// NewMux builds the HTTP handler for retrieval-svc.
func NewMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/retrieval", handleRetrieval)
	mux.HandleFunc("GET /healthz", handleHealthz)
	return mux
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func handleRetrieval(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"type":    "invalid_request_error",
				"message": "invalid JSON body",
			},
		})
		return
	}

	// Extract the W3C traceparent from the incoming request so any span
	// we create is a child of the caller's trace, not a new root.
	ctx := ExtractTraceContext(r.Context(), r.Header)

	ctx, span := tracer().Start(ctx, "retrieval.search")
	defer span.End()

	sleep(simulatedLatency)

	docs := selectDocuments(req.Query)

	_ = ctx // reserved for any future downstream propagation
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(Response{Documents: docs})
}

// selectDocuments performs a naive keyword match against the static doc
// pool, returning up to 3 documents. If no document matches any query
// word, all 3 canned documents are returned.
func selectDocuments(query string) []Document {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return append([]Document(nil), docPool...)
	}

	var matched []Document
	for _, d := range docPool {
		lower := strings.ToLower(d.Text)
		for _, w := range words {
			if w == "" {
				continue
			}
			if strings.Contains(lower, w) {
				matched = append(matched, d)
				break
			}
		}
	}
	if len(matched) == 0 {
		return append([]Document(nil), docPool...)
	}
	return matched
}

// ExtractTraceContext extracts a W3C traceparent (and tracestate) from
// incoming HTTP headers into ctx, so a span started against the returned
// context becomes a child of the caller's span rather than a new root.
func ExtractTraceContext(ctx context.Context, headers http.Header) context.Context {
	propagator := propagation.TraceContext{}
	return propagator.Extract(ctx, propagation.HeaderCarrier(headers))
}
