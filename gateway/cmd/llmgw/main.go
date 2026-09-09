// Command llmgw is the API-key gated LLM gateway.
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/miqui/kind-llm-gateway/gateway/internal/config"
	"github.com/miqui/kind-llm-gateway/gateway/internal/logging"
	"github.com/miqui/kind-llm-gateway/gateway/internal/policy"
	"github.com/miqui/kind-llm-gateway/gateway/internal/quota"
	"github.com/miqui/kind-llm-gateway/gateway/internal/ratelimit"
	"github.com/miqui/kind-llm-gateway/gateway/internal/server"
	"github.com/miqui/kind-llm-gateway/gateway/internal/tenants"
	"github.com/miqui/kind-llm-gateway/gateway/internal/tracing"
)

func main() {
	cfg := config.Load()

	shutdownTracing, err := tracing.InitTracing(context.Background(), tracing.ServiceName, cfg.JaegerOTLPEndpoint)
	if err != nil {
		log.Printf("tracing: init failed (continuing without tracing): %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(shutdownCtx); err != nil {
			log.Printf("tracing: error shutting down tracer provider: %v", err)
		}
	}()

	tenantStore, err := tenants.Open(cfg.SQLitePath)
	if err != nil {
		log.Fatalf("opening tenant store: %v", err)
	}
	defer tenantStore.Close()

	logStore, err := logging.Open(cfg.SQLitePath, cfg.LogMaxRows)
	if err != nil {
		log.Fatalf("opening log store: %v", err)
	}
	defer logStore.Close()

	srv := server.New(&cfg)
	srv.TenantStore = tenantStore
	srv.Quota = quota.NewLedger()
	srv.RateLimiter = ratelimit.NewLimiter()
	srv.LogStore = logStore
	srv.RouterCfg = policy.RouterConfig{
		ChatUpstreamURL:  cfg.LlamaChatURL,
		EmbedUpstreamURL: cfg.LlamaEmbedURL,
	}
	srv.BudgetCfg = policy.BudgetConfig{
		MaxPromptTokens:     cfg.MaxPromptTokens,
		MaxCompletionTokens: cfg.MaxCompletionTokens,
	}

	log.Printf("llmgw listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, srv.Handler()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
