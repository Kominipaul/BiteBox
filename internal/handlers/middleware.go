package handlers

import (
	"context"
	"net/http"
	"time"

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
				auth.ClearSessionCookie(w, r)
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			if !allowed[user.Role] {
				http.Error(w, "403 Forbidden: insufficient role", http.StatusForbidden)
				return
			}

			// A seeded default account (see models.User.MustChangePassword)
			// can reach nothing else — including a websocket upgrade, which
			// is exactly why this has to run before db.TouchLastSeen/
			// next.ServeHTTP below, not as a per-handler check — until it's
			// changed. /change-password itself is registered under this
			// same middleware (RequireRole(admin, worker) — every role that
			// can ever have this flag), so it has to be exempted here or
			// nothing could ever reach it to clear the flag.
			if user.MustChangePassword && r.URL.Path != "/change-password" {
				http.Redirect(w, r, "/change-password", http.StatusSeeOther)
				return
			}
			// Same for an admin without two-step login: nothing else until
			// it's set up (models.User.NeedsTwoFactor).
			if user.NeedsTwoFactor() && !user.TwoFactor && !twoFactorExempt(r.URL.Path) {
				http.Redirect(w, r, "/account/2fa", http.StatusSeeOther)
				return
			}

			// A write per request is wasted work; "active now" only needs
			// minute precision.
			if user.LastSeenAt == nil || time.Since(*user.LastSeenAt) > time.Minute {
				db.TouchLastSeen(user.ID)
			}

			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireDepartment restricts a worker route to specific departments (e.g.
// only "dj" can reach the DJ terminal routes). Must run inside a RequireRole
// group so a user is already in context. Admins bypass this check entirely —
// department is a worker-only concept, admins can reach everything.
func RequireDepartment(departments ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(departments))
	for _, d := range departments {
		allowed[d] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r)
			if !ok {
				http.Error(w, "403 Forbidden", http.StatusForbidden)
				return
			}
			if user.Role == models.RoleAdmin || allowed[user.Department] {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "403 Forbidden: not available to your department", http.StatusForbidden)
		})
	}
}

func UserFromContext(r *http.Request) (models.User, bool) {
	user, ok := r.Context().Value(userContextKey).(models.User)
	return user, ok
}

// RequirePage limits a back-office route to accounts that may open that page
// (models.User.Can; admins always can). Must run inside RequireRole. A plain
// page visit someone can't open goes back to their own start page instead of
// a bare 403.
func RequirePage(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r)
			if ok && user.Can(key) {
				next.ServeHTTP(w, r)
				return
			}
			if ok && r.Method == http.MethodGet && r.Header.Get("HX-Request") == "" && r.Header.Get("Upgrade") == "" {
				if start := startPage(user); start != r.URL.Path {
					http.Redirect(w, r, start, http.StatusSeeOther)
					return
				}
			}
			http.Error(w, "403 Forbidden: you don't have access to this page", http.StatusForbidden)
		})
	}
}

// startPage is where an account goes when it can't open a page: admins to the
// back office, everyone else to their work screen (the staff app), which is
// home for a worker whatever else they may open.
func startPage(u models.User) string {
	if u.Role == models.RoleAdmin {
		return "/admin"
	}
	return "/worker"
}

// workModePages are the pages that belong to work mode:
// Floor and Bookings live inside the staff app (work mode), so having only
// those doesn't mean having an admin side.
var workModePages = map[string]bool{"floor": true, "bookings": true, "live": true}

// hasAdminMode: a worker account with at least one page beyond Floor and
// Bookings (a manager: Dashboard, Menu, Orders, Logs…) has an admin side to
// switch to.
func hasAdminMode(u models.User) bool {
	if u.Role == models.RoleAdmin {
		return false // admins live in the back office; no work side to switch to
	}
	for _, p := range models.BackOfficePages {
		if !workModePages[p.Key] && u.Can(p.Key) {
			return true
		}
	}
	return false
}

// adminStart is the first page of the back office an account may open: the
// dashboard if it can, else the first of the rest.
func adminStart(u models.User) string {
	for _, p := range []struct{ key, url string }{
		{"dashboard", "/admin"}, {"orders", "/admin/orders"}, {"menu", "/admin/menu"}, {"logs", "/admin/logs"},
		{"team", "/admin/team"}, {"map", "/admin/map"}, {"bookings", "/admin/bookings"}, {"floor", "/admin/floor"},
	} {
		if u.Can(p.key) {
			return p.url
		}
	}
	return "/worker"
}
