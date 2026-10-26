package postgresrepo

import (
	"crypto/sha256"
	"encoding/hex"
)

// hashBearerToken returns a stable one-way lookup key for a high-entropy
// bearer token. The raw token is only returned to the caller that created it.
func hashBearerToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
