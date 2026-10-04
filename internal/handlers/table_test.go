package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bitebox/internal/cart"
	"bitebox/internal/db"
	"bitebox/internal/models"
)

// TestLeaveRejectsNonHost is a regression test for SEC-04: /table/{n}/leave
// used to trust host_session_id straight off the URL, letting anyone
// release (and thereby eject the real host from) any table just by
// guessing its number. Leave must resolve the table from the caller's own
// session cookie and refuse anyone else.
func TestLeaveRejectsNonHost(t *testing.T) {
	const tableNum = 9001
	if err := db.CreateTable(tableNum); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if err := db.ClaimTable(tableNum, "real-host-session"); err != nil {
		t.Fatalf("ClaimTable: %v", err)
	}

	h := &TableHandlers{Cart: cart.NewStore()}

	// An attacker with a different (or no) session tries to release it.
	r := httptest.NewRequest(http.MethodPost, "/table/9001/leave", nil)
	r.AddCookie(&http.Cookie{Name: GuestSessionCookieName, Value: "attacker-session"})
	w := httptest.NewRecorder()
	h.Leave(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("attacker's Leave status = %d, want 403", w.Code)
	}
	table, err := db.GetTable(tableNum)
	if err != nil {
		t.Fatalf("GetTable: %v", err)
	}
	if table.Status != "occupied" || table.HostSessionID != "real-host-session" {
		t.Fatalf("table state changed by a non-host's Leave call: %+v", table)
	}

	// The real host can still leave.
	r2 := httptest.NewRequest(http.MethodPost, "/table/9001/leave", nil)
	r2.AddCookie(&http.Cookie{Name: GuestSessionCookieName, Value: "real-host-session"})
	w2 := httptest.NewRecorder()
	h.Leave(w2, r2)

	if w2.Code != http.StatusOK {
		t.Fatalf("real host's Leave status = %d, want 200", w2.Code)
	}
	table, err = db.GetTable(tableNum)
	if err != nil {
		t.Fatalf("GetTable: %v", err)
	}
	if table.Status != "available" {
		t.Fatalf("expected table released by its real host, got status %q", table.Status)
	}
}

// TestLeaveRejectsRequestWithNoCookie covers the even simpler pre-fix
// case: no session cookie at all used to still fall through to releasing
// whatever table number was in the URL.
func TestLeaveRejectsRequestWithNoCookie(t *testing.T) {
	const tableNum = 9002
	if err := db.CreateTable(tableNum); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if err := db.ClaimTable(tableNum, "some-host"); err != nil {
		t.Fatalf("ClaimTable: %v", err)
	}

	h := &TableHandlers{Cart: cart.NewStore()}
	r := httptest.NewRequest(http.MethodPost, "/table/9002/leave", nil)
	w := httptest.NewRecorder()
	h.Leave(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	table, _ := db.GetTable(tableNum)
	if table.Status != "occupied" {
		t.Fatal("table was released by a request carrying no session cookie at all")
	}
}

// TestWorkerAcknowledgeAssistanceRecordsActingUser is the handler-level
// counterpart to db's own TestClearAssistanceLogsAcknowledgment: it checks
// that the logged-in worker's username (not some hardcoded placeholder)
// actually makes it into the assistance_log row.
func TestWorkerAcknowledgeAssistanceRecordsActingUser(t *testing.T) {
	const tableNum = 9003
	if err := db.CreateTable(tableNum); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if _, err := db.RequestAssistance(tableNum, "help"); err != nil {
		t.Fatalf("RequestAssistance: %v", err)
	}

	acker := models.User{ID: 1, Username: "waiter_test_ack", Role: models.RoleWorker}
	r := httptest.NewRequest(http.MethodPost, "/worker/tables/9003/assist/clear", nil)
	r = withChiURLParam(r, "number", "9003")
	r = withPushTestUser(r, acker)
	w := httptest.NewRecorder()

	WorkerAcknowledgeAssistance(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	entries, err := db.GetRecentAssistanceLog(1)
	if err != nil {
		t.Fatalf("GetRecentAssistanceLog: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 assistance_log entry, got %d", len(entries))
	}
	if entries[0].TableNumber != tableNum {
		t.Errorf("TableNumber = %d, want %d", entries[0].TableNumber, tableNum)
	}
	if entries[0].AcknowledgedBy != "waiter_test_ack" {
		t.Errorf("AcknowledgedBy = %q, want %q", entries[0].AcknowledgedBy, "waiter_test_ack")
	}
}

// TestGuestCallAndCancelAssistance: the host calls a waiter with a reason,
// someone else's session can't cancel it, and the host's own cancel clears
// it and hands back the "Call a waiter" form.
func TestGuestCallAndCancelAssistance(t *testing.T) {
	const tableNum = 9005
	if err := db.CreateTable(tableNum); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if err := db.ClaimTable(tableNum, "assist-host"); err != nil {
		t.Fatalf("ClaimTable: %v", err)
	}
	post := func(path, session, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: GuestSessionCookieName, Value: session})
		w := httptest.NewRecorder()
		if strings.HasSuffix(path, "/cancel") {
			CancelAssistance(w, r)
		} else {
			RequestAssistance(w, r)
		}
		return w
	}

	w := post("/table/9005/assist", "assist-host", "reason=bill")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `data-state="pending"`) {
		t.Fatalf("call: %d %s", w.Code, w.Body.String())
	}
	if call, pending, _ := db.AssistanceState(tableNum); !pending || call.Reason != "bill" {
		t.Fatalf("after call: %+v pending=%v", call, pending)
	}

	if w := post("/table/9005/assist/cancel", "someone-else", ""); w.Code != http.StatusForbidden {
		t.Fatalf("stranger's cancel = %d, want 403", w.Code)
	}
	if _, pending, _ := db.AssistanceState(tableNum); !pending {
		t.Fatal("a stranger cancelled the host's call")
	}

	w = post("/table/9005/assist/cancel", "assist-host", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `data-state="idle"`) {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	if _, pending, _ := db.AssistanceState(tableNum); pending {
		t.Fatal("call still pending after the host cancelled")
	}
}
