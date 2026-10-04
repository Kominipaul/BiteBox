package handlers

import (
	"html/template"
	"log"
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

// renderLogin draws /login in the venue's look (brand.Theme login fields).
func renderLogin(w http.ResponseWriter, errMsg string) {
	settings, _ := db.GetSettings()
	t, _, _ := currentTheme()
	logo := t.LoginLogoURL
	if logo == "" {
		logo = t.LogoURL
	}
	tmpl := template.Must(template.ParseFiles("templates/login.html"))
	tmpl.Execute(w, map[string]interface{}{"Error": errMsg, "T": t, "Logo": logo, "VenueName": settings.VenueName})
}

func LoginPage(w http.ResponseWriter, r *http.Request) { renderLogin(w, "") }

var dummyHash, _ = auth.HashPassword("not-a-real-account")

func LoginSubmit(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	renderError := func(msg string) { renderLogin(w, msg) }

	user, err := db.GetUserByUsername(username)
	if err != nil {
		// Same bcrypt cost as a real miss, so timing doesn't reveal which usernames exist.
		user.PasswordHash = dummyHash
	}
	if !auth.CheckPassword(user.PasswordHash, password) || err != nil {
		renderError("Invalid username or password")
		return
	}
	if !user.IsActive {
		renderError("This account has been deactivated")
		return
	}

	if user.TwoFactor {
		startPendingLogin(w, r, user.ID)
		renderLoginCode(w, "")
		return
	}
	finishLogin(w, r, user)
}

// ChangePasswordPage serves both the forced flow (RequireRole redirects
// here whenever the logged-in account has MustChangePassword set — see its
// own doc comment) and voluntary use (a "Change password" link in both
// dashboards' topbars) — same form either way; Forced only changes the
// page's own copy explaining *why* it's here.
func ChangePasswordPage(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r)
	settings, _ := db.GetSettings()
	tmpl := template.Must(template.ParseFiles("templates/change_password.html"))
	tmpl.Execute(w, map[string]interface{}{"Forced": user.MustChangePassword, "VenueName": settings.VenueName})
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
	if msg := auth.CheckNewPassword(user.Username, newPassword); msg != "" {
		renderError(msg)
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
	// Sign out every other device (a stolen session dies with the old
	// password); this one gets a fresh session.
	if err := keepOnlyThisSession(w, r, user.ID); err != nil {
		log.Printf("change password for %q: signing other devices out: %v", user.Username, err)
		renderError("Your password was changed, but other devices couldn't be signed out. Please try again.")
		return
	}
	db.LogAudit(user.Username, "change_password", user.Username, "")

	http.Redirect(w, r, homePathFor(user.Role), http.StatusSeeOther)
}

func Logout(w http.ResponseWriter, r *http.Request) {
	if sessionID, ok := auth.SessionIDFromRequest(r); ok {
		if err := db.DeleteAuthSession(sessionID); err != nil {
			log.Printf("logout: %v", err) // it still expires within 24h
		}
	}
	auth.ClearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
