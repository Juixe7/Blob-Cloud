// Package config loads application configuration from the environment with
// sensible defaults so the server runs out-of-the-box in development.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for the API server.
type Config struct {
	// --- Server ---
	Port            int
	ENV             string
	LocalStorageDir string
	BaseURL         string

	// --- Database (Postgres) ---
	DBDSN             string
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration

	// --- Storage ---
	// StorageProvider selects the block storage backend: "local" (filesystem)
	// or "s3" (AWS S3 / Cloudflare R2).
	StorageProvider string
	// AWSRegion is the target AWS region for S3 operations.
	AWSRegion string
	// AWSS3Bucket is the bucket name where blocks are stored.
	AWSS3Bucket string
	// AWSAccessKeyID is the static access key (for local dev or R2).
	AWSAccessKeyID string
	// AWSSecretAccessKey is the static secret key.
	AWSSecretAccessKey string
	// AWSS3Endpoint is an optional custom endpoint URL. When set, the S3 client
	// routes all requests to this URL instead of the default AWS endpoint. This
	// is required for Cloudflare R2 (e.g. https://<account-id>.r2.cloudflarestorage.com).
	AWSS3Endpoint string
	// CloudFrontDomain is an optional CDN domain (e.g. https://cdn.blobcloud.com).
	// When set, presigned URLs have their raw bucket hostname replaced with this
	// domain while keeping the query-string signature intact.
	CloudFrontDomain string

	// --- SQS (event-driven thumbnail processing) ---
	// SQSQueueURL is the URL of the SQS queue that carries thumbnail jobs.
	SQSQueueURL string
	// SQSNumWorkers is the number of concurrent worker goroutines consuming
	// from the queue.
	SQSNumWorkers int
	// SQSPollTimeoutSec is the long-polling wait time in seconds. Long polling
	// (default 20) minimises empty ReceiveMessage calls, which matters on the
	// AWS Free Tier (1M requests/month).
	SQSPollTimeoutSec int
	// SQSAccessKeyID is the optional dedicated AWS IAM access key for SQS.
	SQSAccessKeyID string
	// SQSSecretAccessKey is the optional dedicated AWS IAM secret key for SQS.
	SQSSecretAccessKey string

	// --- Auth / realtime (Phase 6) ---
	// JWTSecret signs and validates the JWTs used to authenticate WebSocket
	// connections. Must be >=32 bytes. Leave empty to disable WS auth (dev only).
	JWTSecret string
	// WSCORSOrigins is a comma-separated list of origins allowed to open WS
	// connections (CORS check for the WebSocket handshake). "*" allows all.
	WSCORSOrigins []string

	// --- Realtime backplane (Tier 2D) ---
	// RedisURL is the connection URL for the Redis Pub/Sub backplane that allows
	// WebSocket events to be routed across multiple API server instances behind a
	// load balancer. Format: redis://[:password@]host:port[/db]
	// Leave empty (default) to run in single-node mode (Hub only, no Redis).
	RedisURL string

	// --- Rate limiting (Tier 2E) ---
	// Per-IP request limits per zone. "Auth" is tight (brute-force protection).
	// "Upload" is moderate (S3 presign ops are expensive). "API" is generous.
	// Set to 0 to disable rate limiting for that zone.
	RateLimitAuthPerMin   int // default 10
	RateLimitUploadPerMin int // default 30
	RateLimitAPIPerMin    int // default 120
}

