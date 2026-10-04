package handlers

import (
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
)

func workerReq(method, url string, u models.User, params map[string]string) *http.Request {
	r := httptest.NewRequest(method, url, nil)
	rc := chi.NewRouteContext()
	for k, v := range params {
		rc.URLParams.Add(k, v)
	}
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rc)
	return r.WithContext(context.WithValue(ctx, userContextKey, u))
}

func staff(dept string) models.User {
	return models.User{ID: 1, Username: dept + "1", Role: models.RoleWorker, Department: dept, IsActive: true}
}

// TestStaffAppTailoredPerRole: each account type gets the panels it works
// from, and no others.
func TestStaffAppTailoredPerRole(t *testing.T) {
	cases := []struct {
		name string
		user models.User
		has  []string
		not  []string
	}{
		{"kitchen", staff(models.DepartmentKitchen), []string{`data-tab="queue"`, `data-tab="stock"`, ">Queue<"}, []string{`data-tab="pay"`, `data-tab="dj"`, "assistance/ws"}},
		{"bar", staff(models.DepartmentBar), []string{`data-tab="stock"`, ">Queue<"}, []string{`data-tab="pay"`}},
		{"waiter", staff(models.DepartmentWaiter), []string{`data-tab="queue"`, `data-tab="pay"`, ">Deliver<", "assistance/ws"}, []string{`data-tab="stock"`, `data-tab="dj"`}},
		{"manager", staff(models.DepartmentSuperworker), []string{`data-tab="queue"`, `data-tab="pay"`, `data-goto="stock"`, `data-goto="dj"`, `id="dj-feed"`, "assistance/ws"}, nil},
		{"dj", staff(models.DepartmentDJ), []string{`data-tab="dj"`}, []string{`data-tab="queue"`, `data-tab="pay"`, "assistance/ws"}},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		WorkerHome(w, workerReq(http.MethodGet, "/worker", c.user, nil))
		body := w.Body.String()
		if w.Code != http.StatusOK || !strings.Contains(body, "</html>") {
			t.Errorf("%s: status %d", c.name, w.Code)
			continue
		}
		for _, h := range c.has {
			if !strings.Contains(body, h) {
				t.Errorf("%s: missing %q", c.name, h)
			}
		}
		for _, n := range c.not {
			if strings.Contains(body, n) {
				t.Errorf("%s: should not have %q", c.name, n)
			}
		}
	}
}

// TestSoldOutBoardRespectsDepartment: a station can only sell out its own
// categories; managers any; waiters none. The toggle answers with the row.
func TestSoldOutBoardRespectsDepartment(t *testing.T) {
	cats, _ := db.GetCategories()
	var kitchenCat, barCat string
	for _, c := range cats {
		if c.Department == "kitchen" && kitchenCat == "" {
			kitchenCat = c.Name
		}
		if c.Department == "bar" && barCat == "" {
			barCat = c.Name
		}
	}
	food, _ := db.CreateProduct("Soldout Burger", 9, -1, kitchenCat, "", "", 24)
	beer, _ := db.CreateProduct("Soldout Beer", 4, -1, barCat, "", "", 24)
	toggle := func(u models.User, id int) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		WorkerStockToggle(w, workerReq(http.MethodPost, "/worker/stock/x/toggle", u, map[string]string{"id": strconv.Itoa(id)}))
		return w
	}
	if w := toggle(staff(models.DepartmentBar), food); w.Code != http.StatusForbidden {
		t.Fatalf("bar selling out a kitchen item: %d, want 403", w.Code)
	}
	if p, _ := db.GetProductByID(food); !p.IsAvailable {
		t.Fatal("a refused toggle still changed the item")
	}
	if w := toggle(staff(models.DepartmentWaiter), food); w.Code == http.StatusOK {
		t.Fatal("a waiter can sell items out")
	}
	w := toggle(staff(models.DepartmentKitchen), food)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Sold out") || !strings.Contains(w.Header().Get("HX-Trigger"), `"out":true`) {
		t.Fatalf("kitchen toggle: %d %s / %s", w.Code, w.Body, w.Header().Get("HX-Trigger"))
	}
	if p, _ := db.GetProductByID(food); p.IsAvailable {
		t.Fatal("item still on the menu after selling out")
	}
	if w := toggle(staff(models.DepartmentSuperworker), beer); w.Code != http.StatusOK {
		t.Fatalf("manager toggling a bar item: %d", w.Code)
	}
	// The board lists only the station's own items.
	lw := httptest.NewRecorder()
	WorkerStock(lw, workerReq(http.MethodGet, "/worker/stock", staff(models.DepartmentBar), nil))
	if b := lw.Body.String(); !strings.Contains(b, "Soldout Beer") || strings.Contains(b, "Soldout Burger") {
		t.Fatalf("bar board should list bar items only")
	}
}

