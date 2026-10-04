package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"bitebox/internal/auth"
	"bitebox/internal/cart"
	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
)

const GuestSessionCookieName = "bitebox_session"

// setGuestSessionCookie and clearGuestSessionCookie mirror auth.SetSessionCookie/
// ClearSessionCookie for the separate, anonymous guest-session cookie — same
// Secure-if-the-request-actually-arrived-over-TLS derivation (see
// auth.IsSecureRequest), just without a staff user attached.
func setGuestSessionCookie(w http.ResponseWriter, r *http.Request, sessionID string) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- HttpOnly + SameSite set; Secure whenever the request came over HTTPS (auth.IsSecureRequest)
		Name:     GuestSessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   auth.IsSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearGuestSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- HttpOnly + SameSite set; Secure whenever the request came over HTTPS (auth.IsSecureRequest)
		Name:     GuestSessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   auth.IsSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// releasedSessions remembers guest sessions whose table staff (or the idle
// sweep) released, so their phone can't quietly re-claim the table by
// reloading the old page — see endGuestSession and View.
// ponytail: in memory; a server restart forgets them (that phone would then
// re-claim its table on reload, as before).
var releasedSessions = struct {
	sync.Mutex
	m map[string]time.Time
}{m: map[string]time.Time{}}

func wasReleased(sessionID string) bool {
	releasedSessions.Lock()
	defer releasedSessions.Unlock()
	_, ok := releasedSessions.m[sessionID]
	return ok
}

func forgetReleased(sessionID string) {
	releasedSessions.Lock()
	delete(releasedSessions.m, sessionID)
	releasedSessions.Unlock()
}

// sessionEndOOB makes the guest's open menu page leave (host_menu.html
// watches #session-end); it goes back to /table/{n}, which View then sends
// to the home page.
func sessionEndOOB(table int) []byte {
	return []byte(fmt.Sprintf(`<div id="session-end" hx-swap-oob="true" data-go="/table/%d" hidden></div>`, table))
}

// endGuestSession is how a staff or idle release ends a guest's QR session:
// their open phone page is moved off the menu straight away, and a later
// reload of the old page lands on the home page instead of re-claiming.
func endGuestSession(table int, sessionID string) {
	if sessionID == "" {
		return
	}
	releasedSessions.Lock()
	for id, at := range releasedSessions.m {
		if time.Since(at) > 24*time.Hour {
			delete(releasedSessions.m, id)
		}
	}
	releasedSessions.m[sessionID] = time.Now()
	releasedSessions.Unlock()
	Hub.Broadcast(topicSessionOrders(sessionID), sessionEndOOB(table))
}

// TableHandlers groups the guest-facing table routes that need access to the
// shared in-memory cart store (table view renders the current cart, leaving
// a table clears it).
type TableHandlers struct {
	Cart *cart.Store
}

