package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"bitebox/internal/models"
)

// ErrInsufficientStock means a cart item's stock ran out between the guest
// adding it and checking out (e.g. another table beat them to the last unit).
var ErrInsufficientStock = errors.New("insufficient stock")

// jsonArrayOrEmpty encodes a string slice as a JSON array, or "[]" for nil/empty.
func jsonArrayOrEmpty(items []string) (string, error) {
	if len(items) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CreateOrder persists an order and its line items in a single transaction,
// snapshotting item name/price at order time so later product edits don't
// rewrite order history. Tracked-stock items (stock != -1) are decremented
// atomically in the same transaction; if any item no longer has enough
// stock, the whole order is rolled back and ErrInsufficientStock is returned.
// Each item's Excluded ingredients are stored as a JSON array snapshot,
// same "immutable at order time" reasoning as name/price. tipAmount and
// splitCount are guest-entered, informational-beyond-the-tip-itself (see
// models.Order doc comments) — the caller is responsible for validating
// both are sane (non-negative, splitCount >= 1) before calling this.
func CreateOrder(tableNumber int, sessionID, paymentMethod, paymentStatus, note string, totalAmount, tipAmount float64, splitCount int, items []models.OrderItem) (int, error) {
	tx, err := DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		"INSERT INTO orders (table_number, session_id, status, payment_method, payment_status, total_amount, note, tip_amount, split_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		tableNumber, sessionID, models.OrderStatusPending, paymentMethod, paymentStatus, totalAmount, note, tipAmount, splitCount,
	)
	if err != nil {
		return 0, err
	}
	orderID64, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	orderID := int(orderID64)

	for _, item := range items {
		excludedJSON, err := jsonArrayOrEmpty(item.Excluded)
		if err != nil {
			return 0, err
		}
		extrasJSON, err := jsonArrayOrEmpty(item.Extras)
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(
			"INSERT INTO order_items (order_id, product_id, name, price, quantity, excluded_ingredients, extra_ingredients, vat_rate) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			orderID, item.ProductID, item.Name, item.Price, item.Quantity, excludedJSON, extrasJSON, item.VATRate,
		); err != nil {
			return 0, err
		}

		// stock = -1 is the "unlimited, don't track" sentinel and is left
		// untouched; tracked stock only decrements if enough remains.
		result, err := tx.Exec(
			"UPDATE products SET stock = CASE WHEN stock = -1 THEN stock ELSE stock - ? END WHERE id = ? AND (stock = -1 OR stock >= ?)",
			item.Quantity, item.ProductID, item.Quantity,
		)
		if err != nil {
			return 0, err
		}
		if affected, err := result.RowsAffected(); err != nil {
			return 0, err
		} else if affected == 0 {
			return 0, ErrInsufficientStock
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return orderID, nil
}

const orderColumns = "id, table_number, session_id, status, payment_method, payment_status, total_amount, served_by, note, refund_requested, created_at, tip_amount, split_count"

func scanOrderRow(scan func(...interface{}) error, o *models.Order) error {
	var refundRequested int
	if err := scan(&o.ID, &o.TableNumber, &o.SessionID, &o.Status, &o.PaymentMethod, &o.PaymentStatus, &o.TotalAmount, &o.ServedBy, &o.Note, &refundRequested, &o.CreatedAt, &o.TipAmount, &o.SplitCount); err != nil {
		return err
	}
	o.RefundRequested = refundRequested != 0
	return nil
}

func scanOrder(row *sql.Row) (models.Order, error) {
	var o models.Order
	err := scanOrderRow(row.Scan, &o)
	return o, err
}

// staffActive is what the unfiltered ("staff"/admin) order feed
// hides — served/completed are done, cancelled never will be. "ready"
// deliberately stays visible: that's the kitchen/bar → waiter handoff, a
// waiter needs to see it to go deliver it.
// Written as IN lists of the statuses still in play (not NOT IN of the done
// ones) so idx_orders_status can seek straight to the handful of live
// orders instead of scanning every order ever placed.
const staffActive = "('pending', 'preparing', 'ready')"

// categoryActive is what a department-filtered (bar/kitchen) feed
// hides — same as staff's, plus "ready": once bar/kitchen mark something
// ready, it's out of their hands, so it drops from their own view even
// though it's still live for staff.
const categoryActive = "('pending', 'preparing')"

// ProductHasActiveOrders reports whether productID appears in any order
// that isn't yet served/completed/cancelled (same staffActive cut
// as the unfiltered staff feed — "done" is the same line everywhere in this
// app). DeleteProduct's caller (AdminDeleteProduct) uses this to refuse
// deleting a product mid-order: GetActiveOrdersByCategories (the bar/
// kitchen live feed) INNER JOINs order_items to products to route by
// category, so deleting a product out from under a still-pending/preparing
// order wouldn't just leave a dangling reference — it would silently drop
// that *entire order* from the kitchen/bar feed before it's ever prepared,
// with no error and nothing left pointing at what went missing. Once an
// order reaches served/completed/cancelled nothing reads its items through
// that join anymore (order history/CSV export render straight from
// order_items' own name/price snapshot, no products join at all), so
// deletion is safe from that point on.
func ProductHasActiveOrders(productID int) (bool, error) {
	var exists bool
	err := DB.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM orders o JOIN order_items oi ON oi.order_id = o.id WHERE o.status IN "+staffActive+" AND oi.product_id = ?)",
		productID,
	).Scan(&exists)
	return exists, err
}

