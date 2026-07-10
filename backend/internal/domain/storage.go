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

	// DeleteObject removes the object from the underlying store. Deleting a
	// non-existent object is not an error.
	DeleteObject(ctx context.Context, key string) error
}
