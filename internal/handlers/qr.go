package handlers

import (
	"html/template"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"bitebox/internal/auth"
	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
	"rsc.io/qr"
)

// ----- Table QR codes: the printable sheet on the map editor -----
//
// Each table's QR code opens /t/{token} (see TableHandlers.Scan). The token
// is the secret, so the sheet, the images and "New code" are staff-only
// (RequirePage("map")) and never cached.

// qrBase is the address printed in the codes: PUBLIC_URL (e.g.
// https://order.example.com) when set, else the address this page was
// opened on, which is wrong when printing from localhost.
func qrBase(r *http.Request) string {
	if u := strings.TrimRight(os.Getenv("PUBLIC_URL"), "/"); u != "" {
		return u
	}
	scheme := "http"
	if auth.IsSecureRequest(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// qrBaseLooksLocal: guests' phones can't open a localhost or bare-IP address
// over the internet, so the sheet warns before anyone prints it.
func qrBaseLooksLocal(base string) bool {
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host == "localhost" || net.ParseIP(host) != nil
}

type qrCard struct {
	Number int
	Name   string
	Zone   string
	URL    string
	Ver    string // busts the browser's copy of the image after "New code"
}

func newQRCard(r *http.Request, t models.Table) qrCard {
	return qrCard{Number: t.Number, Name: t.Name(), Zone: t.Zone, URL: qrBase(r) + "/t/" + t.QRToken, Ver: t.QRToken}
}

func AdminQRSheet(w http.ResponseWriter, r *http.Request) {
	tables, err := db.GetAllTables()
	if err != nil {
		http.Error(w, "Failed to load tables", http.StatusInternalServerError)
		return
	}
	cards := make([]qrCard, 0, len(tables))
	for _, t := range tables {
		cards = append(cards, newQRCard(r, t))
	}
	base := qrBase(r)
	w.Header().Set("Cache-Control", "no-store")
	staffPage(w, r, "bk_qr.html", "editor", map[string]interface{}{
		"Cards": cards, "QRBase": base, "QRLocal": qrBaseLooksLocal(base), "PublicURLSet": os.Getenv("PUBLIC_URL") != "",
	})
}

func AdminTableQRPNG(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(chi.URLParam(r, "number"))
	t, err := db.GetTable(n)
	if err != nil {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}
	code, err := qr.Encode(qrBase(r)+"/t/"+t.QRToken, qr.M)
	if err != nil {
		http.Error(w, "Failed to make the QR code", http.StatusInternalServerError)
		return
	}
	code.Scale = 8
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Write(code.PNG()) // #nosec G705 -- a PNG image, sent as image/png
}

// AdminRotateTableQR gives a table a new code: the old printed one stops
// working at once (a lost or copied sticker). Whoever is seated keeps
// their session.
func AdminRotateTableQR(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(chi.URLParam(r, "number"))
	if _, err := db.RotateTableToken(n); err != nil {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}
	t, err := db.GetTable(n)
	if err != nil {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}
	user, _ := UserFromContext(r)
	db.LogAudit(user.Username, "qr", "Table "+t.Name(), "New QR code; the old one no longer works")
	w.Header().Set("Cache-Control", "no-store")
	template.Must(template.ParseFiles("templates/bk_qr.html")).ExecuteTemplate(w, "qr-card", newQRCard(r, t))
}
