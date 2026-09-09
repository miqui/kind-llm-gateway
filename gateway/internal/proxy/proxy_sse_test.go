package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miqui/kind-llm-gateway/gateway/internal/auth"
	"github.com/miqui/kind-llm-gateway/gateway/internal/logging"
	"github.com/miqui/kind-llm-gateway/gateway/internal/policy"
	"github.com/miqui/kind-llm-gateway/gateway/internal/quota"
	"github.com/miqui/kind-llm-gateway/gateway/internal/tenants"
)

func testTenant() tenants.Tenant {
	return tenants.Tenant{
		ID:              "team-a",
		Name:            "Team A",
		APIKey:          "sk-test-key",
		RateLimitRPS:    100,
		DailyTokenQuota: 1_000_000,
		AllowedModels:   []string{"qwen-chat", "nomic-embed"},
		CostTier:        "standard",
	}
}

func testRouterCfg(chatURL, embedURL string) policy.RouterConfig {
	return policy.RouterConfig{
		ChatUpstreamURL:  chatURL,
		EmbedUpstreamURL: embedURL,
		Models: map[string]string{
			"qwen-chat":   "chat",
			"nomic-embed": "embed",
		},
	}
}

func newTestHandler(t *testing.T, chatURL, embedURL, retrievalURL string) *Handler {
	t.Helper()
	store, err := logging.Open(":memory:", 1000)
	if err != nil {
		t.Fatalf("logging.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	return NewHandler(Handler{
		RouterCfg:    testRouterCfg(chatURL, embedURL),
		BudgetCfg:    policy.BudgetConfig{MaxPromptTokens: 4096, MaxCompletionTokens: 1024},
		Quota:        quota.NewLedger(),
		LogStore:     store,
		RetrievalURL: retrievalURL,
	})
}

// fakeLookup implements auth.TenantLookup for a single fixed tenant.
type fakeLookup struct {
	tenant tenants.Tenant
}

func (f fakeLookup) GetByAPIKey(key string) (tenants.Tenant, error) {
	if key != f.tenant.APIKey {
		return tenants.Tenant{}, fmt.Errorf("not found")
	}
	return f.tenant, nil
}

func wrapWithAuth(h http.HandlerFunc, tenant tenants.Tenant) http.Handler {
	mw := auth.Middleware(fakeLookup{tenant: tenant})
	return mw(h)
}

func doRequest(t *testing.T, handler http.Handler, method, path string, body []byte, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestChatCompletionsNonStreamCommitsUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != chatPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	defer upstream.Close()

	tenant := testTenant()
	h := newTestHandler(t, upstream.URL, "http://unused", "")
	handler := wrapWithAuth(h.ChatCompletions, tenant)

	reqBody := []byte(`{"model":"qwen-chat","messages":[{"role":"user","content":"hello"}]}`)
	rr := doRequest(t, handler, http.MethodPost, chatPath, reqBody, tenant.APIKey)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if used := h.Quota.Used(tenant.ID); used != 15 {
		t.Fatalf("expected quota used=15, got %d", used)
	}
}

func TestChatCompletionsUpstream500Refunds(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer upstream.Close()

	tenant := testTenant()
	h := newTestHandler(t, upstream.URL, "http://unused", "")
	handler := wrapWithAuth(h.ChatCompletions, tenant)

	reqBody := []byte(`{"model":"qwen-chat","messages":[{"role":"user","content":"hello"}]}`)
	rr := doRequest(t, handler, http.MethodPost, chatPath, reqBody, tenant.APIKey)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 relayed, got %d", rr.Code)
	}
	if got := rr.Body.String(); got != `{"error":"boom"}` {
		t.Fatalf("expected upstream body relayed as-is, got %q", got)
	}
	if used := h.Quota.Used(tenant.ID); used != 0 {
		t.Fatalf("expected quota refunded to 0, got %d", used)
	}
}

