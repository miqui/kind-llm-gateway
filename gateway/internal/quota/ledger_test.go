package quota

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReserveCommitRefund(t *testing.T) {
	l := NewLedger()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	l.SetClock(func() time.Time { return now })

	ok, err := l.Reserve("team-a", 100, 1000)
	if !ok || err != nil {
		t.Fatalf("expected reserve to succeed, ok=%v err=%v", ok, err)
	}

	if err := l.Commit("team-a", 80); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	used := l.Used("team-a")
	if used != 80 {
		t.Fatalf("expected used=80 after commit, got %d", used)
	}
}

func TestRefundReleasesReservation(t *testing.T) {
	l := NewLedger()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	l.SetClock(func() time.Time { return now })

	ok, err := l.Reserve("team-a", 500, 1000)
	if !ok || err != nil {
		t.Fatalf("expected reserve to succeed")
	}
	if err := l.Refund("team-a", 500); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if used := l.Used("team-a"); used != 0 {
		t.Fatalf("expected used=0 after refund, got %d", used)
	}

	// Should be able to reserve full quota again.
	ok, err = l.Reserve("team-a", 1000, 1000)
	if !ok || err != nil {
		t.Fatalf("expected reserve of full quota to succeed after refund, ok=%v err=%v", ok, err)
	}
}

func TestExhaustionReturnsQuotaExceeded(t *testing.T) {
	l := NewLedger()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	l.SetClock(func() time.Time { return now })

	ok, err := l.Reserve("team-a", 1000, 1000)
	if !ok || err != nil {
		t.Fatalf("expected first reserve to succeed")
	}

	ok, err = l.Reserve("team-a", 1, 1000)
	if ok {
		t.Fatalf("expected second reserve to fail (quota exhausted)")
	}
	if err != ErrQuotaExceeded {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
}

func TestMidnightUTCRollover(t *testing.T) {
	l := NewLedger()
	now := time.Date(2026, 9, 9, 23, 59, 0, 0, time.UTC)
	l.SetClock(func() time.Time { return now })

	ok, err := l.Reserve("team-a", 1000, 1000)
	if !ok || err != nil {
		t.Fatalf("expected reserve to succeed")
	}
	ok, _ = l.Reserve("team-a", 1, 1000)
	if ok {
		t.Fatalf("expected exhausted before rollover")
	}

	// Cross UTC midnight.
	now = time.Date(2026, 9, 10, 0, 1, 0, 0, time.UTC)
	ok, err = l.Reserve("team-a", 1000, 1000)
	if !ok || err != nil {
		t.Fatalf("expected reserve to succeed after UTC midnight rollover, ok=%v err=%v", ok, err)
	}
}

func TestMiddleware429OnQuotaExceeded(t *testing.T) {
	l := NewLedger()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	l.SetClock(func() time.Time { return now })

	resolve := func(r *http.Request) (tenantID string, estimatedTokens int64, dailyQuota int64) {
		return "team-a", 600, 1000
	}
	handler := Middleware(l, resolve)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected first request 200, got %d", rr.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr2.Code)
	}
	if want := `"type":"quota_exceeded"`; !contains(rr2.Body.String(), want) {
		t.Fatalf("expected body to contain %q, got %q", want, rr2.Body.String())
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
