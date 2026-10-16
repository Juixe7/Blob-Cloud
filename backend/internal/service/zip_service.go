package service

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
)

// ZipService coordinates on-the-fly ZIP archive packaging and block-by-block streaming.
// It bypasses temporary disk spooling, streaming Deflate-compressed streams directly to clients.
type ZipService struct {
	files   *postgresrepo.FileRepository
	blocks  *postgresrepo.BlockRepository
	storage domain.StorageProvider
}

// NewZipService constructs a ZipService wired with its metadata and storage dependencies.
func NewZipService(
	files *postgresrepo.FileRepository,
	blocks *postgresrepo.BlockRepository,
	storage domain.StorageProvider,
) *ZipService {
	return &ZipService{
		files:   files,
		blocks:  blocks,
		storage: storage,
	}
}

// StreamItemsZip recursively aggregates multiple files and directory hierarchies, Deflate-compresses
// their underlying CAS blocks sequentially, and streams the resulting ZIP archive directly to w in real-time.
//
// Streaming Performance & Memory Safety:
//   - Zero Disk Spooling: Avoids intermediate file creation on server local storage.
//   - Constant Memory Bound: Reads blocks in small buffers (typically 64 KiB), maintaining an $O(1)$ memory footprint
//     regardless of whether the aggregated archive is 10 MiB or 50 GiB.
//   - Shortcut Transparency: Seamlessly dereferences virtual shortcut entities to stream target file blocks.
//
// Parameters:
//   - ctx: Context carrying client disconnection and deadline cancellations.
//   - ids: Slice of file or directory UUIDs selected for batch download.
//   - userID: Authenticated user ID whose permissions govern subtree access.
//   - w: Target io.Writer (typically an http.ResponseWriter configured with chunked transfer).
//
// Returns:
//   - error: Nil on successful stream completion, or an error if client disconnects or block read fails.
func (s *ZipService) StreamItemsZip(ctx context.Context, ids []string, userID string, w io.Writer) error {
	// 1. Retrieve the list of ZippableItem items for the target folder.
	zippables, err := s.files.GetSubtreeForItems(ctx, ids, userID)
	if err != nil {
		return fmt.Errorf("get folder subtree: %w", err)
	}

	// 2. Initialize zip.Writer directly on the target writer.
	zw := zip.NewWriter(w)
	var closed bool
	defer func() {
		if !closed {
			_ = zw.Close()
		}
	}()

	// 3. Loop over and compress each file.
	for _, f := range zippables {
		// Resolve shortcut target if applicable
		var sourceFileID = f.ID
		if f.TargetID != nil && *f.TargetID != "" {
			sourceFileID = *f.TargetID
		}

		if f.MimeType == "application/vnd.google-apps.folder" {
			// Folder: Append trailing slash for zip entry and use zip.Store
			folderPath := f.RelativePath
			if !strings.HasSuffix(folderPath, "/") {
				folderPath += "/"
			}
			header := &zip.FileHeader{
				Name:     folderPath,
				Method:   zip.Store,
				Modified: time.Now(),
			}
			if _, err := zw.CreateHeader(header); err != nil {
				return fmt.Errorf("create zip directory header for %s: %w", f.RelativePath, err)
			}
			continue
		}

		// Configure header with zip.Deflate compression
		header := &zip.FileHeader{
			Name:     f.RelativePath,
			Method:   zip.Deflate,
			Modified: time.Now(),
		}

		zf, err := zw.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("create zip entry header for %s: %w", f.RelativePath, err)
		}

		// Retrieve blocks sequence for the file.
		hashes, err := s.blocks.ListFileBlockHashes(ctx, sourceFileID)
		if err != nil {
			return fmt.Errorf("list hashes for file %s: %w", f.RelativePath, err)
		}

		// Copy block content directly to the zip file writer.
		for _, hash := range hashes {
			rc, err := s.storage.GetObject(ctx, "blocks/"+hash)
			if err != nil {
				return fmt.Errorf("read block %s for file %s: %w", hash, f.RelativePath, err)
			}
			copyErr := func() error {
				defer rc.Close()
				_, copyErr := io.Copy(zf, rc)
				return copyErr
			}()
			if copyErr != nil {
				return fmt.Errorf("copy block %s to zip stream: %w", hash, copyErr)
			}
		}
	}

	closed = true
	if err := zw.Close(); err != nil {
		return fmt.Errorf("close zip writer: %w", err)
	}

	return nil
}
