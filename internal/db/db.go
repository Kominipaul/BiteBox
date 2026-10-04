package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"bitebox/internal/auth"
	"bitebox/internal/models"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

func InitDB() {
	var err error
	// _journal_mode=WAL + _busy_timeout matter a lot here: database/sql's
	// connection pool opens several concurrent connections to the same
	// SQLite file by default, and this app now does frequent background
	// writes (every websocket broadcast re-queries, plus a 5s liveness
	// check per open staff socket). SQLite's default rollback-journal mode
	// takes an exclusive lock per write and fsyncs synchronously on every
	// transaction — with no busy_timeout, concurrent access either stalls
	// or fails outright ("database is locked"), and the fsync pattern is
	// especially slow under WSL2's virtualized filesystem. WAL mode allows
	// concurrent readers alongside a single writer and checkpoints instead
	// of fsyncing every write, which is the actual fix for "menu takes
	// forever to load" on an app this small — it's lock contention, not
	// genuine query cost.
	DB, err = sql.Open("sqlite3", "./bitebox.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("Failed to connect to SQLite: %v", err)
	}

	query := `
	CREATE TABLE IF NOT EXISTS tables (
		number INTEGER PRIMARY KEY,
		status TEXT DEFAULT 'available',
		host_session_id TEXT DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS products (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		price REAL NOT NULL,
		stock INTEGER DEFAULT -1,
		is_available BOOLEAN DEFAULT 1,
		category TEXT DEFAULT 'Other'
	);

	CREATE TABLE IF NOT EXISTS orders (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		table_number INTEGER NOT NULL,
		session_id TEXT NOT NULL,
		status TEXT DEFAULT 'pending',
		payment_method TEXT NOT NULL,
		total_amount REAL NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS song_requests (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		song_name TEXT NOT NULL,
		tip_amount REAL NOT NULL,
		status TEXT DEFAULT 'pending'
	);

	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		role TEXT NOT NULL,
		department TEXT DEFAULT 'staff',
		is_active INTEGER DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_seen_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS auth_sessions (
		session_id TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id)
	);

	CREATE TABLE IF NOT EXISTS order_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		order_id INTEGER NOT NULL,
		product_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		price REAL NOT NULL,
		quantity INTEGER NOT NULL,
		FOREIGN KEY (order_id) REFERENCES orders(id)
	);

	CREATE TABLE IF NOT EXISTS product_ingredients (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		product_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		kind TEXT DEFAULT 'removable',
		FOREIGN KEY (product_id) REFERENCES products(id)
	);

	CREATE TABLE IF NOT EXISTS settings (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		dj_requests_enabled INTEGER DEFAULT 1
	);

	-- Admin-managed menu categories (see models.Category) — replaced a fixed
	-- Food/Coffee & Soft/Beer & Spirits/Cocktails/Wine list plus a free-text
	-- Subcategory field with one flat, venue-editable set. department is
	-- "kitchen" or "bar": which worker order-feed a product in this category
	-- routes to (see CategoryNamesForDepartment).
	CREATE TABLE IF NOT EXISTS categories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		department TEXT NOT NULL DEFAULT 'bar'
	);

	-- Append-only trail of admin/staff-management actions (account create/
	-- update/deactivate/reactivate/password-reset) — actor is the acting
	-- account's username at the time of the action (not a foreign key: an
	-- account can later be renamed or deleted, and the log should still read
	-- correctly), target is whoever the action was performed on.
	CREATE TABLE IF NOT EXISTS audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		actor TEXT NOT NULL,
		action TEXT NOT NULL,
		target TEXT NOT NULL,
		detail TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- One row per device a staff member has enabled real phone notifications
	-- on (see handlers.PushSubscribe) — a lock-screen-visible alert for a
	-- new order/ready-for-pickup, on top of (not instead of) the dashboard's
	-- own in-page banner. endpoint is unique per browser+device install, so
	-- re-subscribing the same device just refreshes its row instead of
	-- piling up duplicates.
	CREATE TABLE IF NOT EXISTS push_subscriptions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		endpoint TEXT NOT NULL UNIQUE,
		p256dh TEXT NOT NULL,
		auth TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id)
	);

	-- The guest-side counterpart to push_subscriptions above. A guest is
	-- never an authenticated user, so this keys off the same anonymous
	-- bitebox_session cookie value every other guest action already keys
	-- off (see handlers.GuestSessionCookieName / resolveHostedTable) rather
	-- than a user_id — "who gets this device's alerts" is "whichever guest
	-- session subscribed it," full stop, no FK to users. Same upsert-on-
	-- endpoint intent as the staff table (see handlers.GuestPushSubscribe).
	CREATE TABLE IF NOT EXISTS guest_push_subscriptions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		endpoint TEXT NOT NULL UNIQUE,
		p256dh TEXT NOT NULL,
		auth TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Append-only "call waiter" trail. tables.assistance_requested is just a
	-- live flag with no memory of who cleared it or how long it sat (see
	-- db.ClearAssistance / db.CancelAssistance) — this is that memory. One row
	-- per answered or guest-cancelled call (outcome), written in the same
	-- transaction that clears the flag; a table released (guest left) while a
	-- request was still pending logs nothing here.
	CREATE TABLE IF NOT EXISTS assistance_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		table_number INTEGER NOT NULL,
		requested_at DATETIME NOT NULL,
		acknowledged_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		acknowledged_by TEXT NOT NULL,
		wait_seconds INTEGER NOT NULL
	);
	`

	_, err = DB.Exec(query)
	if err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Singleton settings row — defaults DJ requests to enabled so existing
	// venues already using the feature don't lose it silently on upgrade.
	DB.Exec(`INSERT OR IGNORE INTO settings (id, dj_requests_enabled) VALUES (1, 1)`)

	// venue_name defaults to EGO — BiteBox's first real client (an all-day
	// bar in Kalamata) — so a fresh install comes up already branded for
	// them instead of a placeholder name; an admin can still rename it
	// anytime from the dashboard.
	if err := addColumnIfMissing("settings", "venue_name", "TEXT DEFAULT 'EGO'"); err != nil {
		log.Fatalf("Failed to migrate settings.venue_name: %v", err)
	}

	// allow_cash_orders lets a venue go card/digital-only at checkout —
	// defaults to 1 (on) so local dev and every existing venue's cash flow
	// keep working unchanged on upgrade; an admin has to deliberately turn
	// it off. Card is never toggleable — see models.Settings' doc comment.
	if err := addColumnIfMissing("settings", "allow_cash_orders", "INTEGER DEFAULT 1"); err != nil {
		log.Fatalf("Failed to migrate settings.allow_cash_orders: %v", err)
	}

	// business_vat_number/tax_office (ΑΦΜ/ΔΟΥ) back the demo Greek-receipt
	// printout (see models.Order.VATBreakdown, templates/_receipt.html) —
	// defaults are deliberately, obviously fake placeholders, never a real
	// tax registration, and nothing here ever talks to myDATA/ΑΑΔΕ or any
	// other government system. An admin can edit both from the dashboard;
	// still demo values either way unless someone puts real ones in, which
	// this app makes no attempt to enforce or validate.
	if err := addColumnIfMissing("settings", "business_vat_number", "TEXT DEFAULT 'EL000000000 (DEMO)'"); err != nil {
		log.Fatalf("Failed to migrate settings.business_vat_number: %v", err)
	}
	if err := addColumnIfMissing("settings", "tax_office", "TEXT DEFAULT 'Δ.Ο.Υ. ΔΗΜΟ (DEMO)'"); err != nil {
		log.Fatalf("Failed to migrate settings.tax_office: %v", err)
	}

	// products.vat_rate / order_items.vat_rate: see models.Product.VATRate
	// and models.OrderItem.VATRate — 24 (Greece's standard rate) is the
	// default for any product that predates this column or never had one
	// explicitly set.
	if err := addColumnIfMissing("products", "vat_rate", "REAL DEFAULT 24"); err != nil {
		log.Fatalf("Failed to migrate products.vat_rate: %v", err)
	}
	if err := addColumnIfMissing("order_items", "vat_rate", "REAL DEFAULT 24"); err != nil {
		log.Fatalf("Failed to migrate order_items.vat_rate: %v", err)
	}

	// bitebox.db may already exist from before payment_status was introduced;
	// CREATE TABLE IF NOT EXISTS won't add columns to an existing table, and
	// SQLite has no ADD COLUMN IF NOT EXISTS, so check first via PRAGMA.
	if err := addColumnIfMissing("orders", "payment_status", "TEXT DEFAULT 'unpaid'"); err != nil {
		log.Fatalf("Failed to migrate orders.payment_status: %v", err)
	}

	// Tracks which staff member marked an order served, for the admin
	// order-history attribution.
	if err := addColumnIfMissing("orders", "served_by", "TEXT DEFAULT ''"); err != nil {
		log.Fatalf("Failed to migrate orders.served_by: %v", err)
	}

	if err := addColumnIfMissing("orders", "note", "TEXT DEFAULT ''"); err != nil {
		log.Fatalf("Failed to migrate orders.note: %v", err)
	}

	// Set when a guest requests a refund on an already-paid order they
	// can't self-cancel (see GuestCancelOrder) — surfaces to staff, who
	// handle the actual cash refund in person.
	if err := addColumnIfMissing("orders", "refund_requested", "INTEGER DEFAULT 0"); err != nil {
		log.Fatalf("Failed to migrate orders.refund_requested: %v", err)
	}

	// JSON-encoded list of ingredient names excluded from this line, e.g.
	// '["Onion","Pickles"]', or '' for none.
	if err := addColumnIfMissing("order_items", "excluded_ingredients", "TEXT DEFAULT ''"); err != nil {
		log.Fatalf("Failed to migrate order_items.excluded_ingredients: %v", err)
	}

	// Same JSON-array convention, for "extra" ingredients added to this line.
	if err := addColumnIfMissing("order_items", "extra_ingredients", "TEXT DEFAULT ''"); err != nil {
		log.Fatalf("Failed to migrate order_items.extra_ingredients: %v", err)
	}

	// song_requests predates linking a request back to the table that sent
	// it; needed so the DJ's accept/reject decision can be pushed live to
	// the right guest.
	if err := addColumnIfMissing("song_requests", "table_number", "INTEGER"); err != nil {
		log.Fatalf("Failed to migrate song_requests.table_number: %v", err)
	}

	if err := addColumnIfMissing("products", "category", "TEXT DEFAULT 'Other'"); err != nil {
		log.Fatalf("Failed to migrate products.category: %v", err)
	}

	// subcategory is retired (see models.Product's Category doc comment) —
	// kept only so this addColumnIfMissing check still finds it on an
	// install that predates it, since the one-time migration a few lines
	// down reads it once more before nothing ever touches it again.
	// Description (guest-facing menu copy) is unrelated and still live —
	// blank by default, purely additive, nothing depends on it being set.
	if err := addColumnIfMissing("products", "subcategory", "TEXT DEFAULT ''"); err != nil {
		log.Fatalf("Failed to migrate products.subcategory: %v", err)
	}
	if err := addColumnIfMissing("products", "description", "TEXT DEFAULT ''"); err != nil {
		log.Fatalf("Failed to migrate products.description: %v", err)
	}

	// Categories used to be a fixed list (Food/Coffee & Soft/Beer &
	// Spirits/Cocktails/Wine) with a free-text Subcategory nested under
	// each; every seeded item already had a specific Subcategory (e.g.
	// "Brunch", "Red Wine"), so it becomes the new flat Category outright —
	// a plain UPDATE, safe to run every startup: once category already
	// equals subcategory there's nothing left to change. A fresh install
	// never touches this at all — seedEGOMenu below writes the flat
	// category directly and leaves subcategory blank.
	if _, err := DB.Exec(`UPDATE products SET category = subcategory WHERE subcategory != '' AND subcategory != category`); err != nil {
		log.Fatalf("Failed to migrate products.category from subcategory: %v", err)
	}

	var categoryCount int
	DB.QueryRow("SELECT COUNT(*) FROM categories").Scan(&categoryCount)
	if categoryCount == 0 {
		seedDefaultCategories()
	}

	// What a guest pays extra for adding this ingredient (0 for a plain
	// "removable" tag, which was the only kind that existed before this
	// column — those rows keep the REAL DEFAULT 0 untouched). 0 is also a
	// valid price for an "extra" someone wants offered at no charge; the
	// guest-facing chip only hides the "+€x.xx" label for that case, it
	// doesn't stop the ingredient from being addable.
	if err := addColumnIfMissing("product_ingredients", "price", "REAL DEFAULT 0"); err != nil {
		log.Fatalf("Failed to migrate product_ingredients.price: %v", err)
	}

	for _, m := range []struct{ column, definition string }{
		{"department", "TEXT DEFAULT 'staff'"},
		{"is_active", "INTEGER DEFAULT 1"},
		{"created_at", "DATETIME DEFAULT CURRENT_TIMESTAMP"},
		{"last_seen_at", "DATETIME"},
		// Forces a redirect to /change-password (see RequireRole) — set on
		// the seeded default accounts only (seedDefaultUsers, and the
		// one-time backfill below for a database that was already seeded
		// before this column existed), never on an admin-created or
		// admin-reset account, whose password nobody else has seen.
		{"must_change_password", "INTEGER DEFAULT 0"},
		// pages: custom back-office access (models.User.Can); NULL = the
		// role's defaults.
		{"pages", "TEXT DEFAULT NULL"},
		// Two-step login (see twofactor.go): the authenticator secret, sealed
		// with auth.Seal; one being set up; the last code step used.
		{"totp_secret", "TEXT DEFAULT ''"},
		{"totp_pending", "TEXT DEFAULT ''"},
		{"totp_last_step", "INTEGER DEFAULT 0"},
	} {
		if err := addColumnIfMissing("users", m.column, m.definition); err != nil {
			log.Fatalf("Failed to migrate users.%s: %v", m.column, err)
		}
	}
	backfillMustChangePasswordForKnownDefaults()

	// last_activity_at drives auto-release of an abandoned table (see
	// sweepIdleTables) — touched on every request that resolves a hosted
	// table (claim, revisit, cart mutation, checkout, DJ request), not just
	// on claim, so an actively-ordering table never gets swept out from
	// under a real guest.
	if err := addColumnIfMissing("tables", "last_activity_at", "DATETIME"); err != nil {
		log.Fatalf("Failed to migrate tables.last_activity_at: %v", err)
	}
	// assistance_requested drives the guest-facing "Call waiter" button —
	// set when a guest asks for help, cleared when staff acknowledges it
	// (see AllowTableClaim's sibling, the assistance handlers in table.go).
	if err := addColumnIfMissing("tables", "assistance_requested", "INTEGER DEFAULT 0"); err != nil {
		log.Fatalf("Failed to migrate tables.assistance_requested: %v", err)
	}
	if err := addColumnIfMissing("tables", "assistance_requested_at", "DATETIME"); err != nil {
		log.Fatalf("Failed to migrate tables.assistance_requested_at: %v", err)
	}
	// Why the guest called (models.AssistReasons key), and on the log whether
	// staff answered it or the guest cancelled it first.
	if err := addColumnIfMissing("tables", "assistance_reason", "TEXT DEFAULT ''"); err != nil {
		log.Fatalf("Failed to migrate tables.assistance_reason: %v", err)
	}
	if err := addColumnIfMissing("assistance_log", "reason", "TEXT DEFAULT ''"); err != nil {
		log.Fatalf("Failed to migrate assistance_log.reason: %v", err)
	}
	if err := addColumnIfMissing("assistance_log", "outcome", "TEXT DEFAULT 'answered'"); err != nil {
		log.Fatalf("Failed to migrate assistance_log.outcome: %v", err)
	}
	// Menus & hours: scheduled menus, the kitchen/bar close switches, and the
	// guest menu's Recommended / Popular rows (see models.Schedule).
	if _, err := DB.Exec(`CREATE TABLE IF NOT EXISTS service_menus (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		days TEXT NOT NULL DEFAULT '1234567',
		start_min INTEGER NOT NULL,
		end_min INTEGER NOT NULL,
		last_orders INTEGER NOT NULL DEFAULT 0,
		everything INTEGER NOT NULL DEFAULT 0,
		categories TEXT NOT NULL DEFAULT '[]',
		products TEXT NOT NULL DEFAULT '[]',
		enabled INTEGER NOT NULL DEFAULT 1,
		position INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS popularity (
		product_id INTEGER NOT NULL,
		daypart INTEGER NOT NULL,
		qty INTEGER NOT NULL,
		PRIMARY KEY (product_id, daypart)
	);
	CREATE TABLE IF NOT EXISTS special_days (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		date_from TEXT NOT NULL,
		date_to TEXT NOT NULL,
		note TEXT NOT NULL DEFAULT '',
		closed INTEGER NOT NULL DEFAULT 0,
		hours TEXT NOT NULL DEFAULT '{}'
	);
	CREATE TABLE IF NOT EXISTS area_closures (
		zone TEXT PRIMARY KEY,
		closed_until TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS stations (
		dept TEXT PRIMARY KEY,
		closed_until TEXT NOT NULL DEFAULT ''
	);`); err != nil {
		log.Fatalf("Failed to create menus & hours tables: %v", err)
	}
	for _, c := range []struct{ table, col, def string }{
		{"products", "recommended", "INTEGER DEFAULT 0"},
		{"settings", "show_recommended", "INTEGER DEFAULT 1"},
		{"settings", "show_popular", "INTEGER DEFAULT 1"},
		{"settings", "recommended_title", "TEXT DEFAULT 'Recommended by us'"},
		{"settings", "popular_days", "INTEGER DEFAULT 30"},
		{"settings", "popular_count", "INTEGER DEFAULT 6"},
		{"settings", "popular_scope", "TEXT DEFAULT 'all'"},
		{"service_menus", "zones", "TEXT NOT NULL DEFAULT '[]'"},
		{"service_menus", "kind", "TEXT NOT NULL DEFAULT 'menu'"},
		{"service_menus", "disc_type", "TEXT NOT NULL DEFAULT ''"},
		{"service_menus", "disc_value", "REAL NOT NULL DEFAULT 0"},
		{"stations", "hours_set", "INTEGER NOT NULL DEFAULT 0"},
		{"stations", "open_min", "INTEGER NOT NULL DEFAULT 0"},
		{"stations", "close_min", "INTEGER NOT NULL DEFAULT 0"},
		{"stations", "hours_last", "INTEGER NOT NULL DEFAULT 0"},
		{"stations", "per_day", "INTEGER NOT NULL DEFAULT 0"},
		{"stations", "week", "TEXT NOT NULL DEFAULT ''"},
		{"stations", "busy_until", "TEXT NOT NULL DEFAULT ''"},
		{"stations", "busy_minutes", "INTEGER NOT NULL DEFAULT 0"},
		{"products", "avail_start", "INTEGER DEFAULT -1"},
		{"products", "avail_end", "INTEGER DEFAULT -1"},
		{"settings", "sort_by_popularity", "INTEGER DEFAULT 1"},
		{"settings", "show_upcoming", "INTEGER DEFAULT 1"},
		{"settings", "popularity_ranked_at", "TEXT DEFAULT ''"},
	} {
		if err := addColumnIfMissing(c.table, c.col, c.def); err != nil {
			log.Fatalf("Failed to migrate %s.%s: %v", c.table, c.col, err)
		}
	}

	// tip_amount is separate from total_amount (which stays "sum of line
	// items", untouched, so every existing stock/revenue-per-item
	// computation keeps working unmodified) — added at checkout, optional,
	// defaults to 0 so every pre-existing order reads as "no tip" rather
	// than NULL.
	if err := addColumnIfMissing("orders", "tip_amount", "REAL DEFAULT 0"); err != nil {
		log.Fatalf("Failed to migrate orders.tip_amount: %v", err)
	}
	// split_count is purely informational (how many people to divide the
	// total between on the receipt) — this app has no per-guest payment
	// collection, one host's session pays for the whole table either way.
	if err := addColumnIfMissing("orders", "split_count", "INTEGER DEFAULT 1"); err != nil {
		log.Fatalf("Failed to migrate orders.split_count: %v", err)
	}

	// image_url is optional guest-facing menu art — blank means "no photo,
	// show the placeholder" (see _guest_product_list.html). A plain URL
	// field, not an upload: this app has no file storage/serving
	// infrastructure for guest-supplied binaries, and a venue's own product
	// photos are far more likely to already be hosted somewhere (their own
	// site, a CDN) than to need one built here.
	if err := addColumnIfMissing("products", "image_url", "TEXT DEFAULT ''"); err != nil {
		log.Fatalf("Failed to migrate products.image_url: %v", err)
	}
	// One-time data fixup, not a schema migration: earlier seeds stored the
	// demo Omelette photo (see seed_ego.go's demoBrunchPhoto) as an inline
	// `data:image/svg+xml;base64,...` URI. html/template's contextual
	// auto-escaper silently defangs any non-http(s)/mailto scheme to
	// "#ZgotmplZ" in a `src="{{ .ImageURL }}"` context, so that photo
	// rendered broken everywhere on any database seeded before
	// demoBrunchPhoto moved to a real static file. Idempotent — matches
	// nothing (a no-op UPDATE) once a database has already been fixed or
	// was seeded after the switch.
	if _, err := DB.Exec(`UPDATE products SET image_url = ? WHERE image_url LIKE 'data:image/svg+xml;base64,%'`, demoBrunchPhoto); err != nil {
		log.Fatalf("Failed to backfill legacy data: URI product photos: %v", err)
	}

	// vapid_public_key/vapid_private_key: this server's own Web Push
	// identity (see handlers.InitPush) — generated once on first run and
	// persisted here so every phone that's ever subscribed keeps working
	// across restarts. Blank until InitPush fills them in; never exposed
	// except vapid_public_key, which the browser needs to open a
	// subscription (see handlers.PushVAPIDPublicKey).
	if err := addColumnIfMissing("settings", "vapid_public_key", "TEXT DEFAULT ''"); err != nil {
		log.Fatalf("Failed to migrate settings.vapid_public_key: %v", err)
	}
	if err := addColumnIfMissing("settings", "vapid_private_key", "TEXT DEFAULT ''"); err != nil {
		log.Fatalf("Failed to migrate settings.vapid_private_key: %v", err)
	}

	// starts_at/ends_at are venue-local models.BookingTimeLayout strings, so
	// overlap checks are plain string comparisons (see bookings.go).
	if _, err := DB.Exec(`CREATE TABLE IF NOT EXISTS bookings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		code TEXT NOT NULL UNIQUE,
		table_number INTEGER NOT NULL,
		starts_at TEXT NOT NULL,
		ends_at TEXT NOT NULL,
		guests INTEGER NOT NULL,
		name TEXT NOT NULL,
		phone TEXT DEFAULT '',
		email TEXT DEFAULT '',
		notes TEXT DEFAULT '',
		status TEXT NOT NULL DEFAULT 'confirmed',
		source TEXT NOT NULL DEFAULT 'online',
		created_by TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_bookings_starts_at ON bookings (starts_at);
	-- Orders / Logs pages filter by time and page newest-first (lists.go).
	CREATE INDEX IF NOT EXISTS idx_orders_created_at ON orders (created_at);
	CREATE INDEX IF NOT EXISTS idx_order_items_order ON order_items (order_id);
	CREATE INDEX IF NOT EXISTS idx_assistance_log_ack ON assistance_log (acknowledged_at);
	CREATE INDEX IF NOT EXISTS idx_audit_log_created ON audit_log (created_at);
	-- Live lookups that would otherwise scan every order ever placed: the
	-- order feeds (status), a table's current order, a guest's own orders.
	CREATE INDEX IF NOT EXISTS idx_orders_status ON orders (status);
	CREATE INDEX IF NOT EXISTS idx_orders_table ON orders (table_number);
	CREATE INDEX IF NOT EXISTS idx_orders_session ON orders (session_id);
	CREATE INDEX IF NOT EXISTS idx_auth_sessions_user ON auth_sessions (user_id);
	CREATE INDEX IF NOT EXISTS idx_guest_push_session ON guest_push_subscriptions (session_id);`); err != nil {
		log.Fatalf("Failed to create bookings: %v", err)
	}

	// Floor-plan layout + booking flags per table, and venue booking
	// settings — see models.Table / models.Settings.
	for _, m := range []struct{ table, column, definition string }{
		{"tables", "zone", "TEXT DEFAULT 'Main'"},
		{"tables", "shape", "TEXT DEFAULT 'round'"},
		{"tables", "min_guests", "INTEGER DEFAULT 1"},
		{"tables", "max_guests", "INTEGER DEFAULT 4"},
		{"tables", "pos_x", "REAL DEFAULT -1"},
		{"tables", "pos_y", "REAL DEFAULT -1"},
		{"tables", "bookable", "INTEGER DEFAULT 1"},
		{"tables", "out_of_service", "INTEGER DEFAULT 0"},
		{"tables", "label", "TEXT DEFAULT ''"},
		{"tables", "fee", "REAL DEFAULT 0"},
		{"tables", "min_spend", "REAL DEFAULT 0"},
		{"tables", "duration", "INTEGER DEFAULT 0"},
		{"tables", "avail_from", "TEXT DEFAULT ''"},
		{"tables", "avail_until", "TEXT DEFAULT ''"},
		{"tables", "full_day", "INTEGER DEFAULT 0"},
		{"tables", "qr_token", "TEXT DEFAULT ''"},
		{"bookings", "hold_token", "TEXT DEFAULT ''"},
		{"bookings", "hold_expires", "TEXT DEFAULT ''"},
		{"settings", "booking_enabled", "INTEGER DEFAULT 1"},
		{"settings", "booking_open", "TEXT DEFAULT '12:00'"},
		{"settings", "booking_close", "TEXT DEFAULT '22:00'"},
		{"settings", "booking_duration", "INTEGER DEFAULT 120"},
		{"settings", "booking_days_ahead", "INTEGER DEFAULT 14"},
		{"settings", "venue_phone", "TEXT DEFAULT ''"},
		{"settings", "theme", "TEXT DEFAULT ''"},
	} {
		if err := addColumnIfMissing(m.table, m.column, m.definition); err != nil {
			log.Fatalf("Failed to migrate %s.%s: %v", m.table, m.column, err)
		}
	}
	DB.Exec("CREATE INDEX IF NOT EXISTS idx_bookings_hold ON bookings (hold_token)")

	// Seed Table 1 if it doesn't exist
	DB.Exec(`INSERT OR IGNORE INTO tables (number, status, host_session_id, qr_token) VALUES (1, 'available', '', ?)`, NewTableToken())

	// Seed EGO's real menu (first client — see seed_ego.go) on a fresh database.
	var count int
	DB.QueryRow("SELECT COUNT(*) FROM products").Scan(&count)
	if count == 0 {
		seedEGOMenu()
	}

	seedDefaultUsers()

	// A floor with nothing placed on the map (fresh install, or tables that
	// predate the floor plan) gets EGO's layout so /book works out of the box.
	var placed int
	DB.QueryRow("SELECT COUNT(*) FROM tables WHERE pos_x >= 0 AND pos_y >= 0").Scan(&placed)
	if placed == 0 {
		if err := ApplyEGOFloor(); err != nil {
			log.Printf("Failed to load the EGO floor layout: %v", err)
		}
	}

	if err := ensureTableTokens(); err != nil {
		log.Fatalf("Failed to give tables QR tokens: %v", err)
	}
	DB.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_tables_qr_token ON tables (qr_token) WHERE qr_token != ''")

	log.Println("⚡ [SQLite] Database initialized and seeded successfully")
}

