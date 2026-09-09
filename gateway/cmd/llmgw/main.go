// Command llmgw is the API-key gated LLM gateway.
package main

import (
	"log"
	"net/http"

	"github.com/miqui/kind-llm-gateway/gateway/internal/config"
	"github.com/miqui/kind-llm-gateway/gateway/internal/server"
)

func main() {
	cfg := config.Load()
	srv := server.New(&cfg)

	log.Printf("llmgw listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, srv.Handler()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
