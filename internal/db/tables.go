package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"bitebox/internal/models"
)

var ErrTableOccupied = errors.New("table is occupied")

const tableColumns = "number, status, host_session_id, assistance_requested, zone, shape, min_guests, max_guests, pos_x, pos_y, bookable, out_of_service, label, fee, min_spend, duration, avail_from, avail_until, full_day, qr_token"

func scanTable(t *models.Table, scan func(...interface{}) error) error {
	var assistance, bookable, off, fullDay int
	if err := scan(&t.Number, &t.Status, &t.HostSessionID, &assistance,
		&t.Zone, &t.Shape, &t.MinGuests, &t.MaxGuests, &t.PosX, &t.PosY, &bookable, &off,
		&t.Label, &t.Fee, &t.MinSpend, &t.Duration, &t.AvailFrom, &t.AvailUntil, &fullDay, &t.QRToken); err != nil {
		return err
	}
	t.AssistanceRequested = assistance != 0
	t.Bookable = bookable != 0
	t.OutOfService = off != 0
	t.FullDay = fullDay != 0
	return nil
}

// UpdateTableLayout saves a table's floor-plan and booking properties (not
// position or service state).
func UpdateTableLayout(t models.Table) error {
	_, err := DB.Exec(`UPDATE tables SET zone = ?, shape = ?, min_guests = ?, max_guests = ?, bookable = ?,
		label = ?, fee = ?, min_spend = ?, duration = ?, avail_from = ?, avail_until = ?, full_day = ? WHERE number = ?`,
		t.Zone, t.Shape, t.MinGuests, t.MaxGuests, t.Bookable,
		t.Label, t.Fee, t.MinSpend, t.Duration, t.AvailFrom, t.AvailUntil, t.FullDay, t.Number)
	return err
}

// ApplyTableLayout creates the table if missing, then sets its layout and
// position — used by the venue floor-plan preset.
func ApplyTableLayout(t models.Table) error {
	if _, err := DB.Exec("INSERT OR IGNORE INTO tables (number, status, host_session_id, qr_token) VALUES (?, 'available', '', ?)", t.Number, NewTableToken()); err != nil {
		return err
	}
	if err := UpdateTableLayout(t); err != nil {
		return err
	}
	return SetTablePosition(t.Number, t.PosX, t.PosY)
}

func SetTablePosition(number int, x, y float64) error {
	_, err := DB.Exec("UPDATE tables SET pos_x = ?, pos_y = ? WHERE number = ?", x, y, number)
	return err
}

func SetTableOutOfService(number int, off bool) error {
	_, err := DB.Exec("UPDATE tables SET out_of_service = ? WHERE number = ?", off, number)
	return err
}

func GetTable(number int) (models.Table, error) {
	var t models.Table
	err := scanTable(&t, DB.QueryRow("SELECT "+tableColumns+" FROM tables WHERE number = ?", number).Scan)
	return t, err
}

// ClaimTable also stamps last_activity_at — a freshly claimed table is by
// definition not idle (see TouchTableActivity/sweepIdleTables).
func ClaimTable(number int, sessionID string) error {
	_, err := DB.Exec("UPDATE tables SET status = 'occupied', host_session_id = ?, last_activity_at = CURRENT_TIMESTAMP WHERE number = ?", sessionID, number)
	return err
}

// ReleaseTable clears host/occupancy state and any pending assistance
// request — a released table has nobody left to help.
func ReleaseTable(number int) error {
	_, err := DB.Exec("UPDATE tables SET status = 'available', host_session_id = '', assistance_requested = 0, assistance_requested_at = NULL WHERE number = ?", number)
	return err
}

func GetAllTables() ([]models.Table, error) {
	rows, err := DB.Query("SELECT " + tableColumns + " FROM tables ORDER BY number")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []models.Table
	for rows.Next() {
		var t models.Table
		if err := scanTable(&t, rows.Scan); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, nil
}

func CreateTable(number int) error {
	_, err := DB.Exec("INSERT INTO tables (number, status, host_session_id, qr_token) VALUES (?, 'available', '', ?)", number, NewTableToken())
	return err
}

// NewTableToken is the secret in a table's printed QR code (/t/{token}):
// 96 random bits, so tables can't be found by counting like /table/1, /table/2.
func NewTableToken() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// GetTableByToken resolves a scanned QR code to its table.
func GetTableByToken(token string) (models.Table, error) {
	var t models.Table
	if token == "" {
		return t, sql.ErrNoRows
	}
	err := scanTable(&t, DB.QueryRow("SELECT "+tableColumns+" FROM tables WHERE qr_token = ?", token).Scan)
	return t, err
}

// RotateTableToken gives a table a new QR code; the old printed code stops
// working at once.
func RotateTableToken(number int) (string, error) {
	token := NewTableToken()
	res, err := DB.Exec("UPDATE tables SET qr_token = ? WHERE number = ?", token, number)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", sql.ErrNoRows
	}
	return token, nil
}

