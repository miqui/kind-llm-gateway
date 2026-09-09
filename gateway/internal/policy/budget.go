// Package policy implements pre-flight request policy enforcement for
// llmgw: token budgets and model allowlist / routing.
package policy

import (
	"encoding/json"
	"fmt"

	"github.com/miqui/kind-llm-gateway/gateway/internal/tenants"
	"github.com/miqui/kind-llm-gateway/gateway/internal/tokens"
)

// BudgetConfig holds the token-budget limits enforced on the request path.
// MaxPromptTokens and MaxCompletionTokens are global defaults sourced from
// config.Config (MAX_PROMPT_TOKENS / MAX_COMPLETION_TOKENS). The struct is
// deliberately per-request-resolvable so a future per-tenant override layer
// can populate it without changing the enforcement function's signature.
type BudgetConfig struct {
	MaxPromptTokens     int
	MaxCompletionTokens int
}

// ChatMessagePrompt is the minimal chat message shape needed for budget
// enforcement (role + content, matching tokens.ChatMessage).
type ChatMessagePrompt struct {
	Role    string
	Name    string
	Content string
}

// ChatBudgetRequest is the subset of a parsed chat-completion request
// relevant to budget enforcement.
type ChatBudgetRequest struct {
	Messages []ChatMessagePrompt
	// MaxTokens is the caller-requested max_tokens value; 0 means
	// unspecified and is never rejected on that basis alone.
	MaxTokens int
}

// BudgetError is a typed, JSON-serializable policy rejection returned by
// EnforceBudget. StatusCode is always 400 for budget violations.
type BudgetError struct {
	Type       string
	Message    string
	StatusCode int
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

type budgetErrorEnvelope struct {
	Error budgetErrorBody `json:"error"`
}

type budgetErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// JSON renders the error in the API's typed error envelope shape:
// {"error":{"type":"...","message":"..."}}.
func (e *BudgetError) JSON() ([]byte, error) {
	return json.Marshal(budgetErrorEnvelope{Error: budgetErrorBody{Type: e.Type, Message: e.Message}})
}

// EnforceBudget checks a parsed chat request against the configured token
// budgets, BEFORE any upstream call is made. It rejects:
//   - prompts whose counted token size exceeds cfg.MaxPromptTokens
//     ("context_length_exceeded")
//   - requests whose max_tokens exceeds cfg.MaxCompletionTokens
//     ("max_tokens_exceeded")
//
// A zero req.MaxTokens is treated as "unspecified" and is never rejected.
func EnforceBudget(cfg BudgetConfig, tenant tenants.Tenant, req ChatBudgetRequest) error {
	msgs := make([]tokens.ChatMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = tokens.ChatMessage{Role: m.Role, Name: m.Name, Content: m.Content}
	}
	promptTokens := tokens.CountChatTokens(msgs)

	if promptTokens > cfg.MaxPromptTokens {
		return &BudgetError{
			Type: "context_length_exceeded",
			Message: fmt.Sprintf(
				"prompt has %d tokens which exceeds the maximum of %d tokens for tenant %s",
				promptTokens, cfg.MaxPromptTokens, tenant.Name,
			),
			StatusCode: 400,
		}
	}

	if req.MaxTokens > 0 && req.MaxTokens > cfg.MaxCompletionTokens {
		return &BudgetError{
			Type: "max_tokens_exceeded",
			Message: fmt.Sprintf(
				"requested max_tokens %d exceeds the maximum of %d tokens for tenant %s",
				req.MaxTokens, cfg.MaxCompletionTokens, tenant.Name,
			),
			StatusCode: 400,
		}
	}

	return nil
}
