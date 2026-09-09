package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/miqui/kind-llm-gateway/gateway/internal/tenants"
)

type fakeLookup struct {
	byKey map[string]tenants.Tenant
}

func (f fakeLookup) GetByAPIKey(key string) (tenants.Tenant, error) {
	t, ok := f.byKey[key]
	if !ok {
		return tenants.Tenant{}, tenants.ErrNotFound
	}
	return t, nil
}

func newHandler(t *testing.T, lookup TenantLookup) http.Handler {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tn, ok := FromContext(r.Context())
		if !ok {
			t.Fatalf("expected tenant in context")
		}
		w.Header().Set("X-Tenant-ID", tn.ID)
		w.WriteHeader(http.StatusOK)
	})
	return Middleware(lookup)(next)
}

func TestMissingAuthHeader(t *testing.T) {
	h := newHandler401(t, fakeLookup{byKey: map[string]tenants.Tenant{}})
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	assert401(t, rr)
}

func TestUnknownAPIKey(t *testing.T) {
	h := newHandler401(t, fakeLookup{byKey: map[string]tenants.Tenant{}})
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-unknown")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	assert401(t, rr)
}

func TestValidAPIKey(t *testing.T) {
	lookup := fakeLookup{byKey: map[string]tenants.Tenant{
		"sk-team-a-0000000000000000": {ID: "team-a", Name: "Team A", APIKey: "sk-team-a-0000000000000000"},
	}}
	h := newHandler(t, lookup)
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-team-a-0000000000000000")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Tenant-ID"); got != "team-a" {
		t.Fatalf("expected tenant id team-a in context, got %q", got)
	}
}

func newHandler401(t *testing.T, lookup TenantLookup) http.Handler {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("next handler should not be called on auth failure")
	})
	return Middleware(lookup)(next)
}

func assert401(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	body := rr.Body.String()
	if want := `"type":"invalid_api_key"`; !contains(body, want) {
		t.Fatalf("expected body to contain %q, got %q", want, body)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}
