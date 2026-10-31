package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/jlindbergh/track-a-doodle/backend/internal/importer"
)

func TestConfigNeedsNoUser(t *testing.T) {
	srv := New(nil, nil, &url.URL{Scheme: "http", Host: "unused"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got appConfig
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Tiles.URL != "/api/tiles/{z}/{x}/{y}" || got.Tiles.MaxZoom != maxZoom {
		t.Errorf("Tiles = %+v", got.Tiles)
	}
	if r := got.Import.DuplicateRule; r.StartToleranceS != importer.DuplicateStartTolerance.Seconds() ||
		r.RelativeTolerance != importer.DuplicateRelativeTolerance {
		t.Errorf("DuplicateRule = %+v", r)
	}
	if !slices.Contains(got.Import.AcceptedExtensions, ".gpx") {
		t.Errorf("AcceptedExtensions = %v", got.Import.AcceptedExtensions)
	}
	if !slices.Contains(got.Sports, "running") {
		t.Errorf("Sports = %v", got.Sports)
	}
}
