// Package server wires up the llmgw HTTP handler.
package server

import (
	"net/http"
	"sync"

	"github.com/miqui/kind-llm-gateway/gateway/internal/auth"
	"github.com/miqui/kind-llm-gateway/gateway/internal/config"
	"github.com/miqui/kind-llm-gateway/gateway/internal/logging"
	"github.com/miqui/kind-llm-gateway/gateway/internal/policy"
	"github.com/miqui/kind-llm-gateway/gateway/internal/proxy"
	"github.com/miqui/kind-llm-gateway/gateway/internal/quota"
	"github.com/miqui/kind-llm-gateway/gateway/internal/ratelimit"
	"github.com/miqui/kind-llm-gateway/gateway/internal/tenants"
)

// Server holds dependencies for building the HTTP handler.
type Server struct {
	cfg *config.Config
	mux *http.ServeMux

	TenantStore *tenants.Store
	Quota       *quota.Ledger
	RateLimiter *ratelimit.Limiter
	LogStore    *logging.Store
	RouterCfg   policy.RouterConfig
	BudgetCfg   policy.BudgetConfig

	routesOnce sync.Once
}

// New constructs a Server. cfg may be nil for handlers that don't need it
// (e.g. in unit tests exercising only /healthz). Callers that need the
// OpenAI-compatible proxy endpoints should set TenantStore, Quota,
// RateLimiter, LogStore, RouterCfg, and BudgetCfg on the returned Server
// before calling Handler() for the first time.
func New(cfg *config.Config) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)

	if s.TenantStore == nil || s.Quota == nil || s.RateLimiter == nil {
		// Proxy endpoints require full dependencies; skip wiring them
		// when a Server is constructed bare (e.g. New(nil) in tests
		// exercising only /healthz).
		return
	}

	proxyHandler := proxy.NewHandler(proxy.Handler{
		RouterCfg: s.RouterCfg,
		BudgetCfg: s.BudgetCfg,
		Quota:     s.Quota,
		LogStore:  s.LogStore,
	})
	if s.cfg != nil {
		proxyHandler.RetrievalURL = s.cfg.RetrievalURL
		proxyHandler.RedactExtraPatterns = s.cfg.RedactExtraPatterns
	}

	authMW := auth.Middleware(s.TenantStore)
	rlMW := ratelimit.Middleware(s.RateLimiter, s.tenantRPS)

	chain := func(h http.HandlerFunc) http.Handler {
		return authMW(rlMW(h))
	}

	s.mux.Handle("POST /v1/chat/completions", chain(proxyHandler.ChatCompletions))
	s.mux.Handle("POST /v1/embeddings", chain(proxyHandler.Embeddings))
}

// tenantRPS resolves the authenticated tenant's configured requests-per-
// second for the rate-limit middleware. It runs after auth.Middleware has
// injected the tenant into the request context.
func (s *Server) tenantRPS(r *http.Request) (string, float64) {
	tenant, ok := auth.FromContext(r.Context())
	if !ok {
		return "", 1
	}
	return tenant.ID, tenant.RateLimitRPS
}

// Handler returns the root http.Handler for the gateway, building routes
// (using whatever dependencies are set on the Server) on first call.
func (s *Server) Handler() http.Handler {
	s.routesOnce.Do(s.routes)
	return s.mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
