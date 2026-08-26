# Screenshots

The `.svg` files in this folder are placeholders referenced from the root `README.md` — dashed-border cards that render cleanly on GitHub instead of broken image icons. Swap them out for real captures when you get a chance; keep the **same filenames** and the root README needs no changes.

## How to capture each one

1. Start the server: `go run ./cmd/server`
2. Run `scripts/reset-db.sh` first if you want clean sample data (empty order history, full stock) in the shots.

| File | URL | Log in as | Notes |
|---|---|---|---|
| `guest-menu.svg` | `http://localhost:8080/table/1` | — (guest) | Capture at a phone width (~390px) in devtools device mode. |
| `customize-panel.svg` | same, tap "Customize" on any item with ingredient tags | — (guest) | Add at least one removable and one extra ingredient to a product first in `/admin` so both colors show. |
| `admin-dashboard.svg` | `http://localhost:8080/admin` | `admin` / `admin123` | Top of the page — revenue overview + trend chart + low-stock banner. |
| `admin-menu.svg` | same page, scrolled to "Menu & inventory" | `admin` / `admin123` | Expand a product's ingredient tags so the green/blue chips are visible. |
| `worker-dashboard.svg` | `http://localhost:8080/worker` | `manager` / `manager123` | The manager account sees the full unfiltered order feed. |
| `dj-terminal.svg` | same URL | `dj` / `dj123` | Submit a song request from a guest tab first so there's something in the queue. |

Export as PNG (1x is fine at these display widths) and replace the matching `.svg` filename — GitHub renders PNG/SVG the same way in Markdown, so you can literally overwrite `guest-menu.svg` with a PNG's bytes only if you also rename the extension in `README.md`, or just save as `guest-menu.png` and update the one `<img src>` line that points to it.
