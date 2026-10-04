package db

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"bitebox/internal/models"
)

// Paged, filtered lists for the Orders and Logs pages. Everything is
// filtered and counted in SQL and fetched a page at a time with a keyset
// cursor ("id < Before", newest first), so a page stays fast however many
// rows the venue piles up. Ids only ever grow with time, so id order is
// time order — and it's the primary key, the cheapest order SQLite has.
// ponytail: free-text search is LIKE '%…%' (a scan, bounded by the page
// size once matches exist); switch to SQLite FTS5 if searches get slow.

// TimeRange is a UTC [From, To) window matching the CURRENT_TIMESTAMP text
// the tables store ("2006-01-02 15:04:05"); empty ends are open.
type TimeRange struct{ From, To string }

const utcLayout = "2006-01-02 15:04:05"

// RangeFor turns a page's "when" filter into UTC bounds of the venue's
// local calendar: today, yesterday, week (last 7 days), month (this
// calendar month); anything else is all time.
func RangeFor(when string) TimeRange {
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	f := func(t time.Time) string { return t.UTC().Format(utcLayout) }
	switch when {
	case "today":
		return TimeRange{f(day), ""}
	case "yesterday":
		return TimeRange{f(day.AddDate(0, 0, -1)), f(day)}
	case "week":
		return TimeRange{f(day.AddDate(0, 0, -6)), ""}
	case "month":
		return TimeRange{f(time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)), ""}
	}
	return TimeRange{}
}

// daysAgo is the local days from `from` days ago through `to` days ago,
// inclusive (to = -1 means open-ended, through now).
func daysAgo(from, to int) TimeRange {
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	r := TimeRange{From: day.AddDate(0, 0, -from).UTC().Format(utcLayout)}
	if to >= 0 {
		r.To = day.AddDate(0, 0, -to+1).UTC().Format(utcLayout)
	}
	return r
}

// clause renders the range as a plain column comparison — never wrap the
// column in date()/strftime(), which would stop SQLite using its index and
// scan every row ever written. The bounds are our own formatted
// timestamps, so inlining them is safe.
func (r TimeRange) clause(col string) string {
	c := "1=1"
	if r.From != "" {
		c += " AND " + col + " >= '" + r.From + "'"
	}
	if r.To != "" {
		c += " AND " + col + " < '" + r.To + "'"
	}
	return c
}

// where collects SQL conditions and their args.
type where struct {
	conds []string
	args  []interface{}
}

func (w *where) add(cond string, args ...interface{}) {
	w.conds = append(w.conds, cond)
	w.args = append(w.args, args...)
}

func (w *where) timeRange(col string, r TimeRange) {
	if r.From != "" {
		w.add(col+" >= ?", r.From)
	}
	if r.To != "" {
		w.add(col+" < ?", r.To)
	}
}

func (w *where) sql() string {
	if len(w.conds) == 0 {
		return "1=1"
	}
	return strings.Join(w.conds, " AND ")
}

func inInts(col string, ids []int) (string, []interface{}) {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return col + " IN (" + ph + ")", args
}

