package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAllowWithinBurst(t *testing.T) {
	l := NewLimiter()
	l.SetClock(func() time.Time { return time.Unix(0, 0) })

	// rps=1 -> burst=2, so first two requests within the same instant
	// should be allowed.
	for i := 0; i < 2; i++ {
		if !l.Allow("team-a", 1) {
			t.Fatalf("expected request %d to be allowed", i)
		}
	}
	if l.Allow("team-a", 1) {
		t.Fatalf("expected 3rd request to exceed burst")
	}
}

func TestRefillOverTime(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiter()
	l.SetClock(func() time.Time { return now })

	for i := 0; i < 2; i++ {
		if !l.Allow("team-a", 1) {
			t.Fatalf("expected request %d to be allowed", i)
		}
	}
	if l.Allow("team-a", 1) {
		t.Fatalf("expected burst to be exhausted")
	}

	now = now.Add(1500 * time.Millisecond) // 1.5 tokens refilled at rps=1
	if !l.Allow("team-a", 1) {
		t.Fatalf("expected token to have refilled after 1.5s")
	}
}

func TestMiddlewareReturns429WithRetryAfter(t *testing.T) {
	l := NewLimiter()
	l.SetClock(func() time.Time { return time.Unix(0, 0) })

	handler := Middleware(l, func(r *http.Request) (string, float64) {
		return "team-a", 1
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, rr.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatalf("expected Retry-After header to be set")
	}
}
