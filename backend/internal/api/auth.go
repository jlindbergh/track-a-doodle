package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jlindbergh/track-a-doodle/backend/internal/account"
	"github.com/jlindbergh/track-a-doodle/backend/internal/auth"
	"github.com/jlindbergh/track-a-doodle/backend/internal/ratelimit"
)

// Authentication modes, as reported in GET /api/config.
const (
	authModeDev      = "dev"
	authModePassword = "password"
)

// maxAuthBody caps the JSON body of the sign-up and sign-in endpoints.
const maxAuthBody = 8 << 10

const tooManyAttemptsMessage = "too many attempts; wait a while and try again"

// Accounts is the account service; *account.Service implements it.
type Accounts interface {
	Register(ctx context.Context, email, password string) error
	Verify(ctx context.Context, token, password string) (account.Session, error)
	Login(ctx context.Context, email, password string) (account.Session, error)
	Logout(ctx context.Context, token string) error
}

// AccountsConfig enables email + password accounts.
type AccountsConfig struct {
	Accounts Accounts
	// CookieSecure marks the session cookie Secure. Only turn it off for
	// plain-HTTP localhost.
	CookieSecure bool
	// TrustProxy takes the client IP for rate limits from X-Forwarded-For.
	TrustProxy bool
}

// accounts holds what the auth handlers need.
type accounts struct {
	AccountsConfig
	// Per client IP. Verify shares the sign-in limit: both check passwords.
	registerLimit *ratelimit.Keyed
	loginLimit    *ratelimit.Keyed
}

// EnableAccounts turns on the sign-up and sign-in endpoints. Call it before
// Routes, and put SessionMiddleware in front of the routes.
func (s *Server) EnableAccounts(cfg AccountsConfig) {
	s.accounts = &accounts{
		AccountsConfig: cfg,
		registerLimit:  ratelimit.NewKeyed(5, time.Hour, 5),
		loginLimit:     ratelimit.NewKeyed(10, time.Minute, 10),
	}
}

// SessionMiddleware attaches the signed-in user to each request.
func (s *Server) SessionMiddleware(lookup auth.SessionLookup, next http.Handler) http.Handler {
	return auth.SessionMiddleware(lookup, s.serverError, next)
}

func (s *Server) authMode() string {
	if s.accounts != nil {
		return authModePassword
	}
	return authModeDev
}

func (s *Server) authRoutes(mux *http.ServeMux) {
	if s.accounts == nil {
		return
	}
	mux.HandleFunc("POST /api/auth/register", s.register)
	mux.HandleFunc("POST /api/auth/verify", s.verify)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type verifyRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type registerResponse struct {
	// Status is always "check_email", whether or not the address was new.
	Status string `json:"status"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.allow(w, r, s.accounts.registerLimit) {
		return
	}
	var req credentials
	if !s.readJSON(w, r, &req) {
		return
	}
	if err := s.accounts.Accounts.Register(r.Context(), req.Email, req.Password); err != nil {
		s.accountError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusAccepted, registerResponse{Status: "check_email"})
}

func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	if !s.allow(w, r, s.accounts.loginLimit) {
		return
	}
	var req verifyRequest
	if !s.readJSON(w, r, &req) {
		return
	}
	sess, err := s.accounts.Accounts.Verify(r.Context(), req.Token, req.Password)
	if err != nil {
		s.accountError(w, r, err)
		return
	}
	s.startSession(w, r, sess)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.allow(w, r, s.accounts.loginLimit) {
		return
	}
	var req credentials
	if !s.readJSON(w, r, &req) {
		return
	}
	sess, err := s.accounts.Accounts.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		s.accountError(w, r, err)
		return
	}
	s.startSession(w, r, sess)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.accounts.Accounts.Logout(r.Context(), auth.SessionToken(r)); err != nil {
		s.serverError(w, r, err)
		return
	}
	auth.ClearSessionCookie(w, s.accounts.CookieSecure)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, sess account.Session) {
	auth.SetSessionCookie(w, sess.Token, sess.ExpiresAt, s.accounts.CookieSecure)
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, r, http.StatusOK, sess.User)
}

// allow answers 429 and returns false if the client IP is over limit.
func (s *Server) allow(w http.ResponseWriter, r *http.Request, limit *ratelimit.Keyed) bool {
	if limit.Allow(ratelimit.ClientIP(r, s.accounts.TrustProxy)) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	http.Error(w, tooManyAttemptsMessage, http.StatusTooManyRequests)
	return false
}

// readJSON decodes a small JSON body into v, answering 400 if it can't.
func (s *Server) readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return false
	}
	return true
}

// accountError answers with the status and message for an account error.
// Input errors carry messages meant for users; others are logged.
func (s *Server) accountError(w http.ResponseWriter, r *http.Request, err error) {
	var in account.InputError
	switch {
	case errors.As(err, &in):
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, account.ErrInvalidCredentials), errors.Is(err, account.ErrWrongPassword):
			status = http.StatusUnauthorized
		case errors.Is(err, account.ErrNotVerified):
			status = http.StatusForbidden
		}
		http.Error(w, in.Error(), status)
	case errors.Is(err, account.ErrMailBusy):
		w.Header().Set("Retry-After", "600")
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, account.ErrSendFailed):
		s.log.ErrorContext(r.Context(), "send email", "err", err)
		http.Error(w, account.ErrSendFailed.Error(), http.StatusBadGateway)
	default:
		s.serverError(w, r, err)
	}
}

// CrossOriginProtection rejects state-changing requests (anything but GET,
// HEAD and OPTIONS) that a browser marks as coming from another site. With
// SameSite=Lax cookies this guards every endpoint against CSRF.
func CrossOriginProtection(h http.Handler) http.Handler {
	return http.NewCrossOriginProtection().Handler(h)
}
