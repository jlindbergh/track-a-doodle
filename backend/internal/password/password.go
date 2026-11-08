// Package password hashes and checks user passwords with argon2id.
//
// Hashes are stored in the PHC string format, e.g.
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
//
// so the parameters can be raised later without breaking existing hashes.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Length limits, following NIST SP 800-63B: a minimum length and no rules
// about character classes. The maximum keeps hashing cheap to request.
const (
	MinLength = 10
	MaxLength = 128
)

// OWASP's recommended argon2id parameters.
const (
	memoryKiB  = 19 * 1024
	iterations = 2
	threads    = 1
	saltLen    = 16
	keyLen     = 32
)

// ErrTooShort and ErrTooLong are returned by Validate.
var (
	ErrTooShort = fmt.Errorf("password must be at least %d characters", MinLength)
	ErrTooLong  = fmt.Errorf("password must be at most %d characters", MaxLength)
)

// Validate checks that pw is acceptable as a new password.
func Validate(pw string) error {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < MinLength:
		return ErrTooShort
	case n > MaxLength:
		return ErrTooLong
	}
	return nil
}

// Hash returns the PHC-encoded argon2id hash of pw with a random salt.
func Hash(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, iterations, memoryKiB, threads, keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memoryKiB, iterations, threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Check reports whether pw matches encoded, a hash from Hash.
func Check(pw, encoded string) (bool, error) {
	p, salt, key, err := decode(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(pw), salt, p.iterations, p.memoryKiB, p.threads, uint32(len(key)))
	return subtle.ConstantTimeCompare(got, key) == 1, nil
}

// dummyHash is checked against when there is no real hash, so that a login
// for an unknown email takes as long as one with a wrong password.
var dummyHash = func() string {
	h, err := Hash("not a real password, only used for timing")
	if err != nil {
		panic(err)
	}
	return h
}()

// CheckDummy spends the same time as Check and always fails. Call it when
// there is no user, so response times don't reveal which emails exist.
func CheckDummy(pw string) {
	_, _ = Check(pw, dummyHash)
}

type params struct {
	memoryKiB  uint32
	iterations uint32
	threads    uint8
}

var errMalformed = errors.New("malformed password hash")

func decode(encoded string) (params, []byte, []byte, error) {
	// "", "argon2id", "v=19", "m=...,t=...,p=...", salt, key
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return params{}, nil, nil, errMalformed
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return params{}, nil, nil, errMalformed
	}
	var p params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memoryKiB, &p.iterations, &p.threads); err != nil {
		return params{}, nil, nil, errMalformed
	}
	if p.memoryKiB == 0 || p.iterations == 0 || p.threads == 0 {
		return params{}, nil, nil, errMalformed
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return params{}, nil, nil, errMalformed
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return params{}, nil, nil, errMalformed
	}
	return p, salt, key, nil
}
