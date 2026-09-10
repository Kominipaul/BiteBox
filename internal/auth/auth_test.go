package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "correct-horse-battery-staple" {
		t.Fatal("HashPassword returned the plaintext unchanged")
	}
	if !CheckPassword(hash, "correct-horse-battery-staple") {
		t.Fatal("CheckPassword rejected the correct password")
	}
	if CheckPassword(hash, "wrong-password") {
		t.Fatal("CheckPassword accepted an incorrect password")
	}
	if CheckPassword(hash, "") {
		t.Fatal("CheckPassword accepted an empty password")
	}
}

func TestGenerateSessionIDIsUniqueAndOpaque(t *testing.T) {
	a := GenerateSessionID()
	b := GenerateSessionID()
	if a == b {
		t.Fatal("GenerateSessionID produced the same value twice in a row")
	}
	if len(a) != 32 { // 16 bytes, hex-encoded
		t.Fatalf("expected a 32-char hex session id, got %d chars: %q", len(a), a)
	}
}

func TestIsSecureRequest(t *testing.T) {
	cases := []struct {
		name  string
		setup func(r *http.Request)
		want  bool
	}{
		{"plain HTTP, no proxy header", func(r *http.Request) {}, false},
		{"behind a TLS-terminating proxy", func(r *http.Request) {
			r.Header.Set("X-Forwarded-Proto", "https")
		}, true},
		{"proxy header is case-insensitive", func(r *http.Request) {
			r.Header.Set("X-Forwarded-Proto", "HTTPS")
		}, true},
		{"proxy header says http", func(r *http.Request) {
			r.Header.Set("X-Forwarded-Proto", "http")
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			c.setup(r)
			if got := IsSecureRequest(r); got != c.want {
				t.Errorf("IsSecureRequest() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestSetSessionCookieSecureFlagFollowsRequestScheme(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	SetSessionCookie(w, r, "some-session-id")
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected exactly one cookie, got %d", len(cookies))
	}
	if cookies[0].Secure {
		t.Error("Secure flag set on a plain-HTTP request — would silently break login on a non-TLS LAN deploy")
	}
	if !cookies[0].HttpOnly {
		t.Error("HttpOnly not set — session cookie readable from JS")
	}

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.Header.Set("X-Forwarded-Proto", "https")
	SetSessionCookie(w2, r2, "some-session-id")
	if !w2.Result().Cookies()[0].Secure {
		t.Error("Secure flag not set behind a TLS-terminating proxy — SEC-01 regression")
	}
}
