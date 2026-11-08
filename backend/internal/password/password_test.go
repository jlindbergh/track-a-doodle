package password

import (
	"strings"
	"testing"
)

func TestHashAndCheck(t *testing.T) {
	h, err := Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("unexpected hash format: %s", h)
	}
	for pw, want := range map[string]bool{
		"correct horse battery":  true,
		"correct horse battery ": false,
		"":                       false,
	} {
		got, err := Check(pw, h)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("Check(%q) = %v, want %v", pw, got, want)
		}
	}
}

func TestHashIsSalted(t *testing.T) {
	a, _ := Hash("same password")
	b, _ := Hash("same password")
	if a == b {
		t.Error("two hashes of the same password are equal")
	}
}

func TestCheckMalformed(t *testing.T) {
	for _, h := range []string{
		"",
		"plain",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$a2V5",
		"$argon2id$v=18$m=19456,t=2,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=0,t=2,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=19456,t=2,p=1$$a2V5",
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$!!",
	} {
		if ok, err := Check("pw", h); err == nil || ok {
			t.Errorf("Check with %q = %v, %v; want an error", h, ok, err)
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		pw   string
		want error
	}{
		{strings.Repeat("a", MinLength-1), ErrTooShort},
		{strings.Repeat("a", MinLength), nil},
		// Length is counted in characters, not bytes.
		{strings.Repeat("å", MinLength), nil},
		{strings.Repeat("a", MaxLength), nil},
		{strings.Repeat("a", MaxLength+1), ErrTooLong},
	}
	for _, tt := range tests {
		if got := Validate(tt.pw); got != tt.want {
			t.Errorf("Validate(%d chars) = %v, want %v", len([]rune(tt.pw)), got, tt.want)
		}
	}
}

func TestCheckDummyDoesNotPanic(t *testing.T) {
	CheckDummy("anything")
}
