package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"bitebox/internal/auth"
	"bitebox/internal/db"
	"bitebox/internal/models"
)

// newChangePasswordTestUser creates a fresh worker account with a known
// plaintext password, returning both the row and the plaintext so tests can
// exercise ChangePasswordSubmit's "current password" check for real instead
// of reaching into the hash.
func newChangePasswordTestUser(t *testing.T, plaintext string, mustChange bool) models.User {
	t.Helper()
	hash, err := auth.HashPassword(plaintext)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	username := "cptest_" + strconv.Itoa(testUserCounter())
	id, err := db.CreateUser(username, hash, models.RoleWorker, models.DepartmentWaiter)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if mustChange {
		if _, err := db.DB.Exec("UPDATE users SET must_change_password = 1 WHERE id = ?", id); err != nil {
			t.Fatalf("failed to flag test user for forced change: %v", err)
		}
	}
	user, err := db.GetUserByID(id)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	return user
}

var changePasswordCounter int

func testUserCounter() int {
	changePasswordCounter++
	return changePasswordCounter
}

func changePasswordForm(current, newPassword, confirm string) *strings.Reader {
	form := url.Values{
		"current_password": {current},
		"new_password":     {newPassword},
		"confirm_password": {confirm},
	}
	return strings.NewReader(form.Encode())
}

