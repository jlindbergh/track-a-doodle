package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jlindbergh/track-a-doodle/backend/internal/activitysync"
	"github.com/jlindbergh/track-a-doodle/backend/internal/auth"
	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

// fakeConnections records the calls it gets and answers with err.
type fakeConnections struct {
	err     error
	calls   []string
	userIDs []string
	key     string
	expires *time.Time
}

func (f *fakeConnections) record(call, userID string) (activitysync.Status, error) {
	f.calls = append(f.calls, call)
	f.userIDs = append(f.userIDs, userID)
	if f.err != nil {
		return activitysync.Status{}, f.err
	}
	return activitysync.Status{Provider: "garmin", Status: "active"}, nil
}

func (f *fakeConnections) ConnectGarmin(_ context.Context, userID, apiKey string, expires *time.Time) (activitysync.Status, error) {
	f.key, f.expires = apiKey, expires
	return f.record("connect", userID)
}

func (f *fakeConnections) Connections(_ context.Context, userID string) ([]activitysync.Status, error) {
	st, err := f.record("list", userID)
	return []activitysync.Status{st}, err
}

func (f *fakeConnections) StartBackfill(_ context.Context, userID, _ string) (activitysync.Status, error) {
	return f.record("start backfill", userID)
}

func (f *fakeConnections) CancelBackfill(_ context.Context, userID, _ string) (activitysync.Status, error) {
	return f.record("cancel backfill", userID)
}

func (f *fakeConnections) Disconnect(_ context.Context, userID, _ string) error {
	_, err := f.record("disconnect", userID)
	return err
}

// connectionsServer serves the API as user u (none if u.ID is "").
func connectionsServer(f *fakeConnections, u store.User) http.Handler {
	srv := New(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if f != nil {
		srv.EnableConnections(f)
	}
	if u.ID == "" {
		return srv.Routes()
	}
	return auth.DevMiddleware(u, srv.Routes())
}

func send(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var alice = store.User{ID: "alice-id", Email: "alice@example.com"}

func TestConnectionsNeedAUser(t *testing.T) {
	h := connectionsServer(&fakeConnections{}, store.User{})
	for _, tt := range []struct{ method, path string }{
		{http.MethodGet, "/api/connections"},
		{http.MethodPut, "/api/connections/garmin"},
		{http.MethodDelete, "/api/connections/garmin"},
		{http.MethodPost, "/api/connections/garmin/backfill"},
		{http.MethodDelete, "/api/connections/garmin/backfill"},
	} {
		if rec := send(h, tt.method, tt.path, `{}`); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status %d, want 401", tt.method, tt.path, rec.Code)
		}
	}
}

func TestConnectionsOff(t *testing.T) {
	h := connectionsServer(nil, alice)
	if rec := send(h, http.MethodGet, "/api/connections", ""); rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404 when connections are off", rec.Code)
	}
	var cfg appConfig
	rec := send(h, http.MethodGet, "/api/config", "")
	if err := json.NewDecoder(rec.Body).Decode(&cfg); err != nil || cfg.Connections.Garmin {
		t.Errorf("config connections = %+v, %v; want Garmin off", cfg.Connections, err)
	}
	cfg = appConfig{}
	rec = send(connectionsServer(&fakeConnections{}, alice), http.MethodGet, "/api/config", "")
	if err := json.NewDecoder(rec.Body).Decode(&cfg); err != nil || !cfg.Connections.Garmin {
		t.Errorf("config connections = %+v, %v; want Garmin on", cfg.Connections, err)
	}
}

