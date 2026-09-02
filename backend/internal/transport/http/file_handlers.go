package httpx

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"go-drive-clone/internal/audit"
	"go-drive-clone/internal/auth"
	"go-drive-clone/internal/domain"
	"go-drive-clone/internal/service"
)

// ---------------------------------------------------------------------------
// JWT-from-query helper (used by the download endpoint)
// ---------------------------------------------------------------------------

// userFromQueryToken extracts and validates a JWT passed as the `token` query
// parameter OR an Authorization: Bearer header.
func (s *Server) userFromQueryToken(r *http.Request) (string, int, string) {
	if s.jwtSecret == "" {
		return "", http.StatusServiceUnavailable, "authentication not configured"
	}
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		header := r.Header.Get("Authorization")
		if strings.HasPrefix(header, "Bearer ") {
			tokenStr = strings.TrimPrefix(header, "Bearer ")
		}
	}
	if tokenStr == "" {
		return "", http.StatusUnauthorized, "missing authorization token"
	}
	claims, err := auth.ValidateToken(s.jwtSecret, tokenStr)
	if err != nil {
		return "", http.StatusUnauthorized, "invalid or expired token"
	}
	if claims.UserID == "" {
		return "", http.StatusUnauthorized, "token has no user identity"
	}
	if s.users != nil {
		if _, err := s.users.GetByID(r.Context(), claims.UserID); err != nil {
			return "", http.StatusUnauthorized, "user session invalid or user not found"
		}
	}
	if claims.SessionID != "" && s.sessions != nil {
		if _, err := s.sessions.GetSessionByID(r.Context(), claims.SessionID); err != nil {
			return "", http.StatusUnauthorized, "session has been revoked or expired"
		}
		_ = s.sessions.TouchSession(r.Context(), claims.SessionID)
	}
	return claims.UserID, 0, ""
}

// userFromBearer extracts the user_id from a standard Authorization: Bearer
// header. Used by PATCH, DELETE, GET, and POST endpoints which are AJAX calls
// (headers are fine). Rejects refresh tokens — they must only be sent to
// POST /api/auth/refresh.
func (s *Server) userFromBearer(r *http.Request) (string, int, string) {
	if s.jwtSecret == "" {
		return "", http.StatusServiceUnavailable, "authentication not configured"
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return "", http.StatusUnauthorized, "missing or invalid authorization header"
	}
	tokenStr := strings.TrimPrefix(header, "Bearer ")
	claims, err := auth.ValidateToken(s.jwtSecret, tokenStr)
	if err != nil {
		return "", http.StatusUnauthorized, "invalid or expired token"
	}
	if claims.UserID == "" {
		return "", http.StatusUnauthorized, "token has no user identity"
	}
	if claims.IsRefresh {
		return "", http.StatusUnauthorized, "refresh tokens cannot be used as bearer tokens"
	}
	if s.users != nil {
		if _, err := s.users.GetByID(r.Context(), claims.UserID); err != nil {
			return "", http.StatusUnauthorized, "user session invalid or user not found"
		}
	}
	if claims.SessionID != "" && s.sessions != nil {
		if _, err := s.sessions.GetSessionByID(r.Context(), claims.SessionID); err != nil {
			return "", http.StatusUnauthorized, "session has been revoked or expired"
		}
		_ = s.sessions.TouchSession(r.Context(), claims.SessionID)
	}
	return claims.UserID, 0, ""
}

// ---------------------------------------------------------------------------
// PATCH /api/files/{id}  —  Rename / Move
// ---------------------------------------------------------------------------

