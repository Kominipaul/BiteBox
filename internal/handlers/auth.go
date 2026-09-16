package handlers

import (
	"html/template"
	"net/http"

	"bitebox/internal/auth"
	"bitebox/internal/db"
)

// homePathFor is where a logged-in user lands after auth succeeds — login,
// or finishing a forced/self-service password change (ChangePasswordSubmit
// uses this too, rather than duplicating the same role check).
func homePathFor(role string) string {
	if role == "admin" {
		return "/admin"
	}
	return "/worker"
}

func LoginPage(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("templates/login.html"))
	tmpl.Execute(w, nil)
}

func LoginSubmit(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	renderError := func(msg string) {
		tmpl := template.Must(template.ParseFiles("templates/login.html"))
		tmpl.Execute(w, map[string]interface{}{"Error": msg})
	}

	user, err := db.GetUserByUsername(username)
	if err != nil || !auth.CheckPassword(user.PasswordHash, password) {
		renderError("Invalid username or password")
		return
	}
	if !user.IsActive {
		renderError("This account has been deactivated")
		return
	}

	sessionID := auth.GenerateSessionID()
	if err := db.CreateAuthSession(sessionID, user.ID); err != nil {
		renderError("Something went wrong, please try again")
		return
	}

	auth.SetSessionCookie(w, r, sessionID)

	// RequireRole redirects here on the very next request anyway if this
	// account still needs to change its password — going there directly
	// just skips that one extra round trip.
	if user.MustChangePassword {
		http.Redirect(w, r, "/change-password", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, homePathFor(user.Role), http.StatusSeeOther)
}

// ChangePasswordPage serves both the forced flow (RequireRole redirects
// here whenever the logged-in account has MustChangePassword set — see its
// own doc comment) and voluntary use (a "Change password" link in both
// dashboards' topbars) — same form either way; Forced only changes the
// page's own copy explaining *why* it's here.
func ChangePasswordPage(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r)
	tmpl := template.Must(template.ParseFiles("templates/change_password.html"))
	tmpl.Execute(w, map[string]interface{}{"Forced": user.MustChangePassword})
}

func ChangePasswordSubmit(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	current := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")
	confirm := r.FormValue("confirm_password")

	renderError := func(msg string) {
		tmpl := template.Must(template.ParseFiles("templates/change_password.html"))
		tmpl.Execute(w, map[string]interface{}{"Forced": user.MustChangePassword, "Error": msg})
	}

	if !auth.CheckPassword(user.PasswordHash, current) {
		renderError("Current password is incorrect")
		return
	}
	if len(newPassword) < 6 {
		renderError("New password must be at least 6 characters")
		return
	}
	if newPassword != confirm {
		renderError("New password and confirmation don't match")
		return
	}
	if newPassword == current {
		renderError("Choose a password different from your current one")
		return
	}

	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		renderError("Something went wrong, please try again")
		return
	}
	if err := db.SetUserPassword(user.ID, hash); err != nil {
		renderError("Something went wrong, please try again")
		return
	}

	http.Redirect(w, r, homePathFor(user.Role), http.StatusSeeOther)
}

func Logout(w http.ResponseWriter, r *http.Request) {
	if sessionID, ok := auth.SessionIDFromRequest(r); ok {
		db.DeleteAuthSession(sessionID)
	}
	auth.ClearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
