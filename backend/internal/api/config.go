package api

import (
	"net/http"

	"github.com/jlindbergh/track-a-doodle/backend/internal/activity"
	"github.com/jlindbergh/track-a-doodle/backend/internal/importer"
	"github.com/jlindbergh/track-a-doodle/backend/internal/password"
)

// Tiles. The zoom range is only enforced here; Martin serves any zoom.
const (
	// tilesPath is both the route of the tile proxy and the URL template the
	// map fetches tiles from: ServeMux and MapLibre share the {z}/{x}/{y} syntax.
	tilesPath = "/api/tiles/{z}/{x}/{y}"
	minZoom   = 0
	maxZoom   = 22
	// martinSource is the id of the activities source in mapserver/config.yaml.
	martinSource = "activities"
	// tileLayer is the name of the layer in the tiles, set by ST_AsMVT in
	// postgis/migrations/00002_tiles.sql.
	tileLayer = "activities"
)

// appConfig is what the frontend needs to know about the backend. It holds
// no user data.
type appConfig struct {
	Tiles  tilesConfig  `json:"tiles"`
	Import importConfig `json:"import"`
	// Sports are the recognized sport ids.
	Sports []string   `json:"sports"`
	Auth   authConfig `json:"auth"`
}

type authConfig struct {
	// Mode is "password" when users sign in, or "dev" when every request
	// is served as one development user.
	Mode              string `json:"mode"`
	PasswordMinLength int    `json:"passwordMinLength"`
	PasswordMaxLength int    `json:"passwordMaxLength"`
}

type tilesConfig struct {
	// URL is a path relative to the API's origin.
	URL         string `json:"url"`
	SourceLayer string `json:"sourceLayer"`
	MinZoom     int    `json:"minZoom"`
	MaxZoom     int    `json:"maxZoom"`
}

type importConfig struct {
	AcceptedExtensions []string      `json:"acceptedExtensions"`
	DuplicateRule      duplicateRule `json:"duplicateRule"`
}

// duplicateRule is importer.DuplicateRule, so that what users are told
// always matches what the importer does.
type duplicateRule struct {
	StartToleranceS   float64 `json:"startToleranceS"`
	RelativeTolerance float64 `json:"relativeTolerance"`
}

func (s *Server) config(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, r, http.StatusOK, appConfig{
		Tiles: tilesConfig{
			URL:         tilesPath,
			SourceLayer: tileLayer,
			MinZoom:     minZoom,
			MaxZoom:     maxZoom,
		},
		Import: importConfig{
			AcceptedExtensions: importer.AcceptedExtensions(),
			DuplicateRule: duplicateRule{
				StartToleranceS:   importer.DuplicateRule.StartTolerance.Seconds(),
				RelativeTolerance: importer.DuplicateRule.RelativeTolerance,
			},
		},
		Sports: activity.Sports(),
		Auth: authConfig{
			Mode:              s.authMode(),
			PasswordMinLength: password.MinLength,
			PasswordMaxLength: password.MaxLength,
		},
	})
}
