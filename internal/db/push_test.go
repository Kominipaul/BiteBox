package db

import (
	"testing"

	"bitebox/internal/models"
)

// mustCreatePushTestUser creates a throwaway staff account for these tests
// rather than reusing/mutating the shared seeded accounts (admin/waiter/
// bar/kitchen/manager) other tests in this package depend on staying
// exactly as seeded.
func mustCreatePushTestUser(t *testing.T, username, department string) int {
	t.Helper()
	id, err := CreateUser(username, "hash", models.RoleWorker, department)
	if err != nil {
		t.Fatalf("CreateUser(%q): %v", username, err)
	}
	return id
}

func TestSavePushSubscriptionUpsertsOnEndpoint(t *testing.T) {
	userID := mustCreatePushTestUser(t, "push_upsert_test", models.DepartmentBar)
	const endpoint = "https://push.example.com/upsert-test"

	if err := SavePushSubscription(userID, endpoint, "p256dh-old", "auth-old"); err != nil {
		t.Fatalf("SavePushSubscription (initial): %v", err)
	}
	if err := SavePushSubscription(userID, endpoint, "p256dh-new", "auth-new"); err != nil {
		t.Fatalf("SavePushSubscription (re-subscribe): %v", err)
	}

	subs, err := GetPushSubscriptionsForDepartments([]string{models.DepartmentBar})
	if err != nil {
		t.Fatalf("GetPushSubscriptionsForDepartments: %v", err)
	}
	var found int
	for _, s := range subs {
		if s.Endpoint == endpoint {
			found++
			if s.P256dh != "p256dh-new" || s.Auth != "auth-new" {
				t.Errorf("subscription keys = (%q, %q), want the refreshed (p256dh-new, auth-new)", s.P256dh, s.Auth)
			}
		}
	}
	if found != 1 {
		t.Errorf("found %d rows for endpoint %q, want exactly 1 (re-subscribing should update, not duplicate)", found, endpoint)
	}
}

func TestGetPushSubscriptionsForDepartmentsFiltersByDepartment(t *testing.T) {
	barUser := mustCreatePushTestUser(t, "push_dept_bar", models.DepartmentBar)
	kitchenUser := mustCreatePushTestUser(t, "push_dept_kitchen", models.DepartmentKitchen)

	barEndpoint := "https://push.example.com/dept-bar"
	kitchenEndpoint := "https://push.example.com/dept-kitchen"
	if err := SavePushSubscription(barUser, barEndpoint, "p", "a"); err != nil {
		t.Fatalf("SavePushSubscription (bar): %v", err)
	}
	if err := SavePushSubscription(kitchenUser, kitchenEndpoint, "p", "a"); err != nil {
		t.Fatalf("SavePushSubscription (kitchen): %v", err)
	}

	subs, err := GetPushSubscriptionsForDepartments([]string{models.DepartmentBar})
	if err != nil {
		t.Fatalf("GetPushSubscriptionsForDepartments: %v", err)
	}
	var sawBar, sawKitchen bool
	for _, s := range subs {
		if s.Endpoint == barEndpoint {
			sawBar = true
		}
		if s.Endpoint == kitchenEndpoint {
			sawKitchen = true
		}
	}
	if !sawBar {
		t.Error("querying [bar] should include the bar department's own subscription")
	}
	if sawKitchen {
		t.Error("querying [bar] should NOT include the kitchen department's subscription")
	}
}

// TestGetPushSubscriptionsForDepartmentsAlwaysIncludesAdminAndSuperworker
// mirrors the order feed's own "admin/superworker see everything" rule
// (see handlers.orderFeedBuckets) — a manager shouldn't have to be in the
// exact department a push was scoped to in order to still hear about it.
func TestGetPushSubscriptionsForDepartmentsAlwaysIncludesAdminAndSuperworker(t *testing.T) {
	adminUser := mustCreatePushTestUser(t, "push_admin_test", models.DepartmentSuperworker)
	// Role, not department, makes this one an admin.
	if _, err := DB.Exec("UPDATE users SET role = ? WHERE id = ?", models.RoleAdmin, adminUser); err != nil {
		t.Fatalf("promoting test user to admin: %v", err)
	}
	managerUser := mustCreatePushTestUser(t, "push_manager_test", models.DepartmentSuperworker)

	adminEndpoint := "https://push.example.com/admin-test"
	managerEndpoint := "https://push.example.com/manager-test"
	if err := SavePushSubscription(adminUser, adminEndpoint, "p", "a"); err != nil {
		t.Fatalf("SavePushSubscription (admin): %v", err)
	}
	if err := SavePushSubscription(managerUser, managerEndpoint, "p", "a"); err != nil {
		t.Fatalf("SavePushSubscription (manager): %v", err)
	}

	// Neither device is in "kitchen" — both should still come back.
	subs, err := GetPushSubscriptionsForDepartments([]string{models.DepartmentKitchen})
	if err != nil {
		t.Fatalf("GetPushSubscriptionsForDepartments: %v", err)
	}
	var sawAdmin, sawManager bool
	for _, s := range subs {
		if s.Endpoint == adminEndpoint {
			sawAdmin = true
		}
		if s.Endpoint == managerEndpoint {
			sawManager = true
		}
	}
	if !sawAdmin {
		t.Error("an admin's device should be notified regardless of the requested department")
	}
	if !sawManager {
		t.Error("a superworker-department device should be notified regardless of the requested department")
	}
}

