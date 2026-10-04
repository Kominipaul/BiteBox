package handlers

import (
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"bitebox/internal/db"
	"bitebox/internal/models"
)

// menuRules is the schedule (Menus & hours) plus each category's kitchen/bar,
// loaded once per request: the one place that decides whether a dish can be
// ordered right now. The guest menu, adding to the cart and checkout all ask
// it, so a closed kitchen or a menu that has ended is enforced on the
// server, not just hidden.
type menuRules struct {
	sched models.Schedule
	dept  map[string]string // category -> department
	zones []string          // the venue's table areas ("" when it has none)
	now   time.Time
}

func loadMenuRules() menuRules { return loadMenuRulesAt(time.Now()) }

// loadMenuRulesAt is the rules as they'd apply at t (the Preview tool).
func loadMenuRulesAt(t time.Time) menuRules {
	r := menuRules{dept: map[string]string{}, now: t}
	r.sched, _ = db.GetSchedule()
	if cats, err := db.GetCategories(); err == nil {
		for _, c := range cats {
			r.dept[c.Name] = c.Department
		}
	}
	if r.zones, _ = db.Zones(); len(r.zones) == 0 {
		r.zones = []string{""}
	}
	return r
}

// nextOrderable is when (within 12 hours) a dish not orderable now becomes
// orderable in this area; zero if not soon (or closed by hand).
func (r menuRules) nextOrderable(p models.Product, zone string) time.Time {
	start := r.now.Truncate(15 * time.Minute)
	for step := 1; step <= 48; step++ {
		at := r
		at.now = start.Add(time.Duration(step) * 15 * time.Minute)
		if at.item(p, zone).Orderable {
			return at.now
		}
	}
	return time.Time{}
}

// item decides a dish for a table in this area (menus can differ per area).
func (r menuRules) item(p models.Product, zone string) models.ItemState {
	return r.sched.Item(p, r.dept[p.Category], zone, r.now)
}

// price is what a dish costs for a table in this area right now (a running
// happy hour lowers it), with the deal's guest-facing label.
func (r menuRules) price(p models.Product, zone string) (float64, string) {
	price, rule, until, ok := r.sched.Price(p, zone, r.now)
	if !ok {
		return p.Price, ""
	}
	return price, rule.Name + " · " + rule.DealLabel() + " until " + until.Format("15:04")
}

// anyZone: can this dish be ordered right now in at least one area (some),
// and in every area (all)? For staff views that aren't about one table.
func (r menuRules) anyZone(p models.Product) (some, all bool) {
	all = true
	for _, z := range r.zones {
		if r.item(p, z).Orderable {
			some = true
		} else {
			all = false
		}
	}
	return some, all
}

// Guest-facing names for the two stations.
var stationLabel = map[string]string{models.DepartmentKitchen: "Food", models.DepartmentBar: "Drinks"}

// guestNotice is one line at the top of the guest menu.
type guestNotice struct {
	Text string
	Kind string // "closed" or "last"
}

// whenLabel is "19:00" today, "Tue 10:00" otherwise.
func whenLabel(t, now time.Time) string {
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return t.Format("15:04")
	}
	return t.Format("Mon 15:04")
}

// guestNotices explains, per station, what guests can't order right now and
// when that changes: closed by hand, between menus, or last orders soon.
func (r menuRules) guestNotices(all []models.Product, zone string) []guestNotice {
	type agg struct {
		has, orderable bool
		last           time.Time
	}
	by := map[string]*agg{}
	any := false
	for _, p := range all {
		d := r.dept[p.Category]
		if by[d] == nil {
			by[d] = &agg{}
		}
		a := by[d]
		a.has = true
		st := r.item(p, zone)
		if st.Orderable {
			a.orderable, any = true, true
			if st.LastCall && (a.last.IsZero() || st.Until.Before(a.last)) {
				a.last = st.Until
			}
		}
	}
	nextFor := func(dept string) time.Time {
		return r.sched.NextStart(r.now, func(m models.ServiceMenu) bool {
			if !m.AppliesTo(zone) {
				return false
			}
			for _, p := range all {
				if r.dept[p.Category] == dept && m.Serves(p) {
					return true
				}
			}
			return false
		})
	}
	// Venue-wide and area-wide pauses explain themselves first.
	if sp, closed := r.sched.ClosedToday(r.now); closed && !any {
		t := "We're closed today"
		if sp.Note != "" {
			t += ": " + sp.Note
		}
		return []guestNotice{{t + ".", "closed"}}
	}
	if closed, until := r.sched.AreaClosed(zone, r.now); closed && zone != "" {
		t := "Ordering is paused at your table's area (" + zone + ")"
		if !until.IsZero() {
			t += " until " + whenLabel(until, r.now)
		}
		return []guestNotice{{t + ". A waiter can still help you.", "closed"}}
	}
	if !any && len(all) > 0 {
		t := "We're not taking orders right now."
		if next := r.sched.NextStart(r.now, func(m models.ServiceMenu) bool { return m.AppliesTo(zone) }); !next.IsZero() {
			t += " Back at " + whenLabel(next, r.now) + "."
		}
		return []guestNotice{{t, "closed"}}
	}
	var out []guestNotice
	for _, d := range []string{models.DepartmentKitchen, models.DepartmentBar} {
		a := by[d]
		if a == nil || !a.has {
			continue
		}
		name := stationLabel[d]
		switch {
		case !a.orderable:
			t := name + " not available right now"
			if closed, until := r.sched.StationClosed(d, r.now); closed && !until.IsZero() {
				t += ". Back at " + whenLabel(until, r.now)
			} else if in, _ := r.sched.WithinHours(d, r.now); !closed && !in {
				if next := r.sched.NextHoursOpen(d, r.now); !next.IsZero() {
					t += ". Back at " + whenLabel(next, r.now)
				}
			} else if !closed {
				if next := nextFor(d); !next.IsZero() {
					t += ". Back at " + whenLabel(next, r.now)
				}
			}
			out = append(out, guestNotice{t + ".", "closed"})
		case !a.last.IsZero():
			out = append(out, guestNotice{name + ": last orders at " + a.last.Format("15:04") + ".", "last"})
		}
		if a.orderable {
			if m := r.sched.BusyNow(d, r.now); m > 0 {
				out = append(out, guestNotice{"It's busy: expect about " + strconv.Itoa(m) + " min extra for " + strings.ToLower(name) + ".", "last"})
			}
		}
	}
	return out
}

