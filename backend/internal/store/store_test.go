package store

import (
	"context"
	"crypto/rand"
	"os"
	"testing"
	"time"

	"github.com/jlindbergh/track-a-doodle/backend/internal/activity"
	"github.com/jlindbergh/track-a-doodle/backend/internal/gpxfile"
)

// These tests need a database with the schema from postgis/init, e.g.
//
//	TEST_DATABASE_URL=postgres://mytracks_app:dev-app-pw@db:5432/mytracks go test ./...
//
// Each test creates its own users and deletes them afterwards.

func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	s, err := New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func testUser(t *testing.T, s *Store) User {
	t.Helper()
	ctx := context.Background()
	u, err := s.EnsureUserByEmail(ctx, "test-"+rand.Text()+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := s.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, u.ID); err != nil {
			t.Errorf("delete test user: %v", err)
		}
	})
	return u
}

var baseStart = time.Date(2026, 6, 1, 7, 0, 0, 0, time.UTC)

// newActivity returns a straight track north from Stockholm. Its length is
// proportional to lengthFactor (1 = about 1.1 km), so distances can be
// varied by exact percentages.
func newActivity(startOffset time.Duration, durationS int, lengthFactor float64) NewActivity {
	start := baseStart.Add(startOffset)
	const n = 10
	line := make([]gpxfile.LonLat, n+1)
	for i := range line {
		line[i] = gpxfile.LonLat{Lon: 18.0, Lat: 59.3 + 0.01*lengthFactor*float64(i)/n}
	}
	return NewActivity{
		Activity: gpxfile.Activity{
			Name: "Run", Sport: "running", StartedAt: &start, DurationS: &durationS,
			Lines: [][]gpxfile.LonLat{line},
		},
		SourceFilename: "run.gpx",
		ContentSHA256:  rand.Text(), // a different file each time unless set
	}
}

var rule = &activity.DuplicateRule{StartTolerance: 2 * time.Second, RelativeTolerance: 0.02}

func insert(t *testing.T, s *Store, userID string, na NewActivity, r *activity.DuplicateRule) InsertResult {
	t.Helper()
	res, err := s.InsertActivity(context.Background(), userID, na, r)
	if err != nil {
		t.Fatalf("InsertActivity: %v", err)
	}
	return res
}

func TestInsertActivityDuplicates(t *testing.T) {
	s := testStore(t)

	tests := []struct {
		name string
		next NewActivity
		rule *activity.DuplicateRule
		want InsertStatus
	}{
		{"start 1s later, 1% longer", newActivity(time.Second, 3636, 1.01), rule, PossibleDuplicate},
		{"start 2s earlier, same", newActivity(-2*time.Second, 3600, 1), rule, PossibleDuplicate},
		{"start 3s later", newActivity(3*time.Second, 3600, 1), rule, Inserted},
		{"distance 3% longer", newActivity(0, 3600, 1.03), rule, Inserted},
		{"duration 3% longer", newActivity(0, 3708, 1), rule, Inserted},
		{"duplicates allowed", newActivity(0, 3600, 1), nil, Inserted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := testUser(t, s)
			first := insert(t, s, u.ID, newActivity(0, 3600, 1), rule)
			if first.Status != Inserted {
				t.Fatalf("first insert: status %v", first.Status)
			}

			got := insert(t, s, u.ID, tt.next, tt.rule)
			if got.Status != tt.want {
				t.Fatalf("status = %v, want %v (new %+v)", got.Status, tt.want, got.New)
			}
			if tt.want != PossibleDuplicate {
				return
			}
			if got.Existing == nil || got.Existing.ID != first.New.ID {
				t.Errorf("Existing = %+v, want activity %s", got.Existing, first.New.ID)
			}
			if got.Comparison == nil {
				t.Error("Comparison is nil")
			}
			if got.New.ID != "" || got.New.DistanceM == 0 {
				t.Errorf("New = %+v, want a measured activity without an ID", got.New)
			}
			list, err := s.ListActivities(context.Background(), u.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 1 {
				t.Errorf("stored %d activities, want 1: the possible duplicate must not be stored", len(list))
			}
		})
	}
}

func TestInsertActivityWithoutDuration(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s)
	insert(t, s, u.ID, newActivity(0, 3600, 1), rule)

	na := newActivity(0, 0, 1)
	na.Activity.DurationS = nil
	if got := insert(t, s, u.ID, na, rule); got.Status != PossibleDuplicate {
		t.Errorf("status = %v, want PossibleDuplicate (duration is skipped when unknown)", got.Status)
	}
}

func TestInsertActivitySameFile(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s)
	na := newActivity(0, 3600, 1)
	insert(t, s, u.ID, na, rule)

	// A byte-identical file is rejected even when duplicates are allowed.
	if got := insert(t, s, u.ID, na, nil); got.Status != SameFile {
		t.Errorf("status = %v, want SameFile", got.Status)
	}
}

func TestInsertActivityOtherUsersAreIgnored(t *testing.T) {
	s := testStore(t)
	alice := testUser(t, s)
	na := newActivity(0, 3600, 1)
	insert(t, s, alice.ID, na, rule)

	// Bob importing the same file, or the same activity, is not a duplicate,
	// and must not reveal anything about Alice's activity.
	for i, next := range []NewActivity{na, newActivity(time.Second, 3600, 1)} {
		bob := testUser(t, s)
		got := insert(t, s, bob.ID, next, rule)
		if got.Status != Inserted || got.Existing != nil {
			t.Errorf("case %d: status = %v, existing = %+v; want Inserted with no match", i, got.Status, got.Existing)
		}
	}
}
