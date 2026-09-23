package httpx

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ledongthuc/pdf"

	"go-drive-clone/internal/domain"
	wsSync "go-drive-clone/internal/sync"
)

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

	if s.aiClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "AI service is not configured"})
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

	if file.IsDirectory {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot generate AI insights for folders"})
		return
	}

	// Read blocks up to 50MB
	hashes, err := s.blocks.ListFileBlockHashes(r.Context(), fileID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list file blocks"})
		return
	}

	var buf bytes.Buffer
	const maxAssemblySize = 50 * 1024 * 1024
	for _, h := range hashes {
		rc, err := s.storage.GetObject(r.Context(), "blocks/"+h)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to get block %s", h)})
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
	lowerName := strings.ToLower(file.Name)

	var tags []string
	var summary string
	var errAI error

	isImage := strings.HasSuffix(lowerName, ".jpg") || strings.HasSuffix(lowerName, ".jpeg") ||
		strings.HasSuffix(lowerName, ".png") || strings.HasSuffix(lowerName, ".webp")
	isDoc := strings.HasSuffix(lowerName, ".txt") || strings.HasSuffix(lowerName, ".md") || strings.HasSuffix(lowerName, ".pdf")

	if !isImage && !isDoc {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported file format for AI analysis"})
		return
	}

	if isImage {
		tags, errAI = s.aiClient.GenerateImageTags(r.Context(), fileBytes)
		if errAI != nil {
			s.log.Error("failed to generate image tags", "err", errAI)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate image tags"})
			return
		}
	} else if isDoc {
		var text string
		if strings.HasSuffix(lowerName, ".pdf") {
			rPdf, err := pdf.NewReader(bytes.NewReader(fileBytes), int64(len(fileBytes)))
			if err == nil {
				textReader, err2 := rPdf.GetPlainText()
				if err2 == nil {
					done := make(chan string, 1)
					go func() {
						b, _ := io.ReadAll(textReader)
						done <- string(b)
					}()
					select {
					case result := <-done:
						text = result
					case <-time.After(15 * time.Second):
						s.log.Error("timeout extracting text from PDF")
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
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate document summary"})
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

	if err := s.files.UpdateAIMetadata(r.Context(), fileID, tagsStr, summaryStr, nil); err != nil {
		s.log.Error("failed to update AI metadata", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save AI metadata"})
		return
	}

	if s.hub != nil {
		s.hub.NotifyUser(userID, wsSync.NotificationEvent{
			Type: wsSync.EventAIMetadataReady,
			Payload: map[string]any{
				"file_id": fileID,
				"tags":    tagsStr,
				"summary": summaryStr,
			},
		})
		s.hub.NotifyUser(userID, wsSync.NotificationEvent{
			Type: wsSync.EventSyncDelta,
			Payload: map[string]any{
				"file_id": fileID,
				"action":  domain.ActionFileUpdated,
			},
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"file_id": fileID,
		"tags":    tagsStr,
		"summary": summaryStr,
	})
}
