package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
)

// The staff app (/worker): one page, tailored to who's signed in. Each role
// gets the panels it works from, in the order it reaches for them:
//
//	kitchen, bar  → Queue (their items), Sold out
//	waiter        → Deliver (ready orders), Collect (unpaid tables), Floor, Bookings
//	manager/admin → Orders (everything), Collect, Floor, Bookings, then Sold out and DJ requests under More: every station
//	dj            → Requests
//
// Pages outside the app (Floor, Bookings…) only appear if the account may
// open them (models.User.Can), so per-account access is respected here too.

type workerTab struct {
	Key, Label, Icon, Href string // Href set = a link out, else a panel
}

// workerKind is the staff-app layout a user gets.
func workerKind(u models.User) string {
	if u.Role == models.RoleAdmin {
		return "manager"
	}
	switch u.Department {
	case models.DepartmentKitchen, models.DepartmentBar, models.DepartmentWaiter, models.DepartmentDJ:
		return u.Department
	}
	return "manager"
}

func workerTabs(u models.User, kind string) []workerTab {
	orders := workerTab{Key: "queue", Label: "Orders", Icon: "list"}
	pay := workerTab{Key: "pay", Label: "Collect", Icon: "cash"}
	stock := workerTab{Key: "stock", Label: "Sold out", Icon: "ban"}
	dj := workerTab{Key: "dj", Label: "Requests", Icon: "music"}
	floor := workerTab{Key: "floor", Label: "Floor", Icon: "map", Href: "/worker/floor"}
	book := workerTab{Key: "bookings", Label: "Bookings", Icon: "cal", Href: "/worker/bookings"}
	var tabs []workerTab
	switch kind {
	case models.DepartmentKitchen, models.DepartmentBar:
		orders.Label = "Queue"
		tabs = []workerTab{orders, stock}
	case models.DepartmentWaiter:
		orders.Label = "Deliver"
		tabs = []workerTab{orders, pay, floor, book}
	case models.DepartmentDJ:
		tabs = []workerTab{dj}
	default:
		// Managers (and the admin) cover every station, DJ included.
		tabs = []workerTab{orders, pay, floor, book, stock, dj}
	}
	var out []workerTab
	for _, t := range tabs {
		if t.Href == "" || u.Can(t.Key) {
			out = append(out, t)
		}
	}
	return out
}

