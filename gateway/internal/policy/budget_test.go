package policy

import (
	"testing"

	"github.com/miqui/kind-llm-gateway/gateway/internal/tenants"
)

func TestBudgetConfig_Defaults(t *testing.T) {
	cfg := BudgetConfig{MaxPromptTokens: 100, MaxCompletionTokens: 50}
	if cfg.MaxPromptTokens != 100 || cfg.MaxCompletionTokens != 50 {
		t.Fatal("BudgetConfig should hold configured values")
	}
}

func TestEnforceBudget_OversizePrompt(t *testing.T) {
	cfg := BudgetConfig{MaxPromptTokens: 5, MaxCompletionTokens: 100}
	tenant := tenants.Tenant{ID: "t1", Name: "team-a"}

	err := EnforceBudget(cfg, tenant, ChatBudgetRequest{
		Messages: []ChatMessagePrompt{{Role: "user", Content: "this is a very long prompt exceeding the limit"}},
	})
	if err == nil {
		t.Fatal("expected error for oversize prompt")
	}
	be, ok := err.(*BudgetError)
	if !ok {
		t.Fatalf("expected *BudgetError, got %T", err)
	}
	if be.Type != "context_length_exceeded" {
		t.Errorf("Type = %q, want context_length_exceeded", be.Type)
	}
	if be.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", be.StatusCode)
	}
}

func TestEnforceBudget_MaxTokensExceeded(t *testing.T) {
	cfg := BudgetConfig{MaxPromptTokens: 4096, MaxCompletionTokens: 10}
	tenant := tenants.Tenant{ID: "t1", Name: "team-a"}

	err := EnforceBudget(cfg, tenant, ChatBudgetRequest{
		Messages:  []ChatMessagePrompt{{Role: "user", Content: "hi"}},
		MaxTokens: 20,
	})
	if err == nil {
		t.Fatal("expected error for max_tokens over budget")
	}
	be, ok := err.(*BudgetError)
	if !ok {
		t.Fatalf("expected *BudgetError, got %T", err)
	}
	if be.Type != "max_tokens_exceeded" {
		t.Errorf("Type = %q, want max_tokens_exceeded", be.Type)
	}
	if be.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", be.StatusCode)
	}
}

func TestEnforceBudget_BoundaryOK(t *testing.T) {
	cfg := BudgetConfig{MaxPromptTokens: 4096, MaxCompletionTokens: 10}
	tenant := tenants.Tenant{ID: "t1", Name: "team-a"}

	// max_tokens exactly equal to budget should pass.
	err := EnforceBudget(cfg, tenant, ChatBudgetRequest{
		Messages:  []ChatMessagePrompt{{Role: "user", Content: "hi"}},
		MaxTokens: 10,
	})
	if err != nil {
		t.Fatalf("expected no error at boundary, got %v", err)
	}
}

func TestEnforceBudget_NoMaxTokensSpecified(t *testing.T) {
	cfg := BudgetConfig{MaxPromptTokens: 4096, MaxCompletionTokens: 10}
	tenant := tenants.Tenant{ID: "t1", Name: "team-a"}

	// MaxTokens == 0 means "not specified"; should not be rejected.
	err := EnforceBudget(cfg, tenant, ChatBudgetRequest{
		Messages: []ChatMessagePrompt{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("expected no error when max_tokens unspecified, got %v", err)
	}
}

func TestBudgetError_JSON(t *testing.T) {
	be := &BudgetError{Type: "context_length_exceeded", Message: "too long", StatusCode: 400}
	body, err := be.JSON()
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}
	want := `{"error":{"type":"context_length_exceeded","message":"too long"}}`
	if string(body) != want {
		t.Errorf("JSON() = %s, want %s", body, want)
	}
}
