package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
)

const (
	slotStep      = 30 * time.Minute
	dateLayout    = "2006-01-02"
	maxBookingLen = 500
)

// ---------- shared helpers ----------

// parseHM turns "HH:MM" into minutes after midnight.
func parseHM(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

func parseDay(s string) (time.Time, bool) {
	t, err := time.ParseInLocation(dateLayout, s, time.Local)
	return t, err == nil
}

func today() time.Time {
	y, m, d := time.Now().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// bookingSlots lists the arrival times guests can pick on day, dropping any
// already past.
func bookingSlots(s models.Settings, day time.Time) []time.Time {
	open, ok1 := parseHM(s.BookingOpen)
	closeAt, ok2 := parseHM(s.BookingClose)
	if !ok1 || !ok2 {
		return nil
	}
	now := time.Now()
	var out []time.Time
	for m := open; m <= closeAt; m += int(slotStep.Minutes()) {
		t := day.Add(time.Duration(m) * time.Minute)
		if t.After(now) {
			out = append(out, t)
		}
	}
	return out
}

func fmtBT(t time.Time) string { return t.Format(models.BookingTimeLayout) }

// overlaps: an active booking on table n overlapping [start, end).
func overlaps(bookings []models.Booking, n int, start, end time.Time) *models.Booking {
	s, e := fmtBT(start), fmtBT(end)
	for i, b := range bookings {
		if b.TableNumber == n && b.Active() && b.StartsAt < e && b.EndsAt > s {
			return &bookings[i]
		}
	}
	return nil
}

// qrBlocks: a table a guest is sitting at right now (QR session) can't be
// promised to anyone arriving before a normal visit would be over.
func qrBlocks(t models.Table, start time.Time, dur time.Duration) bool {
	return t.Status == "occupied" && start.Before(time.Now().Add(dur))
}

// readContact validates the guest-contact fields shared by guest and staff
// forms.
func readContact(r *http.Request, requirePhone bool) (name, phone, email, notes, errMsg string) {
	name = strings.TrimSpace(r.FormValue("name"))
	phone = strings.TrimSpace(r.FormValue("phone"))
	email = strings.TrimSpace(r.FormValue("email"))
	notes = strings.TrimSpace(r.FormValue("notes"))
	digits := 0
	for _, c := range phone {
		if c >= '0' && c <= '9' {
			digits++
		}
	}
	switch {
	case name == "" || len(name) > 80:
		errMsg = "Enter the name for the booking."
	case (requirePhone || phone != "") && (digits < 7 || len(phone) > 30):
		errMsg = "Enter a phone number we can reach you on."
	case email != "" && (len(email) > 120 || !validEmail(email)):
		errMsg = "Check the email address."
	case len(notes) > maxBookingLen:
		errMsg = "Special requests are too long."
	}
	return
}

func validEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}

// publicTable is what /book and the staff floor get for each table — never
// host_session_id or anything else guest-private.
type publicTable struct {
	N        int     `json:"n"`
	Label    string  `json:"label"`
	Zone     string  `json:"zone"`
	Shape    string  `json:"shape"`
	Min      int     `json:"min"`
	Max      int     `json:"max"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Bookable bool    `json:"bookable"`
	Fee      float64 `json:"fee"`
	MinSpend float64 `json:"minSpend"`
	Dur      int     `json:"dur"`
	From     string  `json:"from"`
	Until    string  `json:"until"`
	FullDay  bool    `json:"fullDay"`
}

func toPublic(t models.Table) publicTable {
	return publicTable{t.Number, t.Name(), t.Zone, t.Shape, t.MinGuests, t.MaxGuests, t.PosX, t.PosY,
		t.Bookable && !t.OutOfService, t.Fee, t.MinSpend, t.Duration, t.AvailFrom, t.AvailUntil, t.FullDay}
}

// tableDuration: the table's own booking length, else the venue default.
func tableDuration(t models.Table, s models.Settings) time.Duration {
	if t.Duration > 0 {
		return time.Duration(t.Duration) * time.Minute
	}
	return time.Duration(s.BookingDuration) * time.Minute
}

// bookingWindow is when a booking of t arriving at start occupies it: its
// own length from arrival, or for a full-day spot (beach sets, cabanas) its
// whole day window, whatever the arrival time.
func bookingWindow(t models.Table, s models.Settings, start time.Time) (time.Time, time.Time) {
	if !t.FullDay {
		return start, start.Add(tableDuration(t, s))
	}
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.Local)
	from, ok := parseHM(t.AvailFrom)
	if !ok {
		from = 10 * 60
	}
	until, ok := parseHM(t.AvailUntil)
	if !ok || until <= from {
		until = 19 * 60
	}
	return day.Add(time.Duration(from) * time.Minute), day.Add(time.Duration(until) * time.Minute)
}

// ---------- guest: /book ----------

type bookDate struct {
	ISO   string `json:"iso"`
	Week  string `json:"wd"`
	Day   int    `json:"d"`
	Label string `json:"label"`
	Long  string `json:"long"`
}

func Booking(w http.ResponseWriter, r *http.Request) {
	settings, _ := db.GetSettings()
	tables, _ := db.GetAllTables()
	pub := make([]publicTable, 0, len(tables))
	maxGuests := 1
	for _, t := range tables {
		pub = append(pub, toPublic(t))
		if t.Bookable && t.MaxGuests > maxGuests {
			maxGuests = t.MaxGuests
		}
	}
	var dates []bookDate
	for i := 0; i < settings.BookingDaysAhead; i++ {
		d := today().AddDate(0, 0, i)
		wd := d.Format("Mon")
		if i == 0 {
			wd = "Today"
		}
		dates = append(dates, bookDate{d.Format(dateLayout), wd, d.Day(), d.Format("Mon 2 Jan"), d.Format("Monday 2 January")})
	}
	theme, _, _ := currentTheme()
	tmpl := template.Must(template.ParseFiles("templates/booking.html"))
	tmpl.Execute(w, map[string]interface{}{
		"VenueName": settings.VenueName,
		"Tagline":   theme.Tagline,
		"V":         assetVersion,
		"Phone":     settings.VenuePhone,
		"Enabled":   settings.BookingEnabled,
		"Data": map[string]interface{}{
			"tables":    pub,
			"dates":     dates,
			"duration":  settings.BookingDuration,
			"maxGuests": maxGuests,
			"holdSecs":  int(holdFor.Seconds()),
		},
	})
}

// BookingAvailability returns, for one date, the arrival times still open
// and which tables are taken at each: {"times":[...],"taken":{"20:00":[3,7]}}.
// Per-table rules (guests, from/until) are applied by the page and re-checked
// by BookingHold.
func BookingAvailability(w http.ResponseWriter, r *http.Request) {
	settings, _ := db.GetSettings()
	times, taken := []string{}, map[string][]int{}
	day, ok := parseDay(r.URL.Query().Get("date"))
	if ok && settings.BookingEnabled && !day.Before(today()) && day.Before(today().AddDate(0, 0, settings.BookingDaysAhead)) {
		tables, _ := db.GetAllTables()
		bookings, _ := db.GetBookingsOverlapping(fmtBT(day), fmtBT(day.Add(48*time.Hour)))
		for _, s := range bookingSlots(settings, day) {
			hm := s.Format("15:04")
			times = append(times, hm)
			taken[hm] = []int{}
			for _, t := range tables {
				ws, we := bookingWindow(t, settings, s)
				if overlaps(bookings, t.Number, ws, we) != nil || qrBlocks(t, ws, we.Sub(ws)) || !we.After(time.Now()) {
					taken[hm] = append(taken[hm], t.Number)
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]interface{}{"times": times, "taken": taken})
}

// holdFor is how long a guest keeps a spot while typing their details.
const holdFor = 5 * time.Minute

// holdLimiter bounds holds per IP.
// ponytail: per-IP only; one IP can still hold up to 20 spots for 5 minutes.
var holdLimiter = newIPRateLimiter(20, 10*time.Minute)

func jsonFail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{"error": msg, "conflict": status == http.StatusConflict})
}

// BookingHold reserves a table for holdFor while the guest fills in their
// details — the table shows as taken to everyone else meanwhile.
func BookingHold(w http.ResponseWriter, r *http.Request) {
	if !holdLimiter.Allow(clientIP(r)) {
		jsonFail(w, http.StatusTooManyRequests, "Too many attempts. Please wait a few minutes or call us.")
		return
	}
	settings, _ := db.GetSettings()
	if !settings.BookingEnabled {
		jsonFail(w, http.StatusForbidden, "Online booking is closed right now.")
		return
	}
	day, ok := parseDay(r.FormValue("date"))
	if !ok || day.Before(today()) || !day.Before(today().AddDate(0, 0, settings.BookingDaysAhead)) {
		jsonFail(w, http.StatusBadRequest, "Pick a date from the list.")
		return
	}
	hm := r.FormValue("time")
	var start time.Time
	for _, s := range bookingSlots(settings, day) {
		if s.Format("15:04") == hm {
			start = s
		}
	}
	if start.IsZero() {
		jsonFail(w, http.StatusBadRequest, "That time isn't available any more. Pick another.")
		return
	}
	guests, _ := strconv.Atoi(r.FormValue("guests"))
	number, _ := strconv.Atoi(r.FormValue("table"))
	t, err := db.GetTable(number)
	switch {
	case err != nil || !t.Bookable || t.OutOfService:
		jsonFail(w, http.StatusBadRequest, "That spot can't be booked online.")
		return
	case guests < t.MinGuests || guests > t.MaxGuests:
		jsonFail(w, http.StatusBadRequest, fmt.Sprintf("%s seats %d to %d guests.", t.Name(), t.MinGuests, t.MaxGuests))
		return
	case !t.FullDay && t.AvailFrom != "" && hm < t.AvailFrom:
		jsonFail(w, http.StatusBadRequest, fmt.Sprintf("%s can be booked from %s.", t.Name(), t.AvailFrom))
		return
	case !t.FullDay && t.AvailUntil != "" && hm > t.AvailUntil:
		jsonFail(w, http.StatusBadRequest, fmt.Sprintf("%s can be booked until %s.", t.Name(), t.AvailUntil))
		return
	}
	ws, we := bookingWindow(t, settings, start)
	if !we.After(time.Now()) {
		jsonFail(w, http.StatusBadRequest, fmt.Sprintf("%s can only be booked until %s on this day.", t.Name(), we.Format("15:04")))
		return
	}
	if qrBlocks(t, ws, we.Sub(ws)) {
		jsonFail(w, http.StatusConflict, "This spot was just taken.")
		return
	}
	token := randomHex(16)
	b := models.Booking{TableNumber: t.Number, StartsAt: fmtBT(ws), EndsAt: fmtBT(we), Guests: guests,
		Status: models.BookingHeld, Source: "online", HoldExpires: time.Now().Add(holdFor).Format(models.HoldTimeLayout)}
	if err := db.CreateBooking(&b, token); errors.Is(err, db.ErrBookingConflict) {
		jsonFail(w, http.StatusConflict, "This spot was just reserved.")
		return
	} else if err != nil {
		jsonFail(w, http.StatusInternalServerError, "Something went wrong. Please try again or call us.")
		return
	}
	BroadcastFloor()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"token": token, "expiresIn": int(holdFor.Seconds()),
		"start": ws.Format("15:04"), "end": we.Format("15:04")})
}

// BookingConfirm turns a live hold into a confirmed booking.
func BookingConfirm(w http.ResponseWriter, r *http.Request) {
	if !bookingConfirmLimiter.Allow(clientIP(r)) {
		jsonFail(w, http.StatusTooManyRequests, "Too many tries. Please wait a few minutes, or call us.")
		return
	}
	name, phone, email, notes, msg := readContact(r, true)
	if msg != "" {
		jsonFail(w, http.StatusBadRequest, msg)
		return
	}
	b, err := db.ConfirmHold(r.FormValue("token"), name, phone, email, notes)
	if errors.Is(err, db.ErrHoldExpired) {
		jsonFail(w, http.StatusGone, "Your hold ended and the spot was released. Pick a spot to start again.")
		return
	} else if err != nil {
		jsonFail(w, http.StatusInternalServerError, "Something went wrong. Please try again or call us.")
		return
	}
	BroadcastFloor()
	t, _ := db.GetTable(b.TableNumber)
	start := b.Start()
	go notifyDepartments([]string{models.DepartmentWaiter}, pushPayload{
		Title: "New booking — " + t.Name(),
		Body:  fmt.Sprintf("%s %s · %d guests · %s", start.Format("Mon 2 Jan"), start.Format("15:04"), b.Guests, name),
		URL:   "/admin/bookings",
		Tag:   "booking-" + b.Code,
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"code": b.Code, "table": b.TableNumber, "date": start.Format("Mon 2 Jan"),
		"time": start.Format("15:04"), "end": b.End().Format("15:04"), "guests": b.Guests, "name": name,
	})
}

// BookingRelease gives a held spot back (guest pressed Release or left).
func BookingRelease(w http.ResponseWriter, r *http.Request) {
	if token := r.FormValue("token"); token != "" {
		db.ReleaseHold(token)
		BroadcastFloor()
	}
	w.WriteHeader(http.StatusNoContent)
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------- staff back office: /admin/bookings/* and /admin/map ----------
//
// Every page is templates/bk_layout.html (sidebar shell) plus the page's own
// template, which defines "title", "content" and "scripts".

// assetVersion goes on our CSS/JS links (?v=...) so every server start
// busts whatever copy a browser has cached from before an update.
var assetVersion = strconv.FormatInt(time.Now().Unix(), 10)

// staffPage renders one back-office page in the admin shell. nav marks the
// sidebar item.
func staffPage(w http.ResponseWriter, r *http.Request, page, nav string, data map[string]interface{}, extra ...string) {
	renderShell(w, r, "layout", page, nav, data, extra...)
}

// renderShell renders a back-office page's content in one of the two shells:
// the admin sidebar ("layout") or the work-mode app chrome ("wk-layout"). The
// page content and everything it fetches are the same either way.
func renderShell(w http.ResponseWriter, r *http.Request, layout, page, nav string, data map[string]interface{}, extra ...string) {
	user, _ := UserFromContext(r)
	settings, _ := db.GetSettings()
	isAdmin := user.Role == models.RoleAdmin
	data["VenueName"] = settings.VenueName
	data["V"] = assetVersion
	data["Username"] = user.Username
	data["Initials"] = initials(user.Username)
	data["IsAdmin"] = isAdmin
	data["Nav"] = nav
	data["Role"], data["Back"] = departmentLabel(user.Department), "/worker"
	if isAdmin {
		data["Role"], data["Back"] = "Admin", "/admin"
	}
	can := map[string]bool{}
	for _, p := range models.BackOfficePages {
		can[p.Key] = user.Can(p.Key)
	}
	data["Can"], data["Start"] = can, adminStart(user)
	if _, set := data["Base"]; !set {
		data["Base"] = "/admin" // where this page's own links point: /admin, or /worker in work mode
	}
	if layout == "layout" && !isAdmin {
		// An account with a work side gets the Work / Admin switch in the admin shell too.
		data["ModeNow"], data["ModeWork"] = "admin", "/worker"
		if hasAdminMode(user) {
			data["ModeAdmin"] = adminStart(user)
		}
	}
	files := []string{"templates/bk_layout.html", "templates/" + page}
	for _, f := range extra {
		files = append(files, "templates/"+f)
	}
	tmpl := template.Must(template.ParseFiles(files...))
	if err := tmpl.ExecuteTemplate(w, layout, data); err != nil {
		log.Printf("page %s: %v", page, err)
	}
}

func initials(name string) string {
	out := ""
	for _, f := range strings.Fields(name) {
		out += strings.ToUpper(string([]rune(f)[:1]))
		if len(out) >= 2 {
			break
		}
	}
	if out == "" {
		return "?"
	}
	return out
}

// hue gives each guest a stable avatar colour (av0..av5 in staff.css).
func hue(s string) int {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return h % 6
}

// ----- Bookings list -----

type bookingRow struct {
	models.Booking
	Spot, Zone, Initials, Day, From, Until string
	Hue                                    int
	StartMs, EndMs                         int64 // for the live countdown
}

// bookingRanges are the list's "When" filter options.
var bookingRanges = []struct{ Key, Label string }{
	{"today", "Today"}, {"tomorrow", "Tomorrow"}, {"week", "Next 7 days"},
	{"upcoming", "All upcoming"}, {"past", "Past 30 days"},
}

var bookingStatusFilters = []struct{ Key, Label string }{
	{"", "Any status"}, {"active", "Confirmed & seated"}, {"confirmed", "Confirmed"}, {"seated", "Seated"},
	{"completed", "Completed"}, {"no_show", "No-show"}, {"cancelled", "Cancelled"},
}

// listBookings applies the list page's filters (when, q, status, zone).
func listBookings(r *http.Request) (rows []bookingRow, covers int) {
	t0 := today()
	from, to := t0, t0.AddDate(0, 0, 1)
	switch r.FormValue("when") {
	case "tomorrow":
		from, to = t0.AddDate(0, 0, 1), t0.AddDate(0, 0, 2)
	case "week":
		to = t0.AddDate(0, 0, 7)
	case "upcoming":
		to = t0.AddDate(1, 0, 0)
	case "past":
		from, to = t0.AddDate(0, 0, -30), t0
	}
	q := strings.ToLower(strings.TrimSpace(r.FormValue("q")))
	status, zone := r.FormValue("status"), r.FormValue("zone")
	tables, _ := db.GetAllTables()
	byNum := map[int]models.Table{}
	for _, t := range tables {
		byNum[t.Number] = t
	}
	all, _ := db.GetBookingsOverlapping(fmtBT(from), fmtBT(to))
	for _, b := range all {
		t := byNum[b.TableNumber]
		switch {
		case b.Status == models.BookingHeld,
			status == "active" && !b.Active(),
			status != "" && status != "active" && b.Status != status,
			zone != "" && t.Zone != zone,
			q != "" && !strings.Contains(strings.ToLower(b.Name+" "+b.Phone+" "+b.Email+" "+b.Code+" "+t.Name()), q):
			continue
		}
		start := b.Start()
		rows = append(rows, bookingRow{Booking: b, Spot: t.Name(), Zone: t.Zone, Initials: initials(b.Name), Hue: hue(b.Name),
			Day: start.Format("Mon 2 Jan"), From: b.StartsAt[11:], Until: b.EndsAt[11:],
			StartMs: start.UnixMilli(), EndMs: b.End().UnixMilli()})
		if b.Active() {
			covers += b.Guests
		}
	}
	return rows, covers
}

func zonesOf(tables []models.Table) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range tables {
		if !seen[t.Zone] {
			seen[t.Zone] = true
			out = append(out, t.Zone)
		}
	}
	return out
}

func StaffBookings(w http.ResponseWriter, r *http.Request) { bookingsPage(w, r, false) }

func bookingsPage(w http.ResponseWriter, r *http.Request, work bool) {
	rows, covers := listBookings(r)
	tables, _ := db.GetAllTables()
	when := r.FormValue("when")
	if when == "" {
		when = "today"
	}
	pageFor(w, r, work, "bk_list.html", "bookings", map[string]interface{}{
		"Rows": rows, "Covers": covers, "Ranges": bookingRanges, "Statuses": bookingStatusFilters,
		"Zones": zonesOf(tables), "When": when, "Q": r.FormValue("q"), "Status": r.FormValue("status"),
		"Zone": r.FormValue("zone"),
	})
}

// renderRows re-renders just the list's table (filter changes, status buttons).
func renderRows(w http.ResponseWriter, r *http.Request, msg string) {
	rows, covers := listBookings(r)
	tmpl := template.Must(template.ParseFiles("templates/bk_layout.html", "templates/bk_list.html"))
	tmpl.ExecuteTemplate(w, "rows", map[string]interface{}{"Rows": rows, "Covers": covers, "Msg": msg})
}

func StaffBookingRows(w http.ResponseWriter, r *http.Request) { renderRows(w, r, "") }

var bookingStatuses = map[string]bool{
	models.BookingConfirmed: true, models.BookingSeated: true, models.BookingCompleted: true,
	models.BookingCancelled: true, models.BookingNoShow: true,
}

func StaffBookingStatus(w http.ResponseWriter, r *http.Request) {
	b, ok := bookingFromURL(w, r)
	if !ok {
		return
	}
	if !bookingStatuses[r.FormValue("status")] {
		http.Error(w, "Bad status", http.StatusBadRequest)
		return
	}
	b.Status = r.FormValue("status")
	// Finishing or no-showing a booking during its time frees the spot now,
	// not at the booked end.
	now := time.Now()
	if (b.Status == models.BookingCompleted || b.Status == models.BookingNoShow) && !b.Start().After(now) && b.End().After(now) {
		b.EndsAt = fmtBT(now)
	}
	msg := ""
	if err := db.UpdateBooking(b); err != nil {
		msg = conflictMsg(err, b.TableNumber)
	}
	BroadcastFloor()
	renderRows(w, r, msg)
}

// StartBookingSweep closes finished bookings every minute (db.ExpireBookings)
// and tells every open floor/bookings screen.
func StartBookingSweep() {
	go func() {
		for {
			if n, err := db.ExpireBookings(time.Now()); err != nil {
				log.Printf("booking sweep: %v", err)
			} else if n > 0 {
				BroadcastFloor()
			}
			time.Sleep(time.Minute)
		}
	}()
}

// ----- New / edit booking -----

type zoneGroup struct {
	Zone   string
	Tables []models.Table
}

type bookingForm struct {
	Title, Action, Error, Date, Time, Name, Phone, Email, Notes, Source, Code string
	Duration, Guests, Table                                                   int
	Edit                                                                      bool
	Groups                                                                    []zoneGroup
}

func renderBookingForm(w http.ResponseWriter, r *http.Request, f bookingForm) {
	tables, _ := db.GetAllTables()
	for _, z := range zonesOf(tables) {
		g := zoneGroup{Zone: z}
		for _, t := range tables {
			if t.Zone == z {
				g.Tables = append(g.Tables, t)
			}
		}
		f.Groups = append(f.Groups, g)
	}
	tmpl := template.Must(template.ParseFiles("templates/bk_layout.html", "templates/bk_form.html"))
	if err := tmpl.ExecuteTemplate(w, "booking-form", map[string]interface{}{"F": f}); err != nil {
		log.Printf("booking form: %v", err)
	}
}

// bookingSaved closes the popup: the Bookings page listens for this trigger,
// shows the code and refreshes the list.
func bookingSaved(w http.ResponseWriter, b models.Booking) {
	BroadcastFloor()
	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"bookingSaved":%q}`, b.Code))
	w.WriteHeader(http.StatusNoContent)
}

