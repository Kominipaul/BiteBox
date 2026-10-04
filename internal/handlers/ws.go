package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"bitebox/internal/auth"
	"bitebox/internal/cart"
	"bitebox/internal/db"
	"bitebox/internal/wshub"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

// Hub fans out server-rendered HTML fragments to connected dashboards, so
// worker/guest views update live instead of polling on a timer.
var Hub = wshub.NewHub()

// CartStore is the same in-memory cart store main.go hands to
// CartHandlers/TableHandlers, shared here so handlers outside those structs
// (admin product/table mutations, live-stock rendering) can read/clear
// carts too — set once in main.go, matching the Hub package-level pattern.
var CartStore *cart.Store

const (
	topicMenu      = "menu"
	topicAdminMenu = "admin:menu"
	topicFloor     = "floor"
)

func topicTableStatus(tableNumber int) string {
	return fmt.Sprintf("table:%d:status", tableNumber)
}

// topicSessionOrders is a per-guest-session topic for the "Your orders"
// list — deliberately keyed by session, not table. /table/{n}/ws has no
// access control (by design: it's how an anonymous guest gets live
// updates before any login exists), so anything broadcast on the shared
// table-status topic is visible to literally anyone who opens that socket,
// host or not. A session's own order history — items, note, payment
// status — has no business going out on a topic every visitor to that
// table (or anyone who guesses the URL) is subscribed to.
func topicSessionOrders(sessionID string) string {
	return "session:" + sessionID + ":placed-orders"
}

func topicAdminStats(period string) string {
	return fmt.Sprintf("admin:stats:%s", period)
}

// topicWorkerOrders is department-scoped: bar/kitchen get a category-filtered
// feed, "staff" (and admin, which is mapped to "staff") gets everything.
func topicWorkerOrders(department string) string {
	return fmt.Sprintf("worker:orders:%s", department)
}

const topicWorkerDJ = "worker:dj"

// topicAssistance carries the guest-facing "Call waiter" alert panel — see
// RequestAssistance (table.go) and WorkerAssistanceWS/BroadcastAssistanceAlerts
// (worker.go).
const topicAssistance = "worker:assistance"

// oobWrap wraps an HTML fragment in an htmx out-of-band swap targeting the
// given element id, for pushing over a websocket via the htmx ws extension.
// It swaps the element's contents (innerHTML), not the element itself, so
// the target keeps its own class — replacing it wholesale used to strip
// e.g. #order-feed's "order-grid" class the moment the socket connected.
func oobWrap(id string, content []byte) []byte {
	b := make([]byte, 0, len(content)+64)
	b = append(b, `<div id="`...)
	b = append(b, id...)
	b = append(b, `" hx-swap-oob="innerHTML">`...)
	b = append(b, content...)
	b = append(b, `</div>`...)
	return b
}

// leaving CheckOrigin unset keeps gorilla's default same-origin check,
// which rejects cross-site websocket handshakes carrying our session cookie.
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// sessionRecheckInterval controls how quickly a deactivated (or naturally
// expired) staff session's open websockets notice and disconnect.
const sessionRecheckInterval = 5 * time.Second

// wsKickCloseCode is a custom WebSocket close code (RFC 6455's 4000-4999
// application-specific range) sent when a connected staff member's session
// stops being valid while their socket is still open. worker_app.html
// and admin_dashboard.html both listen for htmx:wsClose and redirect to
// /login when they see it — otherwise a deactivated account's already-open
// tab would keep receiving live updates indefinitely, looking exactly like
// they were never kicked out.
const wsKickCloseCode = 4001

// mergeTopics fans several Hub subscriptions into one channel, so pumpWS's
// select loop can stay single-channel regardless of how many topics a
// connection needs (a table socket subscribes to both the shared
// table-status topic and, when a guest session is present, that session's
// own private orders topic — see topicSessionOrders).
func mergeTopics(topics []string) (merged chan []byte, unsubscribe func()) {
	chans := make([]chan []byte, len(topics))
	for i, topic := range topics {
		chans[i] = Hub.Subscribe(topic)
	}

	out := make(chan []byte, 16)
	var wg sync.WaitGroup
	wg.Add(len(chans))
	for _, ch := range chans {
		go func(ch chan []byte) {
			defer wg.Done()
			for msg := range ch {
				out <- msg
			}
		}(ch)
	}
	go func() {
		wg.Wait()
		close(out)
	}()

	return out, func() {
		for i, ch := range chans {
			Hub.Unsubscribe(topics[i], ch)
		}
	}
}

// pumpWS upgrades the request, sends an initial snapshot, then relays every
// broadcast published to any of topics until the client disconnects. If
// sessionID is non-empty (staff-authenticated sockets only — guest-facing
// sockets like table/menu pass ""), the underlying session is periodically
// re-validated and the connection is force-closed with wsKickCloseCode the
// moment it's no longer valid.
func pumpWS(w http.ResponseWriter, r *http.Request, topics []string, initial []byte, sessionID string) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	// Clients never send data frames; without a cap gorilla buffers any
	// size, and the guest sockets need no login.
	conn.SetReadLimit(512)

	ch, unsubscribe := mergeTopics(topics)
	defer unsubscribe()

	if err := conn.WriteMessage(websocket.TextMessage, initial); err != nil {
		return
	}

	// htmx's ws extension never sends client->server messages for these
	// read-only feeds; we still need to keep reading so control frames
	// (pings/close) are processed and a client disconnect is detected.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	var recheck <-chan time.Time
	if sessionID != "" {
		ticker := time.NewTicker(sessionRecheckInterval)
		defer ticker.Stop()
		recheck = ticker.C
	}

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-recheck:
			if _, err := db.GetUserBySessionID(sessionID); err != nil {
				conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(wsKickCloseCode, "session no longer valid"),
					time.Now().Add(2*time.Second))
				return
			}
		case <-closed:
			return
		}
	}
}

