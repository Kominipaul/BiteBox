package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
