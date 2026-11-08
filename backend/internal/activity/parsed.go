package activity

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ErrNoTrack is returned when a file has no track or route with at least
// two GPS points, e.g. an indoor or treadmill activity. The returned error is
// a *NoTrackError; use errors.As to get the activity's name and sport.
var ErrNoTrack = errors.New("file contains no GPS track")

// NoTrackError describes a file that was readable but has no GPS track.
type NoTrackError struct {
	// Name is the activity name written in the file, or "" if it has none.
	// The fallback name is not used, since nothing is stored.
	Name  string
	Sport string
}

func (e *NoTrackError) Error() string { return ErrNoTrack.Error() }

// Is makes errors.Is(err, ErrNoTrack) true.
func (e *NoTrackError) Is(target error) bool { return target == ErrNoTrack }

// ErrInvalidPosition is returned, wrapped with where in the file it is, when
// a point's latitude or longitude is not a number or out of range. Nothing
// from such a file is stored: the database cannot project those points, and
// skipping them would silently change the track.
var ErrInvalidPosition = errors.New("invalid GPS position")

// ErrInvalidTimestamps is returned, wrapped, when the file's first and last
// timestamps are further apart than MaxDuration.
var ErrInvalidTimestamps = errors.New("invalid timestamps")

// MaxDuration is the longest an activity can last: its duration is stored
// in seconds as a 32-bit integer, which is about 68 years.
const MaxDuration = math.MaxInt32 * time.Second

// Parsed is an activity read from a file (GPX or FIT), ready to be stored.
type Parsed struct {
	Name      string
	Sport     string
	StartedAt *time.Time
	DurationS *int
	// Lines holds the GPS track, e.g. one []LonLat per GPX track segment.
	Lines [][]LonLat
}

// LonLat is a WGS 84 coordinate.
type LonLat struct {
	Lon, Lat float64
}

// CheckPosition returns an error describing what is wrong with lat and lon,
// or nil if they are a valid WGS 84 position.
func CheckPosition(lat, lon float64) error {
	if err := checkCoordinate("latitude", lat, 90); err != nil {
		return err
	}
	return checkCoordinate("longitude", lon, 180)
}

func checkCoordinate(name string, v, limit float64) error {
	switch {
	case math.IsNaN(v) || math.IsInf(v, 0):
		return fmt.Errorf("%s %v, which is not a number", name, v)
	case v < -limit || v > limit:
		return fmt.Errorf("%s %v, outside -%v to %v", name, v, limit, limit)
	}
	return nil
}

// WKT returns the activity's lines as a WKT MULTILINESTRING in EPSG:4326.
func (a Parsed) WKT() string {
	var b strings.Builder
	b.WriteString("MULTILINESTRING(")
	for i, line := range a.Lines {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('(')
		for j, p := range line {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.FormatFloat(p.Lon, 'f', -1, 64))
			b.WriteByte(' ')
			b.WriteString(strconv.FormatFloat(p.Lat, 'f', -1, 64))
		}
		b.WriteByte(')')
	}
	b.WriteByte(')')
	return b.String()
}
