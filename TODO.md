# BiteBox — Status & TODO

Working notes on what's been built, what's still rough, and ideas for later.
Update this as things change — it's meant to be read before picking up work again.

## Done

### DJ song requests are now an admin-controlled venue toggle
Some venues don't run a DJ / don't want song requests taking tips right now —
the "Request a song to DJ" form is no longer permanently on the guest menu.
New singleton `settings` table (`id=1` row, `dj_requests_enabled`, defaults
on) via `db.GetSettings`/`db.SetDJRequestsEnabled`. Admin dashboard gets a new
"DJ song requests" panel (enabled/disabled pill + toggle button, posts to
`/admin/settings/dj-requests/toggle`).

Guest-facing behavior is genuine absence, not a greyed-out state: the form
lives in its own partial (`_dj_section.html`, `{{ if .DJRequestsEnabled }}`)
pushed as an OOB fragment into `#dj-section` over the *existing* `/menu/ws`
connection (reused rather than adding a new socket — it's a venue-wide
setting, not per-table, and OOB swaps target by element id regardless of
which connection delivers them). Toggling in the admin dashboard broadcasts
live: a guest already looking at the menu sees the form vanish/reappear with
no reload.

Deliberately kept `#song-status` (the DJ's accept/reject decision widget)
structurally separate and always present, wired to the *other* existing
socket (`/table/{number}/ws`) instead of `/menu/ws` — two reasons: (1) a
guest who already sent a request before the admin disabled the feature
should still see its outcome, not have it vanish along with the form: and
(2) `/menu/ws`'s initial payload is async relative to `/table/.../ws`'s, so
folding song-status into the same gated fragment would race whichever
connection's snapshot lands first. `_song_status.html` now wraps its content
in a `.section` card only when there's an actual request to show, so an
idle table doesn't display an empty bordered box.

Also closed a gap the toggle alone wouldn't have caught: hiding the form
client-side doesn't stop a guest from `POST`ing `/dj/request` directly.
`DJRequest` (dj.go) now re-checks `db.GetSettings().DJRequestsEnabled`
server-side and 403s if the feature is off — the toggle is authoritative,
not just cosmetic. Verified end-to-end over a live websocket connection:
initial `/menu/ws` payload includes the form when enabled; toggling off
mid-connection broadcasts an OOB swap that removes it; direct POSTs are
rejected with the feature off and accepted with it on; re-enabling restores
the form live.

### Extra ingredients now show in the guest customize panel; investigated (not a bug) "everything served by admin"
**Extras were completely missing from the guest UI** — the customize panel
only ever rendered `kind == "removable"` ingredients; "extra" ones existed
in the data model (admin could tag them) but had zero guest-facing path.
Unified into one interaction, matching the color language asked for:
- Removable ingredients start **green** ("included by default"); tapping
  excludes them (greys out, strikethrough — unchanged).
- Extra ingredients start **blue** ("available, not included"); tapping
  adds them, turning **green** — the same "this is on your order" color a
  kept removable ingredient has. Tapping again reverts to blue.
- One consistent rule: green always means "on your order," for both kinds.

Backend needed a real second parallel field to support this — a cart line's
identity now depends on *two* customization sets, not one:
`models.OrderItem.Extras []string` alongside the existing `Excluded`, a new
`order_items.extra_ingredients` column (same JSON-snapshot convention),
`cart.Store.Add`/`Remove` now key a line by `(productID, excluded set,
extras set)` — `sameExclusions` renamed `sameSet` and reused for both.
`validCustomization` (cart.go) replaced the removable-only validator,
checking submitted `excluded`/`extras` names against the product's actual
ingredients of each specific kind before trusting either. Extras are free
(no price change) — nothing asked for pricing them, so nothing invents it.
`_guest_product_list.html`'s "Customize" link now shows whenever a product
has *any* ingredient tag (`HasCustomizableIngredient`, was removable-only).
Extras display everywhere exclusions already did (worker feed, guest's
placed-orders list, cart summary) — same treatment, blue-tinted instead of
muted.

Verified end-to-end: a product tagged with both a removable and an extra
ingredient showed both chips correctly colored; adding "no onion", "+extra
cheese", and "no onion +extra cheese" of the same product produced three
genuinely distinct cart lines (not merged); all three exclusion/extra sets
persisted correctly through checkout and displayed correctly to both the
kitchen and the guest.

**Investigated "all orders seem to be served by admin"** — tested
exhaustively (waiter marks served → `served_by = "waiter"`; manager marks
served → `"manager"`; admin marks served → `"admin"`), and the attribution
is correct in every case: `WorkerUpdateOrderStatus` always records
whichever account's session actually performed the action, with no
special-casing anywhere that could override it. **This is not a bug.** The
likely explanation: testing everything through a single `admin` login,
which — as the superuser bypass — legitimately *can* act on any order from
any department, so of course it gets recorded as who did it. To see
per-role attribution, use the actual seeded department accounts (see
below) instead of doing every step through `admin`.

### Fixed: guest "Customize" did nothing (real bug) + 5-department restructure + polish
**Root cause of the customize bug, found and fixed**: `host_menu.html`'s
click handler for the menu grid was bound directly to `#product-list` via
`document.getElementById('product-list').addEventListener(...)`. But
`#product-list` gets fully replaced (`hx-swap-oob="true"` defaults to
*outerHTML*, a brand-new DOM node) by `/menu/ws`'s very first push, which
arrives almost immediately after page load — orphaning that listener before
a guest could realistically click anything. Every *other* listener in this
app already correctly delegates from `document`/`document.body` (admin
dashboard, worker dashboard) — this was the one place that didn't. Fixed by
switching to body-delegated listeners (`e.target.closest('#product-list
.item-card')`), and converted the `#cart-summary` listeners the same way
too even though they weren't actually broken (that div is only ever
innerHTML-swapped, never OOB-replaced) — for defense-in-depth/consistency,
so a future change to how it's updated can't silently reintroduce this
exact bug. **General lesson for this codebase**: any custom (non-htmx)
`addEventListener` must delegate from a stable ancestor, never bind
directly to an element that any websocket topic OOB-targets — worth
checking first whenever a client-side interaction "does nothing" on a
live-updated page.

