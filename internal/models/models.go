package models

import (
	"fmt"
	"sort"
	"time"
)

// Settings is a venue-wide singleton row of feature toggles.
type Settings struct {
	DJRequestsEnabled bool
	VenueName         string
	// AllowCashOrders lets a venue turn cash off at checkout — some venues
	// go card/digital-only for cash-handling reasons. Defaults to true
	// (see db.go's migration) so local dev and every existing cash flow
	// keep working unchanged unless an admin deliberately switches it off.
	// Card is never toggleable — it's the one payment method every guest
	// can always fall back to.
	AllowCashOrders bool
	// BusinessVATNumber/TaxOffice (ΑΦΜ/ΔΟΥ) are demo-structure fields for
	// the Greek-receipt-shaped printout (see Order.Receipt) — NOT a real
	// tax registration and NOT connected to myDATA/ΑΑΔΕ in any way. See
	// db.go's migration for the obviously-fake defaults.
	BusinessVATNumber string
	TaxOffice         string
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
	// ImageURL is optional guest-facing menu art — a plain URL, not an
	// upload (this app has no file storage for guest-supplied binaries).
	// Blank means no photo; the guest menu falls back to a placeholder.
	ImageURL string `json:"image_url,omitempty"`
	// VATRate is a percentage (24, 13, or 6 — Greece's standard/reduced/
	// super-reduced rates) used only to build the demo receipt's VAT
	// breakdown table (see Order.VATBreakdown) — display structure, not a
	// real tax calculation feeding any actual filing or myDATA submission.
	VATRate float64 `json:"vat_rate"`
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
	// AssistanceRequested is set by the guest-facing "Call waiter" button
	// and cleared when a staff member acknowledges it (see
	// TableHandlers.RequestAssistance/AcknowledgeAssistance).
	AssistanceRequested bool `json:"assistance_requested"`
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
	// VATRate snapshots the product's VAT rate at order time (same
	// "immutable once ordered" reasoning as Name/Price) — see
	// Product.VATRate's doc comment on what this is and isn't for.
	VATRate float64 `json:"vat_rate,omitempty"`
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
	Items       []OrderItem `json:"items"`
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

// AuditEntry is one row of the append-only staff-management trail (account
// create/update/deactivate/reactivate/password-reset) — see db.LogAudit.
type AuditEntry struct {
	ID        int       `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// AssistanceLogEntry is one row of the "call waiter" acknowledgment trail —
// see db.ClearAssistance. WaitSeconds is computed server-side in SQL at
// acknowledgment time (RequestedAt to AcknowledgedAt), not recomputed here.
type AssistanceLogEntry struct {
	ID             int       `json:"id"`
	TableNumber    int       `json:"table_number"`
	RequestedAt    time.Time `json:"requested_at"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
	AcknowledgedBy string    `json:"acknowledged_by"`
	WaitSeconds    int       `json:"wait_seconds"`
}

// WaitLabel renders WaitSeconds for display — "42s" or "3m 07s" — so
// templates don't need arithmetic helpers to show it.
func (e AssistanceLogEntry) WaitLabel() string {
	if e.WaitSeconds < 60 {
		return fmt.Sprintf("%ds", e.WaitSeconds)
	}
	return fmt.Sprintf("%dm %02ds", e.WaitSeconds/60, e.WaitSeconds%60)
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
	// MustChangePassword forces a redirect to /change-password on the next
	// authenticated request (see RequireRole) before anything else this
	// account can reach. Set true only for the seeded default accounts
	// (admin123 etc. — see seedDefaultUsers) so a fresh install can't stay
	// on a password published in this app's own source forever; an admin
	// creating a new staff account or resetting one themselves picks a
	// password nobody else has seen, so neither of those paths sets it.
	MustChangePassword bool `json:"-"`
}

type Order struct {
	ID            int     `json:"id"`
	TableNumber   int     `json:"table_number"`
	SessionID     string  `json:"session_id"`
	Status        string  `json:"status"`
	PaymentMethod string  `json:"payment_method"`
	PaymentStatus string  `json:"payment_status"`
	TotalAmount   float64 `json:"total_amount"`
	// TipAmount is a separate, optional add-on a guest can leave at
	// checkout — kept apart from TotalAmount (which stays "sum of line
	// items" so every stock/revenue-per-item computation elsewhere is
	// unaffected). GrandTotal() below is what the guest actually pays.
	TipAmount float64 `json:"tip_amount,omitempty"`
	// SplitCount is purely informational — "divide this by N on the
	// receipt." This app has no per-guest payment collection; one host's
	// session always pays for the whole table.
	SplitCount      int         `json:"split_count,omitempty"`
	ServedBy        string      `json:"served_by,omitempty"`
	Note            string      `json:"note,omitempty"`
	RefundRequested bool        `json:"refund_requested,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	Items           []OrderItem `json:"items,omitempty"`
}

// GrandTotal is what the guest actually pays — items plus tip.
func (o Order) GrandTotal() float64 {
	return o.TotalAmount + o.TipAmount
}

// PerPersonTotal divides GrandTotal by SplitCount — informational only,
// display-side (see Order.SplitCount doc comment). SplitCount is always
// >= 1 (the DB column defaults to 1, and checkout never accepts less).
func (o Order) PerPersonTotal() float64 {
	if o.SplitCount <= 1 {
		return o.GrandTotal()
	}
	return o.GrandTotal() / float64(o.SplitCount)
}

// VATLine is one row of a Greek-receipt-style VAT breakdown table — every
// item taxed at the same rate, summed together. A real Greek retail
// receipt (Απόδειξη Λιανικής) itemizes tax by rate category rather than
// one blended tax line, since food/drink items can legitimately carry
// different VAT rates (standard 24%, reduced 13%, super-reduced 6%) on
// the same order.
//
// DEMO STRUCTURE ONLY — see Order.VATBreakdown.
type VATLine struct {
	Rate  float64 // percentage, e.g. 24, 13, 6
	Net   float64 // pre-VAT subtotal at this rate
	VAT   float64 // VAT amount at this rate
	Gross float64 // Net + VAT — what these items actually cost the guest
}

// VATBreakdown groups an order's items by VAT rate — the structural core
// of the demo Greek receipt (see templates/_receipt.html). Item.Price
// throughout this app is already VAT-inclusive (it's just "the price" a
// guest sees and pays), so this backs out the net/VAT split from that
// gross figure using the standard rate-extraction method a VAT-inclusive
// retail price actually uses: net = gross / (1 + rate/100).
//
// This is demo-structure math for a printout, nothing more: it doesn't
// feed any tax filing, isn't validated against real Greek VAT category
// rules for specific goods, and has no connection to myDATA/ΑΑΔΕ.
// o.Items must already be populated (see db.GetOrderItems) — this is a
// pure function over whatever's already loaded, it doesn't query anything
// itself.
func (o Order) VATBreakdown() []VATLine {
	byRate := make(map[float64]*VATLine)
	var rates []float64
	for _, item := range o.Items {
		line, ok := byRate[item.VATRate]
		if !ok {
			line = &VATLine{Rate: item.VATRate}
			byRate[item.VATRate] = line
			rates = append(rates, item.VATRate)
		}
		gross := item.Price * float64(item.Quantity)
		net := gross / (1 + item.VATRate/100)
		line.Gross += gross
		line.Net += net
		line.VAT += gross - net
	}
	sort.Float64s(rates)
	lines := make([]VATLine, len(rates))
	for i, r := range rates {
		lines[i] = *byRate[r]
	}
	return lines
}

// ReceiptNumber formats a sequential-looking receipt number for the demo
// printout — this app's orders.id autoincrement already *is* sequential
// and gapless per venue, which is the actual structural property a real
// Greek receipt number needs; this just formats it to look like one.
func (o Order) ReceiptNumber() string {
	return fmt.Sprintf("%06d", o.ID)
}

// DemoMARK fabricates a myDATA-shaped "unique registration mark" (ΜΑΡΚ) —
// a real one is assigned by myDATA when a receipt is actually transmitted
// there, which this app never does. Deterministic per order (not random)
// so reprinting the same receipt shows the same value, the way a real
// MARK would.
func (o Order) DemoMARK() string {
	return fmt.Sprintf("DEMO-%010d", o.ID)
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
