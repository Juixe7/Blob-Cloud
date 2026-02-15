package httpx

import (
	"net/http"
	"strings"

	"go-drive-clone/internal/domain"
)

// createFolderRequest is the JSON body of POST /api/folders.
type createFolderRequest struct {
	Name     string  `json:"name"`
	ParentID *string `json:"parent_id"` // nil or empty string = root
}

// HandleListFiles implements GET /api/files and GET /api/files?parent_id=<id>.
//
// Returns the immediate children of the directory identified by parent_id
// (or the user's root when parent_id is omitted/empty). Folders are returned
// first, then files, alphabetically — the sort is enforced by the repository.
func (s *Server) HandleListFiles(w http.ResponseWriter, r *http.Request) {
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

	filter := strings.TrimSpace(r.URL.Query().Get("filter"))

	var (
		items []*domain.File
		err   error
	)

	if filter == "shared" {
		items, err = s.fileOps.ListSharedFiles(r.Context(), userID)
	} else {
		// Parse optional parent_id. Empty/missing/blank => nil (root listing).
		var parentID *string
		raw := strings.TrimSpace(r.URL.Query().Get("parent_id"))
		if raw != "" {
			parentID = &raw
			if err := s.fileOps.AuthoriseRead(r.Context(), userID, raw); err != nil {
				s.log.Warn("access denied listing folder", "user_id", userID, "parent_id", raw, "err", err)
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
				return
			}
			if parentFile, err := s.fileOps.GetFileInfo(r.Context(), raw); err == nil && parentFile != nil {
				if parentFile.DeletedAt != nil {
					w.Header().Set("X-Directory-Deleted", "true")
				}
			}
		}
		items, err = s.fileOps.ListDirectory(r.Context(), userID, parentID)
	}

	if err != nil {
		s.log.Error("list files failed", "user_id", userID, "filter", filter, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list files"})
		return
	}

	// Always return a JSON array, even when empty, so the frontend's
	// `items.map(...)` works without a nil-guard.
	if items == nil {
		items = []*domain.File{}
	}
	writeJSON(w, http.StatusOK, items)
}

// HandleCreateFolder implements POST /api/folders.
//
// Inserts a new directory row (is_directory = true) into the files table and,
// in the same transaction, grants the creator OWNER permission on it. Returns
// 201 Created with the new Folder object.
func (s *Server) HandleCreateFolder(w http.ResponseWriter, r *http.Request) {
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

	var req createFolderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "folder name is required"})
		return
	}

	// Normalise parent_id: nil pointer OR pointer-to-empty-string both mean root.
	var parentID *string
	if req.ParentID != nil {
		trimmed := strings.TrimSpace(*req.ParentID)
		if trimmed != "" {
			parentID = &trimmed
		}
	}

	folder, created, err := s.fileOps.CreateFolder(r.Context(), userID, name, parentID)
	if err != nil {
		s.log.Error("create folder failed", "user_id", userID, "name", name, "err", err)
		// Detect a bad/missing parent_id foreign-key violation -> 400.
		if strings.Contains(err.Error(), "23503") || strings.Contains(strings.ToLower(err.Error()), "foreign key") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "parent folder not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create folder"})
		return
	}

	if created {
		writeJSON(w, http.StatusCreated, folder)
	} else {
		writeJSON(w, http.StatusOK, folder)
	}
}

// HandleGetUserStorage implements GET /api/user/storage.
//
// Calculates real total database storage usage for the authenticated user and
// returns byte metrics broken down by file category (images, docs, media, code, other).
func (s *Server) HandleGetUserStorage(w http.ResponseWriter, r *http.Request) {
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

	usage, err := s.fileOps.GetUserStorage(r.Context(), userID)
	if err != nil {
		s.log.Error("get user storage failed", "user_id", userID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to calculate storage usage"})
		return
	}

	if s.hub != nil {
		usage.ActiveSessionsCount = s.hub.GetActiveConnCount(userID)
	}
	if usage.ActiveSessionsCount == 0 {
		usage.ActiveSessionsCount = 1
	}

	writeJSON(w, http.StatusOK, usage)
}

// HandleBulkSoftDelete implements POST /api/files/bulk/delete.
func (s *Server) HandleBulkSoftDelete(w http.ResponseWriter, r *http.Request) {
	if s.fileOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "file operations unavailable"})
		return
	}
	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}
	var req domain.BulkDeleteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}
	if err := s.fileOps.BulkSoftDelete(r.Context(), userID, req); err != nil {
		s.log.Error("bulk soft delete failed", "user_id", userID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "bulk soft delete failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "bulk soft delete succeeded"})
}

// HandleBulkRestore implements POST /api/files/bulk/restore.
func (s *Server) HandleBulkRestore(w http.ResponseWriter, r *http.Request) {
	if s.fileOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "file operations unavailable"})
		return
	}
	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}
	var req domain.BulkRestoreRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}
	if err := s.fileOps.BulkRestore(r.Context(), userID, req); err != nil {
		s.log.Error("bulk restore failed", "user_id", userID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "bulk restore failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "bulk restore succeeded"})
}

// HandleBulkMove implements POST /api/files/bulk/move.
func (s *Server) HandleBulkMove(w http.ResponseWriter, r *http.Request) {
	if s.fileOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "file operations unavailable"})
		return
	}
	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}
	var req domain.BulkMoveRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}
	if err := s.fileOps.BulkMove(r.Context(), userID, req); err != nil {
		s.log.Error("bulk move failed", "user_id", userID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "bulk move failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "bulk move succeeded"})
}

// HandleBulkHardDelete implements DELETE /api/files/bulk/permanent.
func (s *Server) HandleBulkHardDelete(w http.ResponseWriter, r *http.Request) {
	if s.fileOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "file operations unavailable"})
		return
	}
	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}
	var req domain.BulkDeleteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}
	count, err := s.fileOps.BulkHardDelete(r.Context(), userID, req)
	if err != nil {
		s.log.Error("bulk hard delete failed", "user_id", userID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "bulk hard delete failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "bulk hard delete succeeded", "deleted_count": count})
}
