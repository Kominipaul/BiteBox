package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"bitebox/internal/brand"
	"bitebox/internal/cart"
	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
)

func postForm(h http.HandlerFunc, target string, form url.Values, user *models.User) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if user != nil {
		r = r.WithContext(context.WithValue(r.Context(), userContextKey, *user))
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func hold(t *testing.T, table, date, hm string) (int, string) {
	t.Helper()
	w := postForm(BookingHold, "/book/hold", url.Values{"table": {table}, "date": {date}, "time": {hm}, "guests": {"2"}}, nil)
	var out struct{ Token string }
	json.NewDecoder(w.Body).Decode(&out)
	return w.Code, out.Token
}

// TestBookingFlow: hold → confirm on /book, holds and bookings both block
// the table for everyone (online and staff), expired/released holds free it,
// and cancelling frees it again.
func TestBookingFlow(t *testing.T) {
	const table = 9501
	if err := db.CreateTable(table); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	date := today().AddDate(0, 0, 1).Format(dateLayout)
	contact := url.Values{"name": {"Maria"}, "phone": {"6900000000"}}

	code, token := hold(t, "9501", date, "20:00")
	if code != http.StatusOK || token == "" {
		t.Fatalf("hold: %d", code)
	}
	if code, _ := hold(t, "9501", date, "21:00"); code != http.StatusConflict {
		t.Fatalf("hold over a live hold: got %d, want 409", code)
	}
	contact.Set("token", token)
	if w := postForm(BookingConfirm, "/book/confirm", contact, nil); w.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	if w := postForm(BookingConfirm, "/book/confirm", contact, nil); w.Code != http.StatusGone {
		t.Fatalf("confirming the same hold twice: got %d, want 410", w.Code)
	}
	if code, _ := hold(t, "9501", date, "21:00"); code != http.StatusConflict {
		t.Fatalf("hold over a confirmed booking: got %d, want 409", code)
	}

	// Released and expired holds free the table.
	_, tok := hold(t, "9501", date, "12:00")
	postForm(BookingRelease, "/book/release", url.Values{"token": {tok}}, nil)
	_, tok = hold(t, "9501", date, "12:00")
	if tok == "" {
		t.Fatal("hold after release failed")
	}
	db.DB.Exec("UPDATE bookings SET hold_expires = '2000-01-01 00:00:00' WHERE hold_token = ?", tok)
	if w := postForm(BookingConfirm, "/book/confirm", url.Values{"token": {tok}, "name": {"X"}, "phone": {"6900000001"}}, nil); w.Code != http.StatusGone {
		t.Fatalf("confirming an expired hold: got %d, want 410", w.Code)
	}
	if code, _ := hold(t, "9501", date, "12:00"); code != http.StatusOK {
		t.Fatalf("hold after expiry: got %d", code)
	}

	// Staff can't double-book either.
	admin := &models.User{Username: "boss", Role: models.RoleAdmin}
	staff := url.Values{"source": {"phone"}, "date": {date}, "time": {"19:00"}, "duration": {"90"},
		"guests": {"2"}, "table": {"9501"}, "name": {"Nikos"}}
	if w := postForm(StaffBookingCreate, "/admin/bookings", staff, admin); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "already has a booking") {
		t.Fatalf("overlapping staff booking should be refused, got %d", w.Code)
	}

	r := httptest.NewRequest(http.MethodGet, "/book/availability?date="+date, nil)
	w := httptest.NewRecorder()
	BookingAvailability(w, r)
	var av struct{ Taken map[string][]int }
	json.NewDecoder(w.Body).Decode(&av)
	if !containsInt(av.Taken["20:00"], table) || containsInt(av.Taken["16:00"], table) {
		t.Fatalf("availability wrong: 20:00=%v 16:00=%v", av.Taken["20:00"], av.Taken["16:00"])
	}

	// Cancel the online booking; the staff booking now fits.
	bs, _ := db.GetBookingsOverlapping(date+" 19:00", date+" 23:00")
	var b models.Booking
	for _, x := range bs {
		if x.TableNumber == table && x.Status == models.BookingConfirmed {
			b = x
		}
	}
	b.Status = models.BookingCancelled
	if err := db.UpdateBooking(b); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if w := postForm(StaffBookingCreate, "/admin/bookings", staff, admin); w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("HX-Trigger"), "bookingSaved") {
		t.Fatalf("staff booking after cancel: %d %s", w.Code, w.Body)
	}
	b.Status = models.BookingConfirmed
	if err := db.UpdateBooking(b); err != db.ErrBookingConflict {
		t.Fatalf("restore over a newer booking: got %v, want ErrBookingConflict", err)
	}

	// Every back-office page renders, with real data and admin tools.
	for _, pg := range []struct {
		h    http.HandlerFunc
		url  string
		want string
	}{
		{StaffBookings, "/admin/bookings?when=tomorrow", "Nikos"},
		{StaffBookingRows, "/admin/bookings/rows?when=tomorrow&q=niko", "Nikos"},
		{StaffBookingNew, "/admin/bookings/new", "Save booking"},
		{StaffFloor, "/admin/floor", "Floor"},
		{StaffFloorData, "/admin/floor/live?date=" + date + "&time=19:00", "Nikos"},
		{AdminFloorEditor, "/admin/map", "Add spot"},
		{AdminSettingsPage, "/admin/settings", "Reset to EGO layout"},
		{AdminHome, "/admin", "Bookings today"},
		{AdminMenuPage, "/admin/menu", "Add item"},
		{AdminCategoriesPage, "/admin/menu/categories", "Add category"},
		{AdminTeamPage, "/admin/team", "Add account"},
		{AdminOrdersPage, "/admin/orders", "Export CSV"},
		{AdminLogsPage, "/admin/logs", "calls answered"},
		{AdminSystemPage, "/admin/system", "Neon night"},
		{AdminLive, "/admin/live", "Collect"},
	} {
		req := httptest.NewRequest(http.MethodGet, pg.url, nil)
		req.Header.Set("HX-Request", "true") // partials (rows, the booking popup) are fetched by htmx
		req = req.WithContext(context.WithValue(req.Context(), userContextKey, *admin))
		pw := httptest.NewRecorder()
		pg.h(pw, req)
		body := pw.Body.String()
		full := !strings.Contains(pg.url, "/rows") && !strings.Contains(pg.url, "/data") && !strings.Contains(pg.url, "/live") && !strings.Contains(pg.url, "/new")
		if !strings.Contains(body, pg.want) || (full && !strings.Contains(body, "</html>")) {
			t.Errorf("%s: missing %q (status %d)", pg.url, pg.want, pw.Code)
		}
	}
}

