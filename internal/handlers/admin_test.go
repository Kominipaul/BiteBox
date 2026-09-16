package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"bitebox/internal/db"
	"bitebox/internal/models"
)

func TestAdminDeleteProductRemovesProductAndIngredients(t *testing.T) {
	if _, err := db.CreateCategory("Test Delete Category", "kitchen"); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	id, err := db.CreateProduct("Test Delete Item", 5.0, -1, "Test Delete Category", "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	if _, err := db.CreateIngredient(id, "Onion", models.IngredientRemovable, 0); err != nil {
		t.Fatalf("CreateIngredient: %v", err)
	}

	r := httptest.NewRequest(http.MethodDelete, "/admin/products/"+strconv.Itoa(id), nil)
	r = withChiURLParam(r, "id", strconv.Itoa(id))
	w := httptest.NewRecorder()
	AdminDeleteProduct(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	if _, err := db.GetProductByID(id); err == nil {
		t.Error("product should no longer exist after delete")
	}
	byProduct, err := db.GetIngredientsByProductIDs([]int{id})
	if err != nil {
		t.Fatalf("GetIngredientsByProductIDs: %v", err)
	}
	if len(byProduct[id]) != 0 {
		t.Errorf("ingredient tags should have been deleted alongside the product, found %d", len(byProduct[id]))
	}
}

func TestAdminDeleteProductRemovesPhotoFiles(t *testing.T) {
	if _, err := db.CreateCategory("Test Delete Photo Category", "kitchen"); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	body, contentType := buildMultipartProductForm(t, map[string]string{
		"name": "Test Delete Photo Item", "price": "6.00", "category": "Test Delete Photo Category",
	}, testJPEG(t, 300, 200))
	r1 := httptest.NewRequest(http.MethodPost, "/admin/products", body)
	r1.Header.Set("Content-Type", contentType)
	w1 := httptest.NewRecorder()
	AdminCreateProduct(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", w1.Code, w1.Body.String())
	}

	products, _ := db.GetAllProducts()
	var id int
	var fullURL string
	for _, p := range products {
		if p.Name == "Test Delete Photo Item" {
			id, fullURL = p.ID, p.ImageURL
		}
	}
	if fullURL == "" {
		t.Fatal("setup: product should have an uploaded photo")
	}
	fullPath := fullURL[1:]
	thumbPath := strings.TrimSuffix(fullPath, fullSuffix) + thumbSuffix

	r2 := httptest.NewRequest(http.MethodDelete, "/admin/products/"+strconv.Itoa(id), nil)
	r2 = withChiURLParam(r2, "id", strconv.Itoa(id))
	w2 := httptest.NewRecorder()
	AdminDeleteProduct(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", w2.Code, w2.Body.String())
	}

	if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
		t.Errorf("full photo variant should have been deleted, stat err = %v", err)
	}
	if _, err := os.Stat(thumbPath); !os.IsNotExist(err) {
		t.Errorf("thumb photo variant should have been deleted, stat err = %v", err)
	}
}

// TestAdminDeleteProductPreservesOrderHistory guards the reason
// DeleteProduct is safe to offer at all: order_items snapshots name/price
// at order time and carries no foreign key back to products, so deleting a
// discontinued item must never touch — or break the display of — an order
// that included it in the past.
func TestAdminDeleteProductPreservesOrderHistory(t *testing.T) {
	if _, err := db.CreateCategory("Test Delete History Category", "kitchen"); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	id, err := db.CreateProduct("Test Delete History Item", 9.5, -1, "Test Delete History Category", "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	orderID, err := db.CreateOrder(1, "test-session-delete-history", models.PaymentMethodCash, "unpaid", "", 9.5, 0, 1, []models.OrderItem{
		{ProductID: id, Name: "Test Delete History Item", Price: 9.5, Quantity: 1, VATRate: 24},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	// Deletion is blocked while an order's still in progress (see
	// ProductHasActiveOrders) — mark it served first, the realistic
	// "safe to delete now" state this test actually means to exercise.
	if err := db.UpdateOrderStatus(orderID, models.OrderStatusServed); err != nil {
		t.Fatalf("UpdateOrderStatus: %v", err)
	}

	r := httptest.NewRequest(http.MethodDelete, "/admin/products/"+strconv.Itoa(id), nil)
	r = withChiURLParam(r, "id", strconv.Itoa(id))
	w := httptest.NewRecorder()
	AdminDeleteProduct(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	history, err := db.GetOrderHistory(200, 0)
	if err != nil {
		t.Fatalf("GetOrderHistory: %v", err)
	}
	found := false
	for _, o := range history {
		if o.ID != orderID {
			continue
		}
		found = true
		if len(o.Items) != 1 || o.Items[0].Name != "Test Delete History Item" || o.Items[0].Price != 9.5 {
			t.Errorf("order %d's item snapshot changed after deleting the product it referenced: %+v", orderID, o.Items)
		}
	}
	if !found {
		t.Fatalf("order %d not found in history after deleting a product it referenced", orderID)
	}
}

// TestAdminDeleteProductBlockedWhileOrderActive is the actual regression
// test for ProductHasActiveOrders: without this guard, deleting a product
// mid-order wouldn't just orphan a reference — GetActiveOrdersByCategories
// (the bar/kitchen live feed) INNER JOINs order_items to products to route
// by category, so the whole order would silently vanish from the kitchen/
// bar feed before it was ever prepared.
func TestAdminDeleteProductBlockedWhileOrderActive(t *testing.T) {
	if _, err := db.CreateCategory("Test Delete Active Category", "kitchen"); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	id, err := db.CreateProduct("Test Delete Active Item", 7.0, -1, "Test Delete Active Category", "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	// CreateOrder always starts an order at "pending" — still needs the
	// kitchen/bar's attention, the exact case this guard exists for.
	if _, err := db.CreateOrder(1, "test-session-delete-active", models.PaymentMethodCash, "unpaid", "", 7.0, 0, 1, []models.OrderItem{
		{ProductID: id, Name: "Test Delete Active Item", Price: 7.0, Quantity: 1, VATRate: 24},
	}); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	r := httptest.NewRequest(http.MethodDelete, "/admin/products/"+strconv.Itoa(id), nil)
	r = withChiURLParam(r, "id", strconv.Itoa(id))
	w := httptest.NewRecorder()
	AdminDeleteProduct(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s, want 409 while an active order still references this product", w.Code, w.Body.String())
	}
	if _, err := db.GetProductByID(id); err != nil {
		t.Error("product should still exist — the delete should have been rejected, not partially applied")
	}
}

func TestAdminDeleteProductReturns404ForUnknownID(t *testing.T) {
	r := httptest.NewRequest(http.MethodDelete, "/admin/products/999999999", nil)
	r = withChiURLParam(r, "id", "999999999")
	w := httptest.NewRecorder()
	AdminDeleteProduct(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a nonexistent product id", w.Code)
	}
}

func TestAdminDeleteProductRejectsNonNumericID(t *testing.T) {
	r := httptest.NewRequest(http.MethodDelete, "/admin/products/not-a-number", nil)
	r = withChiURLParam(r, "id", "not-a-number")
	w := httptest.NewRecorder()
	AdminDeleteProduct(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a non-numeric id", w.Code)
	}
}