// addColumnIfMissing adds a column to an existing table only if it isn't
// already present, since SQLite has no ADD COLUMN IF NOT EXISTS clause.
// table/column/definition are always internal literals, never user input.
func addColumnIfMissing(table, column, definition string) error {
	rows, err := DB.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}

	_, err = DB.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	return err
}

func seedDefaultUsers() {
	var count int
	DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if count > 0 {
		return
	}

	// One seed account per department, so every role in the system has a
	// working login to test with out of the box.
	seed := []struct {
		username, password, role, department string
	}{
		{"admin", "admin123", models.RoleAdmin, models.DepartmentSuperworker},
		{"manager", "manager123", models.RoleWorker, models.DepartmentSuperworker},
		{"waiter", "waiter123", models.RoleWorker, models.DepartmentWaiter},
		{"bar", "bar123", models.RoleWorker, models.DepartmentBar},
		{"kitchen", "kitchen123", models.RoleWorker, models.DepartmentKitchen},
		{"dj", "dj123", models.RoleWorker, models.DepartmentDJ},
	}

	for _, u := range seed {
		hash, err := auth.HashPassword(u.password)
		if err != nil {
			log.Fatalf("Failed to hash password for seed user %s: %v", u.username, err)
		}
		// must_change_password = 1: these passwords are published in this
		// app's own source (right above), not a secret — see
		// models.User.MustChangePassword's own doc comment.
		if _, err := DB.Exec("INSERT INTO users (username, password_hash, role, department, must_change_password) VALUES (?, ?, ?, ?, 1)", u.username, hash, u.role, u.department); err != nil {
			log.Fatalf("Failed to seed user %s: %v", u.username, err)
		}
	}

	log.Println("⚠️  Seeded default accounts (admin/admin123, manager/manager123, waiter/waiter123, bar/bar123, kitchen/kitchen123, dj/dj123) — must change password on first login")
}

