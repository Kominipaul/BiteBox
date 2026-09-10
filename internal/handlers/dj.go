package handlers

import (
	"fmt"
	"html"
	"math"
	"net/http"
	"strconv"

	"bitebox/internal/db"
)

// maxSongTip bounds a guest-entered tip against both nonsense (NaN/Inf slip
// past a bare tip <= 0 check — strconv.ParseFloat happily parses "NaN" and
// "+Inf", and NaN compares false against everything, including <= 0) and
// implausible amounts reaching the database and the admin revenue stats.
const maxSongTip = 1000.0

func DJRequest(w http.ResponseWriter, r *http.Request) {
	table, _, err := resolveHostedTable(r)
	if err != nil {
		http.Error(w, "You are not currently hosting a table", http.StatusForbidden)
		return
	}
	if !AllowDJRequest(r) {
		http.Error(w, "Too many song requests — please slow down", http.StatusTooManyRequests)
		return
	}

	if settings, err := db.GetSettings(); err != nil || !settings.DJRequestsEnabled {
		http.Error(w, "Song requests are currently disabled", http.StatusForbidden)
		return
	}

	song := r.FormValue("song")
	tip, err := strconv.ParseFloat(r.FormValue("tip"), 64)

	w.Header().Set("Content-Type", "text/html")

	if song == "" || err != nil || math.IsNaN(tip) || math.IsInf(tip, 0) || tip <= 0 || tip > maxSongTip {
		w.Write([]byte(`<p class="empty-note" style="color:var(--danger);">Please enter a song and a valid tip amount (max €1000).</p>`))
		return
	}

	if _, err := db.CreateSongRequest(song, tip, table.Number); err != nil {
		w.Write([]byte(`<p class="empty-note" style="color:var(--danger);">Something went wrong, please try again.</p>`))
		return
	}

	if b, err := renderDJFeedHTML(); err == nil {
		BroadcastDJFeed(b)
	}
	BroadcastSongStatus(table.Number)

	w.Write([]byte(fmt.Sprintf(`<div class="dj-confirm show"><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg> Sent to the DJ — '%s' (€%.2f tip)</div>`, html.EscapeString(song), tip)))
}
