package db

import (
	"encoding/json"
	"time"

	"bitebox/internal/models"
)

// Scheduled menus (Menus & hours) and the kitchen/bar close switches — see
// models.Schedule for the rules. categories/products are small JSON lists
// (category names, product ids) kept on the menu row itself.

const serviceMenuColumns = "id, name, days, start_min, end_min, last_orders, everything, categories, products, enabled, position, zones, kind, disc_type, disc_value"

func scanServiceMenu(scan func(...interface{}) error) (models.ServiceMenu, error) {
	var m models.ServiceMenu
	var cats, prods, zones string
	err := scan(&m.ID, &m.Name, &m.Days, &m.Start, &m.End, &m.LastOrders, &m.Everything, &cats, &prods, &m.Enabled, &m.Position, &zones, &m.Kind, &m.DiscType, &m.DiscValue)
	json.Unmarshal([]byte(zones), &m.Zones)
	json.Unmarshal([]byte(cats), &m.Categories)
	json.Unmarshal([]byte(prods), &m.Products)
	return m, err
}

// ListServiceMenus returns every menu, in the order they're shown.
func ListServiceMenus() ([]models.ServiceMenu, error) {
	rows, err := DB.Query("SELECT " + serviceMenuColumns + " FROM service_menus ORDER BY position, start_min, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ServiceMenu
	for rows.Next() {
		m, err := scanServiceMenu(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func GetServiceMenu(id int) (models.ServiceMenu, error) {
	return scanServiceMenu(DB.QueryRow("SELECT "+serviceMenuColumns+" FROM service_menus WHERE id = ?", id).Scan)
}

// SaveServiceMenu inserts (ID 0) or updates a menu; returns its id.
func SaveServiceMenu(m models.ServiceMenu) (int, error) {
	if m.Categories == nil {
		m.Categories = []string{}
	}
	if m.Products == nil {
		m.Products = []int{}
	}
	if m.Zones == nil {
		m.Zones = []string{}
	}
	cats, _ := json.Marshal(m.Categories)
	prods, _ := json.Marshal(m.Products)
	zones, _ := json.Marshal(m.Zones)
	if m.ID == 0 {
		if m.Kind == "" {
			m.Kind = "menu"
		}
		res, err := DB.Exec(`INSERT INTO service_menus (name, days, start_min, end_min, last_orders, everything, categories, products, enabled, zones, kind, disc_type, disc_value, position)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM service_menus))`,
			m.Name, m.Days, m.Start, m.End, m.LastOrders, m.Everything, string(cats), string(prods), m.Enabled, string(zones), m.Kind, m.DiscType, m.DiscValue)
		if err != nil {
			return 0, err
		}
		id, err := res.LastInsertId()
		return int(id), err
	}
	_, err := DB.Exec(`UPDATE service_menus SET name = ?, days = ?, start_min = ?, end_min = ?, last_orders = ?, everything = ?,
		categories = ?, products = ?, enabled = ?, zones = ?, disc_type = ?, disc_value = ? WHERE id = ?`,
		m.Name, m.Days, m.Start, m.End, m.LastOrders, m.Everything, string(cats), string(prods), m.Enabled, string(zones), m.DiscType, m.DiscValue, m.ID)
	return m.ID, err
}

func SetServiceMenuEnabled(id int, on bool) error {
	_, err := DB.Exec("UPDATE service_menus SET enabled = ? WHERE id = ?", on, id)
	return err
}

func DeleteServiceMenu(id int) error {
	_, err := DB.Exec("DELETE FROM service_menus WHERE id = ?", id)
	return err
}

// SetStationClosed closes a department ("manual", or an RFC3339 reopen
// time) or opens it ("").
func SetStationClosed(dept, until string) error {
	_, err := DB.Exec("INSERT INTO stations (dept, closed_until) VALUES (?, ?) ON CONFLICT(dept) DO UPDATE SET closed_until = excluded.closed_until", dept, until)
	return err
}

// SetStationHours sets (or clears, Set false) a station's daily hours.
func SetStationHours(dept string, h models.StationHours) error {
	week, _ := json.Marshal(h.Week)
	_, err := DB.Exec(`INSERT INTO stations (dept, hours_set, open_min, close_min, hours_last, per_day, week) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(dept) DO UPDATE SET hours_set = excluded.hours_set, open_min = excluded.open_min,
		close_min = excluded.close_min, hours_last = excluded.hours_last, per_day = excluded.per_day, week = excluded.week`,
		dept, h.Set, h.Open, h.Close, h.LastOrders, h.PerDay, string(week))
	return err
}

// SetStationBusy marks a station busy (guests see the wait) until a time,
// or clears it (minutes 0).
func SetStationBusy(dept string, minutes int, until time.Time) error {
	u := ""
	if minutes > 0 {
		u = until.Format(time.RFC3339)
	}
	_, err := DB.Exec(`INSERT INTO stations (dept, busy_until, busy_minutes) VALUES (?, ?, ?)
		ON CONFLICT(dept) DO UPDATE SET busy_until = excluded.busy_until, busy_minutes = excluded.busy_minutes`, dept, u, minutes)
	return err
}

// SetAreaClosed pauses ordering for a table area ("manual" or an RFC3339
// reopen time) or resumes it ("").
func SetAreaClosed(zone, until string) error {
	_, err := DB.Exec("INSERT INTO area_closures (zone, closed_until) VALUES (?, ?) ON CONFLICT(zone) DO UPDATE SET closed_until = excluded.closed_until", zone, until)
	return err
}

// ListSpecialDays returns the exceptions from yesterday on, soonest first.
func ListSpecialDays() ([]models.SpecialDay, error) {
	rows, err := DB.Query("SELECT id, date_from, date_to, note, closed, hours FROM special_days WHERE date_to >= ? ORDER BY date_from, id",
		time.Now().AddDate(0, 0, -1).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.SpecialDay
	for rows.Next() {
		var d models.SpecialDay
		var hours string
		if err := rows.Scan(&d.ID, &d.From, &d.To, &d.Note, &d.Closed, &hours); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(hours), &d.Hours)
		out = append(out, d)
	}
	return out, rows.Err()
}

// SaveSpecialDay adds (ID 0) or updates an exception.
func SaveSpecialDay(d models.SpecialDay) error {
	if d.Hours == nil {
		d.Hours = map[string]models.DayHours{}
	}
	hours, _ := json.Marshal(d.Hours)
	if d.ID == 0 {
		_, err := DB.Exec("INSERT INTO special_days (date_from, date_to, note, closed, hours) VALUES (?, ?, ?, ?, ?)", d.From, d.To, d.Note, d.Closed, string(hours))
		return err
	}
	_, err := DB.Exec("UPDATE special_days SET date_from = ?, date_to = ?, note = ?, closed = ?, hours = ? WHERE id = ?", d.From, d.To, d.Note, d.Closed, string(hours), d.ID)
	return err
}

func GetSpecialDay(id int) (models.SpecialDay, error) {
	var d models.SpecialDay
	var hours string
	err := DB.QueryRow("SELECT id, date_from, date_to, note, closed, hours FROM special_days WHERE id = ?", id).Scan(&d.ID, &d.From, &d.To, &d.Note, &d.Closed, &hours)
	json.Unmarshal([]byte(hours), &d.Hours)
	return d, err
}

func DeleteSpecialDay(id int) error {
	_, err := DB.Exec("DELETE FROM special_days WHERE id = ?", id)
	return err
}

// SetProductHours limits a dish to its own daily hours (-1, -1 = none).
func SetProductHours(id, start, end int) error {
	_, err := DB.Exec("UPDATE products SET avail_start = ?, avail_end = ? WHERE id = ?", start, end, id)
	return err
}

// GetSchedule loads everything models.Schedule needs.
func GetSchedule() (models.Schedule, error) {
	s := models.Schedule{Stations: map[string]string{}, Hours: map[string]models.StationHours{}, Areas: map[string]string{}, Busy: map[string]models.Busy{}}
	all, err := ListServiceMenus()
	if err != nil {
		return s, err
	}
	for _, m := range all { // price rules (happy hours) aren't menus
		if m.Kind == "price" {
			s.Prices = append(s.Prices, m)
		} else {
			s.Menus = append(s.Menus, m)
		}
	}
	rows, err := DB.Query("SELECT dept, closed_until, hours_set, open_min, close_min, hours_last, per_day, week, busy_until, busy_minutes FROM stations")
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var d, v, week, busyUntil string
		var h models.StationHours
		var busyMin int
		if err := rows.Scan(&d, &v, &h.Set, &h.Open, &h.Close, &h.LastOrders, &h.PerDay, &week, &busyUntil, &busyMin); err != nil {
			return s, err
		}
		json.Unmarshal([]byte(week), &h.Week)
		s.Stations[d], s.Hours[d] = v, h
		if t, err := time.Parse(time.RFC3339, busyUntil); err == nil {
			s.Busy[d] = models.Busy{Until: t, Minutes: busyMin}
		}
	}
	rows.Close()
	if s.Specials, err = ListSpecialDays(); err != nil {
		return s, err
	}
	arows, err := DB.Query("SELECT zone, closed_until FROM area_closures")
	if err != nil {
		return s, err
	}
	defer arows.Close()
	for arows.Next() {
		var z, v string
		if err := arows.Scan(&z, &v); err != nil {
			return s, err
		}
		s.Areas[z] = v
	}
	return s, rows.Err()
}

// SetProductRecommended marks a dish for the guest menu's "Recommended".
func SetProductRecommended(id int, on bool) error {
	_, err := DB.Exec("UPDATE products SET recommended = ? WHERE id = ?", on, id)
	return err
}

// RecomputePopularity rebuilds the popularity snapshot from the last
// `days` of orders (cancelled ones left out): units per dish per daypart,
// by the venue's local hour. Stamps when it ran.
func RecomputePopularity(days int, now time.Time) error {
	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM popularity"); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO popularity (product_id, daypart, qty)
		SELECT oi.product_id,
			CASE WHEN h >= 5 AND h < 12 THEN 0 WHEN h >= 12 AND h < 17 THEN 1 WHEN h >= 17 AND h < 22 THEN 2 ELSE 3 END AS dp,
			SUM(oi.quantity)
		FROM order_items oi
		JOIN (SELECT id, CAST(strftime('%H', created_at, 'localtime') AS INTEGER) AS h FROM orders
		      WHERE status != 'cancelled' AND created_at >= ?) o ON o.id = oi.order_id
		WHERE oi.product_id > 0
		GROUP BY oi.product_id, dp`, now.AddDate(0, 0, -days).UTC().Format("2006-01-02 15:04:05")); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE settings SET popularity_ranked_at = ? WHERE id = 1", now.Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

// PopularitySnapshot is the current snapshot: dish id -> units per daypart.
func PopularitySnapshot() (map[int]models.Sales, error) {
	rows, err := DB.Query("SELECT product_id, daypart, qty FROM popularity")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]models.Sales{}
	for rows.Next() {
		var id, dp, qty int
		if err := rows.Scan(&id, &dp, &qty); err != nil {
			return nil, err
		}
		if dp >= 0 && dp < 4 {
			s := out[id]
			s[dp] = qty
			out[id] = s
		}
	}
	return out, rows.Err()
}

// PopularityRankedAt is when the snapshot was last rebuilt (zero = never).
func PopularityRankedAt() time.Time {
	var v string
	DB.QueryRow("SELECT COALESCE(popularity_ranked_at, '') FROM settings WHERE id = 1").Scan(&v)
	t, _ := time.Parse(time.RFC3339, v)
	return t
}

// UnitsSince is units sold per dish since a time, for spotting spikes.
func UnitsSince(since time.Time) (map[int]int, error) {
	rows, err := DB.Query(`SELECT oi.product_id, SUM(oi.quantity) FROM order_items oi JOIN orders o ON o.id = oi.order_id
		WHERE o.status != 'cancelled' AND o.created_at >= ? AND oi.product_id > 0 GROUP BY oi.product_id`,
		since.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]int{}
	for rows.Next() {
		var id, n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// SetGuestSuggestions saves the guest menu's Recommended / Popular options.
func SetGuestSuggestions(s models.Settings) error {
	_, err := DB.Exec(`UPDATE settings SET show_recommended = ?, show_popular = ?, recommended_title = ?,
		popular_days = ?, popular_count = ?, popular_scope = ?, sort_by_popularity = ?, show_upcoming = ? WHERE id = 1`,
		s.ShowRecommended, s.ShowPopular, s.RecommendedTitle, s.PopularDays, s.PopularCount, s.PopularScope, s.SortByPopularity, s.ShowUpcoming)
	return err
}

// Zones lists the venue's table areas (as set in the map editor), A–Z.
func Zones() ([]string, error) {
	rows, err := DB.Query("SELECT DISTINCT zone FROM tables WHERE zone != '' ORDER BY zone")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var z string
		if err := rows.Scan(&z); err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}