// TestCollectGroupsUnpaidByTable: Collect lists each table's unpaid orders
// with a total, and settling a table marks all of them paid.
func TestCollectGroupsUnpaidByTable(t *testing.T) {
	pid, _ := db.CreateProduct("Collect Coffee", 3, -1, models.CategoryOther, "", "", 24)
	db.CreateTable(9901)
	db.CreateTable(9902)
	items := []models.OrderItem{{ProductID: pid, Name: "Collect Coffee", Price: 3, Quantity: 2}}
	var ids []int
	for _, table := range []int{9901, 9901, 9902} {
		id, err := db.CreateOrder(table, "s", models.PaymentMethodCash, models.PaymentStatusUnpaid, "", 6, 0, 1, items)
		if err != nil {
			t.Fatal(err)
		}
		db.UpdateOrderStatus(id, models.OrderStatusServed)
		ids = append(ids, id)
	}
	cancelled, _ := db.CreateOrder(9902, "s", models.PaymentMethodCash, models.PaymentStatusUnpaid, "", 6, 0, 1, items)
	db.UpdateOrderStatus(cancelled, models.OrderStatusCancelled)

	w := httptest.NewRecorder()
	renderPayList(w)
	body := w.Body.String()
	if !strings.Contains(body, "€12.00") || !strings.Contains(body, "€6.00") {
		t.Fatalf("expected a €12.00 table and a €6.00 table:\n%s", body)
	}
	if strings.Contains(body, "#"+strconv.Itoa(cancelled)+"<") || strings.Contains(body, "#"+strconv.Itoa(cancelled)+" ") {
		t.Fatal("a cancelled order is shown as owing")
	}

	pw := httptest.NewRecorder()
	WorkerPayTable(pw, workerReq(http.MethodPost, "/worker/pay/table/9901/paid", staff(models.DepartmentWaiter), map[string]string{"number": "9901"}))
	for _, id := range ids[:2] {
		if o, _ := db.GetOrderByID(id); o.PaymentStatus != models.PaymentStatusPaid {
			t.Fatalf("order %d not paid after settling its table", id)
		}
	}
	if o, _ := db.GetOrderByID(ids[2]); o.PaymentStatus == models.PaymentStatusPaid {
		t.Fatal("settling one table paid another's order")
	}
}

// TestAdminUsesTheAppInsideAdmin: the admin's feed lives under /admin
// (sidebar, admin look); /worker sends admins there and stays the staff app
// for everyone else; other accounts can't open the admin copy.
func TestAdminUsesTheAppInsideAdmin(t *testing.T) {
	admin := models.User{ID: 1, Username: "boss", Role: models.RoleAdmin, IsActive: true}

	w := httptest.NewRecorder()
	WorkerHome(w, workerReq(http.MethodGet, "/worker", admin, nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/live" {
		t.Fatalf("admin on /worker: %d -> %q, want redirect to /admin/live", w.Code, w.Header().Get("Location"))
	}

	w = httptest.NewRecorder()
	AdminLive(w, workerReq(http.MethodGet, "/admin/live", admin, nil))
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "</html>") {
		t.Fatalf("admin live: %d", w.Code)
	}
	for _, want := range []string{`data-tab="queue"`, `data-tab="pay"`, `data-tab="stock"`, `data-tab="dj"`,
		`href="/admin/live"`, `id="order-feed"`, "assistance/ws", `class="side"`, "wk-embed"} {
		if !strings.Contains(body, want) {
			t.Errorf("admin live missing %q", want)
		}
	}
	// The admin shell owns #menuBtn/#toast; the app must not duplicate them.
	if strings.Count(body, `id="toast"`) != 1 || strings.Contains(body, `id="sheet"`) {
		t.Error("app elements clash with the admin shell's ids")
	}

	// A staff account never gets the admin copy, and no admin page offers it.
	if staff(models.DepartmentSuperworker).Can("live") {
		t.Error("a manager can open the admin-only live page")
	}
	w = httptest.NewRecorder()
	WorkerHome(w, workerReq(http.MethodGet, "/worker", staff(models.DepartmentWaiter), nil))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "/admin/live") {
		t.Errorf("waiter on /worker: %d, must stay the staff app", w.Code)
	}
}

