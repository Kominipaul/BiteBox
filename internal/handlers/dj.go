package handlers

import (
	"fmt"
	"html"
	"net/http"
	"strconv"

	"bitebox/internal/db"
)

func DJRequest(w http.ResponseWriter, r *http.Request) {
	song := r.FormValue("song")
	tip, err := strconv.ParseFloat(r.FormValue("tip"), 64)

	w.Header().Set("Content-Type", "text/html")

	if song == "" || err != nil || tip <= 0 {
		w.Write([]byte(`<p style='color: #dc3545;'>Please enter a song and a valid tip amount.</p>`))
		return
	}

	if _, err := db.CreateSongRequest(song, tip); err != nil {
		w.Write([]byte(`<p style='color: #dc3545;'>Something went wrong, please try again.</p>`))
		return
	}

	w.Write([]byte(fmt.Sprintf("<p style='color: #28a745;'>✅ Request sent to DJ for '%s' (€%.2f tip)</p>", html.EscapeString(song), tip)))
}
