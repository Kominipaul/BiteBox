package db

import (
	"testing"
	"time"

	"bitebox/internal/models"
)

// TestBookingTimeRules: a booking holds its spot for its whole time; a QR
// scan at the table during it seats the party; when the time is over it
// closes itself (seated → completed, never arrived → no-show).
func TestBookingTimeRules(t *testing.T) {
	const table = 8801
	if err := CreateTable(table); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	now := time.Now().Truncate(time.Minute)
	bt := func(d time.Duration) string { return now.Add(d).Format(models.BookingTimeLayout) }
	mk := func(start, end time.Duration, table int) models.Booking {
		b := models.Booking{TableNumber: table, StartsAt: bt(start), EndsAt: bt(end), Guests: 2, Name: "Timing", Status: models.BookingConfirmed, Source: "phone"}
		if err := CreateBooking(&b, ""); err != nil {
			t.Fatalf("CreateBooking: %v", err)
		}
		return b
	}

	// Party due 10 minutes from now scans the QR early: seated.
	now1 := mk(10*time.Minute, 2*time.Hour, table)
	if ok, err := SeatArrivedBooking(table, now); err != nil || !ok {
		t.Fatalf("early arrival not seated: %v %v", ok, err)
	}
	if b, _ := GetBooking(now1.ID); b.Status != models.BookingSeated {
		t.Fatalf("status %q, want seated", b.Status)
	}
	// Far-future booking on another table: a scan now doesn't touch it.
	CreateTable(table + 1)
	later := mk(5*time.Hour, 7*time.Hour, table+1)
	if ok, _ := SeatArrivedBooking(table+1, now); ok {
		t.Fatal("a scan hours before the booking seated it")
	}

	// Still inside its time: nothing expires; it keeps holding the spot.
	if n, _ := ExpireBookings(now); n != 0 {
		t.Fatalf("expired %d bookings mid-time", n)
	}
	if free, _ := tableIsFree(table, bt(time.Hour), bt(90*time.Minute), 0); free {
		t.Fatal("spot free during a seated booking")
	}

	// After both have ended: seated → completed, never seated → no-show.
	if n, err := ExpireBookings(now.Add(8 * time.Hour)); err != nil || n < 2 {
		t.Fatalf("expire: %d %v", n, err)
	}
	a, _ := GetBooking(now1.ID)
	b, _ := GetBooking(later.ID)
	if a.Status != models.BookingCompleted || b.Status != models.BookingNoShow {
		t.Fatalf("after time: %q / %q, want completed / no_show", a.Status, b.Status)
	}
}
