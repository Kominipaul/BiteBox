package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"bitebox/internal/auth"
	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
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

// TestProductEditor: the Menu & stock editor panel opens, the list's quick
// stock button nudges stock, and saving from the panel closes it.
func TestProductEditor(t *testing.T) {
	id, err := db.CreateProduct("Editor Test Latte", 3.5, 4, models.CategoryOther, "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	ctx := func(r *http.Request) *http.Request {
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", strconv.Itoa(id))
		return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
	}

	w := httptest.NewRecorder()
	AdminProductEditor(w, ctx(httptest.NewRequest(http.MethodGet, "/admin/products/x/edit", nil)))
	if !strings.Contains(w.Body.String(), "Editor Test Latte") || !strings.Contains(w.Body.String(), "Customisation") {
		t.Fatalf("editor didn't render the item")
	}

	r := httptest.NewRequest(http.MethodPost, "/admin/products/x/stock", strings.NewReader("delta=1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	AdminProductStock(httptest.NewRecorder(), ctx(r))
	if p, _ := db.GetProductByID(id); p.Stock != 5 {
		t.Fatalf("stock after +1 = %d, want 5", p.Stock)
	}

	form := "name=Editor+Test+Latte&price=4&category=" + url.QueryEscape(models.CategoryOther) + "&vat_rate=24&stock=5"
	r = httptest.NewRequest(http.MethodPost, "/admin/products/x?from=editor", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	AdminUpdateProduct(w, ctx(r))
	if w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("HX-Trigger"), "productSaved") {
		t.Fatalf("save from editor: code %d trigger %q body %s", w.Code, w.Header().Get("HX-Trigger"), w.Body)
	}
	if p, _ := db.GetProductByID(id); p.Price != 4 {
		t.Fatalf("price not saved: %v", p.Price)
	}
}

// TestOrdersPage: an order shows up under today's unpaid filter, its panel
// lists the items, and "Mark paid" from the panel sticks.
func TestOrdersPage(t *testing.T) {
	db.CreateTable(9701)
	pid, err := db.CreateProduct("Orders Test Freddo", 3.5, -1, models.CategoryOther, "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	items := []models.OrderItem{{ProductID: pid, Name: "Orders Test Freddo", Price: 3.5, Quantity: 2}}
	id, err := db.CreateOrder(9701, "orders-test", models.PaymentMethodCash, models.PaymentStatusUnpaid, "", 7, 1, 1, items)
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	w := httptest.NewRecorder()
	AdminOrderRows(w, httptest.NewRequest(http.MethodGet, "/admin/orders/rows?when=today&pay=unpaid&q=freddo", nil))
	if !strings.Contains(w.Body.String(), "#"+strconv.Itoa(id)) {
		t.Fatalf("order %d missing from today's unpaid list", id)
	}
	ctx := func(r *http.Request) *http.Request {
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", strconv.Itoa(id))
		return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/orders/x/payment", strings.NewReader("paid=1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	AdminOrderPayment(w, ctx(r))
	if !strings.Contains(w.Body.String(), "Orders Test Freddo") || !strings.Contains(w.Body.String(), "Mark unpaid") {
		t.Fatalf("panel after paying should list the item and offer Mark unpaid")
	}
	if o, _ := db.GetOrderByID(id); o.PaymentStatus != models.PaymentStatusPaid {
		t.Fatalf("payment status = %q", o.PaymentStatus)
	}
}

// TestStaffEditor: add an account from the panel, open it, reset its
// password and deactivate it, all answering the panel.
func TestStaffEditor(t *testing.T) {
	admin := models.User{ID: 1, Username: "admin", Role: models.RoleAdmin}
	as := func(r *http.Request, id string) *http.Request {
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", id)
		ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rc)
		return r.WithContext(context.WithValue(ctx, userContextKey, admin))
	}
	post := func(h http.HandlerFunc, url, body, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h(w, as(r, id))
		return w
	}
	if w := post(AdminCreateStaff, "/admin/staff?from=editor", "username=editor_tess&password=secret99&role=bar", ""); w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("HX-Trigger"), "staffSaved") {
		t.Fatalf("create: %d %q %s", w.Code, w.Header().Get("HX-Trigger"), w.Body)
	}
	u, err := db.GetUserByUsername("editor_tess")
	if err != nil || u.Department != models.DepartmentBar {
		t.Fatalf("created account: %+v %v", u, err)
	}
	id := strconv.Itoa(u.ID)
	w := httptest.NewRecorder()
	AdminStaffEdit(w, as(httptest.NewRequest(http.MethodGet, "/admin/staff/x/edit", nil), id))
	if !strings.Contains(w.Body.String(), "editor_tess") || !strings.Contains(w.Body.String(), "Deactivate") {
		t.Fatalf("edit panel missing account or Deactivate")
	}
	if w := post(AdminResetStaffPassword, "/admin/staff/x/reset-password?from=editor", "password=Newpass-77", id); !strings.Contains(w.Body.String(), "then picks their own") {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	if w := post(AdminDeactivateStaff, "/admin/staff/x/deactivate?from=editor", "", id); !strings.Contains(w.Body.String(), "Activate again") {
		t.Fatalf("deactivate: %d %s", w.Code, w.Body)
	}
	if u, _ := db.GetUserByID(u.ID); u.IsActive {
		t.Fatal("account still active")
	}
}

// TestStaffAccess: custom page access set from the panel is stored, enforced
// by RequirePage, and shows up in the account's history.
func TestStaffAccess(t *testing.T) {
	admin := models.User{ID: 1, Username: "admin", Role: models.RoleAdmin}
	hash, _ := auth.HashPassword("secret99")
	id, err := db.CreateUser("access_anna", hash, models.RoleWorker, models.DepartmentWaiter)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", strconv.Itoa(id))
	r := httptest.NewRequest(http.MethodPost, "/admin/staff/x?from=editor",
		strings.NewReader("username=access_anna&department=waiter&access_form=1&access_custom=1&pages=menu&pages=orders&pages=settings"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(context.WithValue(context.WithValue(r.Context(), chi.RouteCtxKey, rc), userContextKey, admin))
	AdminUpdateStaff(httptest.NewRecorder(), r)

	u, _ := db.GetUserByID(id)
	if !u.PagesCustom || !u.Can("menu") || !u.Can("orders") || u.Can("bookings") || u.Can("settings") {
		t.Fatalf("access not saved right: custom=%v pages=%v", u.PagesCustom, u.Pages)
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	for page, want := range map[string]int{"menu": 200, "bookings": 403} {
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		req = req.WithContext(context.WithValue(req.Context(), userContextKey, u))
		w := httptest.NewRecorder()
		RequirePage(page)(ok).ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("RequirePage(%s) = %d, want %d", page, w.Code, want)
		}
	}
	found := false
	for _, h := range staffHistory("access_anna") {
		found = found || strings.Contains(h.Text, "Page access changed")
	}
	if !found {
		t.Fatal("access change missing from history")
	}
}

// TestStaffListRenders guards the list template itself (a template error
// only shows up when it's executed).
func TestStaffListRenders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/admin/staff", nil)
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, models.User{ID: 1, Username: "admin", Role: models.RoleAdmin}))
	w := httptest.NewRecorder()
	AdminStaffList(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Sign out everywhere") {
		t.Fatalf("staff list: %d %.200s", w.Code, w.Body)
	}
}
