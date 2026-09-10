package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bitebox/internal/db"
	"bitebox/internal/models"
)

// withPushTestUser attaches the given user to a request's context the same
// way RequireRole's middleware does — push_test.go lives in this package
// specifically so it can reach userContextKey directly, without standing
// up a full session/cookie round trip just to test the handler underneath.
func withPushTestUser(r *http.Request, user models.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userContextKey, user))
}

// A real browser never hands back an endpoint like this — used throughout
// as "the one real push service host these tests are allowed to talk to."
const testAllowedEndpoint = "https://fcm.googleapis.com/fcm/send/test-token"

func TestIsAllowedPushEndpoint(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"FCM", "https://fcm.googleapis.com/fcm/send/abc123", true},
		{"Mozilla", "https://updates.push.services.mozilla.com/wpush/v2/abc123", true},
		{"Apple", "https://web.push.apple.com/some-token", true},
		{"Windows subdomain", "https://sn1.notify.windows.com/w/?token=abc", true},
		{"http scheme rejected", "http://fcm.googleapis.com/fcm/send/abc123", false},
		{"unrelated host rejected", "https://attacker.example.com/collect", false},
		{"lookalike suffix rejected", "https://evilfcm.googleapis.com.attacker.com/x", false},
		{"private IP rejected", "https://169.254.169.254/latest/meta-data/", false},
		{"malformed URL rejected", "not a url", false},
		{"empty string rejected", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isAllowedPushEndpoint(c.url); got != c.want {
				t.Errorf("isAllowedPushEndpoint(%q) = %v, want %v", c.url, got, c.want)
			}
		})
	}
}

