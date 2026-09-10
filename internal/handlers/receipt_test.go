package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"bitebox/internal/db"
	"bitebox/internal/models"
)

func TestAdminOrderReceiptRendersDemoStructure(t *testing.T) {
	if _, err := db.CreateCategory("Test Receipt Kitchen", models.DepartmentKitchen); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if _, err := db.CreateCategory("Test Receipt Bar", models.DepartmentBar); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	foodID, err := db.CreateProduct("Test Receipt Food", 14.0, -1, "Test Receipt Kitchen", "", "", 13)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	drinkID, err := db.CreateProduct("Test Receipt Drink", 8.0, -1, "Test Receipt Bar", "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	if err := db.CreateTable(601); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	orderID, err := db.CreateOrder(601, "receipt-test-session", models.PaymentMethodCash, models.PaymentStatusPaid, "", 22.0, 1.0, 2,
		[]models.OrderItem{
			{ProductID: foodID, Name: "Test Receipt Food", Price: 14.0, Quantity: 1, VATRate: 13},
			{ProductID: drinkID, Name: "Test Receipt Drink", Price: 8.0, Quantity: 1, VATRate: 24},
		})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/admin/orders/"+strconv.Itoa(orderID)+"/receipt", nil)
	r = withChiURLParam(r, "id", strconv.Itoa(orderID))
	w := httptest.NewRecorder()
	AdminOrderReceipt(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	// The core "this is not real" guarantee — must survive any future
	// edit to this template without a test catching its removal.
	if !strings.Contains(body, "NOT A REAL FISCAL RECEIPT") {
		t.Error("receipt page is missing its DEMO/not-real banner")
	}
	if !strings.Contains(body, "myDATA") {
		t.Error("receipt page should explicitly disclaim any myDATA connection")
	}

	// Structural content actually present.
	if !strings.Contains(body, "Test Receipt Food") || !strings.Contains(body, "Test Receipt Drink") {
		t.Error("receipt page is missing one or both order items")
	}
	if !strings.Contains(body, "13%") || !strings.Contains(body, "24%") {
		t.Error("receipt page should show a VAT breakdown line for each rate present on the order")
	}

	order, err := db.GetOrderByID(orderID)
	if err != nil {
		t.Fatalf("GetOrderByID: %v", err)
	}
	if !strings.Contains(body, order.ReceiptNumber()) {
		t.Errorf("receipt page missing its own receipt number %q", order.ReceiptNumber())
	}
	if !strings.Contains(body, order.DemoMARK()) {
		t.Errorf("receipt page missing its own demo MARK %q", order.DemoMARK())
	}
}

func TestAdminOrderReceiptRejectsUnknownOrder(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/admin/orders/999999999/receipt", nil)
	r = withChiURLParam(r, "id", "999999999")
	w := httptest.NewRecorder()
	AdminOrderReceipt(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a nonexistent order", w.Code)
	}
}
