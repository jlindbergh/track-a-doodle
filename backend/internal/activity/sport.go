package activity

import (
	"strings"
	"unicode"
)

// Sport ids. An imported activity gets one of these when its file's activity
// type is recognized. Other types are kept as written, in snake_case.
const (
	SportCrossCountrySkiing    = "cross_country_skiing"
	SportCycling               = "cycling"
	SportDownhillSkiing        = "downhill_skiing"
	SportHiking                = "hiking"
	SportRowing                = "rowing"
	SportRunning               = "running"
	SportSkating               = "skating"
	SportStandUpPaddleboarding = "stand_up_paddleboarding"
	SportSwimming              = "swimming"
	SportWalking               = "walking"

	// SportUnknown is used when a file has no activity type.
	SportUnknown = "unknown"
)

// Sports returns the recognized sport ids, sorted. The frontend gets them
// from GET /api/config and picks how to show the ones it knows about.
func Sports() []string {
	return []string{
		SportCrossCountrySkiing, SportCycling, SportDownhillSkiing, SportHiking, SportRowing,
		SportRunning, SportSkating, SportStandUpPaddleboarding, SportSwimming, SportWalking,
	}
}

// sportAliases maps the activity types written by common exporters to the
// sport ids in package activity. Strava's older exports use numeric codes;
// Garmin's types sometimes end in "_ws" (winter sports).
var sportAliases = map[string]string{
	"1":               SportCycling,
	"9":               SportRunning,
	"10":              SportWalking,
	"4":               SportHiking,
	"run":             SportRunning,
	"trail_running":   SportRunning,
	"ride":            SportCycling,
	"biking":          SportCycling,
	"road_biking":     SportCycling,
	"mountain_biking": SportCycling,
	"walk":            SportWalking,
	"hike":            SportHiking,
	"swim":            SportSwimming,

	"nordic_ski":              SportCrossCountrySkiing,
	"nordic_skiing":           SportCrossCountrySkiing,
	"cross_country_skiing_ws": SportCrossCountrySkiing,
	"classic_skiing":          SportCrossCountrySkiing,
	// Cross-country skiing in the skate technique, not skating.
	"skate_skiing":    SportCrossCountrySkiing,
	"skate_skiing_ws": SportCrossCountrySkiing,

	"alpine_ski":                    SportDownhillSkiing,
	"alpine_skiing":                 SportDownhillSkiing,
	"resort_skiing":                 SportDownhillSkiing,
	"resort_skiing_snowboarding":    SportDownhillSkiing,
	"resort_skiing_snowboarding_ws": SportDownhillSkiing,

	"skating_ws":     SportSkating,
	"ice_skate":      SportSkating,
	"ice_skating":    SportSkating,
	"inline_skate":   SportSkating,
	"inline_skating": SportSkating,

	"open_water_swimming":        SportSwimming,
	"rowing_v2":                  SportRowing,
	"stand_up_paddling":          SportStandUpPaddleboarding,
	"stand_up_paddleboarding_v2": SportStandUpPaddleboarding,
	"sup":                        SportStandUpPaddleboarding,
}

// NormalizeSport lowercases a sport type, turning CamelCase ("VirtualRide",
// as Strava writes it) and spaces into snake_case, then applies sportAliases.
func NormalizeSport(s string) string {
	var b strings.Builder
	prevLower := false
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r == ' ' || r == '-':
			r = '_'
		case unicode.IsUpper(r) && prevLower:
			b.WriteByte('_')
		}
		prevLower = unicode.IsLower(r)
		b.WriteRune(unicode.ToLower(r))
	}
	s = b.String()
	if s == "" {
		return SportUnknown
	}
	if alias, ok := sportAliases[s]; ok {
		return alias
	}
	return s
}

// sportPhrases maps phrases in activity names to sport ids. They are matched
// before sportWords, so "Skate Skiing" is cross-country skiing rather than
// skating. "Ski" on its own could be either kind of skiing, so it isn't a word.
var sportPhrases = map[string]string{
	"nordic ski":              SportCrossCountrySkiing,
	"nordic skiing":           SportCrossCountrySkiing,
	"cross country ski":       SportCrossCountrySkiing,
	"cross country skiing":    SportCrossCountrySkiing,
	"classic ski":             SportCrossCountrySkiing,
	"classic skiing":          SportCrossCountrySkiing,
	"skate ski":               SportCrossCountrySkiing,
	"skate skiing":            SportCrossCountrySkiing,
	"alpine ski":              SportDownhillSkiing,
	"alpine skiing":           SportDownhillSkiing,
	"downhill ski":            SportDownhillSkiing,
	"downhill skiing":         SportDownhillSkiing,
	"resort skiing":           SportDownhillSkiing,
	"ice skate":               SportSkating,
	"ice skating":             SportSkating,
	"inline skate":            SportSkating,
	"inline skating":          SportSkating,
	"stand up paddle":         SportStandUpPaddleboarding,
	"stand up paddling":       SportStandUpPaddleboarding,
	"stand up paddleboarding": SportStandUpPaddleboarding,
	"paddle board":            SportStandUpPaddleboarding,
	"paddle boarding":         SportStandUpPaddleboarding,
}

// maxPhraseWords is the number of words in the longest of sportPhrases.
const maxPhraseWords = 3

// sportWords maps words in activity names, as Strava and Garmin write them
// ("Morning Run", "Stockholm Cycling"), to sport ids.
var sportWords = map[string]string{
	"run":      SportRunning,
	"running":  SportRunning,
	"jog":      SportRunning,
	"jogging":  SportRunning,
	"ride":     SportCycling,
	"cycling":  SportCycling,
	"bike":     SportCycling,
	"biking":   SportCycling,
	"walk":     SportWalking,
	"walking":  SportWalking,
	"hike":     SportHiking,
	"hiking":   SportHiking,
	"swim":     SportSwimming,
	"swimming": SportSwimming,
	"skate":    SportSkating,
	"skating":  SportSkating,
	"rowing":   SportRowing,
	"sup":      SportStandUpPaddleboarding,
	// Not "paddle" on its own: it could as well be a kayak or a canoe.
	"paddleboard":    SportStandUpPaddleboarding,
	"paddleboarding": SportStandUpPaddleboarding,
}

// GuessSport returns the sport named in the first of names that names one,
// e.g. a file's track names and then its filename. It is used for files
// without an activity type.
func GuessSport(names ...string) string {
	for _, name := range names {
		if sport := sportFromName(name); sport != SportUnknown {
			return sport
		}
	}
	return SportUnknown
}

// sportFromName returns the sport named by a phrase or word in name, or
// SportUnknown if none does, or if they name different sports.
func sportFromName(name string) string {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) })
	found := SportUnknown
	for i := 0; i < len(words); {
		sport, n := sportAt(words[i:])
		i += n
		if sport == "" {
			continue
		}
		if found != SportUnknown && sport != found {
			return SportUnknown
		}
		found = sport
	}
	return found
}

// sportAt returns the sport named by the longest phrase, or else the word, at
// the start of words, and how many words that took. It returns "" and 1 if
// there is neither.
func sportAt(words []string) (string, int) {
	for n := min(maxPhraseWords, len(words)); n > 1; n-- {
		if sport, ok := sportPhrases[strings.Join(words[:n], " ")]; ok {
			return sport, n
		}
	}
	return sportWords[words[0]], 1
}
