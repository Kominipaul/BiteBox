package handlers

import (
	"encoding/json"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"

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
// ExcludedJSON/ExtrasJSON are precomputed JSON arrays (e.g. ["Onion"]) for
// embedding directly into an hx-vals attribute on the remove button, so it
// targets the exact customization variant, not just the product.
type cartItemView struct {
	models.OrderItem
	Unavailable  bool
	ExcludedJSON string
	ExtrasJSON   string
	LineTotal    float64
}

func jsonArray(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// resolveCart returns per-item views plus a total computed only over items
// that are still purchasable (an unavailable item can't be checked out, so
// it doesn't count toward what the guest owes), and the raw total quantity
// across every line (used for the cart-bar badge, unlike Total this counts
// unavailable lines too — it's "what's physically in your cart").
//
// Price (base and per-extra) is always re-derived from the current catalog
// here, never trusted from the add-time snapshot stored on the item — same
// reasoning as the base product price already got: an admin can change an
// extra's price, or delete it outright, after a guest already added it to
// their cart, and the cart should reflect that the moment it re-renders.
func resolveCart(c models.Cart) (views []cartItemView, total float64, hasUnavailable bool, totalQty int) {
	for _, item := range c.Items {
		v := cartItemView{OrderItem: item, ExcludedJSON: jsonArray(item.Excluded), ExtrasJSON: jsonArray(item.Extras)}
		totalQty += item.Quantity
		product, err := db.GetProductByID(item.ProductID)
		if err != nil || !product.IsAvailable || (product.Stock != -1 && product.Stock < item.Quantity) {
			v.Unavailable = true
			hasUnavailable = true
		} else {
			extrasPrice := 0.0
			ingredients, _ := db.GetIngredientsByProductIDs([]int{item.ProductID})
			for _, ing := range ingredients[item.ProductID] {
				if ing.Kind == models.IngredientExtra && slices.Contains(item.Extras, ing.Name) {
					extrasPrice += ing.Price
				}
			}
			v.Name = product.Name
			v.Price = product.Price + extrasPrice
			total += v.Price * float64(v.Quantity)
		}
		v.LineTotal = v.Price * float64(v.Quantity)
		views = append(views, v)
	}
	return views, total, hasUnavailable, totalQty
}

func renderCartSummary(w http.ResponseWriter, c models.Cart) {
	views, total, hasUnavailable, totalQty := resolveCart(c)
	tmpl := template.Must(template.ParseFiles("templates/_cart_summary.html"))
	tmpl.Execute(w, map[string]interface{}{
		"Items":          views,
		"Total":          total,
		"HasUnavailable": hasUnavailable,
		"TotalQty":       totalQty,
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

// validCustomization filters raw excluded/extra ingredient names down to
// only the ones that are actually that kind of ingredient on this product —
// never trust client-supplied ingredient names outright — and sums the
// price of whichever extras survive that filter, off the catalog's current
// price for each (never a client-supplied number).
func validCustomization(productID int, rawExcluded, rawExtras []string) (excluded, extras []string, extrasPrice float64) {
	if len(rawExcluded) == 0 && len(rawExtras) == 0 {
		return nil, nil, 0
	}
	byProduct, err := db.GetIngredientsByProductIDs([]int{productID})
	if err != nil {
		return nil, nil, 0
	}
	removable := make(map[string]bool)
	extraPrice := make(map[string]float64)
	for _, ing := range byProduct[productID] {
		if ing.Kind == models.IngredientExtra {
			extraPrice[ing.Name] = ing.Price
		} else {
			removable[ing.Name] = true
		}
	}
	for _, name := range rawExcluded {
		if removable[name] {
			excluded = append(excluded, name)
		}
	}
	for _, name := range rawExtras {
		if price, ok := extraPrice[name]; ok {
			extras = append(extras, name)
			extrasPrice += price
		}
	}
	return excluded, extras, extrasPrice
}

func (h *CartHandlers) Add(w http.ResponseWriter, r *http.Request) {
	_, sessionID, err := resolveHostedTable(r)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}

	r.ParseForm()
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
		// Reserved across every guest's cart, not just this session's own —
		// otherwise two guests could each "successfully" reserve the last
		// unit at the same time.
		if h.Cart.ReservedQuantity(product.ID)+1 > product.Stock {
			http.Error(w, "Not enough stock left", http.StatusConflict)
			return
		}
	}

	excluded, extras, extrasPrice := validCustomization(product.ID, r.Form["excluded"], r.Form["extras"])
	updated := h.Cart.Add(sessionID, product.ID, product.Name, product.Price+extrasPrice, excluded, extras)
	renderCartSummary(w, updated)
	BroadcastAllMenus()
}

func (h *CartHandlers) Remove(w http.ResponseWriter, r *http.Request) {
	_, sessionID, err := resolveHostedTable(r)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}

	r.ParseForm()
	productID, err := strconv.Atoi(r.FormValue("product_id"))
	if err != nil {
		http.Error(w, "Invalid product", http.StatusBadRequest)
		return
	}

	updated := h.Cart.Remove(sessionID, productID, r.Form["excluded"], r.Form["extras"])
	renderCartSummary(w, updated)
	BroadcastAllMenus()
}

// Clear empties the caller's entire cart in one action — the guest UI's
// "Cancel order" button, for backing out before checkout (not to be
// confused with cancelling an already-placed order, see GuestCancelOrder).
func (h *CartHandlers) Clear(w http.ResponseWriter, r *http.Request) {
	_, sessionID, err := resolveHostedTable(r)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}
	h.Cart.Clear(sessionID)
	renderCartSummary(w, models.Cart{})
	BroadcastAllMenus()
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

	views, total, hasUnavailable, _ := resolveCart(guestCart)
	if hasUnavailable {
		http.Error(w, "Some items in your cart are no longer available — please remove them first", http.StatusConflict)
		return
	}

	items := make([]models.OrderItem, len(views))
	for i, v := range views {
		items[i] = v.OrderItem
	}

	method := r.FormValue("payment_method")
	if method != models.PaymentMethodCard {
		method = models.PaymentMethodCash
	}
	// Card has no real processor behind it (matches the guest UI's own
	// "Demo" label) — treated as instantly paid, since there's no cash to
	// collect in person the way there is for the cash method.
	paymentStatus := models.PaymentStatusUnpaid
	if method == models.PaymentMethodCard {
		paymentStatus = models.PaymentStatusPaid
	}
	note := strings.TrimSpace(r.FormValue("note"))
	if len(note) > 500 {
		note = note[:500]
	}

	_, err = db.CreateOrder(table.Number, sessionID, method, paymentStatus, note, total, items)
	if err != nil {
		if err == db.ErrInsufficientStock {
			http.Error(w, "Sorry, an item in your cart just sold out — please review your cart", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to place order, please try again", http.StatusInternalServerError)
		return
	}

	h.Cart.Clear(sessionID)

	BroadcastOrderFeed()
	BroadcastTableStatus(table.Number)
	BroadcastPlacedOrders(table.Number, sessionID)
	BroadcastAllMenus()
	BroadcastAdminStats()

	w.Header().Set("HX-Redirect", "/table/"+strconv.Itoa(table.Number))
}