// Load reads configuration from environment variables, applying defaults for
// any that are missing or invalid.
func Load() (Config, error) {
	port, err := envInt("PORT", 8080)
	if err != nil {
		return Config{}, fmt.Errorf("invalid PORT: %w", err)
	}

	maxOpen, err := envInt("DB_MAX_OPEN_CONNS", 25)
	if err != nil {
		return Config{}, fmt.Errorf("invalid DB_MAX_OPEN_CONNS: %w", err)
	}
	maxIdle, err := envInt("DB_MAX_IDLE_CONNS", 25)
	if err != nil {
		return Config{}, fmt.Errorf("invalid DB_MAX_IDLE_CONNS: %w", err)
	}
	lifetime, err := envDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute)
	if err != nil {
		return Config{}, fmt.Errorf("invalid DB_CONN_MAX_LIFETIME: %w", err)
	}

	sqsWorkers, err := envInt("SQS_NUM_WORKERS", 3)
	if err != nil {
		return Config{}, fmt.Errorf("invalid SQS_NUM_WORKERS: %w", err)
	}
	sqsPollTimeout, err := envInt("SQS_POLL_TIMEOUT_SEC", 20)
	if err != nil {
		return Config{}, fmt.Errorf("invalid SQS_POLL_TIMEOUT_SEC: %w", err)
	}

	return Config{
		Port:              port,
		ENV:               envStr("ENV", "development"),
		LocalStorageDir:   envStr("LOCAL_STORAGE_DIR", "./tmp/storage"),
		BaseURL:           strings.TrimRight(envStr("BASE_URL", "http://localhost:8080"), "/"),
		DBDSN:             envStr("DB_DSN", "postgres://postgres:postgres@localhost:5432/godrive?sslmode=disable"),
		DBMaxOpenConns:    maxOpen,
		DBMaxIdleConns:    maxIdle,
		DBConnMaxLifetime: lifetime,

		StorageProvider:    firstEnvStrWithDefault("STORAGE_PROVIDER", "local", "STORAGE_PROVIDER"),
		AWSRegion:          resolveStorageRegion(),
		AWSS3Bucket:        firstEnvStr("AWS_S3_BUCKET", "R2_BUCKET", "R2_BUCKET_NAME"),
		AWSAccessKeyID:     firstEnvStr("AWS_ACCESS_KEY_ID", "R2_ACCESS_KEY_ID"),
		AWSSecretAccessKey: firstEnvStr("AWS_SECRET_ACCESS_KEY", "R2_SECRET_ACCESS_KEY"),
		AWSS3Endpoint:      cleanURL(firstEnvStr("AWS_S3_ENDPOINT", "R2_ENDPOINT")),
		CloudFrontDomain:   strings.TrimRight(cleanURL(envStr("CLOUDFRONT_DOMAIN", "")), "/"),

		// --- SQS (event-driven thumbnail processing) ---
		SQSQueueURL:        envStr("SQS_QUEUE_URL", ""),
		SQSNumWorkers:      sqsWorkers,
		SQSPollTimeoutSec:  sqsPollTimeout,
		SQSAccessKeyID:     firstEnvStr("SQS_AWS_ACCESS_KEY_ID", "AWS_SQS_ACCESS_KEY_ID"),
		SQSSecretAccessKey: firstEnvStr("SQS_AWS_SECRET_ACCESS_KEY", "AWS_SQS_SECRET_ACCESS_KEY"),

		// --- Auth / realtime (Phase 6) ---
		JWTSecret:     envStr("JWT_SECRET", ""),
		WSCORSOrigins: envList("WS_CORS_ORIGINS", []string{"*"}),

		// --- Realtime backplane (Tier 2D) ---
		RedisURL: envStr("REDIS_URL", ""),

		// --- Rate limiting (Tier 2E) ---
		RateLimitAuthPerMin:   mustEnvInt("RL_AUTH_RPM", 10),
		RateLimitUploadPerMin: mustEnvInt("RL_UPLOAD_RPM", 30),
		RateLimitAPIPerMin:    mustEnvInt("RL_API_RPM", 120),
	}, nil
}

func resolveStorageRegion() string {
	if r2Reg := envStr("R2_REGION", ""); r2Reg != "" {
		return r2Reg
	}
	endpoint := envStr("AWS_S3_ENDPOINT", "")
	if strings.Contains(endpoint, "r2.cloudflarestorage.com") {
		return "auto"
	}
	return envStr("AWS_REGION", "us-east-1")
}

// envStr returns the value of the environment variable named by key, or the
// provided fallback if it is empty/unset.
func envStr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// firstEnvStr returns the first non-empty value among the given environment variable keys.
func firstEnvStr(keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// firstEnvStrWithDefault returns the first non-empty value among keys, or the fallback if all are empty.
func firstEnvStrWithDefault(fallback string, keys ...string) string {
	if v := firstEnvStr(keys...); v != "" {
		return v
	}
	return fallback
}

// cleanURL strips accidental markdown brackets/parentheses e.g. [url](url) from pasted URLs.
func cleanURL(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") && strings.Contains(s, "](") {
		idx := strings.Index(s, "](")
		endIdx := strings.LastIndex(s, ")")
		if idx != -1 && endIdx != -1 && endIdx > idx+2 {
			s = s[idx+2 : endIdx]
		}
	}
	return strings.Trim(s, "[]() \t\r\n")
}

// envInt parses the environment variable named by key as an int, returning the
// fallback if it is empty. An invalid (non-empty) value yields an error.
func envInt(key string, fallback int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// envDuration parses the environment variable named by key as a time.Duration,
// returning the fallback if it is empty. Supports both bare-seconds ("300") and
// Go duration syntax ("5m").
func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	return time.Duration(n) * time.Second, nil
}

// envList reads a comma-separated environment variable into a trimmed slice.
// An empty/unset variable returns the fallback.
func envList(key string, fallback []string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

// mustEnvInt reads an integer env var, returning fallback on missing or invalid
// value. Unlike envInt it never surfaces an error — used for optional numeric
// tuning knobs where an invalid value should degrade gracefully to the default.
func mustEnvInt(key string, fallback int) int {
	n, err := envInt(key, fallback)
	if err != nil {
		return fallback
	}
	return n
}
