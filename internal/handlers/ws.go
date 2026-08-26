package handlers

import (
	"fmt"
	"net/http"
	"strconv"
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
	topicMenu        = "menu"
	topicAdminMenu   = "admin:menu"
	topicAdminTables = "admin:tables"
)

func topicTableStatus(tableNumber int) string {
	return fmt.Sprintf("table:%d:status", tableNumber)
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

// oobWrap wraps an HTML fragment in an htmx out-of-band swap targeting the
// given element id, for pushing over a websocket via the htmx ws extension.
func oobWrap(id string, content []byte) []byte {
	b := make([]byte, 0, len(content)+64)
	b = append(b, `<div id="`...)
	b = append(b, id...)
	b = append(b, `" hx-swap-oob="true">`...)
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
// stops being valid while their socket is still open. worker_dashboard.html
// and admin_dashboard.html both listen for htmx:wsClose and redirect to
// /login when they see it — otherwise a deactivated account's already-open
// tab would keep receiving live updates indefinitely, looking exactly like
// they were never kicked out.
const wsKickCloseCode = 4001

// pumpWS upgrades the request, sends an initial snapshot, then relays every
// broadcast published to topic until the client disconnects. If sessionID
// is non-empty (staff-authenticated sockets only — guest-facing sockets
// like table/menu pass ""), the underlying session is periodically
// re-validated and the connection is force-closed with wsKickCloseCode the
// moment it's no longer valid.
func pumpWS(w http.ResponseWriter, r *http.Request, topic string, initial []byte, sessionID string) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch := Hub.Subscribe(topic)
	defer Hub.Unsubscribe(topic, ch)

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
		http.Error(w, "Failed to load orders", http.StatusInternalServerError)
		return
	}
	sessionID, _ := auth.SessionIDFromRequest(r)
	pumpWS(w, r, topicWorkerOrders(bucket), oobWrap("order-feed", b), sessionID)
}

func WorkerDJWS(w http.ResponseWriter, r *http.Request) {
	b, err := renderDJFeedHTML()
	if err != nil {
		http.Error(w, "Failed to load requests", http.StatusInternalServerError)
		return
	}
	sessionID, _ := auth.SessionIDFromRequest(r)
	pumpWS(w, r, topicWorkerDJ, oobWrap("dj-feed", b), sessionID)
}

func TableStatusWS(w http.ResponseWriter, r *http.Request) {
	tableNum, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil {
		http.Error(w, "Invalid table", http.StatusBadRequest)
		return
	}
	initial := append(oobWrap("order-status", renderOrderStatusHTML(tableNum)), oobWrap("song-status", renderSongStatusHTML(tableNum))...)
	if sessionCookie, err := r.Cookie(GuestSessionCookieName); err == nil {
		initial = append(initial, oobWrap("placed-orders", renderPlacedOrdersHTML(sessionCookie.Value))...)
	}
	pumpWS(w, r, topicTableStatus(tableNum), initial, "")
}

// MenuWS serves the guest-facing live menu: product availability/price/
// stock changes and the cart-refresh nudge (see menu.go) both broadcast here.
func MenuWS(w http.ResponseWriter, r *http.Request) {
	b, err := renderGuestProductListHTML()
	if err != nil {
		http.Error(w, "Failed to load menu", http.StatusInternalServerError)
		return
	}
	initial := oobWrap("product-list", b)
	if djSection, err := renderDJSectionHTML(); err == nil {
		initial = append(initial, oobWrap("dj-section", djSection)...)
	}
	pumpWS(w, r, topicMenu, initial, "")
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
	pumpWS(w, r, topicAdminMenu, oobWrap("menuList", b), sessionID)
}

// AdminTablesWS keeps the admin dashboard's table grid live — a guest
// claiming/leaving a table, or an admin force-releasing/adding/removing
// one, all broadcast here.
func AdminTablesWS(w http.ResponseWriter, r *http.Request) {
	b, err := renderTableListHTML()
	if err != nil {
		http.Error(w, "Failed to load tables", http.StatusInternalServerError)
		return
	}
	sessionID, _ := auth.SessionIDFromRequest(r)
	pumpWS(w, r, topicAdminTables, oobWrap("table-grid", b), sessionID)
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
	pumpWS(w, r, topicAdminStats(period), oobWrap("admin-stats", b), sessionID)
}
