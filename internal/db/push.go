package db

import "bitebox/internal/models"

// GuestPushSubscription is one guest device registered for real phone
// notifications (see handlers.GuestPushSubscribe) — same shape as
// PushSubscription, kept as a separate type since it's read from a
// separate table (guest_push_subscriptions) with no user_id at all.
type GuestPushSubscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// PushSubscription is one staff device registered for real phone
// notifications (see handlers.PushSubscribe) — just enough to hand
// straight to webpush-go's SendNotification.
type PushSubscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// SavePushSubscription stores (or refreshes) one device's Web Push
// subscription for a staff account. A device re-subscribing — browser
// storage cleared, permission re-granted, whatever — sends the same
// endpoint again; upserting on the unique endpoint keeps that idempotent
// instead of piling up duplicate rows for the same phone.
func SavePushSubscription(userID int, endpoint, p256dh, auth string) error {
	_, err := DB.Exec(
		`INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth) VALUES (?, ?, ?, ?)
		 ON CONFLICT(endpoint) DO UPDATE SET user_id = excluded.user_id, p256dh = excluded.p256dh, auth = excluded.auth`,
		userID, endpoint, p256dh, auth,
	)
	return err
}

// DeletePushSubscription removes one device's subscription unconditionally
// — used only for the server-driven cleanup path (a push service answering
// 404/410, or a stored row failing revalidation — see
// handlers.notifyDepartments), where the endpoint came from our own DB, not
// from a request. Handler-facing deletes go through
// DeletePushSubscriptionForUser instead.
func DeletePushSubscription(endpoint string) error {
	_, err := DB.Exec("DELETE FROM push_subscriptions WHERE endpoint = ?", endpoint)
	return err
}

// DeletePushSubscriptionForUser removes one device's subscription, scoped
// to the given user — the "turn off phone alerts" action. Scoping to
// user_id (rather than deleting by endpoint alone) means one staff account
// can't blind-delete another's subscription by supplying/guessing its
// endpoint.
func DeletePushSubscriptionForUser(userID int, endpoint string) error {
	_, err := DB.Exec("DELETE FROM push_subscriptions WHERE endpoint = ? AND user_id = ?", endpoint, userID)
	return err
}

// GetPushSubscriptionsForDepartments returns every subscribed device
// belonging to an active user in one of the given departments, plus every
// admin and superworker account regardless of which department was asked
// for — same "sees/handles everything" rule the order feed itself already
// applies (see handlers.orderFeedBuckets). A deactivated account is
// excluded: its live sessions are already being kicked (wsKickCloseCode),
// so a push would be the one channel that kept reaching them anyway.
func GetPushSubscriptionsForDepartments(departments []string) ([]PushSubscription, error) {
	if len(departments) == 0 {
		return nil, nil
	}
	placeholders := make([]byte, 0, len(departments)*2)
	args := make([]interface{}, 0, len(departments)+2)
	args = append(args, models.RoleAdmin, models.DepartmentSuperworker)
	for i, d := range departments {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, d)
	}

	rows, err := DB.Query(
		`SELECT ps.endpoint, ps.p256dh, ps.auth
		 FROM push_subscriptions ps
		 JOIN users u ON u.id = ps.user_id
		 WHERE u.is_active = 1 AND (u.role = ? OR u.department = ? OR u.department IN (`+string(placeholders)+`))`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []PushSubscription
	for rows.Next() {
		var s PushSubscription
		if err := rows.Scan(&s.Endpoint, &s.P256dh, &s.Auth); err != nil {
			return nil, err
		}
		subs = append(subs, s)
	}
	return subs, rows.Err()
}

// GetPushSubscriptionsForExactDepartments is GetPushSubscriptionsForDepartments
// without the "admin/superworker always included" broadening — a literal
// department match only. For the one alert where that broadening is
// actively wrong: "ready for pickup" is the waiter's job to go act on, not
// something a manager needs interrupting their phone for on top of already
// seeing every order in the live dashboard feed. Every other alert
// (new order, assistance) keeps using the broadened version above.
func GetPushSubscriptionsForExactDepartments(departments []string) ([]PushSubscription, error) {
	if len(departments) == 0 {
		return nil, nil
	}
	placeholders := make([]byte, 0, len(departments)*2)
	args := make([]interface{}, 0, len(departments))
	for i, d := range departments {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, d)
	}

	rows, err := DB.Query(
		`SELECT ps.endpoint, ps.p256dh, ps.auth
		 FROM push_subscriptions ps
		 JOIN users u ON u.id = ps.user_id
		 WHERE u.is_active = 1 AND u.department IN (`+string(placeholders)+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []PushSubscription
	for rows.Next() {
		var s PushSubscription
		if err := rows.Scan(&s.Endpoint, &s.P256dh, &s.Auth); err != nil {
			return nil, err
		}
		subs = append(subs, s)
	}
	return subs, rows.Err()
}

// SaveGuestPushSubscription stores (or refreshes) one guest device's Web
// Push subscription, tied to their anonymous session — mirrors
// SavePushSubscription's upsert-on-endpoint behavior for the same reason:
// a device re-subscribing (browser storage cleared, permission re-granted)
// sends the same endpoint again.
func SaveGuestPushSubscription(sessionID, endpoint, p256dh, auth string) error {
	_, err := DB.Exec(
		`INSERT INTO guest_push_subscriptions (session_id, endpoint, p256dh, auth) VALUES (?, ?, ?, ?)
		 ON CONFLICT(endpoint) DO UPDATE SET session_id = excluded.session_id, p256dh = excluded.p256dh, auth = excluded.auth`,
		sessionID, endpoint, p256dh, auth,
	)
	return err
}

// DeleteGuestPushSubscription removes one guest device's subscription
// unconditionally — the server-driven cleanup path (a push service
// answering 404/410), same reasoning as DeletePushSubscription.
// Handler-facing deletes go through DeleteGuestPushSubscriptionForSession.
func DeleteGuestPushSubscription(endpoint string) error {
	_, err := DB.Exec("DELETE FROM guest_push_subscriptions WHERE endpoint = ?", endpoint)
	return err
}

// DeleteGuestPushSubscriptionForSession removes one guest device's
// subscription, scoped to the given session — the guest-facing "turn off
// order alerts" action. Scoping to session_id means one guest can't
// blind-delete another table's subscription by supplying/guessing its
// endpoint, same rule DeletePushSubscriptionForUser applies for staff.
func DeleteGuestPushSubscriptionForSession(sessionID, endpoint string) error {
	_, err := DB.Exec("DELETE FROM guest_push_subscriptions WHERE endpoint = ? AND session_id = ?", endpoint, sessionID)
	return err
}

// GetGuestPushSubscriptionsForSession returns every device a guest session
// has subscribed for order alerts — a guest session almost always means one
// phone, but nothing stops the same session cookie being open in two tabs/
// devices (e.g. desktop + phone at the same table), so this returns all of
// them rather than assuming one.
func GetGuestPushSubscriptionsForSession(sessionID string) ([]GuestPushSubscription, error) {
	rows, err := DB.Query(
		"SELECT endpoint, p256dh, auth FROM guest_push_subscriptions WHERE session_id = ?",
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []GuestPushSubscription
	for rows.Next() {
		var s GuestPushSubscription
		if err := rows.Scan(&s.Endpoint, &s.P256dh, &s.Auth); err != nil {
			return nil, err
		}
		subs = append(subs, s)
	}
	return subs, rows.Err()
}
