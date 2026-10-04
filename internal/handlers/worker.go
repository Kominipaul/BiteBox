package handlers

import (
	"bytes"
	"html/template"
	"net/http"
	"sort"
	"strconv"

	"bitebox/internal/auth"
	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
)

var nextStatusLabel = map[string]string{
	models.OrderStatusPreparing: "Start preparing",
	models.OrderStatusReady:     "Mark ready",
	models.OrderStatusServed:    "Delivered",
}

// lineView is one item on a ticket. Mine is false for items that belong to
// the other department on a mixed order (a burger on a bar ticket): shown
// dimmed, for context only.
type lineView struct {
	models.OrderItem
	Mine bool
}

type orderView struct {
	models.Order
	Lines      []lineView
	Table      string
	NextStatus string
	NextLabel  string
	HasNext    bool
	Others     int  // lines that aren't this department's
	ShowPay    bool // waiter / manager also collect payment from the card
	CanPrint   bool
}

type orderGroup struct {
	Key, Title string
	Orders     []orderView
}

type prepLine struct {
	Name string
	Qty  int
}

// feedRole is how a bucket's feed reads: what each stage is called and what
// an empty feed says.
var feedRoles = map[string]struct{ Empty string }{
	models.DepartmentKitchen:     {"All caught up. No food orders waiting."},
	models.DepartmentBar:         {"All caught up. No drink orders waiting."},
	models.DepartmentWaiter:      {"Nothing to deliver right now."},
	models.DepartmentSuperworker: {"No active orders."},
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

// buildFeed turns a bucket's orders into stage groups (oldest first inside
// each) plus the batch summary a prep station works from.
func buildFeed(bucket string, orders []models.Order) map[string]interface{} {
	station := bucket == models.DepartmentKitchen || bucket == models.DepartmentBar
	var mine map[string]bool
	var cats map[int]string
	if station {
		names, _ := db.CategoryNamesForDepartment(bucket)
		mine = map[string]bool{}
		for _, n := range names {
			mine[n] = true
		}
		cats = db.ProductCategories()
	}
	labels := tableNames()
	summary := map[string]int{}
	groups := map[string]*orderGroup{}
	var order []string
	add := func(key, title string) *orderGroup {
		if g, ok := groups[key]; ok {
			return g
		}
		groups[key] = &orderGroup{Key: key, Title: title}
		order = append(order, key)
		return groups[key]
	}
	switch bucket {
	case models.DepartmentWaiter:
		add("ready", "Ready to deliver")
	case models.DepartmentSuperworker:
		add("pending", "New")
		add("preparing", "In progress")
		add("ready", "Ready to serve")
	default:
		add("pending", "New")
		add("preparing", "In progress")
	}
	for _, o := range orders {
		v := orderView{Order: o, Table: labels[o.TableNumber], ShowPay: !station, CanPrint: bucket != models.DepartmentWaiter}
		if v.Table == "" {
			v.Table = strconv.Itoa(o.TableNumber)
		}
		if next, ok := models.NextOrderStatus(o.Status); ok {
			v.NextStatus, v.NextLabel, v.HasNext = next, nextStatusLabel[next], true
		}
		for _, it := range o.Items {
			l := lineView{OrderItem: it, Mine: !station || mine[cats[it.ProductID]]}
			if !l.Mine {
				v.Others++
			} else if station {
				summary[it.Name] += it.Quantity
			}
			v.Lines = append(v.Lines, l)
		}
		if g, ok := groups[o.Status]; ok {
			g.Orders = append(g.Orders, v)
		}
	}
	feed := make([]orderGroup, 0, len(order))
	total := 0
	for _, k := range order {
		feed = append(feed, *groups[k])
		total += len(groups[k].Orders)
	}
	var prep []prepLine
	for n, q := range summary {
		prep = append(prep, prepLine{n, q})
	}
	sort.Slice(prep, func(i, j int) bool {
		if prep[i].Qty != prep[j].Qty {
			return prep[i].Qty > prep[j].Qty
		}
		return prep[i].Name < prep[j].Name
	})
	return map[string]interface{}{"Groups": feed, "Total": total, "Prep": prep, "Empty": feedRoles[bucket].Empty, "Bucket": bucket}
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
	if err := tmpl.Execute(&buf, buildFeed(bucket, orders)); err != nil {
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
	w.Write(b) // #nosec G705 -- our own html/template output
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
func WorkerMarkUnpaid(w http.ResponseWriter, r *http.Request) { setOrderPaid(w, r, false) }

func WorkerMarkPaid(w http.ResponseWriter, r *http.Request) { setOrderPaid(w, r, true) }

// setOrderPaid flips one order's payment. Called from an order card it
// answers with the order feed; from the Collect panel (view=pay) with that
// panel — the same action either way, so every screen agrees.
func setOrderPaid(w http.ResponseWriter, r *http.Request, paid bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid order id", http.StatusBadRequest)
		return
	}
	user, _ := UserFromContext(r)
	if !authorizeDepartmentMutation(user, id) {
		http.Error(w, "403 Forbidden: not your department's order", http.StatusForbidden)
		return
	}
	status := models.PaymentStatusUnpaid
	if paid {
		status = models.PaymentStatusPaid
	}
	if err := db.UpdateOrderPaymentStatus(id, status); err != nil {
		http.Error(w, "Failed to update payment status", http.StatusInternalServerError)
		return
	}
	afterPaymentChange(w, r, user, id)
}

func afterPaymentChange(w http.ResponseWriter, r *http.Request, user models.User, id int) {
	if r.FormValue("view") == "pay" {
		renderPayList(w)
		BroadcastOrderFeed()
	} else {
		renderOrderFeed(w, orderFeedBucket(user))
	}
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
	if err := tmpl.Execute(&buf, map[string]interface{}{"Requests": requests, "Tables": tableNames()}); err != nil {
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

// renderAssistanceAlertsHTML renders the staff app's "tables asking for
// help" strip, longest-waiting first.
func renderAssistanceAlertsHTML() ([]byte, error) {
	calls, err := db.PendingHelp()
	if err != nil {
		return nil, err
	}
	labels := tableNames()
	type callView struct {
		Number  int
		Label   string
		SinceMs int64
		Reason  string // staff wording, "" = not given
	}
	var views []callView
	for _, c := range calls {
		l := labels[c.Number]
		if l == "" {
			l = strconv.Itoa(c.Number)
		}
		views = append(views, callView{c.Number, l, c.Since.UnixMilli(), models.AssistReasonLabel(c.Reason)})
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_assistance_alerts.html"))
	if err := tmpl.Execute(&buf, map[string]interface{}{"Calls": views}); err != nil {
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
