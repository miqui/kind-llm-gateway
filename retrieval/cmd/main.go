// Command retrieval is a stub retrieval service on the chat path (ADR-0006):
// it receives {query, tenant_id}, simulates vector-search latency, and
// returns a small canned set of context documents. It optionally emits an
// OTel span for each search, propagating the W3C traceparent from the
// incoming request as the parent (never starting a new root trace).
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/miqui/kind-llm-gateway/retrieval/internal/retrieval"
)

func main() {
	addr := listenAddr()

	shutdownTracing := retrieval.InitTracing(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	defer shutdownTracing()

	mux := retrieval.NewMux()

	log.Printf("retrieval-svc listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func listenAddr() string {
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		return v
	}
	if v := os.Getenv("PORT"); v != "" {
		return ":" + v
	}
	return ":8080"
}
