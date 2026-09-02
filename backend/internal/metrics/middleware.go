package metrics

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// responseWriter wraps http.ResponseWriter to capture the status code written
// by the downstream handler so the middleware can label metrics accurately.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// PrometheusMiddleware records blobcloud_http_request_duration_seconds and
// blobcloud_http_requests_total for every request. It uses chi.RouteContext to
// resolve the parameterised route pattern (e.g. /api/files/{id}) rather than
// the raw URL, which keeps metric cardinality bounded.
func PrometheusMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		// Resolve the chi route pattern after the handler ran.
		route := resolveRoute(r)
		statusStr := strconv.Itoa(rw.statusCode)
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
