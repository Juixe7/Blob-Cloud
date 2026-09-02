// Package metrics holds the Prometheus instrumentation for the entire
// blob-cloud backend. Declaring all metrics here — rather than scattered
// across packages — gives one canonical place to read the full observable
// surface of the system.
//
// Design notes:
//   - A private registry (instead of prometheus.DefaultRegisterer) prevents
//     the Go process's built-in metrics from leaking into our /metrics
//     response in tests or embedded tooling.
//   - Buckets are tuned for realistic file-system + S3 latency ranges.
//   - Call Init() exactly once from main before starting the HTTP server.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is the Prometheus registry shared by all instrumented components.
// Callers obtain the HTTP handler via Handler().
var Registry = prometheus.NewRegistry()

// HTTP layer ----------------------------------------------------------------

// HTTPRequestDuration measures end-to-end handler latency bucketed by HTTP
// method and route pattern. Used by the PrometheusMiddleware.
var HTTPRequestDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name: "blobcloud_http_request_duration_seconds",
		Help: "End-to-end HTTP handler latency in seconds.",
		// Covers sub-millisecond DB look-ups through 30 s long-running ops.
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	},
	[]string{"method", "route", "status_code"},
)

// HTTPRequestsTotal counts every HTTP request by outcome.
var HTTPRequestsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "blobcloud_http_requests_total",
		Help: "Total HTTP requests handled, labelled by method, route, and status class.",
	},
	[]string{"method", "route", "status_code"},
)

// Upload / deduplication layer ----------------------------------------------

// BlockDedupHits counts blocks already in the global blocks table during an
// Initiate call — bytes the client was spared uploading.
var BlockDedupHits = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "blobcloud_block_dedup_hits_total",
	Help: "Number of blocks found in the global dedup table (blocks the client skipped uploading).",
})

// BlockDedupMisses counts blocks NOT yet in the store — a fresh presigned
// URL was issued and the client must upload them.
var BlockDedupMisses = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "blobcloud_block_dedup_misses_total",
	Help: "Number of new blocks the client was given a presigned URL to upload.",
})

// UploadsInitiated counts successful POST /api/upload/initiate calls.
var UploadsInitiated = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "blobcloud_uploads_initiated_total",
	Help: "Total number of upload sessions successfully initiated.",
})

// UploadsCompleted counts successful POST /api/upload/complete calls.
var UploadsCompleted = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "blobcloud_uploads_completed_total",
	Help: "Total number of upload sessions successfully committed.",
})

// SQS worker layer ----------------------------------------------------------

// WorkerJobsProcessed counts SQS messages successfully handled.
// Label "job_type" is currently always "thumbnail".
var WorkerJobsProcessed = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "blobcloud_worker_jobs_processed_total",
		Help: "SQS messages successfully processed by the worker pool.",
	},
	[]string{"job_type"},
)

// WorkerJobErrors counts messages where processing failed (left in queue for
// SQS visibility-timeout retry, never deleted).
var WorkerJobErrors = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "blobcloud_worker_job_errors_total",
		Help: "SQS messages that failed processing (remain in queue for retry).",
	},
	[]string{"job_type"},
)

// WorkerJobDuration measures processor wall-clock time per SQS message.
var WorkerJobDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "blobcloud_worker_job_duration_seconds",
		Help:    "Wall-clock time spent processing one SQS message.",
		Buckets: []float64{.1, .5, 1, 2, 5, 10, 30, 60},
	},
	[]string{"job_type"},
)

// WebSocket layer -----------------------------------------------------------

// WSActiveConnections tracks open WebSocket connections in the hub.
// Call Inc() on connect and Dec() on disconnect.
var WSActiveConnections = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "blobcloud_ws_active_connections",
	Help: "Number of WebSocket connections currently held open by the hub.",
})

// Init registers all metrics with the private registry. Call once from main
// before the HTTP server starts.
func Init() {
	Registry.MustRegister(
		HTTPRequestDuration,
		HTTPRequestsTotal,
		BlockDedupHits,
		BlockDedupMisses,
		UploadsInitiated,
		UploadsCompleted,
		WorkerJobsProcessed,
		WorkerJobErrors,
		WorkerJobDuration,
		WSActiveConnections,
		// Standard Go runtime and process collectors.
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)
}

// Handler returns an HTTP handler that serves /metrics from the private registry.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})
}
