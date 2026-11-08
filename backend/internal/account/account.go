// Package account handles sign-up, email verification and sign-in with an
// email address and a password.
//
// Sign-up never says whether an address is already registered: the owner of
// the address is emailed either a verification link or a note that they
// already have an account. The link opens a page that asks for the password
// chosen at sign-up, so only someone who knows the password and can read
// the address's email can activate the account.
package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/jlindbergh/track-a-doodle/backend/internal/auth"
	mailer "github.com/jlindbergh/track-a-doodle/backend/internal/mail"
	"github.com/jlindbergh/track-a-doodle/backend/internal/password"
	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

// Lifetimes and limits.
const (
	VerifyTokenTTL = 24 * time.Hour
	SessionTTL     = 30 * 24 * time.Hour
	// UnverifiedTTL is how long a sign-up that was never verified is kept.
	UnverifiedTTL = 7 * 24 * time.Hour
	maxEmailLen   = 254
)

// SendLimit caps how often one address is emailed.
var SendLimit = store.SendLimit{MinInterval: time.Minute, PerDay: 5}

// Errors returned to the client. Messages are shown to users as they are.
var (
	ErrInvalidEmail       = errors.New("enter a valid email address")
	ErrInvalidCredentials = errors.New("wrong email or password")
	ErrWrongPassword      = errors.New("that is not the password you chose when signing up")
	ErrNotVerified        = errors.New("confirm your email address first: open the link in the email we sent you, or sign up again to get a new one")
	ErrInvalidToken       = errors.New("this confirmation link is invalid or has expired; sign up again to get a new one")
	ErrMailBusy           = errors.New("too many sign-ups right now; try again later")
)

// InputError wraps an error caused by what the client sent.
type InputError struct{ Err error }

func (e InputError) Error() string { return e.Err.Error() }
func (e InputError) Unwrap() error { return e.Err }

// Store is the database access the service needs; *store.Store implements it.
type Store interface {
	Register(ctx context.Context, reg store.Registration) (store.RegisterOutcome, error)
	AccountByEmail(ctx context.Context, email string) (store.Account, error)
	AccountByVerifyToken(ctx context.Context, tokenHash []byte) (store.Account, error)
	Verify(ctx context.Context, tokenHash, sessionHash []byte, sessionExpires time.Time) (store.User, error)
	CreateSession(ctx context.Context, userID string, tokenHash []byte, expiresAt time.Time) error
	DeleteSession(ctx context.Context, tokenHash []byte) error
}

// Session is a new signed-in session. Token goes in the cookie.
type Session struct {
	Token     string
	ExpiresAt time.Time
	User      store.User
}

// Config configures a Service.
type Config struct {
	// BaseURL is where users open the app, e.g. https://example.com.
	BaseURL *url.URL
	// MailPerHour caps all emails sent, whatever the address, so a flood of
	// sign-ups can't make the server send mail without bound.
	MailPerHour int
}

// Service implements the account flows.
type Service struct {
	store Store
	mail  mailer.Sender
	cfg   Config
	mails *rate.Limiter
	log   *slog.Logger
	now   func() time.Time
}

// New returns a Service.
func New(st Store, m mailer.Sender, cfg Config, log *slog.Logger) *Service {
	return &Service{
		store: st,
		mail:  m,
		cfg:   cfg,
		mails: rate.NewLimiter(rate.Limit(float64(cfg.MailPerHour)/3600), cfg.MailPerHour),
		log:   log,
		now:   time.Now,
	}
}

// NormalizeEmail checks that s is a bare email address and returns it
// trimmed and lowercased, the form stored in the database.
func NormalizeEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxEmailLen {
		return "", ErrInvalidEmail
	}
	a, err := mail.ParseAddress(s)
	// Only a bare address: no display name, comments or angle brackets.
	if err != nil || a.Address != s || a.Name != "" {
		return "", ErrInvalidEmail
	}
	// A domain that can receive mail has a dot in it.
	if domain := s[strings.LastIndex(s, "@")+1:]; !strings.Contains(strings.Trim(domain, "."), ".") {
		return "", ErrInvalidEmail
	}
	return strings.ToLower(s), nil
}

