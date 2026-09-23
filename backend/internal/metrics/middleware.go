package metrics

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// PrometheusMiddleware records blobcloud_http_request_duration_seconds and
// blobcloud_http_requests_total for every request. It uses chi.RouteContext to
// resolve the parameterised route pattern (e.g. /api/files/{id}) rather than
// the raw URL, which keeps metric cardinality bounded.
//
// Uses middleware.NewWrapResponseWriter so that downstream handlers (e.g.
// WebSocket upgrades and SSE streaming) retain full http.Hijacker and http.Flusher support.
func PrometheusMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		// Resolve the chi route pattern after the handler ran.
		route := resolveRoute(r)
		statusStr := strconv.Itoa(ww.Status())
		elapsed := time.Since(start).Seconds()

		HTTPRequestDuration.WithLabelValues(r.Method, route, statusStr).Observe(elapsed)
		HTTPRequestsTotal.WithLabelValues(r.Method, route, statusStr).Inc()
	})
}

// resolveRoute returns the parameterised chi route pattern for the request
// (e.g. "/api/files/{id}") or falls back to the raw URL path so the label is
// always non-empty.
func resolveRoute(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	// Fallback: truncate to first 64 chars to avoid unbounded cardinality.
	path := r.URL.Path
	if len(path) > 64 {
		return fmt.Sprintf("%s...", path[:64])
	}
	return path
}
