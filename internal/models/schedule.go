package models

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// ServiceMenu is one scheduled menu ("service period"): which days, from/to,
// how many minutes before the end ordering stops (the kitchen's buffer), and
// what it serves: everything, or chosen categories and/or single items. A
// venue that is a beach bar by day, a restaurant at dinner and a bar late is
// just three of these. With no enabled menus at all, everything is orderable
// (the venue hasn't set hours up).
type ServiceMenu struct {
	ID         int
	Name       string
	Days       string // ISO weekdays it starts on, e.g. "12345" = Mon–Fri
	Start, End int    // minutes after midnight; End <= Start runs past midnight
	LastOrders int    // minutes before End that ordering stops
	Everything bool
	Categories []string
	Products   []int
	// Zones are the table areas it applies to (e.g. "Beach"); empty = every
	// area. So the beach can stop at 22:00 while the bar area goes on.
	Zones []string
	// Kind "price" makes this a price rule (happy hour) instead of a menu:
	// while it's on, what it covers costs less (DiscType "percent"/"amount").
	Kind      string
	DiscType  string
	DiscValue float64
	Enabled   bool
	Position  int
}

// AppliesTo reports whether m covers tables in this area.
func (m ServiceMenu) AppliesTo(zone string) bool {
	if len(m.Zones) == 0 {
		return true
	}
	for _, z := range m.Zones {
		if z == zone {
			return true
		}
	}
	return false
}

// Station kitchen/bar can be closed by hand: "" open, "manual" until someone
// reopens it, or an RFC3339 time it reopens by itself.
const StationClosedManual = "manual"

// DayHours are one day's opening hours (Close <= Open runs past midnight).
type DayHours struct {
	Closed      bool
	Open, Close int // minutes after midnight
}

// StationHours are a station's opening hours ("the kitchen closes at
// 21:00"): the everyday tool, set right on the Kitchen/Bar card. The same
// every day, or (PerDay) a week grid. Custom menus are the advanced layer.
type StationHours struct {
	Set                     bool
	Open, Close, LastOrders int // the every-day hours
	PerDay                  bool
	Week                    [7]DayHours // Monday first; used when PerDay
}

// On is the hours for a weekday, and whether it opens at all that day.
func (h StationHours) On(wd time.Weekday) (DayHours, bool) {
	if h.PerDay {
		d := h.Week[(int(wd)+6)%7]
		return d, !d.Closed
	}
	return DayHours{Open: h.Open, Close: h.Close}, true
}

// Label is "12:00–21:00 · last orders 15 min early" (or "Varies by day").
func (h StationHours) Label() string {
	if !h.Set {
		return "No set hours"
	}
	l := Clock(h.Open) + "–" + Clock(h.Close)
	if h.PerDay {
		l = "Varies by day"
	}
	if h.LastOrders > 0 {
		l += " · last orders " + strconv.Itoa(h.LastOrders) + " min early"
	}
	return l
}

func (h StationHours) OpenClock() string  { return Clock(h.Open) }
func (h StationHours) CloseClock() string { return Clock(h.Close) }

// WeekRows is the week grid for the hours editor.
func (h StationHours) WeekRows() []struct {
	ISO             int
	Label           string
	Closed          bool
	OpenAt, CloseAt string
} {
	out := make([]struct {
		ISO             int
		Label           string
		Closed          bool
		OpenAt, CloseAt string
	}, 7)
	for i := range out {
		d := h.Week[i]
		if !h.PerDay { // start the grid from the every-day hours
			d = DayHours{Open: h.Open, Close: h.Close}
		}
		if !h.Set {
			d = DayHours{Open: 12 * 60, Close: 22 * 60}
		}
		out[i].ISO, out[i].Label, out[i].Closed, out[i].OpenAt, out[i].CloseAt = i+1, WeekdayShort[i], d.Closed, Clock(d.Open), Clock(d.Close)
	}
	return out
}

// SpecialDay is an exception for a date or date range ("closed on 15 Aug",
// "winter: kitchen 12:00–21:00 from 1 Oct to 30 Apr", "private event on
// Saturday"). Closed shuts the whole venue; otherwise Hours replaces a
// station's usual hours on those dates.
type SpecialDay struct {
	ID       int
	From, To string // "2006-01-02", inclusive
	Note     string // guests see it when closed ("Private event")
	Closed   bool
	Hours    map[string]DayHours // department -> hours on these dates
}