// isHX: the request came from htmx (the popup), not a plain page load.
func isHX(r *http.Request) bool { return r.Header.Get("HX-Request") != "" }

// formFromRequest refills the form from what was posted (after an error).
func formFromRequest(r *http.Request, f bookingForm, msg string) bookingForm {
	f.Error = msg
	f.Date, f.Time = r.FormValue("date"), r.FormValue("time")
	f.Duration, _ = strconv.Atoi(r.FormValue("duration"))
	f.Guests, _ = strconv.Atoi(r.FormValue("guests"))
	f.Table, _ = strconv.Atoi(r.FormValue("table"))
	f.Name, f.Phone, f.Email, f.Notes = r.FormValue("name"), r.FormValue("phone"), r.FormValue("email"), r.FormValue("notes")
	f.Source = r.FormValue("source")
	return f
}

// StaffBookingNew is the new-booking form for the popup. A plain visit (the
// Floor page's "Book this spot" link) opens the Bookings page with it open.
func StaffBookingNew(w http.ResponseWriter, r *http.Request) {
	if !isHX(r) {
		q := r.URL.Query()
		q.Set("new", "1")
		http.Redirect(w, r, "/admin/bookings?"+q.Encode(), http.StatusSeeOther) // #nosec G710 -- fixed local path; only the encoded query varies
		return
	}
	settings, _ := db.GetSettings()
	now := time.Now()
	f := bookingForm{Title: "New booking", Action: "/admin/bookings", Source: "phone", Guests: 2,
		Duration: settings.BookingDuration, Date: now.Format(dateLayout), Time: now.Format("15:04")}
	if d := r.FormValue("date"); d != "" {
		f.Date = d
	}
	if t := r.FormValue("time"); t != "" {
		f.Time = t
	}
	f.Table, _ = strconv.Atoi(r.FormValue("table"))
	renderBookingForm(w, r, f)
}

