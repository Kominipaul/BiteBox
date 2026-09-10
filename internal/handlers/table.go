package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"

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
		setGuestSessionCookie(w, r, sessionID)
		return sessionID
	}
	return cookie.Value
}

// setGuestSessionCookie and clearGuestSessionCookie mirror auth.SetSessionCookie/
// ClearSessionCookie for the separate, anonymous guest-session cookie — same
// Secure-if-the-request-actually-arrived-over-TLS derivation (see
// auth.IsSecureRequest), just without a staff user attached.
func setGuestSessionCookie(w http.ResponseWriter, r *http.Request, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     GuestSessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   auth.IsSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearGuestSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     GuestSessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   auth.IsSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
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
		// Rate-limited: sequential table numbers make it trivial to script
		// through every /table/{n} and claim them all before real guests
		// scan in (see SEC-13 in SECURITY_AUDIT.md) — this bounds the blast
		// radius without changing what's printed on any table's QR code.
		if !AllowTableClaim(r) {
			http.Error(w, "Too many table claims from this connection — please try again shortly", http.StatusTooManyRequests)
			return
		}
		db.ClaimTable(tableNum, sessionID)
		table.HostSessionID = sessionID
		BroadcastAdminTables()
	}

	if table.HostSessionID == sessionID {
		db.TouchTableActivity(tableNum)
		settings, _ := db.GetSettings()

		groups, err := guestMenuGroups()
		if err != nil {
			http.Error(w, "Failed to load menu", http.StatusInternalServerError)
			return
		}
		productListHTML, err := renderGuestProductListHTMLFromGroups(groups)
		if err != nil {
			http.Error(w, "Failed to load menu", http.StatusInternalServerError)
			return
		}
		// CategoryTabs drives the guest menu's own category filter bar (see
		// host_menu.html) — same "only categories with something in them,
		// same order" rule as the admin dashboard's tab bar (AdminHome), and
		// the same names groups here, so a tab always matches something
		// actually rendered into #product-list above.
		categoryTabs := make([]string, len(groups))
		for i, g := range groups {
			categoryTabs[i] = g.Name
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
			"CategoryTabs":    categoryTabs,
			"VAPIDPublicKey":  PushVAPIDPublicKey(),
			"IconVersion":     IconVersion(),
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

// Leave releases whichever table the caller's own guest session currently
// hosts — never a client-supplied table number (see resolveHostedTable in
// cart.go for the same rule applied to cart mutations). Without this check
// anyone could POST /table/{n}/leave for any n and force-release a table
// that isn't theirs, ejecting its actual host mid-order.
func (h *TableHandlers) Leave(w http.ResponseWriter, r *http.Request) {
	sessionCookie, err := r.Cookie(GuestSessionCookieName)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}

	table, err := db.GetTableByHostSession(sessionCookie.Value)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}

	h.Cart.Clear(sessionCookie.Value)
	db.ReleaseTable(table.Number)
	BroadcastAdminTables()
	BroadcastAllMenus()

	clearGuestSessionCookie(w, r)

	w.Header().Set("HX-Redirect", fmt.Sprintf("/table/%d/left", table.Number))
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

// tableIdleTTL is how long a claimed table can go without any guest action
// (view, cart add/remove, checkout, DJ request — see resolveHostedTable and
// View's own touch) before sweepIdleTables force-releases it. Generous on
// purpose: a table that's just between rounds shouldn't get swept, only one
// that's genuinely been abandoned (closed tab, walked out without hitting
// "Leave"). tableSweepInterval is how often the sweep actually runs.
const (
	tableIdleTTL       = 45 * time.Minute
	tableSweepInterval = 5 * time.Minute
)

// StartIdleTableSweep runs for the lifetime of the process, periodically
// force-releasing any table nobody's touched in tableIdleTTL — closes the
// "no table auto-release" gap noted in TODO.md: previously the only way an
// abandoned table came back was a staff member noticing and force-releasing
// it from /admin. cartStore clears each released table's reserved cart the
// same way a manual force-release already does (AdminReleaseTable), so
// stock isn't left phantom-reserved once the table itself is freed.
func StartIdleTableSweep(cartStore *cart.Store) {
	go func() {
		ticker := time.NewTicker(tableSweepInterval)
		defer ticker.Stop()
		for range ticker.C {
			released, err := db.ReleaseIdleTables(tableIdleTTL)
			if err != nil {
				log.Printf("idle table sweep: %v", err)
				continue
			}
			if len(released) == 0 {
				continue
			}
			for _, t := range released {
				if t.HostSessionID != "" && cartStore != nil {
					cartStore.Clear(t.HostSessionID)
				}
				log.Printf("⏱️  Table %d auto-released after %v idle", t.Number, tableIdleTTL)
			}
			BroadcastAdminTables()
			BroadcastAllMenus()
		}
	}()
}

// RequestAssistance is the guest-facing "Call waiter" button — flags the
// caller's own hosted table (never a client-supplied number, same
// resolveHostedTable pattern as every other guest action) so staff see it
// live without the guest needing to place an order or flag someone down in
// person.
func RequestAssistance(w http.ResponseWriter, r *http.Request) {
	table, _, err := resolveHostedTable(r)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}
	if err := db.RequestAssistance(table.Number); err != nil {
		http.Error(w, "Failed to send request", http.StatusInternalServerError)
		return
	}
	BroadcastAdminTables()
	BroadcastAssistanceAlerts()
	pushAssistanceAlert(table.Number)
	w.Write([]byte(`<p class="assist-sent">✅ A staff member has been notified.</p>`))
}

// WorkerAcknowledgeAssistance clears a table's assistance flag — the
// worker-side "Got it" button once staff has responded.
func WorkerAcknowledgeAssistance(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil {
		http.Error(w, "Invalid table number", http.StatusBadRequest)
		return
	}
	if err := db.ClearAssistance(number); err != nil {
		http.Error(w, "Failed to clear request", http.StatusInternalServerError)
		return
	}
	BroadcastAdminTables()
	BroadcastAssistanceAlerts()
}