func GetOrderByID(id int) (models.Order, error) {
	return scanOrder(DB.QueryRow("SELECT "+orderColumns+" FROM orders WHERE id = ?", id))
}

func GetOrderItems(orderID int) ([]models.OrderItem, error) {
	rows, err := DB.Query("SELECT product_id, name, price, quantity, excluded_ingredients, extra_ingredients, vat_rate FROM order_items WHERE order_id = ?", orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.OrderItem
	for rows.Next() {
		var it models.OrderItem
		var excludedJSON, extrasJSON string
		if err := rows.Scan(&it.ProductID, &it.Name, &it.Price, &it.Quantity, &excludedJSON, &extrasJSON, &it.VATRate); err != nil {
			return nil, err
		}
		if excludedJSON != "" && excludedJSON != "[]" {
			json.Unmarshal([]byte(excludedJSON), &it.Excluded)
		}
		if extrasJSON != "" && extrasJSON != "[]" {
			json.Unmarshal([]byte(extrasJSON), &it.Extras)
		}
		items = append(items, it)
	}
	return items, nil
}

// GetActiveOrderForTable returns the most recent non-completed order for a
// table, for the guest-facing order-status widget.
//
// ", id DESC" (here and everywhere else in this file sorting by created_at)
// is a tiebreaker, not decoration: created_at comes from SQLite's
// CURRENT_TIMESTAMP, which only has 1-second resolution — two orders placed
// within the same second (two waiters checking out at once, or just two
// requests landing in the same wall-clock second, which a fast test suite
// hits constantly) sort in no defined order without one. id is the
// AUTOINCREMENT rowid, so it's already a perfect insertion-order tiebreaker
// with no extra column needed.
func GetActiveOrderForTable(tableNumber int) (models.Order, error) {
	return scanOrder(DB.QueryRow(
		"SELECT "+orderColumns+" FROM orders WHERE table_number = ? AND status != ? ORDER BY id DESC LIMIT 1",
		tableNumber, models.OrderStatusCompleted,
	))
}

// GetOrdersBySession returns the most recent orders placed by a specific
// guest session, newest first, items populated — powers the guest-facing
// "Your orders" list (every order they placed, not just the latest).
func GetOrdersBySession(sessionID string, limit int) ([]models.Order, error) {
	rows, err := DB.Query(
		"SELECT "+orderColumns+" FROM orders WHERE session_id = ? ORDER BY id DESC LIMIT ?",
		sessionID, limit,
	)
	return collectOrders(rows, err)
}

// GetActiveOrders returns every order still needing the unfiltered/staff
// bucket's attention — pending, preparing, *and* ready (see
// staffActive) — oldest first. Each order's items are populated.
func GetActiveOrders() ([]models.Order, error) {
	rows, err := DB.Query(
		"SELECT " + orderColumns + " FROM orders WHERE status IN " + staffActive + " ORDER BY id ASC",
	)
	return collectOrders(rows, err)
}

// GetReadyOrders returns every order the kitchen/bar have marked "ready",
// any category, oldest first — the waiter's feed. This is the pop-up
// handoff: a waiter never sees pending/preparing orders (that's bar/
// kitchen's job), only ones ready to actually deliver.
func GetReadyOrders() ([]models.Order, error) {
	rows, err := DB.Query(
		"SELECT "+orderColumns+" FROM orders WHERE status = ? ORDER BY id ASC",
		models.OrderStatusReady,
	)
	return collectOrders(rows, err)
}

// GetActiveOrdersByCategories returns orders needing a department-filtered
// (bar/kitchen) bucket's attention — pending/preparing only, not ready (see
// categoryActive) — that contain at least one item currently in
// any of the given product categories (an admin can add as many categories
// as the menu needs, each tagged kitchen or bar — see
// CategoryNamesForDepartment, which is what builds this list). order_items
// only stores the immutable name/price snapshot, not category, so this
// reflects the item's *current* classification, not what it was at order
// time. Each matching order is
// returned whole (every item, not just the matching ones), so a
// department-scoped worker still has full context on the order they're
// acting on. An empty/nil categories list matches nothing (an empty result,
// not "unfiltered" — callers wanting everything should use GetActiveOrders).
func GetActiveOrdersByCategories(categories []string) ([]models.Order, error) {
	if len(categories) == 0 {
		return nil, nil
	}

	placeholders := make([]byte, 0, len(categories)*2)
	var args []interface{}
	for i, cat := range categories {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, cat)
	}

	rows, err := DB.Query(`
		SELECT DISTINCT o.id, o.table_number, o.session_id, o.status, o.payment_method, o.payment_status, o.total_amount, o.served_by, o.note, o.refund_requested, o.created_at, o.tip_amount, o.split_count
		FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		JOIN products p ON p.id = oi.product_id
		WHERE o.status IN `+categoryActive+` AND p.category IN (`+string(placeholders)+`)
		ORDER BY o.id ASC`,
		args...,
	)
	return collectOrders(rows, err)
}