// ensureTableTokens gives every table that predates QR tokens its own.
func ensureTableTokens() error {
	rows, err := DB.Query("SELECT number FROM tables WHERE qr_token IS NULL OR qr_token = ''")
	if err != nil {
		return err
	}
	var numbers []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		numbers = append(numbers, n)
	}
	rows.Close()
	for _, n := range numbers {
		if _, err := RotateTableToken(n); err != nil {
			return err
		}
	}
	return nil
}

func DeleteTable(number int) error {
	table, err := GetTable(number)
	if err != nil {
		return err
	}
	if table.Status == "occupied" {
		return ErrTableOccupied
	}
	_, err = DB.Exec("DELETE FROM tables WHERE number = ?", number)
	return err
}

// GetTableByHostSession resolves the table currently hosted by the given
// guest session. Used to authorize cart/checkout actions server-side instead
// of trusting a client-supplied table number.
func GetTableByHostSession(sessionID string) (models.Table, error) {
	var t models.Table
	err := scanTable(&t, DB.QueryRow("SELECT "+tableColumns+" FROM tables WHERE host_session_id = ? AND status = 'occupied'", sessionID).Scan)
	return t, err
}

// TouchTableActivity stamps a table as just-active — called from every
// guest action that proves someone's still actually at the table (view,
// cart add/remove, checkout, DJ request), so sweepIdleTables never releases
// a table that's genuinely still in use. Errors are ignored by callers:
// this is a liveness heartbeat, not a request-critical write, and failing
// the guest's actual action over it would be the wrong tradeoff.
func TouchTableActivity(number int) error {
	_, err := DB.Exec("UPDATE tables SET last_activity_at = CURRENT_TIMESTAMP WHERE number = ?", number)
	return err
}

// ReleaseIdleTables force-releases every occupied table whose
// last_activity_at is older than idleFor (or, for a table claimed before
// this column existed, NULL — treated as idle rather than exempt, since
// "we don't know when it was last touched" is not a reason to leave it
// locked forever). Returns the tables as they were *before* release
// (host_session_id still populated) so the caller can clear each one's
// cart — ReleaseTable itself blanks host_session_id, so that has to be
// captured first, not re-derived after.
//
// The cutoff is computed entirely in SQL (datetime('now', ...)) rather
// than passed in as a pre-formatted Go time.Time: mattn/go-sqlite3 binds a
// time.Time parameter as an RFC3339 string ("...T...+00:00"), while
// last_activity_at was written by SQLite's own CURRENT_TIMESTAMP
// ("YYYY-MM-DD HH:MM:SS", no 'T', no offset) — SQLite has no real datetime
// type, so `<` between those compares them as plain text, and the 'T' vs.
// space byte at the same position means a same-day CURRENT_TIMESTAMP value
// always sorts "less than" a same-moment RFC3339 one, regardless of actual
// time — every occupied table would look idle immediately. (Caught by
// TestReleaseIdleTablesOnlyReleasesStaleOnes, which is exactly why that
// test exists — this is the same class of bug periodDateClause below
// already had to work around once, for the same underlying reason.)
// Wrapping both sides in SQLite's own datetime() keeps the comparison in
// one consistent, SQLite-native format.
func ReleaseIdleTables(idleFor time.Duration) ([]models.Table, error) {
	cutoffModifier := fmt.Sprintf("-%d seconds", int64(idleFor.Seconds()))
	rows, err := DB.Query(
		"SELECT "+tableColumns+" FROM tables WHERE status = 'occupied' AND (last_activity_at IS NULL OR datetime(last_activity_at) < datetime('now', ?))",
		cutoffModifier,
	)
	if err != nil {
		return nil, err
	}
	var released []models.Table
	for rows.Next() {
		var t models.Table
		if err := scanTable(&t, rows.Scan); err != nil {
			rows.Close()
			return nil, err
		}
		released = append(released, t)
	}
	rows.Close()

	for _, t := range released {
		if err := ReleaseTable(t.Number); err != nil {
			return released, err
		}
	}
	return released, nil
}

// RequestAssistance sets a table's "guest needs help" flag — the
// guest-facing "Call waiter" button — with why they called. Calling again
// while a call is still waiting only updates the reason: the wait clock keeps
// running from the first call. fresh reports whether this started a new call
// (so the caller only alerts staff once).
func RequestAssistance(number int, reason string) (fresh bool, err error) {
	res, err := DB.Exec("UPDATE tables SET assistance_requested = 1, assistance_requested_at = CURRENT_TIMESTAMP, assistance_reason = ? WHERE number = ? AND assistance_requested = 0", reason, number)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return true, nil
	}
	_, err = DB.Exec("UPDATE tables SET assistance_reason = ? WHERE number = ?", reason, number)
	return false, err
}

// ClearAssistance acknowledges a table's assistance request — called once
// staff has responded. Logged as answered, with who and how long it sat.
func ClearAssistance(number int, acknowledgedBy string) error {
	_, err := endAssistance(number, acknowledgedBy, "answered")
	return err
}

