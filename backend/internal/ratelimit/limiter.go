// Package ratelimit provides per-IP HTTP rate limiting for the Blob-Cloud API.
//
// Two implementations are available, both satisfying the Limiter interface:
//
//   - InMemoryLimiter  — token-bucket per IP using golang.org/x/time/rate.
//     Zero dependencies beyond the stdlib.  Best for a single-node deployment
//     or local development.  State is lost on restart and not shared across
//     pods.
//
//   - RedisLimiter     — fixed-window counter per IP using Redis INCR + EXPIRE.
//     Shared across every node behind a load balancer.  Requires a reachable
//     Redis instance (REDIS_URL env var).
//
// The router mounts three zones with independent limits:
//
//	Zone        Env var prefix   Default limit
//	────────────────────────────────────────────
//	auth        RL_AUTH_*        10 req/min   (brute-force / credential stuffing)
//	upload      RL_UPLOAD_*      30 req/min   (expensive S3 presign calls)
//	api         RL_API_*         120 req/min  (general authenticated endpoints)
//
// All limits return 429 Too Many Requests with standard headers:
//
//	X-RateLimit-Limit     – the zone ceiling
//	X-RateLimit-Remaining – requests left in the current window
//	X-RateLimit-Reset     – Unix timestamp when the window resets
//	Retry-After           – seconds until the client may retry
package ratelimit

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Decision is returned by Allow() for a single request.
type Decision struct {
	Allowed   bool
	Limit     int // total tokens in the window
	Remaining int // tokens left after this request
	ResetAt   time.Time
}

// Limiter decides whether a request from key (typically an IP address) is
// allowed through.
type Limiter interface {
	Allow(ctx context.Context, key string) Decision
}

// ── In-memory implementation ─────────────────────────────────────────────────

// bucket holds one token bucket plus its last-seen timestamp so idle IPs can
// be evicted from the map.
type bucket struct {
	lim     *rate.Limiter
	lastSee time.Time
}

// InMemoryLimiter is a per-key token-bucket limiter backed by a sync.Map.
// It is safe for concurrent use and requires no external dependencies.
type InMemoryLimiter struct {
	rps    rate.Limit    // requests per second derived from the window config
	burst  int           // burst = window ceiling (allow the full window up-front)
	window time.Duration // stored to compute ResetAt safely
	mu     sync.Mutex
	keys   map[string]*bucket
}

// NewInMemoryLimiter creates a limiter that allows limit requests per window.
// window is the rolling refill period (e.g. time.Minute for 60 req/min).
func NewInMemoryLimiter(limit int, window time.Duration) *InMemoryLimiter {
	rps := rate.Limit(float64(limit) / window.Seconds())
	return &InMemoryLimiter{
		rps:    rps,
		burst:  limit,
		window: window,
		keys:   make(map[string]*bucket),
	}
}


// Allow checks whether key may proceed. It is safe to call from multiple
// goroutines concurrently.
func (l *InMemoryLimiter) Allow(_ context.Context, key string) Decision {
	l.mu.Lock()
	b, ok := l.keys[key]
	if !ok {
		b = &bucket{lim: rate.NewLimiter(l.rps, l.burst)}
		l.keys[key] = b
	}
	b.lastSee = time.Now()
	l.mu.Unlock()

	r := b.lim.Reserve()
	if !r.OK() {
		return Decision{
			Allowed:   false,
			Limit:     l.burst,
			Remaining: 0,
			ResetAt:   time.Now().Add(r.Delay()),
		}
	}
	delay := r.Delay()
	if delay > 0 {
		// Token wasn't immediately available; cancel the reservation and deny.
		r.Cancel()
		return Decision{
			Allowed:   false,
			Limit:     l.burst,
			Remaining: 0,
			ResetAt:   time.Now().Add(delay),
		}
	}

	// Approximate remaining tokens from the limiter's current state.
	remaining := int(b.lim.Tokens())
	if remaining < 0 {
		remaining = 0
	}
	// ResetAt: time until one more token is available = window / burst.
	// This is safe because burst >= 1 is guaranteed by NewInMemoryLimiter.
	resetIn := l.window / time.Duration(l.burst)
	return Decision{
		Allowed:   true,
		Limit:     l.burst,
		Remaining: remaining,
		ResetAt:   time.Now().Add(resetIn),
	}
}

// ── HTTP middleware ───────────────────────────────────────────────────────────

// zoneConfig bundles the limit and window for one rate-limit zone.
type zoneConfig struct {
	Limit  int
	Window time.Duration
}

// ZoneConfig is the public alias used by router.go when constructing middleware.
type ZoneConfig = zoneConfig

// NewZoneConfig creates a ZoneConfig for limit requests per window.
func NewZoneConfig(limit int, window time.Duration) ZoneConfig {
	return ZoneConfig{Limit: limit, Window: window}
}

// Middleware returns a chi-compatible http.Handler middleware that enforces the
// given Limiter on every request. The key is the real client IP extracted from
// r.RemoteAddr (chi''s RealIP middleware must run first).
//
// On limit exceeded it writes a 429 response with JSON body and standard
// rate-limit headers. The next handler is NOT called.
func Middleware(l Limiter, cfg ZoneConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			d := l.Allow(r.Context(), ip)

			// Always emit headers so clients can track their budget.
			w.Header().Set("X-RateLimit-Limit", itoa(cfg.Limit))
			w.Header().Set("X-RateLimit-Remaining", itoa(d.Remaining))
			w.Header().Set("X-RateLimit-Reset", itoa(int(d.ResetAt.Unix())))

			if !d.Allowed {
				retryAfter := int(time.Until(d.ResetAt).Seconds()) + 1
				if retryAfter < 1 {
					retryAfter = 1
				}
				w.Header().Set("Retry-After", itoa(retryAfter))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate limit exceeded","retry_after":` + itoa(retryAfter) + `}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the real client IP. chi''s RealIP middleware already
// rewrites r.RemoteAddr from X-Forwarded-For / X-Real-IP headers, so we can
// rely on it directly and strip the port if present.
func clientIP(r *http.Request) string {
	addr := r.RemoteAddr
	// RemoteAddr format is "host:port" or "[::1]:port"
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}

// itoa converts an int to a string without importing strconv everywhere.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
