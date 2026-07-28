package models

import "time"

type Product struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	Price       float64 `json:"price"`
	Stock       int     `json:"stock"`
	IsAvailable bool    `json:"is_available"`
}

type Table struct {
	ID            int    `json:"id"`
	Number        int    `json:"number"`
	Status        string `json:"status"` // "available" or "occupied"
	HostSessionID string `json:"host_session_id"`
}

type OrderItem struct {
	ProductID int     `json:"product_id"`
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	Quantity  int     `json:"quantity"`
}

type Cart struct {
	Items      []OrderItem `json:"items"`
	TotalAmount float64     `json:"total_amount"`
}

type SongRequest struct {
	ID        int       `json:"id"`
	SongName  string    `json:"song_name"`
	TipAmount float64   `json:"tip_amount"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

const (
	RoleAdmin  = "admin"
	RoleWorker = "worker"
)

type User struct {
	ID           int    `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
}

type Order struct {
	ID            int       `json:"id"`
	TableNumber   int       `json:"table_number"`
	SessionID     string    `json:"session_id"`
	Status        string    `json:"status"`
	PaymentMethod string    `json:"payment_method"`
	PaymentStatus string    `json:"payment_status"`
	TotalAmount   float64   `json:"total_amount"`
	CreatedAt     time.Time `json:"created_at"`
	Items         []OrderItem `json:"items,omitempty"`
}

const (
	OrderStatusPending   = "pending"
	OrderStatusPreparing = "preparing"
	OrderStatusServed    = "served"
	OrderStatusCompleted = "completed"
)

const (
	PaymentStatusUnpaid = "unpaid"
	PaymentStatusPaid   = "paid"
)

const PaymentMethodCash = "cash"

const (
	SongRequestStatusPending  = "pending"
	SongRequestStatusAccepted = "accepted"
	SongRequestStatusRejected = "rejected"
)

// OrderStatusFlow defines the valid forward progression of order lifecycle states.
var OrderStatusFlow = []string{OrderStatusPending, OrderStatusPreparing, OrderStatusServed, OrderStatusCompleted}

func IsValidOrderStatus(status string) bool {
	for _, s := range OrderStatusFlow {
		if s == status {
			return true
		}
	}
	return false
}

// NextOrderStatus returns the next stage in the lifecycle after current, or
// ok=false if current is already the final stage (or unrecognized).
func NextOrderStatus(current string) (next string, ok bool) {
	for i, s := range OrderStatusFlow {
		if s == current && i+1 < len(OrderStatusFlow) {
			return OrderStatusFlow[i+1], true
		}
	}
	return "", false
}