func StaffBookingEdit(w http.ResponseWriter, r *http.Request) {
	b, ok := bookingFromURL(w, r)
	if !ok {
		return
	}
	if !isHX(r) {
		http.Redirect(w, r, fmt.Sprintf("/admin/bookings?edit=%d", b.ID), http.StatusSeeOther)
		return
	}
	renderBookingForm(w, r, bookingForm{Title: "Edit booking", Action: fmt.Sprintf("/admin/bookings/%d", b.ID), Edit: true,
		Code: b.Code, Date: b.StartsAt[:10], Time: b.StartsAt[11:], Duration: b.DurationMin(), Guests: b.Guests,
		Table: b.TableNumber, Name: b.Name, Phone: b.Phone, Email: b.Email, Notes: b.Notes, Source: b.Source})
}

// readStaffBooking parses the staff create/edit form into b. Staff may book
// outside opening hours and any table size; only overlaps, and a table a
// guest is sitting at right now (qrBlocks), are refused.
func readStaffBooking(r *http.Request, b *models.Booking) string {
	old := *b
	day, ok := parseDay(r.FormValue("date"))
	mins, ok2 := parseHM(r.FormValue("time"))
	dur, _ := strconv.Atoi(r.FormValue("duration"))
	guests, _ := strconv.Atoi(r.FormValue("guests"))
	switch {
	case !ok || !ok2:
		return "Pick a date and time."
	case dur < 15 || dur > 24*60:
		return "Duration must be between 15 minutes and 24 hours."
	case guests < 1 || guests > 200:
		return "Enter the number of guests."
	}
	if r.FormValue("source") == "walk_in" && strings.TrimSpace(r.FormValue("name")) == "" {
		r.Form.Set("name", "Walk-in")
	}
	name, phone, email, notes, msg := readContact(r, false)
	if msg != "" {
		return msg
	}
	start := day.Add(time.Duration(mins) * time.Minute)
	b.StartsAt, b.EndsAt = fmtBT(start), fmtBT(start.Add(time.Duration(dur)*time.Minute))
	b.Guests, b.Name, b.Phone, b.Email, b.Notes = guests, name, phone, email, notes

	length := time.Duration(dur) * time.Minute
	b.TableNumber, _ = strconv.Atoi(r.FormValue("table"))
	if b.TableNumber == 0 {
		n, err := db.FindFreeTable(guests, b.StartsAt, b.EndsAt, false, func(t models.Table) bool { return qrBlocks(t, start, length) })
		if err != nil || n == 0 {
			return "No free spot fits that party at that time. Pick one yourself."
		}
		b.TableNumber = n
	} else if t, err := db.GetTable(b.TableNumber); err != nil {
		return "That spot doesn't exist."
	} else if qrBlocks(t, start, length) && !(old.Status == models.BookingSeated && old.TableNumber == t.Number) {
		// The seated party's own booking stays editable: scanning in
		// (SeatArrivedBooking) is what made their table "occupied".
		return t.Name() + " has guests sitting there now. Pick a later time or another spot."
	}
	return ""
}

