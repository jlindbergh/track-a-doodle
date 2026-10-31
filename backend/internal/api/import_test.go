package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/jlindbergh/track-a-doodle/backend/internal/activity"
	"github.com/jlindbergh/track-a-doodle/backend/internal/auth"
	"github.com/jlindbergh/track-a-doodle/backend/internal/importer"
	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

const validGPX = `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><trkseg>
	<trkpt lat="59.3" lon="18.0"/><trkpt lat="59.4" lon="18.1"/>
</trkseg></trk></gpx>`

// flakyStore fails with err to store the files named in fail, and stores the
// others.
type flakyStore struct {
	fail map[string]bool
	err  error
}

func (s flakyStore) InsertActivity(_ context.Context, _ string, na store.NewActivity, _ *activity.DuplicateRule) (store.InsertResult, error) {
	if s.fail[na.SourceFilename] {
		return store.InsertResult{}, s.err
	}
	return store.InsertResult{Status: store.Inserted, New: activity.Facts{ID: "id-" + na.SourceFilename}}, nil
}

// upload posts files named a.gpx, b.gpx and c.gpx to the import endpoint,
// where st stores them.
func upload(t *testing.T, st importer.Store) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, name := range []string{"a.gpx", "b.gpx", "c.gpx"} {
		fw, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(fw, validGPX)
	}
	mw.Close()

	srv := New(nil, importer.New(st), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := auth.DevMiddleware(store.User{ID: "u"}, srv.Routes())
	req := httptest.NewRequest(http.MethodPost, "/api/activities/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestImportDatabaseUnavailable(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	rec := upload(t, flakyStore{fail: map[string]bool{"b.gpx": true}, err: fmt.Errorf("begin import: %w", refused)})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != databaseUnavailableMessage {
		t.Errorf("body = %q, want %q", got, databaseUnavailableMessage)
	}
}

func TestImportReportsStoreErrorsPerFile(t *testing.T) {
	rec := upload(t, flakyStore{fail: map[string]bool{"b.gpx": true}, err: errors.New("value out of range")})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a result per file", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "out of range") {
		t.Error("the response reveals the database error")
	}
	var results []importer.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatal(err)
	}
	want := []importer.Status{importer.StatusImported, importer.StatusFailed, importer.StatusImported}
	if len(results) != len(want) {
		t.Fatalf("got %d results, want %d: %+v", len(results), len(want), results)
	}
	for i, r := range results {
		if r.Status != want[i] {
			t.Errorf("%s: status = %q, want %q", r.File, r.Status, want[i])
		}
	}
	if results[1].Error == "" {
		t.Error("the failed file has no message for the user")
	}
}

func TestServerErrorSaysWhenDatabaseIsUnavailable(t *testing.T) {
	srv := New(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tt := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("list activities: %w", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}), databaseUnavailableMessage},
		{errors.New("scan activity: boom"), "internal server error"},
	} {
		rec := httptest.NewRecorder()
		srv.serverError(rec, httptest.NewRequest(http.MethodGet, "/api/activities", nil), tt.err)
		if got := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusInternalServerError || got != tt.want {
			t.Errorf("%v: got %d %q, want 500 %q", tt.err, rec.Code, got, tt.want)
		}
	}
}
