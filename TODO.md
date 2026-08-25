# BiteBox — Status & TODO

Working notes on what's been built, what's still rough, and ideas for later.
Update this as things change — it's meant to be read before picking up work again.

## Done

### Live updates over WebSockets (replaced polling)
Previously the worker dashboard and guest table page re-fetched HTML fragments
on a timer (`hx-trigger="every 3s"` / `every 5s"`). That's gone — the server now
pushes updates the instant something changes.

- `internal/wshub/hub.go` — minimal topic-based pub/sub hub (`Subscribe`,
  `Unsubscribe`, `Broadcast`), one buffered channel per subscriber.
- `internal/handlers/ws.go` — websocket upgrade handlers (`WorkerOrdersWS`,
  `WorkerDJWS`, `TableStatusWS`), `oobWrap()` helper that wraps a fragment as
  an `hx-swap-oob` div for the htmx `ws` extension, `pumpWS()` (sends an
  initial snapshot on connect, then relays broadcasts until disconnect).
- Mutation handlers (`worker.go`, `table.go`, `cart.go`, `dj.go`) now broadcast
  to the relevant topic after every DB write — new order, status change,
  paid, DJ accept/reject, DJ request.
- Routes: `GET /table/{number}/ws`, `GET /worker/orders/ws`, `GET /worker/dj/ws`
  (the last two behind the existing `RequireRole` middleware, so auth is
  enforced on the socket handshake same as any other worker route).
- Templates (`worker_dashboard.html`, `host_menu.html`) use
  `hx-ext="ws" ws-connect="..."` instead of polling `hx-trigger`s.
- Old plain-HTTP fragment routes (`/worker/orders/feed`, `/worker/dj/feed`,
  `/table/{number}/order-status`) were **kept**, just no longer called by the
  UI — harmless to leave, see "Maybe later" below.
- Verified end-to-end with a throwaway Go websocket client: initial snapshot
  on connect, live push on mutation, and unauthenticated worker socket
  connections correctly rejected (303, not upgraded).
- Added dependency: `github.com/gorilla/websocket`.

### DB reset script (manual, not wired into anything)
- `scripts/reset-db.sh` — deletes `bitebox.db` (+ `-wal`/`-shm`/`-journal`
  siblings) and recreates it via `cmd/resetdb`. Prompts for confirmation;
  pass `-y` to skip. Run it yourself with `./scripts/reset-db.sh` — nothing
  in the app calls it automatically.
- `cmd/resetdb/main.go` — just calls `db.InitDB()`, reusing the server's own
  schema/seed code instead of duplicating SQL in the shell script.
- Reseeds: table 1 (`available`), 4 menu products, users `admin/admin123`
  and `staff/staff123`.

### Fixed: couldn't log in / tables stuck occupied
Root cause was operational, not a code bug: a leftover `go run` server
process from earlier testing was still holding port 8080 with a stale
sqlite file handle (from before a manual db delete+recreate). Killed it;
fresh server + fresh db logged in fine. Real behavior worth remembering:
- Table occupancy is persisted in `bitebox.db`, **on purpose** — a server
  restart alone does not free tables. Use `scripts/reset-db.sh` if you want
  a clean slate.
- If the app ever behaves inconsistently after a restart, check
  `ss -ltnp | grep 8080` for a stray process before assuming a code bug.

### Live menu sync (price/hide changes reach guests, even mid-cart)
- New global `"menu"` websocket topic/route `GET /menu/ws` (`internal/handlers/menu.go`,
  `MenuWS` in `ws.go`) — pushes the guest product list the instant an admin
  creates/edits/hides a product (`AdminCreateProduct`/`AdminUpdateProduct`/
  `AdminToggleProduct` in `admin.go` all call `BroadcastMenu()`).
- **The cart-refresh nudge**: the server doesn't track who has what in their
  cart, so it can't render each guest's cart-summary directly. Instead every
  menu broadcast also ships a tiny OOB "poke" (`cartRefreshTrigger` in
  `menu.go`) — a hidden `#cart-refresh-trigger` div that, once swapped in,
  fires its own `hx-get="/cart/summary"` to refresh `#cart-summary` from that
  guest's own session. Verified this reaches the socket correctly; the actual
  client-side htmx auto-fire wasn't checked in a real browser (see Maybe later).
