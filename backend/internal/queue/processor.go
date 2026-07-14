package queue

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"strings"

	// Side-effect imports register image format decoders with image.Decode.
	_ "image/gif"
	_ "image/jpeg"
	_ "golang.org/x/image/webp"

	"golang.org/x/image/draw"

	"go-drive-clone/internal/domain"
	wsSync "go-drive-clone/internal/sync"
)

// fileGetter is the subset of domain.FileRepository the processor needs.
// Declared locally so tests can supply a fake without a database.
type fileGetter interface {
	GetByID(ctx context.Context, id string) (*domain.File, error)
}

// blockHashLister is the subset of domain.BlockRepository the processor needs.
type blockHashLister interface {
	ListFileBlockHashes(ctx context.Context, fileID string) ([]string, error)
}

// ThumbnailMaxSize is the maximum bounding box (width x height) for generated
// thumbnails. Images larger than this are scaled down preserving aspect ratio.
const ThumbnailMaxSize = 200

// supportedImageExts lists file extensions the thumbnailer can process.
var supportedImageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
}

// ThumbnailProcessor does the actual work for one SQS message: fetch file
// metadata, assemble the image from its blocks, resize, and upload a
// thumbnail.
type ThumbnailProcessor struct {
	files    fileGetter
	blocks   blockHashLister
	storage  domain.StorageProvider
	notifier wsSync.Notifier // optional; nil-safe via NoopNotifier
	log      *slog.Logger
}

// NewThumbnailProcessor constructs a processor wired with its dependencies.
// notifier may be wsSync.NoopNotifier() if real-time push isn't configured.
func NewThumbnailProcessor(
	files fileGetter,
	blocks blockHashLister,
	storage domain.StorageProvider,
	notifier wsSync.Notifier,
	log *slog.Logger,
) *ThumbnailProcessor {
	if notifier == nil {
		notifier = wsSync.NoopNotifier()
	}
	return &ThumbnailProcessor{files: files, blocks: blocks, storage: storage, notifier: notifier, log: log}
}

// ProcessMessage handles one ThumbnailMessage end to end. It returns nil on
// success (caller deletes the SQS message) or an error (message stays for
// retry).
func (p *ThumbnailProcessor) ProcessMessage(ctx context.Context, msg ThumbnailMessage) error {
	// 1. Fetch file metadata.
	file, err := p.files.GetByID(ctx, msg.FileID)
	if err != nil {
		return fmt.Errorf("fetch file %s: %w", msg.FileID, err)
	}

	// 2. Skip non-images. Return nil so the caller deletes the message (no retry).
	if !isSupportedImage(file.Name) {
		p.log.Info("skipping non-image file", "file_id", msg.FileID, "name", file.Name)
		return nil
	}

	p.log.Info("processing thumbnail", "file_id", msg.FileID, "name", file.Name)

	// 3. Assemble the full image bytes from its ordered blocks.
	imageBytes, err := p.assembleFile(ctx, msg.FileID)
	if err != nil {
		return fmt.Errorf("assemble image: %w", err)
	}

	// 4. Decode, resize, and re-encode as PNG.
	thumbBytes, err := generateThumbnail(imageBytes)
	if err != nil {
		return fmt.Errorf("generate thumbnail: %w", err)
	}

	// 5. Upload the thumbnail to storage.
	thumbKey := fmt.Sprintf("thumbnails/%s.png", msg.FileID)
	if err := p.storage.PutObject(ctx, thumbKey, bytes.NewReader(thumbBytes), int64(len(thumbBytes)), "image/png"); err != nil {
		return fmt.Errorf("upload thumbnail: %w", err)
	}

	// 6. Notify the file owner's open tabs so the UI can refresh the thumbnail.
	// A push failure is non-fatal — the thumbnail exists in storage; the client
	// will eventually see it on its next list/refresh. (notifier is nil-safe.)
	if p.notifier != nil {
		p.notifier.NotifyUser(msg.UserID, wsSync.NotificationEvent{
			Type: wsSync.EventThumbnailReady,
			Payload: map[string]string{
				"file_id":       msg.FileID,
				"thumbnail_url": thumbKey,
			},
		})
	}

	p.log.Info("thumbnail generated and uploaded",
		"file_id", msg.FileID,
		"thumbnail_key", thumbKey,
		"original_bytes", len(imageBytes),
		"thumbnail_bytes", len(thumbBytes),
	)
	return nil
}

// assembleFile downloads all blocks for a file in sequence order and
// concatenates them into a single byte slice in memory. For thumbnails (small
// images) this is acceptable.
func (p *ThumbnailProcessor) assembleFile(ctx context.Context, fileID string) ([]byte, error) {
	hashes, err := p.blocks.ListFileBlockHashes(ctx, fileID)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	for _, sha256 := range hashes {
		rc, err := p.storage.GetObject(ctx, "blocks/"+sha256)
		if err != nil {
			return nil, fmt.Errorf("get block %s: %w", sha256, err)
		}
		if _, err := io.Copy(&buf, rc); err != nil {
			_ = rc.Close()
			return nil, fmt.Errorf("read block %s: %w", sha256, err)
		}
		_ = rc.Close()
	}
	return buf.Bytes(), nil
}

// generateThumbnail decodes the source image, scales it to fit within
// ThumbnailMaxSize x ThumbnailMaxSize preserving aspect ratio, and re-encodes
// as PNG.
func generateThumbnail(src []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	thumb := resizeImage(img, ThumbnailMaxSize)

	var out bytes.Buffer
	if err := png.Encode(&out, thumb); err != nil {
		return nil, fmt.Errorf("encode thumbnail: %w", err)
	}
	return out.Bytes(), nil
}

// resizeImage scales img to fit within maxDim x maxDim using high-quality
// Catmull-Rom interpolation. If the image is already smaller it is returned as-is.
func resizeImage(img image.Image, maxDim int) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= maxDim && h <= maxDim {
		return img
	}

	scale := float64(maxDim) / float64(max(w, h))
	newW := int(float64(w) * scale)
	if newW < 1 {
		newW = 1
	}
	newH := int(float64(h) * scale)
	if newH < 1 {
		newH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
	return dst
}

// isSupportedImage checks whether the filename has a supported image extension.
// Comparison is case-insensitive: "PHOTO.PNG" and "photo.png" both match.
func isSupportedImage(filename string) bool {
	lower := strings.ToLower(filename)
	idx := strings.LastIndex(lower, ".")
	if idx < 0 {
		return false
	}
	return supportedImageExts[lower[idx:]]
}