// menuSignature is what guests currently see (orderable dishes, last-call
// flags, notices), so the watcher only pushes a new menu when it changed.
func menuSignature() string {
	r := loadMenuRules()
	products, err := db.GetProducts()
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, z := range r.zones {
		b.WriteString("|" + z + ":")
		for _, p := range products {
			st := r.item(p, z)
			if st.Orderable {
				b.WriteString(strconv.Itoa(p.ID) + ",")
				if st.LastCall {
					b.WriteByte('!')
				}
			}
		}
		for _, n := range r.guestNotices(products, z) {
			b.WriteString(n.Text)
		}
	}
	return b.String()
}

// StartMenuScheduleWatcher keeps every open guest menu in step with the
// clock: a menu starting or ending, last orders coming up, a timed kitchen/
// bar close running out. Checks every 30s; pushes only on a change.
func StartMenuScheduleWatcher() {
	go func() {
		last := menuSignature()
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			checkLastOrderAlerts()
			if sig := menuSignature(); sig != last {
				last = sig
				log.Printf("🕒 Menu schedule changed what guests can order; updating menus")
				BroadcastAllMenus()
			}
		}
	}()
}

// Last-orders alerts: about LastOrderAlertLead before a station stops taking
// orders in an area, seated guests there (with order alerts on) and the
// waiters get a push, once per cut-off.
const LastOrderAlertLead = 15 * time.Minute

var lastOrderAlerted = struct {
	sync.Mutex
	m map[string]time.Time
}{m: map[string]time.Time{}}

func checkLastOrderAlerts() {
	r := loadMenuRules()
	products, err := db.GetProducts()
	if err != nil {
		return
	}
	tables, _ := db.GetAllTables()
	lastOrderAlerted.Lock()
	defer lastOrderAlerted.Unlock()
	for k, at := range lastOrderAlerted.m { // forget old cut-offs
		if r.now.Sub(at) > 24*time.Hour {
			delete(lastOrderAlerted.m, k)
		}
	}
	for _, d := range []string{models.DepartmentKitchen, models.DepartmentBar} {
		for _, z := range r.zones {
			// When does this station stop serving this area altogether?
			var end time.Time
			open := false
			for _, p := range products {
				if r.dept[p.Category] != d {
					continue
				}
				if st := r.item(p, z); st.Orderable {
					open = true
					if st.Until.IsZero() {
						end = time.Time{}
						break // something runs on with no end: no last call
					}
					if st.Until.After(end) {
						end = st.Until
					}
				}
			}
			if !open || end.IsZero() || end.Sub(r.now) > LastOrderAlertLead || !end.After(r.now) {
				continue
			}
			key := d + "|" + z + "|" + end.Format(time.RFC3339)
			if _, done := lastOrderAlerted.m[key]; done {
				continue
			}
			lastOrderAlerted.m[key] = r.now
			what := strings.ToLower(stationLabel[d])
			for _, t := range tables {
				if t.Zone == z && t.HostSessionID != "" {
					notifyGuestSession(t.HostSessionID, pushPayload{
						Title: "Last orders for " + what + " at " + end.Format("15:04"),
						Body:  "Anything else? Order in the next few minutes.",
						URL:   "/table/" + strconv.Itoa(t.Number),
						Tag:   "last-orders-" + d,
					})
				}
			}
			staffKey := d + "|staff|" + end.Format(time.RFC3339)
			if _, done := lastOrderAlerted.m[staffKey]; !done {
				lastOrderAlerted.m[staffKey] = r.now
				notifyDepartments([]string{models.DepartmentWaiter}, pushPayload{
					Title: stationName[d] + ": last orders at " + end.Format("15:04"),
					Body:  "Guests have been told. Check your tables.",
					URL:   "/worker",
					Tag:   "last-orders-staff-" + d,
				})
			}
			log.Printf("🔔 Last-orders alert: %s in %q at %s", stationName[d], z, end.Format("15:04"))
		}
	}
}
