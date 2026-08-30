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
	// Category is a real, admin-managed category (see Category below) —
	// there used to also be a free-text Subcategory field nested under a
	// handful of fixed top-level categories (Food/Coffee & Soft/Beer &
	// Spirits/Cocktails/Wine), but a generic "Food" bucket holding 50+ items
	// with no real navigation was exactly the bad menu UX admins kept
	// running into. Categories are flat now — what used to be a subcategory
	// (e.g. "Brunch", "Red Wine") *is* the category — and an admin can add
	// new ones by name instead of being stuck with the fixed set (see
	// db.CreateCategory). The subcategory column still exists in the DB
	// (never dropped, see addColumnIfMissing) but nothing writes to it
	// anymore; db.go's one-time migration folded every existing value into
	// Category already.
	Category string `json:"category"`
	// Description is optional guest-facing menu copy shown under the item
	// name (e.g. "Grilled octopus, fava cream, cuttlefish ink..."). Blank
	// means no description line is rendered.
	Description string `json:"description"`
}

// CategoryOther is the one fallback category always guaranteed to exist
// (seeded by db.go) — used when a product's category is blank or no longer
// valid (e.g. legacy data from before categories existed), never a normal
// choice an admin picks deliberately.
const CategoryOther = "Other"

// Category is an admin-defined menu category (see db.CreateCategory) — a
// venue names its own, there's no fixed list. Department decides which
// worker order-feed a product in this category routes to: "kitchen" or
// "bar" (see DepartmentBar/DepartmentKitchen and
// db.CategoryNamesForDepartment). Waiter/superworker/DJ order feeds aren't
// category-filtered at all, so Department only ever holds those two values.
type Category struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Department string `json:"department"`
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
	// Extras are just the chosen ingredients' names — any per-extra price is
	// already folded into Price above at add-time (and re-derived live off
	// the current catalog on every cart render, see cart.go's resolveCart),
	// not tracked separately per name here.
	Extras []string `json:"extras,omitempty"`
}

// Ingredient tags a product with something guests can customize.
// "removable" ingredients are included by default and can be excluded per
// order line at no price change. "extra" ones are optional add-ons a guest
// can opt into from the customize modal; Price is what that add-on costs
// (0 is a valid, deliberate "free add-on" price, not "unset" — the guest
// chip just skips showing a "+€x.xx" label in that case). Price is always
// 0 for a removable ingredient; the column exists on both kinds only
// because they share one table.
type Ingredient struct {
	ID        int     `json:"id"`
	ProductID int     `json:"product_id"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	Price     float64 `json:"price"`
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
	// DepartmentBar sees pending/preparing orders for whichever categories
	// are tagged Department: "bar" (see Category, db.CategoryNamesForDepartment)
	// only — once marked ready it's handed off to the waiter and drops from
	// bar's feed.
	DepartmentBar = "bar"
	// DepartmentKitchen sees pending/preparing orders for "kitchen"-tagged
	// categories only, same ready-handoff rule as bar.
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