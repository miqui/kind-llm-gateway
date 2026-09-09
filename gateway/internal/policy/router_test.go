package policy

import (
	"testing"

	"github.com/miqui/kind-llm-gateway/gateway/internal/tenants"
)

func testRouterConfig() RouterConfig {
	return RouterConfig{
		ChatUpstreamURL:  "http://llama-svc:8080",
		EmbedUpstreamURL: "http://llama-svc:8081",
		Models: map[string]string{
			"qwen2.5-1.5b-instruct": "chat",
			"nomic-embed-text":      "embed",
		},
		TierTargets: map[string]string{
			"standard": "chat",
			"economy":  "chat",
		},
	}
}

func TestResolveTarget_AllowedChatModel(t *testing.T) {
	cfg := testRouterConfig()
	tenant := tenants.Tenant{ID: "t1", Name: "team-a", AllowedModels: []string{"qwen2.5-1.5b-instruct"}, CostTier: "standard"}

	url, err := ResolveTarget(cfg, tenant, "qwen2.5-1.5b-instruct", "/v1/chat/completions")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://llama-svc:8080" {
		t.Errorf("url = %q, want chat upstream", url)
	}
}

func TestResolveTarget_AllowedEmbedModel(t *testing.T) {
	cfg := testRouterConfig()
	tenant := tenants.Tenant{ID: "t1", Name: "team-a", AllowedModels: []string{"nomic-embed-text"}, CostTier: "standard"}

	url, err := ResolveTarget(cfg, tenant, "nomic-embed-text", "/v1/embeddings")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://llama-svc:8081" {
		t.Errorf("url = %q, want embed upstream", url)
	}
}

func TestResolveTarget_EmbeddingsPathAlwaysRoutesToEmbed(t *testing.T) {
	cfg := testRouterConfig()
	// Even a model tagged "chat" hitting /v1/embeddings should still be
	// routed to the embed upstream per the always-embed rule for that path.
	cfg.Models["qwen2.5-1.5b-instruct"] = "chat"
	tenant := tenants.Tenant{ID: "t1", Name: "team-a", AllowedModels: []string{"qwen2.5-1.5b-instruct"}, CostTier: "standard"}

	url, err := ResolveTarget(cfg, tenant, "qwen2.5-1.5b-instruct", "/v1/embeddings")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://llama-svc:8081" {
		t.Errorf("url = %q, want embed upstream for /v1/embeddings", url)
	}
}

func TestResolveTarget_DisallowedModel(t *testing.T) {
	cfg := testRouterConfig()
	tenant := tenants.Tenant{ID: "t1", Name: "team-a", AllowedModels: []string{"nomic-embed-text"}}

	_, err := ResolveTarget(cfg, tenant, "qwen2.5-1.5b-instruct", "/v1/chat/completions")
	if err == nil {
		t.Fatal("expected error for disallowed model")
	}
	re, ok := err.(*RouteError)
	if !ok {
		t.Fatalf("expected *RouteError, got %T", err)
	}
	if re.Type != "model_not_allowed" {
		t.Errorf("Type = %q, want model_not_allowed", re.Type)
	}
	if re.StatusCode != 403 {
		t.Errorf("StatusCode = %d, want 403", re.StatusCode)
	}
}

func TestResolveTarget_UnknownModel(t *testing.T) {
	cfg := testRouterConfig()
	tenant := tenants.Tenant{ID: "t1", Name: "team-a", AllowedModels: []string{"totally-unknown-model"}}

	_, err := ResolveTarget(cfg, tenant, "totally-unknown-model", "/v1/chat/completions")
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
	re, ok := err.(*RouteError)
	if !ok {
		t.Fatalf("expected *RouteError, got %T", err)
	}
	if re.Type != "model_not_found" {
		t.Errorf("Type = %q, want model_not_found", re.Type)
	}
	if re.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", re.StatusCode)
	}
}

func TestResolveTarget_TierMapSelection(t *testing.T) {
	cfg := testRouterConfig()
	cfg.TierTargets = map[string]string{
		"standard": "chat",
		"economy":  "chat",
		"premium":  "embed", // artificial for testability
	}
	cfg.Models["premium-model"] = "" // no explicit workload; falls back to tier map
	tenant := tenants.Tenant{ID: "t1", Name: "team-a", AllowedModels: []string{"premium-model"}, CostTier: "premium"}

	url, err := ResolveTarget(cfg, tenant, "premium-model", "/v1/chat/completions")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://llama-svc:8081" {
		t.Errorf("url = %q, want embed upstream via tier map", url)
	}
}

func TestRouteError_JSON(t *testing.T) {
	re := &RouteError{Type: "model_not_allowed", Message: "model x not allowed for tenant y", StatusCode: 403}
	body, err := re.JSON()
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}
	want := `{"error":{"type":"model_not_allowed","message":"model x not allowed for tenant y"}}`
	if string(body) != want {
		t.Errorf("JSON() = %s, want %s", body, want)
	}
}
