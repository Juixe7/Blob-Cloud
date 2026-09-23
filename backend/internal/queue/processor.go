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
	"time"

	_ "image/gif"
	_ "image/jpeg"
	_ "golang.org/x/image/webp"

	"golang.org/x/image/draw"

	"go-drive-clone/internal/ai"
	"go-drive-clone/internal/antivirus"
	"go-drive-clone/internal/domain"
	wsSync "go-drive-clone/internal/sync"
	"github.com/ledongthuc/pdf"
)

type fileGetter interface {
	GetByID(ctx context.Context, id string) (*domain.File, error)
	UpdateAIMetadata(ctx context.Context, id string, tags, summary *string, embedding []float32) error
	UpdateStatus(ctx context.Context, id string, status string) error
	InsertFileChunks(ctx context.Context, chunks []*domain.FileChunk) error
}

type blockHashLister interface {
	ListFileBlockHashes(ctx context.Context, fileID string) ([]string, error)
}

const ThumbnailMaxSize = 200

// MaxAssemblySize defines the maximum file size (50MB) assembled into memory
// for antivirus scanning, thumbnail extraction, or document parsing.
// Files exceeding this size are marked ACTIVE immediately without buffering into RAM.
const MaxAssemblySize = 50 * 1024 * 1024

var supportedImageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
}

func isSupportedDocument(filename string) bool {
	lower := strings.ToLower(filename)
	return strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".pdf")
}

type FileProcessor struct {
	files    fileGetter
	blocks   blockHashLister
	storage  domain.StorageProvider
	notifier wsSync.Notifier
	aiClient ai.AIClient
	clamAV   *antivirus.ClamAVClient
	log      *slog.Logger
}

func NewFileProcessor(
	files fileGetter,
	blocks blockHashLister,
	storage domain.StorageProvider,
	notifier wsSync.Notifier,
	aiClient ai.AIClient,
	clamAV *antivirus.ClamAVClient,
	log *slog.Logger,
) *FileProcessor {
	if notifier == nil {
		notifier = wsSync.NoopNotifier()
	}
	return &FileProcessor{files: files, blocks: blocks, storage: storage, notifier: notifier, aiClient: aiClient, clamAV: clamAV, log: log}
}

