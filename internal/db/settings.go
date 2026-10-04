package db

import (
	"encoding/json"

	"bitebox/internal/brand"
	"bitebox/internal/models"
)

// GetSettings reads the singleton venue settings row.
func GetSettings() (models.Settings, error) {
	var s models.Settings
	var djEnabled, cashAllowed, bookingEnabled int
	err := DB.QueryRow(`SELECT dj_requests_enabled, venue_name, allow_cash_orders, business_vat_number, tax_office,
		booking_enabled, booking_open, booking_close, booking_duration, booking_days_ahead, venue_phone,
		COALESCE(show_recommended, 1), COALESCE(show_popular, 1), COALESCE(recommended_title, 'Recommended by us'),
		COALESCE(popular_days, 30), COALESCE(popular_count, 6), COALESCE(popular_scope, 'all'),
		COALESCE(sort_by_popularity, 1), COALESCE(show_upcoming, 1) FROM settings WHERE id = 1`).
		Scan(&djEnabled, &s.VenueName, &cashAllowed, &s.BusinessVATNumber, &s.TaxOffice,
			&bookingEnabled, &s.BookingOpen, &s.BookingClose, &s.BookingDuration, &s.BookingDaysAhead, &s.VenuePhone,
			&s.ShowRecommended, &s.ShowPopular, &s.RecommendedTitle, &s.PopularDays, &s.PopularCount, &s.PopularScope,
			&s.SortByPopularity, &s.ShowUpcoming)
	s.DJRequestsEnabled = djEnabled != 0
	s.AllowCashOrders = cashAllowed != 0
	s.BookingEnabled = bookingEnabled != 0
	return s, err
}

func SetBookingSettings(s models.Settings) error {
	_, err := DB.Exec(`UPDATE settings SET booking_enabled = ?, booking_open = ?, booking_close = ?,
		booking_duration = ?, booking_days_ahead = ?, venue_phone = ? WHERE id = 1`,
		s.BookingEnabled, s.BookingOpen, s.BookingClose, s.BookingDuration, s.BookingDaysAhead, s.VenuePhone)
	return err
}

func SetDJRequestsEnabled(enabled bool) error {
	_, err := DB.Exec("UPDATE settings SET dj_requests_enabled = ? WHERE id = 1", enabled)
	return err
}

func SetVenueName(name string) error {
	_, err := DB.Exec("UPDATE settings SET venue_name = ? WHERE id = 1", name)
	return err
}

// SetAllowCashOrders toggles whether guests can pay cash at checkout — see
// models.Settings.AllowCashOrders.
func SetAllowCashOrders(allowed bool) error {
	_, err := DB.Exec("UPDATE settings SET allow_cash_orders = ? WHERE id = 1", allowed)
	return err
}

// SetBusinessDetails updates the demo receipt's ΑΦΜ/ΔΟΥ fields — see
// models.Settings.BusinessVATNumber's doc comment on what these are (and
// very much aren't).
func SetBusinessDetails(vatNumber, taxOffice string) error {
	_, err := DB.Exec("UPDATE settings SET business_vat_number = ?, tax_office = ? WHERE id = 1", vatNumber, taxOffice)
	return err
}

// GetVAPIDKeys/SetVAPIDKeys hold this server's Web Push identity (see
// handlers.InitPush) — deliberately kept out of models.Settings: unlike
// venue name or VAT details, there's no admin UI for these and a guest/
// staff-facing settings struct has no business carrying a private key.
func GetVAPIDKeys() (publicKey, privateKey string, err error) {
	err = DB.QueryRow("SELECT vapid_public_key, vapid_private_key FROM settings WHERE id = 1").Scan(&publicKey, &privateKey)
	return
}

func SetVAPIDKeys(publicKey, privateKey string) error {
	_, err := DB.Exec("UPDATE settings SET vapid_public_key = ?, vapid_private_key = ? WHERE id = 1", publicKey, privateKey)
	return err
}

// GetTheme is the venue's saved look (brand.Default until one is saved).
func GetTheme() brand.Theme {
	var raw string
	DB.QueryRow("SELECT theme FROM settings WHERE id = 1").Scan(&raw)
	t := brand.Default()
	if raw != "" {
		json.Unmarshal([]byte(raw), &t)
	}
	return t.Clean()
}

func SaveTheme(t brand.Theme) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = DB.Exec("UPDATE settings SET theme = ? WHERE id = 1", string(b))
	return err
}
