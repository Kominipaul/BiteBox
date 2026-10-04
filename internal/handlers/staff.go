package handlers

import (
	"fmt"
	"html/template"
	"log"
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
	Hue         int    // avatar colour, av0..av5 in staff.css
	RoleName    string // Admin, Manager, Waiter, Bar, Kitchen, DJ
	RoleKey     string // admin or the department, for the filter chips
	Access      string // what this role can see
	Online      bool
	Sessions    int // devices signed in right now
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
			Hue:         hue(u.Username),
			Online:      u.LastSeenAt != nil && time.Since(*u.LastSeenAt) < 5*time.Minute,
			Sessions:    db.CountAuthSessions(u.ID),
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
		v.RoleKey = u.Department
		if u.Role == models.RoleAdmin {
			v.RoleKey = models.RoleAdmin
		}
		for _, r := range staffRoles {
			if r.Key == v.RoleKey {
				v.RoleName, v.Access = r.Name, r.Access
			}
		}
		views = append(views, v)
	}
	return views
}

// staffRoles are the roles an account can have, as the Staff page shows
// them: key is "admin" or a worker department.
var staffRoles = []struct{ Key, Name, Access string }{
	{models.DepartmentWaiter, "Waiter", "Ready orders, call-waiter alerts, bookings and floor"},
	{models.DepartmentBar, "Bar", "Drink orders to make"},
	{models.DepartmentKitchen, "Kitchen", "Food orders to cook"},
	{models.DepartmentDJ, "DJ", "Song requests"},
	{models.DepartmentSuperworker, "Manager", "Every station's tools, plus menu, orders, logs and helping staff with passwords"},
	{models.RoleAdmin, "Admin", "Everything: menu, staff, reports and settings"},
}

// mayHelp: an admin can act on any account. A manager can help the floor
// staff (waiter, bar, kitchen, DJ) with a forgotten password or a lost
// device, but never on an admin, another manager, or themselves (they use
// Change password). Everything else on the Staff page stays admin-only.
func mayHelp(actor, target models.User) bool {
	if actor.Role == models.RoleAdmin {
		return true
	}
	return target.Role != models.RoleAdmin && target.Department != models.DepartmentSuperworker && target.ID != actor.ID
}

