// Package gpxfile turns GPX files into activities ready to be stored.
package gpxfile

import (
	"fmt"
	"time"

	"github.com/tkrajina/gpxgo/gpx"

	"github.com/jlindbergh/track-a-doodle/backend/internal/activity"
)

// Parse parses a GPX document. fallbackName is used when the file has no
// name. All tracks (or, if the file has none, routes) in the file become
// lines of the same activity.
//
// A file without a GPS track gives an *activity.NoTrackError; positions and
// timestamps that cannot be stored give errors wrapping
// activity.ErrInvalidPosition and activity.ErrInvalidTimestamps.
func Parse(data []byte, fallbackName string) (activity.Parsed, error) {
	g, err := gpx.ParseBytes(data)
	if err != nil {
		return activity.Parsed{}, fmt.Errorf("parse gpx: %w", err)
	}

	a := activity.Parsed{Name: g.Name}
	var trackNames []string
	for i, trk := range g.Tracks {
		if a.Name == "" {
			a.Name = trk.Name
		}
		if a.Sport == "" {
			a.Sport = trk.Type
		}
		trackNames = append(trackNames, trk.Name)
		for j, seg := range trk.Segments {
			if err := addLine(&a, seg.Points, fmt.Sprintf("track %d, segment %d", i+1, j+1)); err != nil {
				return activity.Parsed{}, err
			}
		}
	}
	if len(a.Lines) == 0 {
		for i, rte := range g.Routes {
			if a.Name == "" {
				a.Name = rte.Name
			}
			if err := addLine(&a, rte.Points, fmt.Sprintf("route %d", i+1)); err != nil {
				return activity.Parsed{}, err
			}
		}
	}
	a.Sport = activity.NormalizeSport(a.Sport)
	if a.Sport == activity.SportUnknown {
		a.Sport = activity.GuessSport(append(trackNames, fallbackName)...)
	}

	if len(a.Lines) == 0 {
		return activity.Parsed{}, &activity.NoTrackError{Name: a.Name, Sport: a.Sport}
	}
	if a.Name == "" {
		a.Name = fallbackName
	}

	if tb := g.TimeBounds(); !tb.StartTime.IsZero() {
		start := tb.StartTime.UTC()
		a.StartedAt = &start
		if !tb.EndTime.IsZero() {
			span := tb.EndTime.Sub(tb.StartTime)
			if span > activity.MaxDuration {
				return activity.Parsed{}, fmt.Errorf("%w: the first and last points are %d days apart (%s to %s)",
					activity.ErrInvalidTimestamps, int(span.Hours()/24), tb.StartTime.UTC().Format(time.DateOnly), tb.EndTime.UTC().Format(time.DateOnly))
			}
			d := int(span.Seconds())
			a.DurationS = &d
		}
	} else if g.Time != nil {
		start := g.Time.UTC()
		a.StartedAt = &start
	}

	return a, nil
}

// addLine adds points to a as a line, unless fewer than two of them have a
// GPS fix. where names the line in errors, e.g. "track 1, segment 2".
func addLine(a *activity.Parsed, points []gpx.GPXPoint, where string) error {
	line := make([]activity.LonLat, 0, len(points))
	for i, p := range points {
		if err := activity.CheckPosition(p.Latitude, p.Longitude); err != nil {
			return fmt.Errorf("%w: point %d of %s has %v", activity.ErrInvalidPosition, i+1, where, err)
		}
		// Some exporters write 0,0 for points without a GPS fix.
		if p.Latitude == 0 && p.Longitude == 0 {
			continue
		}
		line = append(line, activity.LonLat{Lon: p.Longitude, Lat: p.Latitude})
	}
	if len(line) >= 2 {
		a.Lines = append(a.Lines, line)
	}
	return nil
}
