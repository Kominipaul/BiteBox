package handlers

import (
	"os"
	"path/filepath"
	"testing"

	"bitebox/internal/db"
)

// TestMain runs every test in this package with the working directory
// pointed at a throwaway scratch dir — never the real bitebox.db — while
// still resolving templates/*.html correctly. Handlers use
// template.ParseFiles("templates/...") with a path relative to the process
// CWD (correct for `go run ./cmd/server` from the repo root, but `go test`
// runs with CWD set to the package's own directory), so tests need to
// chdir the same way db package tests do — plus symlink in the real
// templates/ and static/ directories, which db's own tests don't need.
func TestMain(m *testing.M) {
	repoRoot, err := os.Getwd() // internal/handlers/, before any chdir
	if err != nil {
		panic(err)
	}
	repoRoot = filepath.Join(repoRoot, "..", "..")

	dir, err := os.MkdirTemp("", "bitebox-handlers-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	for _, name := range []string{"templates", "static"} {
		if err := os.Symlink(filepath.Join(repoRoot, name), filepath.Join(dir, name)); err != nil {
			panic(err)
		}
	}

	if err := os.Chdir(dir); err != nil {
		panic(err)
	}
	defer os.Chdir(repoRoot)

	db.InitDB()
	os.Exit(m.Run())
}
