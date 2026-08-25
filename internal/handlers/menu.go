package handlers

import (
	"bytes"
	"html/template"

	"bitebox/internal/db"
)

// cartRefreshTrigger is a standing placeholder in host_menu.html
// (id="cart-refresh-trigger", hidden). Pushing this over the wire replaces
// it via OOB swap with an element that immediately fires its own hx-get,
// re-rendering #cart-summary from the guest's own session — the simplest
// way to keep a guest's already-added cart items in sync with admin price/
// availability changes without the server tracking who has what in their
// cart.
const cartRefreshTrigger = `<div id="cart-refresh-trigger" hx-swap-oob="true" hx-get="/cart/summary" hx-trigger="load" hx-target="#cart-summary" hx-swap="innerHTML" style="display:none;"></div>`

func renderGuestProductListHTML() ([]byte, error) {
	products, err := db.GetProducts()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_guest_product_list.html"))
	if err := tmpl.Execute(&buf, map[string]interface{}{"Products": products}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BroadcastMenu pushes the current guest menu to every table connected over
// /menu/ws, and nudges each of their carts to refresh (see cartRefreshTrigger)
// since a price/availability/stock change can affect items already added.
func BroadcastMenu() {
	b, err := renderGuestProductListHTML()
	if err != nil {
		return
	}
	msg := append(oobWrap("product-list", b), []byte(cartRefreshTrigger)...)
	Hub.Broadcast(topicMenu, msg)
}
