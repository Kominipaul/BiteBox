package handlers

import (
	"strings"
	"testing"
	"time"
)

// TestBroadcastMenuNeverTouchesModalOrTabBar pins what a live menu push may
// replace. /menu/ws OOB-swaps #product-list (plus #cart-refresh-trigger and
// #dj-section when DJ requests are on). It must never carry the Customize
// modal (#customizeOverlay/#customizeChips, populated from a snapshot at
// open time) or the search box (#menuSearch, outside #product-list so what a
// guest is typing survives). The section tabs DO ride inside #product-list
// since Menus & hours: they follow the schedule (a menu ending takes its
// categories away), and host_menu.html remembers the selected tab and
// re-applies it after every swap.
func TestBroadcastMenuNeverTouchesModalOrTabBar(t *testing.T) {
	ch := Hub.Subscribe(topicMenuZone("")) // tables without an area
	defer Hub.Unsubscribe(topicMenuZone(""), ch)

	BroadcastMenu()

	select {
	case msg := <-ch:
		body := string(msg)
		if !strings.Contains(body, `id="product-list"`) {
			t.Errorf("expected the push to include #product-list, got: %s", body)
		}
		forbidden := []string{"customizeOverlay", "customizeChips", "menuSearch"}
		for _, id := range forbidden {
			if strings.Contains(body, id) {
				t.Errorf("live menu push must never touch %q — it would reset the modal/search state guests may have mid-interaction; got: %s", id, body)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for BroadcastMenu's push")
	}
}
