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
	`

	_, err = DB.Exec(query)
	if err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Singleton settings row — defaults DJ requests to enabled so existing
	// venues already using the feature don't lose it silently on upgrade.
	DB.Exec(`INSERT OR IGNORE INTO settings (id, dj_requests_enabled) VALUES (1, 1)`)

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

	for _, m := range []struct{ column, definition string }{
		{"department", "TEXT DEFAULT 'staff'"},
		{"is_active", "INTEGER DEFAULT 1"},
		{"created_at", "DATETIME DEFAULT CURRENT_TIMESTAMP"},
		{"last_seen_at", "DATETIME"},
	} {
		if err := addColumnIfMissing("users", m.column, m.definition); err != nil {
			log.Fatalf("Failed to migrate users.%s: %v", m.column, err)
		}
	}

	// Seed Table 1 if it doesn't exist
	DB.Exec(`INSERT OR IGNORE INTO tables (number, status, host_session_id) VALUES (1, 'available', '')`)

	// Seed mock menu items
	var count int
	DB.QueryRow("SELECT COUNT(*) FROM products").Scan(&count)
	if count == 0 {
		DB.Exec(`INSERT INTO products (name, price, category) VALUES
			('Classic Mojito', 8.50, 'Drinks'),
			('Heineken 0.5L', 5.00, 'Drinks'),
			('Cheeseburger & Fries', 12.00, 'Food'),
			('Club Sandwich', 9.00, 'Food')`)
	}

	seedDefaultUsers()

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
		if _, err := DB.Exec("INSERT INTO users (username, password_hash, role, department) VALUES (?, ?, ?, ?)", u.username, hash, u.role, u.department); err != nil {
			log.Fatalf("Failed to seed user %s: %v", u.username, err)
		}
	}

	log.Println("⚠️  Seeded default accounts (admin/admin123, manager/manager123, waiter/waiter123, bar/bar123, kitchen/kitchen123, dj/dj123) — change these passwords before deploying")
}

const userColumns = "id, username, password_hash, role, department, is_active, created_at, last_seen_at"

func scanUser(u *models.User, scan func(...interface{}) error) error {
	var lastSeen sql.NullTime
	if err := scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Department, &u.IsActive, &u.CreatedAt, &lastSeen); err != nil {
		return err
	}
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
	err := DB.QueryRow(`
		SELECT u.id, u.username, u.password_hash, u.role, u.department, u.is_active, u.created_at, u.last_seen_at, s.created_at
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.session_id = ?`, sessionID).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Department, &u.IsActive, &u.CreatedAt, &lastSeen, &createdAt)
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

// TouchLastSeen records that a user was just active — called from
// RequireRole on every authenticated request, driving the staff panel's
// "Active now / Active Xh ago" display.
func TouchLastSeen(userID int) error {
	_, err := DB.Exec("UPDATE users SET last_seen_at = CURRENT_TIMESTAMP WHERE id = ?", userID)
	return err
}

// GetAllUsers returns every staff account, newest first, for the admin
// staff & access panel.
func GetAllUsers() ([]models.User, error) {
	rows, err := DB.Query("SELECT " + userColumns + " FROM users ORDER BY created_at DESC")
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

