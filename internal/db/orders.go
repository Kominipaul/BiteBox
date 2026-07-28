package db

import (
	"database/sql"

	"bitebox/internal/models"
)

// CreateOrder persists an order and its line items in a single transaction,
// snapshotting item name/price at order time so later product edits don't
// rewrite order history.
func CreateOrder(tableNumber int, sessionID, paymentMethod, paymentStatus string, totalAmount float64, items []models.OrderItem) (int, error) {
	tx, err := DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		"INSERT INTO orders (table_number, session_id, status, payment_method, payment_status, total_amount) VALUES (?, ?, ?, ?, ?, ?)",
		tableNumber, sessionID, models.OrderStatusPending, paymentMethod, paymentStatus, totalAmount,
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
		if _, err := tx.Exec(
			"INSERT INTO order_items (order_id, product_id, name, price, quantity) VALUES (?, ?, ?, ?, ?)",
			orderID, item.ProductID, item.Name, item.Price, item.Quantity,
		); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return orderID, nil
}

func scanOrder(row *sql.Row) (models.Order, error) {
	var o models.Order
	err := row.Scan(&o.ID, &o.TableNumber, &o.SessionID, &o.Status, &o.PaymentMethod, &o.PaymentStatus, &o.TotalAmount, &o.CreatedAt)
	return o, err
}

const orderColumns = "id, table_number, session_id, status, payment_method, payment_status, total_amount, created_at"

func GetOrderByID(id int) (models.Order, error) {
	return scanOrder(DB.QueryRow("SELECT "+orderColumns+" FROM orders WHERE id = ?", id))
}

func GetOrderItems(orderID int) ([]models.OrderItem, error) {
	rows, err := DB.Query("SELECT product_id, name, price, quantity FROM order_items WHERE order_id = ?", orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.OrderItem
	for rows.Next() {
		var it models.OrderItem
		if err := rows.Scan(&it.ProductID, &it.Name, &it.Price, &it.Quantity); err != nil {
			return nil, err
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

// GetActiveOrders returns every non-completed order, oldest first, for the
// worker live feed. Each order's items are populated for display.
func GetActiveOrders() ([]models.Order, error) {
	rows, err := DB.Query(
		"SELECT "+orderColumns+" FROM orders WHERE status != ? ORDER BY created_at ASC",
		models.OrderStatusCompleted,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []models.Order
	for rows.Next() {
		var o models.Order
		if err := rows.Scan(&o.ID, &o.TableNumber, &o.SessionID, &o.Status, &o.PaymentMethod, &o.PaymentStatus, &o.TotalAmount, &o.CreatedAt); err != nil {
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

func UpdateOrderPaymentStatus(orderID int, paymentStatus string) error {
	_, err := DB.Exec("UPDATE orders SET payment_status = ? WHERE id = ?", paymentStatus, orderID)
	return err
}

func GetTodayRevenue() (float64, error) {
	var revenue float64
	err := DB.QueryRow(
		"SELECT COALESCE(SUM(total_amount), 0) FROM orders WHERE date(created_at) = date('now', 'localtime') AND payment_status = ?",
		models.PaymentStatusPaid,
	).Scan(&revenue)
	return revenue, err
}

func GetTodayOrderCount() (int, error) {
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM orders WHERE date(created_at) = date('now', 'localtime')").Scan(&count)
	return count, err
}
