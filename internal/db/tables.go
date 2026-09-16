package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"bitebox/internal/models"
)

var ErrTableOccupied = errors.New("table is occupied")

const tableColumns = "number, status, host_session_id, assistance_requested"

func scanTable(t *models.Table, scan func(...interface{}) error) error {
	var assistance int
	if err := scan(&t.Number, &t.Status, &t.HostSessionID, &assistance); err != nil {
		return err
	}
	t.AssistanceRequested = assistance != 0
	return nil
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
	_, err := DB.Exec("INSERT INTO tables (number, status, host_session_id) VALUES (?, 'available', '')", number)
	return err
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
// guest-facing "Call waiter" button.
func RequestAssistance(number int) error {
	_, err := DB.Exec("UPDATE tables SET assistance_requested = 1, assistance_requested_at = CURRENT_TIMESTAMP WHERE number = ?", number)
	return err
}

// ClearAssistance acknowledges/dismisses a table's assistance request —
// called once staff has responded. Also writes the acknowledgment to
// assistance_log (who, and how long the request sat) inside the same
// transaction that clears the flag, so the two can never drift apart. If the
// table had no pending request (assistance_requested_at already NULL — e.g.
// double-clicking "Got it," or a request that was cleared some other way in
// between), nothing is logged: there's no acknowledger of a request that
// wasn't actually pending.
func ClearAssistance(number int, acknowledgedBy string) error {
	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var requestedAt sql.NullString
	if err := tx.QueryRow("SELECT assistance_requested_at FROM tables WHERE number = ?", number).Scan(&requestedAt); err != nil {
		return err
	}

	if requestedAt.Valid {
		// wait_seconds stays in SQLite's own datetime math (julianday), the
		// same fix ReleaseIdleTables' doc comment above explains — comparing
		// a Go-computed duration against SQLite's CURRENT_TIMESTAMP text
		// format runs into the same RFC3339-vs-"YYYY-MM-DD HH:MM:SS" mismatch
		// that bug was about, so the whole computation is done here instead.
		if _, err := tx.Exec(
			"INSERT INTO assistance_log (table_number, requested_at, acknowledged_by, wait_seconds) VALUES (?, ?, ?, CAST((julianday('now') - julianday(?)) * 86400 AS INTEGER))",
			number, requestedAt.String, acknowledgedBy, requestedAt.String,
		); err != nil {
			return err
		}
	}

	if _, err := tx.Exec("UPDATE tables SET assistance_requested = 0, assistance_requested_at = NULL WHERE number = ?", number); err != nil {
		return err
	}

	return tx.Commit()
}

const assistanceLogColumns = "id, table_number, requested_at, acknowledged_at, acknowledged_by, wait_seconds"

func scanAssistanceLogEntry(scan func(...interface{}) error) (models.AssistanceLogEntry, error) {
	var e models.AssistanceLogEntry
	err := scan(&e.ID, &e.TableNumber, &e.RequestedAt, &e.AcknowledgedAt, &e.AcknowledgedBy, &e.WaitSeconds)
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
