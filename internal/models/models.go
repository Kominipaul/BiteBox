package models

import "time"

// Settings is a venue-wide singleton row of feature toggles.
type Settings struct {
	DJRequestsEnabled bool
	VenueName         string
}

type Product struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	Price       float64 `json:"price"`
	Stock       int     `json:"stock"`
	IsAvailable bool    `json:"is_available"`
	Category    string  `json:"category"`
	// Subcategory is a free-text grouping label within Category (e.g. "Main
	// Courses" within Food) — purely a guest-menu display grouping, not a
	// validated set like Category. Blank means "no sub-heading" for this
	// item; the guest menu renders it under its Category with no group label.
	Subcategory string `json:"subcategory"`
	// Description is optional guest-facing menu copy shown under the item
	// name (e.g. "Grilled octopus, fava cream, cuttlefish ink..."). Blank
	// means no description line is rendered.
	Description string `json:"description"`
}

const (
	CategoryFood        = "Food"
	CategoryCoffeeSoft  = "Coffee & Soft"
	CategoryBeerSpirits = "Beer & Spirits"
	CategoryCocktails   = "Cocktails"
	CategoryWine        = "Wine"
	// CategoryOther is a fallback bucket only — for legacy/blank category
	// data and anything an admin doesn't explicitly categorize — not one of
	// the guest-facing menu tabs.
	CategoryOther = "Other"
)

var ProductCategories = []string{CategoryFood, CategoryCoffeeSoft, CategoryBeerSpirits, CategoryCocktails, CategoryWine, CategoryOther}

func IsValidCategory(c string) bool {
	for _, v := range ProductCategories {
		if v == c {
			return true
		}
	}
	return false
}

type Table struct {
	ID            int    `json:"id"`
	Number        int    `json:"number"`
	Status        string `json:"status"` // "available" or "occupied"
	HostSessionID string `json:"host_session_id"`
}

type OrderItem struct {
	ProductID int      `json:"product_id"`
	Name      string   `json:"name"`
	Price     float64  `json:"price"`
	Quantity  int      `json:"quantity"`
	Excluded  []string `json:"excluded,omitempty"` // removable ingredients left out of this line
	Extras    []string `json:"extras,omitempty"`   // extra ingredients added to this line (free, no price change)
}

// Ingredient tags a product with something guests can customize.
// "removable" ingredients are included by default and can be excluded per
// order line (the only kind the guest UI currently acts on); "extra" tags
// something addable but not yet wired to any guest-facing add-on flow —
// stored for admin management so that's a template-only change later, not
// a schema one.
type Ingredient struct {
	ID        int    `json:"id"`
	ProductID int    `json:"product_id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
}

const (
	IngredientRemovable = "removable"
	IngredientExtra     = "extra"
)

func IsValidIngredientKind(k string) bool {
	return k == IngredientRemovable || k == IngredientExtra
}

type Cart struct {
	Items      []OrderItem `json:"items"`
	TotalAmount float64     `json:"total_amount"`
}

type SongRequest struct {
	ID          int       `json:"id"`
	SongName    string    `json:"song_name"`
	TipAmount   float64   `json:"tip_amount"`
	Status      string    `json:"status"`
	TableNumber int       `json:"table_number"`
	CreatedAt   time.Time `json:"created_at"`
}

const (
	RoleAdmin  = "admin"
	RoleWorker = "worker"
)

// Departments scope what a worker account can see/do beyond the base
// worker role — see RequireDepartment. Admins bypass department checks
// entirely (treated the same as DepartmentSuperworker), so an admin
// account's own department value is never consulted.
const (
	// DepartmentSuperworker sees and can act on every order, every status,
	// every category — the "manages everything" role.
	DepartmentSuperworker = "superworker"
	// DepartmentWaiter sees only orders the kitchen/bar have marked
	// "ready" (any category) — the pop-up-when-ready handoff. A waiter
	// never sees pending/preparing orders; that's bar/kitchen's job.
	DepartmentWaiter = "waiter"
	// DepartmentBar sees pending/preparing Drinks orders only — once
	// marked ready it's handed off to the waiter and drops from bar's feed.
	DepartmentBar = "bar"
	// DepartmentKitchen sees pending/preparing Food orders only, same
	// ready-handoff rule as bar.
	DepartmentKitchen = "kitchen"
	// DepartmentDJ sees the DJ terminal only, no order feed at all.
	DepartmentDJ = "dj"
)

var Departments = []string{DepartmentSuperworker, DepartmentWaiter, DepartmentBar, DepartmentKitchen, DepartmentDJ}

func IsValidDepartment(d string) bool {
	for _, v := range Departments {
		if v == d {
			return true
		}
	}
	return false
}

// DepartmentCategories maps a department to the product categories its
// order feed is filtered to; nil means unfiltered (see everything). Bar
// covers every category that isn't Food — so a menu with any number of
// drink categories (Coffee & Soft, Beer & Spirits, Cocktails, Wine, ...)
// still routes to bar without this needing to name each one.
func DepartmentCategories(department string) []string {
	switch department {
	case DepartmentBar:
		var cats []string
		for _, c := range ProductCategories {
			if c != CategoryFood {
				cats = append(cats, c)
			}
		}
		return cats
	case DepartmentKitchen:
		return []string{CategoryFood}
	default:
		return nil
	}
}

type User struct {
	ID           int        `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`
	Role         string     `json:"role"`
	Department   string     `json:"department"`
	IsActive     bool       `json:"is_active"`
	CreatedAt    time.Time  `json:"created_at"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
}

