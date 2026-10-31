// Package api implements the HTTP API used by the frontend.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jlindbergh/track-a-doodle/backend/internal/auth"
	"github.com/jlindbergh/track-a-doodle/backend/internal/importer"
	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

// maxUploadSize caps one import request, which may hold many files.
const maxUploadSize = 500 << 20

// databaseUnavailableMessage is the body of a 500 caused by the database
// being down, instead of the generic "internal server error".
const databaseUnavailableMessage = "the database is unavailable; try again later"

// Server serves the API.
type Server struct {
	store    *store.Store
	importer *importer.Importer
	martin   *url.URL
	client   *http.Client
	log      *slog.Logger
}

// New returns a Server. martinURL is the internal base URL of Martin.
func New(s *store.Store, im *importer.Importer, martinURL *url.URL, log *slog.Logger) *Server {
	return &Server{
		store:    s,
		importer: im,
		martin:   martinURL,
		client:   &http.Client{Timeout: 30 * time.Second},
		log:      log,
	}
}

// Routes returns the API's routes. Authentication middleware must run first.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", s.healthz)
	mux.HandleFunc("GET /api/config", s.config)
	mux.HandleFunc("GET /api/me", auth.Require(s.me))
	mux.HandleFunc("GET /api/activities", auth.Require(s.listActivities))
	mux.HandleFunc("POST /api/activities/import", auth.Require(s.importActivities))
	mux.HandleFunc("GET "+tilesPath, auth.Require(s.tile))
	return mux
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	s.writeJSON(w, r, http.StatusOK, u)
}

func (s *Server) listActivities(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	list, err := s.store.ListActivities(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, list)
}

// importActivities accepts a multipart form with one or more "files" fields
// and returns one result per file, in upload order. With
// ?allowDuplicates=true, possible duplicates are stored instead of reported.
//
// Each file is stored on its own, so a file that cannot be stored is
// reported as failed and the others are still imported and reported. If the
// database is unavailable, though, the request fails with a 500: every file
// would fail, and the import can safely be repeated once it is back, since
// files already imported are skipped.
func (s *Server) importActivities(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	opts := importer.Options{AllowDuplicates: r.URL.Query().Get("allowDuplicates") == "true"}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "expected multipart/form-data", http.StatusBadRequest)
		return
	}

	results := []importer.Result{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			http.Error(w, "invalid upload: "+err.Error(), http.StatusBadRequest)
			return
		}
		if part.FormName() != "files" || part.FileName() == "" {
			part.Close()
			continue
		}

		data, err := io.ReadAll(io.LimitReader(part, importer.MaxFileSize+1))
		part.Close()
		if err != nil {
			http.Error(w, "invalid upload: "+err.Error(), http.StatusBadRequest)
			return
		}

		res, err := s.importer.ImportGPX(r.Context(), u.ID, part.FileName(), data, opts)
		if err != nil {
			if r.Context().Err() != nil {
				// The client is gone; nobody will read the results.
				return
			}
			if store.IsUnavailable(err) {
				s.serverError(w, r, err)
				return
			}
			s.log.ErrorContext(r.Context(), "import file", "err", err)
		}
		results = append(results, res)
	}

	s.writeJSON(w, r, http.StatusOK, results)
}

// tile proxies a vector tile request to Martin for the authenticated user.
// The user_id sent to Martin always comes from the session, and nothing
// from the client's query string is forwarded.
func (s *Server) tile(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())

	z, x, y, ok := parseTile(r)
	if !ok {
		http.Error(w, "invalid tile coordinates", http.StatusBadRequest)
		return
	}

	target := s.martin.JoinPath(martinSource, strconv.Itoa(z), strconv.Itoa(x), strconv.Itoa(y))
	target.RawQuery = url.Values{"user_id": {u.ID}}.Encode()

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.serverError(w, r, fmt.Errorf("fetch tile: %w", err))
		return
	}
	defer resp.Body.Close()

	// Tiles are private and change with every import.
	w.Header().Set("Cache-Control", "private, no-store")

	switch resp.StatusCode {
	case http.StatusOK:
		w.Header().Set("Content-Type", "application/vnd.mapbox-vector-tile")
		w.WriteHeader(http.StatusOK)
		if _, err := io.Copy(w, resp.Body); err != nil {
			s.log.WarnContext(r.Context(), "copy tile", "err", err)
		}
	case http.StatusNoContent:
		w.WriteHeader(http.StatusNoContent)
	default:
		s.log.ErrorContext(r.Context(), "martin error", "status", resp.StatusCode, "url", target.Path)
		http.Error(w, "tile server error", http.StatusBadGateway)
	}
}

func parseTile(r *http.Request) (z, x, y int, ok bool) {
	z, errZ := strconv.Atoi(r.PathValue("z"))
	x, errX := strconv.Atoi(r.PathValue("x"))
	y, errY := strconv.Atoi(r.PathValue("y"))
	if errZ != nil || errX != nil || errY != nil || z < minZoom || z > maxZoom {
		return 0, 0, 0, false
	}
	n := 1 << z
	if x < 0 || x >= n || y < 0 || y >= n {
		return 0, 0, 0, false
	}
	return z, x, y, true
}

// writeJSON writes v as JSON with the given status. It encodes v before
// writing anything, so a value that cannot be encoded becomes a 500 rather
// than a success status with an empty or cut-off body.
func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		s.serverError(w, r, fmt.Errorf("encode response: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		s.log.WarnContext(r.Context(), "write response", "err", err)
	}
}

// serverError logs err and answers 500, saying so if the database is
// unavailable. Nothing else about err is sent to the client.
func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	msg := "internal server error"
	if store.IsUnavailable(err) {
		msg = databaseUnavailableMessage
	}
	http.Error(w, msg, http.StatusInternalServerError)
}
