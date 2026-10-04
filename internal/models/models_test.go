package models

import "testing"

func TestOrderGrandTotalIncludesTip(t *testing.T) {
	o := Order{TotalAmount: 20.0, TipAmount: 3.5}
	if got := o.GrandTotal(); got != 23.5 {
		t.Errorf("GrandTotal() = %v, want 23.5", got)
	}
}

func TestOrderPerPersonTotal(t *testing.T) {
	cases := []struct {
		name       string
		order      Order
		wantPerson float64
	}{
		{"no split (0, treated as 1)", Order{TotalAmount: 40, SplitCount: 0}, 40},
		{"no split (1)", Order{TotalAmount: 40, SplitCount: 1}, 40},
		{"split 4 ways", Order{TotalAmount: 40, SplitCount: 4}, 10},
		{"split 4 ways with a tip", Order{TotalAmount: 40, TipAmount: 8, SplitCount: 4}, 12},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.order.PerPersonTotal(); got != c.wantPerson {
				t.Errorf("PerPersonTotal() = %v, want %v", got, c.wantPerson)
			}
		})
	}
}

func TestNextOrderStatusProgression(t *testing.T) {
	cases := []struct {
		current  string
		wantNext string
		wantOK   bool
	}{
		{OrderStatusPending, OrderStatusPreparing, true},
		{OrderStatusPreparing, OrderStatusReady, true},
		{OrderStatusReady, OrderStatusServed, true},
		{OrderStatusServed, OrderStatusCompleted, true},
		{OrderStatusCompleted, "", false}, // terminal
		{OrderStatusCancelled, "", false}, // not part of the forward flow at all
		{"bogus-status", "", false},
	}
	for _, c := range cases {
		next, ok := NextOrderStatus(c.current)
		if next != c.wantNext || ok != c.wantOK {
			t.Errorf("NextOrderStatus(%q) = (%q, %v), want (%q, %v)", c.current, next, ok, c.wantNext, c.wantOK)
		}
	}
}

func TestIsValidOrderStatus(t *testing.T) {
	if !IsValidOrderStatus(OrderStatusReady) {
		t.Error("OrderStatusReady should be a valid status")
	}
	if IsValidOrderStatus(OrderStatusCancelled) {
		t.Error("OrderStatusCancelled is deliberately not part of OrderStatusFlow — IsValidOrderStatus should reject it (only reachable via the dedicated cancel endpoint, never the generic status-update one)")
	}
	if IsValidOrderStatus("not-a-real-status") {
		t.Error("garbage input should not be a valid status")
	}
}

func TestIsValidDepartment(t *testing.T) {
	for _, d := range Departments {
		if !IsValidDepartment(d) {
			t.Errorf("%q is listed in Departments but IsValidDepartment rejected it", d)
		}
	}
	if IsValidDepartment("front-desk") {
		t.Error("IsValidDepartment accepted a made-up department")
	}
}

func TestIsValidIngredientKind(t *testing.T) {
	if !IsValidIngredientKind(IngredientRemovable) || !IsValidIngredientKind(IngredientExtra) {
		t.Error("both real ingredient kinds should be valid")
	}
	if IsValidIngredientKind("garnish") {
		t.Error("an unrecognized ingredient kind should not be valid")
	}
}

func TestUserCan(t *testing.T) {
	waiter := User{Role: RoleWorker, Department: DepartmentWaiter}
	if !waiter.Can("bookings") || !waiter.Can("floor") || waiter.Can("menu") {
		t.Fatal("waiter defaults should be bookings + floor only")
	}
	custom := User{Role: RoleWorker, Department: DepartmentWaiter, PagesCustom: true, Pages: []string{"menu", "staff"}}
	if !custom.Can("menu") || custom.Can("bookings") {
		t.Fatal("custom pages should replace the defaults")
	}
	if custom.Can("staff") || custom.Can("settings") {
		t.Fatal("staff/settings are admin-only, never grantable")
	}
	if !(User{Role: RoleAdmin}).Can("settings") {
		t.Fatal("admins can open everything")
	}
}
