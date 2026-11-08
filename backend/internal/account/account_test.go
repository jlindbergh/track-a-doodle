package account

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	mailer "github.com/jlindbergh/track-a-doodle/backend/internal/mail"
	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

// fakeStore keeps accounts in memory with the semantics of *store.Store.
type fakeStore struct {
	mu       sync.Mutex
	users    map[string]*store.Account // by email
	tokens   map[string]string         // token hash -> email
	sessions map[string]string         // session hash -> email
	limited  bool                      // make Register report Limited for existing users
}

func newFakeStore() *fakeStore {
	return &fakeStore{users: map[string]*store.Account{}, tokens: map[string]string{}, sessions: map[string]string{}}
}

func (f *fakeStore) Register(_ context.Context, reg store.Registration) (store.RegisterOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.users[reg.Email]
	if ok && f.limited {
		return store.Limited, nil
	}
	if ok && (a.Verified || a.PasswordHash == "") {
		return store.Exists, nil
	}
	if !ok {
		a = &store.Account{User: store.User{ID: "id-" + reg.Email, Email: reg.Email}}
		f.users[reg.Email] = a
	}
	a.PasswordHash = reg.PasswordHash
	for h, e := range f.tokens {
		if e == reg.Email {
			delete(f.tokens, h)
		}
	}
	f.tokens[string(reg.TokenHash)] = reg.Email
	return store.Pending, nil
}

func (f *fakeStore) AccountByEmail(_ context.Context, email string) (store.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.users[email]; ok {
		return *a, nil
	}
	return store.Account{}, store.ErrNotFound
}

func (f *fakeStore) AccountByVerifyToken(_ context.Context, h []byte) (store.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.tokens[string(h)]; ok {
		return *f.users[e], nil
	}
	return store.Account{}, store.ErrNotFound
}

func (f *fakeStore) Verify(_ context.Context, h, sess []byte, _ time.Time) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.tokens[string(h)]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	delete(f.tokens, string(h))
	f.users[e].Verified = true
	f.sessions[string(sess)] = e
	return f.users[e].User, nil
}

func (f *fakeStore) CreateSession(_ context.Context, userID string, h []byte, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[string(h)] = strings.TrimPrefix(userID, "id-")
	return nil
}

func (f *fakeStore) DeleteSession(_ context.Context, h []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, string(h))
	return nil
}

func newService(t *testing.T) (*Service, *fakeStore, *mailer.Recorder) {
	t.Helper()
	st := newFakeStore()
	rec := &mailer.Recorder{}
	base, _ := url.Parse("http://localhost:5173")
	return New(st, rec, Config{BaseURL: base, MailPerHour: 100}, slog.New(slog.NewTextHandler(io.Discard, nil))), st, rec
}

const pw = "a long enough password"

// tokenFrom returns the token in the link of a verification email.
func tokenFrom(t *testing.T, m mailer.Message) string {
	t.Helper()
	_, after, ok := strings.Cut(m.Body, "/verify#token=")
	if !ok {
		t.Fatalf("no verification link in:\n%s", m.Body)
	}
	return strings.Fields(after)[0]
}

func TestRegisterVerifyLogin(t *testing.T) {
	ctx := context.Background()
	svc, _, rec := newService(t)

	if err := svc.Register(ctx, "  Asa@Example.com ", pw); err != nil {
		t.Fatal(err)
	}
	sent := rec.Sent()
	if len(sent) != 1 || sent[0].To != "asa@example.com" || !strings.Contains(sent[0].Subject, "Confirm") {
		t.Fatalf("sent = %+v", sent)
	}
	token := tokenFrom(t, sent[0])

	_, err := svc.Login(ctx, "asa@example.com", pw)
	if in := (InputError{}); !errors.As(err, &in) || in.Err != ErrNotVerified || in.Reason != ReasonNotVerified {
		t.Fatalf("login before verifying: %v, want ErrNotVerified", err)
	}
	_, err = svc.Verify(ctx, token, "not the password")
	if in := (InputError{}); !errors.As(err, &in) || in.Err != ErrWrongPassword || in.Reason != ReasonWrongPassword {
		t.Fatalf("verify with wrong password: %v", err)
	}
	_, err = svc.Verify(ctx, "no-such-token", pw)
	if in := (InputError{}); !errors.As(err, &in) || in.Reason != ReasonInvalidToken {
		t.Fatalf("verify with unknown token: %v", err)
	}
	// The wrong password didn't use the link up.
	sess, err := svc.Verify(ctx, token, pw)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if sess.Token == "" || sess.User.Email != "asa@example.com" {
		t.Errorf("session = %+v", sess)
	}
	if _, err := svc.Verify(ctx, token, pw); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("reusing the link: %v, want ErrInvalidToken", err)
	}
	if _, err := svc.Login(ctx, "ASA@example.com", pw); err != nil {
		t.Errorf("login after verifying: %v", err)
	}
}

func TestRegisterDoesNotRevealExistingAccounts(t *testing.T) {
	ctx := context.Background()
	svc, st, rec := newService(t)
	st.users["taken@example.com"] = &store.Account{User: store.User{ID: "id-taken@example.com", Email: "taken@example.com"}, PasswordHash: "x", Verified: true}

	errNew := svc.Register(ctx, "new@example.com", pw)
	errTaken := svc.Register(ctx, "taken@example.com", pw)
	if errNew != nil || errTaken != nil {
		t.Fatalf("errors differ: new %v, taken %v", errNew, errTaken)
	}
	sent := rec.Sent()
	if len(sent) != 2 || !strings.Contains(sent[1].Subject, "already have") {
		t.Fatalf("sent = %+v", sent)
	}
	if st.users["taken@example.com"].PasswordHash != "x" {
		t.Error("the existing account's password was changed")
	}
}