- `GET /cart/summary` (`CartHandlers.Summary` in `cart.go`) — renders the
  caller's cart fresh from their session cookie; used both for that nudge and
  for the page's own initial load (`host_menu.html`'s `#cart-summary` div now
  starts empty and `hx-get`s this on load, same "start empty, get filled"
  pattern as the other live widgets).
- **Cart items now resolve against live product data on every render**
  (`resolveCart()` in `cart.go`), not the add-time snapshot — so a price
  change or hide shows up immediately, and an item that's gone unavailable
  is flagged (`Unavailable`), excluded from the total, and blocks checkout
  (409) until removed. `host_menu.html`'s own inline product/cart markup was
  removed in favor of this — it's the only rendering path now.

### DJ decision shown live to the guest; payment only implied on accept
- `song_requests` gained a `table_number` column (migration in `db.go`) so a
  request can be traced back to the table that sent it — it had no guest
  linkage at all before this.
- `DJRequest` (`dj.go`) now requires the caller to be hosting a table
  (via the same `resolveHostedTable` cart.go already used) and stores it.
- New `#song-status` widget on `host_menu.html`, live via the same
  `/table/{number}/ws` connection order-status already uses (`renderSongStatusHTML`/
  `BroadcastSongStatus` in `table.go`, `_song_status.html`) — pending →
  "waiting for the DJ"; accepted → "pay €X cash to the DJ/waiter"; rejected →
  "declined, no charge". `WorkerDJAccept`/`WorkerDJReject` in `worker.go`
  broadcast the decision the moment staff acts.
