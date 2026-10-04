package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"bitebox/internal/db"
	"bitebox/internal/models"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// maxPushRequestBody caps the subscribe/unsubscribe JSON bodies — an
// endpoint URL plus two short base64 keys is well under 1KB in practice;
// this just stops an oversized body from being read into memory at all
// (same pattern photo.go's upload handler already uses).
const maxPushRequestBody = 4 << 10 // 4KB

// allowedPushEndpointHosts are the actual push services real browsers hand
// back from PushManager.subscribe() — Chrome/Edge/Samsung/Opera (FCM),
// Firefox (Mozilla's own), Safari (Apple's), and legacy pre-Chromium Edge
// (WNS, effectively extinct but harmless to keep). Add a browser here only
// if it's a push service host, not a guess.
//
// Validating against this list (rather than, say, blocking private/
// loopback IPs) is the actual fix for the vulnerability it exists to
// close: without it, PushSubscribe would save *any* attacker-supplied
// endpoint URL verbatim, and notifyDepartments later makes a real
// server-side HTTP POST straight to it — carrying live order/table
// details — every time an order fires. That's SSRF plus a built-in
// exfiltration channel, reachable by any logged-in staff account
// regardless of department. An IP-blocklist approach is also just weaker
// here: a hostname can resolve differently between check-time and
// send-time, where this list doesn't need to resolve anything at all.
var allowedPushEndpointHosts = []string{
	"fcm.googleapis.com",
	"updates.push.services.mozilla.com",
	"web.push.apple.com",
	"notify.windows.com",
}

// isAllowedPushEndpoint reports whether rawURL is a real push service
// endpoint: HTTPS, host either an exact match or a subdomain of one of
// allowedPushEndpointHosts (WNS uses per-channel subdomains).
func isAllowedPushEndpoint(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return false
	}
	host := u.Hostname()
	for _, allowed := range allowedPushEndpointHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

