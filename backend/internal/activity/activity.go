// Package activity holds the activity types and rules shared by the store,
// the importer and the API.
package activity

import "time"

// Facts are the values of an activity used to identify it.
type Facts struct {
	ID        string     `json:"id,omitempty"`
	Name      string     `json:"name"`
	Sport     string     `json:"sport"`
	StartedAt *time.Time `json:"startedAt"`
	DurationS *int       `json:"durationS"`
	DistanceM float64    `json:"distanceM"`
}

// Summary is a stored activity without its geometry.
type Summary struct {
	Facts
	// BBox is [west, south, east, north] in WGS 84.
	BBox [4]float64 `json:"bbox"`
	// SourceFilename is the name of the imported file, if known.
	SourceFilename *string   `json:"sourceFilename"`
	ImportedAt     time.Time `json:"importedAt"`
}