func StaffBookingCreate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	user, _ := UserFromContext(r)
	b := models.Booking{Status: models.BookingConfirmed, Source: r.FormValue("source"), CreatedBy: user.Username}
	switch b.Source {
	case "phone", "other":
	case "walk_in":
		b.Status = models.BookingSeated
	default:
		b.Source = "phone"
	}
	base := bookingForm{Title: "New booking", Action: "/admin/bookings"}
	if msg := readStaffBooking(r, &b); msg != "" {
		renderBookingForm(w, r, formFromRequest(r, base, msg))
		return
	}
	if t, _ := db.GetTable(b.TableNumber); t.OutOfService {
		renderBookingForm(w, r, formFromRequest(r, base, t.Name()+" is out of service."))
		return
	}
	if err := db.CreateBooking(&b, ""); err != nil {
		renderBookingForm(w, r, formFromRequest(r, base, conflictMsg(err, b.TableNumber)))
		return
	}
	bookingSaved(w, b)
}

func conflictMsg(err error, table int) string {
	if errors.Is(err, db.ErrBookingConflict) {
		t, _ := db.GetTable(table)
		return fmt.Sprintf("%s already has a booking at that time.", t.Name())
	}
	return "Couldn't save the booking."
}

func bookingFromURL(w http.ResponseWriter, r *http.Request) (models.Booking, bool) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	b, err := db.GetBooking(id)
	if err != nil || b.Status == models.BookingHeld {
		http.Error(w, "Booking not found", http.StatusNotFound)
		return b, false
	}
	return b, true
}

