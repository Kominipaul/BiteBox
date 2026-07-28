package handlers

import (
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
	}

	products, _ := db.GetProducts()

	if table.HostSessionID == sessionID {
		guestCart := h.Cart.Get(sessionID)
		tmpl := template.Must(template.ParseFiles("templates/host_menu.html"))
		tmpl.Execute(w, map[string]interface{}{
			"TableNumber": tableNum,
			"Products":    products,
			"Cart":        guestCart,
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

// OrderStatusPoll renders the guest-facing "your order status" widget,
// polled from host_menu.html.
func OrderStatusPoll(w http.ResponseWriter, r *http.Request) {
	tableNum, _ := strconv.Atoi(chi.URLParam(r, "number"))

	order, err := db.GetActiveOrderForTable(tableNum)
	tmpl := template.Must(template.ParseFiles("templates/_order_status.html"))
	if err != nil {
		tmpl.Execute(w, map[string]interface{}{"HasOrder": false})
		return
	}
	tmpl.Execute(w, map[string]interface{}{
		"HasOrder": true,
		"Order":    order,
	})
}
