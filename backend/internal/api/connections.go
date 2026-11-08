package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jlindbergh/track-a-doodle/backend/internal/activitysync"
	"github.com/jlindbergh/track-a-doodle/backend/internal/auth"
	"github.com/jlindbergh/track-a-doodle/backend/internal/ratelimit"
)

// Connections is the service that connects users to activity services;
// *activitysync.Service implements it.
type Connections interface {
	ConnectGarmin(ctx context.Context, userID, apiKey string, keyExpiresAt *time.Time) (activitysync.Status, error)
	Connections(ctx context.Context, userID string) ([]activitysync.Status, error)
	StartBackfill(ctx context.Context, userID, provider string) (activitysync.Status, error)
	CancelBackfill(ctx context.Context, userID, provider string) (activitysync.Status, error)
	Disconnect(ctx context.Context, userID, provider string) error
}

// maxAPIKeyLength caps the API key a user can send. Garmin's are far
// shorter.
const maxAPIKeyLength = 1024

// connections holds what the connection handlers need.
type connections struct {
	service Connections
	// connectLimit is per user: each attempt asks Garmin to check a key.
	connectLimit *ratelimit.Keyed
}

// EnableConnections turns on the endpoints for connecting to activity
// services. Call it before Routes.
func (s *Server) EnableConnections(c Connections) {
	s.connections = &connections{
		service:      c,
		connectLimit: ratelimit.NewKeyed(10, time.Hour, 10),
	}
}

func (s *Server) connectionRoutes(mux *http.ServeMux) {
	if s.connections == nil {
		return
	}
	mux.HandleFunc("GET /api/connections", auth.Require(s.listConnections))
	mux.HandleFunc("PUT /api/connections/garmin", auth.Require(s.connectGarmin))
	mux.HandleFunc("DELETE /api/connections/garmin", auth.Require(s.disconnectGarmin))
	mux.HandleFunc("POST /api/connections/garmin/backfill", auth.Require(s.startGarminBackfill))
	mux.HandleFunc("DELETE /api/connections/garmin/backfill", auth.Require(s.cancelGarminBackfill))
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	list, err := s.connections.service.Connections(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, r, http.StatusOK, list)
}

type connectGarminRequest struct {
	APIKey string `json:"apiKey"`
	// KeyExpiresAt is the date the key expires, as YYYY-MM-DD, if the user
	// gave it. The key counts as valid through that whole day (UTC).
	KeyExpiresAt string `json:"keyExpiresAt"`
}

// connectGarmin connects the user's Garmin account with a personal API key,
// or replaces the key. The key is checked with Garmin first.
func (s *Server) connectGarmin(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	if !s.connections.connectLimit.Allow(u.ID) {
		s.log.WarnContext(r.Context(), "rate limited", "path", r.URL.Path, "user", u.ID)
		w.Header().Set("Retry-After", "600")
		http.Error(w, tooManyAttemptsMessage, http.StatusTooManyRequests)
		return
	}
	var req connectGarminRequest
	if !s.readJSON(w, r, &req) {
		return
	}
	if len(req.APIKey) > maxAPIKeyLength {
		http.Error(w, "that is too long to be a Garmin API key", http.StatusBadRequest)
		return
	}
	var expires *time.Time
	if d := strings.TrimSpace(req.KeyExpiresAt); d != "" {
		day, err := time.Parse(time.DateOnly, d)
		if err != nil {
			http.Error(w, "keyExpiresAt must be a date like 2027-11-08", http.StatusBadRequest)
			return
		}
		end := day.Add(24*time.Hour - time.Second)
		expires = &end
	}

	st, err := s.connections.service.ConnectGarmin(r.Context(), u.ID, req.APIKey, expires)
	switch {
	case errors.Is(err, activitysync.ErrEmptyKey), errors.Is(err, activitysync.ErrKeyRejected):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, activitysync.ErrGarminUnavailable):
		http.Error(w, err.Error(), http.StatusBadGateway)
	case err != nil:
		s.serverError(w, r, err)
	default:
		s.writeJSON(w, r, http.StatusOK, st)
	}
}

func (s *Server) disconnectGarmin(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	err := s.connections.service.Disconnect(r.Context(), u.ID, activitysync.ProviderGarmin)
	if s.connectionError(w, r, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type backfillRequest struct {
	// Consent must be true: the user has been told what the backfill does
	// and agreed to it.
	Consent bool `json:"consent"`
}

// startGarminBackfill starts importing everything in the user's Garmin
// account, which they must have agreed to.
func (s *Server) startGarminBackfill(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	var req backfillRequest
	if !s.readJSON(w, r, &req) {
		return
	}
	if !req.Consent {
		http.Error(w, "importing all activities from Garmin needs your consent", http.StatusBadRequest)
		return
	}
	st, err := s.connections.service.StartBackfill(r.Context(), u.ID, activitysync.ProviderGarmin)
	if s.connectionError(w, r, err) {
		return
	}
	s.writeJSON(w, r, http.StatusOK, st)
}

func (s *Server) cancelGarminBackfill(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	st, err := s.connections.service.CancelBackfill(r.Context(), u.ID, activitysync.ProviderGarmin)
	if s.connectionError(w, r, err) {
		return
	}
	s.writeJSON(w, r, http.StatusOK, st)
}

// connectionError answers for err, if there is one, and reports whether it
// did.
func (s *Server) connectionError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, activitysync.ErrNotConnected):
		http.Error(w, "Garmin Connect is not connected", http.StatusNotFound)
	default:
		s.serverError(w, r, err)
	}
	return true
}