// pushVAPIDPublicKey/pushVAPIDPrivateKey are this server's Web Push
// identity — loaded (generating a fresh pair on first run) by InitPush,
// which main.go calls once at startup right after db.InitDB. A
// subscription is only ever valid for the public key it was created
// against, so these have to survive restarts — see db.GetVAPIDKeys.
var (
	pushVAPIDPublicKey  string
	pushVAPIDPrivateKey string
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// pushSubscriberContact identifies this server to push services (Google's
// FCM, Mozilla's, ...) in the VAPID JWT's "sub" claim, as the Web Push
// protocol requires — an operator/ops contact, not anything per-user.
// Override with a real contact via PUSH_CONTACT before relying on this in
// production: push services have been known to throttle or block a sender
// they have no way to reach about abuse.
//
// Deliberately a bare email address, NOT "mailto:"-prefixed — webpush-go's
// getVAPIDAuthorizationHeader only skips its own "mailto:" prefixing when
// the string already starts with "https:"; passing an already-prefixed
// "mailto:..." string here gets double-prefixed into "mailto:mailto:...",
// an invalid sub claim. Confirmed this exact bug live: every real push to
// Apple's web.push.apple.com was silently failing 403 "BadJwtToken" (never
// logged — notifyDepartments/notifyGuestSession only handle 404/410, not
// 403) until this was fixed; same "admin@example.com" value with the
// prefix stripped got 201s from Apple immediately. A PUSH_CONTACT override
// must follow the same rule: a bare email, or a full "https://..." URL —
// never "mailto:" prefixed.
var pushSubscriberContact = envOr("PUSH_CONTACT", "admin@example.com")

// InitPush loads this server's VAPID key pair, generating one on first run
// and persisting it so it survives restarts. Must run once at startup
// before any push subscribe/send — left blank (with a logged warning) if
// it fails, which just makes every push a silent no-op rather than a crash;
// the rest of the app has nothing to do with this feature.
func InitPush() {
	pub, priv, err := db.GetVAPIDKeys()
	if err != nil {
		log.Printf("push: failed to read VAPID keys, phone alerts disabled: %v", err)
		return
	}
	if pub == "" || priv == "" {
		priv, pub, err = webpush.GenerateVAPIDKeys() // yes, private first — see its doc comment
		if err != nil {
			log.Printf("push: failed to generate VAPID keys, phone alerts disabled: %v", err)
			return
		}
		if err := db.SetVAPIDKeys(pub, priv); err != nil {
			log.Printf("push: failed to persist VAPID keys, phone alerts disabled: %v", err)
			return
		}
		log.Println("push: generated a new VAPID key pair for this server")
	}
	pushVAPIDPublicKey = pub
	pushVAPIDPrivateKey = priv
}

// PushVAPIDPublicKey exposes the public key to templates — worker_app.html
// hands it to the browser's PushManager.subscribe call. Never the private key.
func PushVAPIDPublicKey() string { return pushVAPIDPublicKey }

type pushSubscriptionRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// PushSubscribe registers the calling device for real phone notifications,
// tied to whichever staff account is logged in on it — see
// db.GetPushSubscriptionsForDepartments for how that's later matched back
// to "which departments should this device hear from."
func PushSubscribe(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r)
	if !ok {
		http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPushRequestBody)
	var req pushSubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		http.Error(w, "Invalid push subscription", http.StatusBadRequest)
		return
	}
	if !isAllowedPushEndpoint(req.Endpoint) {
		// Not a real browser push service — either a malformed client, or
		// someone hand-crafting a request. See allowedPushEndpointHosts for
		// exactly what this blocks and why.
		http.Error(w, "Unsupported push endpoint", http.StatusBadRequest)
		return
	}
	if err := db.SavePushSubscription(user.ID, req.Endpoint, req.Keys.P256dh, req.Keys.Auth); err != nil {
		http.Error(w, "Failed to save subscription", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PushUnsubscribe removes one device's subscription (the dashboard's
// "turn off phone alerts" action) — scoped to the logged-in user's own
// subscriptions, so one staff account can't blind-delete another's device
// by guessing/supplying its endpoint.
func PushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r)
	if !ok {
		http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPushRequestBody)
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Endpoint == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	db.DeletePushSubscriptionForUser(user.ID, req.Endpoint)
	w.WriteHeader(http.StatusNoContent)
}

// pushPayload is the JSON body handed to the service worker's "push" event
// (see static/sw.js) — kept tiny since Web Push has a real size ceiling
// (4KB after encryption overhead) and a lock-screen notification only has
// room to show a title/body anyway.
type pushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag"`
}

// notifyDepartments sends a real phone push — a lock-screen-visible alert,
// on top of (not instead of) the dashboard's own in-page banner/chime — to
// every device subscribed for the given departments, PLUS admin/superworker
// regardless of department (see db.GetPushSubscriptionsForDepartments).
// Used for new-order and assistance alerts, where a manager wanting to know
// about either is the right default. See notifyExactDepartments for the one
// alert (ready-for-pickup) where that broadening is wrong instead.
func notifyDepartments(departments []string, payload pushPayload) {
	subs, err := db.GetPushSubscriptionsForDepartments(departments)
	if err != nil {
		return
	}
	sendPushToSubscriptions(subs, payload)
}

// notifyExactDepartments is notifyDepartments without the admin/superworker
// broadening — only a device whose account is literally in one of the given
// departments hears about it. Exists for pushReadyAlert: "ready for
// pickup" is the waiter's job to go act on, not something that should also
// buzz a manager's phone on top of the live dashboard they can already see
// it in.
func notifyExactDepartments(departments []string, payload pushPayload) {
	subs, err := db.GetPushSubscriptionsForExactDepartments(departments)
	if err != nil {
		return
	}
	sendPushToSubscriptions(subs, payload)
}

