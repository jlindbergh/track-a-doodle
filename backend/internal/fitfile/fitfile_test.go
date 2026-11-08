package fitfile

import (
	"bytes"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"

	"github.com/jlindbergh/track-a-doodle/backend/internal/activity"
)

var t0 = time.Date(2026, 5, 1, 6, 0, 0, 0, time.UTC)

// encode returns a FIT file of the given type with sessions and records.
func encode(t *testing.T, fileType typedef.File, sessions []*mesgdef.Session, records []*mesgdef.Record) []byte {
	t.Helper()
	fa := filedef.NewActivity()
	fa.FileId = *mesgdef.NewFileId(nil).SetType(fileType).SetTimeCreated(t0).
		SetManufacturer(typedef.ManufacturerGarmin)
	fa.Sessions = sessions
	fa.Records = records
	f := fa.ToFIT(nil)
	var buf bytes.Buffer
	if err := encoder.New(&buf).Encode(&f); err != nil {
		t.Fatalf("encode fit: %v", err)
	}
	return buf.Bytes()
}

func session(sport typedef.Sport, profileName string) *mesgdef.Session {
	return mesgdef.NewSession(nil).SetSport(sport).SetSportProfileName(profileName).SetStartTime(t0)
}

func gps(offset time.Duration, lat, lon float64) *mesgdef.Record {
	return mesgdef.NewRecord(nil).SetTimestamp(t0.Add(offset)).SetPositionLatDegrees(lat).SetPositionLongDegrees(lon)
}

func noGPS(offset time.Duration) *mesgdef.Record {
	return mesgdef.NewRecord(nil).SetTimestamp(t0.Add(offset))
}

// track is a short GPS track starting 5 seconds after t0 and lasting a minute.
func track() []*mesgdef.Record {
	return []*mesgdef.Record{
		noGPS(0),
		gps(5*time.Second, 59.3293, 18.0686),
		gps(10*time.Second, 0, 0), // no fix yet on some devices
		gps(30*time.Second, 59.3300, 18.0700),
		gps(65*time.Second, 59.3310, 18.0710),
		noGPS(70 * time.Second),
	}
}

func TestParse(t *testing.T) {
	data := encode(t, typedef.FileActivity, []*mesgdef.Session{session(typedef.SportRunning, "Run")}, track())
	a, err := Parse(data, "fallback")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if a.Name != "fallback" {
		t.Errorf("Name = %q, want fallback", a.Name)
	}
	if a.Sport != activity.SportRunning {
		t.Errorf("Sport = %q, want running", a.Sport)
	}
	if want := t0.Add(5 * time.Second); a.StartedAt == nil || !a.StartedAt.Equal(want) {
		t.Errorf("StartedAt = %v, want %v (the first GPS record)", a.StartedAt, want)
	}
	if a.DurationS == nil || *a.DurationS != 60 {
		t.Errorf("DurationS = %v, want 60 (first to last GPS record)", a.DurationS)
	}
	want := []activity.LonLat{{Lon: 18.0686, Lat: 59.3293}, {Lon: 18.0700, Lat: 59.3300}, {Lon: 18.0710, Lat: 59.3310}}
	if len(a.Lines) != 1 || len(a.Lines[0]) != len(want) {
		t.Fatalf("Lines = %v, want one line of %d points", a.Lines, len(want))
	}
	for i, p := range a.Lines[0] {
		// Semicircles have a resolution of about 1e-7 degrees.
		if math.Abs(p.Lon-want[i].Lon) > 1e-6 || math.Abs(p.Lat-want[i].Lat) > 1e-6 {
			t.Errorf("point %d = %v, want %v", i, p, want[i])
		}
	}
}

