// Package fitfile turns FIT activity files, the format Garmin and most other
// sports watches record in, into activities ready to be stored.
package fitfile

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/profile/basetype"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/proto"

	"github.com/jlindbergh/track-a-doodle/backend/internal/activity"
)

// ErrNotActivity is returned, wrapped, for FIT files that are not activity
// recordings, e.g. courses, workouts or device settings.
var ErrNotActivity = errors.New("not a FIT activity file")

// sportMultisport is the sport of a file whose sessions are different sports,
// e.g. a triathlon. It is what Garmin calls such activities.
const sportMultisport = "multisport"

// Parse parses a FIT activity file. FIT files have no activity name, so the
// activity is named fallbackName.
//
// The track is every record with a GPS position, as one line, and the start
// time and duration are those of the first and last of these records. A GPX
// export of the same activity holds just these points, so importing both is
// caught as a possible duplicate (see importer.DuplicateRule). When the
// records have no timestamps, the sessions' start and elapsed times are used.
//
// Like gpxfile.Parse, a file without a GPS track gives an
// *activity.NoTrackError, and positions and timestamps that cannot be stored
// give errors wrapping activity.ErrInvalidPosition and
// activity.ErrInvalidTimestamps.
func Parse(data []byte, fallbackName string) (activity.Parsed, error) {
	fa, err := decode(data)
	if err != nil {
		return activity.Parsed{}, err
	}

	a := activity.Parsed{Sport: sport(fa, fallbackName)}

	line := make([]activity.LonLat, 0, len(fa.Records))
	var first, last time.Time
	for i, r := range fa.Records {
		if r.PositionLat == basetype.Sint32Invalid || r.PositionLong == basetype.Sint32Invalid {
			continue
		}
		lat, lon := r.PositionLatDegrees(), r.PositionLongDegrees()
		if err := activity.CheckPosition(lat, lon); err != nil {
			return activity.Parsed{}, fmt.Errorf("%w: record %d has %v", activity.ErrInvalidPosition, i+1, err)
		}
		// Some devices write 0,0 before they have a GPS fix.
		if lat == 0 && lon == 0 {
			continue
		}
		line = append(line, activity.LonLat{Lon: lon, Lat: lat})
		if t := r.Timestamp; !t.IsZero() {
			if first.IsZero() || t.Before(first) {
				first = t
			}
			if t.After(last) {
				last = t
			}
		}
	}
	if len(line) < 2 {
		return activity.Parsed{}, &activity.NoTrackError{Sport: a.Sport}
	}
	a.Lines = [][]activity.LonLat{line}
	a.Name = fallbackName

	if !first.IsZero() {
		span := last.Sub(first)
		if span > activity.MaxDuration {
			return activity.Parsed{}, fmt.Errorf("%w: the first and last GPS records are %d days apart (%s to %s)",
				activity.ErrInvalidTimestamps, int(span.Hours()/24), first.UTC().Format(time.DateOnly), last.UTC().Format(time.DateOnly))
		}
		start := first.UTC()
		d := int(span.Seconds())
		a.StartedAt, a.DurationS = &start, &d
	} else {
		a.StartedAt, a.DurationS = sessionTimes(fa.Sessions)
	}
	return a, nil
}

// decode decodes data, which may hold several chained FIT files, as one
// activity.
func decode(data []byte) (*filedef.Activity, error) {
	var mesgs []proto.Message
	dec := decoder.New(bytes.NewReader(data))
	for dec.Next() {
		f, err := dec.Decode()
		if err != nil {
			return nil, fmt.Errorf("parse fit: %w", err)
		}
		mesgs = append(mesgs, f.Messages...)
	}
	if len(mesgs) == 0 {
		return nil, errors.New("parse fit: the file is empty")
	}
	fa := filedef.NewActivity(mesgs...)
	if t := fa.FileId.Type; t != typedef.FileActivity {
		return nil, fmt.Errorf("%w: it is a %s file", ErrNotActivity, t)
	}
	return fa, nil
}

// sport returns the sport of fa's sessions, or of its sport messages if it
// has no sessions. Sessions of different sports make it a multisport
// activity. When the file only says "generic", the sport is guessed from
// the sport's name on the device ("Trail Run") and then from fallbackName.
func sport(fa *filedef.Activity, fallbackName string) string {
	type sportMesg struct {
		sport typedef.Sport
		name  string
	}
	var mesgs []sportMesg
	for _, s := range fa.Sessions {
		mesgs = append(mesgs, sportMesg{s.Sport, s.SportProfileName})
	}
	if len(mesgs) == 0 {
		for _, s := range fa.Sports {
			mesgs = append(mesgs, sportMesg{s.Sport, s.Name})
		}
	}

	found := activity.SportUnknown
	for _, m := range mesgs {
		var sp string
		if m.sport != typedef.SportGeneric && m.sport != typedef.SportAll && m.sport != typedef.SportInvalid {
			sp = activity.NormalizeSport(m.sport.String())
		} else {
			sp = activity.GuessSport(m.name)
		}
		switch {
		case sp == activity.SportUnknown:
		case found == activity.SportUnknown:
			found = sp
		case sp != found:
			return sportMultisport
		}
	}
	if found == activity.SportUnknown {
		found = activity.GuessSport(fallbackName)
	}
	return found
}

// sessionTimes returns the start time of the first session and the elapsed
// time of all sessions together, each nil if unknown.
func sessionTimes(sessions []*mesgdef.Session) (*time.Time, *int) {
	var start *time.Time
	var total float64
	known := false
	for _, s := range sessions {
		if t := s.StartTime; !t.IsZero() && (start == nil || t.Before(*start)) {
			utc := t.UTC()
			start = &utc
		}
		if s.TotalElapsedTime != basetype.Uint32Invalid {
			total += s.TotalElapsedTimeScaled()
			known = true
		}
	}
	if !known {
		return start, nil
	}
	d := int(total)
	return start, &d
}
