package db

import (
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"bitebox/internal/models"
)

// Fifty guests grabbing the same spot at the same moment: exactly one hold
// wins, everyone else gets ErrBookingConflict. Run with -race.
func TestSimultaneousHoldsOnlyOneWins(t *testing.T) {
	const table = 9950
	if err := CreateTable(table); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(48 * time.Hour).Truncate(time.Hour)
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		won, lost int
		other     []error
	)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b := models.Booking{TableNumber: table, StartsAt: start.Format(models.BookingTimeLayout),
				EndsAt: start.Add(2 * time.Hour).Format(models.BookingTimeLayout), Guests: 2, Status: models.BookingHeld,
				Source: "online", HoldExpires: time.Now().Add(5 * time.Minute).Format(models.HoldTimeLayout)}
			err := CreateBooking(&b, "race-"+strconv.Itoa(i))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won++
			case errors.Is(err, ErrBookingConflict):
				lost++
			default:
				other = append(other, err)
			}
		}(i)
	}
	wg.Wait()
	if won != 1 || lost != 49 || len(other) > 0 {
		t.Fatalf("won %d, lost %d, errors %v; want exactly one winner", won, lost, other)
	}
}
