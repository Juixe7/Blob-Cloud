// Package auth handles identity concerns: issuing and validating the JWTs used
// to authenticate WebSocket connections (and, in later phases, REST calls).
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TokenLifetime is how long an issued access token remains valid. Kept short
// because clients obtain a fresh one via refresh tokens; stolen tokens stop
// working quickly.
const TokenLifetime = 24 * time.Hour

// RefreshTokenLifetime is how long a refresh token remains valid. Refresh tokens
// are long-lived so users stay signed in across sessions without re-entering
// credentials.
const RefreshTokenLifetime = 7 * 24 * time.Hour

// Claims is the custom JWT claims body. UserID is the subject; the standard
// RegisteredClaims supply expiry/issued-at for automatic validation. IsRefresh
// distinguishes refresh tokens from access tokens so the Bearer middleware can
// reject refresh tokens used as access tokens.
type Claims struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id,omitempty"`
	IsRefresh bool   `json:"is_refresh"`
	jwt.RegisteredClaims
}

// CreateToken mints a signed access-token JWT for the given user using HMAC-SHA256.
func CreateToken(secret, userID string) (string, error) {
	return CreateTokenWithSession(secret, userID, "")
}

// CreateTokenWithSession mints a signed access-token JWT bound to a specific session_id.
func CreateTokenWithSession(secret, userID, sessionID string) (string, error) {
	if err := VerifySecret(secret); err != nil {
		return "", err
	}
	now := time.Now()
	claims := Claims{
		UserID:    userID,
		SessionID: sessionID,
		IsRefresh: false,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.New().String(),
			ExpiresAt: jwt.NewNumericDate(now.Add(TokenLifetime)),
			IssuedAt:  jwt.NewNumericDate(now),
			Subject:   userID,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// CreateRefreshToken mints a signed refresh-token JWT for the given user.
func CreateRefreshToken(secret, userID string) (string, error) {
	return CreateRefreshTokenWithSession(secret, userID, "")
}

// CreateRefreshTokenWithSession mints a signed refresh-token JWT bound to a specific session_id.
func CreateRefreshTokenWithSession(secret, userID, sessionID string) (string, error) {
	if err := VerifySecret(secret); err != nil {
		return "", err
	}
	now := time.Now()
	claims := Claims{
		UserID:    userID,
		SessionID: sessionID,
		IsRefresh: true,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.New().String(),
			ExpiresAt: jwt.NewNumericDate(now.Add(RefreshTokenLifetime)),
			IssuedAt:  jwt.NewNumericDate(now),
			Subject:   userID,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ValidateToken verifies the signature and expiry of a JWT and returns its
// claims. A bad/modified/expired token returns a descriptive error.
func ValidateToken(secret, tokenStr string) (*Claims, error) {
	if err := VerifySecret(secret); err != nil {
		return nil, err
	}
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		// Ensure the token uses the expected signing method (HMAC), not an
		// algorithm-confusion attack (e.g. "none" or RS256 with the HMAC key).
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("validate token: %w", err)
	}
	return claims, nil
}

// VerifySecret rejects a secret that is too short. 32 bytes is the NIST minimum
// for HMAC-SHA256; anything shorter is a latent vulnerability.
func VerifySecret(secret string) error {
	if len(secret) < 32 {
		return errors.New("JWT_SECRET must be at least 32 bytes")
	}
	return nil
}
