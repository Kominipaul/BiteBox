package handlers

import (
	"encoding/json"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"

	"bitebox/internal/db"
)

// IconVersion returns a cache-busting value derived from
// static/icons/icon-192.png's own last-modified time — appended as
// ?v=<this> to every apple-touch-icon URL (see host_menu.html and
// worker_dashboard.html).
//
// Exists because of a real, live-diagnosed iOS bug: Safari's "Add to Home
// Screen" reads the apple-touch-icon link tag, not the web manifest's icons
// array, and caches whatever it fetched there *by URL* — regenerating
// static/icons/icon-192.png (scripts/gen-icons.go) changes the file on
// disk, but a phone that already fetched the old bytes at that exact URL
// keeps showing the stale icon indefinitely, even across a fresh "Add to
// Home Screen." Baking the file's mtime into the URL means a changed icon
// is automatically a *different* URL, which is the only thing that
// actually busts this cache — a manually-maintained version number would
// work too but would silently go stale the next time someone edits
// gen-icons.go and forgets to bump it.
func IconVersion() string {
	info, err := os.Stat("static/icons/icon-192.png")
	if err != nil {
		return "1" // file missing is a build/deploy problem elsewhere, not something to fail a page render over
	}
	return strconv.FormatInt(info.ModTime().Unix(), 10)
}

// DebugPushClientState is TEMPORARY diagnostic-only instrumentation — logs
// whatever sw.js's push handler observed about open window clients
// (focused/visibilityState) at the exact moment a push arrived, so a
// "still got notified with the app open" report can be checked against
// real device data instead of guessed at. Remove this (and the matching
// fetch() call in static/sw.js) once the on-screen-suppression logic is
// confirmed actually working on a real iPhone.
func DebugPushClientState(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	body, _ := io.ReadAll(r.Body)
	log.Printf("push-debug: client state at push time: %s", body)
	w.WriteHeader(http.StatusNoContent)
}

// Landing serves the bare domain root — anyone who lands here typed the
// address directly or bookmarked it; every real guest arrives via a
// table's own QR code straight into TableHandlers.View instead. Point them
// at a table's QR code rather than leaving chi's plain-text 404 as the
// entire first impression of the app.
func Landing(w http.ResponseWriter, r *http.Request) {
	settings, _ := db.GetSettings()
	tmpl := template.Must(template.ParseFiles("templates/landing.html"))
	tmpl.Execute(w, map[string]interface{}{"VenueName": settings.VenueName})
}

// NotFound renders a branded 404 in place of net/http's plain-text default,
// for any route (guest, staff, or a stale/mistyped link) that doesn't match.
func NotFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	tmpl := template.Must(template.ParseFiles("templates/not_found.html"))
	tmpl.Execute(w, nil)
}

// ServiceWorker serves the phone-notification service worker at the origin
// root rather than under /static/* — a service worker's default control
// scope is the directory of the URL it's served *from*, not where the file
// physically lives, so this is what lets it receive push events and handle
// notification clicks for the whole app instead of just one subdirectory.
func ServiceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	// A service worker file needs this explicitly — with no Cache-Control
	// at all, a browser can (and iOS Safari has been documented to)
	// apply its own heuristic caching to it instead of always
	// revalidating, meaning an edit to sw.js might never actually reach
	// an already-installed device: it keeps running the old cached script
	// indefinitely, no error, no visible sign anything's stale. Real
	// updates to the actual logic still need every open window/tab
	// closed and the app relaunched (self.skipWaiting() +
	// clients.claim() below only take effect on the next activation, not
	// mid-session) — this header is what makes that relaunch actually
	// fetch fresh bytes instead of a cached copy of the old file.
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, "static/sw.js")
}

