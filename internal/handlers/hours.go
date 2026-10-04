package handlers

import (
	"bitebox/internal/auth"
	"bytes"
	"html/template"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
)

// Menus & hours (/admin/menu/hours): scheduled menus, the kitchen/bar close
// switches and the guest menu's Recommended / Popular rows. The rules
// themselves are models.Schedule; every change here re-pushes guest menus.

var stationName = map[string]string{models.DepartmentKitchen: "Kitchen", models.DepartmentBar: "Bar"}

// lastChoices are the "last orders" options (minutes before closing).
var lastChoices = []int{0, 5, 10, 15, 20, 30, 45, 60}

// stationView is one station's switch: open or closed, and what's being
// served there right now.
type stationView struct {
	Dept, Name string
	Closed     bool
	Until      string // "" = until reopened
	Serving    string // e.g. "Dinner · last orders 20:45", or why nothing is
	Hours      models.StationHours
	OffHours   bool   // outside its hours right now
	Busy       int    // extra minutes guests are told to expect (0 = not busy)
	Today      string // a special day's effect today, e.g. "Special hours today: 13:00–20:00"
	HoursText  string // "12:00–21:00 · last orders 15 min early", or today's when it varies by day
}

// activeStations are the stations this venue actually runs: those with at
// least one category, so a club that only serves drinks just gets the bar.
// With no categories yet, both.
func activeStations() []string {
	cats, _ := db.GetCategories()
	var out []string
	for _, d := range []string{models.DepartmentKitchen, models.DepartmentBar} {
		if slices.ContainsFunc(cats, func(c models.Category) bool { return c.Department == d }) {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return []string{models.DepartmentKitchen, models.DepartmentBar}
	}
	return out
}

func stationViews() []stationView {
	r := loadMenuRules()
	products, _ := db.GetProducts()
	var out []stationView
	for _, d := range activeStations() {
		v := stationView{Dept: d, Name: stationName[d], Hours: r.sched.Hours[d], Busy: r.sched.BusyNow(d, r.now)}
		v.HoursText = v.Hours.Label()
		if v.Hours.Set && v.Hours.PerDay {
			if dh, open := v.Hours.On(r.now.Weekday()); open {
				v.HoursText = "Today " + models.Clock(dh.Open) + "–" + models.Clock(dh.Close) + " · varies by day"
			} else {
				v.HoursText = "Closed today · varies by day"
			}
		}
		for _, sp := range r.sched.Specials {
			if sp.Covers(r.now) {
				if sp.Closed {
					v.Today = "Closed today (special day)"
				} else if h, ok := sp.Hours[d]; ok {
					if h.Closed {
						v.Today = "Closed today (special day)"
					} else {
						v.Today = "Special hours today: " + models.Clock(h.Open) + "–" + models.Clock(h.Close)
					}
				}
				if v.Today != "" && sp.Note != "" {
					v.Today += " · " + sp.Note
				}
				break
			}
		}
		in, _ := r.sched.WithinHours(d, r.now)
		v.OffHours = !in
		closed, until := r.sched.StationClosed(d, r.now)
		v.Closed = closed
		if closed && !until.IsZero() {
			v.Until = whenLabel(until, r.now)
		}
		// What's on right now for this station.
		var names []string
		var last time.Time
		orderable := 0
		for _, p := range products {
			if r.dept[p.Category] != d {
				continue
			}
			for _, z := range r.zones { // orderable anywhere counts
				if st := r.item(p, z); st.Orderable {
					orderable++
					if st.Until.After(last) {
						last = st.Until
					}
					break
				}
			}
		}
		if r.sched.HasMenus() {
			for _, m := range r.sched.Menus {
				if !m.Enabled {
					continue
				}
				if _, on := m.ServingNow(r.now); on {
					for _, p := range products {
						if r.dept[p.Category] == d && m.Serves(p) {
							if !slices.Contains(names, m.Name) {
								names = append(names, m.Name)
							}
							break
						}
					}
				}
			}
		}
		switch {
		case closed:
			v.Serving = "Closed by hand: guests can't order from here"
		case v.OffHours:
			v.Serving = "Outside its hours"
			if next := r.sched.NextHoursOpen(d, r.now); !next.IsZero() {
				v.Serving += " · opens " + whenLabel(next, r.now)
			}
		case orderable == 0 && r.sched.HasMenus():
			v.Serving = "No menu on right now"
			if next := r.sched.NextStart(r.now, nil); !next.IsZero() {
				v.Serving += " · next at " + whenLabel(next, r.now)
			}
		case len(names) > 0:
			v.Serving = strings.Join(names, " + ") + " · " + strconv.Itoa(orderable) + " items"
			if !last.IsZero() {
				v.Serving += " · orders until " + last.Format("15:04")
			}
		default:
			v.Serving = "Open · " + strconv.Itoa(orderable) + " items"
		}
		out = append(out, v)
	}
	return out
}

// menuView is a menu card on the page.
type menuView struct {
	models.ServiceMenu
	Serves string // "Everything" / "Brunch, Salads + 2 items"
	OnNow  bool
}

func menuViews(menus []models.ServiceMenu) []menuView {
	now := time.Now()
	out := make([]menuView, 0, len(menus))
	for _, m := range menus {
		v := menuView{ServiceMenu: m}
		if m.Everything {
			v.Serves = "Everything on the menu"
		} else {
			parts := append([]string{}, m.Categories...)
			s := strings.Join(parts, ", ")
			if n := len(m.Products); n > 0 {
				if s != "" {
					s += " + "
				}
				s += strconv.Itoa(n) + " single item"
				if n != 1 {
					s += "s"
				}
			}
			if s == "" {
				s = "Nothing picked yet"
			}
			v.Serves = s
		}
		_, v.OnNow = m.ServingNow(now)
		v.OnNow = v.OnNow && m.Enabled
		out = append(out, v)
	}
	return out
}

// hoursData is everything the Menus & hours page (and its live parts) show.
func hoursData() map[string]interface{} {
	all, _ := db.ListServiceMenus()
	var menus, deals []models.ServiceMenu
	for _, m := range all {
		if m.Kind == "price" {
			deals = append(deals, m)
		} else {
			menus = append(menus, m)
		}
	}
	settings, _ := db.GetSettings()
	products, _ := db.GetAllProducts()
	var picks, others []models.Product
	for _, p := range products {
		if p.Recommended {
			picks = append(picks, p)
		} else if p.IsAvailable {
			others = append(others, p)
		}
	}
	// Popular preview: what guests would see right now with these options.
	var popular []string
	popTitle := ""
	if m, err := guestMenu(""); err == nil {
		popTitle = m.PopularTitle
		for _, g := range m.Groups {
			if g.Name == "Popular" {
				for _, p := range g.Products {
					popular = append(popular, p.Name)
				}
			}
		}
	}
	zones := mustZones()
	return map[string]interface{}{
		"MultiStation": len(activeStations()) > 1,
		"ShowAreas":    len(zones) > 1, // one area (or none) is just the venue: the stations cover it
		"Stations":     stationViews(),
		"Target":       "/admin/stations/",
		"EditHours":    true,
		"Areas":        areaViews(),
		"Specials":     specialViews(),
		"Warnings":     setupWarnings(),
		"Zones":        zones,
		"Today":        time.Now().Format("2006-01-02"),
		"NowClock":     time.Now().Format("15:04"),
		"LastChoices":  lastChoices,
		"Menus":        menuViews(menus),
		"Deals":        menuViews(deals),
		"Settings":     settings,
		"Picks":        picks,
		"Others":       others,
		"Popular":      popular,
		"PopularTitle": popTitle,
		"RankedAt":     rankedLabel(),
		"DayChoices":   []int{7, 30, 90},
		"CountChoices": []int{4, 6, 8, 10},
	}
}

func hoursTemplates() *template.Template {
	return template.Must(template.ParseFiles("templates/bk_layout.html", "templates/adm_hours.html", "templates/_stations.html"))
}

func AdminHoursPage(w http.ResponseWriter, r *http.Request) {
	staffPage(w, r, "adm_hours.html", "hours", hoursData(), "_stations.html")
}

// hoursLiveHTML is the page's live parts as OOB swaps: the kitchen/bar
// switches, which menus are on now, and the recommended / popular previews.
func hoursLiveHTML() []byte {
	t, d := hoursTemplates(), hoursData()
	var out []byte
	for _, part := range []struct{ id, tmpl string }{
		{"stationsWrap", "_stations.html"}, {"menusWrap", "hours-menus"}, {"picksWrap", "hours-picks"}, {"popularWrap", "hours-popular"}, {"rankWrap", "hours-rank"}, {"areasWrap", "hours-areas"}, {"specialsWrap", "hours-specials"}, {"warningsWrap", "hours-warnings"}, {"dealsWrap", "hours-deals"},
	} {
		var buf bytes.Buffer
		if err := t.ExecuteTemplate(&buf, part.tmpl, d); err == nil {
			out = append(out, oobWrap(part.id, buf.Bytes())...)
		}
	}
	return out
}

const topicHours = "admin:hours"

// BroadcastHours refreshes every open Menus & hours page.
func BroadcastHours() { Hub.Broadcast(topicHours, hoursLiveHTML()) }

// AdminHoursWS keeps an open Menus & hours page live.
func AdminHoursWS(w http.ResponseWriter, r *http.Request) {
	sessionID, _ := auth.SessionIDFromRequest(r)
	pumpWS(w, r, []string{topicHours}, hoursLiveHTML(), sessionID)
}

// rankedLabel: "Ranked 3 Sep · next by 3 Oct, sooner on a big spike".
func rankedLabel() string {
	t := db.PopularityRankedAt()
	if t.IsZero() {
		return "Not ranked yet"
	}
	return "Ranked " + t.Format("2 Jan") + " · next by " + t.Add(models.RerankEvery).Format("2 Jan") + ", sooner on a sales spike"
}

// AdminRerankPopularity rebuilds the popularity snapshot now.
func AdminRerankPopularity(w http.ResponseWriter, r *http.Request) {
	rerankPopularity(true)
	w.WriteHeader(http.StatusNoContent)
}

// AdminToggleRecommended stars / unstars a dish for the guest menu.
func AdminToggleRecommended(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	p, err := db.GetProductByID(id)
	if err != nil {
		http.Error(w, "Item not found", http.StatusNotFound)
		return
	}
	db.SetProductRecommended(id, !p.Recommended)
	BroadcastAllMenus()
	w.WriteHeader(http.StatusNoContent)
}

// renderStationsHTML is the "Right now" card (also re-rendered by its buttons).
func renderStationsHTML(w http.ResponseWriter, target string) {
	var buf bytes.Buffer
	template.Must(template.ParseFiles("templates/_stations.html")).Execute(&buf, map[string]interface{}{"Stations": stationViews(), "Target": target, "EditHours": target == "/admin/stations/", "LastChoices": lastChoices})
	w.Write(buf.Bytes())
}

// closeUntil turns a button's choice into the stored closed-until value.
func closeUntil(choice string, now time.Time) (string, bool) {
	switch choice {
	case "open":
		return "", true
	case "manual":
		return models.StationClosedManual, true
	case "today": // venues run past midnight: "the rest of today" ends at 05:00
		t := time.Date(now.Year(), now.Month(), now.Day(), 5, 0, 0, 0, now.Location())
		if !t.After(now) {
			t = t.AddDate(0, 0, 1)
		}
		return t.Format(time.RFC3339), true
	}
	if mins, err := strconv.Atoi(choice); err == nil && mins > 0 && mins <= 24*60 {
		return now.Add(time.Duration(mins) * time.Minute).Format(time.RFC3339), true
	}
	return "", false
}

func setStation(w http.ResponseWriter, r *http.Request, target string) {
	dept := chi.URLParam(r, "dept")
	if stationName[dept] == "" {
		http.Error(w, "Unknown station", http.StatusBadRequest)
		return
	}
	v, ok := closeUntil(r.FormValue("close"), time.Now())
	if !ok {
		http.Error(w, "Invalid choice", http.StatusBadRequest)
		return
	}
	if err := db.SetStationClosed(dept, v); err != nil {
		http.Error(w, "Couldn't save", http.StatusInternalServerError)
		return
	}
	action := "station_close"
	if v == "" {
		action = "station_open"
	}
	db.LogAudit(actorUsername(r), action, stationName[dept], r.FormValue("close"))
	BroadcastAllMenus()
	renderStationsHTML(w, target)
}

// AdminSetStation closes or reopens the kitchen/bar (Menus & hours page).
func AdminSetStation(w http.ResponseWriter, r *http.Request) { setStation(w, r, "/admin/stations/") }

// WorkerSetStation is the same switch from the staff app's Sold-out board:
// kitchen staff for the kitchen, bar staff for the bar, managers for both.
func WorkerSetStation(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r)
	if !canSwitchStation(user, chi.URLParam(r, "dept")) {
		http.Error(w, "403 Forbidden", http.StatusForbidden)
		return
	}
	setStation(w, r, "/worker/stations/")
}

