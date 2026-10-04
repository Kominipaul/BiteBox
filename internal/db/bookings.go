package db

import (
	"crypto/rand"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"bitebox/internal/models"
)

// ErrBookingConflict: the table already has an active booking overlapping
// the requested time.
var ErrBookingConflict = errors.New("table already booked for that time")

// ErrHoldExpired: a guest's 5-minute hold ran out (or was released).
var ErrHoldExpired = errors.New("hold expired")

// activeSQL matches bookings that hold their table: confirmed, seated, or a
// guest's hold that hasn't expired (the ? is now, models.HoldTimeLayout).
const activeSQL = "(status IN ('confirmed','seated') OR (status = 'held' AND hold_expires > ?))"

func holdNow() string { return time.Now().Format(models.HoldTimeLayout) }

// bookingMu serializes check-then-write on bookings so two guests can't both
// pass the overlap check for the same table and slot.
// ponytail: single-process lock; needs a DB-level constraint if this ever
// runs as more than one server process.
var bookingMu sync.Mutex

const bookingColumns = "id, code, table_number, starts_at, ends_at, guests, name, phone, email, notes, status, source, created_by, hold_expires"

func scanBooking(scan func(...interface{}) error) (models.Booking, error) {
	var b models.Booking
	err := scan(&b.ID, &b.Code, &b.TableNumber, &b.StartsAt, &b.EndsAt, &b.Guests,
		&b.Name, &b.Phone, &b.Email, &b.Notes, &b.Status, &b.Source, &b.CreatedBy, &b.HoldExpires)
	return b, err
}