// WebManifest serves the staff dashboard's PWA manifest at the origin
// root, for the same "add to home screen" flow the push-notification setup
// depends on — iOS in particular treats a home-screen-installed page and a
// plain Safari tab of the same URL as different notification permission
// contexts, so this is what makes "Add to Home Screen" actually offered.
//
// Generated rather than served from static/manifest.json (removed — this
// replaces it) so its icon URLs carry the same IconVersion cache-busting
// query param GuestWebManifest's do. See IconVersion's doc comment for the
// actual bug this fixes: without it, a regenerated icon
// (scripts/gen-icons.go) never reaches a device that already cached the
// old one at this exact URL.
func WebManifest(w http.ResponseWriter, r *http.Request) {
	v := IconVersion()
	m := webManifest{
		Name:            "BiteBox Worker",
		ShortName:       "BiteBox",
		Description:     "Live order feed and phone alerts for kitchen, bar, and waitstaff.",
		StartURL:        "/worker",
		Scope:           "/",
		Display:         "standalone",
		BackgroundColor: "#14100D",
		ThemeColor:      "#D9A441",
		Icons: []webManifestIcon{
			{Src: "/static/icons/icon-192.png?v=" + v, Sizes: "192x192", Type: "image/png", Purpose: "any"},
			{Src: "/static/icons/icon-512.png?v=" + v, Sizes: "512x512", Type: "image/png", Purpose: "any"},
		},
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	json.NewEncoder(w).Encode(m)
}

// webManifest is the shape both WebManifest (static, for the staff PWA) and
// GuestWebManifest (generated, below) emit — see the W3C Web App Manifest
// spec for what each field does; kept to just what this app actually sets.
type webManifest struct {
	Name            string            `json:"name"`
	ShortName       string            `json:"short_name"`
	Description     string            `json:"description"`
	StartURL        string            `json:"start_url"`
	Scope           string            `json:"scope"`
	Display         string            `json:"display"`
	BackgroundColor string            `json:"background_color"`
	ThemeColor      string            `json:"theme_color"`
	Icons           []webManifestIcon `json:"icons"`
}

type webManifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose"`
}

// GuestWebManifest serves a per-table PWA manifest — unlike WebManifest
// (the staff dashboard's, a fixed start_url), a guest's start_url has to
// vary: host_menu.html links this with the actual table
// number in the query string (?table=N), so "Add to Home Screen" installs
// an icon that reopens *this* table, not the staff dashboard or the bare
// landing page.
//
// This matters more than it sounds: iOS launches a home-screen PWA at the
// manifest's start_url, not at whatever URL was on screen when "Add to Home
// Screen" was tapped. Without a per-table start_url, every guest at every
// table would get an icon that reopens the same fixed page — for a
// QR-per-table ordering flow, that's the difference between the icon
// actually being useful on a second visit and it just being a way to
// unlock push permission once and never mattering again.
//
// The table number is guest-supplied and deliberately trusted as nothing
// more than "which URL should this icon open" — same trust level as any
// other guest-facing link on the menu (it's not used to authorize
// anything; resolveHostedTable-style session checks still gate every real
// action once that URL loads).
func GuestWebManifest(w http.ResponseWriter, r *http.Request) {
	startURL := "/"
	if n, err := strconv.Atoi(r.URL.Query().Get("table")); err == nil && n > 0 {
		startURL = "/table/" + strconv.Itoa(n)
	}
	settings, _ := db.GetSettings()
	name := settings.VenueName
	if name == "" {
		name = "BiteBox"
	}
	v := IconVersion()
	m := webManifest{
		Name:            name,
		ShortName:       "Menu",
		Description:     "Order from your table and get notified the moment it's ready.",
		StartURL:        startURL,
		Scope:           "/",
		Display:         "standalone",
		BackgroundColor: "#211D18",
		ThemeColor:      "#211D18",
		Icons: []webManifestIcon{
			{Src: "/static/icons/icon-192.png?v=" + v, Sizes: "192x192", Type: "image/png", Purpose: "any"},
			{Src: "/static/icons/icon-512.png?v=" + v, Sizes: "512x512", Type: "image/png", Purpose: "any"},
		},
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	json.NewEncoder(w).Encode(m)
}
