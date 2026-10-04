package handlers

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"bitebox/internal/db"
	"bitebox/internal/models"
)

// TestActiveStationsFollowCategories: a venue that only sells drinks (a
// club) gets just the bar on Menus & hours; one with no categories yet
// gets both.
func TestActiveStationsFollowCategories(t *testing.T) {
	db.CreateCategory("Test Active Bar", models.DepartmentBar)
	var kitchen []int
	cats, err := db.GetCategories()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cats {
		if c.Department == models.DepartmentKitchen {
			kitchen = append(kitchen, c.ID)
			db.DB.Exec("UPDATE categories SET department = ? WHERE id = ?", models.DepartmentBar, c.ID)
		}
	}
	t.Cleanup(func() {
		for _, id := range kitchen {
			db.DB.Exec("UPDATE categories SET department = ? WHERE id = ?", models.DepartmentKitchen, id)
		}
	})
	if got := activeStations(); !slices.Equal(got, []string{models.DepartmentBar}) {
		t.Fatalf("drinks-only venue: got %v, want just the bar", got)
	}
	if got := stationViews(); len(got) != 1 || got[0].Dept != models.DepartmentBar {
		t.Fatalf("station cards: got %d, want only the bar", len(got))
	}
}

// TestGuideRendersForEveryShell: the owner gets the guide in the back
// office on the owner's part; a kitchen account gets it inside the staff
// app, opened on the kitchen & bar part.
func TestGuideRendersForEveryShell(t *testing.T) {
	for _, c := range []struct {
		user      models.User
		who, mark string
	}{
		{models.User{Username: "owner", Role: models.RoleAdmin}, `value="owner" checked`, `class="side"`},
		{models.User{Username: "cook", Role: models.RoleWorker, Department: models.DepartmentKitchen}, `value="station" checked`, `class="wtabs"`},
	} {
		r := httptest.NewRequest("GET", "/admin/guide", nil)
		r = r.WithContext(context.WithValue(r.Context(), userContextKey, c.user))
		w := httptest.NewRecorder()
		AdminGuide(w, r)
		body := w.Body.String()
		if !strings.Contains(body, "How the rules combine") || !strings.Contains(body, c.who) || !strings.Contains(body, c.mark) {
			t.Errorf("%s: guide missing content, role or shell (status %d)", c.user.Username, w.Code)
		}
	}
}
