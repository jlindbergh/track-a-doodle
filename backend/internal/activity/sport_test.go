package activity

import "testing"

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
		if got := NormalizeSport(in); got != want {
			t.Errorf("NormalizeSport(%q) = %q, want %q", in, got, want)
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