// HandleRenameMove implements PATCH /api/files/{id}.
//
// Accepts a JSON body with optional "name" and "parent_id" fields. Validates
// the requester holds EDITOR or OWNER permission on the target file/folder.
func (s *Server) HandleRenameMove(w http.ResponseWriter, r *http.Request) {
	if s.fileOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "file operations unavailable (database not configured)",
		})
		return
	}

	fileID := chi.URLParam(r, "id")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file id"})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	var req service.RenameMoveRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	updated, err := s.fileOps.RenameMove(r.Context(), userID, fileID, req)
	if err != nil {
		code := http.StatusInternalServerError
		// Surface domain-level errors with appropriate HTTP status.
		if strings.Contains(err.Error(), "access denied") {
			code = http.StatusForbidden
		} else if strings.Contains(err.Error(), "not found") || errors.Is(err, sql.ErrNoRows) {
			code = http.StatusNotFound
		} else if strings.Contains(err.Error(), "cannot move") || strings.Contains(err.Error(), "cycle") {
			code = http.StatusBadRequest
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// ---------------------------------------------------------------------------
// DELETE /api/files/{id}
// ---------------------------------------------------------------------------

// HandleDelete implements DELETE /api/files/{id}.
//
// Verifies OWNER permission, then recursively deletes the file/folder and all
// nested descendants. Orphaned storage blocks are garbage-collected in the
// background.
func (s *Server) HandleDelete(w http.ResponseWriter, r *http.Request) {
	if s.fileOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "file operations unavailable (database not configured)",
		})
		return
	}

	fileID := chi.URLParam(r, "id")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file id"})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	result, err := s.fileOps.SoftDelete(r.Context(), userID, fileID)
	if err != nil {
		code := http.StatusInternalServerError
		if strings.Contains(err.Error(), "access denied") {
			code = http.StatusForbidden
		} else if strings.Contains(err.Error(), "not found") || errors.Is(err, sql.ErrNoRows) {
			code = http.StatusNotFound
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}

	// Audit: FILE_DELETED — non-blocking.
	s.auditLog.Log(r.Context(), audit.Entry{
		UserID:       userID,
		Action:       audit.ActionFileDeleted,
		ResourceType: audit.ResourceFile,
		ResourceID:   fileID,
		Metadata:     audit.MarshalMeta(map[string]string{"type": "soft_delete"}),
		ClientIP:     r.RemoteAddr,
	})

	writeJSON(w, http.StatusOK, result)
}

// HandleRestore implements POST /api/files/{id}/restore.
func (s *Server) HandleRestore(w http.ResponseWriter, r *http.Request) {
	if s.fileOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "file operations unavailable (database not configured)",
		})
		return
	}

	fileID := chi.URLParam(r, "id")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file id"})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	result, err := s.fileOps.Restore(r.Context(), userID, fileID)
	if err != nil {
		code := http.StatusInternalServerError
		if strings.Contains(err.Error(), "access denied") {
			code = http.StatusForbidden
		} else if strings.Contains(err.Error(), "not found") || errors.Is(err, sql.ErrNoRows) {
			code = http.StatusNotFound
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// HandlePermanentDelete implements DELETE /api/files/{id}/permanent.
func (s *Server) HandlePermanentDelete(w http.ResponseWriter, r *http.Request) {
	if s.fileOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "file operations unavailable (database not configured)",
		})
		return
	}

	fileID := chi.URLParam(r, "id")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file id"})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	result, err := s.fileOps.PermanentDelete(r.Context(), userID, fileID)
	if err != nil {
		code := http.StatusInternalServerError
		if strings.Contains(err.Error(), "access denied") {
			code = http.StatusForbidden
		} else if strings.Contains(err.Error(), "not found") || errors.Is(err, sql.ErrNoRows) {
			code = http.StatusNotFound
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// HandleListTrash implements GET /api/files/trash.
func (s *Server) HandleListTrash(w http.ResponseWriter, r *http.Request) {
	if s.fileOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "file operations unavailable (database not configured)",
		})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	items, err := s.fileOps.ListTrash(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if items == nil {
		items = []*domain.File{}
	}

	writeJSON(w, http.StatusOK, items)
}

// ---------------------------------------------------------------------------
// GET /api/files/{id}/thumbnail
// ---------------------------------------------------------------------------

// HandleGetThumbnail implements GET /api/files/{id}/thumbnail.
func (s *Server) HandleGetThumbnail(w http.ResponseWriter, r *http.Request) {
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

	if err := s.fileOps.AuthoriseRead(r.Context(), userID, fileID); err != nil {
		status := http.StatusForbidden
		if strings.Contains(err.Error(), "not found") || errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "access denied or not found"})
		return
	}

	thumbKey := fmt.Sprintf("thumbnails/%s.png", fileID)
	rc, err := s.storage.GetObject(r.Context(), thumbKey)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "thumbnail not found"})
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if _, err := io.Copy(w, rc); err != nil {
		s.log.Error("failed to stream thumbnail", "file_id", fileID, "err", err)
	}
}

