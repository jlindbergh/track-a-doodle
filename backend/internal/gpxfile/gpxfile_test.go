package gpxfile

import (
	"errors"
	"testing"
	"time"
)

const twoSegments = `<?xml version="1.0" encoding="UTF-8"?>
<gpx version="1.1" creator="test" xmlns="http://www.topografix.com/GPX/1/1">
  <trk>
    <name>Morning run</name>
    <type>9</type>
    <trkseg>
      <trkpt lat="59.3293" lon="18.0686"><time>2026-05-01T06:00:00Z</time></trkpt>
      <trkpt lat="59.3300" lon="18.0700"><time>2026-05-01T06:01:00Z</time></trkpt>
    </trkseg>
    <trkseg>
      <trkpt lat="59.3310" lon="18.0710"><time>2026-05-01T06:05:00Z</time></trkpt>
      <trkpt lat="59.3320" lon="18.0720"><time>2026-05-01T06:30:00Z</time></trkpt>
    </trkseg>
  </trk>
</gpx>`

func TestParse(t *testing.T) {
	a, err := Parse([]byte(twoSegments), "fallback")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if a.Name != "Morning run" {
		t.Errorf("Name = %q, want %q", a.Name, "Morning run")
	}
	if a.Sport != "running" {
		t.Errorf("Sport = %q, want %q", a.Sport, "running")
	}
	if want := time.Date(2026, 5, 1, 6, 0, 0, 0, time.UTC); a.StartedAt == nil || !a.StartedAt.Equal(want) {
		t.Errorf("StartedAt = %v, want %v", a.StartedAt, want)
	}
	if a.DurationS == nil || *a.DurationS != 30*60 {
		t.Errorf("DurationS = %v, want %d", a.DurationS, 30*60)
	}
	want := "MULTILINESTRING((18.0686 59.3293,18.07 59.33),(18.071 59.331,18.072 59.332))"
	if got := a.WKT(); got != want {
		t.Errorf("WKT() = %q, want %q", got, want)
	}
}

func TestParseFallbackNameAndUnknownSport(t *testing.T) {
	doc := `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><trkseg>
		<trkpt lat="1" lon="2"/><trkpt lat="3" lon="4"/>
	</trkseg></trk></gpx>`
	a, err := Parse([]byte(doc), "my-file")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if a.Name != "my-file" || a.Sport != "unknown" || a.StartedAt != nil {
		t.Errorf("got Name=%q Sport=%q StartedAt=%v", a.Name, a.Sport, a.StartedAt)
	}
}

func TestParseNoTrack(t *testing.T) {
	doc := `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><trkseg>
		<trkpt lat="1" lon="2"/>
	</trkseg></trk></gpx>`
	_, err := Parse([]byte(doc), "x")
	if !errors.Is(err, ErrNoTrack) {
		t.Fatalf("err = %v, want ErrNoTrack", err)
	}
	var nt *NoTrackError
	if errors.As(err, &nt) && nt.Name != "" {
		t.Errorf("Name = %q, want \"\": the fallback name is not used without a track", nt.Name)
	}
}

func TestParseNoTrackReportsActivity(t *testing.T) {
	tests := map[string]string{
		"no segments": `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1">
			<trk><name>Zwift ride</name><type>VirtualRide</type></trk></gpx>`,
		"only 0,0 points": `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1">
			<trk><name>Zwift ride</name><type>VirtualRide</type><trkseg>
			<trkpt lat="0" lon="0"/><trkpt lat="0" lon="0"/><trkpt lat="0" lon="0"/>
			</trkseg></trk></gpx>`,
	}
	for name, doc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(doc), "x")
			var nt *NoTrackError
			if !errors.As(err, &nt) {
				t.Fatalf("err = %v, want *NoTrackError", err)
			}
			if nt.Name != "Zwift ride" || nt.Sport != "virtual_ride" {
				t.Errorf("got Name=%q Sport=%q", nt.Name, nt.Sport)
			}
		})
	}
}

