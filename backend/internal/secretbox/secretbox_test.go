package secretbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func newBox(t *testing.T) *Box {
	t.Helper()
	b, err := New(bytes.Repeat([]byte{7}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpen(t *testing.T) {
	b := newBox(t)
	secret := []byte("my personal api key")
	sealed := b.Seal(secret, "garmin api key|user-1")
	if bytes.Contains(sealed, secret) {
		t.Fatal("sealed secret contains the plaintext")
	}
	got, err := b.Open(sealed, "garmin api key|user-1")
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("Open = %q, %v; want %q", got, err, secret)
	}
	if again := b.Seal(secret, "garmin api key|user-1"); bytes.Equal(again, sealed) {
		t.Error("sealing twice gave the same bytes; the nonce must be random")
	}
}

func TestOpenRejects(t *testing.T) {
	b := newBox(t)
	sealed := b.Seal([]byte("secret"), "ctx|user-1")
	other, err := New(bytes.Repeat([]byte{8}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	flipped := bytes.Clone(sealed)
	flipped[len(flipped)-1] ^= 1
	wrongVersion := bytes.Clone(sealed)
	wrongVersion[0] = 2

	tests := map[string]func() ([]byte, error){
		"other context": func() ([]byte, error) { return b.Open(sealed, "ctx|user-2") },
		"other key":     func() ([]byte, error) { return other.Open(sealed, "ctx|user-1") },
		"changed":       func() ([]byte, error) { return b.Open(flipped, "ctx|user-1") },
		"wrong version": func() ([]byte, error) { return b.Open(wrongVersion, "ctx|user-1") },
		"too short":     func() ([]byte, error) { return b.Open(sealed[:10], "ctx|user-1") },
		"empty":         func() ([]byte, error) { return b.Open(nil, "ctx|user-1") },
	}
	for name, open := range tests {
		t.Run(name, func(t *testing.T) {
			if got, err := open(); !errors.Is(err, ErrDecrypt) || got != nil {
				t.Errorf("Open = %q, %v; want ErrDecrypt", got, err)
			}
		})
	}
}

func TestNewFromBase64(t *testing.T) {
	key := make([]byte, KeySize)
	_, _ = rand.Read(key)
	if _, err := NewFromBase64(base64.StdEncoding.EncodeToString(key)); err != nil {
		t.Errorf("valid key: %v", err)
	}
	for name, s := range map[string]string{
		"not base64": "not base64!",
		"too short":  base64.StdEncoding.EncodeToString(key[:16]),
		"empty":      "",
	} {
		if _, err := NewFromBase64(s); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
