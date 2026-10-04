package handlers

import (
	"encoding/json"
	"html/template"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"bitebox/internal/cart"
	"bitebox/internal/db"
	"bitebox/internal/models"
)

// maxTip and maxSplitCount bound the two optional checkout extras — same
// "guest-entered number, never trust it outright" reasoning as everywhere
// else a guest supplies one (see dj.go's maxSongTip).
const (
	maxTip        = 500.0
	maxSplitCount = 20
)

// parseTip reads the optional "tip" checkout field: blank/unparseable means
// no tip (not an error — a tip is always optional), any parsed value still
// gets the same NaN/Inf/range guard as the DJ tip.
func parseTip(r *http.Request) (float64, bool) {
	raw := strings.TrimSpace(r.FormValue("tip"))
	if raw == "" {
		return 0, true
	}
	tip, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(tip) || math.IsInf(tip, 0) || tip < 0 || tip > maxTip {
		return 0, false
	}
	return tip, true
}

// parseSplitCount reads the optional "split" checkout field: blank or "1"
// means no split (the common case). Anything else must be a sane positive
// integer — capped at maxSplitCount since a table splitting a bill between
// more people than could physically fit around it is a tampered request,
// not a real use, and it's purely a display divisor so the failure mode of
// rejecting it outright (rather than silently clamping) is the guest just
// re-entering a sane number, not a broken order.
func parseSplitCount(r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.FormValue("split"))
	if raw == "" {
		return 1, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxSplitCount {
		return 1, false
	}
	return n, true
}

// resolvePaymentMethod validates the guest's chosen payment method against
// the venue's current cash-orders setting. ok=false means the request
// should be rejected outright: the guest checkout screen already hides the
// cash option the moment it's disabled (see renderCartSummary), so an
// explicit "cash" pick past that point means either a stale page (the
// admin flipped the setting while this tab was already open, before the
// live cart-refresh nudge caught up) or a direct/tampered POST — either
// way, the guest needs a clear "pick something else" answer, not a
// silently-substituted charge method they didn't choose. A blank/
// unrecognized value isn't a rejection, just falls back to whichever of
// cash/card is actually still on offer.
func resolvePaymentMethod(r *http.Request, allowCashOrders bool) (method string, ok bool) {
	switch raw := r.FormValue("payment_method"); {
	case raw == models.PaymentMethodCash && !allowCashOrders:
		return "", false
	case raw == models.PaymentMethodCard:
		return models.PaymentMethodCard, true
	case allowCashOrders:
		return models.PaymentMethodCash, true // preserves the original "anything but card means cash" default
	default:
		return models.PaymentMethodCard, true // cash is off — card is the only option left
	}
}

// CartHandlers groups guest cart/checkout routes needing access to the
// shared in-memory cart store.
type CartHandlers struct {
	Cart *cart.Store
}

