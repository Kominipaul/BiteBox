package db

import "bitebox/internal/models"

// GetSettings reads the singleton venue settings row.
func GetSettings() (models.Settings, error) {
	var s models.Settings
	var enabled int
	err := DB.QueryRow("SELECT dj_requests_enabled, venue_name FROM settings WHERE id = 1").Scan(&enabled, &s.VenueName)
	s.DJRequestsEnabled = enabled != 0
	return s, err
}

func SetDJRequestsEnabled(enabled bool) error {
	_, err := DB.Exec("UPDATE settings SET dj_requests_enabled = ? WHERE id = 1", enabled)
	return err
}

func SetVenueName(name string) error {
	_, err := DB.Exec("UPDATE settings SET venue_name = ? WHERE id = 1", name)
	return err
}