// staffSaved closes the staff panel with a confirmation and refreshes the list.
func staffSaved(w http.ResponseWriter, msg string) {
	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"staffSaved":%q}`, msg))
	w.WriteHeader(http.StatusNoContent)
}

// renderStaffEditor is the add/edit panel on the Staff page (id 0 = new).
func renderStaffEditor(w http.ResponseWriter, r *http.Request, id int, notice string) {
	data := map[string]interface{}{"New": id == 0, "Roles": staffRoles, "Notice": notice}
	if id != 0 {
		u, err := db.GetUserByID(id)
		if err != nil {
			http.Error(w, "Staff account not found", http.StatusNotFound)
			return
		}
		me, _ := UserFromContext(r)
		if !mayHelp(me, u) {
			http.Error(w, "Only an admin can open that account.", http.StatusForbidden)
			return
		}
		data["Limited"] = me.Role != models.RoleAdmin
		data["U"] = buildStaffViews([]models.User{u}, me.ID)[0]
		data["Pages"] = accessRows(u)
		data["Custom"] = u.PagesCustom
		data["History"] = staffHistory(u.Username)
	} else {
		data["U"] = staffRowView{RoleKey: models.DepartmentWaiter}
		data["Pages"] = accessRows(models.User{Role: models.RoleWorker, Department: models.DepartmentWaiter})
	}
	defaults := map[string]string{}
	for _, r := range staffRoles {
		defaults[r.Key] = strings.Join(models.DefaultPages(r.Key), ",")
	}
	data["Defaults"] = defaults
	tmpl := template.Must(template.ParseFiles("templates/bk_layout.html", "templates/_staff_editor.html"))
	if err := tmpl.ExecuteTemplate(w, "staff-editor", data); err != nil {
		log.Printf("staff editor: %v", err)
	}
}

func AdminStaffNew(w http.ResponseWriter, r *http.Request) { renderStaffEditor(w, r, 0, "") }

func AdminStaffEdit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid staff id", http.StatusBadRequest)
		return
	}
	renderStaffEditor(w, r, id, "")
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
	if currentUser.Role != models.RoleAdmin {
		helpable := users[:0]
		for _, u := range users {
			if mayHelp(currentUser, u) {
				helpable = append(helpable, u)
			}
		}
		users = helpable
	}
	tmpl := template.Must(template.ParseFiles("templates/bk_layout.html", "templates/_staff_list.html"))
	views := buildStaffViews(users, currentUser.ID)
	online, inactive := 0, 0
	for _, v := range views {
		if !v.IsActive {
			inactive++
		} else if v.Online {
			online++
		}
	}
	tmpl.ExecuteTemplate(w, "_staff_list.html", map[string]interface{}{"Staff": views, "Online": online, "Inactive": inactive})
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

	if username == "" || len(username) > 50 {
		http.Error(w, "Username required (max 50 characters)", http.StatusBadRequest)
		return
	}
	if msg := auth.CheckNewPassword(username, password); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
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
	newID, err := db.CreateUser(username, hash, role, department)
	if err != nil {
		if err == db.ErrDuplicateUsername {
			http.Error(w, "That username is already taken", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to create account", http.StatusInternalServerError)
		return
	}
	// The creator knows this password: it's a first-login password only.
	if err := db.RequirePasswordChange(newID); err != nil {
		log.Printf("create %q: %v", username, err) // #nosec G706 -- %q escapes control characters
		http.Error(w, "The account was created, but it couldn't be set to change its password. Reset its password to fix this.", http.StatusInternalServerError)
		return
	}
	db.LogAudit(actorUsername(r), "create", username, "role="+role+" department="+department)
	if role != models.RoleAdmin {
		saveAccess(r, newID, username)
	}
	if fromEditor(r) {
		staffSaved(w, username+" can log in now, then picks their own password")
		return
	}
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
	if target.Role != models.RoleAdmin {
		saveAccess(r, id, username)
	}
	if fromEditor(r) {
		staffSaved(w, username+" saved")
		return
	}
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
	if me, _ := UserFromContext(r); !mayHelp(me, target) {
		http.Error(w, "Only an admin can change that account.", http.StatusForbidden)
		return
	}
	password := r.FormValue("password")
	if msg := auth.CheckNewPassword(target.Username, password); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
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
	// The helper knows this password, so it only works once: the owner picks
	// their own at the next login. A reset is also the lost-phone recovery,
	// so two-step login starts over (an admin must set it up again).
	for _, step := range []func(int) error{db.RequirePasswordChange, db.DisableTOTP, db.DeleteAuthSessionsForUser} {
		if err := step(id); err != nil {
			log.Printf("reset %q: %v", target.Username, err)
			http.Error(w, "Failed to reset password", http.StatusInternalServerError)
			return
		}
	}
	db.LogAudit(actorUsername(r), "reset_password", target.Username, "")
	if fromEditor(r) {
		msg := "Password reset. " + target.Username + " was logged out, signs in with it once, then picks their own."
		if target.TwoFactor {
			msg += " Two-step login was reset too."
		}
		renderStaffEditor(w, r, id, msg)
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
	target, _ := db.GetUserByID(id)
	if err := db.SetUserActive(id, false); err != nil {
		http.Error(w, "Failed to deactivate account", http.StatusInternalServerError)
		return
	}
	// GetUserBySessionID refuses inactive accounts anyway; this just tidies.
	if err := db.DeleteAuthSessionsForUser(id); err != nil {
		log.Printf("deactivate %q: dropping sessions: %v", target.Username, err)
	}
	db.LogAudit(currentUser.Username, "deactivate", target.Username, "")
	if fromEditor(r) {
		w.Header().Set("HX-Trigger", "staffChanged")
		renderStaffEditor(w, r, id, target.Username+" is deactivated and was logged out.")
		return
	}
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
	if fromEditor(r) {
		w.Header().Set("HX-Trigger", "staffChanged")
		renderStaffEditor(w, r, id, target.Username+" can log in again.")
		return
	}
	renderStaffList(w, r)
}

type accessRow struct {
	models.BackOfficePage
	Checked bool
}

// accessRows are the page checkboxes for u (admin-only pages left out).
func accessRows(u models.User) []accessRow {
	var rows []accessRow
	for _, p := range models.BackOfficePages {
		if !p.AdminOnly {
			rows = append(rows, accessRow{p, u.Can(p.Key)})
		}
	}
	return rows
}

// saveAccess stores the panel's page access for a worker account: its
// role's defaults, or (access_custom=1) exactly the pages ticked. Only
// requests from the panel carry access_form=1; anything else leaves it be.
func saveAccess(r *http.Request, id int, username string) {
	if r.FormValue("access_form") != "1" {
		return
	}
	custom := r.FormValue("access_custom") == "1"
	var pages []string
	if custom {
		r.ParseForm()
		for _, key := range r.Form["pages"] {
			for _, p := range models.BackOfficePages {
				if p.Key == key && !p.AdminOnly {
					pages = append(pages, key)
				}
			}
		}
	}
	before, _ := db.GetUserByID(id)
	if err := db.SetUserPages(id, custom, pages); err != nil {
		return
	}
	if before.PagesCustom != custom || strings.Join(before.Pages, ",") != strings.Join(pages, ",") {
		detail := "role defaults"
		if custom {
			detail = "custom: " + strings.Join(pages, ", ")
			if len(pages) == 0 {
				detail = "custom: no pages"
			}
		}
		db.LogAudit(actorUsername(r), "access", username, detail)
	}
}

type historyRow struct {
	When, Text string
}

var auditText = map[string]string{
	"create": "Account created", "update": "Name or role changed", "reset_password": "Password changed",
	"deactivate": "Deactivated", "activate": "Activated again", "sign_out_all": "Signed out on every device",
	"access": "Page access changed", "theme": "Look & brand saved",
	"order_status": "Order status changed", "order_cancel": "Order cancelled", "order_payment": "Order payment changed",
	"station_close": "Closed", "station_open": "Reopened", "station_hours": "Opening hours changed", "station_busy": "Busy mode",
	"area_pause": "Ordering paused", "area_resume": "Ordering resumed", "special_day": "Special day saved",
}

// staffHistory is the account's audit trail in plain words.
func staffHistory(username string) []historyRow {
	entries, _ := db.GetAuditForUser(username, 30)
	rows := make([]historyRow, 0, len(entries))
	for _, e := range entries {
		text := auditText[e.Action]
		if text == "" {
			text = e.Action
		}
		switch {
		case e.Target == username && e.Actor != username:
			text += " by " + e.Actor
		case e.Target != username:
			text = text + ": " + e.Target
		}
		if e.Action == "access" && e.Detail != "" {
			text += " (" + e.Detail + ")"
		}
		rows = append(rows, historyRow{e.CreatedAt.Local().Format("Mon 2 Jan 2006, 15:04"), text})
	}
	return rows
}

// AdminStaffSignOut ends every session an account has open (lost phone,
// shared tablet) without changing its password.
func AdminStaffSignOut(w http.ResponseWriter, r *http.Request) {
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
	if me, _ := UserFromContext(r); !mayHelp(me, target) {
		http.Error(w, "Only an admin can change that account.", http.StatusForbidden)
		return
	}
	if err := db.DeleteAuthSessionsForUser(id); err != nil {
		log.Printf("sign out %q: %v", target.Username, err)
		http.Error(w, "Failed to sign the account out", http.StatusInternalServerError)
		return
	}
	db.LogAudit(actorUsername(r), "sign_out_all", target.Username, "")
	if fromEditor(r) {
		w.Header().Set("HX-Trigger", "staffChanged")
		renderStaffEditor(w, r, id, target.Username+" was signed out on every device.")
		return
	}
	renderStaffList(w, r)
}
