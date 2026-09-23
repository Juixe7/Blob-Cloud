package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"go-drive-clone/internal/audit"
	"go-drive-clone/internal/domain"
	wsSync "go-drive-clone/internal/sync"
)

// shareRequest is the body of POST /api/files/{id}/share.
type shareRequest struct {
	GranteeEmail string `json:"grantee_email"`
	Role         string `json:"role"`
	Message      string `json:"message,omitempty"`
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
// Grants an opt-in share invitation on a file/folder to a user (by email).
// Newly created shares start in PENDING status with a 7-day expiration.
// Senders blocked by the recipient are rejected with 403 Forbidden.
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

	sender, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify user account"})
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
	if strings.EqualFold(req.GranteeEmail, sender.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot share a file with yourself"})
		return
	}

	// Check if recipient has blocked this sender
	if granteeUser, gErr := s.users.GetByEmail(r.Context(), req.GranteeEmail); gErr == nil && granteeUser != nil {
		blocked, bErr := s.perms.IsBlocked(r.Context(), granteeUser.ID, sender.Email)
		if bErr == nil && blocked {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "unable to share file with this recipient"})
			return
		}
	}

	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	var msgPtr *string
	if trimmed := strings.TrimSpace(req.Message); trimmed != "" {
		msgPtr = &trimmed
	}

	perm := &domain.Permission{
		FileID:       fileID,
		GranteeEmail: req.GranteeEmail,
		Role:         req.Role,
		Status:       domain.PermissionStatusPending,
		Message:      msgPtr,
		ExpiresAt:    &expiresAt,
		InvitedBy:    &userID,
	}
	if err := s.perms.GrantPermission(r.Context(), perm); err != nil {
		// Unique violation (duplicate grant) -> 409 Conflict.
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "invitation or permission already exists for " + req.GranteeEmail,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Resolve the actual filename for notifications
	filename := fileID
	if s.fileOps != nil {
		if file, _, fErr := s.fileOps.GetDownloadInfo(r.Context(), fileID); fErr == nil && file != nil {
			filename = file.Name
		}
	}

	// Notify the grantee in real-time about the pending invitation so their UI refreshes without polling
	if s.hub != nil && s.users != nil {
		if grantee, err := s.users.GetByEmail(r.Context(), req.GranteeEmail); err == nil && grantee != nil {
			s.hub.NotifyUser(grantee.ID, wsSync.NotificationEvent{
				Type: wsSync.EventShareInvitation,
				Payload: map[string]string{
					"invitation_id": perm.ID,
					"file_id":       fileID,
					"filename":      filename,
					"role":          req.Role,
					"shared_by":     sender.Email,
					"message":       req.Message,
					"expires_at":    expiresAt.Format(time.RFC3339),
				},
			})
		}
	}

	// Dispatch asynchronous background email notification
	if s.mailer != nil {
		baseURL := getEnvOrDefault("APP_BASE_URL", "http://localhost:5173")
		link := strings.TrimRight(baseURL, "/") + "/dashboard"
		go func() {
			_ = s.mailer.SendShareNotificationEmail(req.GranteeEmail, sender.Email, filename, req.Role, link)
		}()
	}

	// Audit: FILE_SHARED — non-blocking.
	s.auditLog.Log(r.Context(), audit.Entry{
		UserID:       userID,
		Action:       audit.ActionFileShared,
		ResourceType: audit.ResourceFile,
		ResourceID:   fileID,
		Metadata:     audit.MarshalMeta(map[string]string{"grantee": req.GranteeEmail, "role": req.Role, "status": domain.PermissionStatusPending}),
		ClientIP:     r.RemoteAddr,
	})

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

// HandleListInvitations implements GET /api/shares/invitations.
// Returns all active, unexpired PENDING share invitations for the authenticated user.
func (s *Server) HandleListInvitations(w http.ResponseWriter, r *http.Request) {
	if s.perms == nil || s.users == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "permissions service unavailable",
		})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	user, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load user profile"})
		return
	}

	invitations, err := s.perms.ListPendingInvitations(r.Context(), user.Email)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if invitations == nil {
		invitations = []*domain.ShareInvitation{}
	}

	writeJSON(w, http.StatusOK, invitations)
}

// HandleAcceptInvitation implements POST /api/shares/invitations/{id}/accept.
// Accepts an unexpired PENDING invitation, transitioning it to ACCEPTED.
func (s *Server) HandleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	if s.perms == nil || s.users == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "permissions service unavailable",
		})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	user, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load user profile"})
		return
	}

	invID := chi.URLParam(r, "id")
	if invID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing invitation id"})
		return
	}

	perm, err := s.perms.RespondToInvitation(r.Context(), invID, user.Email, domain.PermissionStatusAccepted)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Notify the sender if available that their invitation was accepted
	if s.hub != nil && perm.InvitedBy != nil && *perm.InvitedBy != "" {
		s.hub.NotifyUser(*perm.InvitedBy, wsSync.NotificationEvent{
			Type: wsSync.EventFileShared,
			Payload: map[string]string{
				"file_id":     perm.FileID,
				"accepted_by": user.Email,
				"status":      "ACCEPTED",
			},
		})
	}

	// Trigger real-time sync update on recipient's connection so the shared file view refreshes immediately
	if s.hub != nil {
		s.hub.NotifyUser(userID, wsSync.NotificationEvent{
			Type: wsSync.EventSyncDelta,
			Payload: map[string]string{
				"action":  "INVITATION_ACCEPTED",
				"file_id": perm.FileID,
			},
		})
	}

	s.auditLog.Log(r.Context(), audit.Entry{
		UserID:       userID,
		Action:       "INVITATION_ACCEPTED",
		ResourceType: audit.ResourceFile,
		ResourceID:   perm.FileID,
		Metadata:     audit.MarshalMeta(map[string]string{"invitation_id": invID, "role": perm.Role}),
		ClientIP:     r.RemoteAddr,
	})

	writeJSON(w, http.StatusOK, perm)
}

