package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"strconv"

	"bitebox/internal/auth"
	"bitebox/internal/cart"
	"bitebox/internal/db"

	"github.com/go-chi/chi/v5"
)

const GuestSessionCookieName = "bitebox_session"

func GetOrCreateGuestSession(w http.ResponseWriter, r *http.Request) string {
	cookie, err := r.Cookie(GuestSessionCookieName)
	if err != nil {
		sessionID := auth.GenerateSessionID()
		http.SetCookie(w, &http.Cookie{
			Name:     GuestSessionCookieName,
			Value:    sessionID,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		return sessionID
	}
	return cookie.Value
}

// TableHandlers groups the guest-facing table routes that need access to the
// shared in-memory cart store (table view renders the current cart, leaving
// a table clears it).
type TableHandlers struct {
	Cart *cart.Store
}

// View handles /table/{number}: claims the table for a new host, or shows
// the locked guest view to anyone else.
//
// Diagnosed the "menu takes a second or two to appear after the page loads"
// report: it wasn't server time (a timed breakdown of every step here came
// back at ~3ms total, WAL fix and all) and it wasn't payload size (same lag
// reported with a 3-item catalog). #product-list and #dj-section started
// out empty in host_menu.html — "Loading menu..." — and only got real
// content once the browser had parsed the page, run htmx, opened /menu/ws,
// and received its first push. That's several sequential round trips gating
// every item's very first appearance, which is exactly the kind of fixed
// per-load cost that doesn't move with catalog size and does show up as
// "a second or two" over anything less than perfect network. Fix: render
// both directly into the response below, same as the DB already renders
// them for the websocket's first push — the two now use the identical
// html, just reached without a client round trip. /menu/ws stays wired up
// for what it's actually for: live updates after that first paint.
func (h *TableHandlers) View(w http.ResponseWriter, r *http.Request) {
	tableNum, _ := strconv.Atoi(chi.URLParam(r, "number"))

	sessionID := GetOrCreateGuestSession(w, r)

	table, err := db.GetTable(tableNum)
	if err != nil {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}

	if table.Status == "available" || table.HostSessionID == "" {
		db.ClaimTable(tableNum, sessionID)
		table.HostSessionID = sessionID
		BroadcastAdminTables()
	}

	if table.HostSessionID == sessionID {
		settings, _ := db.GetSettings()

		productListHTML, err := renderGuestProductListHTML()
		if err != nil {
			http.Error(w, "Failed to load menu", http.StatusInternalServerError)
			return
		}
		djSectionHTML, err := renderDJSectionHTML()
		if err != nil {
			http.Error(w, "Failed to load menu", http.StatusInternalServerError)
			return
		}

		tmpl := template.Must(template.ParseFiles("templates/host_menu.html"))
		tmpl.Execute(w, map[string]interface{}{
			"TableNumber":     tableNum,
			"VenueName":       settings.VenueName,
			"ProductListHTML": template.HTML(productListHTML),
			"DJSectionHTML":   template.HTML(djSectionHTML),
		})
	} else {
		tmpl := template.Must(template.ParseFiles("templates/guest_menu.html"))
		tmpl.Execute(w, map[string]interface{}{
			"TableNumber": tableNum,
		})
	}
}

func (h *TableHandlers) Left(w http.ResponseWriter, r *http.Request) {
	tableNum, _ := strconv.Atoi(chi.URLParam(r, "number"))

	tmpl := template.Must(template.ParseFiles("templates/table_left.html"))
	tmpl.Execute(w, map[string]interface{}{
		"TableNumber": tableNum,
	})
}

func (h *TableHandlers) Leave(w http.ResponseWriter, r *http.Request) {
	tableNum, _ := strconv.Atoi(chi.URLParam(r, "number"))

	if sessionCookie, err := r.Cookie(GuestSessionCookieName); err == nil {
		h.Cart.Clear(sessionCookie.Value)
	}

	db.ReleaseTable(tableNum)
	BroadcastAdminTables()
	BroadcastAllMenus()

	http.SetCookie(w, &http.Cookie{
		Name:     GuestSessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	w.Header().Set("HX-Redirect", fmt.Sprintf("/table/%d/left", tableNum))
}

// renderOrderStatusHTML renders the guest-facing "your order status" widget
// for a table, used both for the plain HTTP fallback and for websocket pushes.
func renderOrderStatusHTML(tableNumber int) []byte {
	order, err := db.GetActiveOrderForTable(tableNumber)
	tmpl := template.Must(template.ParseFiles("templates/_order_status.html"))
	var buf bytes.Buffer
	if err != nil {
		tmpl.Execute(&buf, map[string]interface{}{"HasOrder": false})
		return buf.Bytes()
	}
	tmpl.Execute(&buf, map[string]interface{}{
		"HasOrder": true,
		"Order":    order,
	})
	return buf.Bytes()
}

// BroadcastTableStatus pushes a fresh order-status fragment to any guest
// connected over that table's websocket.
func BroadcastTableStatus(tableNumber int) {
	Hub.Broadcast(topicTableStatus(tableNumber), oobWrap("order-status", renderOrderStatusHTML(tableNumber)))
}

// renderSongStatusHTML renders the guest-facing "DJ decision" widget for a
// table's most recent song request.
func renderSongStatusHTML(tableNumber int) []byte {
	request, err := db.GetLatestSongRequestForTable(tableNumber)
	tmpl := template.Must(template.ParseFiles("templates/_song_status.html"))
	var buf bytes.Buffer
	if err != nil {
		tmpl.Execute(&buf, map[string]interface{}{"HasRequest": false})
		return buf.Bytes()
	}
	tmpl.Execute(&buf, map[string]interface{}{
		"HasRequest": true,
		"Request":    request,
	})
	return buf.Bytes()
}

// BroadcastSongStatus pushes a fresh DJ-decision fragment to any guest
// connected over that table's websocket.
func BroadcastSongStatus(tableNumber int) {
	Hub.Broadcast(topicTableStatus(tableNumber), oobWrap("song-status", renderSongStatusHTML(tableNumber)))
}
