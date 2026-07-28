package db

import (
	"errors"

	"bitebox/internal/models"
)

var ErrTableOccupied = errors.New("table is occupied")

func GetTable(number int) (models.Table, error) {
	var t models.Table
	err := DB.QueryRow("SELECT number, status, host_session_id FROM tables WHERE number = ?", number).
		Scan(&t.Number, &t.Status, &t.HostSessionID)
	return t, err
}

func ClaimTable(number int, sessionID string) error {
	_, err := DB.Exec("UPDATE tables SET status = 'occupied', host_session_id = ? WHERE number = ?", sessionID, number)
	return err
}

func ReleaseTable(number int) error {
	_, err := DB.Exec("UPDATE tables SET status = 'available', host_session_id = '' WHERE number = ?", number)
	return err
}

func GetAllTables() ([]models.Table, error) {
	rows, err := DB.Query("SELECT number, status, host_session_id FROM tables ORDER BY number")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []models.Table
	for rows.Next() {
		var t models.Table
		if err := rows.Scan(&t.Number, &t.Status, &t.HostSessionID); err != nil {
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
	err := DB.QueryRow("SELECT number, status, host_session_id FROM tables WHERE host_session_id = ? AND status = 'occupied'", sessionID).
		Scan(&t.Number, &t.Status, &t.HostSessionID)
	return t, err
}
