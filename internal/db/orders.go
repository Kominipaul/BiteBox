package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
// same "immutable at order time" reasoning as name/price.
func CreateOrder(tableNumber int, sessionID, paymentMethod, paymentStatus, note string, totalAmount float64, items []models.OrderItem) (int, error) {
	tx, err := DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		"INSERT INTO orders (table_number, session_id, status, payment_method, payment_status, total_amount, note) VALUES (?, ?, ?, ?, ?, ?, ?)",
		tableNumber, sessionID, models.OrderStatusPending, paymentMethod, paymentStatus, totalAmount, note,
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
			"INSERT INTO order_items (order_id, product_id, name, price, quantity, excluded_ingredients, extra_ingredients) VALUES (?, ?, ?, ?, ?, ?, ?)",
			orderID, item.ProductID, item.Name, item.Price, item.Quantity, excludedJSON, extrasJSON,
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

const orderColumns = "id, table_number, session_id, status, payment_method, payment_status, total_amount, served_by, note, refund_requested, created_at"

func scanOrderRow(scan func(...interface{}) error, o *models.Order) error {
	var refundRequested int
	if err := scan(&o.ID, &o.TableNumber, &o.SessionID, &o.Status, &o.PaymentMethod, &o.PaymentStatus, &o.TotalAmount, &o.ServedBy, &o.Note, &refundRequested, &o.CreatedAt); err != nil {
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

// staffExcludedStatuses is what the unfiltered ("staff"/admin) order feed
// hides — served/completed are done, cancelled never will be. "ready"
// deliberately stays visible: that's the kitchen/bar → waiter handoff, a
// waiter needs to see it to go deliver it.
const excludeThree = "(?, ?, ?)"

var staffExcludedStatuses = []interface{}{models.OrderStatusServed, models.OrderStatusCompleted, models.OrderStatusCancelled}

// categoryExcludedStatuses is what a department-filtered (bar/kitchen) feed
// hides — same as staff's, plus "ready": once bar/kitchen mark something
// ready, it's out of their hands, so it drops from their own view even
// though it's still live for staff.
const excludeFour = "(?, ?, ?, ?)"

var categoryExcludedStatuses = []interface{}{models.OrderStatusReady, models.OrderStatusServed, models.OrderStatusCompleted, models.OrderStatusCancelled}

func GetOrderByID(id int) (models.Order, error) {
	return scanOrder(DB.QueryRow("SELECT "+orderColumns+" FROM orders WHERE id = ?", id))
}

func GetOrderItems(orderID int) ([]models.OrderItem, error) {
	rows, err := DB.Query("SELECT product_id, name, price, quantity, excluded_ingredients, extra_ingredients FROM order_items WHERE order_id = ?", orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.OrderItem
	for rows.Next() {
		var it models.OrderItem
		var excludedJSON, extrasJSON string
		if err := rows.Scan(&it.ProductID, &it.Name, &it.Price, &it.Quantity, &excludedJSON, &extrasJSON); err != nil {
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
func GetActiveOrderForTable(tableNumber int) (models.Order, error) {
	return scanOrder(DB.QueryRow(
		"SELECT "+orderColumns+" FROM orders WHERE table_number = ? AND status != ? ORDER BY created_at DESC LIMIT 1",
		tableNumber, models.OrderStatusCompleted,
	))
}

// GetOrdersBySession returns the most recent orders placed by a specific
// guest session, newest first, items populated — powers the guest-facing
// "Your orders" list (every order they placed, not just the latest).
func GetOrdersBySession(sessionID string, limit int) ([]models.Order, error) {
	rows, err := DB.Query(
		"SELECT "+orderColumns+" FROM orders WHERE session_id = ? ORDER BY created_at DESC LIMIT ?",
		sessionID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []models.Order
	for rows.Next() {
		var o models.Order
		if err := scanOrderRow(rows.Scan, &o); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}

	for i := range orders {
		items, err := GetOrderItems(orders[i].ID)
		if err != nil {
			return nil, err
		}
		orders[i].Items = items
	}

	return orders, nil
}

// GetActiveOrders returns every order still needing the unfiltered/staff
// bucket's attention — pending, preparing, *and* ready (see
// staffExcludedStatuses) — oldest first. Each order's items are populated.
func GetActiveOrders() ([]models.Order, error) {
	rows, err := DB.Query(
		"SELECT "+orderColumns+" FROM orders WHERE status NOT IN "+excludeThree+" ORDER BY created_at ASC",
		staffExcludedStatuses...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []models.Order
	for rows.Next() {
		var o models.Order
		if err := scanOrderRow(rows.Scan, &o); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}

	for i := range orders {
		items, err := GetOrderItems(orders[i].ID)
		if err != nil {
			return nil, err
		}
		orders[i].Items = items
	}

	return orders, nil
}

// GetReadyOrders returns every order the kitchen/bar have marked "ready",
// any category, oldest first — the waiter's feed. This is the pop-up
// handoff: a waiter never sees pending/preparing orders (that's bar/
// kitchen's job), only ones ready to actually deliver.
func GetReadyOrders() ([]models.Order, error) {
	rows, err := DB.Query(
		"SELECT "+orderColumns+" FROM orders WHERE status = ? ORDER BY created_at ASC",
		models.OrderStatusReady,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []models.Order
	for rows.Next() {
		var o models.Order
		if err := scanOrderRow(rows.Scan, &o); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}

	for i := range orders {
		items, err := GetOrderItems(orders[i].ID)
		if err != nil {
			return nil, err
		}
		orders[i].Items = items
	}

	return orders, nil
}

// GetActiveOrdersByCategory returns orders needing a department-filtered
// (bar/kitchen) bucket's attention — pending/preparing only, not ready (see
// categoryExcludedStatuses) — that contain at least one item currently in
// the given product category. order_items only stores the immutable name/
// price snapshot, not category, so this reflects the item's *current*
// classification, not what it was at order time. Each matching order is
// returned whole (every item, not just the matching ones), so a
// department-scoped worker still has full context on the order they're
// acting on.
func GetActiveOrdersByCategory(category string) ([]models.Order, error) {
	args := append([]interface{}{}, categoryExcludedStatuses...)
	args = append(args, category)
	rows, err := DB.Query(`
		SELECT DISTINCT o.id, o.table_number, o.session_id, o.status, o.payment_method, o.payment_status, o.total_amount, o.served_by, o.note, o.refund_requested, o.created_at
		FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		JOIN products p ON p.id = oi.product_id
		WHERE o.status NOT IN `+excludeFour+` AND p.category = ?
		ORDER BY o.created_at ASC`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []models.Order
	for rows.Next() {
		var o models.Order
		if err := scanOrderRow(rows.Scan, &o); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}

	for i := range orders {
		items, err := GetOrderItems(orders[i].ID)
		if err != nil {
			return nil, err
		}
		orders[i].Items = items
	}

	return orders, nil
}

// GetOrderHistory returns the most recent orders regardless of status,
// newest first, with items populated — for the admin all-time history view.
// limit bounds the result since history only grows; it's not pagination,
// just a sane cap on a single scrollable panel.
func GetOrderHistory(limit int) ([]models.Order, error) {
	rows, err := DB.Query("SELECT "+orderColumns+" FROM orders ORDER BY created_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []models.Order
	for rows.Next() {
		var o models.Order
		if err := scanOrderRow(rows.Scan, &o); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}

	for i := range orders {
		items, err := GetOrderItems(orders[i].ID)
		if err != nil {
			return nil, err
		}
		orders[i].Items = items
	}

	return orders, nil
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
	case "today":
		return "date(created_at, 'localtime') = date('now', 'localtime')", nil
	case "week":
		return "date(created_at, 'localtime') >= date('now', 'localtime', '-6 days')", nil
	case "month":
		return "strftime('%Y-%m', created_at, 'localtime') = strftime('%Y-%m', 'now', 'localtime')", nil
	default:
		return "", ErrInvalidPeriod
	}
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
		 WHERE payment_status = ?
		 AND date(created_at, 'localtime') BETWEEN date('now', 'localtime', ?) AND date('now', 'localtime', ?)`,
		models.PaymentStatusPaid,
		fmt.Sprintf("-%d days", startDaysAgo), fmt.Sprintf("-%d days", endDaysAgo),
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
		 WHERE payment_status = ? AND date(created_at, 'localtime') >= date('now', 'localtime', ?)
		 GROUP BY d`,
		models.PaymentStatusPaid, fmt.Sprintf("-%d days", days-1),
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
