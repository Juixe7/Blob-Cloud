package httpx

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"go-drive-clone/internal/auth"
	"go-drive-clone/internal/domain"
	postgresrepo "go-drive-clone/internal/repository/postgres"
)

// generateSecureToken generates a random 64-character hex string.
func generateSecureToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type CreateLinkRequest struct {
	AccessTier string     `json:"access_tier"`
	Password   string     `json:"password"`
	ExpiresAt  *time.Time `json:"expires_at"`
}

type CreateLinkResponse struct {
	LinkToken string `json:"link_token"`
	ShareURL  string `json:"share_url"`
}

var errPublicLinkAccessDenied = errors.New("public link management access denied")

func (s *Server) HandleCreatePublicLink(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil || s.perms == nil || s.users == nil || s.files == nil || s.fileOps == nil || s.db == nil || s.journalWriter == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sharing service unavailable"})
		return
	}

	fileID := chi.URLParam(r, "id")
	userID, status, msg := s.userFromBearer(r)
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}

	user, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}
	file, err := s.files.GetByID(r.Context(), fileID)
	if err != nil || file.Status != "ACTIVE" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "file is unavailable until processing is complete"})
		return
	}
	if isShortcutFile(file) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "create the link from the canonical file, not its shortcut"})
		return
	}

	// Verify owner/editor access
	role, err := s.fileOps.GetResolvedPermission(r.Context(), fileID, user.Email)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}
	if role != "OWNER" && role != "EDITOR" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only owners or editors can share publicly"})
		return
	}

	var req CreateLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	if req.AccessTier == "" {
		req.AccessTier = domain.RoleViewer
	}
	if req.AccessTier != domain.RoleViewer {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "public links are read-only; access_tier must be VIEWER"})
		return
	}

	link := &domain.ShareableLink{
		FileID:     fileID,
		LinkToken:  generateSecureToken(),
		AccessTier: req.AccessTier,
		ExpiresAt:  req.ExpiresAt,
	}

	if req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 10)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
			return
		}
		hashStr := string(hash)
		link.PasswordHash = &hashStr
	}

	entries, err := s.journalEntriesForFile(r.Context(), file, domain.ActionPublicLinkCreated, userID)
	if err != nil {
		s.log.Error("failed to resolve public link event recipients", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create share link"})
		return
	}
	if err := s.recordMutationEvents(r.Context(), func(tx postgresrepo.DBTX) error {
		currentFile, err := s.requireSharePermissionInTx(r.Context(), tx, fileID, userID, user.Email)
		if err != nil {
			return err
		}
		if currentFile.DeletedAt != nil || currentFile.Status != "ACTIVE" {
			return errors.New("file is unavailable for public sharing")
		}
		if isShortcutFile(currentFile) {
			return errors.New("cannot create a public link for a shortcut")
		}
		return postgresrepo.NewShareableLinkRepository(tx).Create(r.Context(), link)
	}, entries); err != nil {
		s.log.Error("failed to create share link", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create share link"})
		return
	}

	baseURL := getAppBaseURL()
	shareURL := baseURL + "/share/" + link.LinkToken

	writeJSON(w, http.StatusCreated, CreateLinkResponse{
		LinkToken: link.LinkToken,
		ShareURL:  shareURL,
	})
}

// HandleRevokePublicLink implements DELETE /api/files/{id}/links/{token}.
func (s *Server) HandleRevokePublicLink(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil || s.perms == nil || s.users == nil || s.files == nil || s.db == nil || s.journalWriter == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sharing service unavailable"})
		return
	}
	userID, status, msg := s.userFromBearer(r)
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	fileID := chi.URLParam(r, "id")
	token := chi.URLParam(r, "token")
	if fileID == "" || token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file id and link token are required"})
		return
	}
	link, err := s.shares.GetByToken(r.Context(), token)
	if err != nil || link.FileID != fileID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "link not found"})
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
	entries, err := s.journalEntriesForFile(r.Context(), file, domain.ActionPublicLinkRevoked, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to revoke link"})
		return
	}
	err = s.recordMutationEvents(r.Context(), func(tx postgresrepo.DBTX) error {
		currentFile, err := s.files.WithTx(tx).GetByID(r.Context(), fileID)
		if err != nil {
			return err
		}
		if currentFile.UserID != userID {
			allowed, err := s.perms.WithTx(tx).CheckUserPermission(r.Context(), fileID, user.Email, []string{domain.RoleOwner, domain.RoleEditor})
			if err != nil {
				return err
			}
			if !allowed {
				return errPublicLinkAccessDenied
			}
		}
		return s.shares.WithTx(tx).DeleteByFileAndToken(r.Context(), fileID, token)
	}, entries)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "link not found"})
		return
	}
	if err != nil {
		if errors.Is(err, errPublicLinkAccessDenied) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "only owners or editors can revoke public links"})
			return
		}
		s.log.Error("failed to revoke public link", "file_id", fileID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to revoke link"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "public link revoked"})
}

