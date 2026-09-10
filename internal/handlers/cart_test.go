package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"bitebox/internal/cart"
	"bitebox/internal/db"
	"bitebox/internal/models"
)

func TestParseTip(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		want   float64
		wantOK bool
	}{
		{"blank means no tip", "", 0, true},
		{"a normal amount", "2.50", 2.5, true},
		{"zero is fine", "0", 0, true},
		{"negative is rejected", "-1", 0, false},
		{"NaN is rejected", "NaN", 0, false},
		{"+Inf is rejected", "+Inf", 0, false},
		{"over the cap is rejected", "1000000", 0, false},
		{"garbage is rejected", "not-a-number", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			form := url.Values{"tip": {c.raw}}
			r := httptest.NewRequest(http.MethodPost, "/cart/checkout", nil)
			r.Form = form
			got, ok := parseTip(r)
			if ok != c.wantOK {
				t.Fatalf("parseTip(%q) ok = %v, want %v", c.raw, ok, c.wantOK)
			}
			if ok && got != c.want {
				t.Errorf("parseTip(%q) = %v, want %v", c.raw, got, c.want)
			}
		})
	}
}

func TestParseSplitCount(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		want   int
		wantOK bool
	}{
		{"blank means no split", "", 1, true},
		{"a normal count", "4", 4, true},
		{"zero is rejected", "0", 0, false},
		{"negative is rejected", "-2", 0, false},
		{"over the cap is rejected", "500", 0, false},
		{"garbage is rejected", "many", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			form := url.Values{"split": {c.raw}}
			r := httptest.NewRequest(http.MethodPost, "/cart/checkout", nil)
			r.Form = form
			got, ok := parseSplitCount(r)
			if ok != c.wantOK {
				t.Fatalf("parseSplitCount(%q) ok = %v, want %v", c.raw, ok, c.wantOK)
			}
			if ok && got != c.want {
				t.Errorf("parseSplitCount(%q) = %v, want %v", c.raw, got, c.want)
			}
		})
	}
}

func TestResolvePaymentMethod(t *testing.T) {
	cases := []struct {
		name            string
		rawMethod       string
		allowCashOrders bool
		wantMethod      string
		wantOK          bool
	}{
		{"explicit cash, cash allowed", "cash", true, models.PaymentMethodCash, true},
		{"explicit card, cash allowed", "card", true, models.PaymentMethodCard, true},
		{"blank, cash allowed — defaults to cash (pre-existing behavior)", "", true, models.PaymentMethodCash, true},
		{"garbage, cash allowed — defaults to cash", "bogus", true, models.PaymentMethodCash, true},

		{"explicit cash, cash disabled — rejected", "cash", false, "", false},
		{"explicit card, cash disabled — fine", "card", false, models.PaymentMethodCard, true},
		{"blank, cash disabled — falls back to card, not rejected", "", false, models.PaymentMethodCard, true},
		{"garbage, cash disabled — falls back to card", "bogus", false, models.PaymentMethodCard, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/cart/checkout", nil)
			r.Form = url.Values{"payment_method": {c.rawMethod}}
			method, ok := resolvePaymentMethod(r, c.allowCashOrders)
			if ok != c.wantOK {
				t.Fatalf("resolvePaymentMethod(%q, %v) ok = %v, want %v", c.rawMethod, c.allowCashOrders, ok, c.wantOK)
			}
			if ok && method != c.wantMethod {
				t.Errorf("resolvePaymentMethod(%q, %v) method = %q, want %q", c.rawMethod, c.allowCashOrders, method, c.wantMethod)
			}
		})
	}
}

// TestCheckoutRejectsCashWhenDisabled exercises the full handler, not just
// resolvePaymentMethod in isolation — confirms the settings lookup and
// rejection are actually wired together correctly, and that re-enabling
// cash immediately lets the same request through.
func TestCheckoutRejectsCashWhenDisabled(t *testing.T) {
	if _, err := db.CreateCategory("Test Checkout Category", "kitchen"); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	productID, err := db.CreateProduct("Test Checkout Item", 7.0, -1, "Test Checkout Category", "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	const tableNum = 9201
	if err := db.CreateTable(tableNum); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if err := db.ClaimTable(tableNum, "checkout-test-session"); err != nil {
		t.Fatalf("ClaimTable: %v", err)
	}

	store := cart.NewStore()
	store.Add("checkout-test-session", productID, "Test Checkout Item", 7.0, nil, nil)
	h := &CartHandlers{Cart: store}

	if err := db.SetAllowCashOrders(false); err != nil {
		t.Fatalf("SetAllowCashOrders(false): %v", err)
	}
	defer db.SetAllowCashOrders(true) // restore the default for any test that runs after this one

	r := httptest.NewRequest(http.MethodPost, "/cart/checkout", strings.NewReader("payment_method=cash"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: GuestSessionCookieName, Value: "checkout-test-session"})
	w := httptest.NewRecorder()
	h.Checkout(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s, want 403 (cash disabled)", w.Code, w.Body.String())
	}

	if err := db.SetAllowCashOrders(true); err != nil {
		t.Fatalf("SetAllowCashOrders(true): %v", err)
	}
	r2 := httptest.NewRequest(http.MethodPost, "/cart/checkout", strings.NewReader("payment_method=cash"))
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r2.AddCookie(&http.Cookie{Name: GuestSessionCookieName, Value: "checkout-test-session"})
	w2 := httptest.NewRecorder()
	h.Checkout(w2, r2)

	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200 once cash is re-enabled", w2.Code, w2.Body.String())
	}

	order, err := db.GetOrderByID(mustLatestOrderID(t, tableNum))
	if err != nil {
		t.Fatalf("GetOrderByID: %v", err)
	}
	if order.PaymentMethod != models.PaymentMethodCash {
		t.Errorf("PaymentMethod = %q, want %q", order.PaymentMethod, models.PaymentMethodCash)
	}
}

func mustLatestOrderID(t *testing.T, tableNum int) int {
	t.Helper()
	orders, err := db.GetOrderHistory(1, 0)
	if err != nil || len(orders) == 0 {
		t.Fatalf("GetOrderHistory: %v (orders=%v)", err, orders)
	}
	if orders[0].TableNumber != tableNum {
		t.Fatalf("most recent order is for table %d, want %d", orders[0].TableNumber, tableNum)
	}
	return orders[0].ID
}