func TestChatCompletionsRetrievalFailureNonFatal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"total_tokens":5}}`))
	}))
	defer upstream.Close()

	tenant := testTenant()
	// Retrieval URL points nowhere -> connection refused, should not fail
	// the chat request.
	h := newTestHandler(t, upstream.URL, "http://unused", "http://127.0.0.1:1")
	handler := wrapWithAuth(h.ChatCompletions, tenant)

	reqBody := []byte(`{"model":"qwen-chat","messages":[{"role":"user","content":"hello"}]}`)
	rr := doRequest(t, handler, http.MethodPost, chatPath, reqBody, tenant.APIKey)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 despite retrieval failure, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestChatCompletionsIncludesRetrievedDocsAsSystemMessage(t *testing.T) {
	retrieval := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"documents":[{"id":"doc-1","text":"the sky is blue"}]}`))
	}))
	defer retrieval.Close()

	var capturedReq map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedReq)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"total_tokens":5}}`))
	}))
	defer upstream.Close()

	tenant := testTenant()
	h := newTestHandler(t, upstream.URL, "http://unused", retrieval.URL)
	handler := wrapWithAuth(h.ChatCompletions, tenant)

	reqBody := []byte(`{"model":"qwen-chat","messages":[{"role":"user","content":"what color is the sky"}]}`)
	rr := doRequest(t, handler, http.MethodPost, chatPath, reqBody, tenant.APIKey)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	msgs, ok := capturedReq["messages"].([]any)
	if !ok || len(msgs) < 2 {
		t.Fatalf("expected retrieved docs prefixed as system message, got: %+v", capturedReq)
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("expected first message role=system, got %+v", first)
	}
	if !strings.Contains(fmt.Sprint(first["content"]), "the sky is blue") {
		t.Fatalf("expected retrieved doc text in system message, got %+v", first["content"])
	}
}

func TestChatCompletionsBudgetExceededReturns400BeforeUpstream(t *testing.T) {
	called := int32(0)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tenant := testTenant()
	h := newTestHandler(t, upstream.URL, "http://unused", "")
	h.BudgetCfg = policy.BudgetConfig{MaxPromptTokens: 1, MaxCompletionTokens: 1024}
	handler := wrapWithAuth(h.ChatCompletions, tenant)

	reqBody := []byte(`{"model":"qwen-chat","messages":[{"role":"user","content":"this prompt is definitely longer than one token"}]}`)
	rr := doRequest(t, handler, http.MethodPost, chatPath, reqBody, tenant.APIKey)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if atomic.LoadInt32(&called) != 0 {
		t.Fatalf("expected upstream never called on budget rejection")
	}
}

func TestChatCompletionsModelNotAllowedReturns403(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	tenant := testTenant()
	tenant.AllowedModels = []string{"other-model"}
	h := newTestHandler(t, upstream.URL, "http://unused", "")
	handler := wrapWithAuth(h.ChatCompletions, tenant)

	reqBody := []byte(`{"model":"qwen-chat","messages":[{"role":"user","content":"hi"}]}`)
	rr := doRequest(t, handler, http.MethodPost, chatPath, reqBody, tenant.APIKey)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestEmbeddingsHappyPathCommitsUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != embedPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":3,"total_tokens":3}}`))
	}))
	defer upstream.Close()

	tenant := testTenant()
	h := newTestHandler(t, "http://unused", upstream.URL, "")
	handler := wrapWithAuth(h.Embeddings, tenant)

	reqBody := []byte(`{"model":"nomic-embed","input":"hello world"}`)
	rr := doRequest(t, handler, http.MethodPost, embedPath, reqBody, tenant.APIKey)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if used := h.Quota.Used(tenant.ID); used != 3 {
		t.Fatalf("expected quota used=3, got %d", used)
	}
}

// --- SSE streaming tests ---

func sseWrite(w http.ResponseWriter, flusher http.Flusher, data string) {
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func TestChatCompletionsStreamRelaysEventsInOrderWithFlush(t *testing.T) {
	events := []string{
		`{"choices":[{"delta":{"content":"Hel"}}]}`,
		`{"choices":[{"delta":{"content":"lo"}}]}`,
		`{"choices":[{"delta":{}}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}`,
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, e := range events {
			sseWrite(w, flusher, e)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	tenant := testTenant()
	h := newTestHandler(t, upstream.URL, "http://unused", "")
	handler := wrapWithAuth(h.ChatCompletions, tenant)

	reqBody := []byte(`{"model":"qwen-chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, chatPath, bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+tenant.APIKey)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	body := rr.Body.String()
	idx0 := strings.Index(body, events[0])
	idx1 := strings.Index(body, events[1])
	idx2 := strings.Index(body, events[2])
	if idx0 < 0 || idx1 < 0 || idx2 < 0 || !(idx0 < idx1 && idx1 < idx2) {
		t.Fatalf("expected events relayed in order, got body: %s", body)
	}

	if used := h.Quota.Used(tenant.ID); used != 10 {
		t.Fatalf("expected quota used=10 (from final chunk usage), got %d", used)
	}
}

func TestChatCompletionsStreamEstimatesUsageWhenNoFinalUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseWrite(w, flusher, `{"choices":[{"delta":{"content":"Hello there"}}]}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	tenant := testTenant()
	h := newTestHandler(t, upstream.URL, "http://unused", "")
	handler := wrapWithAuth(h.ChatCompletions, tenant)

	reqBody := []byte(`{"model":"qwen-chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	rr := doRequest(t, handler, http.MethodPost, chatPath, reqBody, tenant.APIKey)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if used := h.Quota.Used(tenant.ID); used <= 0 {
		t.Fatalf("expected estimated usage committed, got %d", used)
	}
}

func TestChatCompletionsStreamUpstream500Refunds(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer upstream.Close()

	tenant := testTenant()
	h := newTestHandler(t, upstream.URL, "http://unused", "")
	handler := wrapWithAuth(h.ChatCompletions, tenant)

	reqBody := []byte(`{"model":"qwen-chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	rr := doRequest(t, handler, http.MethodPost, chatPath, reqBody, tenant.APIKey)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
	if used := h.Quota.Used(tenant.ID); used != 0 {
		t.Fatalf("expected refund to 0, got %d", used)
	}
}

// TestClientDisconnectCancelsUpstream verifies that canceling the incoming
// request's context (simulating client disconnect) propagates to the
// upstream HTTP request, per http.NewRequestWithContext semantics.
func TestClientDisconnectCancelsUpstream(t *testing.T) {
	upstreamCanceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseWrite(w, flusher, `{"choices":[{"delta":{"content":"hi"}}]}`)
		// Block until the request context is canceled (client
		// disconnected / test canceled it) or time out the test.
		select {
		case <-r.Context().Done():
			close(upstreamCanceled)
		case <-time.After(5 * time.Second):
		}
	}))
	defer upstream.Close()

	tenant := testTenant()
	h := newTestHandler(t, upstream.URL, "http://unused", "")
	handler := wrapWithAuth(h.ChatCompletions, tenant)

	reqBody := []byte(`{"model":"qwen-chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, chatPath, bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+tenant.APIKey)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rr, req)
		close(done)
	}()

	// Give the handler a moment to reach the streaming read loop, then
	// cancel to simulate client disconnect.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-upstreamCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("expected upstream request context to be canceled")
	}
	<-done
}

// small helper used above via httptest.NewRequestWithContext-style call;
// bufio import kept for potential future raw SSE parsing assertions.
var _ = bufio.NewReader