func WorkerOrdersWS(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r)
	bucket := orderFeedBucket(user)
	b, err := renderOrderFeedHTML(bucket)
	if err != nil {
		log.Printf("order feed %s: %v", bucket, err) // #nosec G706 -- bucket is one of our department names
		http.Error(w, "Failed to load orders", http.StatusInternalServerError)
		return
	}
	sessionID, _ := auth.SessionIDFromRequest(r)
	pumpWS(w, r, []string{topicWorkerOrders(bucket)}, oobWrap("order-feed", b), sessionID)
}

func WorkerDJWS(w http.ResponseWriter, r *http.Request) {
	b, err := renderDJFeedHTML()
	if err != nil {
		http.Error(w, "Failed to load requests", http.StatusInternalServerError)
		return
	}
	sessionID, _ := auth.SessionIDFromRequest(r)
	pumpWS(w, r, []string{topicWorkerDJ}, oobWrap("dj-feed", b), sessionID)
}

// TableStatusWS is the host's live feed for their table. Only the phone that
// hosts table {number} may open it (anyone could otherwise watch any table's
// orders and song requests by counting numbers), plus a phone whose table was
// just released, which is told to leave.
func TableStatusWS(w http.ResponseWriter, r *http.Request) {
	tableNum, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil {
		http.Error(w, "Invalid table", http.StatusBadRequest)
		return
	}
	sessionCookie, err := r.Cookie(GuestSessionCookieName)
	if err != nil {
		http.Error(w, "Scan the QR code on your table", http.StatusForbidden)
		return
	}
	// A phone waking up after its table was released reconnects here
	// first — send it off the menu straight away.
	if wasReleased(sessionCookie.Value) {
		pumpWS(w, r, []string{topicSessionOrders(sessionCookie.Value)}, sessionEndOOB(tableNum), "")
		return
	}
	if t, err := db.GetTableByHostSession(sessionCookie.Value); err != nil || t.Number != tableNum {
		http.Error(w, "Scan the QR code on your table", http.StatusForbidden)
		return
	}
	initial := append(oobWrap("order-status", renderOrderStatusHTML(tableNum)), oobWrap("song-status", renderSongStatusHTML(tableNum))...)
	initial = append(initial, oobWrap("placed-orders", renderPlacedOrdersHTML(sessionCookie.Value))...)
	// The host's "Call a waiter" sheet rides their session topic too, so a
	// phone that slept through "on the way" / a cancel catches up here.
	initial = append(initial, oobWrap("assist-panel", renderAssistPanelHTML(tableNum, false))...)
	// The table-wide topic carries order-status/song-status; the guest's own
	// "Your orders" list rides a topic scoped to their session (SEC-11).
	topics := []string{topicTableStatus(tableNum), topicSessionOrders(sessionCookie.Value)}
	pumpWS(w, r, topics, initial, "")
}

// MenuWS serves the guest-facing live menu: product availability/price/
// stock changes and the cart-refresh nudge (see menu.go) both broadcast here.
func MenuWS(w http.ResponseWriter, r *http.Request) {
	// The menu depends on the table's area (Menus & hours); ?table=N says which.
	zone := ""
	if n, err := strconv.Atoi(r.URL.Query().Get("table")); err == nil {
		if t, err := db.GetTable(n); err == nil {
			zone = t.Zone
		}
	}
	b, err := renderGuestProductListHTML(zone)
	if err != nil {
		http.Error(w, "Failed to load menu", http.StatusInternalServerError)
		return
	}
	initial := oobWrap("product-list", b)
	if djSection, err := renderDJSectionHTML(); err == nil {
		initial = append(initial, oobWrap("dj-section", djSection)...)
	}
	pumpWS(w, r, []string{topicMenu, topicMenuZone(zone)}, initial, "")
}

// AdminMenuWS keeps the admin dashboard's menu/inventory list live —
// stock/price/availability edits and, importantly, guests adding/removing
// cart items (which shifts live-reserved stock without touching the DB
// column) all broadcast here.
func AdminMenuWS(w http.ResponseWriter, r *http.Request) {
	b, err := renderAdminProductListHTML()
	if err != nil {
		http.Error(w, "Failed to load menu", http.StatusInternalServerError)
		return
	}
	sessionID, _ := auth.SessionIDFromRequest(r)
	pumpWS(w, r, []string{topicAdminMenu}, oobWrap("menuList", b), sessionID)
}

// FloorWS pings the Floor page whenever anything on the floor changes
// (see BroadcastFloor); the page then re-fetches /admin/floor/live.
func FloorWS(w http.ResponseWriter, r *http.Request) {
	sessionID, _ := auth.SessionIDFromRequest(r)
	pumpWS(w, r, []string{topicFloor}, []byte("hello"), sessionID)
}

// AdminStatsWS keeps the admin dashboard's overview numbers live, scoped to
// whichever period tab is currently showing. Sends only the inner content
// (see renderAdminStatsInnerHTML) — never the ws-connect wrapper itself,
// which belongs solely to the outerHTML swap done by period tab clicks.
func AdminStatsWS(w http.ResponseWriter, r *http.Request) {
	period := normalizePeriod(r.URL.Query().Get("period"))
	b, err := renderAdminStatsInnerHTML(period)
	if err != nil {
		http.Error(w, "Failed to load stats", http.StatusInternalServerError)
		return
	}
	sessionID, _ := auth.SessionIDFromRequest(r)
	pumpWS(w, r, []string{topicAdminStats(period)}, oobWrap("admin-stats", b), sessionID)
}
