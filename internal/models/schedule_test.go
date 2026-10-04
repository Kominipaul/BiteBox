package models

import (
	"testing"
	"time"
)

// A beach bar by day, a restaurant at dinner, crêpes + bar late: the rules a
// manager sets up in Menus & hours, checked at the moments that matter.
func TestScheduleItem(t *testing.T) {
	loc := time.UTC
	at := func(day, hh, mm int) time.Time { return time.Date(2026, 9, day, hh, mm, 0, 0, loc) } // 2026-09-28 is a Monday
	oct := func(day, hh, mm int) time.Time { return time.Date(2026, 10, day, hh, mm, 0, 0, loc) }
	omelette := Product{ID: 1, Category: "Brunch"}
	pasta := Product{ID: 2, Category: "Pasta"}
	crepe := Product{ID: 3, Category: "Desserts"}
	beer := Product{ID: 4, Category: "Beers"}
	s := Schedule{Menus: []ServiceMenu{
		{Name: "Day", Days: "1234567", Start: 10 * 60, End: 18 * 60, LastOrders: 15, Categories: []string{"Brunch", "Beers"}, Enabled: true},
		{Name: "Dinner", Days: "1234567", Start: 18 * 60, End: 21 * 60, LastOrders: 15, Categories: []string{"Pasta", "Beers"}, Enabled: true},
		{Name: "Late", Days: "56", Start: 21 * 60, End: 3 * 60, LastOrders: 0, Categories: []string{"Beers"}, Products: []int{3}, Enabled: true},
		{Name: "Off", Days: "1234567", Start: 0, End: 23*60 + 59, Everything: true, Enabled: false},
	}}
	cases := []struct {
		name     string
		p        Product
		now      time.Time
		want     bool
		lastCall bool
	}{
		{"brunch mid-morning", omelette, at(28, 11, 0), true, false},
		{"brunch before opening", omelette, at(28, 9, 59), false, false},
		{"brunch in the buffer", omelette, at(28, 17, 50), false, false},
		{"brunch last call", omelette, at(28, 17, 30), true, true},
		{"pasta at dinner", pasta, at(28, 19, 0), true, false},
		{"pasta after dinner", pasta, at(28, 21, 30), false, false},
		{"crepe late on Friday", crepe, oct(2, 23, 0), true, false}, // Oct 2 = Friday
		{"crepe past midnight into Saturday", crepe, oct(3, 2, 30), true, false},
		{"crepe late on Monday (no late menu)", crepe, at(28, 23, 0), false, false},
		{"beer in the day menu's buffer, before dinner starts", beer, at(28, 17, 50), false, false},
		{"beer at dinner", beer, at(28, 20, 0), true, false},
	}
	for _, c := range cases {
		st := s.Item(c.p, DepartmentKitchen, "", c.now)
		if st.Orderable != c.want || st.LastCall != c.lastCall {
			t.Errorf("%s: got orderable=%v lastCall=%v, want %v/%v", c.name, st.Orderable, st.LastCall, c.want, c.lastCall)
		}
	}

	// A station closed by hand beats every menu; a timed close lifts itself.
	s.Stations = map[string]string{DepartmentKitchen: StationClosedManual}
	if s.Item(pasta, DepartmentKitchen, "", at(28, 19, 0)).Orderable {
		t.Error("closed kitchen still serves pasta")
	}
	s.Stations[DepartmentKitchen] = at(28, 19, 30).Format(time.RFC3339)
	if s.Item(pasta, DepartmentKitchen, "", at(28, 19, 0)).Orderable || !s.Item(pasta, DepartmentKitchen, "", at(28, 19, 45)).Orderable {
		t.Error("timed close should hold until 19:30, then lift")
	}

	// No menus switched on: everything goes, as before any setup.
	if !(Schedule{}).Item(pasta, DepartmentKitchen, "", at(28, 4, 0)).Orderable {
		t.Error("no menus should mean everything is orderable")
	}

	// Next start for the kitchen after dinner on Monday: Day menu, Tuesday 10:00.
	next := s.NextStart(at(28, 22, 0), func(m ServiceMenu) bool { return m.Serves(omelette) })
	if !next.Equal(at(29, 10, 0)) {
		t.Errorf("next brunch = %v, want Tue 10:00", next)
	}

	// Areas: the beach stops at 22:00, the bar area goes on till 02:00, and
	// an area no menu mentions orders everything.
	z := Schedule{Menus: []ServiceMenu{
		{Name: "Beach", Days: "1234567", Start: 10 * 60, End: 22 * 60, Everything: true, Zones: []string{"Beach"}, Enabled: true},
		{Name: "Bar area", Days: "1234567", Start: 10 * 60, End: 2 * 60, Everything: true, Zones: []string{"Bar", "Pergola"}, Enabled: true},
	}}
	if !z.Item(beer, DepartmentBar, "Beach", at(28, 21, 0)).Orderable || z.Item(beer, DepartmentBar, "Beach", at(28, 22, 30)).Orderable {
		t.Error("beach should order until 22:00, then stop")
	}
	if !z.Item(beer, DepartmentBar, "Pergola", at(28, 23, 30)).Orderable {
		t.Error("pergola should still order at 23:30")
	}
	if !z.Item(beer, DepartmentBar, "Garden", at(28, 4, 0)).Orderable {
		t.Error("an area no menu mentions keeps ordering everything")
	}

	// Simple hours: the kitchen closes at 21:00 (last orders 15 min early),
	// the bar runs 10:00–02:00. No menus needed.
	h := Schedule{Hours: map[string]StationHours{
		DepartmentKitchen: {Set: true, Open: 12 * 60, Close: 21 * 60, LastOrders: 15},
		DepartmentBar:     {Set: true, Open: 10 * 60, Close: 2 * 60},
	}}
	if !h.Item(pasta, DepartmentKitchen, "", at(28, 20, 30)).Orderable || h.Item(pasta, DepartmentKitchen, "", at(28, 20, 50)).Orderable {
		t.Error("kitchen: orderable at 20:30, not after last orders at 20:45")
	}
	if st := h.Item(pasta, DepartmentKitchen, "", at(28, 20, 30)); !st.LastCall {
		t.Error("20:30 should be last call for a 20:45 cutoff")
	}
	if !h.Item(beer, DepartmentBar, "", at(29, 1, 30)).Orderable || h.Item(beer, DepartmentBar, "", at(29, 2, 30)).Orderable {
		t.Error("bar: open past midnight until 02:00")
	}
	if next := h.NextHoursOpen(DepartmentKitchen, at(28, 22, 0)); !next.Equal(at(29, 12, 0)) {
		t.Errorf("kitchen reopens %v, want next day 12:00", next)
	}
}