// Fetch Public Share Metadata
// extractPublicShareToken retrieves the share session token from cookie, header, or query.
func extractPublicShareToken(r *http.Request, token string) string {
	if cookie, err := r.Cookie("share_session_" + token); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	if shareHdr := r.Header.Get("X-Share-Token"); shareHdr != "" {
		return shareHdr
	}
	if q := r.URL.Query().Get("share_token"); q != "" {
		return q
	}
	if authHdr := r.Header.Get("Authorization"); strings.HasPrefix(authHdr, "Bearer ") {
		return strings.TrimPrefix(authHdr, "Bearer ")
	}
	return ""
}

func (s *Server) HandleGetPublicShare(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil || s.fileOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "public sharing service unavailable"})
		return
	}

	token := chi.URLParam(r, "token")
	link, err := s.shares.GetByToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "link not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}

	if link.ExpiresAt != nil && time.Now().After(*link.ExpiresAt) {
		writeJSON(w, http.StatusGone, map[string]string{"error": "link expired"})
		return
	}

	// Password protection check via cookie or header/query (token generated by verify)
	var hasVerified bool
	if rawToken := extractPublicShareToken(r, token); rawToken != "" {
		claims, err := auth.ValidateToken(s.jwtSecret, rawToken)
		if err == nil && claims.UserID == "public" && claims.SessionID == token {
			hasVerified = true
		}
	}

	if link.PasswordHash != nil && !hasVerified {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "password_required"})
		return
	}

	file, err := s.fileOps.GetFile(r.Context(), link.FileID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	if isShortcutFile(file) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "link not found"})
		return
	}
	if file.DeletedAt != nil || file.Status != "ACTIVE" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file is unavailable"})
		return
	}

	// Remove sensitive info
	file.Role = link.AccessTier
	file.UserID = ""

	var children []*domain.File
	if file.IsDirectory && s.files != nil {
		children, err = s.files.ListDirectory(r.Context(), "", &file.ID)
		if err == nil {
			visible := children[:0]
			for _, child := range children {
				if child.DeletedAt != nil || child.Status != "ACTIVE" {
					continue
				}
				child.Role = link.AccessTier
				child.UserID = ""
				visible = append(visible, child)
			}
			children = visible
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"file":     file,
		"children": children,
	})
}

type VerifyPublicShareRequest struct {
	Password string `json:"password"`
}

func (s *Server) HandleVerifyPublicShare(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil || s.jwtSecret == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "public sharing service unavailable"})
		return
	}
	token := chi.URLParam(r, "token")
	link, err := s.shares.GetByToken(r.Context(), token)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "link not found"})
		return
	}

	if link.ExpiresAt != nil && time.Now().After(*link.ExpiresAt) {
		writeJSON(w, http.StatusGone, map[string]string{"error": "link expired"})
		return
	}

	var req VerifyPublicShareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	if link.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*link.PasswordHash), []byte(req.Password)) != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid password"})
		return
	}

	// Issue JWT
	jwtToken, err := auth.CreateTokenWithSession(s.jwtSecret, "public", token)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate token"})
		return
	}

	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     "share_session_" + token,
		Value:    jwtToken,
		Path:     "/api/public/shares/" + token,
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "verified",
		"token":   jwtToken,
	})
}