func TestPushSubscribeRejectsInvalidBody(t *testing.T) {
	user, err := db.GetUserByUsername("bar")
	if err != nil {
		t.Fatalf("GetUserByUsername(bar): %v", err)
	}

	cases := []struct {
		name string
		body string
	}{
		{"empty body", ``},
		{"not JSON", `not json`},
		{"missing endpoint", `{"keys":{"p256dh":"p","auth":"a"}}`},
		{"missing keys", `{"endpoint":"` + testAllowedEndpoint + `"}`},
		// The SSRF fix: a syntactically fine subscription pointed at
		// anything other than a real push service host must still be
		// rejected — see isAllowedPushEndpoint's doc comment.
		{"disallowed endpoint host", `{"endpoint":"https://attacker.example.com/collect","keys":{"p256dh":"p","auth":"a"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := withPushTestUser(httptest.NewRequest(http.MethodPost, "/worker/push/subscribe", strings.NewReader(c.body)), user)
			w := httptest.NewRecorder()
			PushSubscribe(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestPushSubscribeRejectsUnauthenticatedRequest(t *testing.T) {
	body := `{"endpoint":"` + testAllowedEndpoint + `","keys":{"p256dh":"p","auth":"a"}}`
	req := httptest.NewRequest(http.MethodPost, "/worker/push/subscribe", strings.NewReader(body)) // no user in context
	w := httptest.NewRecorder()
	PushSubscribe(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestPushSubscribeThenUnsubscribeRoundTrip(t *testing.T) {
	user, err := db.GetUserByUsername("waiter")
	if err != nil {
		t.Fatalf("GetUserByUsername(waiter): %v", err)
	}
	const endpoint = testAllowedEndpoint + "-round-trip"
	body := `{"endpoint":"` + endpoint + `","keys":{"p256dh":"p256dh-val","auth":"auth-val"}}`

	req := withPushTestUser(httptest.NewRequest(http.MethodPost, "/worker/push/subscribe", strings.NewReader(body)), user)
	w := httptest.NewRecorder()
	PushSubscribe(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("subscribe status = %d, want %d", w.Code, http.StatusNoContent)
	}

	subs, err := db.GetPushSubscriptionsForDepartments([]string{models.DepartmentWaiter})
	if err != nil {
		t.Fatalf("GetPushSubscriptionsForDepartments: %v", err)
	}
	found := false
	for _, s := range subs {
		if s.Endpoint == endpoint {
			found = true
		}
	}
	if !found {
		t.Fatal("subscribed endpoint not found in waiter department's subscriptions")
	}

	unsubReq := withPushTestUser(httptest.NewRequest(http.MethodPost, "/worker/push/unsubscribe", strings.NewReader(`{"endpoint":"`+endpoint+`"}`)), user)
	w2 := httptest.NewRecorder()
	PushUnsubscribe(w2, unsubReq)
	if w2.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe status = %d, want %d", w2.Code, http.StatusNoContent)
	}

	subs, err = db.GetPushSubscriptionsForDepartments([]string{models.DepartmentWaiter})
	if err != nil {
		t.Fatalf("GetPushSubscriptionsForDepartments (after unsubscribe): %v", err)
	}
	for _, s := range subs {
		if s.Endpoint == endpoint {
			t.Error("endpoint should be gone after unsubscribe")
		}
	}
}

// TestPushUnsubscribeCannotDeleteAnotherUsersSubscription is the
// authorization-gap fix: unsubscribing has to be scoped to the caller's
// own device, or any logged-in staff account could silently kill another
// department's phone alerts by supplying/guessing its endpoint.
func TestPushUnsubscribeCannotDeleteAnotherUsersSubscription(t *testing.T) {
	owner, err := db.GetUserByUsername("kitchen")
	if err != nil {
		t.Fatalf("GetUserByUsername(kitchen): %v", err)
	}
	other, err := db.GetUserByUsername("bar")
	if err != nil {
		t.Fatalf("GetUserByUsername(bar): %v", err)
	}
	const endpoint = testAllowedEndpoint + "-ownership"
	subReq := withPushTestUser(httptest.NewRequest(http.MethodPost, "/worker/push/subscribe",
		strings.NewReader(`{"endpoint":"`+endpoint+`","keys":{"p256dh":"p","auth":"a"}}`)), owner)
	subW := httptest.NewRecorder()
	PushSubscribe(subW, subReq)
	if subW.Code != http.StatusNoContent {
		t.Fatalf("subscribe status = %d, want %d", subW.Code, http.StatusNoContent)
	}

	// "other" tries to unsubscribe a device that isn't theirs.
	unsubReq := withPushTestUser(httptest.NewRequest(http.MethodPost, "/worker/push/unsubscribe",
		strings.NewReader(`{"endpoint":"`+endpoint+`"}`)), other)
	unsubW := httptest.NewRecorder()
	PushUnsubscribe(unsubW, unsubReq)
	if unsubW.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe (wrong owner) status = %d, want %d (this endpoint returns 204 either way — it's the DB row that must survive)", unsubW.Code, http.StatusNoContent)
	}

	subs, err := db.GetPushSubscriptionsForDepartments([]string{models.DepartmentKitchen})
	if err != nil {
		t.Fatalf("GetPushSubscriptionsForDepartments: %v", err)
	}
	found := false
	for _, s := range subs {
		if s.Endpoint == endpoint {
			found = true
		}
	}
	if !found {
		t.Fatal("another user's unsubscribe request deleted this subscription — should only affect the owner's own devices")
	}

	// The actual owner can still remove it.
	ownUnsubReq := withPushTestUser(httptest.NewRequest(http.MethodPost, "/worker/push/unsubscribe",
		strings.NewReader(`{"endpoint":"`+endpoint+`"}`)), owner)
	ownUnsubW := httptest.NewRecorder()
	PushUnsubscribe(ownUnsubW, ownUnsubReq)
	if ownUnsubW.Code != http.StatusNoContent {
		t.Fatalf("owner unsubscribe status = %d, want %d", ownUnsubW.Code, http.StatusNoContent)
	}
}

func TestSummarizeItemsForPush(t *testing.T) {
	items := []models.OrderItem{
		{Name: "Souvlaki", Quantity: 2},
		{Name: "Beer", Quantity: 1},
	}
	got := summarizeItemsForPush(items)
	want := "2× Souvlaki, 1× Beer"
	if got != want {
		t.Errorf("summarizeItemsForPush() = %q, want %q", got, want)
	}
}

func TestSummarizeItemsForPushEmpty(t *testing.T) {
	if got := summarizeItemsForPush(nil); got != "" {
		t.Errorf("summarizeItemsForPush(nil) = %q, want empty string", got)
	}
}

// ---- Guest-side push (GuestPushSubscribe/GuestPushUnsubscribe) ----

func withGuestSession(r *http.Request, sessionID string) *http.Request {
	r.AddCookie(&http.Cookie{Name: GuestSessionCookieName, Value: sessionID})
	return r
}

func TestGuestPushSubscribeRejectsInvalidBody(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty body", ``},
		{"not JSON", `not json`},
		{"missing endpoint", `{"keys":{"p256dh":"p","auth":"a"}}`},
		{"missing keys", `{"endpoint":"` + testAllowedEndpoint + `"}`},
		{"disallowed endpoint host", `{"endpoint":"https://attacker.example.com/collect","keys":{"p256dh":"p","auth":"a"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := withGuestSession(httptest.NewRequest(http.MethodPost, "/table/1/push/subscribe", strings.NewReader(c.body)), "guest-push-invalid-body")
			w := httptest.NewRecorder()
			GuestPushSubscribe(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestGuestPushSubscribeRejectsRequestWithoutSessionCookie(t *testing.T) {
	body := `{"endpoint":"` + testAllowedEndpoint + `","keys":{"p256dh":"p","auth":"a"}}`
	req := httptest.NewRequest(http.MethodPost, "/table/1/push/subscribe", strings.NewReader(body)) // no guest session cookie
	w := httptest.NewRecorder()
	GuestPushSubscribe(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestGuestPushSubscribeThenUnsubscribeRoundTrip(t *testing.T) {
	const sessionID = "guest-push-round-trip"
	const endpoint = testAllowedEndpoint + "-guest-round-trip"
	body := `{"endpoint":"` + endpoint + `","keys":{"p256dh":"p256dh-val","auth":"auth-val"}}`

	req := withGuestSession(httptest.NewRequest(http.MethodPost, "/table/1/push/subscribe", strings.NewReader(body)), sessionID)
	w := httptest.NewRecorder()
	GuestPushSubscribe(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("subscribe status = %d, want %d", w.Code, http.StatusNoContent)
	}

	subs, err := db.GetGuestPushSubscriptionsForSession(sessionID)
	if err != nil {
		t.Fatalf("GetGuestPushSubscriptionsForSession: %v", err)
	}
	found := false
	for _, s := range subs {
		if s.Endpoint == endpoint {
			found = true
		}
	}
	if !found {
		t.Fatal("subscribed endpoint not found in this session's subscriptions")
	}

	unsubReq := withGuestSession(httptest.NewRequest(http.MethodPost, "/table/1/push/unsubscribe", strings.NewReader(`{"endpoint":"`+endpoint+`"}`)), sessionID)
	w2 := httptest.NewRecorder()
	GuestPushUnsubscribe(w2, unsubReq)
	if w2.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe status = %d, want %d", w2.Code, http.StatusNoContent)
	}

	subs, err = db.GetGuestPushSubscriptionsForSession(sessionID)
	if err != nil {
		t.Fatalf("GetGuestPushSubscriptionsForSession (after unsubscribe): %v", err)
	}
	for _, s := range subs {
		if s.Endpoint == endpoint {
			t.Error("endpoint should be gone after unsubscribe")
		}
	}
}

// TestGuestPushUnsubscribeCannotDeleteAnotherSessionsSubscription mirrors
// TestPushUnsubscribeCannotDeleteAnotherUsersSubscription for the guest
// path: unsubscribing must be scoped to the caller's own session, or any
// guest could silently kill another table's order alerts by
// supplying/guessing its endpoint.
func TestGuestPushUnsubscribeCannotDeleteAnotherSessionsSubscription(t *testing.T) {
	const owner = "guest-push-ownership-owner"
	const other = "guest-push-ownership-other"
	const endpoint = testAllowedEndpoint + "-guest-ownership"

	subReq := withGuestSession(httptest.NewRequest(http.MethodPost, "/table/1/push/subscribe",
		strings.NewReader(`{"endpoint":"`+endpoint+`","keys":{"p256dh":"p","auth":"a"}}`)), owner)
	subW := httptest.NewRecorder()
	GuestPushSubscribe(subW, subReq)
	if subW.Code != http.StatusNoContent {
		t.Fatalf("subscribe status = %d, want %d", subW.Code, http.StatusNoContent)
	}

	// "other" tries to unsubscribe a device that isn't theirs.
	unsubReq := withGuestSession(httptest.NewRequest(http.MethodPost, "/table/1/push/unsubscribe",
		strings.NewReader(`{"endpoint":"`+endpoint+`"}`)), other)
	unsubW := httptest.NewRecorder()
	GuestPushUnsubscribe(unsubW, unsubReq)
	if unsubW.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe (wrong owner) status = %d, want %d (this endpoint returns 204 either way — it's the DB row that must survive)", unsubW.Code, http.StatusNoContent)
	}

	subs, err := db.GetGuestPushSubscriptionsForSession(owner)
	if err != nil {
		t.Fatalf("GetGuestPushSubscriptionsForSession: %v", err)
	}
	found := false
	for _, s := range subs {
		if s.Endpoint == endpoint {
			found = true
		}
	}
	if !found {
		t.Fatal("another session's unsubscribe request deleted this subscription — should only affect the owning session")
	}
}
