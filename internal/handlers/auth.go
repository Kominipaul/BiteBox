package handlers

import (
	"html/template"
	"net/http"

	"bitebox/internal/auth"
	"bitebox/internal/db"
)

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

	sessionID := auth.GenerateSessionID()
	if err := db.CreateAuthSession(sessionID, user.ID); err != nil {
		renderError("Something went wrong, please try again")
		return
	}

	auth.SetSessionCookie(w, sessionID)

	redirect := "/worker"
	if user.Role == "admin" {
		redirect = "/admin"
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func Logout(w http.ResponseWriter, r *http.Request) {
	if sessionID, ok := auth.SessionIDFromRequest(r); ok {
		db.DeleteAuthSession(sessionID)
	}
	auth.ClearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
