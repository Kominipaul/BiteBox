package handlers

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bitebox/internal/auth"
	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
)

type staffRowView struct {
	models.User
	Initials    string
	StatusLabel string
	SubCaption  string
	RoleLabel   string
	IsSelf      bool
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// initialsFor derives a 2-letter avatar tag from a username: first letter
// of each underscore-separated segment (e.g. "nikos_kitchen" -> "NK"), or
// the first two letters if there's only one segment (e.g. "admin" -> "AD").
func initialsFor(username string) string {
	parts := strings.Split(username, "_")
	if len(parts) >= 2 && len(parts[0]) > 0 && len(parts[1]) > 0 {
		return strings.ToUpper(parts[0][:1] + parts[1][:1])
	}
	if len(username) >= 2 {
		return strings.ToUpper(username[:2])
	}
	return strings.ToUpper(username)
}

func computeStaffStatus(u models.User) string {
	if u.LastSeenAt == nil {
		return "Never logged in"
	}
	elapsed := time.Since(*u.LastSeenAt)
	switch {
	case elapsed < 5*time.Minute:
		return "Active now"
	case elapsed < time.Hour:
		return fmt.Sprintf("Active %dm ago", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("Active %dh ago", int(elapsed.Hours()))
	default:
		days := int(elapsed.Hours() / 24)
		if days < 1 {
			days = 1
		}
		return fmt.Sprintf("Offline · %dd ago", days)
	}
}

func pluralize(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s ago", unit)
	}
	return fmt.Sprintf("%d %ss ago", n, unit)
}

func relativeTime(t time.Time) string {
	elapsed := time.Since(t)
	switch {
	case elapsed < 24*time.Hour:
		return "today"
	case elapsed < 30*24*time.Hour:
		return pluralize(int(elapsed.Hours()/24), "day")
	case elapsed < 365*24*time.Hour:
		return pluralize(int(elapsed.Hours()/(24*30)), "month")
	default:
		return pluralize(int(elapsed.Hours()/(24*365)), "year")
	}
}

func buildStaffViews(users []models.User, currentUserID int) []staffRowView {
	views := make([]staffRowView, 0, len(users))
	for _, u := range users {
		v := staffRowView{
			User:        u,
			Initials:    initialsFor(u.Username),
			StatusLabel: computeStaffStatus(u),
			IsSelf:      u.ID == currentUserID,
		}
		if u.Role == models.RoleAdmin {
			v.RoleLabel = "Admin"
		} else {
			v.RoleLabel = "Worker"
		}
		switch {
		case v.IsSelf:
			v.SubCaption = "Added " + relativeTime(u.CreatedAt)
		case u.Role == models.RoleAdmin:
			v.SubCaption = "Admin"
		default:
			v.SubCaption = "Worker · " + capitalize(u.Department)
		}
		views = append(views, v)
	}
	return views
}

// AdminStaffList serves the #staff-list fragment for its initial hx-get load.
func AdminStaffList(w http.ResponseWriter, r *http.Request) {
	renderStaffList(w, r)
}

func renderStaffList(w http.ResponseWriter, r *http.Request) {
	users, err := db.GetAllUsers()
	if err != nil {
		http.Error(w, "Failed to load staff", http.StatusInternalServerError)
		return
	}
	currentUser, _ := UserFromContext(r)
	tmpl := template.Must(template.ParseFiles("templates/_staff_list.html"))
	tmpl.Execute(w, map[string]interface{}{"Staff": buildStaffViews(users, currentUser.ID)})
}

// AdminCreateStaff adds a new staff account. The form's single "role" field
// carries both role and department in one choice, matching the dashboard's
// one dropdown ("Admin" or "Worker — Bar/Kitchen/DJ/Staff"): the literal
// value "admin" means role=admin, anything else must be a valid department
// and implies role=worker.
func AdminCreateStaff(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	roleParam := r.FormValue("role")

	if username == "" || len(password) < 6 {
		http.Error(w, "Username required and password must be at least 6 characters", http.StatusBadRequest)
		return
	}

	var role, department string
	switch {
	case roleParam == models.RoleAdmin:
		role, department = models.RoleAdmin, models.DepartmentSuperworker
	case models.IsValidDepartment(roleParam):
		role, department = models.RoleWorker, roleParam
	default:
		http.Error(w, "Invalid role", http.StatusBadRequest)
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "Failed to create account", http.StatusInternalServerError)
		return
	}
	if _, err := db.CreateUser(username, hash, role, department); err != nil {
		if err == db.ErrDuplicateUsername {
			http.Error(w, "That username is already taken", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to create account", http.StatusInternalServerError)
		return
	}
	renderStaffList(w, r)
}

// AdminDeactivateStaff disables a staff account and immediately kills any
// session it currently has open, so access is cut off right away rather
// than just blocking future logins.
func AdminDeactivateStaff(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid staff id", http.StatusBadRequest)
		return
	}
	currentUser, _ := UserFromContext(r)
	if id == currentUser.ID {
		http.Error(w, "You can't deactivate your own account", http.StatusBadRequest)
		return
	}
	if err := db.SetUserActive(id, false); err != nil {
		http.Error(w, "Failed to deactivate account", http.StatusInternalServerError)
		return
	}
	db.DeleteAuthSessionsForUser(id)
	renderStaffList(w, r)
}

func AdminActivateStaff(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid staff id", http.StatusBadRequest)
		return
	}
	if err := db.SetUserActive(id, true); err != nil {
		http.Error(w, "Failed to activate account", http.StatusInternalServerError)
		return
	}
	renderStaffList(w, r)
}
