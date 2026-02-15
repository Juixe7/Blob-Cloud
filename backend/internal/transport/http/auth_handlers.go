package httpx

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/api/idtoken"

	"go-drive-clone/internal/auth"
	"go-drive-clone/internal/domain"

	"golang.org/x/crypto/bcrypt"
)

// authRequest is the JSON body for both register and login endpoints.
type authRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// authResponse is the JSON body returned on successful register/login.
type authResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
}

// verifyRequest is the JSON body for POST /api/auth/verify.
type verifyRequest struct {
	UserID string `json:"user_id"`
	Code   string `json:"code"`
}

// resendRequest is the JSON body for POST /api/auth/resend-verification.
type resendRequest struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

// googleOAuthRequest is the JSON body for POST /api/auth/google.
type googleOAuthRequest struct {
	IDToken string `json:"id_token"`
}

// refreshRequest is the JSON body for the refresh endpoint.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// refreshResponse is the JSON body returned on successful token refresh.
type refreshResponse struct {
	Token string `json:"token"`
}

// forgotPasswordRequest is the JSON body for POST /api/auth/forgot-password.
type forgotPasswordRequest struct {
	Email string `json:"email"`
}

// resetPasswordRequest is the JSON body for POST /api/auth/reset-password.
type resetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// changePasswordRequest is the JSON body for POST /api/auth/change-password.
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// logoutRequest is the JSON body for POST /api/auth/logout.
type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}


// requireUsers guards handlers that depend on the user repository being wired.
func (s *Server) requireUsers(w http.ResponseWriter) bool {
	if s.users == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "user service unavailable (database not configured)",
		})
		return false
	}
	return true
}

// generate6DigitCode creates a secure random 6-digit string.
func generate6DigitCode() string {
	nBig, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "123456"
	}
	return fmt.Sprintf("%06d", nBig.Int64())
}

// randHex generates a random hex string of length n.
func randHex(n int) string {
	bytes := make([]byte, n)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// HandleRegister implements POST /api/auth/register.
func (s *Server) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.requireUsers(w) {
		return
	}
	if s.jwtSecret == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "authentication not configured",
		})
		return
	}

	var req authRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	// Validate input.
	email := strings.TrimSpace(strings.ToLower(req.Email))
	password := req.Password
	if email == "" || password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email and password are required"})
		return
	}
	if len(password) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		return
	}
	if !strings.Contains(email, "@") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email format"})
		return
	}

	// Check email uniqueness.
	existing, err := s.users.GetByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.log.Error("register: lookup email failed", "email", email, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if existing != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "email already registered"})
		return
	}

	// Hash password.
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		s.log.Error("register: bcrypt hash failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Create user with is_verified = false.
	user := &domain.User{
		Email:        email,
		PasswordHash: string(hash),
		IsVerified:   false,
	}
	if err := s.users.Create(r.Context(), user); err != nil {
		if strings.Contains(err.Error(), "23505") || strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "email already registered"})
			return
		}
		s.log.Error("register: create user failed", "email", email, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Generate 6-digit verification code with 15 minute expiry.
	code := generate6DigitCode()
	vc := &domain.VerificationCode{
		UserID:    user.ID,
		Code:      code,
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	if err := s.users.CreateVerificationCode(r.Context(), vc); err != nil {
		s.log.Error("register: create verification code failed", "user_id", user.ID, "err", err)
	}

	// Dispatch email asynchronously in background.
	if s.mailer != nil {
		go func(toEmail, verifyCode string) {
			_ = s.mailer.SendVerificationEmail(toEmail, verifyCode)
		}(email, code)
	} else {
		s.log.Warn("register: mailer unconfigured; verification code", "email", email, "code", code)
	}

	s.log.Info("user registered (verification required)", "user_id", user.ID, "email", email)

	// Return 201 Created indicating verification is required (do not return JWT yet).
	writeJSON(w, http.StatusCreated, map[string]string{
		"status":  "verification_required",
		"user_id": user.ID,
		"email":   email,
	})
}

// HandleLogin implements POST /api/auth/login.
func (s *Server) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.requireUsers(w) {
		return
	}
	if s.jwtSecret == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "authentication not configured",
		})
		return
	}

	var req authRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	password := req.Password
	if email == "" || password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email and password are required"})
		return
	}

	// Look up user.
	user, err := s.users.GetByEmail(r.Context(), email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
			return
		}
		s.log.Error("login: lookup email failed", "email", email, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Verify password.
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	// If user is not verified, reject login with 403 Forbidden.
	if !user.IsVerified {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error":   "verification_required",
			"user_id": user.ID,
			"email":   user.Email,
		})
		return
	}

	// Issue refresh token.
	refreshToken, err := auth.CreateRefreshToken(s.jwtSecret, user.ID)
	if err != nil {
		s.log.Error("login: create refresh token failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Create persistent session record
	sessionID := ""
	if s.sessions != nil {
		hashedToken := fmt.Sprintf("%x", sha256.Sum256([]byte(refreshToken)))
		sess := &domain.UserSession{
			UserID:         user.ID,
			RefreshTokenID: hashedToken,
			UserAgent:      r.Header.Get("User-Agent"),
			DeviceName:     parseDeviceName(r),
			IPAddress:      getClientIP(r),
			IsActive:       true,
			ExpiresAt:      time.Now().Add(auth.RefreshTokenLifetime),
		}
		if err := s.sessions.CreateSession(r.Context(), sess); err != nil {
			s.log.Error("login: create session failed", "err", err)
		} else {
			sessionID = sess.ID
		}
	}

	// Issue access token.
	accessToken, err := auth.CreateTokenWithSession(s.jwtSecret, user.ID, sessionID)
	if err != nil {
		s.log.Error("login: create access token failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	s.log.Info("user logged in", "user_id", user.ID, "email", email, "session_id", sessionID)

	writeJSON(w, http.StatusOK, authResponse{
		Token:        accessToken,
		RefreshToken: refreshToken,
	})
}

// HandleRefresh implements POST /api/auth/refresh.
//
// Accepts {"refresh_token": "..."}, validates it as a refresh-type JWT, and
// returns a new short-lived access token. Rejects access tokens sent here.
func (s *Server) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	if s.jwtSecret == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "authentication not configured",
		})
		return
	}

	var req refreshRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	if req.RefreshToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refresh_token is required"})
		return
	}

	claims, err := auth.ValidateToken(s.jwtSecret, req.RefreshToken)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired refresh token"})
		return
	}

	// Only refresh tokens are accepted at this endpoint.
	if !claims.IsRefresh {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not a refresh token"})
		return
	}

	if claims.UserID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token has no user identity"})
		return
	}

	if s.users != nil {
		if _, err := s.users.GetByID(r.Context(), claims.UserID); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user session invalid or user not found"})
			return
		}
	}

	sessionID := ""
	if s.sessions != nil {
		hashedToken := fmt.Sprintf("%x", sha256.Sum256([]byte(req.RefreshToken)))
		session, err := s.sessions.GetSessionByRefreshTokenID(r.Context(), hashedToken)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session has been revoked or expired"})
			return
		}
		_ = s.sessions.TouchSession(r.Context(), session.ID)
		sessionID = session.ID
	}

	// Issue a new access token bound to the existing session.
	accessToken, err := auth.CreateTokenWithSession(s.jwtSecret, claims.UserID, sessionID)
	if err != nil {
		s.log.Error("refresh: create access token failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, refreshResponse{
		Token: accessToken,
	})
}

