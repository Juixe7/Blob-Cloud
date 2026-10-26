package queue

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
	_ "image/gif"
	_ "image/jpeg"

	"golang.org/x/image/draw"

	"github.com/ledongthuc/pdf"
	"go-drive-clone/internal/ai"
	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
	wsSync "go-drive-clone/internal/sync"
)

type fileGetter interface {
	GetByID(ctx context.Context, id string) (*domain.File, error)
	UpdateAIMetadata(ctx context.Context, id string, tags, summary *string, embedding []float32) error
	UpdateStatus(ctx context.Context, id string, status string) error
	UpdateProcessingStatus(ctx context.Context, id string, expectedUpdatedAt time.Time, status string) (bool, error)
	InsertFileChunks(ctx context.Context, chunks []*domain.FileChunk) error
}

type transactionalFileGetter interface {
	WithTx(tx postgresrepo.DBTX) *postgresrepo.FileRepository
}

type blockHashLister interface {
	ListFileBlockHashes(ctx context.Context, fileID string) ([]string, error)
}

type virusScanner interface {
	ScanStream(ctx context.Context, reader io.Reader) (bool, string, error)
}

const ThumbnailMaxSize = 200

// MaxAssemblySize caps in-memory thumbnail and AI enrichment; antivirus
// scanning streams blocks independently of this limit.
const MaxAssemblySize = 50 * 1024 * 1024

var supportedImageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
}

func isSupportedDocument(filename string) bool {
	lower := strings.ToLower(filename)
	return strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".pdf")
}

type FileProcessor struct {
	db       *sql.DB
	files    fileGetter
	blocks   blockHashLister
	storage  domain.StorageProvider
	notifier wsSync.Notifier
	aiClient ai.AIClient
	clamAV   virusScanner
	journal  *postgresrepo.JournalRepository
	perms    *postgresrepo.PermissionRepository
	log      *slog.Logger
}

func (p *FileProcessor) WithDatabase(db *sql.DB) *FileProcessor {
	p.db = db
	return p
}

func (p *FileProcessor) WithJournal(journal *postgresrepo.JournalRepository) *FileProcessor {
	p.journal = journal
	return p
}

func (p *FileProcessor) WithPermissions(perms *postgresrepo.PermissionRepository) *FileProcessor {
	p.perms = perms
	return p
}

func (p *FileProcessor) journalRecipients(ctx context.Context, file *domain.File) ([]string, error) {
	ids := map[string]struct{}{}
	if file.UserID != "" {
		ids[file.UserID] = struct{}{}
	}
	if p.perms != nil {
		collaborators, err := p.perms.FindCollaboratorUserIDs(ctx, file.ID)
		if err != nil {
			return nil, fmt.Errorf("list processing-event recipients: %w", err)
		}
		for _, id := range collaborators {
			if id != "" {
				ids[id] = struct{}{}
			}
		}
	}
	recipients := make([]string, 0, len(ids))
	for id := range ids {
		recipients = append(recipients, id)
	}
	sort.Strings(recipients)
	return recipients, nil
}

func (p *FileProcessor) journalEntries(recipients []string, file, updated *domain.File) []*domain.JournalEntry {
	entries := make([]*domain.JournalEntry, 0)
	for _, userID := range recipients {
		entries = append(entries, &domain.JournalEntry{
			UserID: userID, FileID: file.ID, Action: domain.ActionFileUpdated,
			ParentID: updated.ParentID, Name: updated.Name, IsDirectory: updated.IsDirectory,
			SizeBytes: updated.SizeBytes, MimeType: updated.MimeType, Status: updated.Status,
		})
	}
	return entries
}

func (p *FileProcessor) notifyJournalEntries(entries []*domain.JournalEntry) {
	if p.notifier == nil {
		return
	}
	for _, entry := range entries {
		p.notifier.NotifyUser(entry.UserID, wsSync.NotificationEvent{
			Type:    wsSync.EventSyncDelta,
			Payload: map[string]any{"cursor": entry.Cursor, "file_id": entry.FileID, "action": entry.Action},
		})
	}
}

func (p *FileProcessor) updateProcessingStatus(ctx context.Context, file *domain.File, status string) (bool, error) {
	if p.db == nil || p.journal == nil {
		changed, err := p.files.UpdateProcessingStatus(ctx, file.ID, file.UpdatedAt, status)
		if changed && err == nil {
			p.notifyProcessingDelta(file.UserID, file.ID, 0)
		}
		return changed, err
	}
	files, ok := p.files.(transactionalFileGetter)
	if !ok {
		return false, fmt.Errorf("file store does not support transactional processing updates")
	}
	recipients, err := p.journalRecipients(ctx, file)
	if err != nil {
		return false, err
	}
	var changed bool
	var entries []*domain.JournalEntry
	err = postgresrepo.RunInTx(ctx, p.db, func(tx postgresrepo.DBTX) error {
		txFiles := files.WithTx(tx)
		var err error
		changed, err = txFiles.UpdateProcessingStatus(ctx, file.ID, file.UpdatedAt, status)
		if err != nil || !changed {
			return err
		}
		updated, err := txFiles.GetByID(ctx, file.ID)
		if err != nil {
			return fmt.Errorf("read updated processing status: %w", err)
		}
		entries = p.journalEntries(recipients, file, updated)
		cursors, err := p.journal.WithTx(tx).BatchRecord(ctx, entries)
		if err != nil {
			return err
		}
		for i, entry := range entries {
			if i < len(cursors) {
				entry.Cursor = cursors[i]
			}
		}
		return nil
	})
	if err != nil || !changed {
		return changed, err
	}
	p.notifyJournalEntries(entries)
	return true, nil
}

