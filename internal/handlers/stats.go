package handlers

import (
	"bytes"
	"html/template"
	"net/http"

	"bitebox/internal/db"
)

var statsPeriods = []string{"today", "week", "month"}

func normalizePeriod(period string) string {
	for _, p := range statsPeriods {
		if p == period {
			return p
		}
	}
	return "today"
}

func adminStatsData(period string) (map[string]interface{}, error) {
	stats, err := db.GetStatsForPeriod(period)
	if err != nil {
		return nil, err
	}
	tables, err := db.GetAllTables()
	if err != nil {
		return nil, err
	}
	occupied := 0
	for _, t := range tables {
		if t.Status == "occupied" {
			occupied++
		}
	}
	return map[string]interface{}{
		"Period":         period,
		"Stats":          stats,
		"TablesOccupied": occupied,
		"TablesTotal":    len(tables),
	}, nil
}

// renderAdminStatsHTML renders the FULL block: the outer #admin-stats-socket
// div (which owns hx-ext="ws" ws-connect, period baked into the query
// param) wrapping the #admin-stats content. This is only ever used as an
// outerHTML swap target (the period tabs) or the page's initial hx-get —
// never sent over the websocket itself, since re-delivering ws-connect
// through the connection it belongs to would make htmx tear down and
// reopen it on every single message (this exact bug caused a connection
// storm before it was caught — see renderAdminStatsInnerHTML).
func renderAdminStatsHTML(period string) ([]byte, error) {
	period = normalizePeriod(period)
	data, err := adminStatsData(period)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_admin_stats.html"))
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// renderAdminStatsInnerHTML renders just the #admin-stats content (tabs +
// KPI cards, no ws-connect wrapper) — this is what actually goes out over
// an already-open websocket, both as the initial snapshot and every later
// broadcast.
func renderAdminStatsInnerHTML(period string) ([]byte, error) {
	period = normalizePeriod(period)
	data, err := adminStatsData(period)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_admin_stats.html"))
	if err := tmpl.ExecuteTemplate(&buf, "adminStatsInner", data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BroadcastAdminStats pushes fresh numbers to every admin dashboard, for
// whichever period each one currently has open — a new/paid order can shift
// today's, this week's, and this month's totals all at once, so all three
// period topics get a fresh render.
func BroadcastAdminStats() {
	for _, period := range statsPeriods {
		b, err := renderAdminStatsInnerHTML(period)
		if err != nil {
			continue
		}
		Hub.Broadcast(topicAdminStats(period), oobWrap("admin-stats", b))
	}
}

// AdminStatsPeriod serves the full #admin-stats-socket fragment for a given
// ?period=today|week|month, the target of the period tab buttons.
func AdminStatsPeriod(w http.ResponseWriter, r *http.Request) {
	b, err := renderAdminStatsHTML(r.URL.Query().Get("period"))
	if err != nil {
		http.Error(w, "Failed to load stats", http.StatusInternalServerError)
		return
	}
	w.Write(b)
}
