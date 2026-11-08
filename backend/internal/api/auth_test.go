package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jlindbergh/track-a-doodle/backend/internal/account"
	"github.com/jlindbergh/track-a-doodle/backend/internal/auth"
	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

// fakeAccounts returns err from every call, or a session for "tok".
type fakeAccounts struct {
	err       error
	loggedOut string
}

func (f *fakeAccounts) Register(context.Context, string, string) error { return f.err }

func (f *fakeAccounts) Verify(context.Context, string, string) (account.Session, error) {
	return f.session()
}

func (f *fakeAccounts) Login(context.Context, string, string) (account.Session, error) {
	return f.session()
}

func (f *fakeAccounts) Logout(_ context.Context, token string) error {
	f.loggedOut = token
	return f.err
}

func (f *fakeAccounts) session() (account.Session, error) {
	if f.err != nil {
		return account.Session{}, f.err
	}
	return account.Session{Token: "tok", ExpiresAt: time.Now().Add(time.Hour), User: store.User{ID: "u1", Email: "a@example.com"}}, nil
}

func accountsServer(f *fakeAccounts) http.Handler {
	srv := New(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.EnableAccounts(AccountsConfig{Accounts: f, CookieSecure: true})
	return srv.Routes()
}

func post(h http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLoginSetsSessionCookie(t *testing.T) {
	for path, body := range map[string]string{
		"/api/auth/login":  `{"email":"a@example.com","password":"x"}`,
		"/api/auth/verify": `{"token":"t","password":"x"}`,
	} {
		rec := post(accountsServer(&fakeAccounts{}), path, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", path, rec.Code, rec.Body)
		}
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != auth.SessionCookie || cookies[0].Value != "tok" || !cookies[0].Secure {
			t.Errorf("%s: cookies = %+v", path, cookies)
		}
		var u store.User
		json.NewDecoder(rec.Body).Decode(&u)
		if u.ID != "u1" {
			t.Errorf("%s: user = %+v", path, u)
		}
	}
}

func TestAccountErrorStatuses(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{account.InputError{Err: account.ErrInvalidEmail}, http.StatusBadRequest},
		{account.InputError{Err: account.ErrInvalidCredentials}, http.StatusUnauthorized},
		{account.InputError{Err: account.ErrWrongPassword}, http.StatusUnauthorized},
		{account.InputError{Err: account.ErrNotVerified}, http.StatusForbidden},
		{account.InputError{Err: account.ErrInvalidToken}, http.StatusBadRequest},
		{account.ErrMailBusy, http.StatusServiceUnavailable},
		{fmt.Errorf("%w: dial tcp: refused", account.ErrSendFailed), http.StatusBadGateway},
		{fmt.Errorf("boom"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		rec := post(accountsServer(&fakeAccounts{err: tt.err}), "/api/auth/login", `{"email":"a@example.com","password":"x"}`)
		if rec.Code != tt.want {
			t.Errorf("%v: status %d, want %d", tt.err, rec.Code, tt.want)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("%v: a cookie was set", tt.err)
		}
		if strings.Contains(rec.Body.String(), "refused") {
			t.Errorf("%v: internal detail leaked: %s", tt.err, rec.Body)
		}
	}
}

func TestRegisterAccepted(t *testing.T) {
	rec := post(accountsServer(&fakeAccounts{}), "/api/auth/register", `{"email":"a@example.com","password":"x"}`)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "check_email") {
		t.Errorf("got %d %s", rec.Code, rec.Body)
	}
}

func TestAuthRejectsBadBodies(t *testing.T) {
	h := accountsServer(&fakeAccounts{})
	for _, body := range []string{"", "not json", `{"email":"a","password":"b","admin":true}`, `{"email":"` + strings.Repeat("a", maxAuthBody) + `"}`} {
		if rec := post(h, "/api/auth/login", body); rec.Code != http.StatusBadRequest {
			t.Errorf("body %.30q: status %d, want 400", body, rec.Code)
		}
	}
}

func TestRegisterIsRateLimited(t *testing.T) {
	h := accountsServer(&fakeAccounts{})
	var last int
	for range 6 {
		last = post(h, "/api/auth/register", `{"email":"a@example.com","password":"x"}`).Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("sixth sign-up from one IP: %d, want 429", last)
	}
}

func TestLogoutClearsCookie(t *testing.T) {
	f := &fakeAccounts{}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "tok"})
	rec := httptest.NewRecorder()
	accountsServer(f).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || f.loggedOut != "tok" {
		t.Fatalf("status %d, logged out %q", rec.Code, f.loggedOut)
	}
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
		t.Errorf("cookie not cleared: %+v", c)
	}
}

func TestAuthRoutesOnlyInPasswordMode(t *testing.T) {
	srv := New(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if rec := post(srv.Routes(), "/api/auth/login", `{}`); rec.Code != http.StatusNotFound {
		t.Errorf("dev mode login: %d, want 404", rec.Code)
	}
}

func TestCrossOriginProtection(t *testing.T) {
	h := CrossOriginProtection(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tt := range []struct {
		name    string
		method  string
		headers map[string]string
		want    int
	}{
		{"same-origin fetch", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusNoContent},
		{"cross-site fetch", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"foreign Origin", http.MethodDelete, map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"cross-site GET", http.MethodGet, map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusNoContent},
		{"non-browser client", http.MethodPost, nil, http.StatusNoContent},
	} {
		req := httptest.NewRequest(tt.method, "http://localhost:5173/api/activities/import", nil)
		for k, v := range tt.headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Errorf("%s: %d, want %d", tt.name, rec.Code, tt.want)
		}
	}
}