// HandleVerify implements POST /api/auth/verify.
func (s *Server) HandleVerify(w http.ResponseWriter, r *http.Request) {
	if !s.requireUsers(w) {
		return
	}
	if s.jwtSecret == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "authentication not configured",
		})
		return
	}

	var req verifyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	userID := strings.TrimSpace(req.UserID)
	code := strings.TrimSpace(req.Code)
	if userID == "" || code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id and code are required"})
		return
	}

	vc, err := s.users.GetVerificationCode(r.Context(), userID, code)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or expired verification code"})
		return
	}

	// Delete used verification code
	_ = s.users.DeleteVerificationCode(r.Context(), vc.ID)

	// Set user.is_verified = true
	if err := s.users.SetVerified(r.Context(), userID); err != nil {
		s.log.Error("verify: set user verified failed", "user_id", userID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Issue refresh token
	refreshToken, err := auth.CreateRefreshToken(s.jwtSecret, userID)
	if err != nil {
		s.log.Error("verify: create refresh token failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Create persistent session record
	sessionID := ""
	if s.sessions != nil {
		hashedToken := fmt.Sprintf("%x", sha256.Sum256([]byte(refreshToken)))
		sess := &domain.UserSession{
			UserID:         userID,
			RefreshTokenID: hashedToken,
			UserAgent:      r.Header.Get("User-Agent"),
			DeviceName:     parseDeviceName(r),
			IPAddress:      getClientIP(r),
			IsActive:       true,
			ExpiresAt:      time.Now().Add(auth.RefreshTokenLifetime),
		}
		if err := s.sessions.CreateSession(r.Context(), sess); err != nil {
			s.log.Error("verify: create session failed", "err", err)
		} else {
			sessionID = sess.ID
		}
	}

	accessToken, err := auth.CreateTokenWithSession(s.jwtSecret, userID, sessionID)
	if err != nil {
		s.log.Error("verify: create access token failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	s.log.Info("user email verified successfully", "user_id", userID)

	writeJSON(w, http.StatusOK, authResponse{
		Token:        accessToken,
		RefreshToken: refreshToken,
	})
}

// HandleGoogleOAuth implements POST /api/auth/google.
func (s *Server) HandleGoogleOAuth(w http.ResponseWriter, r *http.Request) {
	if !s.requireUsers(w) {
		return
	}
	if s.jwtSecret == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "authentication not configured",
		})
		return
	}

	var req googleOAuthRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	if req.IDToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id_token is required"})
		return
	}

	payload, err := idtoken.Validate(r.Context(), req.IDToken, s.googleClientID)
	if err != nil {
		s.log.Warn("google oauth: id token validation failed", "err", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid google id token"})
		return
	}

	emailRaw, ok := payload.Claims["email"].(string)
	if !ok || emailRaw == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "google token missing email claim"})
		return
	}
	email := strings.TrimSpace(strings.ToLower(emailRaw))

	// Look up user or auto-create if new
	user, err := s.users.GetByEmail(r.Context(), email)
	if err != nil && errors.Is(err, sql.ErrNoRows) {
		// Auto-create Google OAuth user
		randomPwd := fmt.Sprintf("oauth_%d_%s", time.Now().UnixNano(), randHex(16))
		hash, _ := bcrypt.GenerateFromPassword([]byte(randomPwd), bcrypt.DefaultCost)
		newUser := &domain.User{
			Email:        email,
			PasswordHash: string(hash),
			IsVerified:   true,
		}
		if err := s.users.Create(r.Context(), newUser); err != nil {
			s.log.Error("google oauth: create user failed", "email", email, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		user = newUser
	} else if err != nil {
		s.log.Error("google oauth: lookup email failed", "email", email, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	if !user.IsVerified {
		_ = s.users.SetVerified(r.Context(), user.ID)
	}

	// Issue refresh token
	refreshToken, err := auth.CreateRefreshToken(s.jwtSecret, user.ID)
	if err != nil {
		s.log.Error("google oauth: create refresh token failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	
	// Create persistent session record
	sessionID := ""
	if s.sessions != nil {
		hashedToken := fmt.Sprintf("%x", sha256.Sum256([]byte(refreshToken)))
		sess := &domain.UserSession{
			UserID:         user.ID,
			RefreshTokenID: hashedToken,
			UserAgent:      r.Header.Get("User-Agent"),
			DeviceName:     parseDeviceName(r),
			IPAddress:      getClientIP(r),
			IsActive:       true,
			ExpiresAt:      time.Now().Add(auth.RefreshTokenLifetime),
		}
		if err := s.sessions.CreateSession(r.Context(), sess); err != nil {
			s.log.Error("google oauth: create session failed", "err", err)
		} else {
			sessionID = sess.ID
		}
	}

	// Issue access token
	accessToken, err := auth.CreateTokenWithSession(s.jwtSecret, user.ID, sessionID)
	if err != nil {
		s.log.Error("google oauth: create access token failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	s.log.Info("google oauth user authenticated", "user_id", user.ID, "email", email)

	writeJSON(w, http.StatusOK, authResponse{
		Token:        accessToken,
		RefreshToken: refreshToken,
	})
}

// HandleResendVerification implements POST /api/auth/resend-verification.
func (s *Server) HandleResendVerification(w http.ResponseWriter, r *http.Request) {
	if !s.requireUsers(w) {
		return
	}

	var req resendRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	var user *domain.User
	var err error
	userID := strings.TrimSpace(req.UserID)
	emailStr := strings.TrimSpace(strings.ToLower(req.Email))

	if userID != "" {
		user, err = s.users.GetByID(r.Context(), userID)
	} else if emailStr != "" {
		user, err = s.users.GetByEmail(r.Context(), emailStr)
	} else {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id or email is required"})
		return
	}

	if err != nil || user == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}

	if user.IsVerified {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user is already verified"})
		return
	}

	// Generate fresh 6-digit verification code with 15 minute expiry.
	code := generate6DigitCode()
	vc := &domain.VerificationCode{
		UserID:    user.ID,
		Code:      code,
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	if err := s.users.CreateVerificationCode(r.Context(), vc); err != nil {
		s.log.Error("resend: create verification code failed", "user_id", user.ID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Dispatch email asynchronously in background.
	if s.mailer != nil {
		go func(toEmail, verifyCode string) {
			_ = s.mailer.SendVerificationEmail(toEmail, verifyCode)
		}(user.Email, code)
	} else {
		s.log.Warn("resend: mailer unconfigured; verification code", "email", user.Email, "code", code)
	}

	s.log.Info("resend verification code generated", "user_id", user.ID, "email", user.Email)

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Verification code sent successfully",
		"email":   user.Email,
	})
}

// HandleForgotPassword implements POST /api/auth/forgot-password.
func (s *Server) HandleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireUsers(w) {
		return
	}

	var req forgotPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	// Always return 200 OK success message for User Enumeration Defense
	successResponse := map[string]string{
		"message": "Email sent",
	}



	if email == "" || !strings.Contains(email, "@") {
		writeJSON(w, http.StatusOK, successResponse)
		return
	}

	user, err := s.users.GetByEmail(r.Context(), email)
	if err != nil || user == nil {
		// User does not exist - do not reveal user existence
		s.log.Info("forgot password: user not found", "email", email)
		writeJSON(w, http.StatusOK, successResponse)
		return
	}

	// Generate secure 64-character token (32 random bytes = 64 hex chars)
	token := randHex(32)
	expiresAt := time.Now().Add(15 * time.Minute)

	if err := s.users.CreatePasswordResetToken(r.Context(), user.ID, token, expiresAt); err != nil {
		s.log.Error("forgot password: create reset token failed", "user_id", user.ID, "err", err)
		writeJSON(w, http.StatusOK, successResponse)
		return
	}

	baseURL := getEnvOrDefault("APP_BASE_URL", "http://localhost:5173")
	resetLink := fmt.Sprintf("%s/reset-password?token=%s", baseURL, token)

	if s.mailer != nil {
		go func(toEmail, link string) {
			_ = s.mailer.SendPasswordResetEmail(toEmail, link)
		}(email, resetLink)
	} else {
		s.log.Warn("forgot password: mailer unconfigured; password reset link", "email", email, "reset_link", resetLink)
	}

	s.log.Info("forgot password reset token issued", "user_id", user.ID, "email", email)
	writeJSON(w, http.StatusOK, successResponse)
}

// HandleResetPassword implements POST /api/auth/reset-password.
func (s *Server) HandleResetPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireUsers(w) {
		return
	}

	var req resetPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	token := strings.TrimSpace(req.Token)
	password := req.Password
	if token == "" || password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token and password are required"})
		return
	}

	if len(password) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		return
	}

	reset, err := s.users.ValidateResetToken(r.Context(), token)
	if err != nil || reset == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid, expired, or used reset token"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		s.log.Error("reset password: hash failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Update user password & invalidate active sessions
	if err := s.users.UpdatePassword(r.Context(), reset.UserID, string(hash)); err != nil {
		s.log.Error("reset password: update password failed", "user_id", reset.UserID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Mark token as used
	if err := s.users.MarkResetTokenUsed(r.Context(), token); err != nil {
		s.log.Error("reset password: mark token used failed", "token", token, "err", err)
	}

	s.log.Info("user password successfully reset", "user_id", reset.UserID)
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Password has been successfully reset.",
	})
}

// HandleChangePassword implements POST /api/auth/change-password.
func (s *Server) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireUsers(w) {
		return
	}

	userID, status, errMsg := s.userFromBearer(r)
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}

	var req changePasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "current_password and new_password are required"})
		return
	}

	if len(req.NewPassword) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "new password must be at least 8 characters"})
		return
	}

	user, err := s.users.GetByID(r.Context(), userID)
	if err != nil || user == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "current password is incorrect"})
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		s.log.Error("change password: hash failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	if err := s.users.UpdatePassword(r.Context(), userID, string(newHash)); err != nil {
		s.log.Error("change password: update password failed", "user_id", userID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	s.log.Info("user changed password successfully", "user_id", userID)
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Password changed successfully.",
	})
}