func TestGetPushSubscriptionsForDepartmentsExcludesDeactivatedUsers(t *testing.T) {
	userID := mustCreatePushTestUser(t, "push_deactivated_test", models.DepartmentWaiter)
	endpoint := "https://push.example.com/deactivated-test"
	if err := SavePushSubscription(userID, endpoint, "p", "a"); err != nil {
		t.Fatalf("SavePushSubscription: %v", err)
	}
	if err := SetUserActive(userID, false); err != nil {
		t.Fatalf("SetUserActive(false): %v", err)
	}

	subs, err := GetPushSubscriptionsForDepartments([]string{models.DepartmentWaiter})
	if err != nil {
		t.Fatalf("GetPushSubscriptionsForDepartments: %v", err)
	}
	for _, s := range subs {
		if s.Endpoint == endpoint {
			t.Error("a deactivated account's device should not receive push notifications")
		}
	}
}

// TestGetPushSubscriptionsForExactDepartmentsExcludesAdminAndSuperworker is
// the inverse of TestGetPushSubscriptionsForDepartmentsAlwaysIncludesAdminAndSuperworker
// above — this is the query pushReadyAlert uses specifically because a
// manager doesn't need "go pick this up" alerts on top of the live
// dashboard they can already see it in.
func TestGetPushSubscriptionsForExactDepartmentsExcludesAdminAndSuperworker(t *testing.T) {
	waiterUser := mustCreatePushTestUser(t, "exact_dept_waiter", models.DepartmentWaiter)
	adminUser := mustCreatePushTestUser(t, "exact_dept_admin", models.DepartmentSuperworker)
	if _, err := DB.Exec("UPDATE users SET role = ? WHERE id = ?", models.RoleAdmin, adminUser); err != nil {
		t.Fatalf("promoting test user to admin: %v", err)
	}
	managerUser := mustCreatePushTestUser(t, "exact_dept_manager", models.DepartmentSuperworker)

	waiterEndpoint := "https://push.example.com/exact-dept-waiter"
	adminEndpoint := "https://push.example.com/exact-dept-admin"
	managerEndpoint := "https://push.example.com/exact-dept-manager"
	if err := SavePushSubscription(waiterUser, waiterEndpoint, "p", "a"); err != nil {
		t.Fatalf("SavePushSubscription (waiter): %v", err)
	}
	if err := SavePushSubscription(adminUser, adminEndpoint, "p", "a"); err != nil {
		t.Fatalf("SavePushSubscription (admin): %v", err)
	}
	if err := SavePushSubscription(managerUser, managerEndpoint, "p", "a"); err != nil {
		t.Fatalf("SavePushSubscription (manager): %v", err)
	}

	subs, err := GetPushSubscriptionsForExactDepartments([]string{models.DepartmentWaiter})
	if err != nil {
		t.Fatalf("GetPushSubscriptionsForExactDepartments: %v", err)
	}
	var sawWaiter, sawAdmin, sawManager bool
	for _, s := range subs {
		switch s.Endpoint {
		case waiterEndpoint:
			sawWaiter = true
		case adminEndpoint:
			sawAdmin = true
		case managerEndpoint:
			sawManager = true
		}
	}
	if !sawWaiter {
		t.Error("querying [waiter] should include the waiter's own subscription")
	}
	if sawAdmin {
		t.Error("GetPushSubscriptionsForExactDepartments should NOT include admin regardless of department — that's the whole point vs. GetPushSubscriptionsForDepartments")
	}
	if sawManager {
		t.Error("GetPushSubscriptionsForExactDepartments should NOT include a superworker-department device not literally in the requested department")
	}
}

func TestDeletePushSubscriptionRemovesRow(t *testing.T) {
	userID := mustCreatePushTestUser(t, "push_delete_test", models.DepartmentBar)
	endpoint := "https://push.example.com/delete-test"
	if err := SavePushSubscription(userID, endpoint, "p", "a"); err != nil {
		t.Fatalf("SavePushSubscription: %v", err)
	}
	if err := DeletePushSubscription(endpoint); err != nil {
		t.Fatalf("DeletePushSubscription: %v", err)
	}

	subs, err := GetPushSubscriptionsForDepartments([]string{models.DepartmentBar})
	if err != nil {
		t.Fatalf("GetPushSubscriptionsForDepartments: %v", err)
	}
	for _, s := range subs {
		if s.Endpoint == endpoint {
			t.Error("subscription should be gone after DeletePushSubscription")
		}
	}
}

