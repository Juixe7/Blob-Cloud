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

// ZipService handles on-the-fly zip packaging and block-by-block streaming.
type ZipService struct {
	files   *postgresrepo.FileRepository
	blocks  *postgresrepo.BlockRepository
	storage domain.StorageProvider
}

// NewZipService constructs a ZipService with its dependencies.
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

// StreamItemsZip aggregates multiple files and folders recursively, compresses them, and streams the ZIP file to w in real-time.
func (s *ZipService) StreamItemsZip(ctx context.Context, ids []string, userID string, w io.Writer) error {
	// 1. Retrieve the list of ZippableItem items for the target folder.
	zippables, err := s.files.GetSubtreeForItems(ctx, ids, userID)
	if err != nil {
		return fmt.Errorf("get folder subtree: %w", err)
	}

	// 2. Initialize zip.Writer directly on the target writer.
	zw := zip.NewWriter(w)
	defer zw.Close()

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
			_, err = io.Copy(zf, rc)
			rc.Close()
			if err != nil {
				return fmt.Errorf("copy block %s to zip stream: %w", hash, err)
			}
		}
	}

	return nil
}
