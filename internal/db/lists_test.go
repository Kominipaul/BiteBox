package db

import (
	"strings"
	"testing"

	"bitebox/internal/models"
)

// TestListPaging: keyset pages walk the whole filtered set newest-first with
// no gaps or repeats, and search/summary run in SQL.
func TestListPaging(t *testing.T) {
	const table = 8701
	if err := CreateTable(table); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	pid, err := CreateProduct("Pagingtest Burger", 5, -1, models.CategoryOther, "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	var ids []int
	for i := 0; i < 13; i++ {
		name := "Plain Water"
		if i%2 == 0 {
			name = "Pagingtest Burger"
		}
		id, err := CreateOrder(table, "s", models.PaymentMethodCash, models.PaymentStatusUnpaid, "", 5, 0, 1,
			[]models.OrderItem{{ProductID: pid, Name: name, Price: 5, Quantity: 1}})
		if err != nil {
			t.Fatalf("CreateOrder: %v", err)
		}
		ids = append(ids, id)
	}

	f := OrderFilter{Tables: []int{table}}
	var seen []int
	for before := 0; ; {
		page, err := ListOrders(f, before, 5)
		if err != nil {
			t.Fatalf("ListOrders: %v", err)
		}
		for _, o := range page {
			if len(o.Items) != 1 {
				t.Fatalf("order %d items = %d, want 1", o.ID, len(o.Items))
			}
			seen = append(seen, o.ID)
		}
		if len(page) < 5 {
			break
		}
		before = page[len(page)-1].ID
	}
	if len(seen) != 13 || seen[0] != ids[12] || seen[12] != ids[0] {
		t.Fatalf("paged ids = %v, want the 13 new orders newest first", seen)
	}

	sum, err := SummarizeOrders(OrderFilter{Text: "pagingtest"})
	if err != nil || sum.Count != 7 || sum.Revenue != 35 || sum.UnpaidCount != 7 {
		t.Fatalf("search summary = %+v %v, want 7 orders / €35 unpaid", sum, err)
	}
	if got, _ := ListOrders(OrderFilter{Text: "100%_"}, 0, 5); len(got) != 0 {
		t.Fatalf("LIKE wildcards not escaped: %d hits", len(got))
	}

	for i := 0; i < 3; i++ {
		RequestAssistance(table, "help")
		ClearAssistance(table, "pager_eleni")
	}
	help, _ := ListHelp(HelpFilter{Text: "pager_el"}, 0, 2)
	if len(help) != 2 {
		t.Fatalf("ListHelp page = %d, want 2", len(help))
	}
	rest, _ := ListHelp(HelpFilter{Text: "pager_el"}, help[1].ID, 10)
	if hs, _ := SummarizeHelp(HelpFilter{Tables: []int{table}}); hs.Count != 3 || len(rest) != 1 {
		t.Fatalf("help summary %+v, rest %d; want 3 calls, 1 left", hs, len(rest))
	}

	LogAudit("pager_boss", "create", "pager_new", "")
	if a, _ := ListAudit(AuditFilter{Action: "create", Text: "pager_new", Range: RangeFor("today")}, 0, 10); len(a) != 1 {
		t.Fatalf("ListAudit = %d, want 1", len(a))
	}
}

// TestHotQueriesUseIndexes: the queries every page load or live feed runs
// must seek an index, never scan every order ever placed — the thing that
// would make the app slow after a year of service.
func TestHotQueriesUseIndexes(t *testing.T) {
	clause, _ := periodDateClause("today")
	for _, q := range []string{
		"SELECT COUNT(*) FROM orders WHERE " + clause,
		"SELECT id FROM orders WHERE status IN " + staffActive + " ORDER BY id",
		"SELECT id FROM orders WHERE session_id = 'x' ORDER BY id DESC LIMIT 5",
		"SELECT id FROM orders WHERE table_number = 1 AND status != 'completed' ORDER BY id DESC LIMIT 1",
		"SELECT id FROM audit_log WHERE " + RangeFor("week").clause("created_at"),
	} {
		rows, err := DB.Query("EXPLAIN QUERY PLAN " + q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		plan := ""
		for rows.Next() {
			var id, parent, unused int
			var detail string
			rows.Scan(&id, &parent, &unused, &detail)
			plan += detail + "; "
		}
		rows.Close()
		if !strings.Contains(plan, "INDEX") {
			t.Errorf("full scan: %s\n  plan: %s", q, plan)
		}
	}
}

func TestMaintainDropsExpiredSessions(t *testing.T) {
	DB.Exec("INSERT INTO auth_sessions (session_id, user_id, created_at) VALUES ('old-s', 1, datetime('now', '-3 days')), ('new-s', 1, datetime('now'))")
	Maintain()
	var n int
	DB.QueryRow("SELECT COUNT(*) FROM auth_sessions WHERE session_id IN ('old-s', 'new-s')").Scan(&n)
	if n != 1 {
		t.Fatalf("sessions left = %d, want only the fresh one", n)
	}
}