func mgr() models.User {
	u := staff(models.DepartmentSuperworker)
	u.ID = 9001
	u.Username = "mgr"
	return u
}

// TestManagerIsSuperstaffNotOwner pins where the manager's power ends: the
// pages for running and fixing the day are theirs by default; everything that
// could hurt the business if misused stays admin-only, even if someone tried
// to grant it.
func TestManagerIsSuperstaffNotOwner(t *testing.T) {
	m := mgr()
	for _, k := range []string{"dashboard", "bookings", "floor", "menu", "orders", "logs", "team"} {
		if !m.Can(k) {
			t.Errorf("manager can't open %q", k)
		}
	}
	for _, k := range []string{"settings", "staff", "system", "live", "map"} {
		if m.Can(k) {
			t.Errorf("manager can open %q, which is owner-only", k)
		}
	}
	// A custom grant still can't unlock an admin-only page.
	m.PagesCustom, m.Pages = true, []string{"settings", "staff", "system", "orders"}
	if m.Can("settings") || m.Can("staff") || m.Can("system") || !m.Can("orders") {
		t.Error("custom access should grant orders but never the admin-only pages")
	}
	// A waiter keeps the small default.
	if w := staff(models.DepartmentWaiter); w.Can("menu") || w.Can("orders") || w.Can("team") {
		t.Error("a waiter got manager pages")
	}
}