type Order struct {
	ID              int       `json:"id"`
	TableNumber     int       `json:"table_number"`
	SessionID       string    `json:"session_id"`
	Status          string    `json:"status"`
	PaymentMethod   string    `json:"payment_method"`
	PaymentStatus   string    `json:"payment_status"`
	TotalAmount     float64   `json:"total_amount"`
	ServedBy        string    `json:"served_by,omitempty"`
	Note            string    `json:"note,omitempty"`
	RefundRequested bool      `json:"refund_requested,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	Items           []OrderItem `json:"items,omitempty"`
}

const (
	OrderStatusPending   = "pending"
	OrderStatusPreparing = "preparing"
	// OrderStatusReady means the kitchen/bar has finished preparing it —
	// it's done from their side and drops out of *their* department-filtered
	// feed, but is deliberately still "active" for the generic staff/
	// waitstaff bucket (which sees pending+preparing+ready, unlike bar/
	// kitchen which only see pending+preparing): that's the "pop up to the
	// waiter" handoff — a waiter needs to know it's ready to go deliver it.
	OrderStatusReady = "ready"
	// OrderStatusServed means a waiter actually delivered it to the table —
	// this is the true terminal status, excluded from every live feed.
	OrderStatusServed    = "served"
	OrderStatusCompleted = "completed"
	// OrderStatusCancelled is a separate terminal branch, not part of
	// OrderStatusFlow's forward progression — reachable from pending or
	// preparing only via the dedicated cancel endpoint (db.CancelOrder),
	// never through the generic status-update endpoint, since cancelling
	// also has to atomically restore stock.
	OrderStatusCancelled = "cancelled"
)

const (
	PaymentStatusUnpaid = "unpaid"
	PaymentStatusPaid   = "paid"
)

const (
	PaymentMethodCash = "cash"
	// PaymentMethodCard has no real processor behind it (no payment
	// gateway exists in this app) — matching the guest UI's own "Demo"
	// label on the card option, selecting it just marks the order paid
	// immediately at creation (simulating an instant charge) instead of
	// collected in person like cash.
	PaymentMethodCard = "card"
)

const (
	SongRequestStatusPending  = "pending"
	SongRequestStatusAccepted = "accepted"
	SongRequestStatusRejected = "rejected"
)

// OrderStatusFlow defines the valid forward progression of order lifecycle states.
var OrderStatusFlow = []string{OrderStatusPending, OrderStatusPreparing, OrderStatusReady, OrderStatusServed, OrderStatusCompleted}

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