// CancelAssistance is the guest taking their call back before anyone came.
// Logged as cancelled (so it doesn't count towards staff response times).
// ok is false when there was no waiting call to cancel.
func CancelAssistance(number int) (ok bool, err error) {
	return endAssistance(number, "", "cancelled")
}

// endAssistance clears the flag and writes the log row inside one
// transaction, so the two can never drift apart. If the table had no
// pending request (assistance_requested_at already NULL — e.g.
// double-clicking "Got it", or a guest cancelling as staff answers), nothing
// is logged: that call already ended.
func endAssistance(number int, by, outcome string) (bool, error) {
	tx, err := DB.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var requestedAt sql.NullString
	var reason string
	if err := tx.QueryRow("SELECT assistance_requested_at, COALESCE(assistance_reason, '') FROM tables WHERE number = ?", number).Scan(&requestedAt, &reason); err != nil {
		return false, err
	}

	if requestedAt.Valid {
		// wait_seconds stays in SQLite's own datetime math (julianday), the
		// same fix ReleaseIdleTables' doc comment above explains — comparing
		// a Go-computed duration against SQLite's CURRENT_TIMESTAMP text
		// format runs into the same RFC3339-vs-"YYYY-MM-DD HH:MM:SS" mismatch
		// that bug was about, so the whole computation is done here instead.
		if _, err := tx.Exec(
			"INSERT INTO assistance_log (table_number, requested_at, acknowledged_by, wait_seconds, reason, outcome) VALUES (?, ?, ?, CAST((julianday('now') - julianday(?)) * 86400 AS INTEGER), ?, ?)",
			number, requestedAt.String, by, requestedAt.String, reason, outcome,
		); err != nil {
			return false, err
		}
	}

	if _, err := tx.Exec("UPDATE tables SET assistance_requested = 0, assistance_requested_at = NULL, assistance_reason = '' WHERE number = ?", number); err != nil {
		return false, err
	}

	return requestedAt.Valid, tx.Commit()
}

// AssistanceState is a table's current call, for the guest's own view of it.
func AssistanceState(number int) (HelpCall, bool, error) {
	var at sql.NullString
	var pending int
	c := HelpCall{Number: number}
	err := DB.QueryRow("SELECT assistance_requested, assistance_requested_at, COALESCE(assistance_reason, '') FROM tables WHERE number = ?", number).Scan(&pending, &at, &c.Reason)
	if err != nil || pending == 0 {
		return c, false, err
	}
	c.Since = parseDBTime(at)
	return c, true, nil
}

// parseDBTime reads a DATETIME column written by CURRENT_TIMESTAMP (UTC) or
// by Go (RFC3339); a missing or odd value reads as "now".
func parseDBTime(at sql.NullString) time.Time {
	if at.Valid {
		if t, err := time.Parse("2006-01-02 15:04:05", at.String); err == nil {
			return t
		} else if t, err := time.Parse(time.RFC3339, at.String); err == nil {
			return t
		}
	}
	return time.Now()
}

const assistanceLogColumns = "id, table_number, requested_at, acknowledged_at, acknowledged_by, wait_seconds, COALESCE(reason, ''), COALESCE(outcome, 'answered')"

func scanAssistanceLogEntry(scan func(...interface{}) error) (models.AssistanceLogEntry, error) {
	var e models.AssistanceLogEntry
	err := scan(&e.ID, &e.TableNumber, &e.RequestedAt, &e.AcknowledgedAt, &e.AcknowledgedBy, &e.WaitSeconds, &e.Reason, &e.Outcome)
	return e, err
}

// GetRecentAssistanceLog returns the most recent "call waiter" acknowledgments,
// newest first, for the admin dashboard's call-waiter log panel.
func GetRecentAssistanceLog(limit int) ([]models.AssistanceLogEntry, error) {
	rows, err := DB.Query("SELECT "+assistanceLogColumns+" FROM assistance_log ORDER BY acknowledged_at DESC, id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []models.AssistanceLogEntry
	for rows.Next() {
		e, err := scanAssistanceLogEntry(rows.Scan)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// HelpCall is a table asking for a waiter, since when, and why.
type HelpCall struct {
	Number int
	Since  time.Time
	Reason string // models.AssistReasons key; "" = not given
}

// PendingHelp lists tables whose "Call waiter" is still unanswered, oldest
// first, for the staff app's alert strip.
func PendingHelp() ([]HelpCall, error) {
	rows, err := DB.Query("SELECT number, assistance_requested_at, COALESCE(assistance_reason, '') FROM tables WHERE assistance_requested = 1 ORDER BY assistance_requested_at, number")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HelpCall
	for rows.Next() {
		var c HelpCall
		var at sql.NullString
		if err := rows.Scan(&c.Number, &at, &c.Reason); err != nil {
			return nil, err
		}
		c.Since = parseDBTime(at)
		out = append(out, c)
	}
	return out, rows.Err()
}
