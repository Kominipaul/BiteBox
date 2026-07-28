package handlers

import (
	"html/template"
	"net/http"
	"strconv"

	"bitebox/internal/cart"
	"bitebox/internal/db"
	"bitebox/internal/models"
)

// CartHandlers groups guest cart/checkout routes needing access to the
// shared in-memory cart store.
type CartHandlers struct {
	Cart *cart.Store
}

// resolveHostedTable authorizes a cart mutation by resolving which table (if
// any) the caller's guest session currently hosts — never trusting a
// client-supplied table number.
func resolveHostedTable(r *http.Request) (models.Table, string, error) {
	sessionCookie, err := r.Cookie(GuestSessionCookieName)
	if err != nil {
		return models.Table{}, "", err
	}
	table, err := db.GetTableByHostSession(sessionCookie.Value)
	return table, sessionCookie.Value, err
}

func renderCartSummary(w http.ResponseWriter, c models.Cart) {
	tmpl := template.Must(template.ParseFiles("templates/_cart_summary.html"))
	tmpl.Execute(w, map[string]interface{}{"Cart": c})
}

func (h *CartHandlers) Add(w http.ResponseWriter, r *http.Request) {
	_, sessionID, err := resolveHostedTable(r)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}

	productID, err := strconv.Atoi(r.FormValue("product_id"))
	if err != nil {
		http.Error(w, "Invalid product", http.StatusBadRequest)
		return
	}

	product, err := db.GetProductByID(productID)
	if err != nil || !product.IsAvailable {
		http.Error(w, "Product not available", http.StatusBadRequest)
		return
	}

	updated := h.Cart.Add(sessionID, product.ID, product.Name, product.Price)
	renderCartSummary(w, updated)
}

func (h *CartHandlers) Remove(w http.ResponseWriter, r *http.Request) {
	_, sessionID, err := resolveHostedTable(r)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}

	productID, err := strconv.Atoi(r.FormValue("product_id"))
	if err != nil {
		http.Error(w, "Invalid product", http.StatusBadRequest)
		return
	}

	updated := h.Cart.Remove(sessionID, productID)
	renderCartSummary(w, updated)
}

func (h *CartHandlers) Checkout(w http.ResponseWriter, r *http.Request) {
	table, sessionID, err := resolveHostedTable(r)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}

	guestCart := h.Cart.Get(sessionID)
	if len(guestCart.Items) == 0 {
		http.Error(w, "Your cart is empty", http.StatusBadRequest)
		return
	}

	_, err = db.CreateOrder(table.Number, sessionID, models.PaymentMethodCash, models.PaymentStatusUnpaid, guestCart.TotalAmount, guestCart.Items)
	if err != nil {
		http.Error(w, "Failed to place order, please try again", http.StatusInternalServerError)
		return
	}

	h.Cart.Clear(sessionID)

	w.Header().Set("HX-Redirect", "/table/"+strconv.Itoa(table.Number))
}