- No real payment gateway exists in this app (everything is "tell the guest
  what they owe, staff collects cash") — "money sent only if accepted" is
  implemented as messaging, not a new payment/mark-paid flow. If a "DJ mark
  tip paid" workflow (like orders have) turns out to be wanted, that's a
  separate ask — flagged in Maybe later.
- Worker DJ feed (`_dj_feed.html`) now also shows which table sent each
  request, using the new column.

### Stock tracking
- The `products.stock` column already existed (schema + `models.Product`)
  but was never surfaced or enforced anywhere. Convention: `-1` = unlimited/
  untracked (the column default), `>=0` = a real count.
- Admin can now set/edit it (`admin_dashboard.html` / `_product_list.html`
  stock input, `db.CreateProduct`/`UpdateProduct` take a `stock` param).
- Guest menu shows "Only N left" (≤5) or "Sold Out" (`_guest_product_list.html`);
  the Add button is disabled at 0.
- `CartHandlers.Add` soft-checks stock vs. what's already in that cart before
  allowing another add (409 if it would exceed stock).
- The **hard** guarantee is in `db.CreateOrder` (`orders.go`): each item's
  stock is decremented atomically inside the order transaction
  (`UPDATE ... WHERE stock = -1 OR stock >= ?`); if two guests race for the
  last unit, the loser's whole order rolls back with `ErrInsufficientStock`
  (surfaced to that guest as a 409 asking them to review their cart).
- Checkout also calls `BroadcastMenu()` on success, so a sellout is reflected
  live for everyone else browsing, not just the buyer.

Verified all three of the above end-to-end with the same throwaway websocket
client approach as the earlier polling→websocket work: admin creating a
stock=1 product pushed live to a connected menu socket; adding it to a cart
then hiding it server-side flagged it unavailable and blocked checkout;
submitting/accepting/rejecting a DJ request pushed the right message to the
right table; buying the last unit pushed "Sold Out" live and a second add
attempt correctly 409'd.

### Fixed: "Today's Overview" revenue/order counter not updating
Two separate problems, both fixed:
1. **The numbers were never live** — `AdminHome` computed them once at page
   load and nothing pushed changes after that. Fixed the same way as
   everything else in this app: new `"admin:stats"` websocket topic/route
   `GET /admin/stats/ws` (`internal/handlers/stats.go`, `AdminStatsWS` in
   `ws.go`), admin dashboard's stat cards wrapped in `hx-ext="ws"
   ws-connect="/admin/stats/ws"` with `id="admin-stats"`. `BroadcastAdminStats()`
   is called from the two places that actually change either number:
   `cart.Checkout` (order count) and `WorkerMarkPaid` (revenue, since only
   paid orders count).
2. **The bigger bug**: `GetTodayRevenue`/`GetTodayOrderCount` (`db/orders.go`)
   compared `date(created_at)` — a bare UTC date, since SQLite's
   `CURRENT_TIMESTAMP` default is UTC — against `date('now', 'localtime')`, a
   local date. On this machine (UTC+3) that meant orders placed between local
   midnight and ~3am silently didn't count as "today" at all. Fixed by
   applying `'localtime'` to *both* sides of the comparison. This was
   probably the real cause of "not updating" — worth remembering if numbers
   ever look wrong again: check for exactly this kind of bare-vs-localtime
   date mismatch before assuming it's a caching/live-update problem.
   `admin_dashboard.html`'s order-history timestamps now also render via
   `.CreatedAt.Local` for the same reason (they were displaying UTC).

### Order history (all-time, not just today)
- Every order was already fully persisted (`orders`/`order_items` tables) —
  nothing needed to change there. Added `db.GetOrderHistory(limit)`
  (`orders.go`) — all orders regardless of status, newest first, items
  populated, capped at `orderHistoryLimit = 200` (a sane bound, not real
  pagination — see Maybe later).
- New "📜 Order History" section in `admin_dashboard.html`, inside a
  `.scroll-panel` (`max-height: 420px; overflow-y: auto`) so it doesn't turn
  the whole admin page into an endless scroll — the page stays fixed-height,
  only that panel scrolls. Shows table #, order #, local timestamp, full item
  list, status badges, and total per order.
- Deliberately **not** live-updated over websocket (unlike everything else
  in this app) — it's a look-back view, not something that needs to change
  under the admin's cursor. Reflects the state as of the last time `/admin`
  was loaded/refreshed.

## Missing / rough edges

- **No graceful shutdown.** `main.go` calls `http.ListenAndServe` directly
  with no signal handling — Ctrl+C kills the process mid-request/mid-socket
  rather than draining connections. Low priority for local dev, worth fixing
  before any real deployment.
- **No tests.** Nothing in `internal/` or `cmd/` has unit or integration
  tests — not the wshub, not the handlers, not the cart logic.
- **`bitebox.db` is committed to git.** Works fine for a single-dev demo app,
  but it means every test session's leftover data is one `git status` away
  from getting committed by accident (already happened once this session —
  had to `git checkout -- bitebox.db` to undo). Consider `.gitignore`-ing it
  and relying on `scripts/reset-db.sh` / seed-on-first-run instead.
- **Default seeded passwords** (`admin123` / `staff123`) — the server logs a
  warning about this on every fresh seed, but there's no forced change flow.
  Fine for local/demo use; must change before any real deployment.
- **No table auto-release.** If a guest just closes the tab without hitting
  "Leave Table", the table stays occupied until a worker/admin force-releases
  it from `/admin`. No timeout, no heartbeat.
- **The cart-refresh-trigger poke hasn't been checked in a real browser.**
  Confirmed server-side that the OOB fragment is correct and reaches the
  socket; haven't confirmed htmx actually auto-fires `hx-trigger="load"` on
  an OOB-swapped-in element in a live page. If it turns out it doesn't, the
  fallback is trivial (broadcast the resolved cart HTML per-table directly
  instead of the poke — the tricky part, iterating occupied tables/sessions,
  is already sketched in this file's history if needed).
- **DJ song-status only tracks the latest request per table.** If a guest
  sends a second request before the first is resolved, the widget switches
  to showing the new one; the first's outcome is still in the DB but no
  longer surfaced to that guest.
- **No "DJ tip paid" workflow.** Unlike orders (`payment_status` + "Mark
  Paid"), an accepted song request has no staff-side way to record the tip
  as collected. Only add this if actually asked for — see above.

## Maybe later (not needed now, just noted)

- Remove the now-unused plain-HTTP polling routes
  (`/worker/orders/feed`, `/worker/dj/feed`, `/table/{number}/order-status`,
  their handlers `WorkerOrdersFeed`/`WorkerDJFeed`/`OrderStatusPoll`) once
  we're confident nothing external depends on them — currently kept as a
  safety net / manual-debugging fallback.
- Verify behavior across a server restart: does htmx's `ws` extension
  auto-reconnect the socket, or does the widget go silently stale until a
  manual page reload? Not yet tested.
- If this ever sits behind a reverse proxy / HTTPS, confirm the proxy passes
  through `Upgrade`/`Connection` headers for the websocket routes, and that
  the `ws://` vs `wss://` scheme htmx infers from the page matches.
- Rate limiting / abuse protection on guest-facing endpoints (cart add/remove,
  DJ tip requests) — currently unlimited.
- Real pagination for order history once a venue has done more than
  `orderHistoryLimit` (200) orders all-time — right now the 201st-oldest
  order onward just isn't shown, there's no "load more."