func StaffBookingUpdate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	b, ok := bookingFromURL(w, r)
	if !ok {
		return
	}
	base := bookingForm{Title: "Edit booking", Action: fmt.Sprintf("/admin/bookings/%d", b.ID), Edit: true, Code: b.Code}
	if msg := readStaffBooking(r, &b); msg != "" {
		renderBookingForm(w, r, formFromRequest(r, base, msg))
		return
	}
	if err := db.UpdateBooking(b); err != nil {
		renderBookingForm(w, r, formFromRequest(r, base, conflictMsg(err, b.TableNumber)))
		return
	}
	bookingSaved(w, b)
}

// ----- Live floor -----

type floorTable struct {
	publicTable
	Status string   `json:"st"`     // free, soon, reserved, seated, occupied, off
	Label  string   `json:"lbl"`    // who's there / coming
	Day    []string `json:"day"`    // that day's active bookings at this spot
	Online bool     `json:"online"` // bookable online, regardless of service state
	Off    bool     `json:"off"`
	QR     bool     `json:"qr"`   // a guest is ordering at it by QR right now
	Help   bool     `json:"help"` // that guest pressed "Call waiter"
	// Bk is the booking behind the spot's status (reserved/late/seated/soon).
	Bk *spotBooking `json:"bk,omitempty"`
}