func canSwitchStation(u models.User, dept string) bool {
	if stationName[dept] == "" {
		return false
	}
	if u.Role == models.RoleAdmin || u.Department == models.DepartmentSuperworker {
		return true
	}
	return u.Department == dept
}

// AdminSetStationHours saves a station's simple daily hours (or clears them).
func AdminSetStationHours(w http.ResponseWriter, r *http.Request) {
	dept := chi.URLParam(r, "dept")
	if stationName[dept] == "" {
		http.Error(w, "Unknown station", http.StatusBadRequest)
		return
	}
	h := models.StationHours{}
	if r.FormValue("clear") == "" {
		last, _ := strconv.Atoi(r.FormValue("last_orders"))
		valid := func(open, close int, ok bool) bool {
			length := close - open
			if length <= 0 {
				length += 24 * 60
			}
			return ok && open != close && last >= 0 && last < length
		}
		open, ok1 := models.ClockMinutes(r.FormValue("open"))
		close, ok2 := models.ClockMinutes(r.FormValue("close_at"))
		h = models.StationHours{Set: true, Open: open, Close: close, LastOrders: last, PerDay: r.FormValue("per_day") == "1"}
		if h.PerDay {
			for i := range h.Week {
				k := strconv.Itoa(i + 1)
				if r.FormValue("d"+k+"_closed") != "" {
					h.Week[i] = models.DayHours{Closed: true}
					continue
				}
				o, okA := models.ClockMinutes(r.FormValue("d" + k + "_open"))
				c, okB := models.ClockMinutes(r.FormValue("d" + k + "_close"))
				if !valid(o, c, okA && okB) {
					http.Error(w, "Check "+models.WeekdayShort[i]+": opening and closing must differ, and last orders must fall inside them.", http.StatusBadRequest)
					return
				}
				h.Week[i] = models.DayHours{Open: o, Close: c}
			}
			h.Open, h.Close = h.Week[0].Open, h.Week[0].Close
		} else if !valid(open, close, ok1 && ok2) {
			http.Error(w, "Check the times: opening and closing must differ, and last orders must fall inside them.", http.StatusBadRequest)
			return
		}
	}
	if err := db.SetStationHours(dept, h); err != nil {
		http.Error(w, "Couldn't save", http.StatusInternalServerError)
		return
	}
	db.LogAudit(actorUsername(r), "station_hours", stationName[dept], h.Label())
	BroadcastAllMenus()
	renderStationsHTML(w, "/admin/stations/")
}