// HandleLogout implements POST /api/auth/logout.
func (s *Server) HandleLogout(w http.ResponseWriter, r *http.Request) {
	var req logoutRequest
	_ = decodeJSON(r, &req)

	if req.RefreshToken != "" {
		if claims, err := auth.ValidateToken(s.jwtSecret, req.RefreshToken); err == nil && claims != nil {
			if s.sessions != nil {
				hashedToken := fmt.Sprintf("%x", sha256.Sum256([]byte(req.RefreshToken)))
				if sess, err := s.sessions.GetSessionByRefreshTokenID(r.Context(), hashedToken); err == nil {
					_ = s.sessions.DeleteSession(r.Context(), sess.ID, claims.UserID)
				}
			}
			_ = s.users.DeleteRefreshToken(r.Context(), req.RefreshToken)
		} else {
			_ = s.users.DeleteRefreshToken(r.Context(), req.RefreshToken)
		}
	}

	// Also check Authorization header bearer token session if present
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		tokenStr := strings.TrimPrefix(header, "Bearer ")
		if claims, err := auth.ValidateToken(s.jwtSecret, tokenStr); err == nil && claims != nil {
			if claims.SessionID != "" && s.sessions != nil {
				_ = s.sessions.DeleteSession(r.Context(), claims.SessionID, claims.UserID)
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Logged out successfully.",
	})
}

func getEnvOrDefault(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}