**Department model restructured** — the old single "staff" department
conflated two different jobs. Now five, matching what was asked for:
`superworker` (renamed from "staff" — manages/sees literally everything),
`waiter` (new — sees *only* orders marked `ready` by kitchen/bar, any
category; never sees pending/preparing at all, that's not their job),
`bar`/`kitchen` (unchanged: category-filtered, pending/preparing only,
drops out once marked ready), `dj` (unchanged). New `db.GetReadyOrders()`
for the waiter's cross-category ready-only feed;
`orderFeedBucket()`/`renderOrderFeedHTML()`/`BroadcastOrderFeed()` in
worker.go now switch on four order-visible buckets instead of two. Verified
the exact handoff end-to-end with the new seed accounts: kitchen marks an
order ready → vanishes from kitchen's own feed → waiter's feed (which
showed nothing while it was pending/preparing) now shows it with a READY
badge → waiter marks served → gone everywhere, `served_by` correctly
recorded as the waiter.

**One seed account per department**, replacing the old generic
admin/staff pair: `admin`/`admin123` (role=admin), `manager`/`manager123`
(worker, superworker), `waiter`/`waiter123`, `bar`/`bar123`,
`kitchen`/`kitchen123`, `dj`/`dj123` — all in `seedDefaultUsers` (db.go),
one place, so `scripts/reset-db.sh`'s success message no longer hardcodes
a second (and now provably driftable) copy of the account list — it just
points at the server's own startup log line instead.

**Fixed: seeded products all landed in "Other."** The seed INSERT never
set a category, silently relying on the column default — Mojito/Heineken
now seed as Drinks, Cheeseburger/Club Sandwich as Food.

**Polish**: the admin ingredient chips' delete `×` was `color:inherit`
(picking up the pill's own green/blue), not red — fixed to
`var(--danger)`, and moved off inline styles onto real CSS classes
(`.ingredient-tag`, `.ingredient-tag-remove`, `.ingredient-add-form` in
admin_dashboard.html) since the inline-styled version I'd bolted on
in a hurry looked visibly cramped next to the rest of the polished
dashboard. Also caught a real inconsistency while looking for "remove
should be red": the table grid's "Remove" (permanent delete) was styled as
neutral gray while "Force release" (a milder, reversible action) was
already red — backwards. Both are `.btn-outline-danger` now.

### Guest UI rebuild, ingredient customization, order cancellation/refunds, ready→served handoff
The user supplied a new dark-themed guest ordering page design and asked for
it to be fully wired up, plus several backend-only asks bundled into the
same message. In order of how much backend they actually needed:

**Ingredient customization — a real, new data model.** Guests can now
exclude default ingredients per line (e.g. "Cheeseburger, no onion"), and a
different exclusion set is a genuinely separate cart line, not a quantity
bump on the same one:
- New `product_ingredients` table (`id, product_id, name, kind`) — `kind`
  is `"removable"` (default-included, guest can exclude — the only kind
  the guest UI acts on) or `"extra"` (admin can tag these too, but no
  guest-facing add-on/pricing flow exists yet — storing the data model
  now means that's a template change later, not a schema one).
- `cart.Store.Add`/`Remove` now key a line by `(productID, sorted excluded
  set)`, not just `productID` — `sameExclusions()` does the order-
  independent comparison. `models.OrderItem` gained `Excluded []string`,
  persisted through checkout as a JSON snapshot in a new
  `order_items.excluded_ingredients` column (same "immutable at order
  time" reasoning as the existing name/price snapshot).
- Never trust client-supplied ingredient names: `validRemovableExcluded`
  (cart.go) filters whatever the guest's request claims down to that
  product's actual removable ingredients before it ever reaches the cart.
- Admin manages ingredients inline per product row (`_product_list.html`)
  — add via a small form, remove via a `×` on each chip
  (`AdminAddIngredient`/`AdminDeleteIngredient`, admin.go).
- `db.GetIngredientsByProductIDs` batch-fetches for *all* products in one
  query — both the guest menu and admin product list render N products
  without N+1 ingredient queries, deliberately, given the performance
  complaint elsewhere in the same message.

**Order cancellation + refunds, guest-side.** Workers already had
cancellation (see the previous entry below); guests didn't.
`GuestCancelOrder` (guest_orders.go) — unpaid orders cancel instantly
(nothing to reverse); already-paid orders can't self-cancel (no payment
gateway exists to reverse a charge through) — instead `db.RequestRefund`
flags the order (`refund_requested`), which surfaces immediately to staff
(a badge on their order card) and to the guest ("refund requested, ask
staff"). Staff complete the actual cancellation themselves once they've
handled the cash refund in person — this is the "if paid, a refund option
should appear" ask, implemented as a flag-and-notify flow rather than any
automated money movement, since none exists in this app.

**Fixed a real gap my own earlier change had introduced: kitchen/bar
marking something done made it vanish for everyone, including the waiter
who needed to go deliver it.** The previous session's "served = terminal,
drops from every feed" design didn't distinguish "kitchen is done cooking"
from "a waiter delivered it to the table" — those are different people's
jobs. Added a `ready` status between `preparing` and `served`
(`OrderStatusFlow` is now pending→preparing→ready→served→completed).
Department-scoped feeds now have two different exclusion sets instead of
one shared one (orders.go): `GetActiveOrdersByCategory` (bar/kitchen) stops
showing an order once it's `ready` — their job's done — while
`GetActiveOrders` (staff/admin, unfiltered) keeps showing it through
`ready`, which is the actual "pop up to the waiter" behavior asked for.
Both fall out of the *same* generic `NextOrderStatus`/button mechanism
already in place — kitchen's card says "Mark ready", staff's card for that
same order (once it reaches them) says "Mark served", no new per-role
logic needed ("staff-worker is all in one, no logic changes" held).
Verified this exact handoff end-to-end: kitchen marks ready → order
disappears from kitchen's own feed → still present in staff's feed with a
READY badge → guest sees "Ready — on its way!" live → staff marks served →
gone from every feed, `served_by` recorded. Also tightened
`CancelOrder`: a `ready` order can no longer be cancelled (food's already
made) — previously only served/completed/cancelled were blocked.

**Guest page**: `host_menu.html` and its fragments (`_guest_product_list.html`,
`_cart_summary.html`, new `_placed_orders.html`) rebuilt to the supplied
design, wired to real endpoints instead of the mockup's fully-client-side
fake cart. Notable wiring decisions:
- The mockup's cart was a plain JS array; this app's cart is server-side
  (`cart.Store`), so every interaction (customize-and-add, line +/-,
  payment method, note) had to become a real request. Per-line +/- and the
  customize panel's "Add to order" needed a specific ingredient-set
  targeted, which htmx's declarative attributes can't express dynamically
  — `submitCartAction()` builds a throwaway `<form>` with repeated
  `excluded` hidden inputs and fires it via `htmx.ajax(..., {source: form})`,
  which serializes same-name fields as repeated params the same way a
  native multi-checkbox form submission would (verified server-side via
  `r.Form["excluded"]`).
  the payment-method chips / note field live in a different part of the DOM
  than the sticky checkout button — `hx-include` pulls them in by selector
  rather than needing a shared enclosing form.
- New "Your orders" section replaces the old single-order `#order-status`
  widget entirely with a full list (`db.GetOrdersBySession`,
  `renderPlacedOrdersHTML`/`BroadcastPlacedOrders`) — every order this
  guest session placed, live-updated, each with its own cancel/refund
  button. The old widget's code (`renderOrderStatusHTML`, `OrderStatusPoll`)
  was left in place, unused, matching this project's established "don't
  rip out working code just because the UI moved on" pattern — it's
  harmless dead code, not a regression risk.
- Card payment has no real processor (matches the mockup's own "Demo"
  label) — selecting it just marks the order paid immediately at creation
  time (`models.PaymentMethodCard`), simulating an instant charge, instead
  of collected in person like cash.
- DJ section kept its already-working backend (`/dj/request`, live
  `#song-status`) — only the markup/CSS changed to match the new theme.

Tested the whole thing end-to-end against the running server: customized
add-to-cart (excluding an ingredient) produced a separate cart line from a
same-product-different-exclusion add, correctly did *not* merge with a
plain add of the same product; per-line increment matched the exact
customization variant, not just the product id; checkout correctly
persisted the note, the per-line exclusions, and set card orders to
`paid` immediately; the full ready/served handoff (above); guest self-
cancel on an unpaid order; refund-request flow on a paid one; and that a
`ready` order correctly rejects a cancel attempt (409).

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

### Fixed: real performance bug ("menu takes forever to load")
Root cause, confirmed: `sql.Open("sqlite3", "./bitebox.db")` had zero tuning —
no WAL mode, no busy_timeout. `database/sql`'s connection pool opens several
concurrent connections to the same SQLite file by default; SQLite's default
rollback-journal mode takes an exclusive lock per write and fsyncs
synchronously on every transaction. This app had grown steadily more
concurrent background DB activity over the session (every websocket
broadcast re-queries, a 5s liveness check per open staff socket) — with no
busy_timeout, that's lock contention, and the fsync-per-write pattern is
especially slow under WSL2's virtualized filesystem. This was a real,
diagnosable bug, not a "just add caching" situation. Fixed with one change:
`sql.Open("sqlite3", "./bitebox.db?_journal_mode=WAL&_busy_timeout=5000")`
(db.go) — WAL allows concurrent readers alongside one writer and
checkpoints instead of fsyncing every write. Verified: 20 concurrent table-
view loads while admin/worker sockets were also active completed in ~16ms
total with zero "database is locked" errors in the log (grepped for
"locked"/"busy"/"timeout" across a full test session — none). If load ever
gets heavy enough that this stops being enough, the next lever is
`SetMaxOpenConns`, not re-diagnosing from scratch.

### Waiter UI rebuild (reused across staff/bar/kitchen), order cancellation, served-by attribution, login reskin
The user supplied a new dark-themed order-card design ("Nikos" mockup) for
the worker dashboard and asked for it to be made fully functional, reused
as-is across every non-DJ department (each still scoped to its own existing
permission — no new access-control logic needed there, already correct from
the department work), plus a few backend pieces the new UI implied.

**Backend additions actually needed** (kept deliberately minimal — a lot of
the pasted JS, e.g. the elapsed-time urgency color gradient and the periodic
re-tick, is pure client-side cosmetics that never touches the server, per
the user's own instruction to keep that logic front-end-only):
- **Order cancellation is new** — the old app had no way to void an order.
  `db.CancelOrder(orderID)` (orders.go): one transaction that rejects
  (`ErrCannotCancel`) if the order's already served/completed/cancelled,
  otherwise restores each item's stock (mirroring `CreateOrder`'s decrement,
  same `stock = -1` unlimited sentinel respected) and sets
  `status = 'cancelled'`. New `models.OrderStatusCancelled` — deliberately
  *not* part of `OrderStatusFlow`'s forward progression (it's a branch, not
  a step), and deliberately not reachable through the generic
  `/worker/orders/{id}/status` endpoint — only through the dedicated
  `/worker/orders/{id}/cancel` route, so stock restoration can never be
  bypassed by posting the status directly.
- **The new UI only exposes two lifecycle buttons** (Start preparing, Mark
  served) — no third "Mark Completed" step. Rather than inventing a status
  value nothing sets, `GetActiveOrders`/`GetActiveOrdersByCategory` now
  exclude `served`/`completed`/`cancelled` (previously just `completed`),
  so an order simply drops out of the live feed the moment it's marked
  served — verified this end-to-end, no extra transition needed.
- **Served-by attribution**: new `orders.served_by` column
  (`db.SetServedBy`), set from `WorkerUpdateOrderStatus` whenever the target
  status is `served`, using the acting user's own username via
  `UserFromContext`. Shown in the admin order-history panel and the CSV
  export.
- **"Undo" on the toast is a real request, not a fake local revert** — since
  every other connected client already sees the real state the instant the
  original action's request succeeded (via the existing broadcast
  mechanism), a client-side-only undo would leave that one browser tab
  disagreeing with everyone else's live view. So: undoing "Start preparing"/
  "Mark served" re-posts to the *same* `/status` endpoint with the
  order's pre-click status (`WorkerUpdateOrderStatus` already accepted any
  valid status, not just the next one in sequence — no backend change
  needed there). Undoing "Mark paid" needed a genuinely new endpoint though:
  `WorkerMarkUnpaid` (`/worker/orders/{id}/unpaid`), the missing symmetric
  half of the existing `WorkerMarkPaid`.
- Today/week/month order counts (`GetStatsForPeriod`) now exclude cancelled
  orders — a cancelled order never actually happened, shouldn't count.

**Frontend**: `_order_feed.html` rewritten to the new card markup, fed real
data (`data-ordered-at="{{ .CreatedAt.UnixMilli }}"`, `data-order-id`) —
the pasted JS's demo `seedMinutesAgo` fakery was dropped in favor of that
real timestamp. `worker_dashboard.html` rebuilt around it: real username/
department in the topbar (`departmentLabel` map in worker.go), the DJ
terminal restyled to match the same visual language (no DJ-specific mockup
was given, so this is my own extension of the established component
classes — `_dj_feed.html` reuses `.order-card`), and the toast/undo/urgency-
timer JS re-triggered on `htmx:afterSwap`/`htmx:oobAfterSwap` — the pasted
version only ran once on page load, but here the feed's content gets
wholesale-replaced by live pushes from *anyone's* action, not just this
tab's own clicks, so the client-side enhancements need to re-apply after
every swap, not just once.

**Bug caught while testing**: the new `_order_feed.html` ranged over `.`
directly, but `renderOrderFeedHTML` (unchanged) wraps orders in
`{"Orders": ...}` like every other fragment template in this app — a
data-shape mismatch that made every order-feed request 500. Caught via a
disposable throwaway `cmd/` test program that called the real render
pipeline directly (template.Execute surfaces "can't evaluate field ID in
type interface {}" clearly) rather than eyeballing the diff — worth
remembering as a fast way to isolate a template/data mismatch instead of
guessing from the live server's stack-trace-free 500.

**Login page**: `login.html` reskinned to the same dark theme — CSS only,
the form's method/action/field names are byte-for-byte unchanged, so no
handler-side risk.

### Live stock reservations, live table grid, and live kick-on-deactivate
Three more fixes/features in the same session as the reconnect-storm fix,
all following the same "nothing needs a refresh" thread:

**Stock now visibly updates the instant someone adds to their cart** — not
just at checkout. Previously `products.stock` was only ever decremented at
checkout (correctly, atomically); adding to a cart just did a soft
same-session check. Now:
- `cart.Store.ReservedQuantity(productID)` (cart.go) sums a product's
  quantity across *every* active cart, not just one guest's own.
- A new display-only `productView.AvailableStock` (`admin.go`) = raw DB
  stock minus that reservation total (floored at 0; -1/unlimited passes
  through unchanged). This is computed at render time, never written back —
  the raw `Stock` field is untouched, specifically so the admin dashboard's
  editable stock stepper/Save action can never accidentally overwrite real
  inventory with a reservation-adjusted number. The admin menu row shows
  both: the editable raw count, and a small "N in carts right now" note
  alongside it.
- `CartHandlers.Add`'s stock check now uses the cross-session reserved
  total (previously it only checked the calling guest's own cart, so two
  guests could each "successfully" grab the last unit at the same time).
- Both the guest-facing menu (`/menu/ws`) and — new — the admin dashboard's
  own menu list (`/admin/menu/ws`, `BroadcastAdminMenu`/`BroadcastAllMenus`)
  push on every cart add/remove/checkout, not just admin-side edits.

**Admin table grid is now live** (`/admin/tables/ws`, `BroadcastAdminTables`)
— previously only fetched once on page load. Broadcasts on every direction
a table's state can change: a guest claiming it (`TableHandlers.View`),
leaving it (`TableHandlers.Leave`), or an admin creating/force-releasing/
removing one. Force-release also now clears the kicked guest's cart
(`CartStore.Clear`) — otherwise their reservation would sit there forever
with no other path to release it, since they've been kicked and can't
remove it themselves.

**Fixed: deactivating a worker didn't kick out an already-open tab.**
Deactivation already killed the session server-side and blocked new
logins (see the earlier fix in this file), but a worker with the dashboard
*already open* kept receiving live updates indefinitely — nothing
re-checked their session once the websocket was established. Fixed with a
5-second periodic re-check inside `pumpWS` (ws.go) for every staff-
authenticated socket (order-feed, dj-feed, admin stats/menu/tables): the
moment `db.GetUserBySessionID` fails, the connection is closed with a
custom WebSocket close code (4001), and a small static script in both
`worker_dashboard.html` and `admin_dashboard.html` listens for
`htmx:wsClose` with that code and redirects to `/login`. Verified
end-to-end with a raw websocket client: deactivating a logged-in worker
mid-connection closes their socket with code 4001 within ~5s.

A new package-level `handlers.CartStore` (ws.go, set once in main.go,
same pattern as `Hub`) gives admin.go/menu.go read/clear access to the
shared cart store without restructuring every admin handler into a
`*_ struct` method.

### Fixed: admin stats websocket reconnect storm
`_admin_stats.html` had `hx-ext="ws" ws-connect="..."` on the *same* div
that the websocket's own broadcasts OOB-replace. Every push (including the
connection's own initial snapshot) re-inserted that `ws-connect` attribute,
so htmx tore down and reopened the connection on every single message —
which itself immediately received an initial snapshot containing
`ws-connect` again, an infinite self-sustaining loop (visible in the access
log as dozens of `admin/stats/ws` requests per second, status 000). Fixed
by splitting it into two nested divs: an outer `#admin-stats-socket` that
owns `hx-ext="ws" ws-connect="...?period=X"` and is *only* ever touched by
the period-tab outerHTML swap, and an inner `#admin-stats` (a `{{define}}`
block in the same template file) that's the only thing sent over the
websocket itself, with no `ws-connect` in it at all
(`renderAdminStatsInnerHTML` vs. `renderAdminStatsHTML` in stats.go).
**General rule worth remembering for any future htmx-ws work in this app:
never let a websocket's own broadcast content re-include the `ws-connect`
element it's being delivered through** — every other live widget in this
app (order-feed, dj-feed, product-list, order-status, song-status) already
followed this correctly by keeping the `ws-connect` wrapper *outside* the
OOB-swapped id; admin stats was the one place that got collapsed into a
single div, because period-switching needed the connection itself to be
reconnectable.

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

### Admin dashboard rebuild — dark theme, departments, staff, real analytics
Full visual + functional rebuild of `/admin` to a design the user provided
(dark "Ouzeri Marina" theme, Manrope/IBM Plex Mono). Every element that
looked interactive in the mockup is now wired to real data — nothing is
decorative-only except the "Live" pill and venue name (hardcoded, no
settings system exists to back either).

**Departments — real permissions, not labels.** This was the one explicit
scope decision: worker accounts now have a `department` (`staff`/`bar`/
`kitchen`/`dj`), and it actually restricts access, not just display:
- `models.DepartmentCategory` maps `bar`→Drinks, `kitchen`→Food, others→
  unfiltered. `db.GetActiveOrdersByCategory` (orders.go) returns whole
  orders (all items, for context) that contain at least one item in that
  category — category is read live off `products.category` via join, not a
  historical snapshot.
- `RequireDepartment(...)` (middleware.go) gates routes; admins bypass it
  entirely. `/worker/orders/*` requires staff/bar/kitchen (not dj);
  `/worker/dj/*` requires dj only. Enforced at the route level, not just
  hidden in the UI — verified a bar-department session gets a 403 hitting
  `/worker/dj/feed` directly, and vice versa.
- The order-feed websocket got a 3rd axis: `topicWorkerOrders(bucket)` where
  bucket is `staff`/`bar`/`kitchen`. `BroadcastOrderFeed()` (worker.go)
  renders and pushes to all three on every order change, since one change
  can affect the unfiltered view and a filtered one at once.
- `worker_dashboard.html` shows/hides the order feed and DJ terminal
  sections per department (`ShowOrders`/`ShowDJ` computed in `WorkerHome`).
  Not visually redesigned — only the admin dashboard was in scope for the
  new look.

**Staff & access panel** (new — `internal/handlers/staff.go`,
`templates/_staff_list.html`):
- `users` gained `department`, `is_active`, `created_at`, `last_seen_at`
  (migration in db.go). `RequireRole` now calls `db.TouchLastSeen` on every
  authenticated request, driving "Active now / Active Xm ago / Offline ·
  Xd ago" (`computeStaffStatus`).
- Create account: one dropdown combining role+department (matching the
  mockup's single select) — `admin`, or a department value that implies
  `role=worker`.
- Deactivate kills access **immediately**: deletes all the account's
  `auth_sessions` rows *and* blocks future logins. Initially only did the
  session-kill — testing caught that `LoginSubmit` never checked
  `is_active`, so a deactivated account could just log back in with a new
  session. Fixed in auth.go. Worth remembering: any future auth-adjacent
  change should re-check both the "kill existing access" and "block new
  access" halves independently, they're not the same check.
- Self-deactivation is blocked (`AdminDeactivateStaff` compares against the
  caller's own id via `UserFromContext`).

**Product categories & stock, in the new menu UI:**
- `products.category` (Drinks/Food/Other, migration in db.go). Admin sets
  it via the add-product form and each row's hidden field (see below).
- The product row's only *visible* editable control is the stock stepper
  (+/- adjust a number input client-side, "Save" persists it) — matches the
  mockup exactly, which doesn't expose name/price editing on the row at
  all. "Save" still POSTs the full existing `/admin/products/{id}` update
  (name/price/category carried as hidden inputs, unchanged) — no new
  endpoint, reused what already existed.
- Low-stock alert banner: real, computed in Go (`findLowStockProduct`,
  admin.go) — picks the tightest available, stock-tracked (not unlimited)
  product at 1-5 units. Hidden entirely if nothing qualifies (no fake
  placeholder alert).

**Real revenue analytics** (previously only "today" existed):
- `db.GetStatsForPeriod("today"|"week"|"month")` (orders.go) — week is a
  rolling 7-day window (not calendar week, to dodge Monday-vs-Sunday
  ambiguity), month is the current calendar month. Avg order = revenue ÷
  *paid* order count, matching the KPI card's own "Per paid order" caption.
- Period tabs are a genuinely nice htmx trick worth remembering: the
  `#admin-stats` div carries its own `hx-ext="ws" ws-connect="...?period=X"`
  baked in, and clicking a tab does an **outerHTML** swap of that whole div
  (fresh markup, fresh `ws-connect` URL) — this tears down the old
  websocket and opens a new one scoped to the new period automatically. No
  manual reconnect logic needed. `BroadcastAdminStats()` pushes to all
  three period topics on every stats-affecting write, since e.g. marking an
  order paid can shift today/week/month simultaneously.
- 7-day revenue trend bar chart (`db.GetRevenueTrend`) and a week-over-week
  delta (`db.GetRevenueForRange`) — both real, computed at page load (not
  live-pushed; a slow-moving 7-day aggregate doesn't need sub-second
  freshness the way "today" does).
- CSV export (`GET /admin/export.csv`) — all-time, uncapped
  (`GetOrderHistory(-1)`; SQLite's `LIMIT -1` means unlimited).

**Reused, not rebuilt:** htmx's "start empty, `hx-get` on `{load}`, `{swap
into a static div}`" pattern (already established for cart-summary,
order-status, etc.) now also drives the menu list, table grid, staff list,
and stats block — each got a small dedicated `GET` fragment endpoint
(`AdminProductList`, `AdminTableList`, `AdminStaffList`, `AdminStatsPeriod`)
instead of duplicating render logic between the full-page and fragment
templates.

## Missing / rough edges

- **"Extra" ingredients have no guest-facing flow.** Admin can tag one, but
  nothing on the guest menu lets someone add it (no add-on picker, no
  pricing-for-extras concept exists). The data model supports it; only the
  guest UI/pricing logic is missing, if that's ever actually wanted.
- **No pagination on "Your orders."** Capped at the most recent 20 per
  session (`placedOrdersLimit`) — fine for a single table visit, would
  need real pagination if guests ever accumulate more than that in one
  sitting (unlikely, but noted).
- **Customize-panel/category-tab state resets on every live menu push.**
  If a guest has a customize panel open and someone *else's* action
  triggers a menu broadcast (e.g. admin edits a different product), the
  panel closes and any category filter/tab selection needs the click to
  have already registered client-side — not persisted across a
  server-driven re-render. Minor UX rough edge, not a correctness issue.
- **Toast "Undo" targets an order id, not a DOM position** — correct even
  if the feed re-renders from someone else's action between your click and
  hitting Undo, but the toast's *wording* was written at click time, so in
  that window it could theoretically describe a table/order that's since
  changed further. Narrow edge case at this app's traffic level, not
  something to over-build for now.
- **`.pay-online` CSS class is dormant.** Carried over from the pasted
  design for fidelity, but this app only ever creates cash orders
  (`models.PaymentMethodCash` is the only payment method that exists) — the
  template never emits it. Only relevant if an online payment method is
  ever actually added.
- **Cart stock reservations never time out.** If a guest adds something to
  their cart and then just abandons the table (closes the tab without
  hitting "Leave Table" or getting force-released), whatever's in their
  cart stays counted as "reserved" — and therefore unavailable to everyone
  else — indefinitely. Only an explicit remove/checkout/leave/force-release
  clears it. Fine at this app's scale (a venue admin can always force-
  release a table), but a real reservation-expiry timer would close the gap.
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
- **No account management beyond create/deactivate/activate.** No password
  reset/change flow (an admin can't fix a staff member's forgotten
  password — only they could, and only by knowing the old one), no way to
  edit a username or move someone between departments after creation, no
  audit trail of who created/deactivated which account.
- **Bar/kitchen order-feed filtering is a *view* boundary, not a mutation
  one.** A bar-department worker who knows/guesses another department's
  order id can still `POST /worker/orders/{id}/status` or `/paid` on it —
  `RequireDepartment` only gates the DJ/non-DJ split (a real job-function
  boundary), not bar-vs-kitchen (treated as an organizational filter, not a
  security boundary, since staff routinely cover for each other in a small
  venue). Tighten this if that assumption turns out wrong — the check would
  be "does this order contain an item in my category" before allowing the
  mutation, symmetric to the read-side filter.
- **Venue name ("Ouzeri Marina") is hardcoded** in admin_dashboard.html —
  there's no settings/venue-config concept anywhere in the app.
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
