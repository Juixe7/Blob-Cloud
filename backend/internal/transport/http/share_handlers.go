package httpx

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"go-drive-clone/internal/domain"
)

// shareRequest is the body of POST /api/files/{id}/share.
type shareRequest struct {
	GranteeEmail string `json:"grantee_email"`
	Role         string `json:"role"`
}

// validRoles is the allow-list for share roles. We keep it tiny rather than
// building a rank table because the role strings are stored verbatim.
var validRoles = map[string]struct{}{
	domain.RoleViewer: {},
	domain.RoleEditor: {},
	domain.RoleOwner:  {},
}

// HandleShare implements POST /api/files/{id}/share.
//
// Grants a role on a file/folder to a user (by email). A duplicate grant on the
// same (file, email) is rejected by the unique constraint and surfaced as 409.
func (s *Server) HandleShare(w http.ResponseWriter, r *http.Request) {
	if s.perms == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "permissions unavailable (database not configured)",
		})
		return
	}
	fileID := chi.URLParam(r, "id")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file id"})
		return
	}

	var req shareRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}
	if req.GranteeEmail == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "grantee_email is required"})
		return
	}
	if _, ok := validRoles[req.Role]; !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be VIEWER, EDITOR, or OWNER"})
		return
	}

	perm := &domain.Permission{
		FileID:       fileID,
		GranteeEmail: req.GranteeEmail,
		Role:         req.Role,
	}
	if err := s.perms.GrantPermission(r.Context(), perm); err != nil {
		// Unique violation (duplicate grant) -> 409 Conflict.
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "permission already granted to " + req.GranteeEmail,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, perm)
}

// HandleListPermissions implements GET /api/files/{id}/permissions.
//
// Lists the direct grants on a file (does not walk the folder hierarchy).
func (s *Server) HandleListPermissions(w http.ResponseWriter, r *http.Request) {
	if s.perms == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "permissions unavailable (database not configured)",
		})
		return
	}
	fileID := chi.URLParam(r, "id")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file id"})
		return
	}

	perms, err := s.perms.GetPermissionsByFile(r.Context(), fileID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Always return a list, never null, so the JSON is client-friendly.
	if perms == nil {
		perms = []*domain.Permission{}
	}
	writeJSON(w, http.StatusOK, perms)
}

// isUniqueViolation detects a Postgres unique-constraint violation across the
// pgx error surfaces. Kept loose (string match as fallback) so it survives
// driver version changes.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23505") || // sqlstate unique_violation
		strings.Contains(strings.ToLower(msg), "unique")
}
