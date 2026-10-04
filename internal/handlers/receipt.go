package handlers

import (
	"html/template"
	"net/http"
	"strconv"

	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
)

// AdminOrderReceipt renders a demo Greek-retail-receipt-shaped printout for
// one order: ΑΦΜ/ΔΟΥ, a sequential-looking receipt number, a VAT-by-rate
// breakdown table, a fabricated MARK. This is demo structure only — see
// models.Order's VATBreakdown/ReceiptNumber/DemoMARK doc comments and the
// banner baked into the template itself. Nothing here talks to myDATA/ΑΑΔΕ
// or any other government system, and it was built explicitly as non-live
// demo data (no real venue, no real tax numbers) — see TODO.md.
func AdminOrderReceipt(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid order id", http.StatusBadRequest)
		return
	}
	order, err := db.GetOrderByID(id)
	if err != nil {
		http.Error(w, "Order not found", http.StatusNotFound)
		return
	}
	items, err := db.GetOrderItems(id)
	if err != nil {
		http.Error(w, "Failed to load order items", http.StatusInternalServerError)
		return
	}
	order.Items = items

	renderReceipt(w, order)
}

// GuestOrderReceipt is the same printout for a guest's own order ("Receipt"
// on Your orders): only ever an order placed from the caller's own guest
// session, the same ownership rule as GuestCancelOrder.
func GuestOrderReceipt(w http.ResponseWriter, r *http.Request) {
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
	items, err := db.GetOrderItems(id)
	if err != nil {
		http.Error(w, "Failed to load order items", http.StatusInternalServerError)
		return
	}
	order.Items = items
	renderReceipt(w, order)
}

func renderReceipt(w http.ResponseWriter, order models.Order) {
	settings, _ := db.GetSettings()
	tmpl := template.Must(template.ParseFiles("templates/_receipt.html"))
	tmpl.Execute(w, map[string]interface{}{
		"Order":    order,
		"Settings": settings,
	})
}
