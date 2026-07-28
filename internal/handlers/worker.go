package handlers

import (
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

func renderOrderFeed(w http.ResponseWriter) {
	orders, _ := db.GetActiveOrders()
	tmpl := template.Must(template.ParseFiles("templates/_order_feed.html"))
	tmpl.Execute(w, map[string]interface{}{"Orders": buildOrderViews(orders)})
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
}

func renderDJFeed(w http.ResponseWriter) {
	requests, _ := db.GetPendingSongRequests()
	tmpl := template.Must(template.ParseFiles("templates/_dj_feed.html"))
	tmpl.Execute(w, map[string]interface{}{"Requests": requests})
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
}

func WorkerDJReject(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid request id", http.StatusBadRequest)
		return
	}
	db.UpdateSongRequestStatus(id, models.SongRequestStatusRejected)
	renderDJFeed(w)
}