// sendPushToSubscriptions does the actual encrypted send to each
// subscription — shared by notifyDepartments and notifyExactDepartments,
// which differ only in *which* subscriptions they hand it. Best-effort per
// device: one failure never blocks another, and a dead/expired endpoint
// (404/410, the push service's own way of saying "this subscription
// doesn't exist anymore") is cleaned up instead of retried forever.
func sendPushToSubscriptions(subs []db.PushSubscription, payload pushPayload) {
	if pushVAPIDPublicKey == "" || pushVAPIDPrivateKey == "" {
		return // InitPush never got a key pair — nothing to send
	}
	if len(subs) == 0 {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	for _, sub := range subs {
		if !isAllowedPushEndpoint(sub.Endpoint) {
			// Shouldn't happen — PushSubscribe already gates this at write
			// time — but a row could predate that check, or the DB could
			// have been edited directly. Refuse to send and drop it rather
			// than trust it just because it's already in the table.
			log.Printf("push: dropping subscription with disallowed endpoint host")
			db.DeletePushSubscription(sub.Endpoint)
			continue
		}
		go func(sub db.PushSubscription) {
			resp, err := webpush.SendNotification(body, &webpush.Subscription{
				Endpoint: sub.Endpoint,
				Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
			}, &webpush.Options{
				Subscriber:      pushSubscriberContact,
				VAPIDPublicKey:  pushVAPIDPublicKey,
				VAPIDPrivateKey: pushVAPIDPrivateKey,
				TTL:             60,
				Urgency:         webpush.UrgencyHigh,
			})
			if err != nil {
				log.Printf("push: send failed: %v", err)
				return
			}
			defer resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
				// The push service's own way of saying "this subscription
				// doesn't exist anymore" — clean it up rather than retry
				// forever.
				db.DeletePushSubscription(sub.Endpoint)
			case resp.StatusCode >= 300:
				// Anything else non-2xx (403 BadJwtToken, 401 VAPID key
				// mismatch, 413 payload too large, ...) is a real failure
				// worth knowing about, not a dead subscription to silently
				// prune. Logging this is what actually would have caught
				// the "mailto:mailto:" VAPID sub-claim bug live instead of
				// needing a manual debug session — every real send to
				// Apple was 403ing and nothing here said so.
				respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
				log.Printf("push: send to %s rejected: %d %s", sub.Endpoint, resp.StatusCode, respBody)
			}
		}(sub)
	}
}

// summarizeItemsForPush renders an order's items as one line for a push
// notification's body — same "qty× name" shape as the in-page alert
// banner (see worker_app.html's summarizeOrderItems), just built
// server-side here since a push has no DOM to read it back out of.
func summarizeItemsForPush(items []models.OrderItem) string {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, fmt.Sprintf("%d× %s", it.Quantity, it.Name))
	}
	return strings.Join(parts, ", ")
}

// pushNewOrderAlert notifies whichever department(s) actually own this
// order's items — mirrors authorizeDepartmentMutation's own category check
// (worker.go), just answering "who should hear about it" instead of "who
// may act on it." A mixed order (both a kitchen and a bar item) reaches
// both feeds, same as it would in their live order-feed views.
func pushNewOrderAlert(orderID, tableNumber int, items []models.OrderItem) {
	var departments []string
	for _, dept := range []string{models.DepartmentKitchen, models.DepartmentBar} {
		cats, err := db.CategoryNamesForDepartment(dept)
		if err != nil {
			continue
		}
		if ok, err := db.OrderHasAnyCategory(orderID, cats); err == nil && ok {
			departments = append(departments, dept)
		}
	}
	if len(departments) == 0 {
		return
	}
	notifyDepartments(departments, pushPayload{
		Title: fmt.Sprintf("Table %02d — New order", tableNumber),
		Body:  summarizeItemsForPush(items),
		URL:   "/worker",
		Tag:   fmt.Sprintf("order-%d", orderID),
	})
}

// pushReadyAlert notifies the waiter department ONLY — the kitchen/bar →
// waiter handoff, on a phone this time instead of just the dashboard.
// Deliberately notifyExactDepartments, not notifyDepartments: a manager
// (admin/superworker) already sees every order in the live dashboard feed
// and doesn't need "go pick this up" buzzing their phone too — that's
// specifically the waiter's job, unlike a new order or a table calling for
// assistance, which a manager reasonably does want to hear about directly.
func pushReadyAlert(orderID, tableNumber int, items []models.OrderItem) {
	notifyExactDepartments([]string{models.DepartmentWaiter}, pushPayload{
		Title: fmt.Sprintf("Table %02d — Ready for pickup", tableNumber),
		Body:  summarizeItemsForPush(items),
		URL:   "/worker",
		Tag:   fmt.Sprintf("order-%d", orderID),
	})
}

