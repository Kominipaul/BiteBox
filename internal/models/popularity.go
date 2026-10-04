package models

import "time"

// Popularity is a snapshot, not a leaderboard: sales per dish per daypart
// over a period, recomputed on a schedule (monthly, or sooner on a real
// spike, see NeedsRerank) so the guest menu's order stays put between
// re-ranks instead of shuffling after every order.

// Dayparts: when in the day an order was placed (venue local time).
const (
	DaypartMorning   = 0 // 05–12
	DaypartAfternoon = 1 // 12–17
	DaypartEvening   = 2 // 17–22
	DaypartNight     = 3 // 22–05
)

var DaypartNames = [4]string{"Morning", "Afternoon", "Evening", "Night"}

// DaypartOf is the daypart of an hour of the day (0–23).
func DaypartOf(hour int) int {
	switch {
	case hour >= 5 && hour < 12:
		return DaypartMorning
	case hour >= 12 && hour < 17:
		return DaypartAfternoon
	case hour >= 17 && hour < 22:
		return DaypartEvening
	}
	return DaypartNight
}

// Sales is one dish's snapshot: units sold per daypart.
type Sales [4]int

func (s Sales) Total() int { return s[0] + s[1] + s[2] + s[3] }

// Re-rank rules.
const (
	RerankEvery   = 30 * 24 * time.Hour // the normal cadence
	SpikeFactor   = 2.5                 // this week's pace vs what the snapshot knows
	SpikeMinUnits = 10                  // ignore tiny numbers (3 sold vs 1 isn't a trend)
	SpikeCooldown = 3 * 24 * time.Hour  // at most one spike re-rank every few days
)

// NeedsRerank decides whether to recompute the snapshot now: never ranked,
// the month is up, or a dish's last-7-days sales run far ahead of the pace
// the snapshot (covering periodDays) knows for it.
func NeedsRerank(rankedAt, now time.Time, periodDays int, snapshot map[int]Sales, lastWeek map[int]int) (bool, string) {
	if rankedAt.IsZero() {
		return true, "first ranking"
	}
	if now.Sub(rankedAt) >= RerankEvery {
		return true, "monthly re-rank"
	}
	if now.Sub(rankedAt) < SpikeCooldown || periodDays <= 0 {
		return false, ""
	}
	for id, week := range lastWeek {
		if week < SpikeMinUnits {
			continue
		}
		known := float64(snapshot[id].Total()) * 7 / float64(periodDays)
		if float64(week) >= SpikeFactor*known {
			return true, "sales spike"
		}
	}
	return false, ""
}
