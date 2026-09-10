package handlers

import (
	"bytes"
	"html/template"
	"net/http"
	"strconv"

	"bitebox/internal/auth"
	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
)

var nextStatusLabel = map[string]string{
	models.OrderStatusPreparing: "Start preparing",
	models.OrderStatusReady:     "Mark ready",
	models.OrderStatusServed:    "Mark served",
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

var departmentLabels = map[string]string{
	models.DepartmentSuperworker: "Manager",
	models.DepartmentWaiter:      "Waiter",
	models.DepartmentBar:         "Bar",
	models.DepartmentKitchen:     "Kitchen",
	models.DepartmentDJ:          "DJ Booth",
}

func departmentLabel(department string) string {
	if label, ok := departmentLabels[department]; ok {
		return label
	}
	return "Manager"
}

func WorkerHome(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r)
	showDJ := user.Role == models.RoleAdmin || user.Department == models.DepartmentDJ
	showOrders := user.Role == models.RoleAdmin || user.Department != models.DepartmentDJ

	tmpl := template.Must(template.ParseFiles("templates/worker_dashboard.html"))
	tmpl.Execute(w, map[string]interface{}{
		"ShowOrders":      showOrders,
		"ShowDJ":          showDJ,
		"Username":        user.Username,
		"DepartmentLabel": departmentLabel(user.Department),
		"VAPIDPublicKey":  PushVAPIDPublicKey(),
		"IconVersion":     IconVersion(),
	})
}

// orderFeedBucket maps a user to their order-feed topic/filter bucket:
// admins and "superworker" (manager) accounts get the fully unfiltered
// feed; bar/kitchen get their category-filtered feed (pending/preparing
// only); waiter gets the cross-category "ready" feed — the kitchen/bar →
// waiter handoff. DJ-department workers never reach this at all
// (RequireDepartment blocks them from /worker/orders/* entirely).
func orderFeedBucket(user models.User) string {
	if user.Role == models.RoleAdmin {
		return models.DepartmentSuperworker
	}
	switch user.Department {
	case models.DepartmentBar, models.DepartmentKitchen, models.DepartmentWaiter:
		return user.Department
	default:
		return models.DepartmentSuperworker
	}
}

func renderOrderFeedHTML(bucket string) ([]byte, error) {
	var orders []models.Order
	var err error
	switch bucket {
	case models.DepartmentBar, models.DepartmentKitchen:
		var cats []string
		cats, err = db.CategoryNamesForDepartment(bucket)
		if err == nil {
			orders, err = db.GetActiveOrdersByCategories(cats)
		}
	case models.DepartmentWaiter:
		orders, err = db.GetReadyOrders()
	default: // superworker (and admin, mapped to superworker above)
		orders, err = db.GetActiveOrders()
	}
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

// orderFeedBuckets lists every department with its own order-feed view —
// BroadcastOrderFeed re-renders and pushes to all of them, since a single
// order change can affect more than one at once (e.g. marking an order
// ready removes it from kitchen's feed, adds it to waiter's, and it was
// already visible in superworker's).
var orderFeedBuckets = []string{models.DepartmentSuperworker, models.DepartmentBar, models.DepartmentKitchen, models.DepartmentWaiter}

func BroadcastOrderFeed() {
	for _, bucket := range orderFeedBuckets {
		b, err := renderOrderFeedHTML(bucket)
		if err != nil {
			continue
		}
		Hub.Broadcast(topicWorkerOrders(bucket), oobWrap("order-feed", b))
	}
}

func renderOrderFeed(w http.ResponseWriter, bucket string) {
	b, err := renderOrderFeedHTML(bucket)
	if err != nil {
		http.Error(w, "Failed to load orders", http.StatusInternalServerError)
		return
	}
	w.Write(b)
	BroadcastOrderFeed()
}

// authorizeDepartmentMutation reports whether user may mutate orderID.
// Admin and superworker/waiter departments are unrestricted (unfiltered
// access is already correct for them); bar/kitchen may only act on an
// order that actually contains an item in their own department's
// categories. Closes a gap TODO.md flagged as accepted-for-now: the
// department split was previously a *read-side* filter only — a bar
// worker who knew or guessed a kitchen order's id could still POST a
// status/paid/cancel mutation on it directly. Symmetric to the read-side
// filter in GetActiveOrdersByCategories.
func authorizeDepartmentMutation(user models.User, orderID int) bool {
	if user.Role == models.RoleAdmin {
		return true
	}
	switch user.Department {
	case models.DepartmentBar, models.DepartmentKitchen:
		cats, err := db.CategoryNamesForDepartment(user.Department)
		if err != nil {
			return false
		}
		ok, err := db.OrderHasAnyCategory(orderID, cats)
		return err == nil && ok
	default: // superworker, waiter
		return true
	}
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
	if user, _ := UserFromContext(r); !authorizeDepartmentMutation(user, id) {
		http.Error(w, "403 Forbidden: not your department's order", http.StatusForbidden)
		return
	}
	if err := db.UpdateOrderStatus(id, status); err != nil {
		http.Error(w, "Failed to update order", http.StatusInternalServerError)
		return
	}
	user, _ := UserFromContext(r)
	if status == models.OrderStatusServed {
		db.SetServedBy(id, user.Username)
	}
	renderOrderFeed(w, orderFeedBucket(user))
	if order, err := db.GetOrderByID(id); err == nil {
		BroadcastTableStatus(order.TableNumber)
		BroadcastPlacedOrders(order.TableNumber, order.SessionID)
		if status == models.OrderStatusReady {
			if items, err := db.GetOrderItems(id); err == nil {
				pushReadyAlert(id, order.TableNumber, items)
				pushOrderReadyAlert(order.SessionID, id, order.TableNumber, items)
			}
		}
	}
}

// WorkerCancelOrder cancels a not-yet-served order and restores any stock
// it had reserved. Rejected (409) if the order already progressed past the
// point cancelling makes sense (served/completed/already cancelled).
func WorkerCancelOrder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid order id", http.StatusBadRequest)
		return
	}
	if user, _ := UserFromContext(r); !authorizeDepartmentMutation(user, id) {
		http.Error(w, "403 Forbidden: not your department's order", http.StatusForbidden)
		return
	}
	orderBefore, _ := db.GetOrderByID(id)
	tableNumber, err := db.CancelOrder(id)
	if err != nil {
		if err == db.ErrCannotCancel {
			http.Error(w, "This order can no longer be cancelled", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to cancel order", http.StatusInternalServerError)
		return
	}
	user, _ := UserFromContext(r)
	renderOrderFeed(w, orderFeedBucket(user))
	BroadcastTableStatus(tableNumber)
	BroadcastPlacedOrders(tableNumber, orderBefore.SessionID)
	BroadcastAllMenus()
	BroadcastAdminStats()
}

// WorkerMarkUnpaid reverses a "Mark Paid" — the real backend action behind
// the waiter UI's "Undo" on a payment toast, not a purely client-side revert
// (which would leave this client's screen disagreeing with everyone else's
// live view).
func WorkerMarkUnpaid(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid order id", http.StatusBadRequest)
		return
	}
	if user, _ := UserFromContext(r); !authorizeDepartmentMutation(user, id) {
		http.Error(w, "403 Forbidden: not your department's order", http.StatusForbidden)
		return
	}
	if err := db.UpdateOrderPaymentStatus(id, models.PaymentStatusUnpaid); err != nil {
		http.Error(w, "Failed to update payment status", http.StatusInternalServerError)
		return
	}
	user, _ := UserFromContext(r)
	renderOrderFeed(w, orderFeedBucket(user))
	if order, err := db.GetOrderByID(id); err == nil {
		BroadcastTableStatus(order.TableNumber)
		BroadcastPlacedOrders(order.TableNumber, order.SessionID)
	}
	BroadcastAdminStats()
}

