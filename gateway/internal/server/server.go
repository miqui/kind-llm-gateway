// Package server wires up the llmgw HTTP handler.
package server

import (
	"net/http"

	"github.com/miqui/kind-llm-gateway/gateway/internal/config"
)

// Server holds dependencies for building the HTTP handler.
type Server struct {
	cfg *config.Config
	mux *http.ServeMux
}

// New constructs a Server. cfg may be nil for handlers that don't need it
// (e.g. in unit tests exercising only /healthz).
func New(cfg *config.Config) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
}

// Handler returns the root http.Handler for the gateway.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
