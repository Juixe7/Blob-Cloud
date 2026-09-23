package ratelimit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-drive-clone/internal/ratelimit"
)

// ── InMemoryLimiter unit tests ────────────────────────────────────────────────

// TestInMemoryLimiter_AllowsUpToLimit verifies that exactly `limit` requests
// are allowed in a window, and the next one is denied.
func TestInMemoryLimiter_AllowsUpToLimit(t *testing.T) {
	const limit = 5
	l := ratelimit.NewInMemoryLimiter(limit, time.Minute)
	ctx := context.Background()

	for i := 0; i < limit; i++ {
		d := l.Allow(ctx, "1.2.3.4")
		if !d.Allowed {
			t.Fatalf("request %d: expected allowed, got denied", i+1)
		}
		if d.Limit != limit {
			t.Errorf("request %d: Limit want %d, got %d", i+1, limit, d.Limit)
		}
	}

	// The (limit+1)-th request must be denied.
	d := l.Allow(ctx, "1.2.3.4")
	if d.Allowed {
		t.Fatal("expected request beyond limit to be denied")
	}
	if d.Remaining != 0 {
		t.Errorf("Remaining after exhaustion: want 0, got %d", d.Remaining)
	}
}

// TestInMemoryLimiter_IsolatesKeys verifies that two different IPs have
// independent token buckets — exhausting one does not affect the other.
func TestInMemoryLimiter_IsolatesKeys(t *testing.T) {
	l := ratelimit.NewInMemoryLimiter(2, time.Minute)
	ctx := context.Background()

	// Exhaust IP A.
	l.Allow(ctx, "10.0.0.1")
	l.Allow(ctx, "10.0.0.1")
	dA := l.Allow(ctx, "10.0.0.1")
	if dA.Allowed {
		t.Fatal("IP A: expected deny after burst exhaustion")
	}

	// IP B must still be allowed (its bucket is independent).
	dB := l.Allow(ctx, "10.0.0.2")
	if !dB.Allowed {
		t.Fatal("IP B: expected allow (independent bucket from IP A)")
	}
}

// TestInMemoryLimiter_LimitAndRemainingHeaders verifies the Decision fields
// used to populate X-RateLimit-* headers.
func TestInMemoryLimiter_LimitAndRemainingHeaders(t *testing.T) {
	l := ratelimit.NewInMemoryLimiter(10, time.Minute)
	ctx := context.Background()

	d := l.Allow(ctx, "5.5.5.5")
	if !d.Allowed {
		t.Fatal("first request should be allowed")
	}
	if d.Limit != 10 {
		t.Errorf("Limit: want 10, got %d", d.Limit)
	}
	if d.ResetAt.IsZero() {
		t.Error("ResetAt must not be zero")
	}
}

// ── Middleware integration tests ──────────────────────────────────────────────

// TestMiddleware_PassesAllowedRequests verifies that allowed requests reach the
// downstream handler unchanged.
func TestMiddleware_PassesAllowedRequests(t *testing.T) {
	l := ratelimit.NewInMemoryLimiter(100, time.Minute)
	cfg := ratelimit.NewZoneConfig(100, time.Minute)
	mw := ratelimit.Middleware(l, cfg)

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/files/", nil)
	req.RemoteAddr = "192.168.1.1:55001"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Fatal("downstream handler was not called on an allowed request")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
	// Rate-limit headers must be present.
	if w.Header().Get("X-RateLimit-Limit") == "" {
		t.Error("X-RateLimit-Limit header missing")
	}
}

// TestMiddleware_Returns429OnExhaustion verifies the 429 response shape:
// status code, Retry-After header, and JSON body.
func TestMiddleware_Returns429OnExhaustion(t *testing.T) {
	// Limit of 1 so we can exhaust on the second request easily.
	l := ratelimit.NewInMemoryLimiter(1, time.Minute)
	cfg := ratelimit.NewZoneConfig(1, time.Minute)
	mw := ratelimit.Middleware(l, cfg)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	fire := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		req.RemoteAddr = "1.1.1.1:40000"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}

	// First request: allowed.
	if r := fire(); r.Code != http.StatusOK {
		t.Fatalf("first request: want 200, got %d", r.Code)
	}
	// Second request: denied.
	r := fire()
	if r.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: want 429, got %d", r.Code)
	}
	if r.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header missing on 429")
	}
	body := r.Body.String()
	if body == "" {
		t.Error("429 response must include a JSON body")
	}
	if r.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type: want application/json, got %q", r.Header().Get("Content-Type"))
	}
}

// TestMiddleware_DifferentZonesIndependent verifies that wrapping the same
// handler with two middleware instances (simulating separate route-group
// limits) keeps their states independent.
func TestMiddleware_DifferentZonesIndependent(t *testing.T) {
	authLimiter := ratelimit.NewInMemoryLimiter(2, time.Minute)
	apiLimiter := ratelimit.NewInMemoryLimiter(100, time.Minute)

	authMW := ratelimit.Middleware(authLimiter, ratelimit.NewZoneConfig(2, time.Minute))
	apiMW := ratelimit.Middleware(apiLimiter, ratelimit.NewZoneConfig(100, time.Minute))

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	authH := authMW(ok)
	apiH := apiMW(ok)

	fire := func(h http.Handler, path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "8.8.8.8:12345"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	// Exhaust auth zone.
	fire(authH, "/api/auth/login")
	fire(authH, "/api/auth/login")
	if got := fire(authH, "/api/auth/login"); got != http.StatusTooManyRequests {
		t.Errorf("auth zone exhausted: want 429, got %d", got)
	}
	// API zone not affected.
	if got := fire(apiH, "/api/files/"); got != http.StatusOK {
		t.Errorf("api zone: want 200, got %d", got)
	}
}

// TestMiddleware_IPIsolation verifies that two clients from different IPs do
// not share a rate-limit bucket through the middleware layer.
func TestMiddleware_IPIsolation(t *testing.T) {
	l := ratelimit.NewInMemoryLimiter(1, time.Minute)
	cfg := ratelimit.NewZoneConfig(1, time.Minute)
	mw := ratelimit.Middleware(l, cfg)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := mw(ok)

	fire := func(ip string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		req.RemoteAddr = ip + ":9999"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	// Exhaust bucket for 1.1.1.1.
	fire("1.1.1.1")
	if got := fire("1.1.1.1"); got != http.StatusTooManyRequests {
		t.Fatalf("1.1.1.1 after exhaust: want 429, got %d", got)
	}
	// 2.2.2.2 has a fresh bucket.
	if got := fire("2.2.2.2"); got != http.StatusOK {
		t.Fatalf("2.2.2.2 first request: want 200, got %d", got)
	}
}