// Scan handles /t/{token}, the URL in a table's printed QR code — the only
// way to claim a table. The token is random (db.NewTableToken), so nobody can
// reach or squat a table by counting through numbers (SEC-13); staff can
// revoke a code with "New QR code" on the QR sheet. The host is then sent to
// /table/{n}, so the token doesn't sit in the address bar or history.
func (h *TableHandlers) Scan(w http.ResponseWriter, r *http.Request) {
	table, err := db.GetTableByToken(chi.URLParam(r, "token"))
	if err != nil {
		renderTableMessage(w, http.StatusNotFound, 0, "QR code not recognised",
			"This QR code isn't in use any more. Ask a member of staff for help.")
		return
	}

	// A phone whose earlier table was released and scans again is back at
	// the venue: it gets a fresh session rather than being sent home.
	var sessionID string
	if c, err := r.Cookie(GuestSessionCookieName); err == nil && c.Value != "" && !wasReleased(c.Value) {
		sessionID = c.Value
	} else {
		if err == nil {
			forgetReleased(c.Value)
		}
		sessionID = auth.GenerateSessionID()
		setGuestSessionCookie(w, r, sessionID)
	}

	if table.Status == "available" || table.HostSessionID == "" {
		if !AllowTableClaim(r) {
			http.Error(w, "Too many table claims from this connection — please try again shortly", http.StatusTooManyRequests)
			return
		}
		// One phone hosts one table: moving to this one frees the last, so
		// orders can never land on a table the phone has left.
		if prev, err := db.GetTableByHostSession(sessionID); err == nil && prev.Number != table.Number {
			if err := db.ReleaseTable(prev.Number); err != nil {
				http.Error(w, "Something went wrong, please scan again", http.StatusInternalServerError)
				return
			}
		}
		if err := db.ClaimTable(table.Number, sessionID); err != nil {
			http.Error(w, "Something went wrong, please scan again", http.StatusInternalServerError)
			return
		}
		table.HostSessionID = sessionID
		// Scanning in during this table's booking = the party arrived.
		db.SeatArrivedBooking(table.Number, time.Now())
		BroadcastFloor()
	}

	if table.HostSessionID != sessionID {
		renderTableMessage(w, http.StatusOK, table.Number, "Currently occupied",
			"This table is currently hosted by another guest.",
			`To order or take over this table, ask the host to click "Leave Table" or speak with a waiter.`)
		return
	}
	http.Redirect(w, r, "/table/"+strconv.Itoa(table.Number), http.StatusSeeOther)
}

// View handles /table/{number}: the host's menu page. Only the phone that
// claimed the table by scanning its QR code (Scan) gets it — a reload, or the
// home-screen app reopening. Anyone else typing the URL is told to scan.
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

	// Staff released this phone's table: back to the home page with a fresh
	// session, rather than silently re-claiming the table.
	c, err := r.Cookie(GuestSessionCookieName)
	if err == nil && wasReleased(c.Value) {
		forgetReleased(c.Value)
		clearGuestSessionCookie(w, r)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	var table models.Table
	if err == nil {
		table, err = db.GetTableByHostSession(c.Value)
	}
	if err != nil || table.Number != tableNum {
		renderTableMessage(w, http.StatusForbidden, 0, "Scan to order",
			"Scan the QR code on your table to see the menu and order.")
		return
	}

	db.TouchTableActivity(tableNum)
	settings, _ := db.GetSettings()

	menu, err := guestMenu(table.Zone)
	if err != nil {
		http.Error(w, "Failed to load menu", http.StatusInternalServerError)
		return
	}
	// The category tabs, notices and search live inside #product-list
	// (see _guest_product_list.html), so they follow the schedule live.
	productListHTML, err := renderGuestProductListHTMLFrom(menu)
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
		"ProductListHTML": template.HTML(productListHTML),                        // #nosec G203 -- our own html/template output
		"DJSectionHTML":   template.HTML(djSectionHTML),                          // #nosec G203 -- our own html/template output
		"AssistPanelHTML": template.HTML(renderAssistPanelHTML(tableNum, false)), // #nosec G203 -- our own html/template output
		"V":               assetVersion,
		"VAPIDPublicKey":  PushVAPIDPublicKey(),
		"IconVersion":     IconVersion(),
	})
}

// renderTableMessage is the plain guest card (templates/guest_menu.html) for
// every "you can't order here" answer; table 0 hides the table eyebrow.
func renderTableMessage(w http.ResponseWriter, status, table int, title string, lines ...string) {
	settings, _ := db.GetSettings()
	w.WriteHeader(status)
	template.Must(template.ParseFiles("templates/guest_menu.html")).Execute(w, map[string]interface{}{
		"VenueName": settings.VenueName, "TableNumber": table, "Title": title, "Lines": lines, "V": assetVersion,
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
	BroadcastFloor()
	BroadcastAllMenus()

	clearGuestSessionCookie(w, r)

	w.Header().Set("HX-Redirect", "/")
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
// same way a manual force-release already does (StaffReleaseTable), so
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
				endGuestSession(t.Number, t.HostSessionID)
				log.Printf("⏱️  Table %d auto-released after %v idle", t.Number, tableIdleTTL)
			}
			BroadcastFloor()
			BroadcastAllMenus()
		}
	}()
}

