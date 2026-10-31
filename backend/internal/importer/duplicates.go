package importer

import (
	"time"

	"github.com/jlindbergh/track-a-doodle/backend/internal/activity"
)

// Duplicate detection.
//
// A file is "already imported" when the user has imported a byte-identical
// file before. That is always skipped.
//
// A new activity is a "possible duplicate" of one of the user's existing
// activities when ALL of these hold:
//
//   - their start times differ by at most DuplicateStartTolerance,
//   - their distances differ by at most DuplicateRelativeTolerance of the
//     longer one, and
//   - their durations differ by at most DuplicateRelativeTolerance of the
//     longer one (skipped if either duration is unknown).
//
// GPS points are deliberately not compared: Garmin, Strava, Runkeeper etc.
// record and resample them differently, so the same activity rarely has
// identical points in two exports.
//
// Possible duplicates are not stored unless the user explicitly asks for it.
// Files without timestamps can only be detected as already imported.
//
// The frontend reads these values from GET /api/config, so changing
// them here also changes what users are told.
const (
	// DuplicateStartTolerance is how far apart two start times may be.
	DuplicateStartTolerance = 2 * time.Second

	// DuplicateRelativeTolerance is how much distance and duration may
	// differ, as a fraction of the larger value (0.02 = 2%).
	DuplicateRelativeTolerance = 0.02
)

// DuplicateRule is the rule above.
var DuplicateRule = activity.DuplicateRule{
	StartTolerance:    DuplicateStartTolerance,
	RelativeTolerance: DuplicateRelativeTolerance,
}
