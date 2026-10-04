package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- HMAC-SHA1 is what RFC 6238 and every authenticator app use; HMAC-SHA1 is not broken
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Two-step login with an authenticator app (TOTP, RFC 6238: SHA-1, 6 digits,
// 30-second steps — what Google Authenticator, Authy, 1Password etc. use).

const totpStep = 30

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret is a fresh 160-bit secret, base32 as authenticator apps
// expect it.
func NewTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return b32.EncodeToString(b)
}

// TOTPURI is what the setup QR code holds (the Key Uri Format).
func TOTPURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

func totpAt(key []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) // #nosec G115 -- step is Unix time / 30, never negative
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1000000)
}

// TOTPStep is the 30-second step a time falls in.
func TOTPStep(t time.Time) int64 { return t.Unix() / totpStep }

// TOTPCode is the code an app shows for secret at time t.
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	return totpAt(key, TOTPStep(t)), nil
}

// VerifyTOTP checks code against secret at now, allowing one step of clock
// drift either way, and returns the step it matched. Callers must refuse a
// step at or before the last one used (db.UseTOTPStep), so a code seen over
// someone's shoulder can't be replayed.
func VerifyTOTP(secret, code string, now time.Time) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return 0, false
	}
	cur := TOTPStep(now)
	for _, step := range []int64{cur, cur - 1, cur + 1} {
		if subtle.ConstantTimeCompare([]byte(totpAt(key, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