// renderAssistPanelHTML renders the guest's "Call a waiter" sheet for a
// table: "pending" while a call waits, "ack" right after staff answered
// (answered = true; one-off, not stored), otherwise the form.
func renderAssistPanelHTML(tableNumber int, answered bool) []byte {
	data := map[string]interface{}{"Table": tableNumber, "Reasons": models.AssistReasons, "State": "idle"}
	if call, pending, err := db.AssistanceState(tableNumber); err == nil && pending {
		data["State"], data["SinceMs"] = "pending", call.Since.UnixMilli()
		for _, r := range models.AssistReasons {
			if r.Key == call.Reason {
				data["ReasonGuest"] = r.Guest
			}
		}
	} else if answered {
		data["State"] = "ack"
	}
	var buf bytes.Buffer
	template.Must(template.ParseFiles("templates/_assist_panel.html")).Execute(&buf, data)
	return buf.Bytes()
}

// pushAssistPanel sends a table's host their fresh "Call a waiter" sheet over
// their own session socket (never the table-wide one: see TableStatusWS), so
// "a waiter is on the way" shows up live on their phone.
func pushAssistPanel(tableNumber int, answered bool) {
	t, err := db.GetTable(tableNumber)
	if err != nil || t.HostSessionID == "" {
		return
	}
	Hub.Broadcast(topicSessionOrders(t.HostSessionID), oobWrap("assist-panel", renderAssistPanelHTML(tableNumber, answered)))
}

// validAssistReason keeps the stored reason to the known list.
func validAssistReason(key string) string {
	for _, r := range models.AssistReasons {
		if r.Key == key {
			return key
		}
	}
	return models.AssistReasons[0].Key
}

// RequestAssistance is the guest-facing "Call a waiter" button — flags the
// caller's own hosted table (never a client-supplied number, same
// resolveHostedTable pattern as every other guest action) so staff see it
// live, with why. Calling again while one waits just updates the reason and
// doesn't re-alert everyone's phone.
func RequestAssistance(w http.ResponseWriter, r *http.Request) {
	if tooMany(assistLimiter, w, r) {
		return
	}
	table, _, err := resolveHostedTable(r)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}
	reason := validAssistReason(r.FormValue("reason"))
	fresh, err := db.RequestAssistance(table.Number, reason)
	if err != nil {
		http.Error(w, "Failed to send request", http.StatusInternalServerError)
		return
	}
	BroadcastFloor()
	BroadcastAssistanceAlerts()
	if fresh {
		pushAssistanceAlert(table.Number, reason)
	}
	w.Write(renderAssistPanelHTML(table.Number, false))
}

// CancelAssistance is the guest taking their call back ("Cancel request"):
// the alert disappears from every staff phone and the call is logged as
// cancelled. Only ever the caller's own table.
func CancelAssistance(w http.ResponseWriter, r *http.Request) {
	if tooMany(assistLimiter, w, r) {
		return
	}
	table, _, err := resolveHostedTable(r)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}
	if _, err := db.CancelAssistance(table.Number); err != nil {
		http.Error(w, "Failed to cancel request", http.StatusInternalServerError)
		return
	}
	BroadcastFloor()
	BroadcastAssistanceAlerts()
	w.Write(renderAssistPanelHTML(table.Number, false))
}

// WorkerAcknowledgeAssistance clears a table's assistance flag — the
// worker-side "Got it" button once staff has responded. Records who
// acknowledged it and how long it sat (see db.ClearAssistance /
// assistance_log) for the admin dashboard's call-waiter log panel.
func WorkerAcknowledgeAssistance(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil {
		http.Error(w, "Invalid table number", http.StatusBadRequest)
		return
	}
	if err := db.ClearAssistance(number, actorUsername(r)); err != nil {
		http.Error(w, "Failed to clear request", http.StatusInternalServerError)
		return
	}
	BroadcastFloor()
	BroadcastAssistanceAlerts()
	pushAssistPanel(number, true) // the guest's sheet: "a waiter is on the way"
}
