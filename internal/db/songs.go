package db

import (
	"bitebox/internal/models"
)

func CreateSongRequest(songName string, tipAmount float64) (int, error) {
	res, err := DB.Exec(
		"INSERT INTO song_requests (song_name, tip_amount, status) VALUES (?, ?, ?)",
		songName, tipAmount, models.SongRequestStatusPending,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func GetPendingSongRequests() ([]models.SongRequest, error) {
	rows, err := DB.Query(
		"SELECT id, song_name, tip_amount, status FROM song_requests WHERE status = ? ORDER BY id ASC",
		models.SongRequestStatusPending,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []models.SongRequest
	for rows.Next() {
		var s models.SongRequest
		if err := rows.Scan(&s.ID, &s.SongName, &s.TipAmount, &s.Status); err != nil {
			return nil, err
		}
		requests = append(requests, s)
	}
	return requests, nil
}

func UpdateSongRequestStatus(id int, status string) error {
	_, err := DB.Exec("UPDATE song_requests SET status = ? WHERE id = ?", status, id)
	return err
}