func (s *Server) HandlePublicDownload(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil || s.fileOps == nil || s.zipOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "public download service unavailable"})
		return
	}
	token := chi.URLParam(r, "token")
	link, err := s.shares.GetByToken(r.Context(), token)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "link not found"})
		return
	}

	if link.ExpiresAt != nil && time.Now().After(*link.ExpiresAt) {
		writeJSON(w, http.StatusGone, map[string]string{"error": "link expired"})
		return
	}

	// Verify JWT if password-protected
	if link.PasswordHash != nil {
		rawToken := extractPublicShareToken(r, token)
		if rawToken == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		claims, err := auth.ValidateToken(s.jwtSecret, rawToken)
		if err != nil || claims.SessionID != token || claims.UserID != "public" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
	}

	// Bypass Auth context, just download the file
	w.Header().Set("X-Accel-Buffering", "no")

	file, err := s.fileOps.GetFile(r.Context(), link.FileID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	if isShortcutFile(file) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "link not found"})
		return
	}
	if file.DeletedAt != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file has been deleted"})
		return
	}
	if file.Status != "ACTIVE" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "file is unavailable until processing is complete"})
		return
	}

	if !file.IsDirectory {
		s.streamSingleFile(w, r, file)
		return
	}

	ids := []string{link.FileID}
	if err := s.zipOps.StreamItemsZip(r.Context(), ids, file.UserID, w); err != nil {
		s.log.Error("failed to stream public zip", "err", err)
	}
}

// HandlePublicRecordView records a view for a public share if the user is authenticated.
func (s *Server) HandlePublicRecordView(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil || s.files == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "public sharing service unavailable"})
		return
	}
	token := chi.URLParam(r, "token")
	link, err := s.shares.GetByToken(r.Context(), token)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "link not found"})
		return
	}

	if link.ExpiresAt != nil && time.Now().After(*link.ExpiresAt) {
		writeJSON(w, http.StatusGone, map[string]string{"error": "link expired"})
		return
	}

	// We only record if the user is actually authenticated
	userID, code, _ := s.userFromBearer(r)
	if code == 0 && userID != "" {
		err = s.files.RecordView(r.Context(), userID, link.FileID)
		if err != nil {
			s.log.Error("failed to record public view", "err", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandlePublicSaveShortcut creates a shortcut in the authenticated user's drive pointing to the shared file.
func (s *Server) HandlePublicSaveShortcut(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil || s.fileOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "public sharing service unavailable"})
		return
	}
	token := chi.URLParam(r, "token")
	link, err := s.shares.GetByToken(r.Context(), token)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "link not found"})
		return
	}

	if link.ExpiresAt != nil && time.Now().After(*link.ExpiresAt) {
		writeJSON(w, http.StatusGone, map[string]string{"error": "link expired"})
		return
	}
	linkedFile, err := s.fileOps.GetFile(r.Context(), link.FileID)
	if err != nil || isShortcutFile(linkedFile) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "link not found"})
		return
	}

	// Password protection check via cookie or header/query
	var hasVerified bool
	if rawToken := extractPublicShareToken(r, token); rawToken != "" {
		claims, err := auth.ValidateToken(s.jwtSecret, rawToken)
		if err == nil && claims.UserID == "public" && claims.SessionID == token {
			hasVerified = true
		}
	}

	if link.PasswordHash != nil && !hasVerified {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "password_required"})
		return
	}

	// Caller MUST be authenticated to save
	userID, code, msg := s.userFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	// Create shortcut in root directory (parentID = nil)
	shortcut, err := s.fileOps.CreateShortcutFromPublicLink(r.Context(), userID, link.FileID, nil, token)
	if err != nil {
		s.log.Error("failed to save public shortcut", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save to drive"})
		return
	}

	writeJSON(w, http.StatusCreated, shortcut)
}
