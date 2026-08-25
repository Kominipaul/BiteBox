package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"bitebox/internal/wshub"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

// Hub fans out server-rendered HTML fragments to connected dashboards, so
// worker/guest views update live instead of polling on a timer.
var Hub = wshub.NewHub()

const (
	topicWorkerOrders = "worker:orders"
	topicWorkerDJ     = "worker:dj"
	topicMenu         = "menu"
	topicAdminStats   = "admin:stats"
)

func topicTableStatus(tableNumber int) string {
	return fmt.Sprintf("table:%d:status", tableNumber)
}

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

// pumpWS upgrades the request, sends an initial snapshot, then relays every
// broadcast published to topic until the client disconnects.
func pumpWS(w http.ResponseWriter, r *http.Request, topic string, initial []byte) {
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

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-closed:
			return
		}
	}
}

func WorkerOrdersWS(w http.ResponseWriter, r *http.Request) {
	b, err := renderOrderFeedHTML()
	if err != nil {
		http.Error(w, "Failed to load orders", http.StatusInternalServerError)
		return
	}
	pumpWS(w, r, topicWorkerOrders, oobWrap("order-feed", b))
}

func WorkerDJWS(w http.ResponseWriter, r *http.Request) {
	b, err := renderDJFeedHTML()
	if err != nil {
		http.Error(w, "Failed to load requests", http.StatusInternalServerError)
		return
	}
	pumpWS(w, r, topicWorkerDJ, oobWrap("dj-feed", b))
}

func TableStatusWS(w http.ResponseWriter, r *http.Request) {
	tableNum, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil {
		http.Error(w, "Invalid table", http.StatusBadRequest)
		return
	}
	initial := append(oobWrap("order-status", renderOrderStatusHTML(tableNum)), oobWrap("song-status", renderSongStatusHTML(tableNum))...)
	pumpWS(w, r, topicTableStatus(tableNum), initial)
}

// MenuWS serves the guest-facing live menu: product availability/price
// changes and the cart-refresh nudge (see menu.go) both broadcast here.
func MenuWS(w http.ResponseWriter, r *http.Request) {
	b, err := renderGuestProductListHTML()
	if err != nil {
		http.Error(w, "Failed to load menu", http.StatusInternalServerError)
		return
	}
	pumpWS(w, r, topicMenu, oobWrap("product-list", b))
}

// AdminStatsWS keeps the admin dashboard's "Today's Overview" numbers live.
func AdminStatsWS(w http.ResponseWriter, r *http.Request) {
	b, err := renderAdminStatsHTML()
	if err != nil {
		http.Error(w, "Failed to load stats", http.StatusInternalServerError)
		return
	}
	pumpWS(w, r, topicAdminStats, oobWrap("admin-stats", b))
}