// defaultSeedPasswords is seedDefaultUsers' own username→password list,
// factored out so backfillMustChangePasswordForKnownDefaults can check
// against it without duplicating the list or needing seedDefaultUsers to
// have actually run this time (a database from before must_change_password
// existed already has these users; this is what catches that case).
var defaultSeedPasswords = map[string]string{
	"admin": "admin123", "manager": "manager123", "waiter": "waiter123",
	"bar": "bar123", "kitchen": "kitchen123", "dj": "dj123",
}

// backfillMustChangePasswordForKnownDefaults retrofits must_change_password
// for a database that was already seeded before that column existed (a
// fresh install gets it set at INSERT time in seedDefaultUsers instead, and
// this is a no-op there — none of these usernames exist yet). Runs on
// every startup, not just once: idempotent (re-setting 1 to 1 does
// nothing) and cheap (bcrypt-verifying a handful of rows against a fixed
// password is imperceptible at server-start scale), and it's the only way
// to correctly leave alone an account where the admin already changed the
// password away from the published default — comparing password *hashes*
// directly isn't possible (bcrypt salts each one differently), so this
// verifies the real password the same way login does.
func backfillMustChangePasswordForKnownDefaults() {
	for username, password := range defaultSeedPasswords {
		user, err := GetUserByUsername(username)
		if err != nil {
			continue // no such account — nothing to backfill
		}
		if user.MustChangePassword || !auth.CheckPassword(user.PasswordHash, password) {
			continue // already flagged, or the admin already changed it away from the default
		}
		if _, err := DB.Exec("UPDATE users SET must_change_password = 1 WHERE id = ?", user.ID); err != nil {
			log.Printf("backfill: failed to flag %q for a forced password change: %v", username, err)
		}
	}
}

