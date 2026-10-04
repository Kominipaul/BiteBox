package models

import (
	"testing"
	"time"
)

func TestDaypartOf(t *testing.T) {
	for h, want := range map[int]int{4: DaypartNight, 5: DaypartMorning, 11: DaypartMorning, 12: DaypartAfternoon, 17: DaypartEvening, 21: DaypartEvening, 22: DaypartNight, 0: DaypartNight} {
		if got := DaypartOf(h); got != want {
			t.Errorf("DaypartOf(%d) = %d, want %d", h, got, want)
		}
	}
}

func TestNeedsRerank(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	snap := map[int]Sales{1: {30, 0, 0, 0}, 2: {0, 0, 4, 0}} // dish 1: 30/month = 7/week; dish 2: 4/month
	cases := []struct {
		name     string
		rankedAt time.Time
		week     map[int]int
		want     bool
	}{
		{"never ranked", time.Time{}, nil, true},
		{"a month ago", now.Add(-31 * 24 * time.Hour), nil, true},
		{"steady week", now.Add(-10 * 24 * time.Hour), map[int]int{1: 8, 2: 1}, false},
		{"small numbers don't count", now.Add(-10 * 24 * time.Hour), map[int]int{2: 6}, false},
		{"real spike", now.Add(-10 * 24 * time.Hour), map[int]int{2: 12}, true},
		{"new dish selling fast", now.Add(-10 * 24 * time.Hour), map[int]int{3: 15}, true},
		{"spike but just re-ranked", now.Add(-1 * 24 * time.Hour), map[int]int{2: 40}, false},
	}
	for _, c := range cases {
		if got, _ := NeedsRerank(c.rankedAt, now, 30, snap, c.week); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
