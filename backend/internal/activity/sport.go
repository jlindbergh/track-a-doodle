package activity

// Sport ids. An imported activity gets one of these when its file's activity
// type is recognized. Other types are kept as written, in snake_case.
const (
	SportCrossCountrySkiing = "cross_country_skiing"
	SportCycling            = "cycling"
	SportDownhillSkiing     = "downhill_skiing"
	SportHiking             = "hiking"
	SportRunning            = "running"
	SportSkating            = "skating"
	SportSwimming           = "swimming"
	SportWalking            = "walking"

	// SportUnknown is used when a file has no activity type.
	SportUnknown = "unknown"
)

// Sports returns the recognized sport ids, sorted. The frontend gets them
// from GET /api/config and picks how to show the ones it knows about.
func Sports() []string {
	return []string{
		SportCrossCountrySkiing, SportCycling, SportDownhillSkiing, SportHiking,
		SportRunning, SportSkating, SportSwimming, SportWalking,
	}
}