// ---------------------------------------------------------------------------
// GET /api/files/{id}/download
// ---------------------------------------------------------------------------

// HandleDownload implements GET /api/files/{id}/download.
//
// Authenticates via a `?token=<jwt>` query parameter (because browser-native
// downloads cannot set custom headers). Validates the user has VIEWER, EDITOR,
// or OWNER permission, then streams the reassembled file from S3/local storage
// block-by-block using io.Copy — so even multi-GB files use < 1 MB of server
// RAM.
func (s *Server) HandleDownload(w http.ResponseWriter, r *http.Request) {
	if s.fileOps == nil || s.zipOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "file operations unavailable (database not configured)",
		})
		return
	}

	userID, code, msg := s.userFromQueryToken(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	// 1. Resolve target IDs: check query parameter `ids` first, then path parameter `id`
	rawIDs := strings.TrimSpace(r.URL.Query().Get("ids"))
	var ids []string
	if rawIDs != "" {
		ids = strings.Split(rawIDs, ",")
	} else {
		fileID := chi.URLParam(r, "id")
		if fileID != "" {
			ids = []string{fileID}
		}
	}

	if len(ids) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing target file id(s)"})
		return
	}

	// 2. Authorise read access for all items
	for _, id := range ids {
		if err := s.fileOps.AuthoriseRead(r.Context(), userID, id); err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "access denied") {
				status = http.StatusForbidden
			} else if strings.Contains(err.Error(), "not found") || errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]string{"error": fmt.Sprintf("id %s: %s", id, err.Error())})
			return
		}
	}

	// 3. For a single item, check if it is a directory
	if len(ids) == 1 {
		file, err := s.fileOps.GetFileInfo(r.Context(), ids[0])
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "not found") || errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}

		if !file.IsDirectory {
			// Fall back to native single-file block streaming handler
			_, hashes, err := s.fileOps.GetDownloadInfo(r.Context(), file.ID)
			if err != nil {
				status := http.StatusInternalServerError
				msg := err.Error()
				if strings.Contains(err.Error(), "not found") || errors.Is(err, sql.ErrNoRows) {
					status = http.StatusNotFound
					msg = "file not found"
				}
				writeJSON(w, status, map[string]string{"error": msg})
				return
			}

			if len(hashes) == 0 {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "no blocks found for this file"})
				return
			}

			sanitised := service.SanitiseFilename(file.Name)
			contentType := "application/octet-stream"
			ext := strings.ToLower(filepath.Ext(file.Name))
			switch ext {
			case ".pdf":
				contentType = "application/pdf"
			case ".png":
				contentType = "image/png"
			case ".jpg", ".jpeg":
				contentType = "image/jpeg"
			case ".webp":
				contentType = "image/webp"
			case ".gif":
				contentType = "image/gif"
			case ".svg":
				contentType = "image/svg+xml"
			case ".mp4":
				contentType = "video/mp4"
			case ".webm":
				contentType = "video/webm"
			case ".mp3":
				contentType = "audio/mpeg"
			case ".wav":
				contentType = "audio/wav"
			case ".txt", ".md", ".json", ".js", ".ts", ".go", ".py", ".html", ".css":
				contentType = "text/plain; charset=utf-8"
			}

			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Accept-Ranges", "bytes")
			if r.URL.Query().Get("inline") == "true" {
				w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, sanitised))
			} else {
				w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitised))
			}
			w.Header().Set("X-Accel-Buffering", "no")

			// Check if range request is requested
			var isRange bool
			var rangeStart, rangeEnd int64
			rangeHeader := r.Header.Get("Range")
			if rangeHeader != "" {
				rangeStart, rangeEnd, isRange = parseRangeHeader(rangeHeader, file.SizeBytes)
			}

			var startBlockIndex int64
			var blockOffset int64
			var remainingBytes int64

			if isRange {
				remainingBytes = (rangeEnd - rangeStart) + 1
				if s.fileOps != nil {
					startBlockIndex, blockOffset = s.fileOps.CalculateRangeBlockOffset(rangeStart)
				} else {
					startBlockIndex, blockOffset = service.CalculateRangeBlockOffset(rangeStart)
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rangeStart, rangeEnd, file.SizeBytes))
				w.Header().Set("Content-Length", fmt.Sprintf("%d", remainingBytes))
				w.WriteHeader(http.StatusPartialContent)
			} else {
				remainingBytes = file.SizeBytes
				w.Header().Set("Content-Length", fmt.Sprintf("%d", file.SizeBytes))
				w.WriteHeader(http.StatusOK)
			}

			logCtx := s.log.With("file_id", file.ID, "blocks", len(hashes), "size_bytes", file.SizeBytes, "is_range", isRange, "range_start", rangeStart, "range_end", rangeEnd)
			logCtx.Info("download started")

			for idx := int(startBlockIndex); idx < len(hashes); idx++ {
				if remainingBytes <= 0 {
					break
				}

				select {
				case <-r.Context().Done():
					logCtx.Info("download cancelled by client context")
					return
				default:
				}

				hash := hashes[idx]
				var rc io.ReadCloser
				var err error

				if isRange && idx == int(startBlockIndex) {
					rc, err = s.storage.GetObjectRange(r.Context(), "blocks/"+hash, blockOffset, -1)
				} else {
					rc, err = s.storage.GetObject(r.Context(), "blocks/"+hash)
				}

				if err != nil {
					logCtx.Error("download: failed to read block", "block_index", idx, "hash", hash, "err", err)
					return
				}

				copyErr := func() error {
					defer rc.Close()
					n, err := io.Copy(w, io.LimitReader(rc, remainingBytes))
					remainingBytes -= n
					return err
				}()

				if copyErr != nil {
					logCtx.Error("download: stream copy failed", "block_index", idx, "hash", hash, "err", copyErr)
					return
				}
			}

			logCtx.Info("download completed")
			return
		}
	}

	// 4. Multiple items or a single directory: zip on-the-fly and stream it
	var zipName = "download.zip"
	if len(ids) == 1 {
		file, err := s.fileOps.GetFileInfo(r.Context(), ids[0])
		if err == nil {
			zipName = fmt.Sprintf("%s.zip", service.SanitiseFilename(file.Name))
		}
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, zipName))
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	s.log.Info("bulk zip download started", "items_count", len(ids))
	if err := s.zipOps.StreamItemsZip(r.Context(), ids, w); err != nil {
		s.log.Error("failed to stream directory zip", "ids", ids, "err", err)
		return
	}
	s.log.Info("bulk zip download completed")
}