// likeArg escapes a user's text for LIKE ... ESCAPE '\'.
func likeArg(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// ---------- Orders ----------

type OrderFilter struct {
	Range  TimeRange
	Status string // "", "open", or an order status
	Pay    string // "", "paid", "unpaid"
	// Search: Number matches an order # or table number; Tables are table
	// numbers whose label matched (e.g. "F01"); Text matches an item name
	// or the staff member who served it.
	Number int
	Tables []int
	Text   string
}

func (f OrderFilter) where() *where {
	w := &where{}
	w.timeRange("created_at", f.Range)
	switch f.Status {
	case "":
	case "open":
		w.add("status NOT IN ('served','completed','cancelled')")
	default:
		w.add("status = ?", f.Status)
	}
	if f.Pay != "" {
		w.add("payment_status = ?", f.Pay)
	}
	var or []string
	var args []interface{}
	if f.Number > 0 {
		or = append(or, "id = ?", "table_number = ?")
		args = append(args, f.Number, f.Number)
	}
	if len(f.Tables) > 0 {
		c, a := inInts("table_number", f.Tables)
		or = append(or, c)
		args = append(args, a...)
	}
	if f.Text != "" {
		or = append(or, `served_by LIKE ? ESCAPE '\'`,
			`EXISTS (SELECT 1 FROM order_items oi WHERE oi.order_id = orders.id AND oi.name LIKE ? ESCAPE '\')`)
		args = append(args, likeArg(f.Text), likeArg(f.Text))
	}
	if len(or) > 0 {
		w.add("("+strings.Join(or, " OR ")+")", args...)
	}
	return w
}

// ListOrders returns up to limit orders matching f, newest first, older than
// order id before (0 = from the newest), with items.
func ListOrders(f OrderFilter, before, limit int) ([]models.Order, error) {
	w := f.where()
	if before > 0 {
		w.add("id < ?", before)
	}
	return collectOrders(DB.Query("SELECT "+orderColumns+" FROM orders WHERE "+w.sql()+" ORDER BY id DESC LIMIT ?", append(w.args, limit)...))
}

// collectOrders scans an orders query and loads all their items in one
// more query (not one per order).
func collectOrders(rows *sql.Rows, err error) ([]models.Order, error) {
	if err != nil {
		return nil, err
	}
	var orders []models.Order
	for rows.Next() {
		var o models.Order
		if err := scanOrderRow(rows.Scan, &o); err != nil {
			rows.Close()
			return nil, err
		}
		orders = append(orders, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return orders, attachItems(orders)
}

// attachItems loads every listed order's items in one query.
func attachItems(orders []models.Order) error {
	if len(orders) == 0 {
		return nil
	}
	ids := make([]int, len(orders))
	at := map[int]int{}
	for i, o := range orders {
		ids[i], at[o.ID] = o.ID, i
	}
	cond, args := inInts("order_id", ids)
	rows, err := DB.Query("SELECT order_id, product_id, name, price, quantity, excluded_ingredients, extra_ingredients, vat_rate FROM order_items WHERE "+cond+" ORDER BY id", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var oid int
		var it models.OrderItem
		var excluded, extras string
		if err := rows.Scan(&oid, &it.ProductID, &it.Name, &it.Price, &it.Quantity, &excluded, &extras, &it.VATRate); err != nil {
			return err
		}
		if excluded != "" && excluded != "[]" {
			json.Unmarshal([]byte(excluded), &it.Excluded)
		}
		if extras != "" && extras != "[]" {
			json.Unmarshal([]byte(extras), &it.Extras)
		}
		orders[at[oid]].Items = append(orders[at[oid]].Items, it)
	}
	return rows.Err()
}

// OrderTotals is the Orders page summary line for a filter.
type OrderTotals struct {
	Count, UnpaidCount    int
	Revenue, Tips, Unpaid float64
}

func SummarizeOrders(f OrderFilter) (OrderTotals, error) {
	w := f.where()
	var t OrderTotals
	err := DB.QueryRow(`SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN status != 'cancelled' THEN total_amount + tip_amount END), 0),
		COALESCE(SUM(CASE WHEN status != 'cancelled' THEN tip_amount END), 0),
		COALESCE(SUM(CASE WHEN status != 'cancelled' AND payment_status != 'paid' THEN total_amount + tip_amount END), 0),
		COUNT(CASE WHEN status != 'cancelled' AND payment_status != 'paid' THEN 1 END)
		FROM orders WHERE `+w.sql(), w.args...).Scan(&t.Count, &t.Revenue, &t.Tips, &t.Unpaid, &t.UnpaidCount)
	return t, err
}

// ---------- Call-waiter log ----------

type HelpFilter struct {
	Range  TimeRange
	Tables []int  // search: table numbers (by number or label)
	Text   string // search: who answered
}

func (f HelpFilter) where() *where {
	w := &where{}
	w.timeRange("acknowledged_at", f.Range)
	var or []string
	var args []interface{}
	if len(f.Tables) > 0 {
		c, a := inInts("table_number", f.Tables)
		or, args = append(or, c), append(args, a...)
	}
	if f.Text != "" {
		or, args = append(or, `acknowledged_by LIKE ? ESCAPE '\'`), append(args, likeArg(f.Text))
	}
	if len(or) > 0 {
		w.add("("+strings.Join(or, " OR ")+")", args...)
	}
	return w
}

func ListHelp(f HelpFilter, before, limit int) ([]models.AssistanceLogEntry, error) {
	w := f.where()
	if before > 0 {
		w.add("id < ?", before)
	}
	rows, err := DB.Query("SELECT "+assistanceLogColumns+" FROM assistance_log WHERE "+w.sql()+" ORDER BY id DESC LIMIT ?", append(w.args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.AssistanceLogEntry
	for rows.Next() {
		e, err := scanAssistanceLogEntry(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type HelpTotals struct {
	Count         int
	AvgWait, Slow int // seconds; Slow = longest wait
}

func SummarizeHelp(f HelpFilter) (HelpTotals, error) {
	w := f.where()
	var t HelpTotals
	var avg, max sql.NullFloat64
	w.add("COALESCE(outcome, 'answered') = 'answered'") // response-time stats: calls staff actually answered
	err := DB.QueryRow("SELECT COUNT(*), AVG(wait_seconds), MAX(wait_seconds) FROM assistance_log WHERE "+w.sql(), w.args...).Scan(&t.Count, &avg, &max)
	t.AvgWait, t.Slow = int(avg.Float64), int(max.Float64)
	return t, err
}

// ---------- Staff activity (audit log) ----------

type AuditFilter struct {
	Range  TimeRange
	Action string
	Text   string // who did it, or who it was done to
}

func ListAudit(f AuditFilter, before, limit int) ([]models.AuditEntry, error) {
	w := &where{}
	w.timeRange("created_at", f.Range)
	if f.Action != "" {
		w.add("action = ?", f.Action)
	}
	if f.Text != "" {
		w.add(`(actor LIKE ? ESCAPE '\' OR target LIKE ? ESCAPE '\')`, likeArg(f.Text), likeArg(f.Text))
	}
	if before > 0 {
		w.add("id < ?", before)
	}
	rows, err := DB.Query("SELECT "+auditColumns+" FROM audit_log WHERE "+w.sql()+" ORDER BY id DESC LIMIT ?", append(w.args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.AuditEntry
	for rows.Next() {
		var e models.AuditEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.Target, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