// resolveHostedTable authorizes a cart mutation by resolving which table (if
// any) the caller's guest session currently hosts — never trusting a
// client-supplied table number. Also stamps the table's activity heartbeat
// (see db.TouchTableActivity/sweepIdleTables in main.go) — any real guest
// action routes through here, so this one spot covers add/remove/clear/
// checkout/DJ-request without needing a call in each of them individually.
func resolveHostedTable(r *http.Request) (models.Table, string, error) {
	sessionCookie, err := r.Cookie(GuestSessionCookieName)
	if err != nil {
		return models.Table{}, "", err
	}
	table, err := db.GetTableByHostSession(sessionCookie.Value)
	if err == nil {
		db.TouchTableActivity(table.Number)
	}
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
func resolveCart(c models.Cart, zone string) (views []cartItemView, total float64, hasUnavailable bool, totalQty int) {
	rules := loadMenuRules() // Menus & hours: a menu that ended / a closed kitchen makes a line unavailable
	for _, item := range c.Items {
		v := cartItemView{OrderItem: item, ExcludedJSON: jsonArray(item.Excluded), ExtrasJSON: jsonArray(item.Extras)}
		totalQty += item.Quantity
		product, err := db.GetProductByID(item.ProductID)
		if err != nil || !product.IsAvailable || (product.Stock != -1 && product.Stock < item.Quantity) || !rules.item(product, zone).Orderable {
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
			price, _ := rules.price(product, zone) // a running happy hour
			v.Price = price + extrasPrice
			total += v.Price * float64(v.Quantity)
		}
		v.LineTotal = v.Price * float64(v.Quantity)
		views = append(views, v)
	}
	return views, total, hasUnavailable, totalQty
}

// renderCartSummary fetches AllowCashOrders fresh on every render — this is
// how the checkout screen "dynamically fetches venue settings on load":
// #cart-summary already re-renders from scratch on page load and on every
// live cart-refresh nudge (see menu.go's cartRefreshTrigger, pushed by
// AdminToggleCashOrders same as any other menu-affecting change), so
// threading the current setting through here is all a guest's checkout
// screen needs to stay in sync — no separate settings-fetch endpoint.
func renderCartSummary(w http.ResponseWriter, c models.Cart, zone string) {
	views, total, hasUnavailable, totalQty := resolveCart(c, zone)
	settings, _ := db.GetSettings()
	tmpl := template.Must(template.ParseFiles("templates/_cart_summary.html"))
	tmpl.Execute(w, map[string]interface{}{
		"Items":           views,
		"Total":           total,
		"HasUnavailable":  hasUnavailable,
		"TotalQty":        totalQty,
		"AllowCashOrders": settings.AllowCashOrders,
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
		renderCartSummary(w, models.Cart{}, "")
		return
	}
	zone := ""
	if t, err := db.GetTableByHostSession(sessionCookie.Value); err == nil {
		zone = t.Zone
	}
	// A menu ended / the kitchen closed since these went in: take them out
	// and say so (a stock shortage stays, so the guest can lower the amount).
	rules := loadMenuRules()
	dropped := h.Cart.Drop(sessionCookie.Value, func(it models.OrderItem) bool {
		p, err := db.GetProductByID(it.ProductID)
		return err == nil && p.IsAvailable && rules.item(p, zone).Orderable
	})
	if len(dropped) > 0 {
		names := make([]string, len(dropped))
		for i, it := range dropped {
			names[i] = it.Name
		}
		msg := "Removed from your order: " + strings.Join(names, ", ") + " (not served right now)"
		b, _ := json.Marshal(map[string]string{"cartRemoved": msg})
		w.Header().Set("HX-Trigger", string(b))
	}
	renderCartSummary(w, h.Cart.Get(sessionCookie.Value), zone)
	if len(dropped) > 0 {
		// Their reserved stock is free again: refresh the staff list only. A
		// guest-menu push here would replace the hidden element that asked for
		// this cart, and the "removed" toast would fire on nothing.
		BroadcastAdminMenu()
	}
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
	table, sessionID, err := resolveHostedTable(r)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}
	if !AllowCartAction(r) {
		http.Error(w, "Too many cart actions — please slow down", http.StatusTooManyRequests)
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
	rules := loadMenuRules()
	if !rules.item(product, table.Zone).Orderable {
		http.Error(w, "That's not being served right now", http.StatusConflict)
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
	price, _ := rules.price(product, table.Zone) // a running happy hour
	updated := h.Cart.Add(sessionID, product.ID, product.Name, price+extrasPrice, excluded, extras)
	renderCartSummary(w, updated, table.Zone)
	BroadcastAllMenus()
}

func (h *CartHandlers) Remove(w http.ResponseWriter, r *http.Request) {
	table, sessionID, err := resolveHostedTable(r)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}
	if !AllowCartAction(r) {
		http.Error(w, "Too many cart actions — please slow down", http.StatusTooManyRequests)
		return
	}

	r.ParseForm()
	productID, err := strconv.Atoi(r.FormValue("product_id"))
	if err != nil {
		http.Error(w, "Invalid product", http.StatusBadRequest)
		return
	}

	updated := h.Cart.Remove(sessionID, productID, r.Form["excluded"], r.Form["extras"])
	renderCartSummary(w, updated, table.Zone)
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
	renderCartSummary(w, models.Cart{}, "")
	BroadcastAllMenus()
}

func (h *CartHandlers) Checkout(w http.ResponseWriter, r *http.Request) {
	if tooMany(orderLimiter, w, r) {
		return
	}
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

	views, total, hasUnavailable, _ := resolveCart(guestCart, table.Zone)
	if hasUnavailable {
		http.Error(w, "Some items in your cart are no longer available — please remove them first", http.StatusConflict)
		return
	}

	items := make([]models.OrderItem, len(views))
	for i, v := range views {
		items[i] = v.OrderItem
	}

	settings, _ := db.GetSettings()
	method, methodOK := resolvePaymentMethod(r, settings.AllowCashOrders)
	if !methodOK {
		http.Error(w, "Cash payments are currently unavailable at this venue — please pay by card.", http.StatusForbidden)
		return
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

	tip, tipOK := parseTip(r)
	splitCount, splitOK := parseSplitCount(r)
	if !tipOK || !splitOK {
		http.Error(w, "Invalid tip amount or split count", http.StatusBadRequest)
		return
	}

	orderID, err := db.CreateOrder(table.Number, sessionID, method, paymentStatus, note, total, tip, splitCount, items)
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
	pushNewOrderAlert(orderID, table.Number, items)
	pushOrderConfirmedAlert(sessionID, orderID, table.Number, items)

	w.Header().Set("HX-Redirect", "/table/"+strconv.Itoa(table.Number)+"?placed=1") // full reload, opening the Service sheet on "Your orders"
}
