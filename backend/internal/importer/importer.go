// Package importer imports activity files for a user. It is shared by the
// HTTP upload endpoint and the batch import command.
package importer

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jlindbergh/track-a-doodle/backend/internal/activity"
	"github.com/jlindbergh/track-a-doodle/backend/internal/fitfile"
	"github.com/jlindbergh/track-a-doodle/backend/internal/gpxfile"
	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

// MaxFileSize is the largest file accepted for import.
const MaxFileSize = 50 << 20

// parsers maps the extension of each file format that can be imported to
// its parser. fallbackName names activities whose file has no name.
var parsers = map[string]func(data []byte, fallbackName string) (activity.Parsed, error){
	".gpx": gpxfile.Parse,
	".fit": fitfile.Parse,
}

// AcceptedExtensions returns the extensions of the files that can be
// imported, sorted.
func AcceptedExtensions() []string {
	exts := make([]string, 0, len(parsers))
	for ext := range parsers {
		exts = append(exts, ext)
	}
	slices.Sort(exts)
	return exts
}

// Accepts reports whether filename has one of the AcceptedExtensions,
// ignoring case.
func Accepts(filename string) bool {
	ext := filepath.Ext(filename)
	_, ok := parsers[strings.ToLower(ext)]
	return ok
}

// Status is the outcome of importing one file.
type Status string

const (
	// StatusImported means the file was stored as a new activity.
	StatusImported Status = "imported"
	// StatusAlreadyImported means the user has imported this exact file before.
	StatusAlreadyImported Status = "already_imported"
	// StatusPossibleDuplicate means the file matches an existing activity by
	// the DuplicateRule and was not stored. Import it again with
	// Options.AllowDuplicates to store it anyway.
	StatusPossibleDuplicate Status = "possible_duplicate"
	// StatusNoGPSTrack means the file was read but has no GPS track (e.g. an
	// indoor or treadmill activity), so there is nothing to show on the map.
	// Activity holds its name and sport.
	StatusNoGPSTrack Status = "no_gps_track"
	// StatusFailed means the file could not be read, or could not be stored
	// because of a server error (see Import).
	StatusFailed Status = "failed"
)

// storeFailedMessage is what users are told about a file that could not be
// stored. The cause is for the server log, not for them.
const storeFailedMessage = "could not be saved because of a server error; try importing it again"

// Result describes what happened to one file.
type Result struct {
	File       string `json:"file"`
	Status     Status `json:"status"`
	ActivityID string `json:"activityId,omitempty"`
	Error      string `json:"error,omitempty"`
	// Activity is the activity read from the file.
	Activity *activity.Facts `json:"activity,omitempty"`
	// Existing is the stored activity this file matched, for
	// already_imported and possible_duplicate.
	Existing *activity.Facts `json:"existing,omitempty"`
	// Comparison is how Activity and Existing differ, for possible_duplicate.
	Comparison *activity.Comparison `json:"comparison,omitempty"`
}

// Options control an import.
type Options struct {
	// AllowDuplicates stores possible duplicates instead of reporting them.
	// Files that were already imported are skipped regardless.
	AllowDuplicates bool
	// Source is where the activity comes from, stored with it. It defaults
	// to the file's format: "gpx" or "fit".
	Source string
	// ExternalID is the source's own id for the activity. An activity with
	// the same Source and ExternalID is only stored once.
	ExternalID string
	// Name names the activity if the file has no name of its own (FIT files
	// never do). It defaults to the filename without its extension.
	Name string
}

// Store stores activities; *store.Store implements it.
type Store interface {
	InsertActivity(ctx context.Context, userID string, na store.NewActivity, rule *activity.DuplicateRule) (store.InsertResult, error)
}

// Importer stores imported activities.
type Importer struct {
	store Store
}

// New returns an Importer that writes to s.
func New(s Store) *Importer {
	return &Importer{store: s}
}

// Import parses an activity file and stores it as an activity owned by
// userID. The file's format is chosen by the extension of filename (see
// AcceptedExtensions). Problems with the file are reported in the Result,
// with a nil error.
//
// If the activity cannot be stored, Import returns the error together with
// a StatusFailed Result that tells the user to try again, so that callers can
// log the error and still report the file, then go on with the next one.
func (im *Importer) Import(ctx context.Context, userID, filename string, data []byte, opts Options) (Result, error) {
	res := Result{File: filename}

	ext := strings.ToLower(filepath.Ext(filename))
	parse, ok := parsers[ext]
	if !ok {
		res.Status, res.Error = StatusFailed, fmt.Sprintf("unsupported file type; import %s files", strings.Join(AcceptedExtensions(), " or "))
		return res, nil
	}
	if len(data) > MaxFileSize {
		res.Status, res.Error = StatusFailed, "file too large"
		return res, nil
	}

	name := cmp.Or(opts.Name, strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
	act, err := parse(data, name)
	var noTrack *activity.NoTrackError
	if errors.As(err, &noTrack) {
		res.Status = StatusNoGPSTrack
		res.Activity = &activity.Facts{Name: noTrack.Name, Sport: noTrack.Sport}
		return res, nil
	}
	if err != nil {
		res.Status, res.Error = StatusFailed, err.Error()
		return res, nil
	}

	rule := &DuplicateRule
	if opts.AllowDuplicates {
		rule = nil
	}

	sum := sha256.Sum256(data)
	ins, err := im.store.InsertActivity(ctx, userID, store.NewActivity{
		Activity:       act,
		Source:         cmp.Or(opts.Source, strings.TrimPrefix(ext, ".")),
		ExternalID:     opts.ExternalID,
		SourceFilename: filepath.Base(filename),
		ContentSHA256:  hex.EncodeToString(sum[:]),
	}, rule)
	if err != nil {
		res.Status, res.Error = StatusFailed, storeFailedMessage
		return res, fmt.Errorf("store activity: %w", err)
	}

	res.Activity, res.Existing, res.Comparison = &ins.New, ins.Existing, ins.Comparison
	switch ins.Status {
	case store.Inserted:
		res.Status, res.ActivityID = StatusImported, ins.New.ID
	case store.SameFile:
		res.Status = StatusAlreadyImported
	case store.PossibleDuplicate:
		res.Status = StatusPossibleDuplicate
	default:
		res = Result{File: filename, Status: StatusFailed, Error: storeFailedMessage}
		return res, fmt.Errorf("store activity: unexpected insert status %v", ins.Status)
	}
	return res, nil
}