// HandleDeclineInvitation implements POST /api/shares/invitations/{id}/decline.
// Declines an unexpired PENDING invitation, moving it to DECLINED.
func (s *Server) HandleDeclineInvitation(w http.ResponseWriter, r *http.Request) {
	if s.perms == nil || s.users == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "permissions service unavailable",
		})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	user, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load user profile"})
		return
	}

	invID := chi.URLParam(r, "id")
	if invID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing invitation id"})
		return
	}

	perm, err := s.perms.RespondToInvitation(r.Context(), invID, user.Email, domain.PermissionStatusDeclined)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	s.auditLog.Log(r.Context(), audit.Entry{
		UserID:       userID,
		Action:       "INVITATION_DECLINED",
		ResourceType: audit.ResourceFile,
		ResourceID:   perm.FileID,
		Metadata:     audit.MarshalMeta(map[string]string{"invitation_id": invID}),
		ClientIP:     r.RemoteAddr,
	})

	writeJSON(w, http.StatusOK, map[string]string{"message": "invitation declined"})
}

// HandleBlockSender implements POST /api/shares/invitations/{id}/block.
// Blocks future invitations from the sender and declines this invitation.
func (s *Server) HandleBlockSender(w http.ResponseWriter, r *http.Request) {
	if s.perms == nil || s.users == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "permissions service unavailable",
		})
		return
	}

	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	user, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load user profile"})
		return
	}

	invID := chi.URLParam(r, "id")
	if invID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing invitation id"})
		return
	}

	inv, err := s.perms.GetInvitationByID(r.Context(), invID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "invitation not found"})
		return
	}
	if !strings.EqualFold(inv.GranteeEmail, user.Email) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not authorised to manage this invitation"})
		return
	}

	// Block the sender if sender user ID is present
	if inv.InvitedBy != nil && *inv.InvitedBy != "" {
		if senderUser, sErr := s.users.GetByID(r.Context(), *inv.InvitedBy); sErr == nil && senderUser != nil {
			_ = s.perms.BlockUser(r.Context(), userID, senderUser.Email)
		}
	}

	// Automatically decline the pending invitation
	_, _ = s.perms.RespondToInvitation(r.Context(), invID, user.Email, domain.PermissionStatusDeclined)

	s.auditLog.Log(r.Context(), audit.Entry{
		UserID:       userID,
		Action:       "USER_BLOCKED_FROM_INVITATION",
		ResourceType: audit.ResourceFile,
		ResourceID:   inv.FileID,
		Metadata:     audit.MarshalMeta(map[string]string{"invitation_id": invID}),
		ClientIP:     r.RemoteAddr,
	})

	writeJSON(w, http.StatusOK, map[string]string{"message": "sender blocked and invitation declined"})
}

// HandlePreviewInvitation implements GET /api/shares/invitations/{id}/preview.
// Streams an isolated read-only preview of a file in PENDING invitation state.
func (s *Server) HandlePreviewInvitation(w http.ResponseWriter, r *http.Request) {
	if s.perms == nil || s.users == nil || s.files == nil || s.fileOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "preview service unavailable",
		})
		return
	}

	userID, code, msg := s.userFromQueryToken(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	user, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load user profile"})
		return
	}

	invID := chi.URLParam(r, "id")
	if invID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing invitation id"})
		return
	}

	inv, err := s.perms.GetInvitationByID(r.Context(), invID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "invitation not found"})
		return
	}

	if !strings.EqualFold(inv.GranteeEmail, user.Email) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not authorised to preview this invitation"})
		return
	}

	if inv.Status != domain.PermissionStatusPending {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invitation is no longer pending"})
		return
	}
	if inv.ExpiresAt != nil && inv.ExpiresAt.Before(time.Now()) {
		writeJSON(w, http.StatusGone, map[string]string{"error": "invitation has expired"})
		return
	}

	file, err := s.files.GetByID(r.Context(), inv.FileID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	if file.IsDirectory {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot preview a directory directly"})
		return
	}

	// Security sandbox headers: prevent running scripts or navigating outside the sandbox
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Ensure query parameter has inline=true for streamSingleFile
	q := r.URL.Query()
	q.Set("inline", "true")
	r.URL.RawQuery = q.Encode()

	s.streamSingleFile(w, r, file)
}
