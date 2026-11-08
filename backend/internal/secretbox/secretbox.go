// Package secretbox encrypts secrets that the backend must be able to read
// back, such as users' API keys for activity services, before they are
// stored. Passwords and session tokens are hashed instead and never need
// this.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// KeySize is the size of a key in bytes (AES-256).
const KeySize = 32

// version is the first byte of every sealed secret, so the format or key
// can change later without guessing.
const version byte = 1

// ErrDecrypt is returned when a sealed secret cannot be opened: it was
// sealed with another key or other context, or has been changed.
var ErrDecrypt = errors.New("secretbox: cannot decrypt")

// Box seals and opens secrets with one key, using AES-256-GCM.
type Box struct {
	aead cipher.AEAD
}

// New returns a Box using key, which must be KeySize bytes of random data.
func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secretbox: key is %d bytes, want %d", len(key), KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	return &Box{aead: aead}, nil
}

// NewFromBase64 returns a Box using a key written in standard base64, as in
// the SECRETS_KEY environment variable. Make one with: openssl rand -base64 32
func NewFromBase64(s string) (*Box, error) {
	key, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("secretbox: key is not valid base64: %w", err)
	}
	return New(key)
}

// Seal encrypts plaintext. context says what the secret is and whose, e.g.
// "garmin api key|<user id>": Open only succeeds with the same context, so a
// sealed secret copied to another user's row cannot be used there.
func (b *Box) Seal(plaintext []byte, context string) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	// crypto/rand.Read never returns an error.
	_, _ = rand.Read(nonce)
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+b.aead.Overhead())
	out = append(out, version)
	out = append(out, nonce...)
	return b.aead.Seal(out, nonce, plaintext, additionalData(context))
}

// Open decrypts a secret sealed by Seal with the same key and context.
func (b *Box) Open(sealed []byte, context string) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < 1+n+b.aead.Overhead() || sealed[0] != version {
		return nil, ErrDecrypt
	}
	plaintext, err := b.aead.Open(nil, sealed[1:1+n], sealed[1+n:], additionalData(context))
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

// additionalData binds a sealed secret to its version and context.
func additionalData(context string) []byte {
	return append([]byte{version}, context...)
}
