package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// A valid test secret (>= 32 bytes).
const testSecret = "abcdefghijklmnopqrstuvwxyz012345"

// TestCreateAndValidateToken verifies a round-trip: sign → parse → claims match.
func TestCreateAndValidateToken(t *testing.T) {
	token, err := CreateToken(testSecret, "user-42")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if token == "" {
		t.Fatal("CreateToken returned empty token")
	}

	claims, err := ValidateToken(testSecret, token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if claims.UserID != "user-42" {
		t.Fatalf("claims.UserID = %q, want user-42", claims.UserID)
	}
	if claims.Subject != "user-42" {
		t.Fatalf("claims.Subject = %q, want user-42", claims.Subject)
	}
}

// TestTokenExpiry verifies an expired token is rejected.
func TestTokenExpiry(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour)
	claims := Claims{
		UserID: "user-99",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(past),
			IssuedAt:  jwt.NewNumericDate(past.Add(-time.Hour)),
			Subject:   "user-99",
		},
	}
	token := testSign(t, claims)

	_, err := ValidateToken(testSecret, token)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if !strings.Contains(err.Error(), "token is expired") {
		t.Fatalf("error should mention expiry, got: %v", err)
	}
}

// TestWrongSecret verifies a token signed with one secret can't be validated
// with a different secret.
func TestWrongSecret(t *testing.T) {
	token, _ := CreateToken(testSecret, "user-1")
	_, err := ValidateToken("wrong-secret-32-bytes-long-xxx!!", token)
	if err == nil {
		t.Fatal("expected error validating with wrong secret, got nil")
	}
}

// TestTamperedToken verifies modifying the payload rejects the token.
func TestTamperedToken(t *testing.T) {
	token, _ := CreateToken(testSecret, "user-1")
	// Flip a byte in the payload (before the signature part).
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected token format: %d parts", len(parts))
	}
	// Corrupt the payload.
	parts[1] = "corrupted_payload"
	tampered := strings.Join(parts, ".")

	_, err := ValidateToken(testSecret, tampered)
	if err == nil {
		t.Fatal("expected error for tampered token, got nil")
	}
}

// TestVerifySecret checks minimum length enforcement.
func TestVerifySecret(t *testing.T) {
	if err := VerifySecret("short"); err == nil {
		t.Fatal("expected error for short secret, got nil")
	}
	if err := VerifySecret(strings.Repeat("x", 31)); err == nil {
		t.Fatal("expected error for 31-byte secret, got nil")
	}
	if err := VerifySecret(strings.Repeat("x", 32)); err != nil {
		t.Fatalf("32-byte secret should be valid: %v", err)
	}
}

// TestAlgNoneRejected verifies that an unsigned JWT with {"alg":"none"} is strictly rejected.
func TestAlgNoneRejected(t *testing.T) {
	claims := Claims{
		UserID: "user-admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   "user-admin",
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	unsignedStr, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("failed to craft alg:none token: %v", err)
	}

	_, err = ValidateToken(testSecret, unsignedStr)
	if err == nil {
		t.Fatal("SECURITY FAILURE: ValidateToken accepted an unsigned alg:none JWT")
	}
}

// TestRefreshTokenFlagSeparation verifies that refresh tokens carry IsRefresh=true
// while access tokens carry IsRefresh=false so refresh tokens cannot be replayed as Bearer access tokens.
func TestRefreshTokenFlagSeparation(t *testing.T) {
	accessTok, err := CreateTokenWithSession(testSecret, "user-10", "sess-abc")
	if err != nil {
		t.Fatalf("CreateTokenWithSession: %v", err)
	}
	accessClaims, err := ValidateToken(testSecret, accessTok)
	if err != nil {
		t.Fatalf("ValidateToken access: %v", err)
	}
	if accessClaims.IsRefresh {
		t.Fatal("access token must have IsRefresh=false")
	}
	if accessClaims.SessionID != "sess-abc" {
		t.Fatalf("SessionID = %q, want sess-abc", accessClaims.SessionID)
	}

	refreshTok, err := CreateRefreshTokenWithSession(testSecret, "user-10", "sess-abc")
	if err != nil {
		t.Fatalf("CreateRefreshTokenWithSession: %v", err)
	}
	refreshClaims, err := ValidateToken(testSecret, refreshTok)
	if err != nil {
		t.Fatalf("ValidateToken refresh: %v", err)
	}
	if !refreshClaims.IsRefresh {
		t.Fatal("refresh token must have IsRefresh=true")
	}
}

// testSign signs claims with the test secret for test helper purposes.
func testSign(t *testing.T, claims jwt.Claims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := token.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}