// pushAssistanceAlert notifies the waiter department (plus admin/superworker,
// as always — see notifyDepartments) that a guest tapped "Call waiter" —
// same department target as pushReadyAlert, since attending to a table in
// person is the waiter's job either way. Tagged per table rather than per
// request: a guest mashing the button twice before staff responds re-alerts
// (renotify: true in sw.js) instead of stacking duplicate notifications for
// the same still-unacknowledged table.
//
// No body text: unlike the order alerts (where the item list is real,
// non-obvious information), "a guest tapped Call waiter" adds nothing the
// title doesn't already say — a lock-screen glance should end at the
// title, not make someone read a second line to learn nothing new.
func pushAssistanceAlert(tableNumber int, reason string) {
	notifyDepartments([]string{models.DepartmentWaiter}, pushPayload{
		Title: fmt.Sprintf("Table %02d — Needs assistance", tableNumber),
		Body:  models.AssistReasonLabel(reason),
		URL:   "/worker",
		Tag:   fmt.Sprintf("assist-table-%d", tableNumber),
	})
}

// pushOrderConfirmedAlert notifies the guest who just placed an order —
// called right after pushNewOrderAlert notifies staff of the same order,
// from the same Checkout handler. Tagged by order id like the staff alerts
// are, so a guest who placed two orders gets two distinct lock-screen
// notifications rather than the second silently replacing the first.
func pushOrderConfirmedAlert(sessionID string, orderID, tableNumber int, items []models.OrderItem) {
	notifyGuestSession(sessionID, pushPayload{
		Title: "Order confirmed",
		Body:  summarizeItemsForPush(items),
		URL:   fmt.Sprintf("/table/%d", tableNumber),
		Tag:   fmt.Sprintf("guest-order-%d", orderID),
	})
}

// pushOrderReadyAlert notifies the guest that their order is ready — the
// customer-facing half of the same status flip that fires pushReadyAlert to
// the waiter department. Same tag as pushOrderConfirmedAlert's for this
// order: the "ready" push replaces the still-on-screen "confirmed" one for
// the same order instead of stacking a second notification for it.
func pushOrderReadyAlert(sessionID string, orderID, tableNumber int, items []models.OrderItem) {
	notifyGuestSession(sessionID, pushPayload{
		Title: "Your order is ready! 🎉",
		Body:  summarizeItemsForPush(items),
		URL:   fmt.Sprintf("/table/%d", tableNumber),
		Tag:   fmt.Sprintf("guest-order-%d", orderID),
	})
}

// ---- Guest-side push (the customer's own phone, not staff's) ----
//
// Same VAPID identity, same webpush-go call, same 404/410 cleanup — the
// only real difference from the staff path above is *who* a subscription
// belongs to: a guest is never an authenticated user, so these key off the
// same anonymous bitebox_session cookie every other guest action already
// keys off (see GuestSessionCookieName / resolveHostedTable in cart.go)
// instead of a user_id. See db.GuestPushSubscription /
// guest_push_subscriptions for the storage side.

type guestPushSubscriptionRequest = pushSubscriptionRequest // identical wire shape

