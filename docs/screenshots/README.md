# Screenshots

The `.png` files in this folder are real captures of the running app,
referenced from the root `README.md`. They're not automatically kept in
sync with the UI — when a screen changes enough to make one stale, recapture
it and overwrite the same filename; the root README needs no changes as
long as the filename stays put.

## How to recapture one

1. Reset to clean sample data: `scripts/reset-db.sh`
2. Start the server: `go run ./cmd/server`

| File | URL | Log in as | Notes |
|---|---|---|---|
| `guest-menu.png` | `http://localhost:8080/table/1` | — (guest) | Capture at a phone width (~390px) in devtools device mode. |
| `customize-panel.png` | same, tap "Customize" on any item with ingredient tags | — (guest) | Give the product at least one removable and one priced "extra" ingredient first in `/admin` so both chip colors show. |
| `admin-dashboard.png` | `http://localhost:8080/admin` | `admin` / `admin123` | Top of the page — revenue overview + trend chart + low-stock banner. Place and mark a real order paid first so it isn't all zeroes. |
| `admin-menu.png` | same page, scrolled to "Menu categories" / "Menu & inventory" | `admin` / `admin123` | Expand a product's ingredient tags so the green/blue chips are visible. |
| `worker-dashboard.png` | `http://localhost:8080/worker` | `manager` / `manager123` | The manager account sees the full unfiltered order feed — place a guest order first so it isn't empty. |
| `dj-terminal.png` | same URL | `dj` / `dj123` | Submit a song request from a guest tab first so there's something in the queue. |

Export as PNG (1x is fine at these display widths) and overwrite the
matching filename — crop out empty space below the actual content first so
the image doesn't carry a lot of dead background.
