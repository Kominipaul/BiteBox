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

// actorUsername returns the acting admin's own username for audit logging,
// falling back to "unknown" rather than panicking if somehow called outside
// RequireRole (which is what actually populates the user in context).
func actorUsername(r *http.Request) string {
	if u, ok := UserFromContext(r); ok {
		return u.Username
	}
	return "unknown"
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

	if username == "" || len(username) > 50 || len(password) < 6 {
		http.Error(w, "Username required (max 50 characters) and password must be at least 6 characters", http.StatusBadRequest)
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
	db.LogAudit(actorUsername(r), "create", username, "role="+role+" department="+department)
	renderStaffList(w, r)
}

// AdminUpdateStaff renames an account and/or moves it between departments —
// role (admin vs. worker) is deliberately not editable here, see
// db.UpdateUserProfile's doc comment.
func AdminUpdateStaff(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid staff id", http.StatusBadRequest)
		return
	}
	target, err := db.GetUserByID(id)
	if err != nil {
		http.Error(w, "Staff account not found", http.StatusNotFound)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	if username == "" || len(username) > 50 {
		http.Error(w, "Username required (max 50 characters)", http.StatusBadRequest)
		return
	}

	department := target.Department
	if target.Role != models.RoleAdmin {
		deptParam := r.FormValue("department")
		if !models.IsValidDepartment(deptParam) {
			http.Error(w, "Invalid department", http.StatusBadRequest)
			return
		}
		department = deptParam
	}

	if err := db.UpdateUserProfile(id, username, department); err != nil {
		if err == db.ErrDuplicateUsername {
			http.Error(w, "That username is already taken", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to update account", http.StatusInternalServerError)
		return
	}
	db.LogAudit(actorUsername(r), "update", username, fmt.Sprintf("was %q/%s, now %q/%s", target.Username, target.Department, username, department))
	renderStaffList(w, r)
}

// AdminResetStaffPassword sets a new password for a staff account directly —
// there's no self-service reset flow (no email system exists in this app),
// so this is the only way to recover an account whose password is forgotten
// short of deactivate-and-recreate. Also force-closes any session the
// account currently has open, same reasoning as deactivation: a password
// reset should invalidate whatever session was authenticated under the old
// one.
func AdminResetStaffPassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid staff id", http.StatusBadRequest)
		return
	}
	target, err := db.GetUserByID(id)
	if err != nil {
		http.Error(w, "Staff account not found", http.StatusNotFound)
		return
	}
	password := r.FormValue("password")
	if len(password) < 6 {
		http.Error(w, "Password must be at least 6 characters", http.StatusBadRequest)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "Failed to reset password", http.StatusInternalServerError)
		return
	}
	if err := db.SetUserPassword(id, hash); err != nil {
		http.Error(w, "Failed to reset password", http.StatusInternalServerError)
		return
	}
	db.DeleteAuthSessionsForUser(id)
	db.LogAudit(actorUsername(r), "reset_password", target.Username, "")
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
	target, _ := db.GetUserByID(id)
	if err := db.SetUserActive(id, false); err != nil {
		http.Error(w, "Failed to deactivate account", http.StatusInternalServerError)
		return
	}
	db.DeleteAuthSessionsForUser(id)
	db.LogAudit(currentUser.Username, "deactivate", target.Username, "")
	renderStaffList(w, r)
}

func AdminActivateStaff(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid staff id", http.StatusBadRequest)
		return
	}
	target, _ := db.GetUserByID(id)
	if err := db.SetUserActive(id, true); err != nil {
		http.Error(w, "Failed to activate account", http.StatusInternalServerError)
		return
	}
	db.LogAudit(actorUsername(r), "activate", target.Username, "")
	renderStaffList(w, r)
}

// auditLimit bounds the audit trail panel — most-recent-first, not full
// pagination, same reasoning as the order-history cap before it got real
// pagination: this one's low-volume enough (staff-management actions only,
// not every order) that a sane cap is still the right call.
const auditLimit = 100

// AdminAuditLog serves the #audit-log fragment for its initial hx-get load.
func AdminAuditLog(w http.ResponseWriter, r *http.Request) {
	entries, err := db.GetRecentAuditLog(auditLimit)
	if err != nil {
		http.Error(w, "Failed to load audit log", http.StatusInternalServerError)
		return
	}
	tmpl := template.Must(template.ParseFiles("templates/_audit_log.html"))
	tmpl.Execute(w, map[string]interface{}{"Entries": entries})
}