// GuestPushSubscribe registers the calling device for real phone
// notifications about *this table's* orders — tied to whichever guest
// session cookie the request carries, resolved the same
// never-trust-a-client-supplied-table-number way every other guest mutation
// is (see RequestAssistance). No table number is stored: a subscription is
// looked up by session_id, and pushOrderConfirmedAlert/pushOrderReadyAlert
// below are always called with the session_id an order was actually placed
// under, not a client-supplied one.
func GuestPushSubscribe(w http.ResponseWriter, r *http.Request) {
	if !AllowGuestPushAction(r) {
		http.Error(w, "Too many requests — please try again shortly", http.StatusTooManyRequests)
		return
	}
	sessionCookie, err := r.Cookie(GuestSessionCookieName)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPushRequestBody)
	var req guestPushSubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		http.Error(w, "Invalid push subscription", http.StatusBadRequest)
		return
	}
	if !isAllowedPushEndpoint(req.Endpoint) {
		// See allowedPushEndpointHosts — same SSRF/exfiltration concern
		// applies here: notifyGuestSession below makes a real server-side
		// POST straight to whatever endpoint gets stored.
		http.Error(w, "Unsupported push endpoint", http.StatusBadRequest)
		return
	}
	if err := db.SaveGuestPushSubscription(sessionCookie.Value, req.Endpoint, req.Keys.P256dh, req.Keys.Auth); err != nil {
		http.Error(w, "Failed to save subscription", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GuestPushUnsubscribe removes one device's subscription — the guest-facing
// "turn off order alerts" action, scoped to the caller's own session so one
// guest can't blind-delete another table's subscription by guessing its
// endpoint.
func GuestPushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if !AllowGuestPushAction(r) {
		http.Error(w, "Too many requests — please try again shortly", http.StatusTooManyRequests)
		return
	}
	sessionCookie, err := r.Cookie(GuestSessionCookieName)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPushRequestBody)
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Endpoint == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	db.DeleteGuestPushSubscriptionForSession(sessionCookie.Value, req.Endpoint)
	w.WriteHeader(http.StatusNoContent)
}

// notifyGuestSession sends a real phone push to every device the given
// guest session has subscribed — mirrors notifyDepartments' best-effort,
// dead-endpoint-cleans-itself-up behavior exactly, just against the guest
// table instead of the staff one.
func notifyGuestSession(sessionID string, payload pushPayload) {
	if pushVAPIDPublicKey == "" || pushVAPIDPrivateKey == "" || sessionID == "" {
		return
	}
	subs, err := db.GetGuestPushSubscriptionsForSession(sessionID)
	if err != nil || len(subs) == 0 {
		notifyGuestFallback(sessionID, payload)
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	for _, sub := range subs {
		if !isAllowedPushEndpoint(sub.Endpoint) {
			log.Printf("push: dropping guest subscription with disallowed endpoint host")
			db.DeleteGuestPushSubscription(sub.Endpoint)
			continue
		}
		go func(sub db.GuestPushSubscription) {
			resp, err := webpush.SendNotification(body, &webpush.Subscription{
				Endpoint: sub.Endpoint,
				Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
			}, &webpush.Options{
				Subscriber:      pushSubscriberContact,
				VAPIDPublicKey:  pushVAPIDPublicKey,
				VAPIDPrivateKey: pushVAPIDPrivateKey,
				TTL:             60,
				Urgency:         webpush.UrgencyHigh,
			})
			if err != nil {
				log.Printf("push: guest send failed: %v", err)
				return
			}
			defer resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
				db.DeleteGuestPushSubscription(sub.Endpoint)
			case resp.StatusCode >= 300:
				respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
				log.Printf("push: guest send to %s rejected: %d %s", sub.Endpoint, resp.StatusCode, respBody)
			}
		}(sub)
	}
}

// notifyGuestFallback is the hook for a guest who never installed the PWA
// (iOS Safari tab, declined the permission prompt, etc.) and so has no push
// subscription for this session at all — the moment above where an SMS or
// email fallback would actually fire.
//
// TODO: no SMS/email provider is wired up in this app. To add one: collect
// a phone number or email at checkout (optional field, cart.go's Checkout),
// persist it on the order, and send it here — this is deliberately left as
// a no-op rather than guessed at, since it implies a real third-party
// account (Twilio, an email provider) and a decision about what guest data
// to collect that hasn't been made. Signature intentionally matches
// notifyGuestSession's own payload so wiring one in later is a one-line
// call, not a redesign.
func notifyGuestFallback(sessionID string, payload pushPayload) {}