// Covers reports whether the special day applies to a date.
func (d SpecialDay) Covers(day time.Time) bool {
	k := day.Format("2006-01-02")
	return k >= d.From && k <= d.To
}

// Label is "15 Aug" or "1 Oct – 30 Apr".
func (d SpecialDay) Label() string {
	f, _ := time.Parse("2006-01-02", d.From)
	t, _ := time.Parse("2006-01-02", d.To)
	if d.From == d.To {
		return f.Format("Mon 2 Jan")
	}
	return f.Format("2 Jan") + " – " + t.Format("2 Jan 2006")
}

// Busy is a station taking orders slowly: guests see the wait.
type Busy struct {
	Until   time.Time
	Minutes int
}

// Schedule is everything that decides what's orderable at a moment.
type Schedule struct {
	Menus    []ServiceMenu
	Stations map[string]string       // department -> closed-until ("" = open)
	Hours    map[string]StationHours // department -> opening hours
	Specials []SpecialDay
	Areas    map[string]string // table area -> closed-until ("" = open)
	Busy     map[string]Busy   // department -> busy until
	Prices   []ServiceMenu     // price rules (happy hours), Kind "price"
}

// Discounted is base after this price rule (never below 0, to the cent).
func (m ServiceMenu) Discounted(base float64) float64 {
	v := base
	switch m.DiscType {
	case "percent":
		v = base * (1 - m.DiscValue/100)
	case "amount":
		v = base - m.DiscValue
	}
	if v < 0 {
		v = 0
	}
	return math.Round(v*100) / 100
}

// DealLabel is "20% off" / "€2 off".
func (m ServiceMenu) DealLabel() string {
	if m.DiscType == "percent" {
		return strconv.FormatFloat(m.DiscValue, 'f', -1, 64) + "% off"
	}
	return "€" + strconv.FormatFloat(m.DiscValue, 'f', -1, 64) + " off"
}

// Price is what a dish costs for a table in this area at now: the best
// running price rule wins. ok is false when none applies.
func (s Schedule) Price(p Product, zone string, now time.Time) (price float64, rule ServiceMenu, until time.Time, ok bool) {
	price = p.Price
	for _, m := range s.Prices {
		if !m.Enabled || !m.AppliesTo(zone) || !m.Serves(p) {
			continue
		}
		stop, on := m.cutoff(now)
		if !on {
			continue
		}
		if d := m.Discounted(p.Price); d < price {
			price, rule, until, ok = d, m, stop, true
		}
	}
	return price, rule, until, ok
}

// special is the exception covering a date, if any (the first one listed).
func (s Schedule) special(day time.Time) (SpecialDay, bool) {
	for _, d := range s.Specials {
		if d.Covers(day) {
			return d, true
		}
	}
	return SpecialDay{}, false
}

// window is a station's ordering window for the service that starts on a
// given day: (start, last-order stop, ok). limited is false when nothing
// restricts that day at all (no hours set, no exception).
func (s Schedule) window(dept string, day time.Time) (start, stop time.Time, ok, limited bool) {
	h := s.Hours[dept]
	dh, has := DayHours{}, false
	if sp, found := s.special(day); found {
		if sp.Closed {
			return start, stop, false, true
		}
		if o, set := sp.Hours[dept]; set {
			dh, has = o, !o.Closed
			limited = true
			if !has {
				return start, stop, false, true
			}
		}
	}
	if !limited {
		if !h.Set {
			return start, stop, false, false
		}
		limited = true
		if dh, has = h.On(day.Weekday()); !has {
			return start, stop, false, true
		}
	}
	d0 := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	end := dh.Close
	if end <= dh.Open {
		end += 24 * 60
	}
	start = d0.Add(time.Duration(dh.Open) * time.Minute)
	stop = d0.Add(time.Duration(end-h.LastOrders) * time.Minute)
	return start, stop, stop.After(start), true
}

// WithinHours reports whether a station's hours (and any special day) allow
// ordering at now, and until when (zero = no limit).
func (s Schedule) WithinHours(dept string, now time.Time) (bool, time.Time) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	anyLimit := false
	for _, back := range []int{0, 1} {
		start, stop, ok, limited := s.window(dept, today.AddDate(0, 0, -back))
		if back == 0 && !limited {
			return true, time.Time{} // nothing limits today (yesterday's overnight is irrelevant then)
		}
		anyLimit = anyLimit || limited
		if ok && !now.Before(start) && now.Before(stop) {
			return true, stop
		}
	}
	return !anyLimit, time.Time{}
}

