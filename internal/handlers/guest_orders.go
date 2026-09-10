package handlers

import (
	"bytes"
	"html/template"
	"net/http"
	"strconv"

	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
)

// placedOrdersLimit bounds the guest-facing "Your orders" list — not
// pagination, just a sane cap on a single table visit's history.
const placedOrdersLimit = 20

// renderPlacedOrdersHTML renders the guest-facing "Your orders" list for a
// session — every order they've placed at this table, newest first.
func renderPlacedOrdersHTML(sessionID string) []byte {
	orders, err := db.GetOrdersBySession(sessionID, placedOrdersLimit)
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_placed_orders.html"))
	if err != nil {
		orders = nil
	}
	tmpl.Execute(&buf, map[string]interface{}{"Orders": orders})
	return buf.Bytes()
}

// BroadcastPlacedOrders pushes a fresh "Your orders" list to the one guest
// session it belongs to — called whenever one of that guest's orders
// changes (new order, status update, cancel, refund request). This rides
// topicSessionOrders, not the shared per-table topic: order history,
// payment status, and notes are this guest's own, and /table/{n}/ws has no
// access control gating who else can subscribe to a given table (see
// topicSessionOrders and TableStatusWS).
func BroadcastPlacedOrders(tableNumber int, sessionID string) {
	Hub.Broadcast(topicSessionOrders(sessionID), oobWrap("placed-orders", renderPlacedOrdersHTML(sessionID)))
}

// GuestCancelOrder lets a guest cancel their own not-yet-served order.
// Unpaid orders cancel instantly (nothing to reverse). Already-paid orders
// can't be self-cancelled — there's no payment gateway to reverse a charge
// through, and cash already collected needs handing back in person — so
// instead this flags the order for staff (RefundRequested), who complete
// the actual cancellation themselves once they've handled the refund.
func GuestCancelOrder(w http.ResponseWriter, r *http.Request) {
	sessionCookie, err := r.Cookie(GuestSessionCookieName)
	if err != nil {
		http.Error(w, "Not authorized", http.StatusForbidden)
		return
	}
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid order id", http.StatusBadRequest)
		return
	}
	order, err := db.GetOrderByID(id)
	if err != nil || order.SessionID != sessionCookie.Value {
		http.Error(w, "Order not found", http.StatusNotFound)
		return
	}

	if order.PaymentStatus == models.PaymentStatusPaid {
		if order.Status != models.OrderStatusPending && order.Status != models.OrderStatusPreparing {
			http.Error(w, "This order can no longer be cancelled", http.StatusConflict)
			return
		}
		if err := db.RequestRefund(id); err != nil {
			http.Error(w, "Failed to request a refund", http.StatusInternalServerError)
			return
		}
		BroadcastOrderFeed()
		BroadcastPlacedOrders(order.TableNumber, order.SessionID)
		w.Write(renderPlacedOrdersHTML(order.SessionID))
		return
	}

	tableNumber, err := db.CancelOrder(id)
	if err != nil {
		if err == db.ErrCannotCancel {
			http.Error(w, "This order can no longer be cancelled", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to cancel order", http.StatusInternalServerError)
		return
	}
	BroadcastOrderFeed()
	BroadcastTableStatus(tableNumber)
	BroadcastPlacedOrders(tableNumber, order.SessionID)
	BroadcastAllMenus()
	BroadcastAdminStats()
	w.Write(renderPlacedOrdersHTML(order.SessionID))
}
