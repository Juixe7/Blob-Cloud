package httpx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"go-drive-clone/internal/audit"
	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
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
	if s.perms == nil || s.users == nil || s.files == nil || s.db == nil || s.journalWriter == nil {
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
	file, err := s.files.GetByID(r.Context(), fileID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	if isShortcutFile(file) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "share the canonical file instead of its shortcut"})
		return
	}
	if file.Status != "ACTIVE" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot share a file until processing is complete"})
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
	if req.Role == domain.RoleOwner {
		fileOwner, ownerErr := s.files.GetByID(r.Context(), fileID)
		if ownerErr != nil || fileOwner.UserID != userID {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the file owner can grant OWNER access"})
			return
		}
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
	var granteeID string
	if grantee, err := s.users.GetByEmail(r.Context(), req.GranteeEmail); err == nil && grantee != nil {
		granteeID = grantee.ID
	}
	entries := shareJournalEntries(file, domain.ActionShareInvitation, userID)
	if granteeID != "" {
		entries = append(entries, shareJournalEntries(file, domain.ActionShareInvitation, granteeID)...)
	}
	if err := s.recordMutationEvents(r.Context(), func(tx postgresrepo.DBTX) error {
		currentFile, err := s.requireSharePermissionInTx(r.Context(), tx, fileID, userID, sender.Email)
		if err != nil {
			return err
		}
		if currentFile.DeletedAt != nil || currentFile.Status != "ACTIVE" {
			return errors.New("cannot share a file until it is active")
		}
		if isShortcutFile(currentFile) {
			return errors.New("cannot share a shortcut; share the canonical file")
		}
		if req.Role == domain.RoleOwner && currentFile.UserID != userID {
			return errors.New("only the file owner can grant OWNER access")
		}
		return s.perms.WithTx(tx).GrantPermission(r.Context(), perm)
	}, entries); err != nil {
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
		if file, fErr := s.fileOps.GetFile(r.Context(), fileID); fErr == nil && file != nil {
			filename = file.Name
		}
	}

	// Notify the grantee in real-time about the pending invitation so their UI refreshes without polling
	if granteeID != "" {
		s.notifyUser(granteeID, wsSync.NotificationEvent{
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

	// Dispatch asynchronous background email notification
	if s.mailer != nil {
		baseURL := getAppBaseURL()
		link := baseURL + "/dashboard"
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

func shareJournalEntries(file *domain.File, action, userID string) []*domain.JournalEntry {
	if file == nil || userID == "" {
		return nil
	}
	return []*domain.JournalEntry{{
		UserID: userID, FileID: file.ID, Action: action, ParentID: file.ParentID,
		Name: file.Name, IsDirectory: file.IsDirectory, SizeBytes: file.SizeBytes,
		MimeType: file.MimeType, Status: file.Status,
	}}
}

// HandleListPermissions implements GET /api/files/{id}/permissions.
//
// Lists the direct grants on a file (does not walk the folder hierarchy).
// Requires authentication.
func (s *Server) HandleListPermissions(w http.ResponseWriter, r *http.Request) {
	if s.perms == nil || s.files == nil || s.users == nil || s.db == nil {
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

	// A VIEWER may read file content, but the direct ACL contains other users'
	// email addresses, invitation state, and messages. Check the manager role
	// and read the roster in one transaction so revocation cannot race between
	// the authorization check and the sensitive query.
	var grants []*domain.Permission
	err := postgresrepo.RunInTx(r.Context(), s.db, func(tx postgresrepo.DBTX) error {
		// Read first without a row lock so collaborators can lock their effective
		// grant before locking the file. Share mutations use that same
		// grant-then-file order; reversing it here can deadlock with a revoke or
		// role update that already holds the grant row.
		file, err := s.files.WithTx(tx).GetByID(r.Context(), fileID)
		if err != nil {
			return err
		}
		if file.DeletedAt != nil || file.Status == "TRASHED" || file.Status == "QUARANTINED" || (file.Status != "" && file.Status != "ACTIVE") {
			return errors.New("file is unavailable")
		}
		if file.UserID != userID {
			user, err := s.users.WithTx(tx).GetByID(r.Context(), userID)
			if err != nil {
				return fmt.Errorf("resolve user: %w", err)
			}
			allowed, err := s.perms.WithTx(tx).CheckUserPermission(r.Context(), fileID, user.Email, []string{domain.RoleEditor, domain.RoleOwner})
			if err != nil {
				return err
			}
			if !allowed {
				return errors.New("access denied")
			}
		}
		// Do not lock the file row for this read. The grant lock above protects
		// the authorization decision through the roster query; taking a file
		// lock here could deadlock with a role update that holds another actor's
		// grant and then locks this file before changing the roster.
		grants, err = s.perms.WithTx(tx).GetPermissionsByFile(r.Context(), fileID)
		return err
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		} else if strings.Contains(err.Error(), "access denied") || strings.Contains(err.Error(), "unavailable") {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		} else {
			s.log.Error("failed to list permission roster", "file_id", fileID, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list permissions"})
		}
		return
	}
	if grants == nil {
		grants = []*domain.Permission{}
	}
	writeJSON(w, http.StatusOK, grants)
}

// HandleUpdateShare implements PATCH /api/files/{id}/share/{email}.
// Updates the role of an existing permission.
// Requires OWNER or EDITOR permission on the file.
func (s *Server) HandleUpdateShare(w http.ResponseWriter, r *http.Request) {
	if s.perms == nil || s.users == nil || s.files == nil || s.db == nil || s.journalWriter == nil {
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
	actor, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}

	file, err := s.files.GetByID(r.Context(), fileID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	grants, err := s.perms.GetPermissionsByFile(r.Context(), fileID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load permission"})
		return
	}
	var targetGrant *domain.Permission
	for _, grant := range grants {
		if strings.EqualFold(strings.TrimSpace(grant.GranteeEmail), strings.TrimSpace(granteeEmail)) {
			targetGrant = grant
			break
		}
	}
	if targetGrant == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "permission not found"})
		return
	}
	if file.UserID != userID && (targetGrant.Role == domain.RoleOwner || req.Role == domain.RoleOwner) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the file owner can modify OWNER access"})
		return
	}
	var recipientID string
	if grantee, err := s.users.GetByEmail(r.Context(), granteeEmail); err == nil && grantee != nil {
		recipientID = grantee.ID
	}
	entries := shareJournalEntries(file, domain.ActionShareUpdated, userID)
	if recipientID != "" {
		entries = append(entries, shareJournalEntries(file, domain.ActionShareUpdated, recipientID)...)
	}
	if err := s.recordMutationEvents(r.Context(), func(tx postgresrepo.DBTX) error {
		currentFile, err := s.requireSharePermissionInTx(r.Context(), tx, fileID, userID, actor.Email)
		if err != nil {
			return err
		}
		currentGrant, err := s.perms.WithTx(tx).GetPermissionByFileAndGranteeForUpdate(r.Context(), fileID, granteeEmail)
		if err != nil {
			return err
		}
		if currentFile.UserID != userID && (currentGrant.Role == domain.RoleOwner || req.Role == domain.RoleOwner) {
			return errors.New("only the file owner can modify OWNER access")
		}
		return s.perms.WithTx(tx).UpdatePermission(r.Context(), fileID, granteeEmail, req.Role)
	}, entries); err != nil {
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
	if s.perms == nil || s.users == nil || s.files == nil || s.db == nil || s.journalWriter == nil {
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
	actor, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}

	file, err := s.files.GetByID(r.Context(), fileID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	grants, err := s.perms.GetPermissionsByFile(r.Context(), fileID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load permission"})
		return
	}
	for _, grant := range grants {
		if strings.EqualFold(strings.TrimSpace(grant.GranteeEmail), strings.TrimSpace(granteeEmail)) && grant.Role == domain.RoleOwner && file.UserID != userID {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the file owner can revoke OWNER access"})
			return
		}
	}
	var recipientID string
	if grantee, err := s.users.GetByEmail(r.Context(), granteeEmail); err == nil && grantee != nil {
		recipientID = grantee.ID
	}
	entries := shareJournalEntries(file, domain.ActionShareRevoked, userID)
	if recipientID != "" {
		entries = append(entries, shareJournalEntries(file, domain.ActionShareRevoked, recipientID)...)
	}
	if err := s.recordMutationEvents(r.Context(), func(tx postgresrepo.DBTX) error {
		currentFile, err := s.requireSharePermissionInTx(r.Context(), tx, fileID, userID, actor.Email)
		if err != nil {
			return err
		}
		currentGrant, err := s.perms.WithTx(tx).GetPermissionByFileAndGranteeForUpdate(r.Context(), fileID, granteeEmail)
		if err != nil {
			return err
		}
		if currentFile.UserID != userID && currentGrant.Role == domain.RoleOwner {
			return errors.New("only the file owner can revoke OWNER access")
		}
		return s.perms.WithTx(tx).RevokePermission(r.Context(), fileID, granteeEmail)
	}, entries); err != nil {
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
	if s.perms == nil || s.users == nil || s.files == nil || s.db == nil || s.journalWriter == nil {
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

	invitation, err := s.perms.GetInvitationByIDForUser(r.Context(), invID, user.Email)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invitation not found"})
		return
	}
	file, err := s.files.GetByID(r.Context(), invitation.FileID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "shared file no longer exists"})
		return
	}
	if isShortcutFile(file) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invitation targets a shortcut; ask the sender to share the canonical file"})
		return
	}
	entries := shareJournalEntries(file, domain.ActionShareAccepted, userID)
	if invitation.InvitedBy != nil {
		entries = append(entries, shareJournalEntries(file, domain.ActionShareAccepted, *invitation.InvitedBy)...)
	}
	var perm *domain.Permission
	if err := s.recordMutationEvents(r.Context(), func(tx postgresrepo.DBTX) error {
		currentFile, err := s.files.WithTx(tx).GetByIDForUpdate(r.Context(), invitation.FileID)
		if err != nil {
			return err
		}
		if currentFile.DeletedAt != nil || currentFile.Status != "ACTIVE" {
			return errors.New("shared file is unavailable")
		}
		if isShortcutFile(currentFile) {
			return errors.New("invitation targets a shortcut; share the canonical file")
		}
		perm, err = s.perms.WithTx(tx).RespondToInvitation(r.Context(), invID, user.Email, domain.PermissionStatusAccepted)
		return err
	}, entries); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Notify the sender if available that their invitation was accepted
	if perm.InvitedBy != nil && *perm.InvitedBy != "" {
		s.notifyUser(*perm.InvitedBy, wsSync.NotificationEvent{
			Type: wsSync.EventFileShared,
			Payload: map[string]string{
				"file_id":     perm.FileID,
				"accepted_by": user.Email,
				"status":      "ACCEPTED",
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
	if s.perms == nil || s.users == nil || s.files == nil || s.db == nil || s.journalWriter == nil {
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

	invitation, err := s.perms.GetInvitationByIDForUser(r.Context(), invID, user.Email)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invitation not found"})
		return
	}
	file, err := s.files.GetByID(r.Context(), invitation.FileID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "shared file no longer exists"})
		return
	}
	entries := shareJournalEntries(file, domain.ActionShareDeclined, userID)
	if invitation.InvitedBy != nil {
		entries = append(entries, shareJournalEntries(file, domain.ActionShareDeclined, *invitation.InvitedBy)...)
	}
	var perm *domain.Permission
	if err := s.recordMutationEvents(r.Context(), func(tx postgresrepo.DBTX) error {
		var err error
		perm, err = s.perms.WithTx(tx).RespondToInvitation(r.Context(), invID, user.Email, domain.PermissionStatusDeclined)
		return err
	}, entries); err != nil {
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
	if s.perms == nil || s.users == nil || s.files == nil || s.db == nil || s.journalWriter == nil {
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

	inv, err := s.perms.GetInvitationByIDForUser(r.Context(), invID, user.Email)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "invitation not found"})
		return
	}
	file, err := s.files.GetByID(r.Context(), inv.FileID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "shared file no longer exists"})
		return
	}
	var senderEmail string
	if inv.InvitedBy != nil && *inv.InvitedBy != "" {
		senderUser, senderErr := s.users.GetByID(r.Context(), *inv.InvitedBy)
		if senderErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load invitation sender"})
			return
		}
		senderEmail = senderUser.Email
	}
	entries := shareJournalEntries(file, domain.ActionShareDeclined, userID)
	if inv.InvitedBy != nil && *inv.InvitedBy != "" {
		entries = append(entries, shareJournalEntries(file, domain.ActionShareDeclined, *inv.InvitedBy)...)
	}
	if err := s.recordMutationEvents(r.Context(), func(tx postgresrepo.DBTX) error {
		perms := s.perms.WithTx(tx)
		if senderEmail != "" {
			if err := perms.BlockUser(r.Context(), userID, senderEmail); err != nil {
				return err
			}
		}
		_, err := perms.RespondToInvitation(r.Context(), invID, user.Email, domain.PermissionStatusDeclined)
		return err
	}, entries); err != nil {
		s.log.Error("failed to block sender and decline invitation", "invitation_id", invID, "err", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "invitation is no longer pending or could not be processed"})
		return
	}

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
	if s.perms == nil || s.users == nil || s.files == nil || s.fileOps == nil || s.blocks == nil || s.storage == nil {
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
	if isShortcutFile(file) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "invitation not found"})
		return
	}
	if file.IsDirectory {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot preview a directory directly"})
		return
	}
	if file.Status != "ACTIVE" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "file is unavailable until processing is complete"})
		return
	}

	// Security preview headers: prevent script execution while permitting browser document rendering
	w.Header().Set("Content-Security-Policy", "script-src 'none'; object-src 'self' data: blob:;")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Ensure query parameter has inline=true for streamSingleFile
	q := r.URL.Query()
	q.Set("inline", "true")
	r.URL.RawQuery = q.Encode()

	s.streamSingleFile(w, r, file)
}

