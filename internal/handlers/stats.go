package handlers

import (
	"bytes"
	"html/template"

	"bitebox/internal/db"
)

func renderAdminStatsHTML() ([]byte, error) {
	revenue, err := db.GetTodayRevenue()
	if err != nil {
		return nil, err
	}
	orderCount, err := db.GetTodayOrderCount()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_admin_stats.html"))
	if err := tmpl.Execute(&buf, map[string]interface{}{"Revenue": revenue, "OrderCount": orderCount}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BroadcastAdminStats pushes fresh today's-revenue/order-count numbers to
// every admin dashboard connected over /admin/stats/ws. Called whenever a
// write can change either figure: a new order (order count) or a payment
// being marked paid (revenue only counts paid orders).
func BroadcastAdminStats() {
	b, err := renderAdminStatsHTML()
	if err != nil {
		return
	}
	Hub.Broadcast(topicAdminStats, oobWrap("admin-stats", b))
}
