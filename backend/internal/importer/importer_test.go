package importer

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"

	"github.com/jlindbergh/track-a-doodle/backend/internal/activity"
	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

func TestImportReportsInvalidFiles(t *testing.T) {
	// Invalid files are rejected before anything is stored, so no store is needed.
	im := New(nil)
	for name, doc := range map[string]string{
		"not xml": "not xml",
		"invalid position": `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><trkseg>
			<trkpt lat="59.3" lon="18.0"/><trkpt lat="NaN" lon="18.1"/>
		</trkseg></trk></gpx>`,
	} {
		t.Run(name, func(t *testing.T) {
			res, err := im.Import(context.Background(), "user", "dir/run.gpx", []byte(doc), Options{})
			if err != nil {
				t.Fatalf("Import returned error %v; invalid files must be reported in the Result", err)
			}
			if res.Status != StatusFailed || res.Error == "" || res.File != "dir/run.gpx" {
				t.Errorf("got %+v, want status %q with an error message", res, StatusFailed)
			}
		})
	}
}

func TestImportReportsInvalidPositionLocation(t *testing.T) {
	doc := `<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><trkseg>
		<trkpt lat="59.3" lon="18.0"/><trkpt lat="95" lon="18.1"/>
	</trkseg></trk></gpx>`
	res, err := New(nil).Import(context.Background(), "user", "run.gpx", []byte(doc), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "point 2 of track 1, segment 1 has latitude 95") {
		t.Errorf("Error = %q, want it to say which point is wrong", res.Error)
	}
}

// recordingStore records the activities it is asked to insert.
type recordingStore struct {
	got []store.NewActivity
}

func (s *recordingStore) InsertActivity(_ context.Context, _ string, na store.NewActivity, _ *activity.DuplicateRule) (store.InsertResult, error) {
	s.got = append(s.got, na)
	return store.InsertResult{Status: store.Inserted, New: activity.Facts{ID: "id"}}, nil
}

func TestImportChoosesFormatAndSource(t *testing.T) {
	fit := encodeFIT(t)
	tests := []struct {
		name, filename string
		data           []byte
		opts           Options
		wantSource     string
	}{
		{"gpx", "dir/Run.GPX", []byte(validGPX), Options{}, "gpx"},
		{"fit", "123.fit", fit, Options{}, "fit"},
		{"source given", "123.fit", fit, Options{Source: "garmin"}, "garmin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &recordingStore{}
			res, err := New(st).Import(context.Background(), "user", tt.filename, tt.data, tt.opts)
			if err != nil || res.Status != StatusImported {
				t.Fatalf("Import = %+v, %v; want imported", res, err)
			}
			if len(st.got) != 1 || st.got[0].Source != tt.wantSource {
				t.Errorf("stored %+v, want source %q", st.got, tt.wantSource)
			}
		})
	}
}

func TestImportUnsupportedType(t *testing.T) {
	res, err := New(nil).Import(context.Background(), "user", "run.tcx", []byte("<x/>"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFailed || !strings.Contains(res.Error, ".fit or .gpx") {
		t.Errorf("got %+v, want a failure that lists the supported types", res)
	}
}

func TestAccepts(t *testing.T) {
	for name, want := range map[string]bool{"a.gpx": true, "b.FIT": true, "c.tcx": false, "fit": false} {
		if got := Accepts(name); got != want {
			t.Errorf("Accepts(%q) = %v, want %v", name, got, want)
		}
	}
}

// encodeFIT returns a FIT activity file with a two-point GPS track.
func encodeFIT(t *testing.T) []byte {
	t.Helper()
	t0 := time.Date(2026, 5, 1, 6, 0, 0, 0, time.UTC)
	fa := filedef.NewActivity()
	fa.FileId = *mesgdef.NewFileId(nil).SetType(typedef.FileActivity).SetTimeCreated(t0)
	fa.Sessions = []*mesgdef.Session{mesgdef.NewSession(nil).SetSport(typedef.SportRunning).SetStartTime(t0)}
	fa.Records = []*mesgdef.Record{
		mesgdef.NewRecord(nil).SetTimestamp(t0).SetPositionLatDegrees(59.3).SetPositionLongDegrees(18.0),
		mesgdef.NewRecord(nil).SetTimestamp(t0.Add(time.Minute)).SetPositionLatDegrees(59.4).SetPositionLongDegrees(18.1),
	}
	f := fa.ToFIT(nil)
	var buf bytes.Buffer
	if err := encoder.New(&buf).Encode(&f); err != nil {
		t.Fatalf("encode fit: %v", err)
	}
	return buf.Bytes()
}