func TestParseSport(t *testing.T) {
	tests := []struct {
		name     string
		sessions []*mesgdef.Session
		fallback string
		want     string
	}{
		{"device sport", []*mesgdef.Session{session(typedef.SportCycling, "")}, "x", activity.SportCycling},
		{"alias", []*mesgdef.Session{session(typedef.SportAlpineSkiing, "")}, "x", activity.SportDownhillSkiing},
		{"generic with profile name", []*mesgdef.Session{session(typedef.SportGeneric, "Trail Run")}, "x", activity.SportRunning},
		{"generic with fallback name", []*mesgdef.Session{session(typedef.SportGeneric, "")}, "Morning_Ride", activity.SportCycling},
		{"nothing known", []*mesgdef.Session{session(typedef.SportGeneric, "")}, "123456789", activity.SportUnknown},
		{"no sessions", nil, "Evening Walk", activity.SportWalking},
		{"same sport twice", []*mesgdef.Session{session(typedef.SportRunning, ""), session(typedef.SportRunning, "")}, "x", activity.SportRunning},
		{"different sports", []*mesgdef.Session{session(typedef.SportSwimming, ""), session(typedef.SportCycling, ""), session(typedef.SportRunning, "")}, "x", "multisport"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := Parse(encode(t, typedef.FileActivity, tt.sessions, track()), tt.fallback)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if a.Sport != tt.want {
				t.Errorf("Sport = %q, want %q", a.Sport, tt.want)
			}
		})
	}
}

func TestParseNoTrack(t *testing.T) {
	records := []*mesgdef.Record{noGPS(0), noGPS(time.Second), gps(2*time.Second, 59.3, 18.0)}
	data := encode(t, typedef.FileActivity, []*mesgdef.Session{session(typedef.SportTraining, "Strength")}, records)
	_, err := Parse(data, "x")
	var nt *activity.NoTrackError
	if !errors.As(err, &nt) {
		t.Fatalf("err = %v, want *activity.NoTrackError", err)
	}
	if nt.Name != "" || nt.Sport != "training" {
		t.Errorf("got Name=%q Sport=%q, want no name and training", nt.Name, nt.Sport)
	}
}

func TestParseSessionTimesWithoutRecordTimestamps(t *testing.T) {
	s := session(typedef.SportHiking, "").SetTotalElapsedTimeScaled(3600)
	records := []*mesgdef.Record{
		mesgdef.NewRecord(nil).SetPositionLatDegrees(59.3).SetPositionLongDegrees(18.0),
		mesgdef.NewRecord(nil).SetPositionLatDegrees(59.4).SetPositionLongDegrees(18.1),
	}
	a, err := Parse(encode(t, typedef.FileActivity, []*mesgdef.Session{s}, records), "x")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if a.StartedAt == nil || !a.StartedAt.Equal(t0) {
		t.Errorf("StartedAt = %v, want the session start %v", a.StartedAt, t0)
	}
	if a.DurationS == nil || *a.DurationS != 3600 {
		t.Errorf("DurationS = %v, want the session's 3600", a.DurationS)
	}
}

func TestParseInvalidPosition(t *testing.T) {
	records := track()
	// Semicircles that convert to a latitude of about 180 degrees.
	records = append(records, mesgdef.NewRecord(nil).SetTimestamp(t0.Add(time.Minute)).
		SetPositionLat(math.MaxInt32-1).SetPositionLong(0))
	_, err := Parse(encode(t, typedef.FileActivity, nil, records), "x")
	if !errors.Is(err, activity.ErrInvalidPosition) {
		t.Errorf("err = %v, want ErrInvalidPosition", err)
	}
}

func TestParseNotActivity(t *testing.T) {
	_, err := Parse(encode(t, typedef.FileCourse, nil, track()), "x")
	if !errors.Is(err, ErrNotActivity) {
		t.Errorf("err = %v, want ErrNotActivity", err)
	}
}

func TestParseInvalid(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty":   nil,
		"not fit": []byte("<gpx></gpx> and some more bytes to fill a header"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(data, "x"); err == nil {
				t.Error("expected an error")
			}
		})
	}
}