func queryBookings(where string, args ...interface{}) ([]models.Booking, error) {
	rows, err := DB.Query("SELECT "+bookingColumns+" FROM bookings WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Booking
	for rows.Next() {
		b, err := scanBooking(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func GetBooking(id int) (models.Booking, error) {
	return scanBooking(DB.QueryRow("SELECT "+bookingColumns+" FROM bookings WHERE id = ?", id).Scan)
}

// GetBookingsOverlapping returns every booking (any status) overlapping
// [from, to), ordered by start — both bounds are BookingTimeLayout strings.
func GetBookingsOverlapping(from, to string) ([]models.Booking, error) {
	return queryBookings("starts_at < ? AND ends_at > ? ORDER BY starts_at, table_number", to, from)
}

// tableIsFree: no active booking on the table overlaps [start, end),
// ignoring excludeID (the booking being edited). Caller holds bookingMu.
func tableIsFree(table int, start, end string, excludeID int) (bool, error) {
	var n int
	err := DB.QueryRow(`SELECT COUNT(*) FROM bookings WHERE table_number = ? AND id != ?
		AND `+activeSQL+` AND starts_at < ? AND ends_at > ?`,
		table, excludeID, holdNow(), end, start).Scan(&n)
	return n == 0, err
}

// CreateBooking inserts b (Code/ID filled in) if its table is free for its
// time, else ErrBookingConflict. holdToken is only set for BookingHeld.
func CreateBooking(b *models.Booking, holdToken string) error {
	bookingMu.Lock()
	defer bookingMu.Unlock()
	DB.Exec("DELETE FROM bookings WHERE status = 'held' AND hold_expires <= ?", holdNow())
	if free, err := tableIsFree(b.TableNumber, b.StartsAt, b.EndsAt, 0); err != nil {
		return err
	} else if !free {
		return ErrBookingConflict
	}
	for attempt := 0; ; attempt++ {
		b.Code = newBookingCode()
		res, err := DB.Exec(`INSERT INTO bookings (code, table_number, starts_at, ends_at, guests, name, phone, email, notes, status, source, created_by, hold_token, hold_expires)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			b.Code, b.TableNumber, b.StartsAt, b.EndsAt, b.Guests, b.Name, b.Phone, b.Email, b.Notes, b.Status, b.Source, b.CreatedBy, holdToken, b.HoldExpires)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") && attempt < 5 {
				continue // code collision, draw another
			}
			return err
		}
		id, _ := res.LastInsertId()
		b.ID = int(id)
		return nil
	}
}

// UpdateBooking saves every editable field of b, re-checking the overlap
// whenever the result would be active.
func UpdateBooking(b models.Booking) error {
	bookingMu.Lock()
	defer bookingMu.Unlock()
	if b.Active() {
		if free, err := tableIsFree(b.TableNumber, b.StartsAt, b.EndsAt, b.ID); err != nil {
			return err
		} else if !free {
			return ErrBookingConflict
		}
	}
	_, err := DB.Exec(`UPDATE bookings SET table_number = ?, starts_at = ?, ends_at = ?, guests = ?, name = ?,
		phone = ?, email = ?, notes = ?, status = ? WHERE id = ?`,
		b.TableNumber, b.StartsAt, b.EndsAt, b.Guests, b.Name, b.Phone, b.Email, b.Notes, b.Status, b.ID)
	return err
}

// ConfirmHold turns a guest's live hold into a confirmed booking with their
// contact details, or ErrHoldExpired.
func ConfirmHold(token, name, phone, email, notes string) (models.Booking, error) {
	bookingMu.Lock()
	defer bookingMu.Unlock()
	b, err := scanBooking(DB.QueryRow("SELECT "+bookingColumns+" FROM bookings WHERE hold_token = ? AND status = 'held' AND hold_expires > ?",
		token, holdNow()).Scan)
	if err != nil || token == "" {
		return b, ErrHoldExpired
	}
	b.Status, b.HoldExpires, b.Name, b.Phone, b.Email, b.Notes = models.BookingConfirmed, "", name, phone, email, notes
	_, err = DB.Exec(`UPDATE bookings SET status = 'confirmed', hold_token = '', hold_expires = '',
		name = ?, phone = ?, email = ?, notes = ? WHERE id = ?`, name, phone, email, notes, b.ID)
	return b, err
}

// ReleaseHold drops a guest's hold so the table frees up immediately.
func ReleaseHold(token string) error {
	_, err := DB.Exec("DELETE FROM bookings WHERE hold_token = ? AND status = 'held'", token)
	return err
}

// FindFreeTable picks the smallest in-service table that seats guests and
// is free for [start, end) — never a full-day spot (beach), which staff pick
// by hand; onlyBookable limits it to tables
// guests may book online. Returns 0 if none.
func FindFreeTable(guests int, start, end string, onlyBookable bool, skip func(models.Table) bool) (int, error) {
	tables, err := GetAllTables()
	if err != nil {
		return 0, err
	}
	sort.SliceStable(tables, func(i, j int) bool { return tables[i].MaxGuests < tables[j].MaxGuests })
	bookingMu.Lock()
	defer bookingMu.Unlock()
	for _, t := range tables {
		if t.OutOfService || t.FullDay || (onlyBookable && !t.Bookable) || guests > t.MaxGuests || guests < t.MinGuests || (skip != nil && skip(t)) {
			continue
		}
		if free, err := tableIsFree(t.Number, start, end, 0); err != nil {
			return 0, err
		} else if free {
			return t.Number, nil
		}
	}
	return 0, nil
}

// newBookingCode: 6 chars without look-alikes (0/O, 1/I), read over the phone.
func newBookingCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 6)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// CountUpcomingBookings: active bookings on a table that haven't ended yet
// — a spot with any can't be removed from the floor.
func CountUpcomingBookings(table int) (int, error) {
	var n int
	err := DB.QueryRow("SELECT COUNT(*) FROM bookings WHERE table_number = ? AND "+activeSQL+" AND ends_at > ?",
		table, holdNow(), time.Now().Format(models.BookingTimeLayout)).Scan(&n)
	return n, err
}

// ExpireBookings closes bookings whose time is over, so they stop holding
// their spot: seated → completed, confirmed but never seated → no-show.
// Until then a booking keeps its spot for its whole window; only staff free
// it early (see handlers.StaffBookingStatus). Returns how many changed.
func ExpireBookings(now time.Time) (int64, error) {
	n := now.Format(models.BookingTimeLayout)
	bookingMu.Lock()
	defer bookingMu.Unlock()
	var total int64
	for _, q := range []string{
		"UPDATE bookings SET status = 'completed' WHERE status = 'seated' AND ends_at <= ?",
		"UPDATE bookings SET status = 'no_show' WHERE status = 'confirmed' AND ends_at <= ?",
	} {
		res, err := DB.Exec(q, n)
		if err != nil {
			return total, err
		}
		c, _ := res.RowsAffected()
		total += c
	}
	return total, nil
}

// ArrivalGrace is how early a booked party can turn up and still count as
// arrived for their booking.
const ArrivalGrace = 20 * time.Minute

// SeatArrivedBooking marks the booking at table seated when its guests
// arrive — a QR scan at the table from just before its start until its end.
func SeatArrivedBooking(table int, now time.Time) (bool, error) {
	bookingMu.Lock()
	defer bookingMu.Unlock()
	res, err := DB.Exec(`UPDATE bookings SET status = 'seated' WHERE table_number = ? AND status = 'confirmed'
		AND starts_at <= ? AND ends_at > ?`, table, now.Add(ArrivalGrace).Format(models.BookingTimeLayout), now.Format(models.BookingTimeLayout))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
