package handlers

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"bitebox/internal/db"
)

// AdminDownloadBackup streams a consistent point-in-time snapshot of the
// live database. SQLite's own `VACUUM INTO` is the safe way to copy a live
// WAL-mode database — unlike a raw copy of bitebox.db, it doesn't need the
// -wal/-shm sidecars copied alongside it and can't produce a torn read
// mid-write the way copying the plain file while a transaction is in
// flight could. Written to a temp file first (VACUUM INTO targets a path,
// not a response writer), streamed, then removed — nothing about this
// endpoint leaves a copy sitting on disk afterward.
func AdminDownloadBackup(w http.ResponseWriter, r *http.Request) {
	tmpFile, err := os.CreateTemp("", "bitebox-backup-*.db")
	if err != nil {
		http.Error(w, "Failed to prepare backup", http.StatusInternalServerError)
		return
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	if _, err := db.DB.Exec("VACUUM INTO ?", tmpPath); err != nil {
		http.Error(w, "Failed to create backup", http.StatusInternalServerError)
		return
	}

	filename := fmt.Sprintf("bitebox-backup-%s.db", time.Now().Format("2006-01-02-1504"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	http.ServeFile(w, r, tmpPath)
}
