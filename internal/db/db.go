package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"bitebox/internal/auth"
	"bitebox/internal/models"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

func InitDB() {
	var err error
	DB, err = sql.Open("sqlite3", "./bitebox.db")
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
		is_available BOOLEAN DEFAULT 1
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
		role TEXT NOT NULL
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
	`

	_, err = DB.Exec(query)
	if err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// bitebox.db may already exist from before payment_status was introduced;
	// CREATE TABLE IF NOT EXISTS won't add columns to an existing table, and
	// SQLite has no ADD COLUMN IF NOT EXISTS, so check first via PRAGMA.
	if err := addColumnIfMissing("orders", "payment_status", "TEXT DEFAULT 'unpaid'"); err != nil {
		log.Fatalf("Failed to migrate orders.payment_status: %v", err)
	}

	// song_requests predates linking a request back to the table that sent
	// it; needed so the DJ's accept/reject decision can be pushed live to
	// the right guest.
	if err := addColumnIfMissing("song_requests", "table_number", "INTEGER"); err != nil {
		log.Fatalf("Failed to migrate song_requests.table_number: %v", err)
	}

	// Seed Table 1 if it doesn't exist
	DB.Exec(`INSERT OR IGNORE INTO tables (number, status, host_session_id) VALUES (1, 'available', '')`)

	// Seed mock menu items
	var count int
	DB.QueryRow("SELECT COUNT(*) FROM products").Scan(&count)
	if count == 0 {
		DB.Exec(`INSERT INTO products (name, price) VALUES 
			('Classic Mojito', 8.50),
			('Heineken 0.5L', 5.00),
			('Cheeseburger & Fries', 12.00),
			('Club Sandwich', 9.00)`)
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

	seed := []struct {
		username string
		password string
		role     string
	}{
		{"admin", "admin123", models.RoleAdmin},
		{"staff", "staff123", models.RoleWorker},
	}

	for _, u := range seed {
		hash, err := auth.HashPassword(u.password)
		if err != nil {
			log.Fatalf("Failed to hash password for seed user %s: %v", u.username, err)
		}
		if _, err := DB.Exec("INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)", u.username, hash, u.role); err != nil {
			log.Fatalf("Failed to seed user %s: %v", u.username, err)
		}
	}

	log.Println("⚠️  Seeded default users admin/admin123 and staff/staff123 — change these passwords before deploying")
}

func GetUserByUsername(username string) (models.User, error) {
	var u models.User
	err := DB.QueryRow("SELECT id, username, password_hash, role FROM users WHERE username = ?", username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role)
	return u, err
}

func GetUserByID(id int) (models.User, error) {
	var u models.User
	err := DB.QueryRow("SELECT id, username, password_hash, role FROM users WHERE id = ?", id).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role)
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

// GetUserBySessionID resolves an auth session to its user, rejecting sessions older than 24h.
func GetUserBySessionID(sessionID string) (models.User, error) {
	var u models.User
	var createdAt time.Time
	err := DB.QueryRow(`
		SELECT u.id, u.username, u.password_hash, u.role, s.created_at
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.session_id = ?`, sessionID).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &createdAt)
	if err != nil {
		return u, err
	}
	if time.Since(createdAt) > 24*time.Hour {
		DeleteAuthSession(sessionID)
		return u, sql.ErrNoRows
	}
	return u, nil
}

