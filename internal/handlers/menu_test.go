package handlers

import (
	"strings"
	"testing"
	"time"
)

// TestBroadcastMenuNeverTouchesModalOrTabBar is a regression test for the
// TODO.md item "Customize-panel/category-tab state resets on every live
// menu push" — investigated live (a real websocket connection, toggling a
// product back and forth, dumping the raw frames) and found not
// reproducible: /menu/ws only ever OOB-swaps #product-list (plus
// #cart-refresh-trigger and, when DJ requests are on, #dj-section/
// #djConfirm) — never #customizeOverlay/#customizeChips (the modal
// host_menu.html's JS populates from a snapshot at open time, so it can't
// be affected by a swap of an element it isn't part of) or .cat-tabs (a
// sibling bar outside the swapped subtree entirely, whose "active" tab
// selection host_menu.html's applyCategoryFilter already re-applies after
// every afterSwap/oobAfterSwap). This test pins that contract so a future
// change to BroadcastMenu can't silently start sending OOB swaps for either
// element without a test noticing.
func TestBroadcastMenuNeverTouchesModalOrTabBar(t *testing.T) {
	ch := Hub.Subscribe(topicMenu)
	defer Hub.Unsubscribe(topicMenu, ch)

	BroadcastMenu()

	select {
	case msg := <-ch:
		body := string(msg)
		if !strings.Contains(body, `id="product-list"`) {
			t.Errorf("expected the push to include #product-list, got: %s", body)
		}
		forbidden := []string{"customizeOverlay", "customizeChips", "cat-tabs", `class="cat-tab`}
		for _, id := range forbidden {
			if strings.Contains(body, id) {
				t.Errorf("live menu push must never touch %q — it would reset the modal/tab-bar state guests may have mid-interaction; got: %s", id, body)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for BroadcastMenu's push")
	}
}