// GetOrderHistory returns up to limit orders regardless of status, newest
// first, with items populated, skipping the first offset — real pagination
// for the admin all-time history view (offset=0, called again with the next
// offset for "Load more"). limit=-1 means unlimited (SQLite's own LIMIT -1
// convention) — used only by AdminExportCSV, which wants everything.
//
// The ", id DESC" tiebreaker matters more here than anywhere else it's
// used in this file: this is the one query paginated across multiple
// requests (offset increasing each "Load more" click). Without a
// deterministic total order, two orders tied on created_at's 1-second
// resolution could sort differently between one request and the next,
// which for a paginated view doesn't just look wrong once — it can skip an
// order entirely (it shifts to a page already fetched) or repeat one
// (it shifts to a page not fetched yet).
func GetOrderHistory(limit, offset int) ([]models.Order, error) {
	rows, err := DB.Query("SELECT "+orderColumns+" FROM orders ORDER BY id DESC LIMIT ? OFFSET ?", limit, offset)
	return collectOrders(rows, err)
}

// GetOrderHistoryCount is the total row count GetOrderHistory paginates
// over — the admin history panel's "Load more" button hides itself once
// offset+limit reaches this.
func GetOrderHistoryCount() (int, error) {
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM orders").Scan(&count)
	return count, err
}

// OrderHasAnyCategory reports whether orderID contains at least one item
// currently classified (via products.category, joined live — same
// "current classification, not order-time snapshot" reasoning as
// GetActiveOrdersByCategories) under any of the given category names.
// Used to tighten a bar/kitchen worker's write access to only orders that
// are actually theirs (see RequireDepartment's doc comment on the
// department-boundary gap this closes) — the read-side feed already
// filters this way; this is the same rule enforced on the mutating routes.
func OrderHasAnyCategory(orderID int, categories []string) (bool, error) {
	if len(categories) == 0 {
		return false, nil
	}
	placeholders := make([]byte, 0, len(categories)*2)
	args := []interface{}{orderID}
	for i, cat := range categories {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, cat)
	}
	var exists int
	err := DB.QueryRow(
		`SELECT EXISTS(
			SELECT 1 FROM order_items oi
			JOIN products p ON p.id = oi.product_id
			WHERE oi.order_id = ? AND p.category IN (`+string(placeholders)+`)
		)`,
		args...,
	).Scan(&exists)
	return exists != 0, err
}

func UpdateOrderStatus(orderID int, status string) error {
	_, err := DB.Exec("UPDATE orders SET status = ? WHERE id = ?", status, orderID)
	return err
}

// SetServedBy records which staff member marked an order served, for the
// admin order-history attribution.
func SetServedBy(orderID int, username string) error {
	_, err := DB.Exec("UPDATE orders SET served_by = ? WHERE id = ?", username, orderID)
	return err
}

func UpdateOrderPaymentStatus(orderID int, paymentStatus string) error {
	_, err := DB.Exec("UPDATE orders SET payment_status = ? WHERE id = ?", paymentStatus, orderID)
	return err
}

// RequestRefund flags an order for staff attention — set when a guest tries
// to cancel an order that's already paid, which they can't do instantly
// self-service (no payment gateway exists to reverse a charge, and cash
// already collected needs to be handed back in person).
func RequestRefund(orderID int) error {
	_, err := DB.Exec("UPDATE orders SET refund_requested = 1 WHERE id = ?", orderID)
	return err
}

