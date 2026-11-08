package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

func TestSessionMiddleware(t *testing.T) {
	good, goodHash, _ := NewToken()
	lookup := func(_ context.Context, h []byte) (store.User, error) {
		switch {
		case bytes.Equal(h, goodHash):
			return store.User{ID: "u1"}, nil
		case bytes.Equal(h, HashToken("broken")):
			return store.User{}, errors.New("database down")
		}
		return store.User{}, store.ErrNotFound
	}
	var errored bool
	h := SessionMiddleware(lookup, func(w http.ResponseWriter, _ *http.Request, _ error) {
		errored = true
		w.WriteHeader(http.StatusInternalServerError)
	}, Require(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		w.Write([]byte(u.ID))
	}))

	for _, tt := range []struct {
		name   string
		cookie string
		want   int
		body   string
	}{
		{"no cookie", "", http.StatusUnauthorized, ""},
		{"unknown session", "nope", http.StatusUnauthorized, ""},
		{"valid session", good, http.StatusOK, "u1"},
		{"lookup error", "broken", http.StatusInternalServerError, ""},
	} {
		errored = false
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if tt.cookie != "" {
			req.AddCookie(&http.Cookie{Name: SessionCookie, Value: tt.cookie})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tt.want || (tt.body != "" && rec.Body.String() != tt.body) {
			t.Errorf("%s: %d %q, want %d %q", tt.name, rec.Code, rec.Body.String(), tt.want, tt.body)
		}
		if errored != (tt.want == http.StatusInternalServerError) {
			t.Errorf("%s: onError called = %v", tt.name, errored)
		}
	}
}

func TestSessionCookieFlags(t *testing.T) {
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, "tok", time.Now().Add(time.Hour), true)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("%d cookies", len(cookies))
	}
	c := cookies[0]
	if c.Name != SessionCookie || c.Value != "tok" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge <= 0 {
		t.Errorf("cookie = %+v", c)
	}

	rec = httptest.NewRecorder()
	ClearSessionCookie(rec, false)
	if c := rec.Result().Cookies()[0]; c.MaxAge >= 0 || c.Value != "" {
		t.Errorf("cleared cookie = %+v", c)
	}
}

func TestNewToken(t *testing.T) {
	a, ha, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := NewToken()
	if a == b || len(a) != 43 || !bytes.Equal(ha, HashToken(a)) {
		t.Errorf("tokens %q %q", a, b)
	}
}