func TestHoursDaysSpecialsAreas(t *testing.T) {
	loc := time.UTC
	at := func(m time.Month, day, hh, mm int) time.Time { return time.Date(2026, m, day, hh, mm, 0, 0, loc) }
	pasta := Product{ID: 2, Category: "Pasta"}
	beer := Product{ID: 4, Category: "Beers"}
	kitchen := StationHours{Set: true, PerDay: true, LastOrders: 15}
	for i := range kitchen.Week {
		kitchen.Week[i] = DayHours{Open: 12 * 60, Close: 21 * 60}
	}
	kitchen.Week[4] = DayHours{Open: 12 * 60, Close: 23 * 60} // Friday later
	kitchen.Week[5] = DayHours{Open: 12 * 60, Close: 23 * 60} // Saturday later
	kitchen.Week[0] = DayHours{Closed: true}                  // closed Mondays
	s := Schedule{Hours: map[string]StationHours{DepartmentKitchen: kitchen}}
	k := func(now time.Time) bool { return s.Item(pasta, DepartmentKitchen, "", now).Orderable }

	// 2026-10-02 is a Friday, 10-05 a Monday, 10-06 a Tuesday.
	if !k(at(10, 2, 22, 0)) || k(at(10, 6, 22, 0)) {
		t.Error("per-day: Friday open at 22:00, Tuesday not")
	}
	if k(at(10, 5, 13, 0)) {
		t.Error("closed on Mondays")
	}
	if next := s.NextHoursOpen(DepartmentKitchen, at(10, 4, 22, 0)); !next.Equal(at(10, 6, 12, 0)) { // Sun night -> Tue noon
		t.Errorf("next opening after Sunday = %v, want Tuesday 12:00", next)
	}

	// Special days: a holiday closes everything (even the bar with no hours);
	// winter hours replace the kitchen's for a date range.
	s.Specials = []SpecialDay{
		{From: "2026-08-15", To: "2026-08-15", Note: "Public holiday", Closed: true},
		{From: "2026-11-01", To: "2027-03-31", Hours: map[string]DayHours{DepartmentKitchen: {Open: 13 * 60, Close: 20 * 60}}},
	}
	if s.Item(beer, DepartmentBar, "", at(8, 15, 14, 0)).Orderable {
		t.Error("closed day: the bar must be closed too")
	}
	if sp, closed := s.ClosedToday(at(8, 15, 9, 0)); !closed || sp.Note != "Public holiday" {
		t.Error("ClosedToday should report the holiday")
	}
	if k(at(11, 10, 12, 30)) || !k(at(11, 10, 13, 30)) || k(at(11, 10, 19, 50)) {
		t.Error("winter: kitchen 13:00–20:00 with 15 min last orders")
	}

	// The dish's own hours, and a paused area.
	oj := Product{ID: 9, Category: "Juices", AvailStart: 7 * 60, AvailEnd: 12 * 60}
	var none Schedule
	if !none.Item(oj, DepartmentBar, "", at(10, 6, 9, 0)).Orderable || none.Item(oj, DepartmentBar, "", at(10, 6, 13, 0)).Orderable {
		t.Error("orange juice only 07:00–12:00")
	}
	none.Areas = map[string]string{"Beach": StationClosedManual}
	if none.Item(beer, DepartmentBar, "Beach", at(10, 6, 15, 0)).Orderable || !none.Item(beer, DepartmentBar, "Pergola", at(10, 6, 15, 0)).Orderable {
		t.Error("paused beach blocks only the beach")
	}
	none.Busy = map[string]Busy{DepartmentKitchen: {Until: at(10, 6, 21, 0), Minutes: 35}}
	if none.BusyNow(DepartmentKitchen, at(10, 6, 20, 0)) != 35 || none.BusyNow(DepartmentKitchen, at(10, 6, 21, 30)) != 0 {
		t.Error("busy mode lasts until it runs out")
	}
}