// TestFloorPresetAndSpotRules: the EGO preset creates labelled spots, and a
// spot's online hours are enforced by BookingHold.
func TestFloorPresetAndSpotRules(t *testing.T) {
	if w := postForm(AdminFloorPreset, "/admin/map/preset", nil, nil); w.Code != http.StatusNoContent {
		t.Fatalf("preset: %d %s", w.Code, w.Body)
	}
	b, err := db.GetTable(101)
	if err != nil || b.Label != "B01" || b.Shape != "set" || !b.FullDay || b.Fee != 20 {
		t.Fatalf("beach set B01 not set up: %+v %v", b, err)
	}
	date := today().AddDate(0, 0, 2).Format(dateLayout)
	// Full day: booked 10:00-19:00 whatever the arrival time, so a second
	// hold later that day is refused.
	if code, _ := hold(t, "101", date, "12:00"); code != http.StatusOK {
		t.Fatalf("beach set at 12:00: got %d", code)
	}
	if code, _ := hold(t, "101", date, "17:00"); code != http.StatusConflict {
		t.Fatalf("second hold on a full-day beach set: got %d, want 409", code)
	}
	// Terrace lounge opens online from 18:00.
	if code, _ := hold(t, "31", date, "12:00"); code != http.StatusBadRequest {
		t.Fatalf("lounge at 12:00 should be refused, got %d", code)
	}
}

func containsInt(xs []int, n int) bool {
	for _, x := range xs {
		if x == n {
			return true
		}
	}
	return false
}

