package handlers

import (
	"net"
	"net/http"
	"sync"
	"time"

	"bitebox/internal/auth"
)

// SecurityHeadersMiddleware sets the handful of response headers that cost
// nothing and close off whole classes of attack a browser would otherwise
// happily allow: clickjacking (X-Frame-Options), MIME-sniffing a guest
// upload or product image into executable content (X-Content-Type-Options),
// leaking full URLs (with e.g. a session-bearing query string, if one ever
// gets added) to third-party referrers (Referrer-Policy), and a CSP that
// keeps every script/style/font/connection first-party except the two CDN
// dependencies the templates actually use (Google Fonts, water.css).
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	const csp = "default-src 'self'; " +
		"script-src 'self' 'unsafe-inline'; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://cdn.jsdelivr.net; " +
		"font-src 'self' https://fonts.gstatic.com; " +
		// https: (any host) because a product photo can be a plain URL an
		// admin points at wherever their photos already live (see
		// models.Product.ImageURL) — 'self' alone silently blocked every
		// one of those, which is why photos stopped rendering the moment
		// this CSP shipped: the <img> tag was there, the src was correct,
		// the browser just refused to load it and said nothing louder
		// than a broken-image icon. Images can't execute script, so
		// widening just this directive carries none of the risk a
		// broader script-src/connect-src relaxation would.
		"img-src 'self' data: https:; " +
		"connect-src 'self' ws: wss:; " +
		"frame-ancestors 'none'"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// CSRFProtectionMiddleware rejects cross-site state-changing requests by
// checking Origin (falling back to Sec-Fetch-Site) against the request's
// own Host. This is a second layer on top of the session cookies' own
// SameSite=Lax (which already blocks a cross-site <form> POST or fetch from
// carrying them) — for older browsers, or anything that doesn't honor
// SameSite, this still holds. A request with neither header (a same-origin
// navigation, or a non-browser client like curl) is allowed through rather
// than rejected, since absence of both isn't itself evidence of a
// cross-site request.
func CSRFProtectionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if origin := r.Header.Get("Origin"); origin != "" {
				scheme := "http"
				if auth.IsSecureRequest(r) {
					scheme = "https"
				}
				if origin != scheme+"://"+r.Host {
					http.Error(w, "403 Forbidden: cross-site request rejected", http.StatusForbidden)
					return
				}
			} else if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "403 Forbidden: cross-site request rejected", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ipRateLimiter is a small fixed-window counter keyed by client IP,
// guarded by its own mutex rather than reusing any DB/session machinery —
// this has to work even when whatever it's guarding against is an endpoint
// under active abuse, so it can't depend on anything that itself does a
// database round trip per check. Shared by the login and table-claim
// limiters below.
type ipRateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	limit   int
	buckets map[string]*ipRateBucket
}

type ipRateBucket struct {
	count       int
	windowStart time.Time
}

// newIPRateLimiter allows up to limit attempts per IP per window, and
// runs a background sweep to drop buckets nobody's touched in a while so
// this doesn't grow unboundedly on an internet-facing deploy fielding
// traffic from many distinct IPs.
func newIPRateLimiter(limit int, window time.Duration) *ipRateLimiter {
	l := &ipRateLimiter{
		window:  window,
		limit:   limit,
		buckets: make(map[string]*ipRateBucket),
	}
	go l.sweep()
	return l
}

func (l *ipRateLimiter) sweep() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-10 * l.window)
		l.mu.Lock()
		for ip, b := range l.buckets {
			if b.windowStart.Before(cutoff) {
				delete(l.buckets, ip)
			}
		}
		l.mu.Unlock()
	}
}

// Allow reports whether ip is still under its attempt limit for the current
// window, incrementing its count either way (a rejected attempt still
// counts, so a burst of failures can't reset its own window by never
// stopping).
func (l *ipRateLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok || now.Sub(b.windowStart) > l.window {
		b = &ipRateBucket{windowStart: now}
		l.buckets[ip] = b
	}
	b.count++
	return b.count <= l.limit
}

// clientIP takes the TCP peer address only — never an X-Forwarded-For or
// similar client-supplied header, which anyone the limiter is meant to
// stop can set to an arbitrary value and rate-limit somebody else's IP
// instead of their own. A deploy that sits behind a trusted reverse proxy
// and wants real client IPs here would need that proxy's specific header
// explicitly trusted, not a blanket read of a spoofable one.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginLimiter allows 10 attempts per IP per minute — enough headroom for a
// staff member fumbling a password a few times, tight enough to make
// credential-stuffing against the fixed set of seed accounts (or any real
// account) impractical over HTTP round trips.
var loginLimiter = newIPRateLimiter(10, time.Minute)

func LoginRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loginLimiter.Allow(clientIP(r)) {
			http.Error(w, "429 Too Many Requests: slow down and try again shortly", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// tableClaimLimiter throttles how many *new* tables one IP can claim as
// host in a window — mitigation for SEC-13 (sequential table numbers let
// anyone script through every /table/{n} and squat all of them before real
// guests scan in). 20 per 10 minutes is deliberately generous: on the
// primary deploy story (a Pi serving the venue's own LAN, per README) every
// guest phone gets its own IP, so this should never trip during ordinary
// service — but it does blunt a script racing through dozens of tables in
// seconds. It's a mitigation, not a fix: table numbers are still
// sequential and guessable, and a deploy where guests reach the app through
// one shared NAT'd IP (e.g. cloud-hosted, guests on venue WiFi routed out
// through one address) would see every guest at the venue sharing this
// same budget. Closing this properly means unguessable per-table tokens
// (see SEC-13 in SECURITY_AUDIT.md) — a bigger change since it means
// reprinting whatever's on the physical QR codes.
var tableClaimLimiter = newIPRateLimiter(20, 10*time.Minute)

func AllowTableClaim(r *http.Request) bool {
	return tableClaimLimiter.Allow(clientIP(r))
}

// cartActionLimiter bounds cart add/remove — previously fully unbounded
// (see "Maybe later" in TODO.md). 60/min per IP is generous for genuine
// rapid tapping through a menu (a guest customizing several items in a
// row) while still bounding a scripted hammer against a shared resource
// (stock reservations, DB writes fanned out to every connected menu socket).
var cartActionLimiter = newIPRateLimiter(60, time.Minute)

func AllowCartAction(r *http.Request) bool {
	return cartActionLimiter.Allow(clientIP(r))
}

// djRequestLimiter bounds DJ song requests — same TODO.md gap as cart
// actions. Tighter than cart (5/min): a real guest sends at most a
// handful of song requests in an evening, and each one is more expensive
// than a cart tap (a DB write plus two broadcasts).
var djRequestLimiter = newIPRateLimiter(5, time.Minute)

func AllowDJRequest(r *http.Request) bool {
	return djRequestLimiter.Allow(clientIP(r))
}

// guestPushLimiter bounds guest subscribe/unsubscribe calls — a real guest
// hits this at most twice a visit (enable, maybe disable); 20/min per IP
// still comfortably covers a shared-router venue full of tables doing it
// around the same time without leaving it open to a scripted hammer against
// the same DB write + endpoint-allowlist check the staff version guards.
var guestPushLimiter = newIPRateLimiter(20, time.Minute)

func AllowGuestPushAction(r *http.Request) bool {
	return guestPushLimiter.Allow(clientIP(r))
}