func TestChangePasswordSubmitSuccessClearsForcedFlag(t *testing.T) {
	user := newChangePasswordTestUser(t, "oldpass1", true)

	r := httptest.NewRequest(http.MethodPost, "/change-password", changePasswordForm("oldpass1", "newpass1", "newpass1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = withPushTestUser(r, user)
	w := httptest.NewRecorder()

	ChangePasswordSubmit(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/worker" {
		t.Errorf("redirect location = %q, want /worker", loc)
	}

	updated, err := db.GetUserByID(user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if updated.MustChangePassword {
		t.Error("must_change_password should be cleared after a successful change")
	}
	if !auth.CheckPassword(updated.PasswordHash, "newpass1") {
		t.Error("password hash was not updated to the new password")
	}
	if auth.CheckPassword(updated.PasswordHash, "oldpass1") {
		t.Error("old password should no longer work")
	}
}

func TestChangePasswordSubmitRejectsWrongCurrentPassword(t *testing.T) {
	user := newChangePasswordTestUser(t, "correctpw", false)

	r := httptest.NewRequest(http.MethodPost, "/change-password", changePasswordForm("wrongpw", "newpass2", "newpass2"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = withPushTestUser(r, user)
	w := httptest.NewRecorder()

	ChangePasswordSubmit(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (re-rendered form with error)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Current password is incorrect") {
		t.Errorf("body missing expected error message: %s", w.Body.String())
	}

	updated, err := db.GetUserByID(user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if !auth.CheckPassword(updated.PasswordHash, "correctpw") {
		t.Error("password should not have changed")
	}
}

func TestChangePasswordSubmitRejectsMismatchedConfirmation(t *testing.T) {
	user := newChangePasswordTestUser(t, "correctpw2", false)

	r := httptest.NewRequest(http.MethodPost, "/change-password", changePasswordForm("correctpw2", "newpassA", "newpassB"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = withPushTestUser(r, user)
	w := httptest.NewRecorder()

	ChangePasswordSubmit(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (re-rendered form with error)", w.Code)
	}
	// html/template escapes the apostrophe in "don't" to &#39; — check for
	// the unescaped surrounding words instead of the literal error string.
	if !strings.Contains(w.Body.String(), "confirmation don") || !strings.Contains(w.Body.String(), "match") {
		t.Errorf("body missing expected mismatch error: %s", w.Body.String())
	}
}

func TestChangePasswordSubmitRejectsTooShortPassword(t *testing.T) {
	user := newChangePasswordTestUser(t, "correctpw3", false)

	r := httptest.NewRequest(http.MethodPost, "/change-password", changePasswordForm("correctpw3", "abc", "abc"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = withPushTestUser(r, user)
	w := httptest.NewRecorder()

	ChangePasswordSubmit(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (re-rendered form with error)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "at least 8 characters") {
		t.Errorf("body missing expected length error: %s", w.Body.String())
	}
}

func TestChangePasswordSubmitRejectsSamePassword(t *testing.T) {
	user := newChangePasswordTestUser(t, "samepass1", false)

	r := httptest.NewRequest(http.MethodPost, "/change-password", changePasswordForm("samepass1", "samepass1", "samepass1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = withPushTestUser(r, user)
	w := httptest.NewRecorder()

	ChangePasswordSubmit(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (re-rendered form with error)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "different from your current one") {
		t.Errorf("body missing expected same-password error: %s", w.Body.String())
	}
}

func TestChangePasswordPageReflectsForcedFlag(t *testing.T) {
	forced := newChangePasswordTestUser(t, "forcedpw1", true)
	r := httptest.NewRequest(http.MethodGet, "/change-password", nil)
	r = withPushTestUser(r, forced)
	w := httptest.NewRecorder()

	ChangePasswordPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "someone else chose") {
		t.Errorf("forced page should explain why the user landed here: %s", w.Body.String())
	}

	voluntary := newChangePasswordTestUser(t, "voluntarypw1", false)
	r2 := httptest.NewRequest(http.MethodGet, "/change-password", nil)
	r2 = withPushTestUser(r2, voluntary)
	w2 := httptest.NewRecorder()

	ChangePasswordPage(w2, r2)

	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w2.Code, w2.Body.String())
	}
	if strings.Contains(w2.Body.String(), "someone else chose") {
		t.Error("voluntary visit should not show the forced-flow notice")
	}
}

// TestRequireRoleRedirectsForcedAccountsExceptToChangePassword exercises the
// middleware guard itself, not just the handler: a forced account must be
// bounced from any other route in the group, but must still be able to
// reach /change-password (or nothing could ever clear the flag).
func TestRequireRoleRedirectsForcedAccountsExceptToChangePassword(t *testing.T) {
	forced := newChangePasswordTestUser(t, "middlewarepw1", true)
	sessionID := auth.GenerateSessionID()
	if err := db.CreateAuthSession(sessionID, forced.ID); err != nil {
		t.Fatalf("CreateAuthSession: %v", err)
	}

	mw := RequireRole(models.RoleAdmin, models.RoleWorker)
	reached := false
	inner := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/worker", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionID})
	w := httptest.NewRecorder()
	inner.ServeHTTP(w, r)

	if reached {
		t.Error("a forced account should not reach /worker")
	}
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/change-password" {
		t.Errorf("status = %d, location = %q, want 303 to /change-password", w.Code, w.Header().Get("Location"))
	}

	reached = false
	r2 := httptest.NewRequest(http.MethodGet, "/change-password", nil)
	r2.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionID})
	w2 := httptest.NewRecorder()
	inner.ServeHTTP(w2, r2)

	if !reached {
		t.Errorf("a forced account must still reach /change-password itself, got status %d", w2.Code)
	}
}

// Changing a password signs out every other session, so a stolen cookie
// stops working.
func TestChangePasswordSubmitEndsOtherSessions(t *testing.T) {
	user := newChangePasswordTestUser(t, "oldpass1", false)
	stolen := auth.GenerateSessionID()
	if err := db.CreateAuthSession(stolen, user.ID); err != nil {
		t.Fatalf("CreateAuthSession: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/change-password", changePasswordForm("oldpass1", "newpass1", "newpass1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	ChangePasswordSubmit(w, withPushTestUser(r, user))

	if _, err := db.GetUserBySessionID(stolen); err == nil {
		t.Error("old session still valid after password change")
	}
	var fresh string
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			fresh = c.Value
		}
	}
	if _, err := db.GetUserBySessionID(fresh); err != nil {
		t.Errorf("current device should get a working new session: %v", err)
	}
}
