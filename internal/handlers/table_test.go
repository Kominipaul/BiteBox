package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"bitebox/internal/cart"
	"bitebox/internal/db"
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