// spotBooking is a booking as the floor shows it. Start/End are unix ms:
// the page counts down on its own clock, no polling.
type spotBooking struct {
	ID     int    `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Phone  string `json:"phone"`
	Notes  string `json:"notes"`
	Guests int    `json:"guests"`
	Status string `json:"status"`
	Spot   string `json:"spot"`
	N      int    `json:"n"`
	Start  int64  `json:"start"`
	End    int64  `json:"end"`
}

func toSpotBooking(b models.Booking, spot string) *spotBooking {
	return &spotBooking{b.ID, b.Code, b.Name, b.Phone, b.Notes, b.Guests, b.Status, spot, b.TableNumber,
		b.Start().UnixMilli(), b.End().UnixMilli()}
}

type dayView struct {
	Date, Time, DateLabel string
	Live                  bool // showing right now (no time picked)
	Now                   int64
	Floor                 []floorTable
	Active, Covers, Free  int
	Help, Late, Seated    int
	Bookings              []*spotBooking // the day's live bookings, by start
}

// buildDay is the floor at date/hm: each spot's status and that day's
// bookings.
func buildDay(date, hm string) dayView {
	day, ok := parseDay(date)
	if !ok {
		day = today()
	}
	now := time.Now()
	mins, ok := parseHM(hm)
	live := !ok && day.Equal(today())
	if !ok {
		mins = now.Hour()*60 + now.Minute()
		if !live {
			mins = 19 * 60
		}
	}
	at := day.Add(time.Duration(mins) * time.Minute)
	v := dayView{Date: day.Format(dateLayout), Time: at.Format("15:04"), DateLabel: day.Format("Monday 2 January"),
		Live: live, Now: now.UnixMilli(), Floor: []floorTable{}, Bookings: []*spotBooking{}}

	tables, _ := db.GetAllTables()
	all, _ := db.GetBookingsOverlapping(fmtBT(day), fmtBT(day.AddDate(0, 0, 1)))
	for _, b := range all {
		if b.Active() && b.Status != models.BookingHeld {
			v.Active++
			v.Covers += b.Guests
		}
	}
	names := map[int]string{}
	for _, t := range tables {
		names[t.Number] = t.Name()
	}
	for _, b := range all {
		if b.Active() && b.Status != models.BookingHeld && b.End().After(now) {
			v.Bookings = append(v.Bookings, toSpotBooking(b, names[b.TableNumber]))
		}
	}
	for _, t := range tables {
		f := floorTable{publicTable: toPublic(t), Status: "free", Day: []string{}, Online: t.Bookable, Off: t.OutOfService,
			QR: t.Status == "occupied", Help: t.AssistanceRequested}
		if f.Help {
			v.Help++
		}
		for _, b := range all {
			if b.TableNumber == t.Number && b.Active() && b.Status != models.BookingHeld {
				f.Day = append(f.Day, fmt.Sprintf("%s–%s %s (%d)", b.StartsAt[11:], b.EndsAt[11:], b.Name, b.Guests))
			}
		}
		if b := overlaps(all, t.Number, at, at.Add(time.Minute)); b != nil {
			f.Status, f.Label, f.Bk = "reserved", b.Name+" · "+b.StartsAt[11:], toSpotBooking(*b, t.Name())
			switch {
			case b.Status == models.BookingSeated:
				f.Status = "seated"
				v.Seated++
			case b.Status == models.BookingHeld:
				f.Label, f.Bk = "A guest is booking it online", nil
			case live && !at.Before(b.Start()):
				// Their time has started and nobody's arrived: the spot stays
				// theirs until staff mark a no-show (or the time runs out).
				f.Status = "late"
				v.Late++
			}
		} else if t.Status == "occupied" && at.After(now.Add(-time.Hour)) && at.Before(now.Add(slotStep)) {
			f.Status, f.Label = "occupied", "Ordering by QR"
		} else if b := overlaps(all, t.Number, at, at.Add(time.Hour)); b != nil {
			f.Status, f.Label, f.Bk = "soon", b.Name+" · "+b.StartsAt[11:], toSpotBooking(*b, t.Name())
		}
		if t.OutOfService {
			f.Status, f.Label = "off", "Out of service"
		}
		if f.Status == "free" {
			v.Free++
		}
		v.Floor = append(v.Floor, f)
	}
	return v
}

func StaffFloor(w http.ResponseWriter, r *http.Request) { floorPage(w, r, false) }

func floorPage(w http.ResponseWriter, r *http.Request, work bool) {
	v := buildDay(r.FormValue("date"), r.FormValue("time"))
	t := ""
	if !v.Live {
		t = v.Time
	}
	pageFor(w, r, work, "bk_floor.html", "floor", map[string]interface{}{"Date": v.Date, "Time": t})
}

func StaffFloorData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(buildDay(r.FormValue("date"), r.FormValue("time")))
}

func StaffTableService(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(chi.URLParam(r, "number"))
	t, err := db.GetTable(n)
	if err != nil {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}
	db.SetTableOutOfService(n, !t.OutOfService)
	BroadcastFloor()
	w.WriteHeader(http.StatusNoContent)
}

// ----- Admin: map editor + settings pages -----

func AdminFloorEditor(w http.ResponseWriter, r *http.Request) {
	staffPage(w, r, "bk_editor.html", "editor", map[string]interface{}{"Shapes": models.TableShapes})
}

// AdminFloorData is every spot for the map editor (same shape as the live
// floor's data, without statuses).
func AdminFloorData(w http.ResponseWriter, r *http.Request) {
	tables, _ := db.GetAllTables()
	out := []floorTable{}
	for _, t := range tables {
		out = append(out, floorTable{publicTable: toPublic(t), Status: "free", Day: []string{}, Online: t.Bookable, Off: t.OutOfService})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

// spotDefaults are what a newly added spot of each shape starts with.
var spotDefaults = map[string]models.Table{
	"round":  {MinGuests: 2, MaxGuests: 4},
	"high":   {MinGuests: 1, MaxGuests: 4},
	"rect4":  {MinGuests: 2, MaxGuests: 4},
	"lounge": {MinGuests: 4, MaxGuests: 8},
	"set":    {MinGuests: 1, MaxGuests: 3, FullDay: true, AvailFrom: "10:00", AvailUntil: "19:00"},
	"cabana": {MinGuests: 2, MaxGuests: 4, FullDay: true, AvailFrom: "10:00", AvailUntil: "19:00"},
}

// AdminAddSpot creates a new spot (a new QR table number) on the map.
func AdminAddSpot(w http.ResponseWriter, r *http.Request) {
	t, ok := spotDefaults[r.FormValue("shape")]
	if !ok {
		http.Error(w, "Pick a shape.", http.StatusBadRequest)
		return
	}
	tables, _ := db.GetAllTables()
	for _, x := range tables {
		if x.Number >= t.Number {
			t.Number = x.Number + 1
		}
	}
	if t.Number == 0 {
		t.Number = 1
	}
	t.Shape, t.Bookable, t.Zone = r.FormValue("shape"), true, strings.TrimSpace(r.FormValue("zone"))
	if t.Zone == "" || len(t.Zone) > 40 {
		t.Zone = "New"
	}
	t.PosX, _ = strconv.ParseFloat(r.FormValue("x"), 64)
	t.PosY, _ = strconv.ParseFloat(r.FormValue("y"), 64)
	if t.PosX <= 0 || t.PosY <= 0 || t.PosX > 5000 || t.PosY > 5000 {
		t.PosX, t.PosY = 360, 500
	}
	if err := db.ApplyTableLayout(t); err != nil {
		http.Error(w, "Couldn't add the spot.", http.StatusInternalServerError)
		return
	}
	BroadcastFloor()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"n": t.Number})
}

// AdminDeleteSpot removes a spot, unless someone is at it or it still has
// upcoming bookings.
func AdminDeleteSpot(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(chi.URLParam(r, "number"))
	if c, err := db.CountUpcomingBookings(n); err != nil || c > 0 {
		http.Error(w, fmt.Sprintf("This spot has %d upcoming booking(s). Move or cancel them first.", c), http.StatusConflict)
		return
	}
	if err := db.DeleteTable(n); errors.Is(err, db.ErrTableOccupied) {
		http.Error(w, "A guest is ordering at this spot right now.", http.StatusConflict)
		return
	} else if err != nil {
		http.Error(w, "Couldn't remove the spot.", http.StatusInternalServerError)
		return
	}
	BroadcastFloor()
	w.WriteHeader(http.StatusNoContent)
}

func AdminBookingSettingsPage(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/settings#booking", http.StatusSeeOther)
}

// ---------- admin: floor layout + booking settings ----------

func AdminTableLayout(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(chi.URLParam(r, "number"))
	t, err := db.GetTable(n)
	if err != nil {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}
	t.Label = strings.TrimSpace(r.FormValue("label"))
	t.Zone = strings.TrimSpace(r.FormValue("zone"))
	t.Shape = r.FormValue("shape")
	t.MinGuests, _ = strconv.Atoi(r.FormValue("min_guests"))
	t.MaxGuests, _ = strconv.Atoi(r.FormValue("max_guests"))
	t.Bookable = r.FormValue("bookable") != ""
	t.Fee, _ = strconv.ParseFloat(r.FormValue("fee"), 64)
	t.MinSpend, _ = strconv.ParseFloat(r.FormValue("min_spend"), 64)
	t.Duration, _ = strconv.Atoi(r.FormValue("duration"))
	t.AvailFrom, t.AvailUntil = r.FormValue("avail_from"), r.FormValue("avail_until")
	t.FullDay = r.FormValue("full_day") != ""
	validShape := false
	for _, s := range models.TableShapes {
		validShape = validShape || s == t.Shape
	}
	_, fromOK := parseHM(t.AvailFrom)
	_, untilOK := parseHM(t.AvailUntil)
	msg := ""
	switch {
	case len(t.Label) > 12:
		msg = "Label must be 12 characters or fewer."
	case t.Zone == "" || len(t.Zone) > 40:
		msg = "Enter a zone (up to 40 characters)."
	case !validShape:
		msg = "Pick a shape."
	case t.MinGuests < 1 || t.MaxGuests < t.MinGuests || t.MaxGuests > 50:
		msg = "Seats: min at least 1, max at least min, at most 50."
	case t.Fee < 0 || t.Fee > 100000 || t.MinSpend < 0 || t.MinSpend > 100000:
		msg = "Fee and minimum spend must be between 0 and 100000."
	case t.Duration != 0 && (t.Duration < 15 || t.Duration > 24*60):
		msg = "Length must be 0 (venue default) or 15 to 1440 minutes."
	case (t.AvailFrom != "" && !fromOK) || (t.AvailUntil != "" && !untilOK):
		msg = "Online from/until must be times like 18:00, or empty."
	}
	if msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	if err := db.UpdateTableLayout(t); err != nil {
		http.Error(w, "Couldn't save", http.StatusInternalServerError)
		return
	}
	BroadcastFloor()
	w.WriteHeader(http.StatusNoContent)
}

// AdminFloorPreset reloads the EGO floor layout (see db.ApplyEGOFloor).
func AdminFloorPreset(w http.ResponseWriter, r *http.Request) {
	if err := db.ApplyEGOFloor(); err != nil {
		http.Error(w, "Couldn't apply the layout", http.StatusInternalServerError)
		return
	}
	BroadcastFloor()
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

func AdminTablePosition(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(chi.URLParam(r, "number"))
	x, err1 := strconv.ParseFloat(r.FormValue("x"), 64)
	y, err2 := strconv.ParseFloat(r.FormValue("y"), 64)
	if err1 != nil || err2 != nil || x < 0 || y < 0 || x > 5000 || y > 5000 {
		http.Error(w, "Bad position", http.StatusBadRequest)
		return
	}
	db.SetTablePosition(n, x, y)
	BroadcastFloor()
	w.WriteHeader(http.StatusNoContent)
}

func AdminBookingSettings(w http.ResponseWriter, r *http.Request) {
	s, _ := db.GetSettings()
	s.BookingEnabled = r.FormValue("enabled") != ""
	s.BookingOpen, s.BookingClose = r.FormValue("open"), r.FormValue("close")
	s.BookingDuration, _ = strconv.Atoi(r.FormValue("duration"))
	s.BookingDaysAhead, _ = strconv.Atoi(r.FormValue("days_ahead"))
	s.VenuePhone = strings.TrimSpace(r.FormValue("phone"))
	open, ok1 := parseHM(s.BookingOpen)
	closeAt, ok2 := parseHM(s.BookingClose)
	msg := ""
	switch {
	case !ok1 || !ok2 || closeAt <= open:
		msg = "Last arrival must be after first arrival, on the same day."
	case s.BookingDuration < 15 || s.BookingDuration > 24*60:
		msg = "Duration must be between 15 and 1440 minutes."
	case s.BookingDaysAhead < 1 || s.BookingDaysAhead > 365:
		msg = "Days ahead must be between 1 and 365."
	case len(s.VenuePhone) > 30:
		msg = "Phone number is too long."
	}
	if msg == "" {
		if err := db.SetBookingSettings(s); err != nil {
			msg = "Couldn't save."
		}
	}
	saved, _ := db.GetSettings()
	tmpl := template.Must(template.ParseFiles("templates/adm_settings.html"))
	tmpl.ExecuteTemplate(w, "booking-settings", map[string]interface{}{"Settings": saved, "Msg": msg, "Saved": msg == ""})
}