// workerChrome is everything the work-mode top bar, tabs and menu sheet need
// for user: their tabs (four on the bottom bar, the rest under More), the
// admin tools they can open, and the Work / Admin switch. onPage is true for
// Floor and Bookings, which are pages of their own rather than panels of the
// app: there the panel tabs become links back to it.
func workerChrome(user models.User, onPage bool) map[string]interface{} {
	kind := workerKind(user)
	tabs := workerTabs(user, kind)
	for i := range tabs {
		if onPage && tabs[i].Href == "" {
			tabs[i].Href = "/worker#" + tabs[i].Key
		}
	}
	main, more := tabs, []workerTab(nil)
	if len(tabs) > 4 {
		main, more = tabs[:4], tabs[4:]
	}
	inApp := map[string]bool{}
	has := map[string]bool{}
	for _, t := range tabs {
		inApp[t.Key] = true
		if t.Href == "" || onPage {
			has[t.Key] = true
		}
	}
	// Admin tools: the back-office pages that aren't part of work mode.
	var pages []workerTab
	if hasAdminMode(user) {
		urls := map[string]string{"dashboard": "/admin", "menu": "/admin/menu", "map": "/admin/map", "orders": "/admin/orders",
			"logs": "/admin/logs", "team": "/admin/team", "staff": "/admin/team", "settings": "/admin/settings", "system": "/admin/system"}
		for _, p := range models.BackOfficePages {
			if href := urls[p.Key]; href != "" && user.Can(p.Key) && !inApp[p.Key] {
				pages = append(pages, workerTab{Key: p.Key, Label: p.Name, Href: href})
			}
		}
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	chrome := map[string]interface{}{
		"Kind": kind, "Tabs": main, "More": more, "Pages": pages, "Has": has, "First": tabs[0].Key, "Active": "",
		"Username": user.Username, "Initials": initials(user.Username), "RoleLabel": departmentLabel(user.Department),
		"Served":  db.CountServedBy(user.Username, midnight),
		"ModeNow": "work", "ModeWork": "/worker",
	}
	if hasAdminMode(user) {
		chrome["ModeAdmin"] = adminStart(user)
	}
	return chrome
}

// fillPanels renders every panel's current content into the page itself, so the
// app shows real orders on the very first paint instead of waiting for the live
// connection. The websockets then only carry changes.
func fillPanels(data map[string]interface{}, user models.User) {
	has, _ := data["Has"].(map[string]bool)
	html := func(b []byte, err error) template.HTML {
		if err != nil {
			return ""
		}
		return template.HTML(b) // #nosec G203 -- our own html/template output
	}
	if has["queue"] {
		data["FeedHTML"] = html(renderOrderFeedHTML(orderFeedBucket(user)))
	}
	if has["pay"] {
		data["PayHTML"] = template.HTML(payListHTML()) // #nosec G203 -- our own html/template output
	}
	if has["dj"] {
		data["DJHTML"] = html(renderDJFeedHTML())
	}
	if help, _ := data["HasHelp"].(bool); help {
		data["HelpHTML"] = html(renderAssistanceAlertsHTML())
	}
}

func WorkerHome(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r)
	// The admin's live feed lives inside the admin area (AdminLive); /worker
	// is the staff app for everyone else.
	if user.Role == models.RoleAdmin {
		http.Redirect(w, r, "/admin/live", http.StatusSeeOther)
		return
	}
	settings, _ := db.GetSettings()
	kind := workerKind(user)
	data := workerChrome(user, false)
	data["VenueName"], data["V"] = settings.VenueName, assetVersion
	data["HasHelp"] = kind == models.DepartmentWaiter || kind == "manager"
	data["Bucket"] = orderFeedBucket(user)
	data["KeepAwake"] = kind == models.DepartmentKitchen || kind == models.DepartmentBar
	data["VAPIDPublicKey"], data["IconVersion"] = PushVAPIDPublicKey(), IconVersion()
	fillPanels(data, user)
	tmpl := template.Must(template.ParseFiles("templates/bk_layout.html", "templates/_worker_panels.html", "templates/worker_app.html"))
	if err := tmpl.ExecuteTemplate(w, "worker", data); err != nil {
		http.Error(w, "Failed to load the staff app", http.StatusInternalServerError)
	}
}

// pageFor renders a page that exists in both modes (Floor, Bookings) in the
// shell the visitor is in: the admin sidebar, or the staff app's chrome.
func pageFor(w http.ResponseWriter, r *http.Request, work bool, page, nav string, data map[string]interface{}) {
	if !work {
		staffPage(w, r, page, nav, data)
		return
	}
	user, _ := UserFromContext(r)
	for k, v := range workerChrome(user, true) {
		data[k] = v
	}
	data["Active"], data["Base"] = nav, "/worker"
	renderShell(w, r, "wk-layout", page, nav, data, "_worker_panels.html", "wk_layout.html")
}

// WorkFloor and WorkBookings are Floor and Bookings inside the staff app.
// The admin has no work side, so they get the back-office pages.
func WorkFloor(w http.ResponseWriter, r *http.Request) {
	if u, _ := UserFromContext(r); u.Role == models.RoleAdmin {
		http.Redirect(w, r, "/admin/floor", http.StatusSeeOther)
		return
	}
	floorPage(w, r, true)
}

func WorkBookings(w http.ResponseWriter, r *http.Request) {
	if u, _ := UserFromContext(r); u.Role == models.RoleAdmin {
		http.Redirect(w, r, "/admin/bookings", http.StatusSeeOther)
		return
	}
	bookingsPage(w, r, true)
}

