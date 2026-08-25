package db

import (
	"database/sql"

	"bitebox/internal/models"
)

const songRequestColumns = "id, song_name, tip_amount, status, table_number"

func scanSongRequest(row *sql.Row) (models.SongRequest, error) {
	var s models.SongRequest
	var tableNumber sql.NullInt64
	err := row.Scan(&s.ID, &s.SongName, &s.TipAmount, &s.Status, &tableNumber)
	s.TableNumber = int(tableNumber.Int64)
	return s, err
}

func CreateSongRequest(songName string, tipAmount float64, tableNumber int) (int, error) {
	res, err := DB.Exec(
		"INSERT INTO song_requests (song_name, tip_amount, status, table_number) VALUES (?, ?, ?, ?)",
		songName, tipAmount, models.SongRequestStatusPending, tableNumber,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func GetSongRequestByID(id int) (models.SongRequest, error) {
	return scanSongRequest(DB.QueryRow("SELECT "+songRequestColumns+" FROM song_requests WHERE id = ?", id))
}

// GetLatestSongRequestForTable returns the most recent song request sent
// from a table, for the guest-facing "DJ decision" widget.
func GetLatestSongRequestForTable(tableNumber int) (models.SongRequest, error) {
	return scanSongRequest(DB.QueryRow(
		"SELECT "+songRequestColumns+" FROM song_requests WHERE table_number = ? ORDER BY id DESC LIMIT 1",
		tableNumber,
	))
}

func GetPendingSongRequests() ([]models.SongRequest, error) {
	rows, err := DB.Query(
		"SELECT "+songRequestColumns+" FROM song_requests WHERE status = ? ORDER BY id ASC",
		models.SongRequestStatusPending,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []models.SongRequest
	for rows.Next() {
		var s models.SongRequest
		var tableNumber sql.NullInt64
		if err := rows.Scan(&s.ID, &s.SongName, &s.TipAmount, &s.Status, &tableNumber); err != nil {
			return nil, err
		}
		s.TableNumber = int(tableNumber.Int64)
		requests = append(requests, s)
	}
	return requests, nil
}

func UpdateSongRequestStatus(id int, status string) error {
	_, err := DB.Exec("UPDATE song_requests SET status = ? WHERE id = ?", status, id)
	return err
}