// TestFloorQRAndHelp: a QR guest and their "Call waiter" show on the Floor
// data, and staff can release the table from there.
func TestFloorQRAndHelp(t *testing.T) {
	const table = 9601
	db.CreateTable(table)
	db.ClaimTable(table, "guest-session")
	db.RequestAssistance(table, "help")

	w := httptest.NewRecorder()
	StaffFloorData(w, httptest.NewRequest(http.MethodGet, "/admin/floor/live", nil))
	var v struct {
		Help  int
		Floor []struct {
			N    int  `json:"n"`
			QR   bool `json:"qr"`
			Help bool `json:"help"`
		}
	}
	json.NewDecoder(w.Body).Decode(&v)
	found := false
	for _, f := range v.Floor {
		if f.N == table {
			found = f.QR && f.Help
		}
	}
	if !found || v.Help < 1 {
		t.Fatalf("floor data should show table %d as QR-occupied and needing help", table)
	}

	r := httptest.NewRequest(http.MethodPost, "/admin/tables/9601/release", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("number", "9601")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
	w = httptest.NewRecorder()
	StaffReleaseTable(w, r)
	if tb, _ := db.GetTable(table); w.Code != http.StatusNoContent || tb.Status != "available" || tb.AssistanceRequested {
		t.Fatalf("release: code %d, table %+v", w.Code, tb)
	}

	// The released guest's phone reloading the old page goes home instead
	// of re-claiming the table.
	h := &TableHandlers{Cart: cart.NewStore()}
	r = httptest.NewRequest(http.MethodGet, "/table/9601", nil)
	r.AddCookie(&http.Cookie{Name: GuestSessionCookieName, Value: "guest-session"})
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
	w = httptest.NewRecorder()
	h.View(w, r)
	if tb, _ := db.GetTable(table); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" || tb.Status != "available" {
		t.Fatalf("stale reload: code %d location %q table %+v", w.Code, w.Header().Get("Location"), tb)
	}
}