const userColumns = "id, username, password_hash, role, department, is_active, created_at, last_seen_at, must_change_password, pages, totp_secret != ''"

func scanUser(u *models.User, scan func(...interface{}) error) error {
	var lastSeen sql.NullTime
	var pages sql.NullString
	if err := scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Department, &u.IsActive, &u.CreatedAt, &lastSeen, &u.MustChangePassword, &pages, &u.TwoFactor); err != nil {
		return err
	}
	setPages(u, pages)
	if lastSeen.Valid {
		t := lastSeen.Time
		u.LastSeenAt = &t
	}
	return nil
}

func GetUserByUsername(username string) (models.User, error) {
	var u models.User
	row := DB.QueryRow("SELECT "+userColumns+" FROM users WHERE username = ?", username)
	err := scanUser(&u, row.Scan)
	return u, err
}

func GetUserByID(id int) (models.User, error) {
	var u models.User
	row := DB.QueryRow("SELECT "+userColumns+" FROM users WHERE id = ?", id)
	err := scanUser(&u, row.Scan)
	return u, err
}

func CreateAuthSession(sessionID string, userID int) error {
	_, err := DB.Exec("INSERT INTO auth_sessions (session_id, user_id) VALUES (?, ?)", sessionID, userID)
	return err
}

func DeleteAuthSession(sessionID string) error {
	_, err := DB.Exec("DELETE FROM auth_sessions WHERE session_id = ?", sessionID)
	return err
}

