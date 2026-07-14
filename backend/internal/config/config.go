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

	// --- Auth / realtime (Phase 6) ---
	// JWTSecret signs and validates the JWTs used to authenticate WebSocket
	// connections. Must be >=32 bytes. Leave empty to disable WS auth (dev only).
	JWTSecret string
	// WSCORSOrigins is a comma-separated list of origins allowed to open WS
	// connections (CORS check for the WebSocket handshake). "*" allows all.
	WSCORSOrigins []string
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

		StorageProvider:    envStr("STORAGE_PROVIDER", "local"),
		AWSRegion:          envStr("AWS_REGION", "us-east-1"),
		AWSS3Bucket:        envStr("AWS_S3_BUCKET", ""),
		AWSAccessKeyID:     envStr("AWS_ACCESS_KEY_ID", ""),
		AWSSecretAccessKey: envStr("AWS_SECRET_ACCESS_KEY", ""),
		AWSS3Endpoint:      envStr("AWS_S3_ENDPOINT", ""),
		CloudFrontDomain:   strings.TrimRight(envStr("CLOUDFRONT_DOMAIN", ""), "/"),

		// --- SQS (event-driven thumbnail processing) ---
		SQSQueueURL:      envStr("SQS_QUEUE_URL", ""),
		SQSNumWorkers:    sqsWorkers,
		SQSPollTimeoutSec: sqsPollTimeout,

		// --- Auth / realtime (Phase 6) ---
		JWTSecret:     envStr("JWT_SECRET", ""),
		WSCORSOrigins: envList("WS_CORS_ORIGINS", []string{"*"}),
	}, nil
}

// envStr returns the value of the environment variable named by key, or the
// provided fallback if it is empty/unset.
func envStr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
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
