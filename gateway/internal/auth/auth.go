// Package auth implements API-key authentication middleware for llmgw.
package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/miqui/kind-llm-gateway/gateway/internal/tenants"
)

// TenantLookup resolves a tenant by API key. tenants.Store satisfies this.
type TenantLookup interface {
	GetByAPIKey(key string) (tenants.Tenant, error)
}

type contextKey int

const tenantContextKey contextKey = iota

// FromContext extracts the authenticated tenant from the request context.
func FromContext(ctx context.Context) (tenants.Tenant, bool) {
	t, ok := ctx.Value(tenantContextKey).(tenants.Tenant)
	return t, ok
}

func withTenant(ctx context.Context, t tenants.Tenant) context.Context {
	return context.WithValue(ctx, tenantContextKey, t)
}

// Middleware returns an http middleware that authenticates requests via a
// Bearer API key against the given TenantLookup, injecting the resolved
// tenant into the request context on success.
func Middleware(lookup TenantLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, ok := extractBearer(r.Header.Get("Authorization"))
			if !ok {
				writeUnauthorized(w)
				return
			}

			tenant, err := lookup.GetByAPIKey(key)
			if err != nil {
				writeUnauthorized(w)
				return
			}

			// Constant-time re-verification of the resolved key, defending
			// against any timing side channel in the lookup path itself.
			if subtle.ConstantTimeCompare([]byte(tenant.APIKey), []byte(key)) != 1 {
				writeUnauthorized(w)
				return
			}

			ctx := withTenant(r.Context(), tenant)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if header == "" || !strings.HasPrefix(header, prefix) {
		return "", false
	}
	key := strings.TrimPrefix(header, prefix)
	if key == "" {
		return "", false
	}
	return key, true
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"type":    "invalid_api_key",
			"message": "missing or invalid API key",
		},
	})
}