// NextHoursOpen is when a station next opens (within 60 days; zero if none).
func (s Schedule) NextHoursOpen(dept string, now time.Time) time.Time {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for d := 0; d <= 60; d++ {
		if start, _, ok, _ := s.window(dept, today.AddDate(0, 0, d)); ok && start.After(now) {
			return start
		}
	}
	return time.Time{}
}

// ClosedToday is the venue-wide closure covering today, if any.
func (s Schedule) ClosedToday(now time.Time) (SpecialDay, bool) {
	sp, ok := s.special(now)
	return sp, ok && sp.Closed
}

// closedUntil reads a closed-until value: "" open, "manual", or RFC3339.
func closedUntil(v string, now time.Time) (bool, time.Time) {
	if v == "" {
		return false, time.Time{}
	}
	if v == StationClosedManual {
		return true, time.Time{}
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil || !now.Before(t) {
		return false, time.Time{}
	}
	return true, t
}

// AreaClosed reports whether ordering is paused for a table area.
func (s Schedule) AreaClosed(zone string, now time.Time) (bool, time.Time) {
	return closedUntil(s.Areas[zone], now)
}

// BusyNow is a station's current extra wait in minutes (0 = not busy).
func (s Schedule) BusyNow(dept string, now time.Time) int {
	if b, ok := s.Busy[dept]; ok && now.Before(b.Until) {
		return b.Minutes
	}
	return 0
}

// ItemState is one dish right now: can it be ordered, and if ordering stops
// soon (LastCall) when (Until).
type ItemState struct {
	Orderable bool
	LastCall  bool
	Until     time.Time // last-order time of the latest menu serving it
}

// LastCallWindow is how long before last orders guests get warned.
const LastCallWindow = 20 * time.Minute

func (m ServiceMenu) runsOn(d time.Weekday) bool {
	iso := int(d)
	if iso == 0 {
		iso = 7
	}
	return strings.ContainsRune(m.Days, rune('0'+iso))
}

// cutoff returns the last-order time of the occurrence of m that is serving
// at now (started today, or yesterday and still running past midnight).
func (m ServiceMenu) cutoff(now time.Time) (time.Time, bool) {
	day0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, back := range []int{0, 1} {
		day := day0.AddDate(0, 0, -back)
		if !m.runsOn(day.Weekday()) {
			continue
		}
		start := day.Add(time.Duration(m.Start) * time.Minute)
		endMin := m.End
		if endMin <= m.Start {
			endMin += 24 * 60
		}
		stop := day.Add(time.Duration(endMin-m.LastOrders) * time.Minute)
		if !now.Before(start) && now.Before(stop) {
			return stop, true
		}
	}
	return time.Time{}, false
}

// ServingNow reports whether m is taking orders at now, and until when.
func (m ServiceMenu) ServingNow(now time.Time) (time.Time, bool) { return m.cutoff(now) }

// Serves reports whether m includes this dish (by its category or itself).
func (m ServiceMenu) Serves(p Product) bool {
	if m.Everything {
		return true
	}
	for _, c := range m.Categories {
		if c == p.Category {
			return true
		}
	}
	for _, id := range m.Products {
		if id == p.ID {
			return true
		}
	}
	return false
}

// StationClosed reports whether a department is closed by hand right now.
func (s Schedule) StationClosed(dept string, now time.Time) (closed bool, until time.Time) {
	return closedUntil(s.Stations[dept], now)
}

// HasMenus is true once at least one menu is switched on.
func (s Schedule) HasMenus() bool {
	for _, m := range s.Menus {
		if m.Enabled {
			return true
		}
	}
	return false
}

// HasMenusFor is true when at least one switched-on menu covers this area:
// only then do menus decide what its tables can order (an area no menu
// mentions keeps ordering everything).
func (s Schedule) HasMenusFor(zone string) bool {
	for _, m := range s.Menus {
		if m.Enabled && m.AppliesTo(zone) {
			return true
		}
	}
	return false
}

// Item decides one dish (dept = its category's kitchen/bar) for a table in
// this area at now.
func (s Schedule) Item(p Product, dept, zone string, now time.Time) ItemState {
	if closed, _ := s.StationClosed(dept, now); closed {
		return ItemState{}
	}
	if closed, _ := s.AreaClosed(zone, now); closed {
		return ItemState{}
	}
	in, hoursStop := s.WithinHours(dept, now)
	if !in {
		return ItemState{}
	}
	if p.HasOwnHours() { // the dish's own hours ("orange juice until 12:00")
		own, ok := ServiceMenu{Days: "1234567", Start: p.AvailStart, End: p.AvailEnd, Everything: true, Enabled: true}.cutoff(now)
		if !ok {
			return ItemState{}
		}
		if hoursStop.IsZero() || own.Before(hoursStop) {
			hoursStop = own
		}
	}
	if !s.HasMenusFor(zone) {
		st := ItemState{Orderable: true, Until: hoursStop}
		st.LastCall = !hoursStop.IsZero() && hoursStop.Sub(now) <= LastCallWindow
		return st
	}
	var st ItemState
	for _, m := range s.Menus {
		if !m.Enabled || !m.AppliesTo(zone) || !m.Serves(p) {
			continue
		}
		if stop, ok := m.cutoff(now); ok {
			st.Orderable = true
			if stop.After(st.Until) {
				st.Until = stop
			}
		}
	}
	if st.Orderable && !hoursStop.IsZero() && hoursStop.Before(st.Until) {
		st.Until = hoursStop // the station's hours end before the menu does
	}
	st.LastCall = st.Orderable && st.Until.Sub(now) <= LastCallWindow
	return st
}

// NextStart is the soonest time (within a week) any enabled menu that
// serves something matching want starts; zero if none.
func (s Schedule) NextStart(now time.Time, want func(ServiceMenu) bool) time.Time {
	var best time.Time
	day0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, m := range s.Menus {
		if !m.Enabled || (want != nil && !want(m)) {
			continue
		}
		for d := 0; d <= 7; d++ {
			day := day0.AddDate(0, 0, d)
			start := day.Add(time.Duration(m.Start) * time.Minute)
			if m.runsOn(day.Weekday()) && start.After(now) {
				if best.IsZero() || start.Before(best) {
					best = start
				}
				break
			}
		}
	}
	return best
}

// ---- small helpers for forms and display ----

// ClockMinutes parses "HH:MM" into minutes after midnight.
func ClockMinutes(s string) (int, bool) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// Clock renders minutes after midnight as "HH:MM".
func Clock(min int) string {
	min = ((min % 1440) + 1440) % 1440
	return strconvPad(min/60) + ":" + strconvPad(min%60)
}

func strconvPad(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// WeekdayShort are ISO weekdays 1..7 as short labels (index 0 = Monday).
var WeekdayShort = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

// DaysLabel is a compact "Every day" / "Mon–Fri" / "Sat, Sun" for a Days string.
func DaysLabel(days string) string {
	switch days {
	case "1234567":
		return "Every day"
	case "12345":
		return "Mon–Fri"
	case "67":
		return "Weekends"
	case "":
		return "No days"
	}
	var out []string
	for _, r := range days {
		if i := int(r - '1'); i >= 0 && i < 7 {
			out = append(out, WeekdayShort[i])
		}
	}
	return strings.Join(out, ", ")
}

// ZonesLabel is "All areas" or "Beach, Pergola".
func (m ServiceMenu) ZonesLabel() string {
	if len(m.Zones) == 0 {
		return "All areas"
	}
	return strings.Join(m.Zones, ", ")
}

// InZone is for the editor's area checkboxes.
func (m ServiceMenu) InZone(z string) bool {
	for _, x := range m.Zones {
		if x == z {
			return true
		}
	}
	return false
}

// Label is the menu's hours for display, e.g. "Every day · 21:00–03:00".
func (m ServiceMenu) Label() string {
	return DaysLabel(m.Days) + " · " + Clock(m.Start) + "–" + Clock(m.End)
}

// RunsOnDay is for the editor's day checkboxes (1 = Monday).
func (m ServiceMenu) RunsOnDay(iso int) bool { return strings.ContainsRune(m.Days, rune('0'+iso)) } // #nosec G115 -- iso weekday is 1..7

// ServesCategory / ServesProduct are for the editor's checkboxes.
func (m ServiceMenu) ServesCategory(name string) bool {
	for _, c := range m.Categories {
		if c == name {
			return true
		}
	}
	return false
}

func (m ServiceMenu) ServesProduct(id int) bool {
	for _, p := range m.Products {
		if p == id {
			return true
		}
	}
	return false
}