func (p *FileProcessor) updateAIMetadata(ctx context.Context, file *domain.File, tags, summary *string) error {
	if p.db == nil || p.journal == nil {
		return p.files.UpdateAIMetadata(ctx, file.ID, tags, summary, nil)
	}
	files, ok := p.files.(transactionalFileGetter)
	if !ok {
		return fmt.Errorf("file store does not support transactional metadata updates")
	}
	recipients, err := p.journalRecipients(ctx, file)
	if err != nil {
		return err
	}
	var entries []*domain.JournalEntry
	err = postgresrepo.RunInTx(ctx, p.db, func(tx postgresrepo.DBTX) error {
		txFiles := files.WithTx(tx)
		if err := txFiles.UpdateAIMetadata(ctx, file.ID, tags, summary, nil); err != nil {
			return err
		}
		updated, err := txFiles.GetByID(ctx, file.ID)
		if err != nil {
			return fmt.Errorf("read updated AI metadata: %w", err)
		}
		entries = p.journalEntries(recipients, file, updated)
		cursors, err := p.journal.WithTx(tx).BatchRecord(ctx, entries)
		if err != nil {
			return err
		}
		for i, entry := range entries {
			if i < len(cursors) {
				entry.Cursor = cursors[i]
			}
		}
		return nil
	})
	if err == nil {
		p.notifyJournalEntries(entries)
	}
	return err
}

func (p *FileProcessor) notifyProcessingDelta(userID, fileID string, cursor int64) {
	if p.notifier == nil {
		return
	}
	p.notifier.NotifyUser(userID, wsSync.NotificationEvent{
		Type:    wsSync.EventSyncDelta,
		Payload: map[string]any{"cursor": cursor, "file_id": fileID, "action": domain.ActionFileUpdated},
	})
}