// setBusy marks a station busy for the next hour (guests see the extra wait)
// or clears it; shared by the Menus & hours page and the staff app.
func setBusy(w http.ResponseWriter, r *http.Request, target string) {
	dept := chi.URLParam(r, "dept")
	minutes, _ := strconv.Atoi(r.FormValue("minutes"))
	if stationName[dept] == "" || minutes < 0 || minutes > 120 {
		http.Error(w, "Invalid choice", http.StatusBadRequest)
		return
	}
	if err := db.SetStationBusy(dept, minutes, time.Now().Add(time.Hour)); err != nil {
		http.Error(w, "Couldn't save", http.StatusInternalServerError)
		return
	}
	db.LogAudit(actorUsername(r), "station_busy", stationName[dept], strconv.Itoa(minutes)+" min")
	BroadcastAllMenus()
	renderStationsHTML(w, target)
}

func AdminSetBusy(w http.ResponseWriter, r *http.Request) { setBusy(w, r, "/admin/stations/") }

func WorkerSetBusy(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r)
	if !canSwitchStation(user, chi.URLParam(r, "dept")) {
		http.Error(w, "403 Forbidden", http.StatusForbidden)
		return
	}
	setBusy(w, r, "/worker/stations/")
}

// specialView is a special day as listed on the page.
type specialView struct {
	models.SpecialDay
	Summary string // "Closed" / "Kitchen 13:00–20:00 · Bar closed"
	Now     bool   // covers today
}

