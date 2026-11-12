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

// spanned is a session starting at start and lasting elapsed.
func spanned(start, elapsed time.Duration) *mesgdef.Session {
	return mesgdef.NewSession(nil).SetSport(typedef.SportRunning).
		SetStartTime(t0.Add(start)).SetTotalElapsedTimeScaled(elapsed.Seconds())
}

// outside is a GPS track with records before and after a session from 20 s
// to 80 s after t0, like a file whose activity was trimmed afterwards.
func outside() []*mesgdef.Record {
	return []*mesgdef.Record{
		gps(5*time.Second, 59.3200, 18.0600), // before the timer was started
		gps(19*time.Second, 59.3250, 18.0650),
		gps(20*time.Second, 59.3293, 18.0686),
		gps(50*time.Second, 59.3300, 18.0700),
		gps(80*time.Second, 59.3310, 18.0710),
		gps(81*time.Second, 59.3315, 18.0715),  // within a second of the end
		gps(120*time.Second, 59.3400, 18.0800), // after the timer was stopped
	}
}

func TestParseSessionSpan(t *testing.T) {
	tests := []struct {
		name      string
		sessions  []*mesgdef.Session
		records   []*mesgdef.Record
		wantStart time.Duration
		wantDur   int
		wantLen   int
	}{
		{
			name:      "records before and after the session are left out",
			sessions:  []*mesgdef.Session{spanned(20*time.Second, 60*time.Second)},
			records:   outside(),
			wantStart: 19 * time.Second, wantDur: 62, wantLen: 5,
		},
		{
			name: "session end from its timestamp when the elapsed time is unknown",
			sessions: []*mesgdef.Session{mesgdef.NewSession(nil).SetSport(typedef.SportRunning).
				SetStartTime(t0.Add(20 * time.Second)).SetTimestamp(t0.Add(80 * time.Second))},
			records:   outside(),
			wantStart: 19 * time.Second, wantDur: 62, wantLen: 5,
		},
		{
			name: "several sessions span from the first start to the last end",
			sessions: []*mesgdef.Session{
				spanned(50*time.Second, 30*time.Second),
				spanned(30*time.Second, 10*time.Second),
			},
			records:   outside(),
			wantStart: 50 * time.Second, wantDur: 31, wantLen: 3,
		},
		{
			name:      "no sessions keeps every record",
			sessions:  nil,
			records:   outside(),
			wantStart: 5 * time.Second, wantDur: 115, wantLen: 7,
		},
		{
			name:      "session without an end keeps every record",
			sessions:  []*mesgdef.Session{session(typedef.SportRunning, "")},
			records:   outside(),
			wantStart: 5 * time.Second, wantDur: 115, wantLen: 7,
		},
		{
			name:      "fewer than two records in the session keeps every record",
			sessions:  []*mesgdef.Session{spanned(200*time.Second, 60*time.Second)},
			records:   outside(),
			wantStart: 5 * time.Second, wantDur: 115, wantLen: 7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := Parse(encode(t, typedef.FileActivity, tt.sessions, tt.records), "x")
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if want := t0.Add(tt.wantStart); a.StartedAt == nil || !a.StartedAt.Equal(want) {
				t.Errorf("StartedAt = %v, want %v", a.StartedAt, want)
			}
			if a.DurationS == nil || *a.DurationS != tt.wantDur {
				t.Errorf("DurationS = %v, want %d", a.DurationS, tt.wantDur)
			}
			if len(a.Lines) != 1 || len(a.Lines[0]) != tt.wantLen {
				t.Errorf("Lines = %v, want one line of %d points", a.Lines, tt.wantLen)
			}
		})
	}
}

func TestParseChainedSessionSpan(t *testing.T) {
	// A multisport activity recorded as two chained FIT files, each with
	// GPS records outside its session.
	first := encode(t, typedef.FileActivity, []*mesgdef.Session{spanned(20*time.Second, 30*time.Second)}, []*mesgdef.Record{
		gps(0, 59.3200, 18.0600),
		gps(20*time.Second, 59.3293, 18.0686),
		gps(50*time.Second, 59.3300, 18.0700),
	})
	second := encode(t, typedef.FileActivity, []*mesgdef.Session{spanned(60*time.Second, 40*time.Second)}, []*mesgdef.Record{
		gps(60*time.Second, 59.3310, 18.0710),
		gps(100*time.Second, 59.3320, 18.0720),
		gps(130*time.Second, 59.3400, 18.0800),
	})
	a, err := Parse(append(first, second...), "x")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if want := t0.Add(20 * time.Second); a.StartedAt == nil || !a.StartedAt.Equal(want) {
		t.Errorf("StartedAt = %v, want %v", a.StartedAt, want)
	}
	if a.DurationS == nil || *a.DurationS != 80 {
		t.Errorf("DurationS = %v, want 80", a.DurationS)
	}
	if len(a.Lines) != 1 || len(a.Lines[0]) != 4 {
		t.Errorf("Lines = %v, want one line of 4 points", a.Lines)
	}
}
