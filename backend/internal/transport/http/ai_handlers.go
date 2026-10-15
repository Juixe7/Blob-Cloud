package httpx

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ledongthuc/pdf"

	"go-drive-clone/internal/domain"
	wsSync "go-drive-clone/internal/sync"
)

// aiInFlight prevents duplicate concurrent Gemini LLM calls for the same target file ID.
var aiInFlight sync.Map

type aiInFlightCall struct {
	wg      sync.WaitGroup
	tags    *string
	summary *string
	err     error
	status  int
}

// HandleGenerateAIInsights generates AI tags and summaries on demand for a file.
// POST /api/files/{id}/ai-insights
func (s *Server) HandleGenerateAIInsights(w http.ResponseWriter, r *http.Request) {
	userID, code, msg := s.userFromQueryToken(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	fileID := chi.URLParam(r, "id")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file id"})
		return
	}

	if s.fileOps == nil || s.files == nil || s.blocks == nil || s.storage == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "service unavailable"})
		return
	}

	if err := s.fileOps.AuthoriseRead(r.Context(), userID, fileID); err != nil {
		status := http.StatusForbidden
		if strings.Contains(err.Error(), "not found") || errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "access denied or not found"})
		return
	}

	file, err := s.files.GetByID(r.Context(), fileID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}

	// Resolve shortcut to the underlying target file so blocks and AI metadata live on the canonical target.
	targetFileID := fileID
	targetFile := file
	if file.TargetID != nil && *file.TargetID != "" {
		targetFileID = *file.TargetID
	} else if file.ShortcutTargetID != nil && *file.ShortcutTargetID != "" {
		targetFileID = *file.ShortcutTargetID
	}
	if targetFileID != fileID {
		tf, err := s.files.GetByID(r.Context(), targetFileID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "target file not found"})
			return
		}
		targetFile = tf
	}

	if targetFile.IsDirectory {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot generate AI insights for folders"})
		return
	}
	if targetFile.IsEncrypted {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot generate server-side AI insights for zero-trust encrypted files"})
		return
	}

	force := strings.EqualFold(r.URL.Query().Get("force"), "true")

	// Fast Path 1: Unless explicitly forced, return already-computed AI metadata from the database immediately.
	if !force {
		hasSummary := targetFile.Summary != nil && strings.TrimSpace(*targetFile.Summary) != ""
		hasTags := targetFile.Tags != nil && strings.TrimSpace(*targetFile.Tags) != ""
		if hasSummary || hasTags {
			writeJSON(w, http.StatusOK, map[string]any{
				"file_id": fileID,
				"tags":    targetFile.Tags,
				"summary": targetFile.Summary,
				"cached":  true,
			})
			return
		}

		// Fast Path 2: Check if another file in CAS has the exact same ordered block hashes and reuse its AI metadata.
		if _, copiedTags, copiedSummary, copied, errCopy := s.files.TryCopyAIMetadataFromCAS(r.Context(), targetFileID); errCopy == nil && copied {
			s.notifyAIMetadataUpdated(userID, fileID, targetFileID, copiedTags, copiedSummary)
			writeJSON(w, http.StatusOK, map[string]any{
				"file_id": fileID,
				"tags":    copiedTags,
				"summary": copiedSummary,
				"cached":  true,
			})
			return
		}
	} else {
		// Force re-analysis requires write permission (OWNER or EDITOR) on the target file so VIEWERs cannot burn LLM quota.
		if err := s.fileOps.AuthoriseWrite(r.Context(), userID, targetFileID); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "only owners or editors can force AI re-analysis"})
			return
		}
	}

	if s.aiClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "AI service is not configured"})
		return
	}

	// Singleflight deduplication per targetFileID: if an identical request is already running, wait for its result.
	call := &aiInFlightCall{}
	call.wg.Add(1)
	if actual, loaded := aiInFlight.LoadOrStore(targetFileID, call); loaded {
		existing := actual.(*aiInFlightCall)
		existing.wg.Wait()
		if existing.err != nil {
			status := existing.status
			if status == 0 {
				status = http.StatusInternalServerError
			}
			writeJSON(w, status, map[string]string{"error": existing.err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"file_id": fileID,
			"tags":    existing.tags,
			"summary": existing.summary,
		})
		return
	}
	defer func() {
		aiInFlight.Delete(targetFileID)
		call.wg.Done()
	}()

	// Read blocks up to 50MB from the canonical target file
	hashes, err := s.blocks.ListFileBlockHashes(r.Context(), targetFileID)
	if err != nil {
		call.status = http.StatusInternalServerError
		call.err = errors.New("failed to list file blocks")
		writeJSON(w, call.status, map[string]string{"error": call.err.Error()})
		return
	}

	var buf bytes.Buffer
	const maxAssemblySize = 50 * 1024 * 1024
	for _, h := range hashes {
		rc, err := s.storage.GetObject(r.Context(), "blocks/"+h)
		if err != nil {
			call.status = http.StatusInternalServerError
			call.err = fmt.Errorf("failed to get block %s", h)
			writeJSON(w, call.status, map[string]string{"error": call.err.Error()})
			return
		}
		remaining := int64(maxAssemblySize) - int64(buf.Len())
		if remaining <= 0 {
			_ = rc.Close()
			break
		}
		lr := io.LimitReader(rc, remaining+1)
		_, _ = io.Copy(&buf, lr)
		_ = rc.Close()
	}

	fileBytes := buf.Bytes()
	lowerName := strings.ToLower(targetFile.Name)

	var tags []string
	var summary string
	var errAI error

	isImage := strings.HasSuffix(lowerName, ".jpg") || strings.HasSuffix(lowerName, ".jpeg") ||
		strings.HasSuffix(lowerName, ".png") || strings.HasSuffix(lowerName, ".webp")
	isDoc := strings.HasSuffix(lowerName, ".txt") || strings.HasSuffix(lowerName, ".md") || strings.HasSuffix(lowerName, ".pdf")

	if !isImage && !isDoc {
		call.status = http.StatusBadRequest
		call.err = errors.New("unsupported file format for AI analysis")
		writeJSON(w, call.status, map[string]string{"error": call.err.Error()})
		return
	}

	if isImage {
		tags, errAI = s.aiClient.GenerateImageTags(r.Context(), fileBytes)
		if errAI != nil {
			s.log.Error("failed to generate image tags", "err", errAI)
			call.status = http.StatusInternalServerError
			call.err = errors.New("failed to generate image tags")
			writeJSON(w, call.status, map[string]string{"error": call.err.Error()})
			return
		}
	} else if isDoc {
		var text string
		if strings.HasSuffix(lowerName, ".pdf") {
			rPdf, err := pdf.NewReader(bytes.NewReader(fileBytes), int64(len(fileBytes)))
			if err == nil {
				textReader, err2 := rPdf.GetPlainText()
				if err2 == nil {
					const maxPDFTextBytes = 2 * 1024 * 1024
					limitReader := io.LimitReader(textReader, maxPDFTextBytes)
					done := make(chan string, 1)
					go func() {
						b, _ := io.ReadAll(limitReader)
						done <- string(b)
					}()
					select {
					case result := <-done:
						text = result
					case <-time.After(15 * time.Second):
						s.log.Error("timeout extracting text from PDF")
						if closer, ok := textReader.(io.Closer); ok {
							_ = closer.Close()
						}
					}
				}
			}
		} else {
			text = string(fileBytes)
		}

		if strings.TrimSpace(text) != "" {
			summary, tags, errAI = s.aiClient.GenerateDocumentSummary(r.Context(), text)
			if errAI != nil {
				s.log.Error("failed to generate doc summary", "err", errAI)
				call.status = http.StatusInternalServerError
				call.err = errors.New("failed to generate document summary")
				writeJSON(w, call.status, map[string]string{"error": call.err.Error()})
				return
			}
		} else if strings.HasSuffix(lowerName, ".pdf") {
			summary = "PDF document (scanned or image-only format without extractable text layer)."
			tags = []string{"pdf", "document"}
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

	if err := s.files.UpdateAIMetadata(r.Context(), targetFileID, tagsStr, summaryStr, nil); err != nil {
		s.log.Error("failed to update AI metadata", "err", err)
		call.status = http.StatusInternalServerError
		call.err = errors.New("failed to save AI metadata")
		writeJSON(w, call.status, map[string]string{"error": call.err.Error()})
		return
	}

	call.tags = tagsStr
	call.summary = summaryStr

	s.notifyAIMetadataUpdated(userID, fileID, targetFileID, tagsStr, summaryStr)

	writeJSON(w, http.StatusOK, map[string]any{
		"file_id": fileID,
		"tags":    tagsStr,
		"summary": summaryStr,
	})
}

func (s *Server) notifyAIMetadataUpdated(userID, requestedFileID, targetFileID string, tags, summary *string) {
	s.notifyUser(userID, wsSync.NotificationEvent{
		Type: wsSync.EventAIMetadataReady,
		Payload: map[string]any{
			"file_id": requestedFileID,
			"tags":    tags,
			"summary": summary,
		},
	})
	s.notifyUser(userID, wsSync.NotificationEvent{
		Type: wsSync.EventSyncDelta,
		Payload: map[string]any{
			"file_id": requestedFileID,
			"action":  domain.ActionFileUpdated,
		},
	})
	if targetFileID != requestedFileID {
		s.notifyUser(userID, wsSync.NotificationEvent{
			Type: wsSync.EventAIMetadataReady,
			Payload: map[string]any{
				"file_id": targetFileID,
				"tags":    tags,
				"summary": summary,
			},
		})
	}
}

