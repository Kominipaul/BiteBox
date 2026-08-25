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

var nextStatusLabel = map[string]string{
	models.OrderStatusPreparing: "Start Preparing",
	models.OrderStatusServed:    "Mark Served",
	models.OrderStatusCompleted: "Mark Completed",
}

type orderView struct {
	models.Order
	NextStatus string
	NextLabel  string
	HasNext    bool
}

func buildOrderViews(orders []models.Order) []orderView {
	views := make([]orderView, 0, len(orders))
	for _, o := range orders {
		v := orderView{Order: o}
		if next, ok := models.NextOrderStatus(o.Status); ok {
			v.NextStatus = next
			v.NextLabel = nextStatusLabel[next]
			v.HasNext = true
		}
		views = append(views, v)
	}
	return views
}

func WorkerHome(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("templates/worker_dashboard.html"))
	tmpl.Execute(w, nil)
}

func renderOrderFeedHTML() ([]byte, error) {
	orders, err := db.GetActiveOrders()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_order_feed.html"))
	if err := tmpl.Execute(&buf, map[string]interface{}{"Orders": buildOrderViews(orders)}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BroadcastOrderFeed pushes an already-rendered order feed fragment to every
// worker dashboard connected over the orders websocket.
func BroadcastOrderFeed(b []byte) {
	Hub.Broadcast(topicWorkerOrders, oobWrap("order-feed", b))
}

func renderOrderFeed(w http.ResponseWriter) {
	b, err := renderOrderFeedHTML()
	if err != nil {
		http.Error(w, "Failed to load orders", http.StatusInternalServerError)
		return
	}
	w.Write(b)
	BroadcastOrderFeed(b)
}

func WorkerOrdersFeed(w http.ResponseWriter, r *http.Request) {
	renderOrderFeed(w)
}

func WorkerUpdateOrderStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid order id", http.StatusBadRequest)
		return
	}
	status := r.FormValue("status")
	if !models.IsValidOrderStatus(status) {
		http.Error(w, "Invalid order status", http.StatusBadRequest)
		return
	}
	if err := db.UpdateOrderStatus(id, status); err != nil {
		http.Error(w, "Failed to update order", http.StatusInternalServerError)
		return
	}
	renderOrderFeed(w)
	if order, err := db.GetOrderByID(id); err == nil {
		BroadcastTableStatus(order.TableNumber)
	}
}

func WorkerMarkPaid(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid order id", http.StatusBadRequest)
		return
	}
	if err := db.UpdateOrderPaymentStatus(id, models.PaymentStatusPaid); err != nil {
		http.Error(w, "Failed to update payment status", http.StatusInternalServerError)
		return
	}
	renderOrderFeed(w)
	if order, err := db.GetOrderByID(id); err == nil {
		BroadcastTableStatus(order.TableNumber)
	}
	BroadcastAdminStats()
}

func renderDJFeedHTML() ([]byte, error) {
	requests, err := db.GetPendingSongRequests()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_dj_feed.html"))
	if err := tmpl.Execute(&buf, map[string]interface{}{"Requests": requests}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BroadcastDJFeed pushes an already-rendered DJ feed fragment to every
// worker dashboard connected over the DJ websocket.
func BroadcastDJFeed(b []byte) {
	Hub.Broadcast(topicWorkerDJ, oobWrap("dj-feed", b))
}

func renderDJFeed(w http.ResponseWriter) {
	b, err := renderDJFeedHTML()
	if err != nil {
		http.Error(w, "Failed to load requests", http.StatusInternalServerError)
		return
	}
	w.Write(b)
	BroadcastDJFeed(b)
}

func WorkerDJFeed(w http.ResponseWriter, r *http.Request) {
	renderDJFeed(w)
}

func WorkerDJAccept(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid request id", http.StatusBadRequest)
		return
	}
	db.UpdateSongRequestStatus(id, models.SongRequestStatusAccepted)
	renderDJFeed(w)
	if req, err := db.GetSongRequestByID(id); err == nil && req.TableNumber > 0 {
		BroadcastSongStatus(req.TableNumber)
	}
}

func WorkerDJReject(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid request id", http.StatusBadRequest)
		return
	}
	db.UpdateSongRequestStatus(id, models.SongRequestStatusRejected)
	renderDJFeed(w)
	if req, err := db.GetSongRequestByID(id); err == nil && req.TableNumber > 0 {
		BroadcastSongStatus(req.TableNumber)
	}
}