// ErrCannotCancel means the order has already progressed past the point
// where cancelling makes sense (already ready/served/completed/cancelled —
// once the kitchen/bar has finished preparing it, cancelling would just
// waste the food/drink already made).
var ErrCannotCancel = errors.New("order can no longer be cancelled")

// CancelOrder marks an order cancelled and restores any stock its items had
// reserved (only tracked items — stock=-1 stays untouched, same "unlimited"
// sentinel CreateOrder respects). Only pending/preparing orders can be
// cancelled, all inside one transaction so the status check and stock
// restore can't race against a concurrent status update. Returns the
// order's table number, so the caller can broadcast to the right places
// without a second query.
func CancelOrder(orderID int) (tableNumber int, err error) {
	tx, err := DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var status string
	if err := tx.QueryRow("SELECT table_number, status FROM orders WHERE id = ?", orderID).Scan(&tableNumber, &status); err != nil {
		return 0, err
	}
	if status != models.OrderStatusPending && status != models.OrderStatusPreparing {
		return 0, ErrCannotCancel
	}

	items, err := GetOrderItems(orderID)
	if err != nil {
		return 0, err
	}
	for _, item := range items {
		if _, err := tx.Exec(
			"UPDATE products SET stock = CASE WHEN stock = -1 THEN stock ELSE stock + ? END WHERE id = ?",
			item.Quantity, item.ProductID,
		); err != nil {
			return 0, err
		}
	}

	if _, err := tx.Exec("UPDATE orders SET status = ? WHERE id = ?", models.OrderStatusCancelled, orderID); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return tableNumber, nil
}

// ErrInvalidPeriod is returned by GetStatsForPeriod for any period other
// than "today", "week", or "month".
var ErrInvalidPeriod = errors.New("invalid period")

type PeriodStats struct {
	Revenue        float64 // sum of paid orders' totals in the period
	OrderCount     int     // all orders in the period, any status
	PaidOrderCount int     // orders in the period with payment_status = paid
	AvgOrder       float64 // Revenue / PaidOrderCount, 0 if none paid yet
}

// periodDateClause returns a SQL boolean expression selecting orders.created_at
// within the given period, in the venue's local calendar. created_at is
// stored as SQLite's default UTC timestamp, so every comparison needs the
// 'localtime' modifier — comparing a bare UTC date against a localtime one
// silently drops/misdates orders for part of every day (worse the further
// the server's timezone sits from UTC, e.g. after local midnight but before
// UTC midnight, exactly prime hours for a bar/restaurant).
//
// "week" is a rolling 7-day window (today and the 6 days before it), not a
// calendar week — avoids Monday-vs-Sunday week-start ambiguity. "month" is
// the current calendar month, resetting on the 1st.
func periodDateClause(period string) (string, error) {
	switch period {
	case "today", "week", "month":
		return RangeFor(period).clause("created_at"), nil
	}
	return "", ErrInvalidPeriod
}

// GetStatsForPeriod computes the admin overview KPIs for "today", "week",
// or "month".
func GetStatsForPeriod(period string) (PeriodStats, error) {
	var s PeriodStats
	clause, err := periodDateClause(period)
	if err != nil {
		return s, err
	}

	if err := DB.QueryRow(
		"SELECT COUNT(*) FROM orders WHERE "+clause+" AND status != ?",
		models.OrderStatusCancelled,
	).Scan(&s.OrderCount); err != nil {
		return s, err
	}
	if err := DB.QueryRow(
		"SELECT COALESCE(SUM(total_amount), 0), COUNT(*) FROM orders WHERE "+clause+" AND payment_status = ?",
		models.PaymentStatusPaid,
	).Scan(&s.Revenue, &s.PaidOrderCount); err != nil {
		return s, err
	}
	if s.PaidOrderCount > 0 {
		s.AvgOrder = s.Revenue / float64(s.PaidOrderCount)
	}
	return s, nil
}

// GetRevenueForRange sums paid revenue between two "days ago" offsets from
// today, inclusive, in the venue's local calendar — e.g. (13, 7) means
// "8 to 14 days ago", used to compare the trailing 7 days against the 7
// days before that.
func GetRevenueForRange(startDaysAgo, endDaysAgo int) (float64, error) {
	var revenue float64
	err := DB.QueryRow(
		`SELECT COALESCE(SUM(total_amount), 0) FROM orders
		 WHERE payment_status = ? AND `+daysAgo(startDaysAgo, endDaysAgo-1).clause("created_at"),
		models.PaymentStatusPaid,
	).Scan(&revenue)
	return revenue, err
}

