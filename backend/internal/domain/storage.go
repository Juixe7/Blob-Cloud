// Package domain contains the core business types and interfaces of the
// application. It deliberately has no dependencies on concrete storage or
// transport technologies (no AWS SDK, no HTTP server) so that higher layers
// depend only on abstractions.
package domain

import (
	"context"
	"io"
	"time"
)

// ObjectMetadata contains server-authoritative properties of an object in storage.
type ObjectMetadata struct {
	ContentLength int64
	ETag          string
}

// StorageProvider abstracts a content-addressable block store. Phase 1 ships a
// local disk implementation that mirrors how AWS S3 behaves; Phase 4 will add an
// S3 implementation that satisfies the same interface. Callers (HTTP handlers,
// background workers) depend on this interface and never on a concrete driver.
type StorageProvider interface {
	// GenerateUploadURL returns a URL that a client can use to upload a block
	// via a direct HTTP PUT. For the local driver this points back at our own
	// server; for the S3 driver this will be a presigned S3 PUT URL.
	GenerateUploadURL(ctx context.Context, blockHash string, expires time.Duration) (string, error)

	// PutObject uploads a stream (used for small assets or internal/server-side
	// writes). The concrete driver is responsible for persisting the bytes.
	PutObject(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error

	// GetObject opens the stored object for reading. The caller must close the
	// returned ReadCloser.
	GetObject(ctx context.Context, key string) (io.ReadCloser, error)

	// GetObjectRange opens the stored object starting at offset for up to length bytes.
	// If length is negative, it reads to the end of the object.
	GetObjectRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)

	// HeadObject returns server-authoritative metadata (ContentLength, ETag) directly from storage.
	HeadObject(ctx context.Context, key string) (*ObjectMetadata, error)

	// DeleteObject removes the object from the underlying store. Deleting a
	// non-existent object is not an error.
	DeleteObject(ctx context.Context, key string) error
}

// ── Multipart upload (Tier 3G) ────────────────────────────────────────────────

// MPUBlockThreshold is the maximum block size supported by a single presigned
// PUT URL (S3 hard limit = 5 GiB). Blocks larger than this threshold must use
// the multipart upload path via MultipartUploadProvider.
const MPUBlockThreshold = int64(5) * 1024 * 1024 * 1024 // 5 GiB

// MPUPartSize is the size of each multipart part (except the last).
// S3 minimum part size = 5 MiB; we use 100 MiB for good throughput.
const MPUPartSize = int64(100) * 1024 * 1024 // 100 MiB

// MultipartUploadProvider is the opt-in interface for storage backends that
// support S3 multipart upload. *S3Storage implements this; *LocalStore does not
// (files large enough to need MPU don't arise in local dev).
//
// Callers MUST check whether the concrete StorageProvider satisfies this
// interface before using it:
//
//	if mpu, ok := storage.(domain.MultipartUploadProvider); ok { ... }
type MultipartUploadProvider interface {
	// CreateMultipartUpload initiates an MPU and returns the upload ID.
	// The key must be the final destination key (e.g. "blocks/<sha256>").
	CreateMultipartUpload(ctx context.Context, key string) (uploadID string, err error)

	// PresignUploadPart returns a presigned PUT URL for one MPU part.
	// partNumber is 1-indexed (S3 requirement). expires controls URL lifetime.
	PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, expires time.Duration) (string, error)

	// CompleteMultipartUpload finalises the MPU. etags must be the ETag values
	// returned by the client after each part PUT, in part-number order.
	CompleteMultipartUpload(ctx context.Context, key, uploadID string, etags []string) error

	// AbortMultipartUpload cancels an in-progress MPU and releases its storage.
	AbortMultipartUpload(ctx context.Context, key, uploadID string) error
}