func specialViews() []specialView {
	list, _ := db.ListSpecialDays()
	now := time.Now()
	out := make([]specialView, 0, len(list))
	for _, d := range list {
		v := specialView{SpecialDay: d, Now: d.Covers(now)}
		if d.Closed {
			v.Summary = "Closed all day"
		} else {
			var parts []string
			for _, dept := range activeStations() {
				if h, ok := d.Hours[dept]; ok {
					if h.Closed {
						parts = append(parts, stationName[dept]+" closed")
					} else {
						parts = append(parts, stationName[dept]+" "+models.Clock(h.Open)+"–"+models.Clock(h.Close))
					}
				}
			}
			v.Summary = strings.Join(parts, " · ")
		}
		out = append(out, v)
	}
	return out
}

// areaView is one table area's pause switch.
type areaView struct {
	Zone   string
	Closed bool
	Until  string
	Menus  []string // menus and offers aimed at this area only, e.g. "Beach day 10:00–22:00"
}

func areaViews() []areaView {
	s, _ := db.GetSchedule()
	zones, _ := db.Zones()
	now := time.Now()
	out := make([]areaView, 0, len(zones))
	for _, z := range zones {
		closed, until := s.AreaClosed(z, now)
		v := areaView{Zone: z, Closed: closed}
		for _, m := range s.Menus {
			if m.Enabled && len(m.Zones) > 0 && m.AppliesTo(z) {
				v.Menus = append(v.Menus, m.Name+" "+models.Clock(m.Start)+"–"+models.Clock(m.End))
			}
		}
		for _, m := range s.Prices {
			if m.Enabled && len(m.Zones) > 0 && m.AppliesTo(z) {
				v.Menus = append(v.Menus, m.Name+" "+m.DealLabel())
			}
		}
		if closed && !until.IsZero() {
			v.Until = whenLabel(until, now)
		}
		out = append(out, v)
	}
	return out
}

