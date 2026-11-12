package activity

import (
	"math"
	"testing"
	"time"
)

func TestDistanceM(t *testing.T) {
	// 0.001° of latitude is about 111 m everywhere.
	if d := distanceM(Point{59.3, 18.0}, Point{59.301, 18.0}); math.Abs(d-111.2) > 0.5 {
		t.Errorf("distance = %.1f m, want about 111.2", d)
	}
	if d := distanceM(Point{59.3, 18.0}, Point{59.3, 18.0}); d != 0 {
		t.Errorf("distance to itself = %v", d)
	}
}

func TestSummaryRuleMatch(t *testing.T) {
	rule := SummaryRule{StartEarlier: 2 * time.Second, StartLater: time.Minute, StartDistanceM: 100,
		ElapsedTolerance: 0.03, DistanceTolerance: 0.10}
	start := time.Date(2019, 6, 5, 16, 30, 0, 0, time.UTC)
	home := Point{59.3, 18.0}
	// Garmin's summary of a 50-minute, 10 km run.
	summary := ServiceSummary{StartedAt: start, Start: home, ElapsedS: 3000, DistanceM: 10000}

	stored := func(id string, startOffset time.Duration, at Point, durationS int, distanceM float64) Located {
		s := start.Add(startOffset)
		return Located{Facts: Facts{ID: id, StartedAt: &s, DurationS: &durationS, DistanceM: distanceM}, Start: at}
	}
	noDuration := stored("a", 0, home, 0, 10000)
	noDuration.DurationS = nil
	noStart := stored("a", 0, home, 3000, 10000)
	noStart.StartedAt = nil

	tests := []struct {
		name    string
		summary ServiceSummary
		stored  Located
		want    bool
	}{
		{"the same activity", summary, stored("a", 0, home, 3000, 10000), true},
		// The file's first GPS point comes after the timer start while the
		// GPS finds its fix.
		{"GPS fix 45 s after the start", summary, stored("a", 45*time.Second, home, 2955, 9950), true},
		{"GPS fix 61 s after the start", summary, stored("a", 61*time.Second, home, 2939, 9950), false},
		{"starts 2 s earlier", summary, stored("a", -2*time.Second, home, 3002, 10000), true},
		{"starts 3 s earlier", summary, stored("a", -3*time.Second, home, 3003, 10000), false},
		{"starts 90 m away", summary, stored("a", 0, Point{59.3008, 18.0}, 3000, 10000), true},
		{"starts 150 m away", summary, stored("a", 0, Point{59.30135, 18.0}, 3000, 10000), false},
		{"elapsed time 3% longer", summary, stored("a", 0, home, 3090, 10000), true},
		{"elapsed time 4% longer", summary, stored("a", 0, home, 3125, 10000), false},
		// The database measures raw GPS points, which zig-zag; Garmin smooths.
		{"distance 9% longer", summary, stored("a", 0, home, 3000, 10990), true},
		{"distance 12% longer", summary, stored("a", 0, home, 3000, 11360), false},
		{"stored activity without a duration", summary, noDuration, false},
		{"stored activity without a start time", summary, noStart, false},
		{"summary without an elapsed time", ServiceSummary{StartedAt: start, Start: home, DistanceM: 10000}, stored("a", 0, home, 0, 10000), false},
		{"summary without a distance", ServiceSummary{StartedAt: start, Start: home, ElapsedS: 3000}, stored("a", 0, home, 3000, 0), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, got := rule.Match(tt.summary, []Located{tt.stored}); got != tt.want {
				t.Errorf("Match = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("closest start of several", func(t *testing.T) {
		candidates := []Located{
			stored("earlier", -time.Second, home, 3000, 10000),
			stored("closest", 0, home, 3000, 10000),
			stored("later", 30*time.Second, home, 2970, 10000),
			stored("elsewhere", 0, Point{57.7, 11.97}, 3000, 10000),
		}
		if m, ok := rule.Match(summary, candidates); !ok || m.ID != "closest" {
			t.Errorf("Match = %q, %v; want closest", m.ID, ok)
		}
	})
	t.Run("no candidates", func(t *testing.T) {
		if _, ok := rule.Match(summary, nil); ok {
			t.Error("matched nothing")
		}
	})
}
