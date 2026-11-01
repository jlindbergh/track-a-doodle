package api

import "testing"

func TestIsUUID(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"7b30d70a-6373-4b28-b4fa-b09dab57051c", true},
		{"7B30D70A-6373-4B28-B4FA-B09DAB57051C", true},
		{"", false},
		{"not-a-uuid", false},
		{"7b30d70a63734b28b4fab09dab57051c", false},
		{"7b30d70a-6373-4b28-b4fa-b09dab57051", false},
		{"7b30d70a-6373-4b28-b4fa-b09dab57051c0", false},
		{"7b30d70a-6373-4b28-b4fa-b09dab57051g", false},
		{"7b30d70a-6373-4b28-b4fa-b09dab57051c\n", false},
	}
	for _, tt := range tests {
		if got := isUUID(tt.in); got != tt.want {
			t.Errorf("isUUID(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