// TestThemeSaveRestylesEveryPage: saving the editor form changes /brand.css
// (new ETag, new colours), and a 304 is sent while nothing changed.
func TestThemeSaveRestylesEveryPage(t *testing.T) {
	get := func(etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/brand.css", nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		w := httptest.NewRecorder()
		BrandCSS(w, req)
		return w
	}
	before := get("")
	if w := get(before.Header().Get("ETag")); w.Code != http.StatusNotModified {
		t.Fatalf("unchanged theme: %d, want 304", w.Code)
	}
	admin := &models.User{ID: 1, Username: "admin", Role: models.RoleAdmin}
	form := url.Values{"preset": {"neon"}, "venue_name": {"Test Venue"}, "primary": {"#12AB34"}, "header": {"primary"}, "corners": {"pill"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/system", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, *admin))
	w := httptest.NewRecorder()
	AdminSystemSave(w, req)
	if w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("HX-Trigger"), "themeSaved") {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	after := get(before.Header().Get("ETag"))
	css := after.Body.String()
	if after.Code != http.StatusOK || !strings.Contains(css, "--gold:#12AB34") || !strings.Contains(css, "--radius-pill:999px") {
		t.Fatalf("brand.css not updated (%d):\n%.400s", after.Code, css)
	}
	if s, _ := db.GetSettings(); s.VenueName != "Test Venue" || db.GetTheme().Primary != "#12AB34" {
		t.Fatalf("not saved: %q %q", s.VenueName, db.GetTheme().Primary)
	}
	setTheme(brand.Default())
	db.SaveTheme(brand.Default())
}

// TestFinishingEarlyFreesTheSpot: staff finishing (or no-showing) a booking
// during its time end it now, so the spot can be booked again straight
// away, and the floor shows the late party until then.
func TestFinishingEarlyFreesTheSpot(t *testing.T) {
	const table = 9701
	db.CreateTable(table)
	now := time.Now().Truncate(time.Minute)
	b := models.Booking{TableNumber: table, StartsAt: fmtBT(now.Add(-30 * time.Minute)), EndsAt: fmtBT(now.Add(90 * time.Minute)),
		Guests: 2, Name: "Early Leaver", Status: models.BookingConfirmed, Source: "phone"}
	if err := db.CreateBooking(&b, ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, f := range buildDay("", "").Floor {
		if f.N == table && (f.Status != "late" || f.Bk == nil || f.Bk.ID != b.ID) {
			t.Fatalf("started, nobody here: floor shows %q (bk %+v), want late", f.Status, f.Bk)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/bookings/x/status", strings.NewReader("status=no_show"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", strconv.Itoa(b.ID))
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
	StaffBookingStatus(httptest.NewRecorder(), r)
	got, _ := db.GetBooking(b.ID)
	if got.Status != models.BookingNoShow || got.End().After(time.Now()) {
		t.Fatalf("after no-show: %q ends %s, want ended now", got.Status, got.EndsAt)
	}
	walkIn := models.Booking{TableNumber: table, StartsAt: fmtBT(now.Add(time.Minute)), EndsAt: fmtBT(now.Add(time.Hour)),
		Guests: 2, Name: "Walk-in", Status: models.BookingConfirmed, Source: "walk_in"}
	if err := db.CreateBooking(&walkIn, ""); err != nil {
		t.Fatalf("spot not free after no-show: %v", err)
	}
}

// TestStaffAppFeedsRender: every station's live feed renders with real
// orders in it. The kitchen/bar query once lacked the tip/split columns, so
// their whole feed failed while the manager's looked fine.
func TestStaffAppFeedsRender(t *testing.T) {
	cats, _ := db.GetCategories()
	cat := ""
	for _, c := range cats {
		if c.Department == "kitchen" {
			cat = c.Name
		}
	}
	if cat == "" {
		t.Skip("no kitchen category")
	}
	pid, err := db.CreateProduct("Feedtest Burger", 9, -1, cat, "", "", 24)
	if err != nil {
		t.Fatal(err)
	}
	db.CreateTable(9801)
	oid, err := db.CreateOrder(9801, "s", models.PaymentMethodCash, models.PaymentStatusUnpaid, "no nuts", 18, 2, 2,
		[]models.OrderItem{{ProductID: pid, Name: "Feedtest Burger", Price: 9, Quantity: 2, Excluded: []string{"onion"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range []string{"kitchen", "bar", "waiter", "superworker"} {
		b, err := renderOrderFeedHTML(bucket)
		if err != nil {
			t.Fatalf("%s feed: %v", bucket, err)
		}
		has := strings.Contains(string(b), "Feedtest Burger")
		if want := bucket == "kitchen" || bucket == "superworker"; has != want {
			t.Errorf("%s feed shows the kitchen order = %v, want %v", bucket, has, want)
		}
	}
	// A bar station sees the kitchen order's burger dimmed, not as its own.
	db.UpdateOrderStatus(oid, models.OrderStatusReady)
	if b, _ := renderOrderFeedHTML("waiter"); !strings.Contains(string(b), "Feedtest Burger") || !strings.Contains(string(b), "Delivered") {
		t.Error("waiter should see the ready order with a Delivered button")
	}
}

// TestStaffCantBookOccupiedTable: while a guest sits at a table (QR claim),
// staff can't book it for now, but can for later.
func TestStaffCantBookOccupiedTable(t *testing.T) {
	const table = 9701
	db.CreateTable(table)
	db.ClaimTable(table, "sitting-guest")
	admin := &models.User{Username: "boss", Role: models.RoleAdmin}
	soon := time.Now().Add(10 * time.Minute)
	form := url.Values{"source": {"phone"}, "date": {soon.Format(dateLayout)}, "time": {soon.Format("15:04")},
		"duration": {"90"}, "guests": {"2"}, "table": {"9701"}, "name": {"Nikos"}}
	if w := postForm(StaffBookingCreate, "/admin/bookings", form, admin); !strings.Contains(w.Body.String(), "guests sitting there now") {
		t.Fatalf("booking an occupied table now should be refused, got %d", w.Code)
	}
	later := time.Now().Add(26 * time.Hour)
	form.Set("date", later.Format(dateLayout))
	form.Set("time", later.Format("15:04"))
	if w := postForm(StaffBookingCreate, "/admin/bookings", form, admin); w.Code != http.StatusNoContent {
		t.Fatalf("booking the same table tomorrow: %d %s", w.Code, w.Body)
	}
}
