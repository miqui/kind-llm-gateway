// Package ratelimit implements per-tenant token-bucket rate limiting.
package ratelimit

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// clockFunc returns the current time; overridable in tests.
type clockFunc func() time.Time

// Limiter holds one token bucket per tenant, keyed by tenant ID. Buckets are
// created lazily on first use for a given rps.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*limiterEntry
	clock   clockFunc
}

type limiterEntry struct {
	limiter *rate.Limiter
	rps     float64
}

// NewLimiter constructs an empty Limiter using the real wall clock.
func NewLimiter() *Limiter {
	return &Limiter{
		buckets: make(map[string]*limiterEntry),
		clock:   time.Now,
	}
}

// SetClock overrides the clock used for token refill calculations, for
// deterministic tests.
func (l *Limiter) SetClock(c clockFunc) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.clock = c
}

// Allow reports whether a request for tenantID is permitted right now,
// given the tenant's configured requests-per-second. Burst is 2x rps.
func (l *Limiter) Allow(tenantID string, rps float64) bool {
	l.mu.Lock()
	entry, ok := l.buckets[tenantID]
	if !ok || entry.rps != rps {
		burst := int(rps * 2)
		if burst < 1 {
			burst = 1
		}
		lim := rate.NewLimiter(rate.Limit(rps), burst)
		entry = &limiterEntry{limiter: lim, rps: rps}
		l.buckets[tenantID] = entry
	}
	clock := l.clock
	l.mu.Unlock()

	return entry.limiter.AllowN(clock(), 1)
}

// RetryAfterSeconds returns a conservative Retry-After value for a tenant
// whose bucket is currently exhausted, in whole seconds (minimum 1).
func (l *Limiter) RetryAfterSeconds(tenantID string, rps float64) int {
	if rps <= 0 {
		return 1
	}
	secs := int(1.0/rps + 0.999)
	if secs < 1 {
		secs = 1
	}
	return secs
}

// TenantResolver extracts a tenant ID and its configured rps from a request.
type TenantResolver func(r *http.Request) (tenantID string, rps float64)

// Middleware returns an http middleware enforcing per-tenant rate limits,
// responding 429 with a Retry-After header when exceeded.
func Middleware(l *Limiter, resolve TenantResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenantID, rps := resolve(r)
			if !l.Allow(tenantID, rps) {
				w.Header().Set("Retry-After", strconv.Itoa(l.RetryAfterSeconds(tenantID, rps)))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"type":    "rate_limit_exceeded",
						"message": "rate limit exceeded, slow down",
					},
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
