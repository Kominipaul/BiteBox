package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bitebox/internal/cart"
	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
)

func withParam(r *http.Request, key, value string) *http.Request {
	rc := chi.NewRouteContext()
	rc.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
}

func scan(h *TableHandlers, token, session string) *httptest.ResponseRecorder {
	r := withParam(httptest.NewRequest(http.MethodGet, "/t/"+token, nil), "token", token)
	if session != "" {
		r.AddCookie(&http.Cookie{Name: GuestSessionCookieName, Value: session})
	}
	w := httptest.NewRecorder()
	h.Scan(w, r)
	return w
}

func viewTable(h *TableHandlers, number, session string) *httptest.ResponseRecorder {
	r := withParam(httptest.NewRequest(http.MethodGet, "/table/"+number, nil), "number", number)
	if session != "" {
		r.AddCookie(&http.Cookie{Name: GuestSessionCookieName, Value: session})
	}
	w := httptest.NewRecorder()
	h.View(w, r)
	return w
}

// SEC-13: a table can only be claimed by scanning its secret QR link, never
// by typing /table/{n}.
func TestTableClaimNeedsQRToken(t *testing.T) {
	for _, n := range []int{9841, 9842} {
		if err := db.CreateTable(n); err != nil {
			t.Fatalf("CreateTable: %v", err)
		}
	}
	h := &TableHandlers{Cart: cart.NewStore()}
	t1, _ := db.GetTable(9841)
	if len(t1.QRToken) < 16 {
		t.Fatalf("new table got token %q, want a long random one", t1.QRToken)
	}

	// Typing the number claims nothing.
	if w := viewTable(h, "9841", "typist"); w.Code != http.StatusForbidden {
		t.Fatalf("/table/9841 without scanning: %d, want 403", w.Code)
	}
	if tb, _ := db.GetTable(9841); tb.Status != "available" {
		t.Fatalf("typing the URL claimed the table: %+v", tb)
	}

	// Guessed or wrong token: nothing.
	if w := scan(h, "not-a-real-token", "typist"); w.Code != http.StatusNotFound {
		t.Fatalf("bad token: %d, want 404", w.Code)
	}

	// Scanning claims it and lands on the host page.
	w := scan(h, t1.QRToken, "host")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/table/9841" {
		t.Fatalf("scan: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := viewTable(h, "9841", "host"); w.Code != http.StatusOK {
		t.Fatalf("host page: %d", w.Code)
	}
	// A second phone scanning the same code doesn't take over.
	if w := scan(h, t1.QRToken, "friend"); w.Code != http.StatusOK {
		t.Fatalf("second scanner: %d, want the occupied page", w.Code)
	}
	if tb, _ := db.GetTable(9841); tb.HostSessionID != "host" {
		t.Fatalf("second scanner took the table: %+v", tb)
	}
	if w := viewTable(h, "9841", "friend"); w.Code != http.StatusForbidden {
		t.Fatalf("non-host /table/9841: %d, want 403", w.Code)
	}

	// Moving to another table frees the first.
	t2, _ := db.GetTable(9842)
	scan(h, t2.QRToken, "host")
	if tb, _ := db.GetTable(9841); tb.Status != "available" {
		t.Fatalf("old table still held after the phone moved: %+v", tb)
	}

	// A new code revokes the printed one.
	if _, err := db.RotateTableToken(9842); err != nil {
		t.Fatal(err)
	}
	if w := scan(h, t2.QRToken, "late"); w.Code != http.StatusNotFound {
		t.Fatalf("revoked code still works: %d", w.Code)
	}
}

// The table socket carries the table's orders: only its host may open it.
func TestTableStatusWSRejectsNonHost(t *testing.T) {
	if err := db.CreateTable(9843); err != nil {
		t.Fatal(err)
	}
	db.ClaimTable(9843, "ws-host")
	for _, session := range []string{"", "ws-stranger"} {
		r := withParam(httptest.NewRequest(http.MethodGet, "/table/9843/ws", nil), "number", "9843")
		if session != "" {
			r.AddCookie(&http.Cookie{Name: GuestSessionCookieName, Value: session})
		}
		w := httptest.NewRecorder()
		TableStatusWS(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("session %q: %d, want 403", session, w.Code)
		}
	}
}

func TestQRSheetPNGAndRotate(t *testing.T) {
	if err := db.CreateTable(9844); err != nil {
		t.Fatal(err)
	}
	before, _ := db.GetTable(9844)
	admin := models.User{ID: 1, Username: "admin", Role: models.RoleAdmin}

	w := httptest.NewRecorder()
	AdminQRSheet(w, withPushTestUser(httptest.NewRequest(http.MethodGet, "http://venue.example/admin/map/qr", nil), admin))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "http://venue.example/t/"+before.QRToken) {
		t.Fatalf("sheet: %d, missing the table's link", w.Code)
	}

	w = httptest.NewRecorder()
	AdminTableQRPNG(w, withParam(httptest.NewRequest(http.MethodGet, "/admin/tables/9844/qr.png", nil), "number", "9844"))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(w.Body.String(), "\x89PNG") {
		t.Fatalf("png: %d %q", w.Code, w.Header().Get("Content-Type"))
	}

	w = httptest.NewRecorder()
	r := withParam(httptest.NewRequest(http.MethodPost, "/admin/tables/9844/qr/rotate", nil), "number", "9844")
	AdminRotateTableQR(w, withPushTestUser(r, admin))
	after, _ := db.GetTable(9844)
	if w.Code != http.StatusOK || after.QRToken == before.QRToken || !strings.Contains(w.Body.String(), after.QRToken) {
		t.Fatalf("rotate: %d, token %q → %q", w.Code, before.QRToken, after.QRToken)
	}
}
