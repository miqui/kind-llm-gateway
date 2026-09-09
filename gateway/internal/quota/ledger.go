// Package quota implements per-tenant daily UTC token-quota ledgers.
package quota

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

// ErrQuotaExceeded is returned when a reservation would exceed the tenant's
// daily token quota.
var ErrQuotaExceeded = errors.New("daily token quota exceeded")

type clockFunc func() time.Time

type tenantState struct {
	date    string  // YYYY-MM-DD in UTC
	used    int64   // committed + currently-reserved tokens for the day
	pending []int64 // FIFO of outstanding reservation amounts awaiting Commit/Refund
}

// Ledger tracks per-tenant daily token usage with UTC-midnight rollover.
type Ledger struct {
	mu    sync.Mutex
	state map[string]*tenantState
	clock clockFunc
}

// NewLedger constructs an empty Ledger using the real wall clock.
func NewLedger() *Ledger {
	return &Ledger{
		state: make(map[string]*tenantState),
		clock: time.Now,
	}
}

// SetClock overrides the clock used for UTC day computation, for
// deterministic tests.
func (l *Ledger) SetClock(c clockFunc) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.clock = c
}

func utcDate(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// getState returns the tenant's state, resetting it if the UTC day has
// rolled over. Caller must hold l.mu.
func (l *Ledger) getState(tenantID string) *tenantState {
	today := utcDate(l.clock())
	st, ok := l.state[tenantID]
	if !ok || st.date != today {
		st = &tenantState{date: today}
		l.state[tenantID] = st
	}
	return st
}

// Reserve attempts to pre-allocate n tokens against the tenant's dailyQuota
// for the current UTC day. Returns (true, nil) on success, or
// (false, ErrQuotaExceeded) if the reservation would exceed quota.
func (l *Ledger) Reserve(tenantID string, n int64, dailyQuota int64) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	st := l.getState(tenantID)
	if st.used+n > dailyQuota {
		return false, ErrQuotaExceeded
	}
	st.used += n
	st.pending = append(st.pending, n)
	return true, nil
}

// Commit adjusts the oldest outstanding reservation to the actual token
// usage: the reserved estimate is removed from the running total and the
// actual amount is added in its place. This lets Reserve use a conservative
// pre-flight estimate while the ledger ends up reflecting real usage.
func (l *Ledger) Commit(tenantID string, actual int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.getState(tenantID)
	if len(st.pending) > 0 {
		reserved := st.pending[0]
		st.pending = st.pending[1:]
		st.used -= reserved
	}
	st.used += actual
	if st.used < 0 {
		st.used = 0
	}
	return nil
}

// Refund releases n previously reserved tokens back to the tenant's daily
// budget (e.g. after an upstream failure where no tokens were actually
// consumed).
func (l *Ledger) Refund(tenantID string, n int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	st := l.getState(tenantID)
	if len(st.pending) > 0 {
		reserved := st.pending[0]
		st.pending = st.pending[1:]
		st.used -= reserved
	} else {
		st.used -= n
	}
	if st.used < 0 {
		st.used = 0
	}
	return nil
}

// Used returns the tenant's committed+reserved token usage for the current
// UTC day.
func (l *Ledger) Used(tenantID string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.getState(tenantID)
	return st.used
}

// TenantQuotaResolver extracts a tenant ID, estimated token count, and daily
// quota from a request.
type TenantQuotaResolver func(r *http.Request) (tenantID string, estimatedTokens int64, dailyQuota int64)

// Middleware returns an http middleware enforcing per-tenant daily token
// quotas, responding 429 with error type "quota_exceeded" when exhausted.
func Middleware(l *Ledger, resolve TenantQuotaResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenantID, estimated, dailyQuota := resolve(r)
			ok, err := l.Reserve(tenantID, estimated, dailyQuota)
			if !ok || err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"type":    "quota_exceeded",
						"message": "daily token quota exceeded",
					},
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