// AdminSetArea pauses or resumes ordering for one table area ("the beach,
// it's windy"), answering with the fresh areas block.
func AdminSetArea(w http.ResponseWriter, r *http.Request) {
	zone := r.FormValue("zone")
	known, _ := db.Zones()
	if !slices.Contains(known, zone) {
		http.Error(w, "Unknown area", http.StatusBadRequest)
		return
	}
	v, ok := closeUntil(r.FormValue("close"), time.Now())
	if !ok {
		http.Error(w, "Invalid choice", http.StatusBadRequest)
		return
	}
	if err := db.SetAreaClosed(zone, v); err != nil {
		http.Error(w, "Couldn't save", http.StatusInternalServerError)
		return
	}
	action := "area_pause"
	if v == "" {
		action = "area_resume"
	}
	db.LogAudit(actorUsername(r), action, zone, r.FormValue("close"))
	BroadcastAllMenus()
	var buf bytes.Buffer
	hoursTemplates().ExecuteTemplate(&buf, "hours-areas", hoursData())
	w.Write(buf.Bytes())
}

// AdminSpecialDayForm is the add/edit special day layer.
func AdminSpecialDayForm(w http.ResponseWriter, r *http.Request) {
	d := models.SpecialDay{From: time.Now().Format("2006-01-02"), To: time.Now().Format("2006-01-02"), Closed: true}
	if id, err := strconv.Atoi(chi.URLParam(r, "id")); err == nil {
		if got, err := db.GetSpecialDay(id); err == nil {
			d = got
		}
	}
	renderSpecialDayForm(w, d, "")
}

func renderSpecialDayForm(w http.ResponseWriter, d models.SpecialDay, errMsg string) {
	type st struct {
		Dept, Name      string
		Set, Closed     bool
		OpenAt, CloseAt string
	}
	var sts []st
	for _, dept := range activeStations() {
		h, set := d.Hours[dept]
		v := st{Dept: dept, Name: stationName[dept], Set: set, Closed: h.Closed, OpenAt: "12:00", CloseAt: "22:00"}
		if set && !h.Closed {
			v.OpenAt, v.CloseAt = models.Clock(h.Open), models.Clock(h.Close)
		}
		sts = append(sts, v)
	}
	var buf bytes.Buffer
	err := template.Must(template.ParseFiles("templates/bk_layout.html", "templates/_special_day_form.html")).ExecuteTemplate(&buf, "special-day-form",
		map[string]interface{}{"D": d, "Stations": sts, "Error": errMsg})
	if err != nil {
		http.Error(w, "Failed to render", http.StatusInternalServerError)
		return
	}
	w.Write(buf.Bytes())
}

// AdminSpecialDaySave adds or updates a special day.
func AdminSpecialDaySave(w http.ResponseWriter, r *http.Request) {
	d := models.SpecialDay{Hours: map[string]models.DayHours{}}
	if id, err := strconv.Atoi(chi.URLParam(r, "id")); err == nil {
		d.ID = id
	}
	d.From, d.To = r.FormValue("from"), r.FormValue("to")
	if d.To == "" {
		d.To = d.From
	}
	d.Note = strings.TrimSpace(r.FormValue("note"))
	d.Closed = r.FormValue("kind") == "closed"
	var problem string
	f, err1 := time.Parse("2006-01-02", d.From)
	t, err2 := time.Parse("2006-01-02", d.To)
	switch {
	case err1 != nil || err2 != nil:
		problem = "Pick the date (and an end date for a range)."
	case t.Before(f):
		problem = "The end date is before the start."
	case len(d.Note) > 80:
		problem = "Keep the note short (80 characters)."
	}
	if problem == "" && !d.Closed {
		for _, dept := range activeStations() {
			switch r.FormValue(dept + "_mode") {
			case "closed":
				d.Hours[dept] = models.DayHours{Closed: true}
			case "hours":
				o, ok1 := models.ClockMinutes(r.FormValue(dept + "_open"))
				c, ok2 := models.ClockMinutes(r.FormValue(dept + "_close"))
				if !ok1 || !ok2 || o == c {
					problem = "Check the " + stationName[dept] + " hours."
				}
				d.Hours[dept] = models.DayHours{Open: o, Close: c}
			}
		}
		if problem == "" && len(d.Hours) == 0 {
			problem = "Choose closed, or change the hours for these dates."
		}
	}
	if problem != "" {
		renderSpecialDayForm(w, d, problem)
		return
	}
	if err := db.SaveSpecialDay(d); err != nil {
		http.Error(w, "Couldn't save", http.StatusInternalServerError)
		return
	}
	db.LogAudit(actorUsername(r), "special_day", d.Label(), d.Note)
	BroadcastAllMenus()
	w.Header().Set("HX-Trigger", `{"closeLayer":true}`)
	w.WriteHeader(http.StatusNoContent)
}

func AdminSpecialDayDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if err := db.DeleteSpecialDay(id); err != nil {
		http.Error(w, "Couldn't delete", http.StatusInternalServerError)
		return
	}
	BroadcastAllMenus()
	w.WriteHeader(http.StatusNoContent)
}

// AdminServiceMenuForm is the add/edit menu layer.
func AdminServiceMenuForm(w http.ResponseWriter, r *http.Request) {
	m := models.ServiceMenu{Days: "1234567", Start: 12 * 60, End: 23 * 60, LastOrders: 15, Enabled: true, Kind: "menu"}
	if r.URL.Query().Get("kind") == "price" { // a happy hour: 18:00–20:00, 20% off
		m = models.ServiceMenu{Days: "1234567", Start: 18 * 60, End: 20 * 60, Enabled: true, Kind: "price", DiscType: "percent", DiscValue: 20}
	}
	if z := r.URL.Query().Get("zone"); slices.Contains(mustZones(), z) { // "Own hours" on an area
		m.Zones = []string{z}
	}
	if id, err := strconv.Atoi(chi.URLParam(r, "id")); err == nil {
		if got, err := db.GetServiceMenu(id); err == nil {
			m = got
		}
	}
	renderServiceMenuForm(w, m, "")
}

func renderServiceMenuForm(w http.ResponseWriter, m models.ServiceMenu, errMsg string) {
	cats, _ := db.GetCategories()
	products, _ := db.GetAllProducts()
	type catView struct {
		models.Category
		Products []models.Product
	}
	var kitchen, bar []catView
	for _, c := range cats {
		cv := catView{Category: c}
		for _, p := range products {
			if p.Category == c.Name {
				cv.Products = append(cv.Products, p)
			}
		}
		if len(cv.Products) == 0 {
			continue
		}
		if c.Department == models.DepartmentBar {
			bar = append(bar, cv)
		} else {
			kitchen = append(kitchen, cv)
		}
	}
	days := make([]struct {
		ISO   int
		Label string
		On    bool
	}, 7)
	for i := range days {
		days[i].ISO, days[i].Label, days[i].On = i+1, models.WeekdayShort[i], m.RunsOnDay(i+1)
	}
	var buf bytes.Buffer
	zones, _ := db.Zones()
	err := template.Must(template.ParseFiles("templates/bk_layout.html", "templates/_service_menu_form.html")).ExecuteTemplate(&buf, "service-menu-form", map[string]interface{}{
		"M": m, "Days": days, "Kitchen": kitchen, "Bar": bar, "Error": errMsg, "Zones": zones,
		"StartClock": models.Clock(m.Start), "EndClock": models.Clock(m.End),
		"BufferChoices": lastChoices,
	})
	if err != nil {
		http.Error(w, "Failed to render", http.StatusInternalServerError)
		return
	}
	w.Write(buf.Bytes())
}

