// resetpassword is the way back in after a forgotten password or a lost
// phone: it gives an account a random one-time password, turns two-step
// login off (an admin sets it up again right after logging in), signs every
// device out, and forces a new password on the next login.
//
//	go run ./cmd/resetpassword admin
package main

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"log"
	"os"
	"strings"

	"bitebox/internal/auth"
	"bitebox/internal/db"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/resetpassword <username>")
		os.Exit(2)
	}
	username := os.Args[1]
	db.InitDB()
	user, err := db.GetUserByUsername(username)
	if err != nil {
		log.Fatalf("No account called %q.", username) // #nosec G706 -- %q escapes it; the operator typed it
	}
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		log.Fatal(err)
	}
	temp := strings.ToLower(base32.StdEncoding.EncodeToString(b)) // 16 characters, 80 random bits
	hash, err := auth.HashPassword(temp)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.SetUserPassword(user.ID, hash); err != nil {
		log.Fatal(err)
	}
	if err := db.RequirePasswordChange(user.ID); err != nil {
		log.Fatal(err)
	}
	if err := db.DisableTOTP(user.ID); err != nil {
		log.Fatal(err)
	}
	if err := db.SetUserActive(user.ID, true); err != nil {
		log.Fatal(err)
	}
	if err := db.DeleteAuthSessionsForUser(user.ID); err != nil {
		log.Fatal(err)
	}
	db.LogAudit("server console", "reset_password", username, "Reset with cmd/resetpassword; two-step login turned off")
	fmt.Printf("Reset %q: log in with the one-time password %s, then pick a new one", username, temp)
	if user.NeedsTwoFactor() {
		fmt.Print(" and set up two-step login again")
	}
	fmt.Println(".")
}
