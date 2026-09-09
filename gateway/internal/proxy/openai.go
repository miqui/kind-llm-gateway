// Package proxy implements the OpenAI-compatible pass-through for
// /v1/chat/completions and /v1/embeddings: policy enforcement, quota
// accounting, retrieval augmentation (chat only), and forwarding to the
// llama.cpp upstream — including SSE stream relay with cancellation
// propagation.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/miqui/kind-llm-gateway/gateway/internal/auth"
	"github.com/miqui/kind-llm-gateway/gateway/internal/logging"
	"github.com/miqui/kind-llm-gateway/gateway/internal/policy"
	"github.com/miqui/kind-llm-gateway/gateway/internal/quota"
	"github.com/miqui/kind-llm-gateway/gateway/internal/tokens"
	"github.com/miqui/kind-llm-gateway/gateway/internal/tracing"
)

const (
	chatPath  = "/v1/chat/completions"
	embedPath = "/v1/embeddings"

	traceparentHeader = "traceparent"
	tenantIDHeader    = "X-Tenant-Id"
)

// Handler serves the OpenAI-compatible proxy endpoints.
type Handler struct {
	RouterCfg           policy.RouterConfig
	BudgetCfg           policy.BudgetConfig
	Quota               *quota.Ledger
	LogStore            *logging.Store
	RetrievalURL        string
	RedactExtraPatterns []string
	Client              *http.Client

	// now is overridable for deterministic tests.
	now func() time.Time
}