// TestManagerHelpsStaffButNotAdmins: resetting a password and signing a
// device out work on floor staff; admins, other managers and self are off
// limits, and the manager only ever sees the accounts they can help.
func TestManagerHelpsStaffButNotAdmins(t *testing.T) {
	hash := "x"
	waiterID, _ := db.CreateUser("help_waiter", hash, models.RoleWorker, models.DepartmentWaiter)
	otherMgrID, _ := db.CreateUser("help_mgr2", hash, models.RoleWorker, models.DepartmentSuperworker)
	adminID, _ := db.CreateUser("help_admin", hash, models.RoleAdmin, "")
	m := mgr()
	m.ID = 424242

	call := func(h http.HandlerFunc, id int, form string) *httptest.ResponseRecorder {
		r := workerReq(http.MethodPost, "/x?from=editor", m, map[string]string{"id": strconv.Itoa(id)})
		r.Body = http.NoBody
		if form != "" {
			r = httptest.NewRequest(http.MethodPost, "/x?from=editor", strings.NewReader(form))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rc := chi.NewRouteContext()
			rc.URLParams.Add("id", strconv.Itoa(id))
			r = r.WithContext(context.WithValue(context.WithValue(r.Context(), chi.RouteCtxKey, rc), userContextKey, m))
		}
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	if w := call(AdminResetStaffPassword, waiterID, "password=Newpass-99"); w.Code != http.StatusOK {
		t.Fatalf("manager resetting a waiter's password: %d %s", w.Code, w.Body)
	}
	if w := call(AdminStaffSignOut, waiterID, ""); w.Code >= 400 {
		t.Fatalf("manager signing a waiter out: %d", w.Code)
	}
	for _, id := range []int{adminID, otherMgrID} {
		if w := call(AdminResetStaffPassword, id, "password=hijacked1"); w.Code != http.StatusForbidden {
			t.Errorf("manager reset account %d's password: %d, want 403", id, w.Code)
		}
		if w := call(AdminStaffSignOut, id, ""); w.Code != http.StatusForbidden {
			t.Errorf("manager signed account %d out: %d, want 403", id, w.Code)
		}
		if w := call(AdminStaffEdit, id, ""); w.Code != http.StatusForbidden {
			t.Errorf("manager opened account %d's editor: %d, want 403", id, w.Code)
		}
	}
	if u, _ := db.GetUserByID(adminID); u.PasswordHash != hash {
		t.Fatal("the admin's password was changed by a manager")
	}

	// The limited editor is only password / devices / history: no role or access.
	w := call(AdminStaffEdit, waiterID, "")
	b := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(b, "Sign out everywhere") || strings.Contains(b, "Page access") || strings.Contains(b, `name="department"`) || strings.Contains(b, "Deactivate") {
		t.Errorf("manager's editor should be password/devices only:\n%.600s", b)
	}
	// And the list only names accounts they can help.
	lw := httptest.NewRecorder()
	AdminStaffList(lw, workerReq(http.MethodGet, "/admin/staff", m, nil))
	lb := lw.Body.String()
	if !strings.Contains(lb, "help_waiter") || strings.Contains(lb, "help_admin") || strings.Contains(lb, "help_mgr2") {
		t.Error("manager's staff list shows accounts they can't help")
	}
	// The audit trail names who did it.
	entries, _ := db.ListAudit(db.AuditFilter{Action: "reset_password", Text: "help_waiter"}, 0, 5)
	if len(entries) == 0 || entries[0].Actor != "mgr" {
		t.Errorf("password reset not attributed to the manager: %+v", entries)
	}
}

// TestFixingAnOrderIsLogged: moving an order between stages and cancelling
// it work from the Orders panel, refuse what makes no sense, and leave a
// staff-activity entry each time.
func TestFixingAnOrderIsLogged(t *testing.T) {
	pid, _ := db.CreateProduct("Fixit Toastie", 5, 10, models.CategoryOther, "", "", 24)
	db.CreateTable(9950)
	id, err := db.CreateOrder(9950, "s", models.PaymentMethodCash, models.PaymentStatusUnpaid, "", 5, 0, 1,
		[]models.OrderItem{{ProductID: pid, Name: "Fixit Toastie", Price: 5, Quantity: 1}})
	if err != nil {
		t.Fatal(err)
	}
	m := mgr()
	post := func(h http.HandlerFunc, oid int, form string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(form))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", strconv.Itoa(oid))
		r = r.WithContext(context.WithValue(context.WithValue(r.Context(), chi.RouteCtxKey, rc), userContextKey, m))
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	if w := post(AdminOrderStatus, id, "status=ready"); w.Code != http.StatusOK {
		t.Fatalf("move to ready: %d %s", w.Code, w.Body)
	}
	if o, _ := db.GetOrderByID(id); o.Status != models.OrderStatusReady {
		t.Fatalf("status %q, want ready", o.Status)
	}
	if w := post(AdminOrderStatus, id, "status=exploded"); w.Code != http.StatusNotFound {
		t.Errorf("nonsense stage accepted: %d", w.Code)
	}
	if w := post(AdminOrderCancel, id, ""); w.Code != http.StatusConflict {
		t.Fatalf("cancelling a Ready order (food already made): %d, want 409", w.Code)
	}
	post(AdminOrderStatus, id, "status=preparing") // the explicit step back, also logged
	if w := post(AdminOrderCancel, id, ""); w.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", w.Code, w.Body)
	}
	if p, _ := db.GetProductByID(pid); p.Stock != 10 {
		t.Errorf("stock %d after cancelling, want 10 back", p.Stock)
	}
	if w := post(AdminOrderStatus, id, "status=served"); w.Code != http.StatusConflict {
		t.Errorf("moving a cancelled order: %d, want 409", w.Code)
	}
	served, _ := db.CreateOrder(9950, "s", models.PaymentMethodCash, models.PaymentStatusPaid, "", 5, 0, 1,
		[]models.OrderItem{{ProductID: pid, Name: "Fixit Toastie", Price: 5, Quantity: 1}})
	db.UpdateOrderStatus(served, models.OrderStatusServed)
	if w := post(AdminOrderCancel, served, ""); w.Code != http.StatusConflict {
		t.Errorf("cancelling a served order: %d, want 409", w.Code)
	}
	for _, act := range []string{"order_status", "order_cancel"} {
		es, _ := db.ListAudit(db.AuditFilter{Action: act, Text: "mgr"}, 0, 5)
		if len(es) == 0 {
			t.Errorf("no %q entry in the activity log", act)
		}
	}
}

