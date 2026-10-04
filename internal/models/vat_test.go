package models

import "testing"

func almostEqual(a, b float64) bool {
	const eps = 0.005
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < eps
}

func TestVATBreakdownSingleRate(t *testing.T) {
	o := Order{Items: []OrderItem{
		{Name: "Beer", Price: 5.0, Quantity: 2, VATRate: 24},
	}}
	lines := o.VATBreakdown()
	if len(lines) != 1 {
		t.Fatalf("expected 1 VAT line, got %d: %+v", len(lines), lines)
	}
	line := lines[0]
	if line.Rate != 24 {
		t.Errorf("Rate = %v, want 24", line.Rate)
	}
	// Gross is always exact — it's the guest-facing price, unaffected by
	// how net/VAT get split out of it.
	if !almostEqual(line.Gross, 10.0) {
		t.Errorf("Gross = %v, want 10.0", line.Gross)
	}
	if !almostEqual(line.Net+line.VAT, line.Gross) {
		t.Errorf("Net (%v) + VAT (%v) should equal Gross (%v)", line.Net, line.VAT, line.Gross)
	}
	// 10.0 gross at 24% VAT: net = 10/1.24 ≈ 8.06, vat ≈ 1.94
	if !almostEqual(line.Net, 8.0645) {
		t.Errorf("Net = %v, want ~8.0645", line.Net)
	}
}

func TestVATBreakdownGroupsMultipleRates(t *testing.T) {
	o := Order{Items: []OrderItem{
		{Name: "Cocktail", Price: 8.0, Quantity: 2, VATRate: 24}, // gross 16
		{Name: "Sandwich", Price: 14.0, Quantity: 1, VATRate: 13},
		{Name: "Beer", Price: 4.0, Quantity: 1, VATRate: 24}, // same rate as cocktail, should merge
	}}
	lines := o.VATBreakdown()
	if len(lines) != 2 {
		t.Fatalf("expected 2 VAT lines (24%% and 13%%), got %d: %+v", len(lines), lines)
	}
	// Sorted ascending by rate.
	if lines[0].Rate != 13 || lines[1].Rate != 24 {
		t.Errorf("lines not sorted by rate ascending: %+v", lines)
	}
	if !almostEqual(lines[1].Gross, 20.0) { // 16 (cocktail x2) + 4 (beer) = 20
		t.Errorf("24%% line Gross = %v, want 20.0 (cocktail line + beer line merged)", lines[1].Gross)
	}
	if !almostEqual(lines[0].Gross, 14.0) {
		t.Errorf("13%% line Gross = %v, want 14.0", lines[0].Gross)
	}
}

func TestVATBreakdownEmptyOrder(t *testing.T) {
	o := Order{}
	lines := o.VATBreakdown()
	if len(lines) != 0 {
		t.Errorf("expected no VAT lines for an order with no items, got %+v", lines)
	}
}

func TestReceiptNumberAndDemoMARKAreDeterministic(t *testing.T) {
	o := Order{ID: 42}
	if o.ReceiptNumber() != "000042" {
		t.Errorf("ReceiptNumber() = %q, want %q", o.ReceiptNumber(), "000042")
	}
	if first, again := o.ReceiptNumber(), o.ReceiptNumber(); first != again {
		t.Error("ReceiptNumber() should be deterministic for the same order")
	}
	mark1 := o.DemoMARK()
	mark2 := o.DemoMARK()
	if mark1 != mark2 {
		t.Error("DemoMARK() should be deterministic — reprinting the same order should show the same value")
	}
	other := Order{ID: 43}
	if o.DemoMARK() == other.DemoMARK() {
		t.Error("different orders should get different DemoMARK values")
	}
}
