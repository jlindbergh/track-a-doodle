// Package auth resolves the user behind each request.
//
// In password mode the user comes from a session cookie set at sign-in. In
// development mode every request belongs to one configured user.
package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jlindbergh/track-a-doodle/backend/internal/store"
)

type contextKey struct{}

// WithUser returns a copy of ctx carrying u.
func WithUser(ctx context.Context, u store.User) context.Context {
	return context.WithValue(ctx, contextKey{}, u)
}

// UserFrom returns the authenticated user stored in ctx.
func UserFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(contextKey{}).(store.User)
	return u, ok
}

// DevMiddleware treats every request as coming from u.
// It must never be enabled outside local development.
func DevMiddleware(u store.User, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
	})
}

// Require wraps h so that it only runs for authenticated requests.
func Require(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// SessionCookie is the name of the cookie holding the session token.
const SessionCookie = "tad_session"

// SetSessionCookie sets the session cookie. It is HttpOnly, so scripts
// can't read it, and SameSite=Lax, so other sites can't send it along with
// their requests. Secure must be true unless served over plain-HTTP localhost.
func SetSessionCookie(w http.ResponseWriter, token string, expires time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie tells the browser to drop the session cookie.
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// SessionToken returns the session token sent with r, or "".
func SessionToken(r *http.Request) string {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// SessionLookup returns the user of a live session given its token hash,
// or store.ErrNotFound.
type SessionLookup func(ctx context.Context, tokenHash []byte) (store.User, error)

// SessionMiddleware attaches the user of the request's session, if any. A
// missing, unknown or expired session leaves the request unauthenticated,
// so Require answers 401. Other lookup errors go to onError.
func SessionMiddleware(lookup SessionLookup, onError func(http.ResponseWriter, *http.Request, error), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := SessionToken(r)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, err := lookup(r.Context(), HashToken(token))
		switch {
		case errors.Is(err, store.ErrNotFound):
			next.ServeHTTP(w, r)
		case err != nil:
			onError(w, r, err)
		default:
			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
		}
	})
}