func TestConnectGarmin(t *testing.T) {
	f := &fakeConnections{}
	rec := send(connectionsServer(f, alice), http.MethodPut, "/api/connections/garmin",
		`{"apiKey":"the-key","keyExpiresAt":"2027-11-08"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if len(f.userIDs) != 1 || f.userIDs[0] != alice.ID || f.key != "the-key" {
		t.Errorf("service got users %v and key %q", f.userIDs, f.key)
	}
	if want := time.Date(2027, 11, 8, 23, 59, 59, 0, time.UTC); f.expires == nil || !f.expires.Equal(want) {
		t.Errorf("expires = %v, want the end of the day %v", f.expires, want)
	}
	if strings.Contains(rec.Body.String(), "the-key") {
		t.Errorf("response contains the key: %s", rec.Body)
	}
}

func TestConnectGarminErrors(t *testing.T) {
	tests := []struct {
		name, body string
		err        error
		wantStatus int
		wantCalled bool
	}{
		{"rejected", `{"apiKey":"x"}`, activitysync.ErrKeyRejected, http.StatusBadRequest, true},
		{"empty", `{"apiKey":""}`, activitysync.ErrEmptyKey, http.StatusBadRequest, true},
		{"garmin down", `{"apiKey":"x"}`, activitysync.ErrGarminUnavailable, http.StatusBadGateway, true},
		{"database down", `{"apiKey":"x"}`, errors.New("db"), http.StatusInternalServerError, true},
		{"bad date", `{"apiKey":"x","keyExpiresAt":"next year"}`, nil, http.StatusBadRequest, false},
		{"too long", `{"apiKey":"` + strings.Repeat("k", maxAPIKeyLength+1) + `"}`, nil, http.StatusBadRequest, false},
		{"unknown field", `{"key":"x"}`, nil, http.StatusBadRequest, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeConnections{err: tt.err}
			rec := send(connectionsServer(f, alice), http.MethodPut, "/api/connections/garmin", tt.body)
			if rec.Code != tt.wantStatus {
				t.Errorf("status %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if called := len(f.calls) > 0; called != tt.wantCalled {
				t.Errorf("service called = %v, want %v", called, tt.wantCalled)
			}
			if tt.err == activitysync.ErrKeyRejected && !strings.Contains(rec.Body.String(), "rejected") {
				t.Errorf("body %q, want the reason", rec.Body)
			}
		})
	}
}

func TestConnectGarminRateLimit(t *testing.T) {
	h := connectionsServer(&fakeConnections{err: activitysync.ErrKeyRejected}, alice)
	var last int
	for range 11 {
		last = send(h, http.MethodPut, "/api/connections/garmin", `{"apiKey":"x"}`).Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("11th attempt: status %d, want 429", last)
	}
}

func TestStartBackfillNeedsConsent(t *testing.T) {
	f := &fakeConnections{}
	h := connectionsServer(f, alice)
	for _, body := range []string{`{}`, `{"consent":false}`} {
		if rec := send(h, http.MethodPost, "/api/connections/garmin/backfill", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", body, rec.Code)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("service called without consent: %v", f.calls)
	}
	if rec := send(h, http.MethodPost, "/api/connections/garmin/backfill", `{"consent":true}`); rec.Code != http.StatusOK {
		t.Errorf("with consent: status %d: %s", rec.Code, rec.Body)
	}
	if len(f.userIDs) != 1 || f.userIDs[0] != alice.ID {
		t.Errorf("service got users %v, want alice", f.userIDs)
	}
}

func TestConnectionCallsUseTheSessionUser(t *testing.T) {
	f := &fakeConnections{}
	h := connectionsServer(f, alice)
	send(h, http.MethodGet, "/api/connections", "")
	send(h, http.MethodDelete, "/api/connections/garmin/backfill", "")
	send(h, http.MethodDelete, "/api/connections/garmin", "")
	if len(f.userIDs) != 3 {
		t.Fatalf("calls = %v", f.calls)
	}
	for i, id := range f.userIDs {
		if id != alice.ID {
			t.Errorf("%s got user %q, want alice", f.calls[i], id)
		}
	}
}

func TestNotConnected(t *testing.T) {
	h := connectionsServer(&fakeConnections{err: activitysync.ErrNotConnected}, alice)
	for _, tt := range []struct{ method, path, body string }{
		{http.MethodDelete, "/api/connections/garmin", ""},
		{http.MethodPost, "/api/connections/garmin/backfill", `{"consent":true}`},
		{http.MethodDelete, "/api/connections/garmin/backfill", ""},
	} {
		if rec := send(h, tt.method, tt.path, tt.body); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status %d, want 404", tt.method, tt.path, rec.Code)
		}
	}
}