func TestHappyHourPrice(t *testing.T) {
	at := func(hh int) time.Time { return time.Date(2026, 10, 2, hh, 0, 0, 0, time.UTC) } // Friday
	mojito := Product{ID: 7, Category: "Cocktails", Price: 10}
	s := Schedule{Prices: []ServiceMenu{
		{Name: "Happy hour", Kind: "price", Days: "12345", Start: 18 * 60, End: 20 * 60, Categories: []string{"Cocktails"}, DiscType: "percent", DiscValue: 20, Enabled: true},
		{Name: "Beach deal", Kind: "price", Days: "1234567", Start: 18 * 60, End: 19 * 60, Products: []int{7}, Zones: []string{"Beach"}, DiscType: "amount", DiscValue: 3, Enabled: true},
	}}
	if p, _, _, ok := s.Price(mojito, "Pergola", at(17)); ok || p != 10 {
		t.Errorf("before happy hour: %v %v", p, ok)
	}
	if p, r, _, ok := s.Price(mojito, "Pergola", at(18)); !ok || p != 8 || r.Name != "Happy hour" {
		t.Errorf("happy hour: %v %v %v", p, r.Name, ok)
	}
	if p, r, _, _ := s.Price(mojito, "Beach", at(18)); p != 7 || r.Name != "Beach deal" {
		t.Errorf("best deal wins at the beach: %v %v", p, r.Name)
	}
	if (ServiceMenu{DiscType: "amount", DiscValue: 50}).Discounted(10) != 0 {
		t.Error("never below zero")
	}
}
