package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

func randomHash() []byte {
	h := sha256.Sum256([]byte(rand.Text()))
	return h[:]
}

// testEmail returns a fresh address and deletes its user after the test.
func testEmail(t *testing.T, s *Store) string {
	t.Helper()
	email := "test-" + rand.Text() + "@example.test"
	t.Cleanup(func() {
		if _, err := s.pool.Exec(context.Background(), `DELETE FROM users WHERE email = $1`, email); err != nil {
			t.Errorf("delete test user: %v", err)
		}
	})
	return email
}

var noLimit = SendLimit{PerDay: 100}

func register(t *testing.T, s *Store, email string, limit SendLimit) (RegisterOutcome, []byte) {
	t.Helper()
	tok := randomHash()
	out, err := s.Register(context.Background(), Registration{
		Email: email, PasswordHash: "hash-" + rand.Text(), TokenHash: tok,
		ExpiresAt: time.Now().Add(time.Hour), Limit: limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	return out, tok
}

func TestRegisterAndVerify(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	email := testEmail(t, s)

	out, tok1 := register(t, s, email, noLimit)
	if out != Pending {
		t.Fatalf("first sign-up: %v", out)
	}
	_, tok2 := register(t, s, email, noLimit)
	if _, err := s.AccountByVerifyToken(ctx, tok1); !errors.Is(err, ErrNotFound) {
		t.Errorf("first token still works after signing up again: %v", err)
	}
	acct, err := s.AccountByVerifyToken(ctx, tok2)
	if err != nil || acct.Email != email || acct.Verified {
		t.Fatalf("AccountByVerifyToken = %+v, %v", acct, err)
	}

	sess := randomHash()
	u, err := s.Verify(ctx, tok2, sess, time.Now().Add(time.Hour))
	if err != nil || u.ID != acct.ID {
		t.Fatalf("Verify = %+v, %v", u, err)
	}
	if _, err := s.Verify(ctx, tok2, randomHash(), time.Now().Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Errorf("token used twice: %v", err)
	}
	if got, err := s.UserBySession(ctx, sess); err != nil || got.ID != u.ID {
		t.Errorf("UserBySession = %+v, %v", got, err)
	}

	// The account is verified now and must never change hands.
	before, _ := s.AccountByEmail(ctx, email)
	if out, _ := register(t, s, email, noLimit); out != Exists {
		t.Errorf("signing up a verified address: %v, want Exists", out)
	}
	after, _ := s.AccountByEmail(ctx, email)
	if after.PasswordHash != before.PasswordHash || !after.Verified {
		t.Error("a verified account was changed by a sign-up")
	}
}

func TestRegisterSendLimit(t *testing.T) {
	s := testStore(t)
	email := testEmail(t, s)
	limit := SendLimit{MinInterval: time.Minute, PerDay: 5}
	register(t, s, email, limit)
	ctx := context.Background()
	acct, _ := s.AccountByEmail(ctx, email)

	out, tok := register(t, s, email, limit)
	if out != Limited {
		t.Fatalf("second sign-up within a minute: %v, want Limited", out)
	}
	if _, err := s.AccountByVerifyToken(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Error("a limited sign-up stored its token")
	}
	if after, _ := s.AccountByEmail(ctx, email); after.PasswordHash != acct.PasswordHash {
		t.Error("a limited sign-up changed the password")
	}

	// Pretend the first email went out two minutes ago; then the daily cap.
	s.pool.Exec(ctx, `UPDATE email_sends SET sent_at = now() - interval '2 minutes' WHERE user_id = $1`, acct.ID)
	if out, _ := register(t, s, email, limit); out != Pending {
		t.Errorf("after the interval: %v, want Pending", out)
	}
	s.pool.Exec(ctx, `UPDATE email_sends SET sent_at = now() - interval '2 minutes' WHERE user_id = $1`, acct.ID)
	if out, _ := register(t, s, email, SendLimit{PerDay: 2}); out != Limited {
		t.Errorf("over the daily cap: %v, want Limited", out)
	}
}

func TestRegisterDoesNotClaimPasswordlessUsers(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s) // as created in dev mode: no password, unverified
	if out, _ := register(t, s, u.Email, noLimit); out != Exists {
		t.Errorf("sign-up for a dev-mode user: %v, want Exists", out)
	}
	if a, _ := s.AccountByEmail(context.Background(), u.Email); a.PasswordHash != "" {
		t.Error("the dev-mode user got a password")
	}
}

func TestSessionsBelongToTheirUser(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a, b := testUser(t, s), testUser(t, s)
	ha, hb := randomHash(), randomHash()
	for _, c := range []struct {
		id string
		h  []byte
	}{{a.ID, ha}, {b.ID, hb}} {
		if err := s.CreateSession(ctx, c.id, c.h, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.UserBySession(ctx, ha); got.ID != a.ID {
		t.Errorf("a's session resolved to %s", got.ID)
	}
	if got, _ := s.UserBySession(ctx, hb); got.ID != b.ID {
		t.Errorf("b's session resolved to %s", got.ID)
	}
	if _, err := s.UserBySession(ctx, randomHash()); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown session: %v", err)
	}

	if err := s.DeleteSession(ctx, ha); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserBySession(ctx, ha); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted session: %v", err)
	}

	expired := randomHash()
	s.CreateSession(ctx, a.ID, expired, time.Now().Add(-time.Second))
	if _, err := s.UserBySession(ctx, expired); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired session: %v", err)
	}
}

func TestPurgeExpired(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	stale, fresh := testEmail(t, s), testEmail(t, s)
	_, staleTok := register(t, s, stale, noLimit)
	register(t, s, fresh, noLimit)
	dev := testUser(t, s)
	s.pool.Exec(ctx, `UPDATE users SET created_at = now() - interval '30 days' WHERE email IN ($1, $2, $3)`, stale, fresh, dev.Email)
	// stale's link has expired; fresh's still works, so it is kept for now.
	s.pool.Exec(ctx, `UPDATE email_tokens SET expires_at = now() - interval '1 second' WHERE token_hash = $1`, staleTok)

	if _, err := s.PurgeExpired(ctx, 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AccountByEmail(ctx, stale); !errors.Is(err, ErrNotFound) {
		t.Errorf("stale sign-up not purged: %v", err)
	}
	if _, err := s.AccountByEmail(ctx, fresh); err != nil {
		t.Errorf("sign-up with a live link purged: %v", err)
	}
	if _, err := s.AccountByEmail(ctx, dev.Email); err != nil {
		t.Errorf("dev-mode user purged: %v", err)
	}
}