// Register signs up email with pw and emails the address. It returns the
// same for new and existing addresses, so it can't be used to find out who
// has an account.
func (s *Service) Register(ctx context.Context, email, pw string) error {
	email, err := NormalizeEmail(email)
	if err != nil {
		return InputError{err}
	}
	if err := password.Validate(pw); err != nil {
		return InputError{err}
	}
	// Checked before anything is stored, so a sign-up that can't be mailed
	// doesn't replace the link of an earlier one.
	if !s.mails.Allow() {
		s.log.WarnContext(ctx, "hourly email limit reached; refusing sign-up")
		return ErrMailBusy
	}
	hash, err := password.Hash(pw)
	if err != nil {
		return err
	}
	token, tokenHash, err := auth.NewToken()
	if err != nil {
		return err
	}
	outcome, err := s.store.Register(ctx, store.Registration{
		Email:        email,
		PasswordHash: hash,
		TokenHash:    tokenHash,
		ExpiresAt:    s.now().Add(VerifyTokenTTL),
		Limit:        SendLimit,
	})
	if err != nil {
		return err
	}

	var msg mailer.Message
	switch outcome {
	case store.Pending:
		msg = verifyMessage(email, s.verifyURL(token))
	case store.Exists:
		msg = existsMessage(email, s.cfg.BaseURL.String())
	case store.Limited:
		// The address got an email moments ago. Say the same as always;
		// the user can try again in a minute.
		return nil
	}
	if err := s.mail.Send(ctx, msg); err != nil {
		return fmt.Errorf("send sign-up email: %w", err)
	}
	return nil
}

// verifyURL is the link in the verification email. The token is in the
// fragment, so it never reaches server logs or Referer headers.
func (s *Service) verifyURL(token string) string {
	u := s.cfg.BaseURL.JoinPath("verify")
	u.Fragment = "token=" + token
	return u.String()
}

// Verify activates the account the token was sent for, if pw is its
// password, and signs the user in. A wrong password leaves the token usable.
func (s *Service) Verify(ctx context.Context, token, pw string) (Session, error) {
	if token == "" {
		return Session{}, InputError{ErrInvalidToken}
	}
	tokenHash := auth.HashToken(token)
	acct, err := s.store.AccountByVerifyToken(ctx, tokenHash)
	if errors.Is(err, store.ErrNotFound) {
		return Session{}, InputError{ErrInvalidToken}
	}
	if err != nil {
		return Session{}, err
	}
	ok, err := password.Check(pw, acct.PasswordHash)
	if err != nil {
		return Session{}, fmt.Errorf("check password of %s: %w", acct.ID, err)
	}
	if !ok {
		return Session{}, InputError{ErrWrongPassword}
	}

	sess, sessHash, err := s.newSession()
	if err != nil {
		return Session{}, err
	}
	u, err := s.store.Verify(ctx, tokenHash, sessHash, sess.ExpiresAt)
	if errors.Is(err, store.ErrNotFound) {
		// Replaced by a newer sign-up while the password was checked.
		return Session{}, InputError{ErrInvalidToken}
	}
	if err != nil {
		return Session{}, err
	}
	sess.User = u
	return sess, nil
}

// Login signs in with email and password. Unknown emails and wrong
// passwords get the same error and take the same time.
func (s *Service) Login(ctx context.Context, email, pw string) (Session, error) {
	email, err := NormalizeEmail(email)
	if err != nil || len(pw) > 4*password.MaxLength {
		password.CheckDummy(pw)
		return Session{}, InputError{ErrInvalidCredentials}
	}
	acct, err := s.store.AccountByEmail(ctx, email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return Session{}, err
	}
	if err != nil || acct.PasswordHash == "" {
		password.CheckDummy(pw)
		return Session{}, InputError{ErrInvalidCredentials}
	}
	ok, err := password.Check(pw, acct.PasswordHash)
	if err != nil {
		return Session{}, fmt.Errorf("check password of %s: %w", acct.ID, err)
	}
	if !ok {
		return Session{}, InputError{ErrInvalidCredentials}
	}
	// Only said once the password is known to be right.
	if !acct.Verified {
		return Session{}, InputError{ErrNotVerified}
	}

	sess, sessHash, err := s.newSession()
	if err != nil {
		return Session{}, err
	}
	if err := s.store.CreateSession(ctx, acct.ID, sessHash, sess.ExpiresAt); err != nil {
		return Session{}, err
	}
	sess.User = acct.User
	return sess, nil
}

// Logout ends the session with the given cookie token.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, auth.HashToken(token))
}

func (s *Service) newSession() (Session, []byte, error) {
	token, hash, err := auth.NewToken()
	if err != nil {
		return Session{}, nil, err
	}
	return Session{Token: token, ExpiresAt: s.now().Add(SessionTTL)}, hash, nil
}
