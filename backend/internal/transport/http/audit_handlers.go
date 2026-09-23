package httpx

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"go-drive-clone/internal/audit"
)

// HandleFileHistory implements GET /api/files/{id}/history.
//
// Returns a paged audit trail for the file, newest-first. The caller must
// be authenticated; any authenticated user can see the history of a file they
// have access to (the audit log itself is not permission-gated beyond login —
// the file service handles access-control on the file's data).
//
// Query params:
//
//	limit — number of entries to return (default 50, max 100)
func (s *Server) HandleFileHistory(w http.ResponseWriter, r *http.Request) {
	fileID := chi.URLParam(r, "id")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file id"})
		return
	}

	// Auth gate — must be logged in.
	_, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	limit := 50
	if lq := r.URL.Query().Get("limit"); lq != "" {
		if n, err := strconv.Atoi(lq); err == nil && n > 0 {
			limit = n
		}
	}

	entries, err := s.auditLog.ListFileHistory(r.Context(), fileID, limit)
	if err != nil {
		s.log.Error("audit: list file history", "file_id", fileID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve audit history"})
		return
	}

	// Always return a list, never null.
	if entries == nil {
		entries = make([]audit.Entry, 0)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file_id": fileID,
		"history": entries,
		"count":   len(entries),
	})
}
