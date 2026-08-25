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

// cartItemView re-derives a line item's current name/price/availability from
// the product catalog rather than trusting the add-time snapshot, so an
// admin price change or hide/stock-out is reflected the moment the cart
// re-renders (on the guest's own action, or nudged by BroadcastMenu).
type cartItemView struct {
	models.OrderItem
	Unavailable bool
}

// resolveCart returns per-item views plus a total computed only over items
// that are still purchasable (an unavailable item can't be checked out, so
// it doesn't count toward what the guest owes).
func resolveCart(c models.Cart) (views []cartItemView, total float64, hasUnavailable bool) {
	for _, item := range c.Items {
		v := cartItemView{OrderItem: item}
		product, err := db.GetProductByID(item.ProductID)
		if err != nil || !product.IsAvailable || (product.Stock != -1 && product.Stock < item.Quantity) {
			v.Unavailable = true
			hasUnavailable = true
		} else {
			v.Name = product.Name
			v.Price = product.Price
			total += v.Price * float64(v.Quantity)
		}
		views = append(views, v)
	}
	return views, total, hasUnavailable
}

func renderCartSummary(w http.ResponseWriter, c models.Cart) {
	views, total, hasUnavailable := resolveCart(c)
	tmpl := template.Must(template.ParseFiles("templates/_cart_summary.html"))
	tmpl.Execute(w, map[string]interface{}{
		"Items":          views,
		"Total":          total,
		"HasUnavailable": hasUnavailable,
	})
}

// Summary renders the caller's cart as-is (from their session cookie). It's
// the target of the cart-refresh nudge broadcast alongside every menu change
// (see menu.go's cartRefreshTrigger), so a guest's already-added items stay
// in sync with admin price/availability/stock edits without the server
// needing to track who has what in their cart.
func (h *CartHandlers) Summary(w http.ResponseWriter, r *http.Request) {
	sessionCookie, err := r.Cookie(GuestSessionCookieName)
	if err != nil {
		renderCartSummary(w, models.Cart{})
		return
	}
	renderCartSummary(w, h.Cart.Get(sessionCookie.Value))
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

	if product.Stock != -1 {
		current := h.Cart.Get(sessionID)
		var inCart int
		for _, item := range current.Items {
			if item.ProductID == product.ID {
				inCart = item.Quantity
				break
			}
		}
		if inCart+1 > product.Stock {
			http.Error(w, "Not enough stock left", http.StatusConflict)
			return
		}
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

	views, total, hasUnavailable := resolveCart(guestCart)
	if hasUnavailable {
		http.Error(w, "Some items in your cart are no longer available — please remove them first", http.StatusConflict)
		return
	}

	items := make([]models.OrderItem, len(views))
	for i, v := range views {
		items[i] = v.OrderItem
	}

	_, err = db.CreateOrder(table.Number, sessionID, models.PaymentMethodCash, models.PaymentStatusUnpaid, total, items)
	if err != nil {
		if err == db.ErrInsufficientStock {
			http.Error(w, "Sorry, an item in your cart just sold out — please review your cart", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to place order, please try again", http.StatusInternalServerError)
		return
	}

	h.Cart.Clear(sessionID)

	if b, err := renderOrderFeedHTML(); err == nil {
		BroadcastOrderFeed(b)
	}
	BroadcastTableStatus(table.Number)
	BroadcastMenu()
	BroadcastAdminStats()

	w.Header().Set("HX-Redirect", "/table/"+strconv.Itoa(table.Number))
}
