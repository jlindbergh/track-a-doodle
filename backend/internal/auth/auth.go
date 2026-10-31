// Package auth resolves the user behind each request.
//
// Only a development mode exists so far, where every request belongs to one
// configured user. OIDC login (Zitadel/Strava) will replace it.
package auth

import (
	"context"
	"net/http"

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
