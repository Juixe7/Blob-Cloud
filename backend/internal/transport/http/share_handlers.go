package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"go-drive-clone/internal/domain"
	wsSync "go-drive-clone/internal/sync"
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
// Requires OWNER or EDITOR permission on the file.
func (s *Server) HandleShare(w http.ResponseWriter, r *http.Request) {
	if s.perms == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "permissions unavailable (database not configured)",
		})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	fileID := chi.URLParam(r, "id")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file id"})
		return
	}

	allowed, err := s.checkSharePermission(r.Context(), userID, fileID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify permissions"})
		return
	}
	if !allowed {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "you do not have permission to modify access for this file"})
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

	// Notify the grantee in real-time so their UI refreshes without polling.
	if s.hub != nil && s.users != nil {
		if grantee, err := s.users.GetByEmail(r.Context(), req.GranteeEmail); err == nil {
			// Resolve the actual filename for the notification payload.
			filename := fileID // fallback to file id if lookup fails
			if s.fileOps != nil {
				if file, _, fErr := s.fileOps.GetDownloadInfo(r.Context(), fileID); fErr == nil {
					filename = file.Name
				}
			}
			s.hub.NotifyUser(grantee.ID, wsSync.NotificationEvent{
				Type: wsSync.EventFileShared,
				Payload: map[string]string{
					"file_id":   fileID,
					"filename":  filename,
					"shared_by": "a collaborator",
				},
			})
		}
	}

	// Dispatch asynchronous background email notification
	if s.mailer != nil && s.users != nil {
		if sender, err := s.users.GetByID(r.Context(), userID); err == nil {
			filename := fileID
			if s.fileOps != nil {
				if file, _, fErr := s.fileOps.GetDownloadInfo(r.Context(), fileID); fErr == nil {
					filename = file.Name
				}
			}
			link := "http://localhost:5173/dashboard"
			go func() {
				_ = s.mailer.SendShareNotificationEmail(req.GranteeEmail, sender.Email, filename, req.Role, link)
			}()
		}
	}

	writeJSON(w, http.StatusCreated, perm)
}

// HandleListPermissions implements GET /api/files/{id}/permissions.
//
// Lists the direct grants on a file (does not walk the folder hierarchy).
// Requires authentication.
func (s *Server) HandleListPermissions(w http.ResponseWriter, r *http.Request) {
	if s.perms == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "permissions unavailable (database not configured)",
		})
		return
	}

	_, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
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

// HandleUpdateShare implements PATCH /api/files/{id}/share/{email}.
// Updates the role of an existing permission.
// Requires OWNER or EDITOR permission on the file.
func (s *Server) HandleUpdateShare(w http.ResponseWriter, r *http.Request) {
	if s.perms == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "permissions unavailable (database not configured)",
		})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	fileID := chi.URLParam(r, "id")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file id"})
		return
	}

	allowed, err := s.checkSharePermission(r.Context(), userID, fileID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify permissions"})
		return
	}
	if !allowed {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "you do not have permission to modify access for this file"})
		return
	}

	granteeEmail, _ := url.PathUnescape(chi.URLParam(r, "email"))
	if granteeEmail == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing grantee email"})
		return
	}

	var req shareRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	if _, ok := validRoles[req.Role]; !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be VIEWER, EDITOR, or OWNER"})
		return
	}

	if err := s.perms.UpdatePermission(r.Context(), fileID, granteeEmail, req.Role); err != nil {
		if errors.Is(err, domain.ErrPermissionNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "permission not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "permission updated"})
}

// HandleRevokeShare implements DELETE /api/files/{id}/share/{email}.
// Revokes access to the file.
// Requires OWNER or EDITOR permission on the file.
func (s *Server) HandleRevokeShare(w http.ResponseWriter, r *http.Request) {
	if s.perms == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "permissions unavailable (database not configured)",
		})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	fileID := chi.URLParam(r, "id")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file id"})
		return
	}

	allowed, err := s.checkSharePermission(r.Context(), userID, fileID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify permissions"})
		return
	}
	if !allowed {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "you do not have permission to modify access for this file"})
		return
	}

	granteeEmail, _ := url.PathUnescape(chi.URLParam(r, "email"))
	if granteeEmail == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing grantee email"})
		return
	}

	if err := s.perms.RevokePermission(r.Context(), fileID, granteeEmail); err != nil {
		if errors.Is(err, domain.ErrPermissionNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "permission not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "permission revoked"})
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

// checkSharePermission verifies if the user is the file owner or has an EDITOR/OWNER role.
func (s *Server) checkSharePermission(ctx context.Context, userID, fileID string) (bool, error) {
	if s.users == nil || s.perms == nil || s.files == nil {
		return false, errors.New("dependencies missing")
	}

	file, err := s.files.GetByID(ctx, fileID)
	if err != nil {
		return false, err
	}
	if file.UserID == userID {
		return true, nil
	}

	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return false, err
	}

	return s.perms.CheckUserPermission(ctx, fileID, user.Email, []string{domain.RoleOwner, domain.RoleEditor})
}