// DeleteAuthSessionsForUser drops every active session for a user, used to
// cut off access immediately when an admin deactivates their account.
func DeleteAuthSessionsForUser(userID int) error {
	_, err := DB.Exec("DELETE FROM auth_sessions WHERE user_id = ?", userID)
	return err
}

// GetUserBySessionID resolves an auth session to its user, rejecting sessions
// older than 24h or belonging to a deactivated account (the latter is a
// belt-and-suspenders check: deactivating also deletes the user's sessions
// outright, but this catches anything left over).
func GetUserBySessionID(sessionID string) (models.User, error) {
	var u models.User
	var createdAt time.Time
	var lastSeen sql.NullTime
	var pages sql.NullString
	err := DB.QueryRow(`
		SELECT u.id, u.username, u.password_hash, u.role, u.department, u.is_active, u.created_at, u.last_seen_at, u.must_change_password, u.pages, u.totp_secret != '', s.created_at
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.session_id = ?`, sessionID).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Department, &u.IsActive, &u.CreatedAt, &lastSeen, &u.MustChangePassword, &pages, &u.TwoFactor, &createdAt)
	setPages(&u, pages)
	if err != nil {
		return u, err
	}
	if lastSeen.Valid {
		t := lastSeen.Time
		u.LastSeenAt = &t
	}
	if !u.IsActive {
		DeleteAuthSession(sessionID)
		return u, sql.ErrNoRows
	}
	if time.Since(createdAt) > 24*time.Hour {
		DeleteAuthSession(sessionID)
		return u, sql.ErrNoRows
	}
	return u, nil
}

