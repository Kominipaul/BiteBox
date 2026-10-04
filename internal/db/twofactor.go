package db

import (
	"database/sql"

	"bitebox/internal/auth"
)

// Two-step login storage. Secrets are sealed (auth.Seal) with a key kept
// outside the database, so a copied database or backup can't mint codes.

// GetTOTPSecret is the user's active authenticator secret ("" = not set up).
func GetTOTPSecret(userID int) (string, error) {
	var sealed string
	if err := DB.QueryRow("SELECT totp_secret FROM users WHERE id = ?", userID).Scan(&sealed); err != nil {
		return "", err
	}
	return auth.Unseal(sealed)
}

// GetTOTPPending is the secret shown on the setup page, not active until a
// code from it is confirmed (EnableTOTP).
func GetTOTPPending(userID int) (string, error) {
	var sealed string
	if err := DB.QueryRow("SELECT totp_pending FROM users WHERE id = ?", userID).Scan(&sealed); err != nil {
		return "", err
	}
	return auth.Unseal(sealed)
}

func SetTOTPPending(userID int, secret string) error {
	sealed, err := auth.Seal(secret)
	if err != nil {
		return err
	}
	_, err = DB.Exec("UPDATE users SET totp_pending = ? WHERE id = ?", sealed, userID)
	return err
}

// EnableTOTP makes the pending secret the active one; step is the code step
// just used to confirm it, so that code can't be used again to log in.
func EnableTOTP(userID int, step int64) error {
	res, err := DB.Exec("UPDATE users SET totp_secret = totp_pending, totp_pending = '', totp_last_step = ? WHERE id = ? AND totp_pending != ''", step, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DisableTOTP turns two-step login off (an admin is asked to set it up again
// on their next request).
func DisableTOTP(userID int) error {
	_, err := DB.Exec("UPDATE users SET totp_secret = '', totp_pending = '', totp_last_step = 0 WHERE id = ?", userID)
	return err
}

// UseTOTPStep records a code's step as used. false means that step (or a
// later one) was already used: a replayed code. Atomic, so two requests
// racing with the same code can't both win.
func UseTOTPStep(userID int, step int64) bool {
	res, err := DB.Exec("UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?", step, userID, step)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}
