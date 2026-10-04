package auth

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// RFC 6238 appendix B (SHA-1 key "12345678901234567890"), last 6 digits.
func TestTOTPMatchesRFC6238(t *testing.T) {
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		got, err := TOTPCode(secret, time.Unix(unix, 0))
		if err != nil || got != want {
			t.Errorf("T=%d: %q, want %q (%v)", unix, got, want, err)
		}
	}
}

func TestVerifyTOTPWindow(t *testing.T) {
	secret := NewTOTPSecret()
	now := time.Unix(1700000000, 0)
	code, _ := TOTPCode(secret, now)
	if step, ok := VerifyTOTP(secret, code, now.Add(25*time.Second)); !ok || step != TOTPStep(now) {
		t.Error("code from the previous step should still pass (clock drift)")
	}
	if _, ok := VerifyTOTP(secret, code, now.Add(2*time.Minute)); ok {
		t.Error("a two-minute-old code must fail")
	}
	if _, ok := VerifyTOTP(secret, "000000", now); ok && code != "000000" {
		t.Error("wrong code passed")
	}
	if _, ok := VerifyTOTP(secret, code[:3]+" "+code[3:], now); !ok {
		t.Error("a code typed with a space should pass")
	}
	if !strings.HasPrefix(TOTPURI("EGO Beach", "admin", secret), "otpauth://totp/EGO%20Beach:admin?") {
		t.Error("bad otpauth URI")
	}
}

func TestSealRoundTripAndKeyFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BITEBOX_KEY_FILE", filepath.Join(dir, "k"))
	keyOnce, key, keyErr = sync.Once{}, nil, nil // fresh key for this test
	sealed, err := Seal("JBSWY3DPEHPK3PXP")
	if err != nil || sealed == "JBSWY3DPEHPK3PXP" || !strings.HasPrefix(sealed, "v1:") {
		t.Fatalf("Seal: %q %v", sealed, err)
	}
	if out, err := Unseal(sealed); err != nil || out != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("Unseal: %q %v", out, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "k")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", info, err)
	}
	if s, _ := Seal(""); s != "" {
		t.Error("empty should stay empty")
	}
}

func TestCheckNewPassword(t *testing.T) {
	for _, bad := range []string{"short", "password", "12345678", "waiter123", "Waiter12", "waiter2026", "waiter-1234", "aaaaaaaaaa", strings.Repeat("x", 73)} {
		if CheckNewPassword("waiter", bad) == "" {
			t.Errorf("%q should be refused", bad)
		}
	}
	for _, good := range []string{"correct horse", "Sunset-Terrace-7", "dj-booth-nights", "waiter on the beach"} {
		if msg := CheckNewPassword("waiter", good); msg != "" {
			t.Errorf("%q refused: %s", good, msg)
		}
	}
}
