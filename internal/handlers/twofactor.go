package handlers

import (
	"encoding/base64"
	"html/template"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"bitebox/internal/auth"
	"bitebox/internal/db"
	"bitebox/internal/models"

	"rsc.io/qr"
)

// ----- Two-step login (authenticator app codes) -----
//
// Login with two-step on: the password is checked first (LoginSubmit), then
// instead of a session the browser gets a short-lived pending-login cookie
// and the code page; only a correct, unused code (POST /login/code) creates
// the session. Admins must have it (models.User.NeedsTwoFactor, enforced in
// RequireRole); anyone else can turn it on from /account/2fa. A lost phone:
// an admin's "reset password" for staff, or cmd/resetpassword for an admin.

const (
	pendingLoginCookie = "bitebox_login"
	pendingLoginTTL    = 5 * time.Minute
	maxCodeAttempts    = 5
)

type pendingLogin struct {
	userID   int
	expires  time.Time
	attempts int
}

// ponytail: in memory; a restart mid-login just means typing the password
// again.
var pendingLogins = struct {
	sync.Mutex
	m map[string]*pendingLogin
}{m: map[string]*pendingLogin{}}

func startPendingLogin(w http.ResponseWriter, r *http.Request, userID int) {
	token := auth.GenerateSessionID()
	pendingLogins.Lock()
	for t, p := range pendingLogins.m {
		if time.Now().After(p.expires) {
			delete(pendingLogins.m, t)
		}
	}
	pendingLogins.m[token] = &pendingLogin{userID: userID, expires: time.Now().Add(pendingLoginTTL)}
	pendingLogins.Unlock()
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- HttpOnly + SameSite set; Secure whenever the request came over HTTPS (auth.IsSecureRequest)
		Name: pendingLoginCookie, Value: token, Path: "/login", MaxAge: int(pendingLoginTTL.Seconds()),
		HttpOnly: true, Secure: auth.IsSecureRequest(r), SameSite: http.SameSiteStrictMode,
	})
}

func clearPendingLogin(w http.ResponseWriter, r *http.Request, token string) {
	pendingLogins.Lock()
	delete(pendingLogins.m, token)
	pendingLogins.Unlock()
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- HttpOnly + SameSite set; Secure whenever the request came over HTTPS (auth.IsSecureRequest)
		Name: pendingLoginCookie, Value: "", Path: "/login", MaxAge: -1,
		HttpOnly: true, Secure: auth.IsSecureRequest(r), SameSite: http.SameSiteStrictMode,
	})
}

func renderLoginCode(w http.ResponseWriter, errMsg string) {
	settings, _ := db.GetSettings()
	w.Header().Set("Cache-Control", "no-store")
	template.Must(template.ParseFiles("templates/login_code.html")).Execute(w, map[string]interface{}{
		"Error": errMsg, "VenueName": settings.VenueName,
	})
}

// LoginCodeSubmit is the second login step. It sits behind the same per-IP
// limiter as /login, and each pending login gets maxCodeAttempts tries.
func LoginCodeSubmit(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(pendingLoginCookie)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	pendingLogins.Lock()
	p, ok := pendingLogins.m[c.Value]
	ok = ok && time.Now().Before(p.expires)
	var userID, attempts int
	if ok {
		p.attempts++
		userID, attempts = p.userID, p.attempts
	}
	pendingLogins.Unlock()
	if !ok || attempts > maxCodeAttempts {
		clearPendingLogin(w, r, c.Value)
		renderLogin(w, "Please log in again.")
		return
	}

	user, err := db.GetUserByID(userID)
	secret, serr := db.GetTOTPSecret(userID)
	if err != nil || serr != nil || !user.IsActive || secret == "" {
		if serr != nil {
			log.Printf("two-step login for user %d: %v", userID, serr)
		}
		clearPendingLogin(w, r, c.Value)
		renderLogin(w, "Something went wrong, please try again")
		return
	}
	step, valid := auth.VerifyTOTP(secret, r.FormValue("code"), time.Now())
	if !valid || !db.UseTOTPStep(userID, step) {
		if attempts >= maxCodeAttempts {
			clearPendingLogin(w, r, c.Value)
			renderLogin(w, "Too many wrong codes. Log in again.")
			return
		}
		renderLoginCode(w, "That code didn't work. Codes change every 30 seconds: use the one showing now.")
		return
	}

	clearPendingLogin(w, r, c.Value)
	finishLogin(w, r, user)
}

// finishLogin creates the session once every login step has passed.
func finishLogin(w http.ResponseWriter, r *http.Request, user models.User) {
	sessionID := auth.GenerateSessionID()
	if err := db.CreateAuthSession(sessionID, user.ID); err != nil {
		renderLogin(w, "Something went wrong, please try again")
		return
	}
	auth.SetSessionCookie(w, r, sessionID)
	// RequireRole would redirect on the next request anyway; going there
	// directly skips that round trip.
	switch {
	case user.MustChangePassword:
		http.Redirect(w, r, "/change-password", http.StatusSeeOther)
	case user.NeedsTwoFactor() && !user.TwoFactor:
		http.Redirect(w, r, "/account/2fa", http.StatusSeeOther)
	default:
		http.Redirect(w, r, homePathFor(user.Role), http.StatusSeeOther)
	}
}