// TestTwoModesWorkAndAdmin: a manager has a Work side (the staff app, with
// Floor and Bookings inside it) and an Admin side, a Work / Admin switch in
// both, and never gets dropped into the other mode by a link. A waiter has
// only the work side.
func TestTwoModesWorkAndAdmin(t *testing.T) {
	get := func(h http.HandlerFunc, u models.User, url string) (int, string) {
		w := httptest.NewRecorder()
		h(w, workerReq(http.MethodGet, url, u, nil))
		return w.Code, w.Body.String()
	}
	m := mgr()

	// Work mode: the app links Floor and Bookings to work-mode pages, and
	// shows the switch with Work as the current mode.
	code, home := get(WorkerHome, m, "/worker")
	if code != http.StatusOK || !strings.Contains(home, `class="modesw"`) || !strings.Contains(home, `href="/worker/floor"`) {
		t.Fatalf("manager app: %d, missing switch or work-mode Floor link", code)
	}
	if strings.Contains(home, `href="/admin/floor"`) || strings.Contains(home, `href="/admin/bookings"`) {
		t.Error("the app sends the manager into the admin shell for Floor/Bookings")
	}
	if !strings.Contains(home, "Admin tools") || !strings.Contains(home, `href="/admin/orders"`) {
		t.Error("the menu sheet should list the manager's admin tools")
	}

	// Floor inside the app: app chrome (tabs, sheet), not the admin sidebar;
	// Floor is the active tab; "Book this spot" stays in work mode.
	code, floor := get(WorkFloor, m, "/worker/floor")
	if code != http.StatusOK || !strings.Contains(floor, `class="wtabs"`) || strings.Contains(floor, `class="side"`) {
		t.Fatalf("work-mode floor: %d, wrong chrome", code)
	}
	if !strings.Contains(floor, `href="/worker/floor" aria-current="page"`) || !strings.Contains(floor, `id="bkModal"`) || !strings.Contains(floor, `href="/worker#queue"`) {
		t.Error("work-mode floor lacks the active tab, the modal layer, or a way back to Orders")
	}
	if strings.Contains(floor, `href="/admin/`) && strings.Contains(floor, `href="/admin/floor"`) {
		t.Error("work-mode floor links into admin mode")
	}
	// The link is built by static/js/floor-page.js from the page's base.
	js, _ := os.ReadFile("static/js/floor-page.js")
	if !strings.Contains(floor, `{"base": "/worker"}`) || !strings.Contains(string(js), "${BASE}/bookings?new=1") {
		t.Error("Book this spot should stay inside work mode")
	}
	code, book := get(WorkBookings, m, "/worker/bookings")
	if code != http.StatusOK || !strings.Contains(book, `href="/worker/bookings" aria-current="page"`) || !strings.Contains(book, "hide-phone") {
		t.Errorf("work-mode bookings: %d", code)
	}

	// Admin mode: the sidebar shell, with the switch (Admin current) and a
	// way back to work.
	code, adm := get(AdminTeamPage, m, "/admin/team")
	if code != http.StatusOK || !strings.Contains(adm, `class="side"`) || !strings.Contains(adm, `class="modesw"`) ||
		!strings.Contains(adm, `href="/worker"`) || !strings.Contains(adm, `href="/admin"`) {
		t.Errorf("manager in admin mode: %d, missing sidebar or Work/Admin switch", code)
	}
	if !strings.Contains(adm, `aria-current="true"><svg class="i" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 21v-7`) {
		t.Error("Admin should be the current mode in the admin shell")
	}

	// A waiter has only the work side: no switch in the app.
	wtr := staff(models.DepartmentWaiter)
	_, whome := get(WorkerHome, wtr, "/worker")
	if strings.Contains(whome, `class="modesw"`) || strings.Contains(whome, "Admin tools") {
		t.Error("a waiter sees an admin switch")
	}
	if code, _ := get(WorkFloor, wtr, "/worker/floor"); code != http.StatusOK {
		t.Errorf("waiter's work-mode floor: %d", code)
	}
	// If a waiter lands in the admin shell by URL, there's one way back to work.
	if _, wa := get(StaffBookings, wtr, "/admin/bookings"); !strings.Contains(wa, "Back to work") {
		t.Error("a waiter in the admin shell has no way back to work")
	}

	// Admins have no work side: /worker/floor goes to the back office one.
	admin := models.User{ID: 1, Username: "boss", Role: models.RoleAdmin, IsActive: true}
	w := httptest.NewRecorder()
	WorkFloor(w, workerReq(http.MethodGet, "/worker/floor", admin, nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/floor" {
		t.Errorf("admin on /worker/floor: %d -> %q", w.Code, w.Header().Get("Location"))
	}
	if startPage(m) != "/worker" || startPage(admin) != "/admin" {
		t.Error("a blocked manager should land back in work mode, an admin in the back office")
	}
}

// TestInstallableEverywhere: whichever page someone adds to their home screen,
// it carries the venue's own icon and a manifest (so iOS installs it as a
// full-screen app with the logo, not a Safari bookmark with a letter), and
// the icon is a real opaque PNG made from the venue's theme.
func TestInstallableEverywhere(t *testing.T) {
	db.SetVenueName("Blue Harbor")
	RefreshTheme()
	m := mgr()
	pages := map[string]func() string{
		"login": func() string {
			w := httptest.NewRecorder()
			LoginPage(w, httptest.NewRequest(http.MethodGet, "/login", nil))
			return w.Body.String()
		},
		"landing": func() string {
			w := httptest.NewRecorder()
			Landing(w, httptest.NewRequest(http.MethodGet, "/", nil))
			return w.Body.String()
		},
		"notfound": func() string {
			w := httptest.NewRecorder()
			NotFound(w, httptest.NewRequest(http.MethodGet, "/x", nil))
			return w.Body.String()
		},
		"booking": func() string {
			w := httptest.NewRecorder()
			Booking(w, httptest.NewRequest(http.MethodGet, "/book", nil))
			return w.Body.String()
		},
		"password": func() string {
			w := httptest.NewRecorder()
			ChangePasswordPage(w, workerReq(http.MethodGet, "/change-password", m, nil))
			return w.Body.String()
		},
		"app": func() string {
			w := httptest.NewRecorder()
			WorkerHome(w, workerReq(http.MethodGet, "/worker", m, nil))
			return w.Body.String()
		},
		"floor": func() string {
			w := httptest.NewRecorder()
			WorkFloor(w, workerReq(http.MethodGet, "/worker/floor", m, nil))
			return w.Body.String()
		},
		"admin": func() string {
			w := httptest.NewRecorder()
			AdminTeamPage(w, workerReq(http.MethodGet, "/admin/team", m, nil))
			return w.Body.String()
		},
	}
	for name, render := range pages {
		b := render()
		for _, want := range []string{`rel="apple-touch-icon" href="/brand/apple-touch-icon.png"`, `rel="manifest"`, `apple-mobile-web-app-capable`,
			`apple-mobile-web-app-title" content="Blue Harbor"`} {
			if !strings.Contains(b, want) {
				t.Errorf("%s page: missing %s", name, want)
			}
		}
	}

	// The icon: a 180x180 opaque PNG, cached with an ETag.
	w := httptest.NewRecorder()
	BrandAppleIcon(w, httptest.NewRequest(http.MethodGet, "/brand/apple-touch-icon.png", nil))
	img, err := png.Decode(w.Body)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" || err != nil || img.Bounds().Dx() != 180 {
		t.Fatalf("apple icon: %d %s %v", w.Code, w.Header().Get("Content-Type"), err)
	}
	req := httptest.NewRequest(http.MethodGet, "/brand/apple-touch-icon.png", nil)
	req.Header.Set("If-None-Match", w.Header().Get("ETag"))
	w2 := httptest.NewRecorder()
	BrandAppleIcon(w2, req)
	if w2.Code != http.StatusNotModified {
		t.Errorf("repeat fetch: %d, want 304", w2.Code)
	}

	// Manifests point at those icons, for the staff and the public pages.
	for _, h := range []http.HandlerFunc{WebManifest, SiteManifest} {
		mw := httptest.NewRecorder()
		h(mw, httptest.NewRequest(http.MethodGet, "/manifest.json", nil))
		var man struct {
			Name, Display string
			Icons         []struct{ Src, Type string }
		}
		if err := json.Unmarshal(mw.Body.Bytes(), &man); err != nil || man.Display != "standalone" || len(man.Icons) < 2 {
			t.Fatalf("manifest: %v %s", err, mw.Body)
		}
		for _, ic := range man.Icons {
			if ic.Type != "image/png" || !strings.HasPrefix(ic.Src, "/brand/icon.png?s=") {
				t.Errorf("manifest icon %+v isn't the venue's generated icon", ic)
			}
		}
	}

	// The logo monogram follows the venue name, including after a rename.
	css := func() string {
		cw := httptest.NewRecorder()
		BrandCSS(cw, httptest.NewRequest(http.MethodGet, "/brand.css", nil))
		return cw.Body.String()
	}
	if !strings.Contains(css(), `--monogram:"B"`) {
		t.Error("brand.css has no monogram for Blue Harbor")
	}
	db.SetVenueName("Zorba")
	RefreshTheme()
	if !strings.Contains(css(), `--monogram:"Z"`) {
		t.Error("the monogram didn't follow the venue rename")
	}
}
