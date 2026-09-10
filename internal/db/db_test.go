package db

import (
	"os"
	"testing"
	"time"

	"bitebox/internal/auth"
	"bitebox/internal/models"
)

// TestMain runs every test in this package against a throwaway SQLite file
// in its own temp directory — never the real bitebox.db. InitDB always
// opens "./bitebox.db" relative to the process's working directory, so
// isolation here means actually chdir-ing into a scratch directory before
// calling it, not just picking a different filename.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bitebox-db-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	origWD, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	if err := os.Chdir(dir); err != nil {
		panic(err)
	}
	defer os.Chdir(origWD)

	InitDB()
	os.Exit(m.Run())
}

func TestSeedDefaultUsersCreatesOneAccountPerDepartment(t *testing.T) {
	admin, err := GetUserByUsername("admin")
	if err != nil {
		t.Fatalf("seeded admin account not found: %v", err)
	}
	if admin.Role != models.RoleAdmin {
		t.Errorf("seeded admin's role = %q, want %q", admin.Role, models.RoleAdmin)
	}
	if !auth.CheckPassword(admin.PasswordHash, "admin123") {
		t.Error("seeded admin password doesn't match the documented default (admin123) — README's Seed accounts table would be wrong")
	}
}

func TestCreateOrderPersistsTipAndSplitCount(t *testing.T) {
	if err := CreateTable(101); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	productID, err := CreateProduct("Test Beer", 4.0, -1, models.CategoryOther, "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	items := []models.OrderItem{{ProductID: productID, Name: "Test Beer", Price: 4.0, Quantity: 2}}
	orderID, err := CreateOrder(101, "test-session", models.PaymentMethodCash, models.PaymentStatusUnpaid, "", 8.0, 1.5, 3, items)
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	order, err := GetOrderByID(orderID)
	if err != nil {
		t.Fatalf("GetOrderByID: %v", err)
	}
	if order.TipAmount != 1.5 {
		t.Errorf("TipAmount = %v, want 1.5", order.TipAmount)
	}
	if order.SplitCount != 3 {
		t.Errorf("SplitCount = %d, want 3", order.SplitCount)
	}
	if order.GrandTotal() != 9.5 {
		t.Errorf("GrandTotal() = %v, want 9.5", order.GrandTotal())
	}
}

func TestReleaseIdleTablesOnlyReleasesStaleOnes(t *testing.T) {
	if err := CreateTable(201); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if err := CreateTable(202); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if err := ClaimTable(201, "stale-session"); err != nil {
		t.Fatalf("ClaimTable: %v", err)
	}
	if err := ClaimTable(202, "fresh-session"); err != nil {
		t.Fatalf("ClaimTable: %v", err)
	}
	// Backdate only table 201's activity — table 202 was just claimed
	// (last_activity_at = CURRENT_TIMESTAMP, i.e. now) and must survive.
	if _, err := DB.Exec("UPDATE tables SET last_activity_at = ? WHERE number = ?", time.Now().Add(-time.Hour), 201); err != nil {
		t.Fatalf("backdating table 201: %v", err)
	}

	released, err := ReleaseIdleTables(30 * time.Minute)
	if err != nil {
		t.Fatalf("ReleaseIdleTables: %v", err)
	}
	if len(released) != 1 || released[0].Number != 201 {
		t.Fatalf("expected only table 201 released, got %+v", released)
	}
	if released[0].HostSessionID != "stale-session" {
		t.Errorf("released table's HostSessionID = %q, want the pre-release value %q (needed to clear that session's cart)", released[0].HostSessionID, "stale-session")
	}

	table201, err := GetTable(201)
	if err != nil {
		t.Fatalf("GetTable(201): %v", err)
	}
	if table201.Status != "available" {
		t.Errorf("table 201 status = %q, want released to available", table201.Status)
	}

	table202, err := GetTable(202)
	if err != nil {
		t.Fatalf("GetTable(202): %v", err)
	}
	if table202.Status != "occupied" {
		t.Error("table 202 was active within the idle window and should NOT have been released")
	}
}

func TestOrderHasAnyCategory(t *testing.T) {
	if _, err := CreateCategory("Test Kitchen Cat", models.DepartmentKitchen); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if _, err := CreateCategory("Test Bar Cat", models.DepartmentBar); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	kitchenProductID, err := CreateProduct("Test Steak", 15.0, -1, "Test Kitchen Cat", "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	if err := CreateTable(301); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	items := []models.OrderItem{{ProductID: kitchenProductID, Name: "Test Steak", Price: 15.0, Quantity: 1}}
	orderID, err := CreateOrder(301, "test-session-2", models.PaymentMethodCash, models.PaymentStatusUnpaid, "", 15.0, 0, 1, items)
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	kitchenCats, _ := CategoryNamesForDepartment(models.DepartmentKitchen)
	hasKitchen, err := OrderHasAnyCategory(orderID, kitchenCats)
	if err != nil {
		t.Fatalf("OrderHasAnyCategory (kitchen): %v", err)
	}
	if !hasKitchen {
		t.Error("order contains a kitchen-category item but OrderHasAnyCategory(kitchen) said no — a kitchen worker's own order would get wrongly 403'd")
	}

	barCats, _ := CategoryNamesForDepartment(models.DepartmentBar)
	hasBar, err := OrderHasAnyCategory(orderID, barCats)
	if err != nil {
		t.Fatalf("OrderHasAnyCategory (bar): %v", err)
	}
	if hasBar {
		t.Error("order has no bar-category items but OrderHasAnyCategory(bar) said yes — this is the exact department-boundary gap the check exists to close")
	}
}

func TestSetAllowCashOrdersRoundTrip(t *testing.T) {
	// Default is on (see the settings.allow_cash_orders migration) — local
	// dev and every existing cash flow must keep working unless an admin
	// deliberately switches it off.
	settings, err := GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if !settings.AllowCashOrders {
		t.Fatal("AllowCashOrders should default to true")
	}

	if err := SetAllowCashOrders(false); err != nil {
		t.Fatalf("SetAllowCashOrders(false): %v", err)
	}
	settings, err = GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if settings.AllowCashOrders {
		t.Fatal("AllowCashOrders should be false after SetAllowCashOrders(false)")
	}

	if err := SetAllowCashOrders(true); err != nil {
		t.Fatalf("SetAllowCashOrders(true): %v", err)
	}
	settings, err = GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if !settings.AllowCashOrders {
		t.Fatal("AllowCashOrders should be true again after SetAllowCashOrders(true)")
	}
}

func TestSetBusinessDetailsRoundTrip(t *testing.T) {
	// Defaults are obviously-fake demo placeholders (see the migration) —
	// not blank, so a fresh install's receipt printout doesn't show empty
	// ΑΦΜ/ΔΟΥ fields, but never real either.
	settings, err := GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if settings.BusinessVATNumber == "" || settings.TaxOffice == "" {
		t.Error("BusinessVATNumber/TaxOffice should default to non-empty demo placeholders")
	}

	if err := SetBusinessDetails("EL999888777 (DEMO)", "Δ.Ο.Υ. Test (DEMO)"); err != nil {
		t.Fatalf("SetBusinessDetails: %v", err)
	}
	settings, err = GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if settings.BusinessVATNumber != "EL999888777 (DEMO)" {
		t.Errorf("BusinessVATNumber = %q, want %q", settings.BusinessVATNumber, "EL999888777 (DEMO)")
	}
	if settings.TaxOffice != "Δ.Ο.Υ. Test (DEMO)" {
		t.Errorf("TaxOffice = %q, want %q", settings.TaxOffice, "Δ.Ο.Υ. Test (DEMO)")
	}
}

func TestProductAndOrderItemVATRateRoundTrip(t *testing.T) {
	if _, err := CreateCategory("Test VAT Category", models.DepartmentKitchen); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	productID, err := CreateProduct("Test VAT Item", 10.0, -1, "Test VAT Category", "", "", 13)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	product, err := GetProductByID(productID)
	if err != nil {
		t.Fatalf("GetProductByID: %v", err)
	}
	if product.VATRate != 13 {
		t.Errorf("product.VATRate = %v, want 13", product.VATRate)
	}

	if err := CreateTable(501); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	orderID, err := CreateOrder(501, "vat-test-session", models.PaymentMethodCash, models.PaymentStatusUnpaid, "", 10.0, 0, 1,
		[]models.OrderItem{{ProductID: productID, Name: "Test VAT Item", Price: 10.0, Quantity: 1, VATRate: 13}})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	items, err := GetOrderItems(orderID)
	if err != nil {
		t.Fatalf("GetOrderItems: %v", err)
	}
	if len(items) != 1 || items[0].VATRate != 13 {
		t.Errorf("order item VATRate not persisted correctly: %+v", items)
	}
}

func TestAuditLogRoundTrip(t *testing.T) {
	if err := LogAudit("test-admin", "create", "test-target", "role=worker department=bar"); err != nil {
		t.Fatalf("LogAudit: %v", err)
	}
	entries, err := GetRecentAuditLog(10)
	if err != nil {
		t.Fatalf("GetRecentAuditLog: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one audit entry")
	}
	found := false
	for _, e := range entries {
		if e.Actor == "test-admin" && e.Action == "create" && e.Target == "test-target" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("logged audit entry not found in GetRecentAuditLog results: %+v", entries)
	}
}

func TestGetOrderHistoryPagination(t *testing.T) {
	if err := CreateTable(401); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := CreateOrder(401, "pagination-session", models.PaymentMethodCash, models.PaymentStatusUnpaid, "", 5.0, 0, 1, nil); err != nil {
			t.Fatalf("CreateOrder #%d: %v", i, err)
		}
	}

	firstPage, err := GetOrderHistory(2, 0)
	if err != nil {
		t.Fatalf("GetOrderHistory page 1: %v", err)
	}
	if len(firstPage) != 2 {
		t.Fatalf("expected page size 2, got %d", len(firstPage))
	}
	secondPage, err := GetOrderHistory(2, 2)
	if err != nil {
		t.Fatalf("GetOrderHistory page 2: %v", err)
	}
	if len(secondPage) != 2 {
		t.Fatalf("expected page size 2, got %d", len(secondPage))
	}
	if firstPage[0].ID == secondPage[0].ID || firstPage[1].ID == secondPage[0].ID {
		t.Error("second page overlaps the first — offset isn't being applied correctly")
	}
}