func NewFileProcessor(
	files fileGetter,
	blocks blockHashLister,
	storage domain.StorageProvider,
	notifier wsSync.Notifier,
	aiClient ai.AIClient,
	clamAV virusScanner,
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
	if file.Status == "QUARANTINED" || file.Status == "ACTIVE" {
		return nil
	}

	p.log.Info("processing file in background", "file_id", msg.FileID, "name", file.Name, "size_bytes", file.SizeBytes)

	// Zero-Trust guard: client-side encrypted files contain ciphertext that server cannot and should not decrypt.
	// Immediately activate the file and skip thumbnail generation, ClamAV antivirus scanning, and Gemini AI processing.
	if file.IsEncrypted {
		p.log.Info("file is client-side encrypted (zero-trust); bypassing server-side processing",
			"file_id", msg.FileID, "name", file.Name)
		if _, err := p.updateProcessingStatus(ctx, file, "ACTIVE"); err != nil {
			return fmt.Errorf("activate encrypted file: %w", err)
		}
		return nil
	}
	if p.clamAV == nil {
		return fmt.Errorf("antivirus scanner unavailable; file %s remains unavailable", msg.FileID)
	}
	clean, virusName, err := p.scanFile(ctx, msg.FileID)
	if err != nil {
		return fmt.Errorf("scan file: %w", err)
	}
	if !clean {
		quarantined, err := p.updateProcessingStatus(ctx, file, "QUARANTINED")
		if err != nil {
			return fmt.Errorf("quarantine infected file: %w", err)
		}
		if quarantined && p.notifier != nil {
			p.notifier.NotifyUser(file.UserID, wsSync.NotificationEvent{
				Type:    "VIRUS_DETECTED",
				Payload: map[string]any{"file_id": msg.FileID, "filename": file.Name, "virus_name": virusName},
			})
		}
		return nil
	}
	activated, err := p.updateProcessingStatus(ctx, file, "ACTIVE")
	if err != nil {
		return fmt.Errorf("activate clean file: %w", err)
	}
	if !activated {
		p.log.Info("discarding stale processing result", "file_id", msg.FileID)
		return nil
	}
	if file.SizeBytes > MaxAssemblySize {
		p.log.Info("clean file exceeds in-memory enrichment limit; skipping thumbnail and AI processing",
			"file_id", msg.FileID, "size_bytes", file.SizeBytes, "max_assembly_bytes", MaxAssemblySize)
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

	// CAS Content-Addressable AI Deduplication Check:
	// If the file already inherited AI metadata at upload completion, or another active unencrypted file
	// has the exact same ordered SHA-256 block sequence, reuse its AI summary, tags, and RAG chunks
	// instead of re-invoking the LLM.
	aiAlreadyPopulated := (file.Summary != nil && strings.TrimSpace(*file.Summary) != "") ||
		(file.Tags != nil && strings.TrimSpace(*file.Tags) != "")
	if !aiAlreadyPopulated {
		if casCopier, ok := p.files.(interface {
			TryCopyAIMetadataFromCAS(ctx context.Context, fileID string) (string, *string, *string, bool, error)
		}); ok {
			if donorID, copiedTags, copiedSummary, copied, errCopy := casCopier.TryCopyAIMetadataFromCAS(ctx, msg.FileID); errCopy == nil && copied {
				p.log.Info("reused AI metadata and RAG chunks from CAS deduplication donor", "file_id", msg.FileID, "donor_id", donorID)
				aiAlreadyPopulated = true
				if p.notifier != nil {
					p.notifier.NotifyUser(msg.UserID, wsSync.NotificationEvent{
						Type: wsSync.EventAIMetadataReady,
						Payload: map[string]any{
							"file_id": msg.FileID,
							"tags":    copiedTags,
							"summary": copiedSummary,
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
	} else if p.notifier != nil {
		// Notify connected clients that AI metadata is already ready from upload-time CAS deduplication
		p.notifier.NotifyUser(msg.UserID, wsSync.NotificationEvent{
			Type: wsSync.EventAIMetadataReady,
			Payload: map[string]any{
				"file_id": msg.FileID,
				"tags":    file.Tags,
				"summary": file.Summary,
			},
		})
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
					},
					)
				}
			} else {
				p.log.Error("failed to put thumbnail object", "err", errPut)
			}
		} else {
			p.log.Error("failed to generate thumbnail", "err", err)
		}

		if p.aiClient != nil && !aiAlreadyPopulated {
			tags, errAI = p.aiClient.GenerateImageTags(ctx, imageBytes)
			if errAI != nil {
				p.log.Error("failed to generate image tags", "err", errAI)
			} else if len(tags) > 0 {
				ts := strings.Join(tags, ", ")
				if errUp := p.updateAIMetadata(ctx, file, &ts, nil); errUp != nil {
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
	if p.aiClient != nil && !aiAlreadyPopulated && (strings.HasSuffix(lowerName, ".txt") || strings.HasSuffix(lowerName, ".md") || strings.HasSuffix(lowerName, ".pdf")) {
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
			if err := p.updateAIMetadata(ctx, file, tagsStr, summaryStr); err != nil {
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
						// Exponential/context-aware backoff if error hit (e.g. rate limit quota)
						errTimer := time.NewTimer(3 * time.Second)
						select {
						case <-ctx.Done():
							errTimer.Stop()
							return ctx.Err()
						case <-errTimer.C:
						}
					} else if len(embedding) > 0 {
						fileChunks = append(fileChunks, &domain.FileChunk{
							FileID:     msg.FileID,
							ChunkIndex: i,
							ChunkText:  chunkText,
							Embedding:  embedding,
						})

						// Configurable pacing between successful chunk embeddings (default: 200ms)
						paceDelay := 200 * time.Millisecond
						if envDelay := os.Getenv("GEMINI_EMBEDDING_PACE_MS"); envDelay != "" {
							if ms, err := strconv.Atoi(envDelay); err == nil && ms >= 0 {
								paceDelay = time.Duration(ms) * time.Millisecond
							}
						}
						if paceDelay > 0 {
							paceTimer := time.NewTimer(paceDelay)
							select {
							case <-ctx.Done():
								paceTimer.Stop()
								return ctx.Err()
							case <-paceTimer.C:
							}
						}
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

// scanFile streams ordered CAS blocks into ClamAV without assembling the file
// in memory, so the antivirus gate also covers files above the enrichment limit.
func (p *FileProcessor) scanFile(ctx context.Context, fileID string) (bool, string, error) {
	hashes, err := p.blocks.ListFileBlockHashes(ctx, fileID)
	if err != nil {
		return false, "", err
	}
	reader, writer := io.Pipe()
	written := make(chan error, 1)
	go func() {
		for _, hash := range hashes {
			rc, err := p.storage.GetObject(ctx, "blocks/"+hash)
			if err != nil {
				_ = writer.CloseWithError(err)
				written <- err
				return
			}
			_, copyErr := io.Copy(writer, rc)
			_ = rc.Close()
			if copyErr != nil {
				_ = writer.CloseWithError(copyErr)
				written <- copyErr
				return
			}
		}
		written <- writer.Close()
	}()

	clean, virus, scanErr := p.clamAV.ScanStream(ctx, reader)
	_ = reader.Close()
	writeErr := <-written
	if scanErr != nil {
		return false, "", scanErr
	}
	if writeErr != nil {
		return false, "", writeErr
	}
	return clean, virus, nil
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
