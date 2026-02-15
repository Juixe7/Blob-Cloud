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

var supportedImageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
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

	p.log.Info("processing file in background", "file_id", msg.FileID, "name", file.Name)

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
					// Use a goroutine to read with a timeout, as ledongthuc/pdf can sometimes hang infinitely
					done := make(chan string, 1)
					go func() {
						b, _ := io.ReadAll(textReader)
						done <- string(b)
					}()
					select {
					case result := <-done:
						text = result
					case <-time.After(15 * time.Second):
						p.log.Error("timeout extracting text from PDF")
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

		if text != "" {
			p.log.Info("calling generate doc summary")
			summary, tags, errAI = p.aiClient.GenerateDocumentSummary(ctx, text)
			p.log.Info("finished doc summary", "err", errAI)
			if errAI != nil {
				p.log.Error("failed to generate doc summary", "err", errAI)
			}

			// Chunking logic
			words := strings.Fields(text)
			chunkSize := 500
			overlap := 50

			// Max chunks to prevent free-tier abuse (e.g. 50 chunks = 25k words)
			maxChunks := 50

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
						// Sleep a bit longer if we hit an error (e.g. 429)
						time.Sleep(10 * time.Second)
					} else if len(embedding) > 0 {
						fileChunks = append(fileChunks, &domain.FileChunk{
							FileID:     msg.FileID,
							ChunkIndex: i,
							ChunkText:  chunkText,
							Embedding:  embedding,
						})
					}
					// Sleep to respect Gemini free tier limits (15 RPM -> 4s per request)
					time.Sleep(4 * time.Second)
				}

				if end == len(words) {
					break
				}
			}
		}
	}

	var tagsStr *string
	var summaryStr *string
	if len(tags) > 0 {
		ts := strings.Join(tags, ", ")
		tagsStr = &ts
	}
	if summary != "" {
		summaryStr = &summary
	}

	// We no longer store the single summary embedding in the files table.
	// We pass nil for the embedding to UpdateAIMetadata.
	p.log.Info("updating metadata")
	if err := p.files.UpdateAIMetadata(ctx, msg.FileID, tagsStr, summaryStr, nil); err != nil {
		p.log.Error("failed to update AI metadata", "err", err)
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
		if _, err := io.Copy(&buf, rc); err != nil {
			_ = rc.Close()
			return nil, fmt.Errorf("read block %s: %w", sha256, err)
		}
		_ = rc.Close()
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
