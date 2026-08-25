// resetdb recreates bitebox.db from scratch: schema + seed data, via the
// same db.InitDB used by the server. It does not delete the file itself —
// that's scripts/reset-db.sh's job — it just rebuilds whatever file is (or
// isn't) there.
package main

import (
	"bitebox/internal/db"
)

func main() {
	db.InitDB()
}
