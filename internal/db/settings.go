package db

import "bitebox/internal/models"

// GetSettings reads the singleton venue settings row.
func GetSettings() (models.Settings, error) {
	var s models.Settings
	var djEnabled, cashAllowed int
	err := DB.QueryRow("SELECT dj_requests_enabled, venue_name, allow_cash_orders, business_vat_number, tax_office FROM settings WHERE id = 1").
		Scan(&djEnabled, &s.VenueName, &cashAllowed, &s.BusinessVATNumber, &s.TaxOffice)
	s.DJRequestsEnabled = djEnabled != 0
	s.AllowCashOrders = cashAllowed != 0
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