// Maintain drops rows that can never be used again and refreshes SQLite's
// query-planner statistics. Run at startup and then daily (cmd/server), so
// the database only grows by real business records — orders, bookings and
// the two logs, which are kept for good (a few MB a year at venue scale);
// bookings are deleted a year after the visit.
func Maintain() {
	for _, q := range []string{
		// Sessions expire after 24h (GetUserBySessionID); every login adds one.
		"DELETE FROM auth_sessions WHERE created_at < datetime('now', '-1 day')",
		// A guest's session ends when the table is released.
		"DELETE FROM guest_push_subscriptions WHERE created_at < datetime('now', '-2 days')",
		// GDPR storage limit, promised on /privacy. Nothing reads bookings
		// older than the staff list's 30-day "Past" view, so drop them whole.
		"DELETE FROM bookings WHERE ends_at < datetime('now', '-1 year')",
		"PRAGMA optimize",
	} {
		if _, err := DB.Exec(q); err != nil {
			log.Printf("maintenance: %s: %v", q, err)
		}
	}
}

// TouchLastSeen records that a user was just active — called from
// RequireRole on every authenticated request, driving the staff panel's
// "Active now / Active Xh ago" display.
func TouchLastSeen(userID int) error {
	_, err := DB.Exec("UPDATE users SET last_seen_at = CURRENT_TIMESTAMP WHERE id = ?", userID)
	return err
}

