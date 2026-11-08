package ratelimit

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestKeyed(t *testing.T) {
	k := NewKeyed(1, time.Minute, 2)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	k.now = func() time.Time { return now }

	if !k.Allow("a") || !k.Allow("a") {
		t.Fatal("burst not allowed")
	}
	if k.Allow("a") {
		t.Fatal("third event allowed within the burst window")
	}
	if !k.Allow("b") {
		t.Fatal("keys are not independent")
	}
	now = now.Add(time.Minute)
	if !k.Allow("a") {
		t.Fatal("not refilled after a period")
	}
}

func TestKeyedPrunes(t *testing.T) {
	k := NewKeyed(1, time.Minute, 1)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	k.now = func() time.Time { return now }
	k.Allow("a")
	now = now.Add(time.Hour)
	k.Allow("b")
	if _, ok := k.entries["a"]; ok {
		t.Error("idle key was not pruned")
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name  string
		xff   []string
		trust bool
		want  string
	}{
		{"remote addr", nil, false, "192.0.2.1"},
		{"ignores header unless trusted", []string{"203.0.113.9"}, false, "192.0.2.1"},
		{"trusted header", []string{"203.0.113.9"}, true, "203.0.113.9"},
		{"rightmost entry", []string{"10.0.0.1, 203.0.113.9"}, true, "203.0.113.9"},
		{"last header line", []string{"10.0.0.1", "203.0.113.9"}, true, "203.0.113.9"},
		{"garbage falls back", []string{"nonsense"}, true, "192.0.2.1"},
	}
	for _, tt := range tests {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "192.0.2.1:1234"
		for _, v := range tt.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := ClientIP(r, tt.trust); got != tt.want {
			t.Errorf("%s: got %s, want %s", tt.name, got, tt.want)
		}
	}
}