// NewHandler constructs a Handler with sane defaults for unset fields.
func NewHandler(h Handler) *Handler {
	if h.Client == nil {
		h.Client = &http.Client{}
	}
	if h.now == nil {
		h.now = time.Now
	}
	return &h
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

type retrievalDoc struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type retrievalResponse struct {
	Documents []retrievalDoc `json:"documents"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func writeTypedError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"type":    errType,
			"message": message,
		},
	})
}

func writeJSONError(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ChatCompletions handles POST /v1/chat/completions.
func (h *Handler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	tenant, ok := auth.FromContext(r.Context())
	if !ok {
		writeTypedError(w, http.StatusUnauthorized, "invalid_api_key", "missing tenant context")
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, "invalid_request_error", "failed to read request body")
		return
	}

	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		writeTypedError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body")
		return
	}

	model, _ := raw["model"].(string)
	msgs, err := parseChatMessages(raw["messages"])
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	maxTokens := intFromAny(raw["max_tokens"])
	stream, _ := raw["stream"].(bool)

	target, rerr := policy.ResolveTarget(h.RouterCfg, tenant, model, chatPath)
	if rerr != nil {
		relayPolicyError(w, rerr)
		return
	}

	budgetMsgs := make([]policy.ChatMessagePrompt, len(msgs))
	for i, m := range msgs {
		budgetMsgs[i] = policy.ChatMessagePrompt{Role: m.Role, Name: m.Name, Content: m.Content}
	}
	if berr := policy.EnforceBudget(h.BudgetCfg, tenant, policy.ChatBudgetRequest{
		Messages:  budgetMsgs,
		MaxTokens: maxTokens,
	}); berr != nil {
		relayBudgetError(w, berr)
		return
	}
	tracing.AddEvent(r.Context(), "budget.ok")

	tokMsgs := make([]tokens.ChatMessage, len(msgs))
	for i, m := range msgs {
		tokMsgs[i] = tokens.ChatMessage{Role: m.Role, Name: m.Name, Content: m.Content}
	}
	promptTokens := tokens.CountChatTokens(tokMsgs)

	completionEstimate := maxTokens
	if completionEstimate <= 0 {
		completionEstimate = h.BudgetCfg.MaxCompletionTokens
	}
	estimated := int64(promptTokens + completionEstimate)

	okReserve, qerr := h.Quota.Reserve(tenant.ID, estimated, tenant.DailyTokenQuota)
	if !okReserve || qerr != nil {
		writeTypedError(w, http.StatusTooManyRequests, "quota_exceeded", "daily token quota exceeded")
		return
	}
	tracing.AddEvent(r.Context(), "quota.reserved", attribute.Int64("quota.reserved_tokens", estimated))

	// Retrieval augmentation (chat path only). Failure is non-fatal: log
	// and continue without context docs.
	var docs []retrievalDoc
	if h.RetrievalURL != "" {
		query := lastUserMessage(msgs)
		fetched, ferr := h.fetchRetrieval(r.Context(), query, tenant.ID, r.Header.Get(traceparentHeader))
		if ferr != nil {
			log.Printf("proxy: retrieval failed for tenant %s, continuing without context docs: %v", tenant.ID, ferr)
		} else {
			docs = fetched
		}
	}

	outMsgs := msgs
	if len(docs) > 0 {
		var sb strings.Builder
		sb.WriteString("Relevant context:\n")
		for _, d := range docs {
			sb.WriteString("- ")
			sb.WriteString(d.Text)
			sb.WriteString("\n")
		}
		outMsgs = append([]chatMessage{{Role: "system", Content: sb.String()}}, msgs...)
	}

	raw["messages"] = outMsgs
	outBody, err := json.Marshal(raw)
	if err != nil {
		h.Quota.Refund(tenant.ID, estimated)
		writeTypedError(w, http.StatusInternalServerError, "internal_error", "failed to build upstream request")
		return
	}

	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target+chatPath, bytes.NewReader(outBody))
	if err != nil {
		h.Quota.Refund(tenant.ID, estimated)
		writeTypedError(w, http.StatusInternalServerError, "internal_error", "failed to build upstream request")
		return
	}
	copyForwardHeaders(upstreamReq, r, tenant.ID)

	upstreamCtx, upstreamSpan := tracing.StartUpstreamSpan(r.Context(), "llm.upstream.chat", upstreamReq)
	upstreamReq = upstreamReq.WithContext(upstreamCtx)

	resp, err := h.Client.Do(upstreamReq)
	if err != nil {
		upstreamSpan.End()
		h.Quota.Refund(tenant.ID, estimated)
		writeTypedError(w, http.StatusBadGateway, "upstream_error", "failed to reach upstream: "+err.Error())
		return
	}
	upstreamSpan.End()
	defer resp.Body.Close()

	if stream {
		h.proxyStream(w, resp, tenant.ID, estimated, promptTokens, model, chatPath, bodyBytes)
		return
	}

	h.relayNonStream(w, resp, tenant.ID, estimated, promptTokens, model, chatPath, bodyBytes)
}

// Embeddings handles POST /v1/embeddings.
func (h *Handler) Embeddings(w http.ResponseWriter, r *http.Request) {
	tenant, ok := auth.FromContext(r.Context())
	if !ok {
		writeTypedError(w, http.StatusUnauthorized, "invalid_api_key", "missing tenant context")
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, "invalid_request_error", "failed to read request body")
		return
	}

	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		writeTypedError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body")
		return
	}

	model, _ := raw["model"].(string)
	inputText, err := parseEmbeddingInput(raw["input"])
	if err != nil {
		writeTypedError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	target, rerr := policy.ResolveTarget(h.RouterCfg, tenant, model, embedPath)
	if rerr != nil {
		relayPolicyError(w, rerr)
		return
	}

	if berr := policy.EnforceBudget(h.BudgetCfg, tenant, policy.ChatBudgetRequest{
		Messages: []policy.ChatMessagePrompt{{Role: "user", Content: inputText}},
	}); berr != nil {
		relayBudgetError(w, berr)
		return
	}
	tracing.AddEvent(r.Context(), "budget.ok")

	promptTokens := tokens.CountTokens(inputText)
	estimated := int64(promptTokens)

	okReserve, qerr := h.Quota.Reserve(tenant.ID, estimated, tenant.DailyTokenQuota)
	if !okReserve || qerr != nil {
		writeTypedError(w, http.StatusTooManyRequests, "quota_exceeded", "daily token quota exceeded")
		return
	}
	tracing.AddEvent(r.Context(), "quota.reserved", attribute.Int64("quota.reserved_tokens", estimated))

	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target+embedPath, bytes.NewReader(bodyBytes))
	if err != nil {
		h.Quota.Refund(tenant.ID, estimated)
		writeTypedError(w, http.StatusInternalServerError, "internal_error", "failed to build upstream request")
		return
	}
	copyForwardHeaders(upstreamReq, r, tenant.ID)

	upstreamCtx, upstreamSpan := tracing.StartUpstreamSpan(r.Context(), "llm.upstream.embed", upstreamReq)
	upstreamReq = upstreamReq.WithContext(upstreamCtx)

	resp, err := h.Client.Do(upstreamReq)
	if err != nil {
		upstreamSpan.End()
		h.Quota.Refund(tenant.ID, estimated)
		writeTypedError(w, http.StatusBadGateway, "upstream_error", "failed to reach upstream: "+err.Error())
		return
	}
	upstreamSpan.End()
	defer resp.Body.Close()

	h.relayNonStream(w, resp, tenant.ID, estimated, promptTokens, model, embedPath, bodyBytes)
}

// relayNonStream reads the full upstream response, commits/refunds quota
// accordingly, logs a redacted record, and relays the response to the
// client.
func (h *Handler) relayNonStream(w http.ResponseWriter, resp *http.Response, tenantID string, estimated int64, promptTokens int, model, route string, reqBody []byte) {
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		h.Quota.Refund(tenantID, estimated)
		writeTypedError(w, http.StatusBadGateway, "upstream_error", "failed to read upstream response")
		return
	}

	if resp.StatusCode >= 500 {
		h.Quota.Refund(tenantID, estimated)
		relayUpstreamHeaders(w, resp)
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(respBody)
		return
	}

	actual := int64(promptTokens)
	var u struct {
		Usage usage `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &u); err == nil && u.Usage.TotalTokens > 0 {
		actual = int64(u.Usage.TotalTokens)
	} else {
		actual = estimated
	}
	_ = h.Quota.Commit(tenantID, actual)

	h.logRequest(tenantID, model, route, promptTokens, int(actual-int64(promptTokens)), resp.StatusCode, reqBody, respBody)

	relayUpstreamHeaders(w, resp)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

func (h *Handler) logRequest(tenantID, model, route string, promptTokens, completionTokens, statusCode int, reqBody, respBody []byte) {
	if h.LogStore == nil {
		return
	}
	rec := logging.Record{
		TS:               h.now(),
		TenantID:         tenantID,
		Model:            model,
		Route:            route,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		StatusCode:       statusCode,
		LatencyMS:        0,
		RequestJSON:      string(logging.Redact(reqBody, h.RedactExtraPatterns)),
		ResponseJSON:     string(logging.Redact(respBody, h.RedactExtraPatterns)),
	}
	if err := h.LogStore.Insert(rec); err != nil {
		log.Printf("proxy: failed to insert log record: %v", err)
	}
}

func relayUpstreamHeaders(w http.ResponseWriter, resp *http.Response) {
	for k, vs := range resp.Header {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
}

func relayPolicyError(w http.ResponseWriter, err error) {
	if rerr, ok := err.(*policy.RouteError); ok {
		body, jerr := rerr.JSON()
		if jerr == nil {
			writeJSONError(w, rerr.StatusCode, body)
			return
		}
	}
	writeTypedError(w, http.StatusInternalServerError, "internal_error", "policy error")
}

func relayBudgetError(w http.ResponseWriter, err error) {
	if berr, ok := err.(*policy.BudgetError); ok {
		body, jerr := berr.JSON()
		if jerr == nil {
			writeJSONError(w, berr.StatusCode, body)
			return
		}
	}
	writeTypedError(w, http.StatusBadRequest, "invalid_request_error", "budget error")
}

func copyForwardHeaders(upstreamReq *http.Request, r *http.Request, tenantID string) {
	for k, vs := range r.Header {
		if strings.EqualFold(k, "Authorization") {
			continue
		}
		for _, v := range vs {
			upstreamReq.Header.Add(k, v)
		}
	}
	upstreamReq.Header.Set(tenantIDHeader, tenantID)
	if tp := r.Header.Get(traceparentHeader); tp != "" {
		upstreamReq.Header.Set(traceparentHeader, tp)
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
}

func parseChatMessages(v any) ([]chatMessage, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("messages must be an array")
	}
	out := make([]chatMessage, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("messages must contain objects")
		}
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		name, _ := m["name"].(string)
		out = append(out, chatMessage{Role: role, Content: content, Name: name})
	}
	return out, nil
}

func parseEmbeddingInput(v any) (string, error) {
	switch val := v.(type) {
	case string:
		return val, nil
	case []any:
		var sb strings.Builder
		for _, item := range val {
			s, ok := item.(string)
			if !ok {
				return "", fmt.Errorf("input array must contain strings")
			}
			sb.WriteString(s)
			sb.WriteString("\n")
		}
		return sb.String(), nil
	default:
		return "", fmt.Errorf("input must be a string or array of strings")
	}
}

func intFromAny(v any) int {
	f, ok := v.(float64)
	if !ok {
		return 0
	}
	return int(f)
}

func lastUserMessage(msgs []chatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	if len(msgs) > 0 {
		return msgs[len(msgs)-1].Content
	}
	return ""
}

func (h *Handler) fetchRetrieval(ctx context.Context, query, tenantID, traceparent string) ([]retrievalDoc, error) {
	payload, err := json.Marshal(map[string]string{"query": query, "tenant_id": tenantID})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.RetrievalURL+"/v1/retrieval", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Inject the current trace context (honoring any client-supplied
	// traceparent already extracted onto ctx by the server middleware) so
	// retrieval-svc's own span becomes a child of this request's span.
	tracing.InjectTraceparent(ctx, req)
	if traceparent != "" && req.Header.Get(traceparentHeader) == "" {
		req.Header.Set(traceparentHeader, traceparent)
	}

	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("retrieval upstream returned status %d", resp.StatusCode)
	}

	var rr retrievalResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return nil, err
	}
	return rr.Documents, nil
}