// AdminServiceMenuSave adds or updates a menu from the layer's form.
func AdminServiceMenuSave(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	m := models.ServiceMenu{Enabled: true, Kind: "menu"}
	if r.FormValue("kind") == "price" {
		m.Kind = "price"
	}
	if id, err := strconv.Atoi(chi.URLParam(r, "id")); err == nil {
		old, err := db.GetServiceMenu(id)
		if err != nil {
			http.Error(w, "Menu not found", http.StatusNotFound)
			return
		}
		m.ID, m.Enabled, m.Kind = id, old.Enabled, old.Kind
	}
	if m.Kind == "price" {
		m.LastOrders = 0 // a price rule simply ends
		m.DiscType = r.FormValue("disc_type")
		m.DiscValue, _ = strconv.ParseFloat(strings.Replace(r.FormValue("disc_value"), ",", ".", 1), 64)
	}
	m.Name = strings.TrimSpace(r.FormValue("name"))
	var days []string
	for _, d := range r.Form["days"] {
		if len(d) == 1 && d >= "1" && d <= "7" && !strings.Contains(strings.Join(days, ""), d) {
			days = append(days, d)
		}
	}
	sort.Strings(days)
	m.Days = strings.Join(days, "")
	start, ok1 := models.ClockMinutes(r.FormValue("start"))
	end, ok2 := models.ClockMinutes(r.FormValue("end"))
	m.Start, m.End = start, end
	m.LastOrders, _ = strconv.Atoi(r.FormValue("last_orders"))
	m.Everything = r.FormValue("serves") == "everything"
	if r.FormValue("where") == "some" {
		known, _ := db.Zones()
		for _, z := range r.Form["zones"] {
			if slices.Contains(known, z) && !slices.Contains(m.Zones, z) {
				m.Zones = append(m.Zones, z)
			}
		}
	}
	if !m.Everything {
		valid := map[string]bool{}
		if cats, err := db.GetCategories(); err == nil {
			for _, c := range cats {
				valid[c.Name] = true
			}
		}
		for _, c := range r.Form["categories"] {
			if valid[c] {
				m.Categories = append(m.Categories, c)
			}
		}
		for _, p := range r.Form["products"] {
			if id, err := strconv.Atoi(p); err == nil && id > 0 {
				m.Products = append(m.Products, id)
			}
		}
	}
	length := m.End - m.Start
	if length <= 0 {
		length += 24 * 60
	}
	var problem string
	switch {
	case m.Name == "" || len(m.Name) > 60:
		problem = "Give the menu a name (up to 60 characters)."
	case m.Days == "":
		problem = "Pick at least one day."
	case !ok1 || !ok2:
		problem = "Set a start and an end time."
	case m.Start == m.End:
		problem = "Start and end can't be the same time."
	case m.LastOrders < 0 || m.LastOrders >= length:
		problem = "Last orders must be inside the menu's hours."
	case r.FormValue("where") == "some" && len(m.Zones) == 0:
		problem = "Pick at least one area, or choose \"All areas\"."
	case m.Kind == "price" && m.DiscType == "percent" && (m.DiscValue <= 0 || m.DiscValue > 90):
		problem = "A percentage between 1 and 90, please."
	case m.Kind == "price" && m.DiscType == "amount" && (m.DiscValue <= 0 || m.DiscValue > 500):
		problem = "Take off an amount above €0."
	case m.Kind == "price" && m.DiscType != "percent" && m.DiscType != "amount":
		problem = "Choose a % or € discount."
	case !m.Everything && len(m.Categories) == 0 && len(m.Products) == 0:
		problem = "Pick what this menu serves: everything, or some categories or items."
	}
	if problem != "" { // 200 on purpose: htmx only swaps a successful reply back into the layer
		renderServiceMenuForm(w, m, problem)
		return
	}
	if _, err := db.SaveServiceMenu(m); err != nil {
		http.Error(w, "Couldn't save the menu", http.StatusInternalServerError)
		return
	}
	BroadcastAllMenus()
	w.Header().Set("HX-Refresh", "true")
}

func AdminServiceMenuToggle(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	m, err := db.GetServiceMenu(id)
	if err != nil {
		http.Error(w, "Menu not found", http.StatusNotFound)
		return
	}
	db.SetServiceMenuEnabled(id, !m.Enabled)
	BroadcastAllMenus()
	w.WriteHeader(http.StatusNoContent) // the page is live: BroadcastHours redraws it
}

func AdminServiceMenuDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	if err := db.DeleteServiceMenu(id); err != nil {
		http.Error(w, "Couldn't delete", http.StatusInternalServerError)
		return
	}
	BroadcastAllMenus()
	w.WriteHeader(http.StatusNoContent) // the page is live: BroadcastHours redraws it
}

// AdminGuestSuggestions saves the Recommended / Popular options.
func AdminGuestSuggestions(w http.ResponseWriter, r *http.Request) {
	cur, _ := db.GetSettings()
	cur.ShowRecommended = r.FormValue("recommended") != ""
	cur.ShowPopular = r.FormValue("popular") != ""
	if t := strings.TrimSpace(r.FormValue("recommended_title")); t != "" && len(t) <= 40 {
		cur.RecommendedTitle = t
	}
	oldDays := cur.PopularDays
	if d, _ := strconv.Atoi(r.FormValue("popular_days")); d == 7 || d == 30 || d == 90 {
		cur.PopularDays = d
	}
	cur.SortByPopularity = r.FormValue("sort_popular") != ""
	cur.ShowUpcoming = r.FormValue("show_upcoming") != ""
	if c, _ := strconv.Atoi(r.FormValue("popular_count")); c >= 2 && c <= 12 {
		cur.PopularCount = c
	}
	if sc := r.FormValue("popular_scope"); sc == "all" || sc == models.DepartmentKitchen || sc == models.DepartmentBar {
		cur.PopularScope = sc
	}
	if err := db.SetGuestSuggestions(cur); err != nil {
		http.Error(w, "Couldn't save", http.StatusInternalServerError)
		return
	}
	if cur.PopularDays != oldDays { // a new period needs a fresh snapshot
		rerankPopularity(true)
	}
	BroadcastAllMenus()
	w.WriteHeader(http.StatusNoContent)
}

