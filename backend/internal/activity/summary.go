package activity

import (
	"math"
	"time"
)

// Point is a position in WGS 84.
type Point struct {
	Lat, Lon float64
}

// distanceM returns the great-circle distance between a and b in metres.
func distanceM(a, b Point) float64 {
	const earthRadiusM = 6371000.0
	lat1, lat2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dLat, dLon := lat2-lat1, (b.Lon-a.Lon)*math.Pi/180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Sqrt(h))
}

// ServiceSummary is what a service such as Garmin Connect says about an
// activity in its activity list, without its file.
type ServiceSummary struct {
	StartedAt time.Time
	Start     Point
	// ElapsedS is the time from start to finish, pauses included.
	ElapsedS  float64
	DistanceM float64
}

// Located is a stored activity with where its track starts.
type Located struct {
	Facts
	Start Point
}

// SummaryRule decides when an activity known only from a ServiceSummary is
// a possible duplicate of a stored one, so that its file needn't be
// downloaded to find out. A summary matches a stored activity when ALL of
// these hold:
//
//   - the stored activity starts at most StartEarlier before and at most
//     StartLater after the summary's start,
//   - their start positions are at most StartDistanceM apart,
//   - their elapsed times differ by at most ElapsedTolerance, and their
//     distances by at most DistanceTolerance, as a fraction of the larger.
//
// A stored activity without a duration never matches, and neither does a
// summary without an elapsed time or distance: the file decides those.
type SummaryRule struct {
	StartEarlier, StartLater time.Duration
	StartDistanceM           float64
	ElapsedTolerance         float64
	DistanceTolerance        float64
}

// Match returns the activity among candidates that s matches and that
// starts closest to it.
func (r SummaryRule) Match(s ServiceSummary, candidates []Located) (Located, bool) {
	var best Located
	found := false
	bestDiff := time.Duration(math.MaxInt64)
	for _, c := range candidates {
		if !r.matches(s, c) {
			continue
		}
		diff := c.StartedAt.Sub(s.StartedAt).Abs()
		if diff < bestDiff {
			best, bestDiff, found = c, diff, true
		}
	}
	return best, found
}

func (r SummaryRule) matches(s ServiceSummary, c Located) bool {
	if c.StartedAt == nil || c.DurationS == nil || s.ElapsedS <= 0 || s.DistanceM <= 0 {
		return false
	}
	diff := c.StartedAt.Sub(s.StartedAt)
	return diff >= -r.StartEarlier && diff <= r.StartLater &&
		distanceM(s.Start, c.Start) <= r.StartDistanceM &&
		relativeDiff(s.ElapsedS, float64(*c.DurationS)) <= r.ElapsedTolerance &&
		relativeDiff(s.DistanceM, c.DistanceM) <= r.DistanceTolerance
}
