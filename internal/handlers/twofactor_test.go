package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"bitebox/internal/auth"
	"bitebox/internal/db"
	"bitebox/internal/models"
)

func newTwoFactorUser(t *testing.T, role string) (models.User, string) {
	t.Helper()
	u := newChangePasswordTestUser(t, "Tw0-step-pass", false)
	if role == models.RoleAdmin {
		db.DB.Exec("UPDATE users SET role = ? WHERE id = ?", models.RoleAdmin, u.ID)
	}
	secret := auth.NewTOTPSecret()
	if err := db.SetTOTPPending(u.ID, secret); err != nil {
		t.Fatal(err)
	}
	if err := db.EnableTOTP(u.ID, 0); err != nil {
		t.Fatal(err)
	}
	u, _ = db.GetUserByID(u.ID)
	return u, secret
}

func postLogin(h http.HandlerFunc, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func cookieNamed(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name && c.MaxAge >= 0 {
			return c
		}
	}
	return nil
}

func TestTwoStepLogin(t *testing.T) {
	u, secret := newTwoFactorUser(t, models.RoleWorker)

	// Right password: no session yet, just the code page.
	w := postLogin(LoginSubmit, "/login", url.Values{"username": {u.Username}, "password": {"Tw0-step-pass"}})
	if cookieNamed(w, auth.SessionCookieName) != nil || !strings.Contains(w.Body.String(), "Enter your code") {
		t.Fatalf("password alone logged in, or no code page: %d", w.Code)
	}
	pending := cookieNamed(w, pendingLoginCookie)
	if pending == nil || pending.Path != "/login" || !pending.HttpOnly {
		t.Fatalf("pending-login cookie: %+v", pending)
	}

	// Wrong code: still no session.
	w = postLogin(LoginCodeSubmit, "/login/code", url.Values{"code": {"000000"}}, pending)
	if cookieNamed(w, auth.SessionCookieName) != nil {
		t.Fatal("wrong code logged in")
	}

	// Right code: session.
	code, _ := auth.TOTPCode(secret, time.Now())
	w = postLogin(LoginCodeSubmit, "/login/code", url.Values{"code": {code}}, pending)
	if cookieNamed(w, auth.SessionCookieName) == nil || w.Code != http.StatusSeeOther {
		t.Fatalf("right code didn't log in: %d %s", w.Code, w.Body)
	}

	// The same code again (shoulder-surfed) is refused.
	w = postLogin(LoginSubmit, "/login", url.Values{"username": {u.Username}, "password": {"Tw0-step-pass"}})
	w = postLogin(LoginCodeSubmit, "/login/code", url.Values{"code": {code}}, cookieNamed(w, pendingLoginCookie))
	if cookieNamed(w, auth.SessionCookieName) != nil {
		t.Fatal("a used code worked twice")
	}

	// No pending login (skipped the password): nothing.
	w = postLogin(LoginCodeSubmit, "/login/code", url.Values{"code": {code}}, &http.Cookie{Name: pendingLoginCookie, Value: "made-up"})
	if cookieNamed(w, auth.SessionCookieName) != nil {
		t.Fatal("code without the password step logged in")
	}
}

func TestTwoStepLoginLocksAfterTooManyCodes(t *testing.T) {
	u, secret := newTwoFactorUser(t, models.RoleWorker)
	w := postLogin(LoginSubmit, "/login", url.Values{"username": {u.Username}, "password": {"Tw0-step-pass"}})
	pending := cookieNamed(w, pendingLoginCookie)
	for i := 0; i < maxCodeAttempts; i++ {
		postLogin(LoginCodeSubmit, "/login/code", url.Values{"code": {"111111"}}, pending)
	}
	code, _ := auth.TOTPCode(secret, time.Now())
	w = postLogin(LoginCodeSubmit, "/login/code", url.Values{"code": {code}}, pending)
	if cookieNamed(w, auth.SessionCookieName) != nil {
		t.Fatal("guessing should end the pending login")
	}
}

func TestAdminMustSetUpTwoStep(t *testing.T) {
	u := newChangePasswordTestUser(t, "Admin-pass-1", false)
	db.DB.Exec("UPDATE users SET role = ? WHERE id = ?", models.RoleAdmin, u.ID)
	sid := auth.GenerateSessionID()
	db.CreateAuthSession(sid, u.ID)
	ok := RequireRole(models.RoleAdmin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		w := httptest.NewRecorder()
		ok.ServeHTTP(w, r)
		return w
	}
	if w := get("/admin"); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/account/2fa" {
		t.Fatalf("admin without two-step: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := get("/account/2fa"); w.Code != http.StatusTeapot {
		t.Fatalf("setup page itself must be reachable: %d", w.Code)
	}

	// Set it up through the page.
	page := httptest.NewRecorder()
	user, _ := db.GetUserByID(u.ID)
	TwoFactorPage(page, withPushTestUser(httptest.NewRequest(http.MethodGet, "/account/2fa", nil), user))
	if !strings.Contains(page.Body.String(), "data:image/png;base64,") {
		t.Fatal("setup page has no QR code")
	}
	secret, _ := db.GetTOTPPending(u.ID)
	code, _ := auth.TOTPCode(secret, time.Now())
	r := httptest.NewRequest(http.MethodPost, "/account/2fa", strings.NewReader(url.Values{"code": {code}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	TwoFactorEnable(w, withPushTestUser(r, user))
	if after, _ := db.GetUserByID(u.ID); !after.TwoFactor {
		t.Fatalf("not enabled: %d %s", w.Code, w.Body)
	}
	// Turning it on signed the old session out; the new one works.
	sid = cookieNamed(w, auth.SessionCookieName).Value
	if w := get("/admin"); w.Code != http.StatusTeapot {
		t.Fatalf("admin with two-step: %d", w.Code)
	}
	// The stored secret is sealed, not readable from the database.
	var stored string
	db.DB.QueryRow("SELECT totp_secret FROM users WHERE id = ?", u.ID).Scan(&stored)
	if stored == secret || !strings.HasPrefix(stored, "v1:") {
		t.Fatalf("secret stored in the clear: %q", stored)
	}
}

// A staff reset is the lost-phone path: two-step starts over and the
// password only works once.
func TestStaffResetClearsTwoStepAndForcesChange(t *testing.T) {
	u, _ := newTwoFactorUser(t, models.RoleWorker)
	admin := models.User{ID: 1, Username: "admin", Role: models.RoleAdmin}
	r := httptest.NewRequest(http.MethodPost, "/admin/staff/x/reset-password", strings.NewReader("password=short"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	AdminResetStaffPassword(w, withPushTestUser(withParam(r, "id", strconv.Itoa(u.ID)), admin))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("weak reset password accepted: %d", w.Code)
	}
	r = httptest.NewRequest(http.MethodPost, "/admin/staff/x/reset-password", strings.NewReader("password=Temp-pass-4821"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	AdminResetStaffPassword(w, withPushTestUser(withParam(r, "id", strconv.Itoa(u.ID)), admin))
	after, _ := db.GetUserByID(u.ID)
	if w.Code != http.StatusOK || after.TwoFactor || !after.MustChangePassword {
		t.Fatalf("reset: %d twoFactor=%v mustChange=%v", w.Code, after.TwoFactor, after.MustChangePassword)
	}
}