func TestRegisterAgainReplacesLink(t *testing.T) {
	ctx := context.Background()
	svc, _, rec := newService(t)
	svc.Register(ctx, "a@example.com", "first password!")
	svc.Register(ctx, "a@example.com", "second password!")
	sent := rec.Sent()
	if _, err := svc.Verify(ctx, tokenFrom(t, sent[0]), "first password!"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("old link: %v, want ErrInvalidToken", err)
	}
	// Someone who signed up with your address and their password can't
	// get you to activate it with your link: it wants their password.
	if _, err := svc.Verify(ctx, tokenFrom(t, sent[1]), "first password!"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("new link with old password: %v, want ErrWrongPassword", err)
	}
	if _, err := svc.Verify(ctx, tokenFrom(t, sent[1]), "second password!"); err != nil {
		t.Errorf("new link: %v", err)
	}
}

func TestRegisterLimitedSendsNothing(t *testing.T) {
	ctx := context.Background()
	svc, st, rec := newService(t)
	svc.Register(ctx, "a@example.com", pw)
	st.limited = true
	if err := svc.Register(ctx, "a@example.com", pw); err != nil {
		t.Fatalf("limited sign-up returned %v; want the same as always", err)
	}
	if n := len(rec.Sent()); n != 1 {
		t.Errorf("%d emails sent, want 1", n)
	}
}

func TestRegisterHourlyMailCap(t *testing.T) {
	ctx := context.Background()
	svc, _, rec := newService(t)
	base, _ := url.Parse("http://localhost")
	svc = New(svc.store, rec, Config{BaseURL: base, MailPerHour: 2}, svc.log)
	for i, addr := range []string{"a@example.com", "b@example.com"} {
		if err := svc.Register(ctx, addr, pw); err != nil {
			t.Fatalf("sign-up %d: %v", i, err)
		}
	}
	if err := svc.Register(ctx, "c@example.com", pw); !errors.Is(err, ErrMailBusy) {
		t.Errorf("third sign-up: %v, want ErrMailBusy", err)
	}
}

func TestRegisterValidates(t *testing.T) {
	svc, _, rec := newService(t)
	var in InputError
	if err := svc.Register(context.Background(), "nope", pw); !errors.As(err, &in) {
		t.Errorf("bad email: %v", err)
	}
	if err := svc.Register(context.Background(), "a@example.com", "short"); !errors.As(err, &in) {
		t.Errorf("short password: %v", err)
	}
	if len(rec.Sent()) != 0 {
		t.Error("an invalid sign-up sent email")
	}
}

func TestLoginErrorsAreTheSame(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newService(t)
	svc.Register(ctx, "a@example.com", pw)
	st.users["a@example.com"].Verified = true
	// A dev-mode user has no password and can never sign in with one.
	st.users["dev@example.com"] = &store.Account{User: store.User{ID: "id-dev@example.com"}, Verified: true}

	for _, c := range []struct {
		email, pw string
		reason    Reason
	}{
		{"unknown@example.com", pw, ReasonUnknownEmail},
		{"a@example.com", "wrong password", ReasonWrongPassword},
		{"dev@example.com", "", ReasonNoPassword},
		{"not an email", pw, ReasonInvalidInput},
	} {
		_, err := svc.Login(ctx, c.email, c.pw)
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Login(%q): %v, want ErrInvalidCredentials", c.email, err)
		}
		// The client sees one error; the log tells them apart.
		var in InputError
		if !errors.As(err, &in) || in.Reason != c.reason {
			t.Errorf("Login(%q): reason %q, want %q", c.email, in.Reason, c.reason)
		}
	}
}

func TestLogout(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newService(t)
	svc.Register(ctx, "a@example.com", pw)
	st.users["a@example.com"].Verified = true
	sess, err := svc.Login(ctx, "a@example.com", pw)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.sessions) != 1 {
		t.Fatalf("%d sessions", len(st.sessions))
	}
	if err := svc.Logout(ctx, sess.Token); err != nil {
		t.Fatal(err)
	}
	if len(st.sessions) != 0 {
		t.Error("session still exists after logout")
	}
}

func TestNormalizeEmail(t *testing.T) {
	for in, want := range map[string]string{
		"a@example.com":         "a@example.com",
		" A.B+tag@Example.COM ": "a.b+tag@example.com",
	} {
		if got, err := NormalizeEmail(in); err != nil || got != want {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "a", "a@", "a@localhost", "Name <a@example.com>", "a@example.com, b@example.com", "a@example.com\r\nBcc: x@example.com", strings.Repeat("a", 250) + "@example.com"} {
		if got, err := NormalizeEmail(in); err == nil {
			t.Errorf("NormalizeEmail(%q) = %q, want an error", in, got)
		}
	}
}

func TestVerifyLinkKeepsTokenOutOfServerRequests(t *testing.T) {
	svc, _, rec := newService(t)
	svc.Register(context.Background(), "a@example.com", pw)
	if body := rec.Sent()[0].Body; !bytes.Contains([]byte(body), []byte("http://localhost:5173/verify#token=")) {
		t.Errorf("link is not in the fragment:\n%s", body)
	}
}