// ----- Collect: unpaid tables -----

type payOrder struct {
	ID        int
	Items     string
	Total     float64
	Method    string
	Split     int
	PerPerson float64
	Status    string
	AgeMs     int64
}

type payTable struct {
	Number   int
	Label    string
	Total    float64
	Orders   []payOrder
	OldestMs int64
}

// unpaidWindow is how far back Collect looks: long enough for a night that
// runs past midnight, short enough that a stale forgotten order from last
// week doesn't nag every shift (the Orders page still has it).
const unpaidWindow = 18 * time.Hour

func recentUnpaid() []models.Order {
	from := time.Now().Add(-unpaidWindow).UTC().Format("2006-01-02 15:04:05")
	orders, _ := db.ListOrders(db.OrderFilter{Range: db.TimeRange{From: from}, Pay: models.PaymentStatusUnpaid}, 0, 300)
	out := orders[:0]
	for _, o := range orders {
		if o.Status != models.OrderStatusCancelled {
			out = append(out, o)
		}
	}
	return out
}

func renderPayList(w http.ResponseWriter) { w.Write(payListHTML()) }

func payListHTML() []byte {
	labels := tableNames()
	byTable := map[int]*payTable{}
	var order []int
	for _, o := range recentUnpaid() {
		pt := byTable[o.TableNumber]
		if pt == nil {
			label := labels[o.TableNumber]
			if label == "" {
				label = strconv.Itoa(o.TableNumber)
			}
			pt = &payTable{Number: o.TableNumber, Label: label, OldestMs: o.CreatedAt.UnixMilli()}
			byTable[o.TableNumber] = pt
			order = append(order, o.TableNumber)
		}
		var parts []string
		for _, it := range o.Items {
			parts = append(parts, fmt.Sprintf("%d× %s", it.Quantity, it.Name))
		}
		po := payOrder{ID: o.ID, Items: strings.Join(parts, ", "), Total: o.GrandTotal(), Method: o.PaymentMethod,
			Split: o.SplitCount, PerPerson: o.PerPersonTotal(), Status: o.Status, AgeMs: o.CreatedAt.UnixMilli()}
		pt.Orders = append(pt.Orders, po)
		pt.Total += po.Total
		if po.AgeMs < pt.OldestMs {
			pt.OldestMs = po.AgeMs
		}
	}
	tables := make([]payTable, 0, len(order))
	var sum float64
	for _, n := range order {
		// ListOrders is newest first; show a table's orders oldest first.
		pt := byTable[n]
		for i, j := 0, len(pt.Orders)-1; i < j; i, j = i+1, j-1 {
			pt.Orders[i], pt.Orders[j] = pt.Orders[j], pt.Orders[i]
		}
		tables = append(tables, *pt)
		sum += pt.Total
	}
	// Longest-waiting table first.
	for i := 1; i < len(tables); i++ {
		for j := i; j > 0 && tables[j].OldestMs < tables[j-1].OldestMs; j-- {
			tables[j], tables[j-1] = tables[j-1], tables[j]
		}
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_pay_list.html"))
	tmpl.Execute(&buf, map[string]interface{}{"Tables": tables, "Sum": sum, "Count": len(tables)})
	return buf.Bytes()
}

func WorkerPayList(w http.ResponseWriter, r *http.Request) { renderPayList(w) }

// WorkerPayTable marks every unpaid order at one table paid — "the table
// settled up" in one tap.
func WorkerPayTable(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil {
		http.Error(w, "Invalid table", http.StatusBadRequest)
		return
	}
	for _, o := range recentUnpaid() {
		if o.TableNumber == n {
			db.UpdateOrderPaymentStatus(o.ID, models.PaymentStatusPaid)
			BroadcastPlacedOrders(o.TableNumber, o.SessionID)
		}
	}
	renderPayList(w)
	BroadcastTableStatus(n)
	BroadcastOrderFeed()
	BroadcastAdminStats()
}

// ----- Sold out -----

// stockCategories is the categories an account may sell out: its own
// department's for kitchen/bar, everything (nil) for a manager or admin.
func stockCategories(u models.User) ([]string, bool) {
	switch workerKind(u) {
	case models.DepartmentKitchen, models.DepartmentBar:
		cats, err := db.CategoryNamesForDepartment(u.Department)
		if cats == nil {
			cats = []string{}
		}
		return cats, err == nil
	case "manager":
		return nil, true
	}
	return nil, false
}

type stockGroup struct {
	Category string
	Items    []models.Product
}

func WorkerStock(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r)
	cats, ok := stockCategories(user)
	if !ok {
		http.Error(w, "403 Forbidden", http.StatusForbidden)
		return
	}
	products, _ := db.ProductsInCategories(cats)
	var groups []stockGroup
	sold := 0
	for _, p := range products {
		if !p.IsAvailable {
			sold++
		}
		if n := len(groups); n == 0 || groups[n-1].Category != p.Category {
			groups = append(groups, stockGroup{Category: p.Category})
		}
		g := &groups[len(groups)-1]
		g.Items = append(g.Items, p)
	}
	var stations []stationView
	for _, st := range stationViews() {
		if canSwitchStation(user, st.Dept) {
			stations = append(stations, st)
		}
	}
	var buf bytes.Buffer
	template.Must(template.ParseFiles("templates/_stock_list.html", "templates/_stations.html")).Execute(&buf,
		map[string]interface{}{"Groups": groups, "Sold": sold, "Total": len(products), "Stations": stations, "Target": "/worker/stations/"})
	w.Write(buf.Bytes())
}