// HandleRemoveSharedWithMe implements DELETE /api/shares/shared-with-me/{id}.
// It allows a collaborator (recipient) to remove a shared file from their own "Shared with me" view.
// It deletes or revokes the caller's permission record for the file without modifying the file or affecting other collaborators.
func (s *Server) HandleRemoveSharedWithMe(w http.ResponseWriter, r *http.Request) {
	if s.perms == nil || s.users == nil || s.files == nil || s.db == nil || s.journalWriter == nil {
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

	fileID := chi.URLParam(r, "id")
	if fileID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file id"})
		return
	}

	user, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}
	file, err := s.files.GetByID(r.Context(), fileID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	grants, err := s.perms.GetPermissionsByFile(r.Context(), fileID)
	if err != nil {
		s.log.Error("failed to load shared permission", "file_id", fileID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove shared file"})
		return
	}
	grantExists := false
	for _, grant := range grants {
		if strings.EqualFold(strings.TrimSpace(grant.GranteeEmail), strings.TrimSpace(user.Email)) {
			grantExists = true
			break
		}
	}
	if !grantExists {
		writeJSON(w, http.StatusOK, map[string]string{"message": "removed from shared with me"})
		return
	}

	entries := shareJournalEntries(file, domain.ActionShareRevoked, userID)
	if file.UserID != "" && file.UserID != userID {
		entries = append(entries, shareJournalEntries(file, domain.ActionShareRevoked, file.UserID)...)
	}
	if err := s.recordMutationEvents(r.Context(), func(tx postgresrepo.DBTX) error {
		return s.perms.WithTx(tx).RevokePermission(r.Context(), fileID, user.Email)
	}, entries); err != nil {
		if errors.Is(err, domain.ErrPermissionNotFound) {
			writeJSON(w, http.StatusOK, map[string]string{"message": "removed from shared with me"})
			return
		}
		s.log.Error("failed to remove shared file for user", "file_id", fileID, "email", user.Email, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove shared file"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "removed from shared with me"})
}
