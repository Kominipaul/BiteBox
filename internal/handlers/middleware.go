package handlers

import (
	"context"
	"net/http"

	"bitebox/internal/auth"
	"bitebox/internal/db"
	"bitebox/internal/models"
)

type contextKey string

const userContextKey contextKey = "user"

// RequireRole redirects unauthenticated requests to /login and rejects
// authenticated requests whose role isn't in the allowed set with 403.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sessionID, ok := auth.SessionIDFromRequest(r)
			if !ok {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			user, err := db.GetUserBySessionID(sessionID)
			if err != nil {
				auth.ClearSessionCookie(w)
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			if !allowed[user.Role] {
				http.Error(w, "403 Forbidden: insufficient role", http.StatusForbidden)
				return
			}

			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func UserFromContext(r *http.Request) (models.User, bool) {
	user, ok := r.Context().Value(userContextKey).(models.User)
	return user, ok
}