func (p *FileProcessor) ProcessMessage(ctx context.Context, msg ThumbnailMessage) error {
	file, err := p.files.GetByID(ctx, msg.FileID)
	if err != nil {
		return fmt.Errorf("fetch file %s: %w", msg.FileID, err)
	}

	p.log.Info("processing file in background", "file_id", msg.FileID, "name", file.Name, "size_bytes", file.SizeBytes)

	// Zero-Trust guard: client-side encrypted files contain ciphertext that server cannot and should not decrypt.
	// Immediately activate the file and skip thumbnail generation, ClamAV antivirus scanning, and Gemini AI processing.
	if file.IsEncrypted {
		p.log.Info("file is client-side encrypted (zero-trust); bypassing server-side processing",
			"file_id", msg.FileID, "name", file.Name)
		if err := p.files.UpdateStatus(ctx, msg.FileID, "ACTIVE"); err != nil {
			p.log.Error("failed to set status to ACTIVE for encrypted file", "err", err)
		}
		return nil
	}

	// Memory safety guard: skip in-memory assembly for large files (> 50MB) to prevent worker OOM kills.
	if file.SizeBytes > MaxAssemblySize {
		p.log.Info("file exceeds maximum in-memory processing threshold; skipping buffer assembly",
			"file_id", msg.FileID, "size_bytes", file.SizeBytes, "max_assembly_bytes", MaxAssemblySize)
		if err := p.files.UpdateStatus(ctx, msg.FileID, "ACTIVE"); err != nil {
			p.log.Error("failed to set status to ACTIVE", "err", err)
		}
		return nil
	}

	// Early activation: if file requires no post-processing (not an image, not an AI document, and no ClamAV scanner),
	// activate immediately without downloading or assembling blocks into RAM.
	needsAssembly := (p.clamAV != nil) || isSupportedImage(file.Name) || isSupportedDocument(file.Name)
	if !needsAssembly {
		p.log.Info("file type requires no post-processing; activating immediately",
			"file_id", msg.FileID, "name", file.Name)
		if err := p.files.UpdateStatus(ctx, msg.FileID, "ACTIVE"); err != nil {
			p.log.Error("failed to set status to ACTIVE", "err", err)
		}
		return nil
	}

	var tags []string
	var summary string
	var errAI error

	lowerName := strings.ToLower(file.Name)

	fileBytes, err := p.assembleFile(ctx, msg.FileID)
	if err != nil {
		return fmt.Errorf("assemble file: %w", err)
	}

	// 1. Antivirus Scan
	if p.clamAV != nil {
		isClean, virusName, errScan := p.clamAV.ScanStream(ctx, bytes.NewReader(fileBytes))
		if errScan != nil {
			p.log.Error("failed to scan file with clamav", "err", errScan)
			// Proceed or block on error? Let's assume it failed and we might want to block or allow. The prompt says "If the Scan is CLEAN... If the Scan is INFECTED...". If it fails, we shouldn't quarantine. But we should log.
		} else if !isClean {
			p.log.Warn("malware detected in file", "file_id", msg.FileID, "virus", virusName)
			if err := p.files.UpdateStatus(ctx, msg.FileID, "QUARANTINED"); err != nil {
				p.log.Error("failed to quarantine file", "err", err)
			}
			if p.notifier != nil {
				p.notifier.NotifyUser(file.UserID, wsSync.NotificationEvent{
					Type: "VIRUS_DETECTED", // we will define this as string
					Payload: map[string]interface{}{
						"file_id":    msg.FileID,
						"filename":   file.Name,
						"virus_name": virusName,
					},
				})
			}
			return nil // Abort further processing
		}
	}

	// If clean, mark as ACTIVE
	if err := p.files.UpdateStatus(ctx, msg.FileID, "ACTIVE"); err != nil {
		p.log.Error("failed to set status to ACTIVE", "err", err)
	}

	if isSupportedImage(file.Name) {
		imageBytes := fileBytes


		thumbBytes, err := generateThumbnail(imageBytes)
		if err == nil {
			thumbKey := fmt.Sprintf("thumbnails/%s.png", msg.FileID)
			if errPut := p.storage.PutObject(ctx, thumbKey, bytes.NewReader(thumbBytes), int64(len(thumbBytes)), "image/png"); errPut == nil {
				if p.notifier != nil {
					p.notifier.NotifyUser(msg.UserID, wsSync.NotificationEvent{
						Type: wsSync.EventThumbnailReady,
						Payload: map[string]string{
							"file_id":       msg.FileID,
							"thumbnail_url": fmt.Sprintf("/api/files/%s/thumbnail", msg.FileID),
						},
					})
				}
			} else {
				p.log.Error("failed to put thumbnail object", "err", errPut)
			}
		} else {
			p.log.Error("failed to generate thumbnail", "err", err)
		}

		if p.aiClient != nil {
			tags, errAI = p.aiClient.GenerateImageTags(ctx, imageBytes)
			if errAI != nil {
				p.log.Error("failed to generate image tags", "err", errAI)
			} else if len(tags) > 0 {
				ts := strings.Join(tags, ", ")
				if errUp := p.files.UpdateAIMetadata(ctx, msg.FileID, &ts, nil, nil); errUp != nil {
					p.log.Error("failed to update image AI tags", "err", errUp)
				}
				if p.notifier != nil {
					p.notifier.NotifyUser(msg.UserID, wsSync.NotificationEvent{
						Type: wsSync.EventAIMetadataReady,
						Payload: map[string]any{
							"file_id": msg.FileID,
							"tags":    ts,
							"summary": nil,
						},
					})
					p.notifier.NotifyUser(msg.UserID, wsSync.NotificationEvent{
						Type: wsSync.EventSyncDelta,
						Payload: map[string]any{
							"file_id": msg.FileID,
							"action":  domain.ActionFileUpdated,
						},
					})
				}
			}
		}

	}

	var fileChunks []*domain.FileChunk
	if p.aiClient != nil && (strings.HasSuffix(lowerName, ".txt") || strings.HasSuffix(lowerName, ".md") || strings.HasSuffix(lowerName, ".pdf")) {
		var text string
		if strings.HasSuffix(lowerName, ".pdf") {
			r, err := pdf.NewReader(bytes.NewReader(fileBytes), int64(len(fileBytes)))
			if err == nil {
				textReader, err2 := r.GetPlainText()
				if err2 == nil {
					// Use a bounded goroutine with timeout and limit reader to prevent goroutine leaks and OOM
					const maxPDFTextBytes = 2 * 1024 * 1024 // 2MB max
					limitReader := io.LimitReader(textReader, maxPDFTextBytes)

					done := make(chan string, 1)
					ctxExtract, cancelExtract := context.WithTimeout(ctx, 15*time.Second)
					defer cancelExtract()

					go func() {
						b, _ := io.ReadAll(limitReader)
						done <- string(b)
					}()

					select {
					case result := <-done:
						text = result
					case <-ctxExtract.Done():
						p.log.Error("timeout extracting text from PDF")
						if closer, ok := textReader.(io.Closer); ok {
							_ = closer.Close()
						}
					}
				} else {
					p.log.Error("failed to extract text from PDF", "err", err2)
				}
			} else {
				p.log.Error("failed to read PDF", "err", err)
			}
		} else {
			text = string(fileBytes)
		}

		p.log.Info("extracted text length", "len", len(text))

		if strings.TrimSpace(text) != "" {
			p.log.Info("calling generate doc summary")
			summary, tags, errAI = p.aiClient.GenerateDocumentSummary(ctx, text)
			p.log.Info("finished doc summary", "err", errAI)
			if errAI != nil {
				p.log.Error("failed to generate doc summary", "err", errAI)
			}
		} else if strings.HasSuffix(lowerName, ".pdf") {
			summary = "PDF document (scanned or image-only format without extractable text layer)."
			tags = []string{"pdf", "document"}
		}

		// Persist summary & tags immediately so UI gets it right away!
		var tagsStr *string
		var summaryStr *string
		if len(tags) > 0 {
			ts := strings.Join(tags, ", ")
			tagsStr = &ts
		}
		if summary != "" {
			summaryStr = &summary
		}

		if tagsStr != nil || summaryStr != nil {
			p.log.Info("persisting AI metadata immediately", "file_id", msg.FileID)
			if err := p.files.UpdateAIMetadata(ctx, msg.FileID, tagsStr, summaryStr, nil); err != nil {
				p.log.Error("failed to update AI metadata", "err", err)
			}
			if p.notifier != nil {
				p.notifier.NotifyUser(msg.UserID, wsSync.NotificationEvent{
					Type: wsSync.EventAIMetadataReady,
					Payload: map[string]any{
						"file_id": msg.FileID,
						"tags":    tagsStr,
						"summary": summaryStr,
					},
				})
				p.notifier.NotifyUser(msg.UserID, wsSync.NotificationEvent{
					Type: wsSync.EventSyncDelta,
					Payload: map[string]any{
						"file_id": msg.FileID,
						"action":  domain.ActionFileUpdated,
					},
				})
			}
		}

		// Chunking logic: cap at 10 chunks (~5k words) to respect SQS 30s visibility timeout
		if strings.TrimSpace(text) != "" {
			words := strings.Fields(text)
			chunkSize := 500
			overlap := 50
			maxChunks := 10

			for i, start := 0, 0; start < len(words) && i < maxChunks; i, start = i+1, start+chunkSize-overlap {
				end := start + chunkSize
				if end > len(words) {
					end = len(words)
				}
				chunkWords := words[start:end]
				chunkText := strings.Join(chunkWords, " ")

				if p.aiClient != nil {
					p.log.Info("generating embedding", "chunk", i)
					embedding, err := p.aiClient.GetTextEmbedding(ctx, chunkText)
					p.log.Info("finished embedding", "chunk", i, "err", err)
					if err != nil {
						p.log.Error("failed to generate chunk embedding", "chunk_index", i, "err", err)
						// Exponential/context-aware sleep if error hit
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-time.After(5 * time.Second):
						}
					} else if len(embedding) > 0 {
						fileChunks = append(fileChunks, &domain.FileChunk{
							FileID:     msg.FileID,
							ChunkIndex: i,
							ChunkText:  chunkText,
							Embedding:  embedding,
						})
					}
					// Context-aware pacing to respect Gemini RPM limits while allowing prompt shutdown
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(2 * time.Second):
					}
				}

				if end == len(words) {
					break
				}
			}
		}
	}

	if len(fileChunks) > 0 {
		p.log.Info("inserting chunks", "count", len(fileChunks))
		if err := p.files.InsertFileChunks(ctx, fileChunks); err != nil {
			p.log.Error("failed to insert file chunks", "err", err)
		}
	}

	p.log.Info("process message complete")
	return nil
}

func (p *FileProcessor) assembleFile(ctx context.Context, fileID string) ([]byte, error) {
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
		// Defensive memory bounds check against buffer runaway
		remaining := int64(MaxAssemblySize) - int64(buf.Len())
		if remaining <= 0 {
			_ = rc.Close()
			return nil, fmt.Errorf("file %s exceeded max assembly size limit of %d bytes", fileID, MaxAssemblySize)
		}
		lr := io.LimitReader(rc, remaining+1)
		_, errCopy := io.Copy(&buf, lr)
		_ = rc.Close()
		if errCopy != nil {
			return nil, fmt.Errorf("read block %s: %w", sha256, errCopy)
		}
		if int64(buf.Len()) > MaxAssemblySize {
			return nil, fmt.Errorf("file %s exceeded max assembly size limit of %d bytes", fileID, MaxAssemblySize)
		}
	}
	return buf.Bytes(), nil
}

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

func isSupportedImage(filename string) bool {
	lower := strings.ToLower(filename)
	idx := strings.LastIndex(lower, ".")
	if idx < 0 {
		return false
	}
	return supportedImageExts[lower[idx:]]
}
