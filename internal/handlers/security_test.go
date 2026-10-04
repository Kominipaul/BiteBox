package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"bitebox/internal/db"
)

func TestIPRateLimiterAllowsUpToLimitThenBlocks(t *testing.T) {
	l := newIPRateLimiter(3, time.Minute)
	ip := "203.0.113.9"

	for i := 1; i <= 3; i++ {
		if !l.Allow(ip) {
			t.Fatalf("request %d should have been allowed (limit is 3)", i)
		}
	}
	if l.Allow(ip) {
		t.Fatal("4th request should have been blocked")
	}
}

func TestIPRateLimiterTracksEachIPSeparately(t *testing.T) {
	l := newIPRateLimiter(1, time.Minute)
	if !l.Allow("203.0.113.1") {
		t.Fatal("first IP's first request should be allowed")
	}
	if !l.Allow("203.0.113.2") {
		t.Fatal("a different IP should have its own independent budget")
	}
	if l.Allow("203.0.113.1") {
		t.Fatal("first IP's second request should be blocked")
	}
}

func TestIPRateLimiterWindowResetsAfterExpiry(t *testing.T) {
	l := newIPRateLimiter(1, 50*time.Millisecond)
	ip := "203.0.113.5"
	if !l.Allow(ip) {
		t.Fatal("first request should be allowed")
	}
	if l.Allow(ip) {
		t.Fatal("second request within the window should be blocked")
	}
	time.Sleep(80 * time.Millisecond)
	if !l.Allow(ip) {
		t.Fatal("request after the window expired should be allowed again")
	}
}

func TestCSRFProtectionMiddlewareRejectsCrossSiteOrigin(t *testing.T) {
	handlerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handlerCalled = true })
	mw := CSRFProtectionMiddleware(inner)

	r := httptest.NewRequest(http.MethodPost, "http://bitebox.example/cart/add", nil)
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if handlerCalled {
		t.Error("inner handler ran despite a cross-site Origin")
	}
}

func TestCSRFProtectionMiddlewareAllowsSameOriginPost(t *testing.T) {
	handlerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handlerCalled = true })
	mw := CSRFProtectionMiddleware(inner)

	r := httptest.NewRequest(http.MethodPost, "http://bitebox.example/cart/add", nil)
	r.Header.Set("Origin", "http://bitebox.example")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !handlerCalled {
		t.Error("inner handler should have run for a same-origin request")
	}
}

func TestCSRFProtectionMiddlewareAllowsRequestsWithNoOriginHeader(t *testing.T) {
	// A same-origin top-level navigation, or a non-browser client (curl) —
	// absence of both Origin and Sec-Fetch-Site isn't itself evidence of a
	// cross-site request, so this must pass through, not be rejected.
	handlerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handlerCalled = true })
	mw := CSRFProtectionMiddleware(inner)

	r := httptest.NewRequest(http.MethodPost, "http://bitebox.example/cart/add", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusOK || !handlerCalled {
		t.Errorf("a request with neither Origin nor Sec-Fetch-Site should pass through; status=%d called=%v", w.Code, handlerCalled)
	}
}

func TestCSRFProtectionMiddlewareIgnoresGET(t *testing.T) {
	handlerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handlerCalled = true })
	mw := CSRFProtectionMiddleware(inner)

	r := httptest.NewRequest(http.MethodGet, "http://bitebox.example/table/1", nil)
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusOK || !handlerCalled {
		t.Error("GET requests aren't state-changing and should never be blocked by CSRF protection")
	}
}

func TestSecurityHeadersMiddlewareSetsExpectedHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := SecurityHeadersMiddleware(inner)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	for _, header := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Content-Security-Policy"} {
		if w.Header().Get(header) == "" {
			t.Errorf("expected %s to be set", header)
		}
	}
}

// The CSP runs no inline script (script-src 'self'), so any inline <script>,
// on*= attribute or hx-on in a template is dead code in the browser. Page
// scripts belong in static/js/, data in <script type="application/json">.
func TestNoInlineScriptsInTemplates(t *testing.T) {
	files, err := filepath.Glob("templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates found: %v", err)
	}
	script := regexp.MustCompile(`(?i)<script\b[^>]*>`)
	handler := regexp.MustCompile(`(?i)\son[a-z]+\s*=|\shx-on[:-]|javascript:`)
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, tag := range script.FindAllString(string(b), -1) {
			if !strings.Contains(tag, " src=") && !strings.Contains(tag, `type="application/json"`) {
				t.Errorf("%s: inline script %s", f, tag)
			}
		}
		if m := handler.FindString(string(b)); m != "" {
			t.Errorf("%s: inline handler %q", f, m)
		}
	}
}

func TestCSPBlocksInlineScriptAndBadHost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://venue.example:8080/", nil)
	csp := cspFor(r)
	if !strings.Contains(csp, "script-src 'self';") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("script-src not strict: %s", csp)
	}
	if !strings.Contains(csp, "wss://venue.example:8080") || strings.Contains(csp, " ws: ") {
		t.Errorf("connect-src should name only this host: %s", csp)
	}
	r.Host = "x; script-src *"
	if strings.Contains(cspFor(r), "script-src *") {
		t.Error("a hostile Host header got into the CSP")
	}
}

func TestClientIPBehindTrustedProxy(t *testing.T) {
	req := func(peer, xff string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = peer
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	// No trusted proxy: the header is ignored, always.
	if ip := clientIP(req("203.0.113.5:4000", "1.2.3.4")); ip != "203.0.113.5" {
		t.Errorf("untrusted peer: %q", ip)
	}
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1, ::1")
	if ip := clientIP(req("127.0.0.1:5000", "6.6.6.6, 198.51.100.7")); ip != "198.51.100.7" {
		t.Errorf("behind proxy: %q, want the address the proxy appended", ip)
	}
	if ip := clientIP(req("[::1]:5000", "198.51.100.8")); ip != "198.51.100.8" {
		t.Errorf("behind IPv6 proxy: %q", ip)
	}
	if ip := clientIP(req("203.0.113.5:4000", "1.2.3.4")); ip != "203.0.113.5" {
		t.Errorf("a client that isn't the proxy can't pick its own IP: %q", ip)
	}
	if ip := clientIP(req("127.0.0.1:5000", "")); ip != "127.0.0.1" {
		t.Errorf("proxy without the header: %q", ip)
	}
}

func TestCallWaiterIsRateLimited(t *testing.T) {
	if err := db.CreateTable(9851); err != nil {
		t.Fatal(err)
	}
	db.ClaimTable(9851, "spammer")
	codes := map[int]int{}
	for i := 0; i < 15; i++ {
		r := httptest.NewRequest(http.MethodPost, "/table/9851/assist", strings.NewReader("reason=help"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = "198.51.100.99:1234"
		r.AddCookie(&http.Cookie{Name: GuestSessionCookieName, Value: "spammer"})
		w := httptest.NewRecorder()
		RequestAssistance(w, r)
		codes[w.Code]++
	}
	if codes[http.StatusOK] != 10 || codes[http.StatusTooManyRequests] != 5 {
		t.Fatalf("got %v, want 10 OK then 429s", codes)
	}
}