type DayRevenue struct {
	Date    string // YYYY-MM-DD, local calendar day
	Label   string // short weekday label, e.g. "Tue"
	Revenue float64
	IsToday bool
}

// GetRevenueTrend returns paid revenue for each of the last `days` local
// calendar days, oldest first with today last, zero-filled for days with no
// paid orders (so the caller can render a fixed-width bar chart).
func GetRevenueTrend(days int) ([]DayRevenue, error) {
	rows, err := DB.Query(
		`SELECT date(created_at, 'localtime') AS d, SUM(total_amount)
		 FROM orders
		 WHERE payment_status = ? AND `+daysAgo(days-1, -1).clause("created_at")+`
		 GROUP BY d`,
		models.PaymentStatusPaid,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byDate := make(map[string]float64)
	for rows.Next() {
		var d string
		var revenue float64
		if err := rows.Scan(&d, &revenue); err != nil {
			return nil, err
		}
		byDate[d] = revenue
	}

	today := time.Now()
	result := make([]DayRevenue, days)
	for i := 0; i < days; i++ {
		day := today.AddDate(0, 0, -(days - 1 - i))
		key := day.Format("2006-01-02")
		result[i] = DayRevenue{
			Date:    key,
			Label:   day.Format("Mon"),
			Revenue: byDate[key],
			IsToday: i == days-1,
		}
	}
	return result, nil
}

// TopProduct is one row of the admin overview's best-sellers list.
type TopProduct struct {
	Name     string
	Quantity int
	Revenue  float64
	Percent  float64 // of this list's top row's revenue, for the in-row bar
}

// GetTopProducts returns the best-selling items (by units sold, on paid
// orders) for the given period, highest first, capped at limit. Revenue is
// quantity * the order_item's own snapshotted price (see CreateOrder), so a
// since-changed product price doesn't retroactively rewrite past revenue.
func GetTopProducts(period string, limit int) ([]TopProduct, error) {
	clause, err := periodDateClause(period)
	if err != nil {
		return nil, err
	}
	rows, err := DB.Query(
		`SELECT oi.name, SUM(oi.quantity) AS qty, SUM(oi.quantity * oi.price) AS revenue
		 FROM order_items oi
		 JOIN orders o ON o.id = oi.order_id
		 WHERE o.payment_status = ? AND `+clause+`
		 GROUP BY oi.name
		 ORDER BY qty DESC
		 LIMIT ?`,
		models.PaymentStatusPaid, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var top []TopProduct
	for rows.Next() {
		var p TopProduct
		if err := rows.Scan(&p.Name, &p.Quantity, &p.Revenue); err != nil {
			return nil, err
		}
		top = append(top, p)
	}
	if len(top) > 0 {
		best := top[0].Quantity
		for i := range top {
			top[i].Percent = float64(top[i].Quantity) / float64(best) * 100
		}
	}
	return top, rows.Err()
}

// CategorySales is one slice of the admin overview's kitchen-vs-bar revenue
// split (see categories.department).
type CategorySales struct {
	Department string // "kitchen" or "bar"
	Revenue    float64
	Percent    float64 // of total revenue across both departments, 0-100
}

// GetCategorySales splits paid revenue for the given period between the
// kitchen and bar departments, by joining each order item back to its
// product's category and that category's department. Items whose product
// or category has since been deleted are excluded (nothing to attribute
// them to), same as any other historical-snapshot edge case here.
func GetCategorySales(period string) ([]CategorySales, error) {
	clause, err := periodDateClause(period)
	if err != nil {
		return nil, err
	}
	rows, err := DB.Query(
		`SELECT c.department, SUM(oi.quantity * oi.price) AS revenue
		 FROM order_items oi
		 JOIN orders o ON o.id = oi.order_id
		 JOIN products p ON p.id = oi.product_id
		 JOIN categories c ON c.name = p.category
		 WHERE o.payment_status = ? AND `+clause+`
		 GROUP BY c.department`,
		models.PaymentStatusPaid,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CategorySales
	var total float64
	for rows.Next() {
		var c CategorySales
		if err := rows.Scan(&c.Department, &c.Revenue); err != nil {
			return nil, err
		}
		total += c.Revenue
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if total > 0 {
		for i := range out {
			out[i].Percent = out[i].Revenue / total * 100
		}
	}
	return out, nil
}

// CountServedBy is how many orders username has marked served since t —
// the staff app's "your shift so far".
func CountServedBy(username string, since time.Time) int {
	var n int
	DB.QueryRow(`SELECT COUNT(*) FROM orders WHERE served_by = ? AND created_at >= ?`,
		username, since.UTC().Format("2006-01-02 15:04:05")).Scan(&n)
	return n
}
