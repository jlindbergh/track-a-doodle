package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/jlindbergh/track-a-doodle/backend/internal/auth"
	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

func TestTileUsesSessionUser(t *testing.T) {
	var gotPath, gotQuery string
	martin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Write([]byte("tile"))
	}))
	defer martin.Close()

	martinURL, _ := url.Parse(martin.URL)
	srv := New(nil, nil, martinURL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	user := store.User{ID: "11111111-1111-1111-1111-111111111111"}
	h := auth.DevMiddleware(user, srv.Routes())

	// A client trying to pick someone else's user_id must be ignored.
	req := httptest.NewRequest(http.MethodGet,
		"/api/tiles/3/4/2?user_id=22222222-2222-2222-2222-222222222222", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotPath != "/activities/3/4/2" {
		t.Errorf("martin path = %q", gotPath)
	}
	if want := "user_id=" + user.ID; gotQuery != want {
		t.Errorf("martin query = %q, want %q", gotQuery, want)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
}

func TestTileRequiresUser(t *testing.T) {
	srv := New(nil, nil, &url.URL{Scheme: "http", Host: "unused"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/tiles/0/0/0", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestTileRejectsInvalidCoordinates(t *testing.T) {
	srv := New(nil, nil, &url.URL{Scheme: "http", Host: "unused"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := auth.DevMiddleware(store.User{ID: "u"}, srv.Routes())
	for _, path := range []string{"/api/tiles/1/2/0", "/api/tiles/23/0/0", "/api/tiles/-1/0/0", "/api/tiles/a/0/0"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, rec.Code)
		}
	}
}