// GetAllUsers returns every staff account, newest first, for the admin
// staff & access panel. ", id DESC" tiebreaks created_at's 1-second SQLite
// resolution — see GetOrderHistory's own doc comment (orders.go) for why
// this matters wherever multiple rows can share a timestamp.
func GetAllUsers() ([]models.User, error) {
	rows, err := DB.Query("SELECT " + userColumns + " FROM users ORDER BY created_at DESC, id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		var u models.User
		if err := scanUser(&u, rows.Scan); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

// ErrDuplicateUsername is returned by CreateUser when the username is
// already taken.
var ErrDuplicateUsername = errors.New("username already exists")

func CreateUser(username, passwordHash, role, department string) (int, error) {
	res, err := DB.Exec(
		"INSERT INTO users (username, password_hash, role, department) VALUES (?, ?, ?, ?)",
		username, passwordHash, role, department,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, ErrDuplicateUsername
		}
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

// SetUserActive flips a staff account's active flag. Deactivating does not
// delete the account or its history — callers should also call
// DeleteAuthSessionsForUser to cut off any live session immediately.
func SetUserActive(id int, active bool) error {
	_, err := DB.Exec("UPDATE users SET is_active = ? WHERE id = ?", active, id)
	return err
}

// UpdateUserProfile renames a staff account and/or moves it between
// departments — the two things account creation locks in that previously
// had no way to change afterward short of deactivate-and-recreate (which
// loses the account's own identity/history association). role is
// deliberately not editable here: admin vs. worker is a bigger boundary
// than this form exists to cross: promoting a worker to admin should be a
// deliberate "create a new admin account" action, not a field edit.
func UpdateUserProfile(id int, username, department string) error {
	_, err := DB.Exec("UPDATE users SET username = ?, department = ? WHERE id = ?", username, department, id)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrDuplicateUsername
	}
	return err
}

// SetUserPassword overwrites a staff account's password hash directly —
// used both by the admin-side "reset password" action (for a staff member
// who forgot theirs; logged via LogAudit by that caller) and by the
// self-service change-password flow (ChangePasswordSubmit). There's still
// no *forgot-password* recovery (no email system exists in this app) —
// that's a different thing from a logged-in user changing a password they
// already know, which this now also covers. Always clears
// must_change_password: whichever path set a new password, the published
// default this flag exists to move an account off of is no longer in use
// either way.
// RequirePasswordChange makes the account pick a new password at its next
// request (someone else set the current one).
func RequirePasswordChange(id int) error {
	_, err := DB.Exec("UPDATE users SET must_change_password = 1 WHERE id = ?", id)
	return err
}

func SetUserPassword(id int, passwordHash string) error {
	_, err := DB.Exec("UPDATE users SET password_hash = ?, must_change_password = 0 WHERE id = ?", passwordHash, id)
	return err
}

// LogAudit appends one row to the staff-management audit trail. actor is
// the acting account's username at the time of the action (not a foreign
// key — stays readable even if that account is later renamed or removed).
// Errors are deliberately swallowed by callers that treat this as
// best-effort bookkeeping, not a condition that should fail the action it's
// logging (losing an audit row is much cheaper than blocking a legitimate
// deactivate/reset because the log insert hiccuped).
func LogAudit(actor, action, target, detail string) error {
	_, err := DB.Exec("INSERT INTO audit_log (actor, action, target, detail) VALUES (?, ?, ?, ?)", actor, action, target, detail)
	return err
}

// setPages reads users.pages: NULL = the role's default pages, otherwise a
// comma-separated custom list (possibly empty).
func setPages(u *models.User, pages sql.NullString) {
	u.PagesCustom = pages.Valid
	u.Pages = nil
	if pages.Valid && pages.String != "" {
		u.Pages = strings.Split(pages.String, ",")
	}
}

// SetUserPages stores custom page access (custom=false goes back to the
// role's defaults).
func SetUserPages(id int, custom bool, pages []string) error {
	var v interface{}
	if custom {
		v = strings.Join(pages, ",")
	}
	_, err := DB.Exec("UPDATE users SET pages = ? WHERE id = ?", v, id)
	return err
}

// CountAuthSessions is how many devices an account is signed in on (sessions
// expire after 24h, see GetUserBySessionID).
func CountAuthSessions(userID int) int {
	var n int
	DB.QueryRow("SELECT COUNT(*) FROM auth_sessions WHERE user_id = ? AND datetime(created_at) > datetime('now', '-24 hours')", userID).Scan(&n)
	return n
}

// GetAuditForUser is the staff audit trail for one account: things done to
// it and things it did.
func GetAuditForUser(username string, limit int) ([]models.AuditEntry, error) {
	rows, err := DB.Query("SELECT "+auditColumns+" FROM audit_log WHERE target = ? OR actor = ? ORDER BY created_at DESC, id DESC LIMIT ?", username, username, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []models.AuditEntry
	for rows.Next() {
		var e models.AuditEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.Target, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// auditColumns/scanAudit mirror the userColumns/scanUser pattern above.
const auditColumns = "id, actor, action, target, detail, created_at"

// GetRecentAuditLog returns the most recent staff-management actions,
// newest first, for the admin "Staff & access" panel's audit trail.
// ", id DESC" tiebreaks created_at's 1-second SQLite resolution — see
// GetOrderHistory's own doc comment (orders.go).
func GetRecentAuditLog(limit int) ([]models.AuditEntry, error) {
	rows, err := DB.Query("SELECT "+auditColumns+" FROM audit_log ORDER BY created_at DESC, id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []models.AuditEntry
	for rows.Next() {
		var e models.AuditEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.Target, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}