// twoFactorExempt: what an admin who still has to set up two-step login can
// reach (RequireRole sends everything else to /account/2fa).
func twoFactorExempt(path string) bool {
	return path == "/account/2fa" || path == "/account/2fa/off" || path == "/change-password"
}

func qrDataURI(text string) (template.URL, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	code.Scale = 6
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())), nil // #nosec G203 -- a base64 PNG we just made
}

func renderTwoFactorPage(w http.ResponseWriter, r *http.Request, user models.User, errMsg string) {
	settings, _ := db.GetSettings()
	data := map[string]interface{}{
		"VenueName": settings.VenueName, "Username": user.Username, "On": user.TwoFactor,
		"Required": user.NeedsTwoFactor(), "Home": homePathFor(user.Role), "Error": errMsg,
	}
	if !user.TwoFactor {
		// The same secret until it's confirmed, so a reload doesn't
		// invalidate what the phone already scanned.
		secret, err := db.GetTOTPPending(user.ID)
		if err != nil || secret == "" {
			secret = auth.NewTOTPSecret()
			if err := db.SetTOTPPending(user.ID, secret); err != nil {
				log.Printf("two-step setup for %q: %v", user.Username, err)
				http.Error(w, "Could not start two-step login setup", http.StatusInternalServerError)
				return
			}
		}
		issuer := settings.VenueName
		if issuer == "" {
			issuer = "BiteBox"
		}
		img, err := qrDataURI(auth.TOTPURI(issuer, user.Username, secret))
		if err != nil {
			http.Error(w, "Could not make the setup code", http.StatusInternalServerError)
			return
		}
		var groups []string
		for i := 0; i < len(secret); i += 4 {
			groups = append(groups, secret[i:min(i+4, len(secret))])
		}
		data["QR"], data["Secret"] = img, strings.Join(groups, " ")
	}
	w.Header().Set("Cache-Control", "no-store")
	template.Must(template.ParseFiles("templates/account_2fa.html")).Execute(w, data)
}

func TwoFactorPage(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r)
	renderTwoFactorPage(w, r, user, "")
}

// TwoFactorEnable confirms the scanned secret with a code from it.
func TwoFactorEnable(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r)
	if user.TwoFactor {
		http.Redirect(w, r, "/account/2fa", http.StatusSeeOther)
		return
	}
	secret, err := db.GetTOTPPending(user.ID)
	if err != nil || secret == "" {
		renderTwoFactorPage(w, r, user, "Setup expired, scan the new code.")
		return
	}
	step, ok := auth.VerifyTOTP(secret, r.FormValue("code"), time.Now())
	if !ok {
		renderTwoFactorPage(w, r, user, "That code didn't match. Check the app shows "+user.Username+" and type the code showing now.")
		return
	}
	if err := db.EnableTOTP(user.ID, step); err != nil {
		renderTwoFactorPage(w, r, user, "Something went wrong, please try again")
		return
	}
	// Other devices logged in with the password alone: sign them out.
	if err := keepOnlyThisSession(w, r, user.ID); err != nil {
		log.Printf("two-step on for %q: signing other devices out: %v", user.Username, err)
		renderTwoFactorPage(w, r, user, "Two-step login is on, but other devices couldn't be signed out. Use Sign out on every device in Staff help.")
		return
	}
	db.LogAudit(user.Username, "2fa_on", user.Username, "Two-step login turned on")
	http.Redirect(w, r, "/account/2fa", http.StatusSeeOther)
}

// TwoFactorDisable needs the password and a current code, so an unattended
// logged-in tablet can't be used to strip it.
func TwoFactorDisable(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r)
	if !user.TwoFactor {
		http.Redirect(w, r, "/account/2fa", http.StatusSeeOther)
		return
	}
	secret, err := db.GetTOTPSecret(user.ID)
	step, ok := auth.VerifyTOTP(secret, r.FormValue("code"), time.Now())
	if err != nil || !auth.CheckPassword(user.PasswordHash, r.FormValue("password")) || !ok || !db.UseTOTPStep(user.ID, step) {
		renderTwoFactorPage(w, r, user, "Password or code is wrong.")
		return
	}
	if err := db.DisableTOTP(user.ID); err != nil {
		renderTwoFactorPage(w, r, user, "Something went wrong, please try again")
		return
	}
	db.LogAudit(user.Username, "2fa_off", user.Username, "Two-step login turned off")
	http.Redirect(w, r, "/account/2fa", http.StatusSeeOther)
}

// keepOnlyThisSession signs a user out everywhere else and gives this
// device a fresh session (after a password change or turning on two-step).
func keepOnlyThisSession(w http.ResponseWriter, r *http.Request, userID int) error {
	if err := db.DeleteAuthSessionsForUser(userID); err != nil {
		return err
	}
	sessionID := auth.GenerateSessionID()
	if err := db.CreateAuthSession(sessionID, userID); err != nil {
		auth.ClearSessionCookie(w, r) // signed out here too; log in again
		return nil
	}
	auth.SetSessionCookie(w, r, sessionID)
	return nil
}
