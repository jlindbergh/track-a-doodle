package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// NewToken returns a random token for a link or cookie, and the hash to store.
func NewToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken returns the SHA-256 of token, which is all the database keeps.
// The tokens are random, so a fast unsalted hash is enough.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