func TestGetPushSubscriptionsForDepartmentsEmptyInput(t *testing.T) {
	subs, err := GetPushSubscriptionsForDepartments(nil)
	if err != nil {
		t.Fatalf("GetPushSubscriptionsForDepartments(nil): %v", err)
	}
	if subs != nil {
		t.Errorf("GetPushSubscriptionsForDepartments(nil) = %v, want nil", subs)
	}
}

func TestSaveGuestPushSubscriptionUpsertsOnEndpoint(t *testing.T) {
	const sessionID = "guest_push_upsert_test"
	const endpoint = "https://push.example.com/guest-upsert-test"

	if err := SaveGuestPushSubscription(sessionID, endpoint, "p256dh-old", "auth-old"); err != nil {
		t.Fatalf("SaveGuestPushSubscription (initial): %v", err)
	}
	if err := SaveGuestPushSubscription(sessionID, endpoint, "p256dh-new", "auth-new"); err != nil {
		t.Fatalf("SaveGuestPushSubscription (re-subscribe): %v", err)
	}

	subs, err := GetGuestPushSubscriptionsForSession(sessionID)
	if err != nil {
		t.Fatalf("GetGuestPushSubscriptionsForSession: %v", err)
	}
	var found int
	for _, s := range subs {
		if s.Endpoint == endpoint {
			found++
			if s.P256dh != "p256dh-new" || s.Auth != "auth-new" {
				t.Errorf("subscription keys = (%q, %q), want the refreshed (p256dh-new, auth-new)", s.P256dh, s.Auth)
			}
		}
	}
	if found != 1 {
		t.Errorf("found %d rows for endpoint %q, want exactly 1 (re-subscribing should update, not duplicate)", found, endpoint)
	}
}

func TestGetGuestPushSubscriptionsForSessionFiltersBySession(t *testing.T) {
	const sessionA = "guest_push_session_a"
	const sessionB = "guest_push_session_b"
	endpointA := "https://push.example.com/guest-session-a"
	endpointB := "https://push.example.com/guest-session-b"
	if err := SaveGuestPushSubscription(sessionA, endpointA, "p", "a"); err != nil {
		t.Fatalf("SaveGuestPushSubscription (a): %v", err)
	}
	if err := SaveGuestPushSubscription(sessionB, endpointB, "p", "a"); err != nil {
		t.Fatalf("SaveGuestPushSubscription (b): %v", err)
	}

	subs, err := GetGuestPushSubscriptionsForSession(sessionA)
	if err != nil {
		t.Fatalf("GetGuestPushSubscriptionsForSession: %v", err)
	}
	var sawA, sawB bool
	for _, s := range subs {
		if s.Endpoint == endpointA {
			sawA = true
		}
		if s.Endpoint == endpointB {
			sawB = true
		}
	}
	if !sawA {
		t.Error("querying session A should include session A's own subscription")
	}
	if sawB {
		t.Error("querying session A should NOT include session B's subscription")
	}
}

func TestDeleteGuestPushSubscriptionForSessionScopedToOwner(t *testing.T) {
	const owner = "guest_push_delete_owner"
	const other = "guest_push_delete_other"
	const endpoint = "https://push.example.com/guest-delete-test"
	if err := SaveGuestPushSubscription(owner, endpoint, "p", "a"); err != nil {
		t.Fatalf("SaveGuestPushSubscription: %v", err)
	}

	// A different session can't delete it.
	if err := DeleteGuestPushSubscriptionForSession(other, endpoint); err != nil {
		t.Fatalf("DeleteGuestPushSubscriptionForSession (wrong owner): %v", err)
	}
	subs, err := GetGuestPushSubscriptionsForSession(owner)
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
		t.Fatal("another session's delete request removed this subscription — should only affect the owning session")
	}

	// The actual owner can.
	if err := DeleteGuestPushSubscriptionForSession(owner, endpoint); err != nil {
		t.Fatalf("DeleteGuestPushSubscriptionForSession (owner): %v", err)
	}
	subs, err = GetGuestPushSubscriptionsForSession(owner)
	if err != nil {
		t.Fatalf("GetGuestPushSubscriptionsForSession (after delete): %v", err)
	}
	for _, s := range subs {
		if s.Endpoint == endpoint {
			t.Error("subscription should be gone after the owning session deletes it")
		}
	}
}

func TestDeleteGuestPushSubscriptionRemovesRowUnconditionally(t *testing.T) {
	const sessionID = "guest_push_unconditional_delete"
	const endpoint = "https://push.example.com/guest-unconditional-delete"
	if err := SaveGuestPushSubscription(sessionID, endpoint, "p", "a"); err != nil {
		t.Fatalf("SaveGuestPushSubscription: %v", err)
	}
	if err := DeleteGuestPushSubscription(endpoint); err != nil {
		t.Fatalf("DeleteGuestPushSubscription: %v", err)
	}
	subs, err := GetGuestPushSubscriptionsForSession(sessionID)
	if err != nil {
		t.Fatalf("GetGuestPushSubscriptionsForSession: %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("GetGuestPushSubscriptionsForSession after unconditional delete = %v, want empty", subs)
	}
}