func WorkerMarkPaid(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid order id", http.StatusBadRequest)
		return
	}
	if user, _ := UserFromContext(r); !authorizeDepartmentMutation(user, id) {
		http.Error(w, "403 Forbidden: not your department's order", http.StatusForbidden)
		return
	}
	if err := db.UpdateOrderPaymentStatus(id, models.PaymentStatusPaid); err != nil {
		http.Error(w, "Failed to update payment status", http.StatusInternalServerError)
		return
	}
	user, _ := UserFromContext(r)
	renderOrderFeed(w, orderFeedBucket(user))
	if order, err := db.GetOrderByID(id); err == nil {
		BroadcastTableStatus(order.TableNumber)
		BroadcastPlacedOrders(order.TableNumber, order.SessionID)
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

// renderAssistanceAlertsHTML renders the worker-facing "tables asking for
// help" panel — every table with AssistanceRequested set (see
// RequestAssistance in table.go), most-recently-requested first isn't
// tracked separately; GetAllTables already orders by number, which is fine
// for a handful of concurrent requests at this app's scale.
func renderAssistanceAlertsHTML() ([]byte, error) {
	tables, err := db.GetAllTables()
	if err != nil {
		return nil, err
	}
	var needsHelp []models.Table
	for _, t := range tables {
		if t.AssistanceRequested {
			needsHelp = append(needsHelp, t)
		}
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_assistance_alerts.html"))
	if err := tmpl.Execute(&buf, map[string]interface{}{"Tables": needsHelp}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BroadcastAssistanceAlerts pushes the current assistance-request list to
// every connected worker dashboard's alert panel (waiter/superworker/admin —
// see the route's RequireDepartment scope in main.go).
func BroadcastAssistanceAlerts() {
	b, err := renderAssistanceAlertsHTML()
	if err != nil {
		return
	}
	Hub.Broadcast(topicAssistance, oobWrap("assistance-alerts", b))
}

// WorkerAssistanceWS keeps the assistance-alert panel live.
func WorkerAssistanceWS(w http.ResponseWriter, r *http.Request) {
	b, err := renderAssistanceAlertsHTML()
	if err != nil {
		http.Error(w, "Failed to load assistance requests", http.StatusInternalServerError)
		return
	}
	sessionID, _ := auth.SessionIDFromRequest(r)
	pumpWS(w, r, []string{topicAssistance}, oobWrap("assistance-alerts", b), sessionID)
}