// HandleCreateShortcut implements POST /api/files/shortcut.
func (s *Server) HandleCreateShortcut(w http.ResponseWriter, r *http.Request) {
	if s.fileOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "file operations unavailable (database not configured)",
		})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	var req service.CreateShortcutRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	shortcut, err := s.fileOps.CreateShortcut(r.Context(), userID, req)
	if err != nil {
		code := http.StatusInternalServerError
		if strings.Contains(err.Error(), "access denied") {
			code = http.StatusForbidden
		} else if strings.Contains(err.Error(), "not found") || errors.Is(err, sql.ErrNoRows) {
			code = http.StatusNotFound
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, shortcut)
}

// parseRangeHeader parses HTTP Range header e.g. "bytes=0-100" or "bytes=100-".
// Returns start, end, and true if parsed.
func parseRangeHeader(header string, fileSize int64) (int64, int64, bool) {
	if header == "" || !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false
	}
	parts := strings.Split(header[6:], "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	startStr := strings.TrimSpace(parts[0])
	endStr := strings.TrimSpace(parts[1])

	var start, end int64
	var err error
	if startStr == "" {
		return 0, 0, false
	}
	start, err = strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 || start >= fileSize {
		return 0, 0, false
	}

	if endStr == "" {
		end = fileSize - 1
	} else {
		end, err = strconv.ParseInt(endStr, 10, 64)
		if err != nil || end < start || end >= fileSize {
			return 0, 0, false
		}
	}
	return start, end, true
}
