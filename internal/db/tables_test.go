package db

import (
	"testing"
	"time"

	"bitebox/internal/models"
)

// TestClearAssistanceLogsAcknowledgment is the regression test for the
// "call waiter" audit trail: acknowledging a pending request must both
// clear the live flag and leave a durable assistance_log row recording who
// cleared it.
func TestClearAssistanceLogsAcknowledgment(t *testing.T) {
	const tableNum = 9301
	if err := CreateTable(tableNum); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if _, err := RequestAssistance(tableNum, "help"); err != nil {
		t.Fatalf("RequestAssistance: %v", err)
	}

	before, err := GetRecentAssistanceLog(1000)
	if err != nil {
		t.Fatalf("GetRecentAssistanceLog: %v", err)
	}

	if err := ClearAssistance(tableNum, "waiter_maria"); err != nil {
		t.Fatalf("ClearAssistance: %v", err)
	}

	table, err := GetTable(tableNum)
	if err != nil {
		t.Fatalf("GetTable: %v", err)
	}
	if table.AssistanceRequested {
		t.Error("assistance_requested should be cleared")
	}

	after, err := GetRecentAssistanceLog(1000)
	if err != nil {
		t.Fatalf("GetRecentAssistanceLog: %v", err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("expected exactly one new assistance_log row, before=%d after=%d", len(before), len(after))
	}

	entry := after[0] // newest first
	if entry.TableNumber != tableNum {
		t.Errorf("TableNumber = %d, want %d", entry.TableNumber, tableNum)
	}
	if entry.AcknowledgedBy != "waiter_maria" {
		t.Errorf("AcknowledgedBy = %q, want %q", entry.AcknowledgedBy, "waiter_maria")
	}
	if entry.WaitSeconds < 0 {
		t.Errorf("WaitSeconds = %d, want >= 0", entry.WaitSeconds)
	}
	if entry.RequestedAt.After(entry.AcknowledgedAt) {
		t.Errorf("RequestedAt (%v) should not be after AcknowledgedAt (%v)", entry.RequestedAt, entry.AcknowledgedAt)
	}
}

// TestClearAssistanceWithNoPendingRequestDoesNotLog covers double-ack (or
// any other path that clears an already-clear flag): there's no
// acknowledger of a request that wasn't actually pending, so no log row
// should appear.
func TestClearAssistanceWithNoPendingRequestDoesNotLog(t *testing.T) {
	const tableNum = 9302
	if err := CreateTable(tableNum); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	// Never called RequestAssistance — nothing pending.

	before, err := GetRecentAssistanceLog(1000)
	if err != nil {
		t.Fatalf("GetRecentAssistanceLog: %v", err)
	}

	if err := ClearAssistance(tableNum, "waiter_maria"); err != nil {
		t.Fatalf("ClearAssistance: %v", err)
	}

	after, err := GetRecentAssistanceLog(1000)
	if err != nil {
		t.Fatalf("GetRecentAssistanceLog: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("expected no new assistance_log row when nothing was pending, before=%d after=%d", len(before), len(after))
	}
}

// TestClearAssistanceComputesWaitSeconds backdates a request's timestamp
// directly (rather than sleeping in the test) and checks the logged
// wait_seconds reflects that gap, confirming the SQL-side julianday math is
// actually computing a duration and not just recording a fixed value.
func TestClearAssistanceComputesWaitSeconds(t *testing.T) {
	const tableNum = 9303
	if err := CreateTable(tableNum); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if _, err := RequestAssistance(tableNum, "help"); err != nil {
		t.Fatalf("RequestAssistance: %v", err)
	}
	// Back-date the request by 5 real minutes.
	if _, err := DB.Exec(
		"UPDATE tables SET assistance_requested_at = datetime(assistance_requested_at, '-5 minutes') WHERE number = ?",
		tableNum,
	); err != nil {
		t.Fatalf("failed to backdate assistance_requested_at: %v", err)
	}

	if err := ClearAssistance(tableNum, "waiter_maria"); err != nil {
		t.Fatalf("ClearAssistance: %v", err)
	}

	entries, err := GetRecentAssistanceLog(1)
	if err != nil {
		t.Fatalf("GetRecentAssistanceLog: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	// Allow a few seconds of slack for real wall-clock time elapsed during
	// the test itself.
	const wantMin, wantMax = 295, 310
	if got := entries[0].WaitSeconds; got < wantMin || got > wantMax {
		t.Errorf("WaitSeconds = %d, want between %d and %d (~5 minutes)", got, wantMin, wantMax)
	}
}

// TestCancelAssistance covers the guest taking a call back: the flag clears,
// the log keeps it as "cancelled" with its reason, a second cancel is a
// no-op, a repeat call only updates the reason, and cancelled calls stay out
// of the response-time stats.
func TestCancelAssistance(t *testing.T) {
	const tableNum = 9304
	if err := CreateTable(tableNum); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if fresh, err := RequestAssistance(tableNum, "help"); err != nil || !fresh {
		t.Fatalf("first call: fresh=%v err=%v", fresh, err)
	}
	if fresh, err := RequestAssistance(tableNum, "bill"); err != nil || fresh {
		t.Fatalf("repeat call should update, not start anew: fresh=%v err=%v", fresh, err)
	}
	if call, pending, err := AssistanceState(tableNum); err != nil || !pending || call.Reason != "bill" {
		t.Fatalf("state after repeat: %+v pending=%v err=%v", call, pending, err)
	}
	before, _ := SummarizeHelp(HelpFilter{Tables: []int{tableNum}})

	if ok, err := CancelAssistance(tableNum); err != nil || !ok {
		t.Fatalf("CancelAssistance: ok=%v err=%v", ok, err)
	}
	if ok, err := CancelAssistance(tableNum); err != nil || ok {
		t.Fatalf("second cancel should be a no-op: ok=%v err=%v", ok, err)
	}
	if _, pending, _ := AssistanceState(tableNum); pending {
		t.Error("call should no longer be pending")
	}
	entries, err := ListHelp(HelpFilter{Tables: []int{tableNum}}, 0, 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("want 1 log row, got %d (err %v)", len(entries), err)
	}
	if e := entries[0]; e.Outcome != "cancelled" || e.Reason != "bill" || e.AcknowledgedBy != "" {
		t.Errorf("log row = %+v, want cancelled / bill / no staff", e)
	}
	if after, _ := SummarizeHelp(HelpFilter{Tables: []int{tableNum}}); after.Count != before.Count {
		t.Errorf("cancelled call counted in stats: %d -> %d", before.Count, after.Count)
	}
}

// TestRecomputePopularity: the snapshot counts units per dish per daypart
// (by local hour), leaves cancelled orders out, and stamps when it ran.
func TestRecomputePopularity(t *testing.T) {
	CreateCategory("Pop Test Bar", "bar")
	pid, err := CreateProduct("Pop Test Beer", 5, -1, "Pop Test Bar", "", "", 24)
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	CreateTable(9305)
	add := func(qty int, localHour int, status string) {
		id, err := CreateOrder(9305, "pop-test", "cash", "unpaid", "", 5, 0, 1, []models.OrderItem{{ProductID: pid, Name: "Pop Test Beer", Price: 5, Quantity: qty}})
		if err != nil {
			t.Fatalf("CreateOrder: %v", err)
		}
		now := time.Now()
		at := time.Date(now.Year(), now.Month(), now.Day(), localHour, 30, 0, 0, time.Local).AddDate(0, 0, -1)
		DB.Exec("UPDATE orders SET created_at = ?, status = ? WHERE id = ?", at.UTC().Format("2006-01-02 15:04:05"), status, id)
	}
	add(3, 23, "served") // night
	add(2, 1, "served")  // night (after midnight)
	add(4, 13, "served") // afternoon
	add(9, 23, "cancelled")
	if err := RecomputePopularity(30, time.Now()); err != nil {
		t.Fatalf("RecomputePopularity: %v", err)
	}
	snap, _ := PopularitySnapshot()
	got := snap[pid]
	if got[models.DaypartNight] != 5 || got[models.DaypartAfternoon] != 4 || got.Total() != 9 {
		t.Errorf("snapshot = %v, want night 5, afternoon 4, total 9 (cancelled left out)", got)
	}
	if PopularityRankedAt().IsZero() {
		t.Error("ranked-at not stamped")
	}
}