// AdminHoursPreview answers "what would a table in this area see at this
// day and time?": notices, and how many dishes per section, orderable now
// or greyed for later.
func AdminHoursPreview(w http.ResponseWriter, r *http.Request) {
	day, err := time.ParseInLocation("2006-01-02 15:04", r.FormValue("date")+" "+r.FormValue("time"), time.Local)
	if err != nil {
		http.Error(w, "Pick a day and a time", http.StatusBadRequest)
		return
	}
	zone := r.FormValue("zone")
	m, err := guestMenuAt(zone, day)
	if err != nil {
		http.Error(w, "Couldn't work it out", http.StatusInternalServerError)
		return
	}
	type sec struct {
		Name       string
		Now, Later int
	}
	var secs []sec
	total := 0
	for _, g := range m.Groups {
		if g.Name == "Recommended" || g.Name == "Popular" {
			continue
		}
		s := sec{Name: g.Name}
		for _, p := range g.Products {
			if p.Later != "" {
				s.Later++
			} else {
				s.Now++
			}
		}
		total += s.Now
		secs = append(secs, s)
	}
	where := zone
	if where == "" {
		where = "any area"
	}
	var buf bytes.Buffer
	hoursTemplates().ExecuteTemplate(&buf, "hours-preview-result", map[string]interface{}{
		"When": day.Format("Mon 2 Jan, 15:04"), "Where": where, "Notices": m.Notices, "Sections": secs, "Total": total,
	})
	w.Write(buf.Bytes())
}

// setupWarnings points out set-ups that don't do what the owner likely
// meant: dishes no menu ever serves in an area, menus running outside
// their station's hours, menus aimed at areas or categories that are gone.
func setupWarnings() []string {
	r := loadMenuRules()
	products, _ := db.GetProducts()
	cats, _ := db.GetCategories()
	catOK := map[string]bool{}
	for _, c := range cats {
		catOK[c.Name] = true
	}
	var out []string
	zones := r.zones
	for _, z := range zones {
		if !r.sched.HasMenusFor(z) {
			continue
		}
		var never []string
		for _, p := range products {
			served := false
			for _, m := range r.sched.Menus {
				if m.Enabled && m.AppliesTo(z) && m.Serves(p) {
					served = true
					break
				}
			}
			if !served {
				never = append(never, p.Name)
			}
		}
		if n := len(never); n > 0 {
			area := z
			if area == "" {
				area = "your tables"
			}
			ex := strings.Join(never[:min(3, n)], ", ")
			if n > 3 {
				ex += "…"
			}
			out = append(out, strconv.Itoa(n)+" dishes are never on a menu for "+area+" (e.g. "+ex+"). Add them to a menu or they can't be ordered there.")
		}
	}
	inWin := func(m, open, close int) bool {
		if close <= open {
			return m >= open || m < close
		}
		return m >= open && m < close
	}
	for _, m := range r.sched.Menus {
		if !m.Enabled {
			continue
		}
		for _, z := range m.Zones {
			if !slices.Contains(zones, z) {
				out = append(out, "“"+m.Name+"” is set for the area “"+z+"”, which no table has anymore.")
			}
		}
		if !m.Everything {
			for _, c := range m.Categories {
				if !catOK[c] {
					out = append(out, "“"+m.Name+"” serves the category “"+c+"”, which doesn't exist anymore.")
				}
			}
		}
		for _, d := range []string{models.DepartmentKitchen, models.DepartmentBar} {
			h := r.sched.Hours[d]
			if !h.Set || h.PerDay {
				continue
			}
			serves := false
			for _, p := range products {
				if r.dept[p.Category] == d && m.Serves(p) {
					serves = true
					break
				}
			}
			if !serves {
				continue
			}
			end := m.End
			if end <= m.Start {
				end += 24 * 60
			}
			for t := m.Start; t < end-m.LastOrders; t += 15 {
				if !inWin(t%(24*60), h.Open, h.Close) {
					out = append(out, "“"+m.Name+"” runs "+models.Clock(m.Start)+"–"+models.Clock(m.End)+", but the "+stationName[d]+" is only open "+models.Clock(h.Open)+"–"+models.Clock(h.Close)+": outside those hours its "+strings.ToLower(stationName[d])+" dishes can't be ordered.")
					break
				}
			}
		}
	}
	return out
}

func mustZones() []string { z, _ := db.Zones(); return z }