func TestParseInvalidPosition(t *testing.T) {
	tests := []struct {
		name, lat, lon, want string
	}{
		{"latitude too large", "95", "18", "invalid GPS position: point 2 of track 1, segment 1 has latitude 95, outside -90 to 90"},
		{"latitude too small", "-90.5", "18", "invalid GPS position: point 2 of track 1, segment 1 has latitude -90.5, outside -90 to 90"},
		{"longitude too large", "59", "180.1", "invalid GPS position: point 2 of track 1, segment 1 has longitude 180.1, outside -180 to 180"},
		{"latitude NaN", "NaN", "18", "invalid GPS position: point 2 of track 1, segment 1 has latitude NaN, which is not a number"},
		{"longitude infinite", "59", "Inf", "invalid GPS position: point 2 of track 1, segment 1 has longitude +Inf, which is not a number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><trkseg>
				<trkpt lat="59.3" lon="18.0"/><trkpt lat="` + tt.lat + `" lon="` + tt.lon + `"/><trkpt lat="59.4" lon="18.1"/>
			</trkseg></trk></gpx>`
			_, err := Parse([]byte(doc), "x")
			if !errors.Is(err, ErrInvalidPosition) {
				t.Fatalf("err = %v, want ErrInvalidPosition", err)
			}
			if err.Error() != tt.want {
				t.Errorf("err = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestParseInvalidPositionInRoute(t *testing.T) {
	doc := `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><rte>
		<rtept lat="59.3" lon="18.0"/><rtept lat="59.4" lon="-200"/>
	</rte></gpx>`
	_, err := Parse([]byte(doc), "x")
	if want := "invalid GPS position: point 2 of route 1 has longitude -200, outside -180 to 180"; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestParseLimitPositions(t *testing.T) {
	doc := `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><trkseg>
		<trkpt lat="-90" lon="-180"/><trkpt lat="90" lon="180"/>
	</trkseg></trk></gpx>`
	if _, err := Parse([]byte(doc), "x"); err != nil {
		t.Errorf("Parse: %v, want positions on the limits to be accepted", err)
	}
}

func TestParseInvalidTimestamps(t *testing.T) {
	doc := `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><trkseg>
		<trkpt lat="59.3" lon="18.0"><time>2001-01-01T00:00:00Z</time></trkpt>
		<trkpt lat="59.4" lon="18.1"><time>2100-01-01T00:00:00Z</time></trkpt>
	</trkseg></trk></gpx>`
	_, err := Parse([]byte(doc), "x")
	if !errors.Is(err, ErrInvalidTimestamps) {
		t.Fatalf("err = %v, want ErrInvalidTimestamps", err)
	}
	if want := "invalid timestamps: the first and last points are 36159 days apart (2001-01-01 to 2100-01-01)"; err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

func TestNormalizeSport(t *testing.T) {
	for in, want := range map[string]string{
		"":                              "unknown",
		" Running ":                     "running",
		"Run":                           "running",
		"VirtualRide":                   "virtual_ride",
		"indoor_cycling":                "indoor_cycling",
		"Indoor Rowing":                 "indoor_rowing",
		"9":                             "running",
		"NordicSki":                     "cross_country_skiing",
		"cross_country_skiing_ws":       "cross_country_skiing",
		"skate_skiing":                  "cross_country_skiing",
		"AlpineSki":                     "downhill_skiing",
		"resort_skiing_snowboarding_ws": "downhill_skiing",
		"IceSkate":                      "skating",
		"InlineSkate":                   "skating",
		"skating_ws":                    "skating",
		"open_water_swimming":           "swimming",
		"Rowing":                        "rowing",
		"rowing_v2":                     "rowing",
		"StandUpPaddling":               "stand_up_paddleboarding",
		"stand_up_paddleboarding_v2":    "stand_up_paddleboarding",
		"BackcountrySki":                "backcountry_ski",
	} {
		if got := normalizeSport(in); got != want {
			t.Errorf("normalizeSport(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSportFromName(t *testing.T) {
	for in, want := range map[string]string{
		"":                                 "unknown",
		"Morning Run":                      "running",
		"Afternoon Ride":                   "cycling",
		"Stockholm - Cycling":              "cycling",
		"Morning_Run":                      "running",
		"Hike!":                            "hiking",
		"Run, running":                     "running",
		"Brunch":                           "unknown",
		"Walk then run":                    "unknown",
		"activity_123456":                  "unknown",
		"Morning Nordic Ski":               "cross_country_skiing",
		"Åre Cross Country Classic Skiing": "cross_country_skiing",
		"Skate Skiing":                     "cross_country_skiing",
		"Afternoon Alpine Ski":             "downhill_skiing",
		"Sälen Resort Skiing":              "downhill_skiing",
		"Evening Ice Skate":                "skating",
		"Skating on the lake":              "skating",
		"Morning Ski":                      "unknown",
		"Cross country run":                "running",
		"Skate ski then skate":             "unknown",
		"Morning Stand Up Paddle":          "stand_up_paddleboarding",
		"Evening SUP":                      "stand_up_paddleboarding",
		"Paddle board tour":                "stand_up_paddleboarding",
		"Kayak paddle":                     "unknown",
		"Lunch Rowing":                     "rowing",
		"Open water swim":                  "swimming",
	} {
		if got := sportFromName(in); got != want {
			t.Errorf("sportFromName(%q) = %q, want %q", in, got, want)
		}
	}
}

// sportDoc returns a GPX document with one track. Empty arguments leave out
// the metadata name, track name or type; trkName is written as CDATA.
func sportDoc(metaName, trkName, typ string) string {
	doc := `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1">`
	if metaName != "" {
		doc += `<metadata><name>` + metaName + `</name></metadata>`
	}
	doc += `<trk>`
	if trkName != "" {
		doc += `<name><![CDATA[` + trkName + `]]></name>`
	}
	if typ != "" {
		doc += `<type>` + typ + `</type>`
	}
	return doc + `<trkseg><trkpt lat="1" lon="2"/><trkpt lat="3" lon="4"/></trkseg></trk></gpx>`
}

func TestParseSportFallback(t *testing.T) {
	tests := []struct {
		name              string
		doc, fallbackName string
		wantSport         string
	}{
		{"type wins over track name", sportDoc("", "Morning Run", "ride"), "Evening_Walk", "cycling"},
		{"unrecognized type wins", sportDoc("", "Morning Run", "kayaking"), "x", "kayaking"},
		{"track name in CDATA", sportDoc("", "Evening Walk", ""), "x", "walking"},
		{"filename when track name has no sport", sportDoc("", "Lunch", ""), "Morning_Run", "running"},
		{"filename when track names two sports", sportDoc("", "Walk then run", ""), "Swim", "swimming"},
		{"metadata name is not used", sportDoc("Morning Run", "Lunch", ""), "x", "unknown"},
		{"nothing matches", sportDoc("", "Lunch", ""), "activity_123", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := Parse([]byte(tt.doc), tt.fallbackName)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if a.Sport != tt.wantSport {
				t.Errorf("Sport = %q, want %q", a.Sport, tt.wantSport)
			}
		})
	}
}

func TestParseSportFallbackWithoutTrack(t *testing.T) {
	doc := `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><name>Treadmill run</name></trk></gpx>`
	var noTrack *NoTrackError
	if _, err := Parse([]byte(doc), "x"); !errors.As(err, &noTrack) {
		t.Fatalf("err = %v, want a NoTrackError", err)
	}
	if noTrack.Sport != "running" {
		t.Errorf("Sport = %q, want running", noTrack.Sport)
	}
}

func TestParseInvalid(t *testing.T) {
	if _, err := Parse([]byte("not xml"), "x"); err == nil {
		t.Error("expected an error for invalid input")
	}
}
