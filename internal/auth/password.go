package auth

import (
	"strings"
)

// Password rules for every place a password is chosen (change password,
// new account, staff reset). OWASP ASVS 2.1: at least 8 characters, no
// composition rules, and refuse the obviously guessable ones.
const (
	MinPasswordLen = 8
	// bcrypt only uses the first 72 bytes, and x/crypto refuses longer.
	MaxPasswordBytes = 72
)

// commonPasswords are the first guesses of any credential-stuffing list.
var commonPasswords = map[string]bool{
	"password": true, "password1": true, "password123": true, "passw0rd": true,
	"12345678": true, "123456789": true, "1234567890": true, "87654321": true,
	"11111111": true, "00000000": true, "qwertyui": true, "qwerty123": true,
	"iloveyou": true, "abcd1234": true, "abc12345": true, "letmein1": true,
	"welcome1": true, "admin123": true, "changeme": true, "bitebox1": true,
}

// CheckNewPassword returns why password can't be used for username, or ""
// when it's fine.
func CheckNewPassword(username, password string) string {
	switch {
	case len([]rune(password)) < MinPasswordLen:
		return "Password must be at least 8 characters"
	case len(password) > MaxPasswordBytes:
		return "Password must be at most 72 characters"
	}
	p := strings.ToLower(password)
	if commonPasswords[p] || strings.Count(p, p[:1]) == len(p) || nameWithSuffix(p, strings.ToLower(username)) {
		return "That password is too easy to guess: pick something longer or less common"
	}
	return ""
}

// nameWithSuffix: the username plus a few characters or just digits
// ("waiter1", "admin2026") — the first thing anyone tries.
func nameWithSuffix(p, u string) bool {
	if u == "" || !strings.Contains(p, u) {
		return false
	}
	rest := strings.Replace(p, u, "", 1)
	return len(rest) < 4 || strings.Trim(rest, "0123456789!.-_ ") == ""
}
