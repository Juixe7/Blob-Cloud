package httpx

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"go-drive-clone/internal/auth"

	"golang.org/x/crypto/bcrypt"
)

type revokeSessionRequest struct {
	SessionID string `json:"session_id"`
	Password  string `json:"password"`
}

type revokeAllRequest struct {
	Password string `json:"password"`
}

// HandleListSessions implements GET /api/user/sessions.
func (s *Server) HandleListSessions(w http.ResponseWriter, r *http.Request) {
	userID, currentSessionID, code, msg := s.userAndSessionFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	if s.sessions == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "session repository unconfigured"})
		return
	}

	sessions, err := s.sessions.GetSessionsByUserID(r.Context(), userID)
	if err != nil {
		s.log.Error("list sessions failed", "user_id", userID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list user sessions"})
		return
	}

	for _, sess := range sessions {
		if sess.ID == currentSessionID {
			sess.IsCurrent = true
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": sessions,
		"total":    len(sessions),
	})
}

// HandleRevokeSession implements POST /api/user/sessions/revoke.
// Requires password verification for security.
func (s *Server) HandleRevokeSession(w http.ResponseWriter, r *http.Request) {
	userID, currentSessionID, code, msg := s.userAndSessionFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	if s.sessions == nil || s.users == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "service unconfigured"})
		return
	}

	var req revokeSessionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Password = strings.TrimSpace(req.Password)

	if req.SessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}
	if req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password is required to confirm session revocation"})
		return
	}

	// Verify user password
	user, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "incorrect password"})
		return
	}

	// Revoke the target session
	if err := s.sessions.DeleteSession(r.Context(), req.SessionID, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		s.log.Error("revoke session failed", "session_id", req.SessionID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to revoke session"})
		return
	}

	if s.hub != nil {
		s.hub.KickSession(req.SessionID)
	}

	isCurrentRevoked := (req.SessionID == currentSessionID)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":             "revoked",
		"revoked_session_id": req.SessionID,
		"is_current_revoked": isCurrentRevoked,
	})
}

// HandleRevokeAllOtherSessions implements POST /api/user/sessions/revoke-all.
// Requires password verification.
func (s *Server) HandleRevokeAllOtherSessions(w http.ResponseWriter, r *http.Request) {
	userID, currentSessionID, code, msg := s.userAndSessionFromBearer(r)
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	if s.sessions == nil || s.users == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "service unconfigured"})
		return
	}

	var req revokeAllRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	req.Password = strings.TrimSpace(req.Password)
	if req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password is required to confirm revoking all other sessions"})
		return
	}

	// Verify user password
	user, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "incorrect password"})
		return
	}

	// Fetch active sessions to know which ones to kick
	sessions, err := s.sessions.GetSessionsByUserID(r.Context(), userID)
	if err == nil && s.hub != nil {
		for _, sess := range sessions {
			if sess.ID != currentSessionID {
				s.hub.KickSession(sess.ID)
			}
		}
	}

	count, err := s.sessions.DeleteOtherSessions(r.Context(), currentSessionID, userID)
	if err != nil {
		s.log.Error("revoke all other sessions failed", "user_id", userID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to revoke other sessions"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "revoked_other_sessions",
		"revoked_count": count,
	})
}

// userAndSessionFromBearer extracts both user_id and session_id from JWT bearer token.
func (s *Server) userAndSessionFromBearer(r *http.Request) (string, string, int, string) {
	if s.jwtSecret == "" {
		return "", "", http.StatusServiceUnavailable, "authentication not configured"
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return "", "", http.StatusUnauthorized, "missing or invalid authorization header"
	}
	tokenStr := strings.TrimPrefix(header, "Bearer ")
	claims, err := auth.ValidateToken(s.jwtSecret, tokenStr)
	if err != nil {
		return "", "", http.StatusUnauthorized, "invalid or expired token"
	}
	if claims.UserID == "" {
		return "", "", http.StatusUnauthorized, "token has no user identity"
	}
	if claims.IsRefresh {
		return "", "", http.StatusUnauthorized, "refresh tokens cannot be used as bearer tokens"
	}
	if s.users != nil {
		if _, err := s.users.GetByID(r.Context(), claims.UserID); err != nil {
			return "", "", http.StatusUnauthorized, "user session invalid or user not found"
		}
	}
	if claims.SessionID != "" && s.sessions != nil {
		if _, err := s.sessions.GetSessionByID(r.Context(), claims.SessionID); err != nil {
			return "", "", http.StatusUnauthorized, "session has been revoked or expired"
		}
		// Touch session last_seen_at
		_ = s.sessions.TouchSession(r.Context(), claims.SessionID)
	}
	return claims.UserID, claims.SessionID, 0, ""
}