// WorkerStockToggle flips one item between on the menu and sold out, then
// answers with just that row (and tells every guest's menu).
func WorkerStockToggle(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r)
	cats, ok := stockCategories(user)
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	p, perr := db.GetProductByID(id)
	if !ok || err != nil || perr != nil {
		http.Error(w, "Item not found", http.StatusNotFound)
		return
	}
	if cats != nil {
		allowed := false
		for _, c := range cats {
			allowed = allowed || c == p.Category
		}
		if !allowed {
			http.Error(w, "403 Forbidden: not your department's item", http.StatusForbidden)
			return
		}
	}
	if err := db.SetProductAvailability(id, !p.IsAvailable); err != nil {
		http.Error(w, "Failed to update item", http.StatusInternalServerError)
		return
	}
	p.IsAvailable = !p.IsAvailable
	BroadcastAllMenus()
	var buf bytes.Buffer
	template.Must(template.ParseFiles("templates/_stock_list.html")).ExecuteTemplate(&buf, "stock-row", p)
	trigger, _ := json.Marshal(map[string]interface{}{"stockChanged": map[string]interface{}{"name": p.Name, "out": !p.IsAvailable}})
	w.Header().Set("HX-Trigger", string(trigger))
	w.Write(buf.Bytes())
}

// AdminLive is the staff app inside the admin shell: the same live panels as
// /worker (templates/_worker_panels.html), for the admin, with the admin
// sidebar around them. Floor and Bookings are in that sidebar already, so
// only the panels that live in the app itself are tabs here.
func AdminLive(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r)
	var tabs []workerTab
	has := map[string]bool{}
	for _, t := range workerTabs(user, "manager") {
		if t.Href == "" {
			tabs = append(tabs, t)
			has[t.Key] = true
		}
	}
	tabs[0].Label = "Orders"
	data := map[string]interface{}{
		"Kind": "manager", "Tabs": tabs, "Has": has, "HasHelp": true, "First": tabs[0].Key,
		"Bucket": orderFeedBucket(user), "VAPIDPublicKey": PushVAPIDPublicKey(),
	}
	fillPanels(data, user)
	staffPage(w, r, "adm_live.html", "live", data, "_worker_panels.html")
}
