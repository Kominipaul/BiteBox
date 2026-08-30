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

// guestMenuGroups fetches and groups the current guest-facing catalog —
// shared by renderGuestProductListHTML (the /menu/ws payload) and
// TableHandlers.View (which also needs the group names alone, for the
// guest menu's category-tab bar, without re-rendering the HTML twice).
func guestMenuGroups() ([]productCategoryGroup, error) {
	products, err := db.GetProducts()
	if err != nil {
		return nil, err
	}
	categories, err := db.GetCategories()
	if err != nil {
		return nil, err
	}
	return groupProductsByCategory(buildProductViews(products), categoryNames(categories)), nil
}

func renderGuestProductListHTMLFromGroups(groups []productCategoryGroup) ([]byte, error) {
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_guest_product_list.html"))
	if err := tmpl.Execute(&buf, map[string]interface{}{"Groups": groups}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func renderGuestProductListHTML() ([]byte, error) {
	groups, err := guestMenuGroups()
	if err != nil {
		return nil, err
	}
	return renderGuestProductListHTMLFromGroups(groups)
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

// BroadcastAllMenus pushes fresh menu content to both the guest-facing live
// menu and the admin dashboard's menu list. Anything that changes what's
// orderable needs both kept in sync — an admin edit, or a guest reserving
// stock by adding it to their cart (which shifts live availability without
// touching the products.stock column at all; only checkout does that).
func BroadcastAllMenus() {
	BroadcastMenu()
	BroadcastAdminMenu()
}

func BroadcastAdminMenu() {
	b, err := renderAdminProductListHTML()
	if err != nil {
		return
	}
	Hub.Broadcast(topicAdminMenu, oobWrap("menuList", b))
}

// renderDJSectionHTML renders the guest-facing "Request a song to DJ" form
// section — entirely absent when the admin has disabled it (not a disabled/
// greyed-out state, genuinely not there, matching "song requests aren't
// shown to the guest at all" when a venue doesn't want them). This is only
// the submission form; the #song-status decision widget is a separate,
// always-present element (see host_menu.html) so an admin flipping this off
// mid-visit doesn't hide the outcome of a request a guest already sent.
func renderDJSectionHTML() ([]byte, error) {
	settings, err := db.GetSettings()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_dj_section.html"))
	if err := tmpl.Execute(&buf, map[string]interface{}{"DJRequestsEnabled": settings.DJRequestsEnabled}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BroadcastDJSection pushes the DJ request form's current on/off state to
// every guest connected over /menu/ws (a venue-wide setting, not per-table
// — reusing the global menu topic rather than needing a new connection).
func BroadcastDJSection() {
	b, err := renderDJSectionHTML()
	if err != nil {
		return
	}
	Hub.Broadcast(topicMenu, oobWrap("dj-section", b))
}

// BroadcastVenueName pushes a renamed venue to every guest already browsing
// the menu, live. Unlike the DJ section, the venue name doesn't need its own
// render-on-connect step in MenuWS's initial payload — it's already part of
// host_menu.html's own server-rendered HTML at page-load time (see
// TableHandlers.View); this broadcast only matters for a tab that's already
// open when an admin renames the venue mid-service.
//
// Built by hand rather than via oobWrap: the target element also carries
// class="name" (host_menu.html's venue-banner styling), and an OOB swap
// replaces the whole element outerHTML — reusing oobWrap here would silently
// drop that class off the live-updated element.
func BroadcastVenueName(name string) {
	frag := `<div id="venue-name" class="name" hx-swap-oob="true">` + template.HTMLEscapeString(name) + `</div>`
	Hub.Broadcast(topicMenu, []byte(frag))
}
