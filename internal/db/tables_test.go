package db

import (
	"testing"
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
	if err := RequestAssistance(tableNum); err != nil {
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
	if err := RequestAssistance(tableNum); err != nil {
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
