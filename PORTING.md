# BiteBox: Go → Laravel porting plan

This document is the map between the existing Go application (`cmd/`, `internal/`,
`templates/`) and the Laravel rewrite that lives in `laravel/`. It exists so the
rewrite is a *translation with reasons*, not a fresh guess at the same features.

Read it top to bottom once before writing any Laravel code. Each section names the
Go source file, the Laravel equivalent, and — most importantly — **what changes
conceptually**, because a few things in the Go app are done by hand that Laravel
does for you (and one or two things Laravel makes *harder*, which is worth knowing).

---

## 1. The shape of the two apps

| Concern | Go (current) | Laravel (target) |
|---|---|---|
| Entry point | `cmd/server/main.go` — builds the router, wires the hub, starts `http.ListenAndServe` | `public/index.php` + `bootstrap/app.php`; you never write the server loop |
| Routing | `go-chi` calls in `main.go` | `routes/web.php`, `routes/channels.php` |
| Request handling | `internal/handlers/*.go`, plain `http.HandlerFunc` | `app/Http/Controllers/*` — one class per resource, methods per action |
| Data access | `internal/db/*.go` — hand-written SQL against `database/sql` | Eloquent models in `app/Models/`, migrations in `database/migrations/` |
| Domain types | `internal/models/models.go` — structs + `const` blocks | Eloquent models + PHP 8 `enum`s in `app/Enums/` |
| Templates | `html/template` in `templates/`, parsed at boot | Blade in `resources/views/` |
| Live updates | `internal/wshub/hub.go` + `gorilla/websocket` + htmx `ws` ext | Laravel Reverb + Echo, broadcast events in `app/Events/` |
| Auth (staff) | `internal/auth/auth.go` — bcrypt + own `auth_sessions` table | Laravel's built-in session guard (`Auth::attempt`) |
| Auth (guest) | table host locking via a session cookie | same idea, but on Laravel's session |
| Cart | `internal/cart/cart.go` — in-memory `map` behind a `sync.RWMutex` | Redis-backed cart keyed by session id |
| Database | SQLite (WAL) | MySQL in Docker (SQLite still fine for local tests) |

**The single biggest conceptual change:** in Go you own concurrency. `cart.Store`
needs a mutex because one process holds every cart in memory and many goroutines
touch it. PHP-FPM has no shared memory between requests — each request is a fresh
process — so the cart *must* move to shared storage (Redis). That is not a
downgrade; it is why the Laravel version scales past one box, and it is a good
interview answer to have ready.

---

## 2. Database schema

Source of truth today: the `CREATE TABLE` block and the `addColumnIfMissing`
calls in `internal/db/db.go`. Those incremental `ALTER`s are a hand-rolled
migration system — in Laravel each one becomes a real migration file, and since
this is a rewrite we can collapse them into clean initial migrations.

| Go table | Laravel migration / model | Notes for the port |
|---|---|---|
| `tables` (`number` PK, `status`, `host_session_id`) | `tables` → `venue_tables` / `App\Models\VenueTable` | `tables` is a reserved-ish word and collides conceptually with Laravel's own vocabulary; rename to `venue_tables`. Keep `number` unique, add a real auto-increment `id`. |
| `products` (`name`, `price`, `stock`, `is_available`, `category`, `subcategory`, `description`) | `products` / `Product` | `price` becomes `decimal(8,2)`, **not** float — money in a REAL column is a bug waiting to happen. `stock = -1` means untracked; keep that convention but document it. |
| `product_ingredients` (`product_id`, `name`, `kind`) | `ingredients` / `Ingredient` | `kind` becomes an enum cast (`removable` / `extra`). `belongsTo(Product)`. |
| `orders` (+ `payment_status`, `served_by`, `note`, `refund_requested`) | `orders` / `Order` | `served_by` is a username string in Go; make it a nullable `served_by_user_id` FK. `status`/`payment_status`/`payment_method` become enums. |
| `order_items` (+ `excluded_ingredients`, `extra_ingredients` as JSON text) | `order_items` / `OrderItem` | Those two become real `json` columns with an `array` cast. |
| `song_requests` (`song_name`, `tip_amount`, `status`, `table_number`) | `song_requests` / `SongRequest` | `table_number` → `venue_table_id` FK. |
| `users` (`username`, `password_hash`, `role`, `department`, `is_active`, `last_seen_at`) | `users` / `User` | Laravel's default users table uses `email`; we keep **username** login instead (matches the venue's reality — staff don't have emails). `password_hash` → `password` (Laravel's convention, so `Auth::attempt` works untouched). |
| `auth_sessions` (`session_id`, `user_id`) | *(deleted)* | Laravel's own session driver replaces this table entirely. This is the clearest "the framework already did it" win in the whole port. |
| `settings` (singleton row, `dj_requests_enabled`, `venue_name`) | `settings` / `Setting` | Keep the single-row pattern with a `CHECK (id = 1)` equivalent, or move to a key/value table. Decide during the slice; single row is simpler and honest. |

---

## 3. Routes

Left column is `cmd/server/main.go`. Right column is `routes/web.php`.

### Guest (no auth, session-cookie identity)

| Go | Laravel |
|---|---|
| `GET /table/{number}` → `table.View` | `GET /table/{number}` → `TableController@show` |
| `GET /table/{number}/left` → `table.Left` | `GET /table/{number}/left` → `TableController@left` |
| `POST /table/{number}/leave` → `table.Leave` | `DELETE /table/{number}/session` → `TableController@leave` |
| `GET /table/{number}/ws` → `TableStatusWS` | Echo private channel `table.{number}` |
| `GET /cart/summary` → `cart.Summary` | `GET /cart` → `CartController@show` |
| `POST /cart/add` / `remove` / `clear` | `CartController@store` / `destroy` / `clear` |
| `POST /cart/checkout` | `CheckoutController@store` |
| `POST /orders/{id}/cancel` → `GuestCancelOrder` | `DELETE /orders/{order}` → `GuestOrderController@destroy` |
| `GET /menu/ws` → `MenuWS` | Echo channel `menu` |
| `POST /dj/request` → `DJRequest` | `POST /song-requests` → `SongRequestController@store` |

### Auth

| Go | Laravel |
|---|---|
| `GET /login` → `LoginPage` | `GET /login` → `Auth\LoginController@create` |
| `POST /login` → `LoginSubmit` | `POST /login` → `Auth\LoginController@store` |
| `POST /logout` → `Logout` | `POST /logout` → `Auth\LoginController@destroy` |

### Admin (`RequireRole("admin")` → middleware `auth` + `role:admin`)

`AdminHome`, `AdminStatsPeriod`, `AdminExportCSV`, product CRUD + ingredient
add/delete, table create/release/delete, staff create/activate/deactivate,
DJ-toggle and venue-name settings — all become `Admin\*Controller` resource
controllers under a `Route::prefix('admin')->middleware(['auth','role:admin'])`
group. The three admin `*/ws` routes collapse into Echo channels
`admin-stats`, `admin-menu`, `table-grid`.

### Worker (`RequireRole("worker","admin")` + `RequireDepartment(...)`)

`WorkerHome`, order status/paid/unpaid/cancel, and the DJ accept/reject pair,
under `Route::middleware(['auth','role:worker,admin'])`, with the DJ routes
additionally behind `department:dj`.

---

## 4. Middleware mapping

`internal/handlers/middleware.go` has two pieces:

- `RequireRole(roles...)` → a `EnsureUserHasRole` middleware, aliased `role`,
  used as `role:admin` or `role:worker,admin`. Laravel already handles the
  "no session → redirect to /login" half via the `auth` middleware, so our
  middleware only checks the role.
- `RequireDepartment(departments...)` → `EnsureUserHasDepartment`, aliased
  `department`. Keep the Go rule exactly: **admins bypass the department check**.
- `db.TouchLastSeen(user.ID)` on every authenticated request → keep it, but as a
  separate `TouchLastSeen` middleware so it isn't tangled with authorization.

`UserFromContext(r)` has no equivalent and needs none — `$request->user()` and
`Auth::user()` are the framework's version of the same thing.

---

## 5. Live updates: the hub → Reverb

`internal/wshub/hub.go` is ~50 lines: a `map[topic]map[chan []byte]bool`, a
mutex, and a non-blocking send that drops messages for slow consumers. Each
handler renders an HTML fragment and calls `hub.Broadcast(topic, html)`; htmx's
`ws` extension swaps it in out-of-band.

Reverb replaces the transport, not the idea. The topics map to channels:

| Go topic | Laravel channel | Kind |
|---|---|---|
| `menu` | `menu` | public |
| `order-feed` | `orders.department.{department}` | private |
| `order-status`, `placed-orders` | `table.{number}` | private |
| `dj-feed`, `dj-section` | `dj` | private |
| `song-status` | `table.{number}` | private |
| `admin-stats` | `admin` | private |
| `table-grid` | `admin` | private |

Two real decisions to make in that slice:

1. **What travels over the wire.** Go sends rendered HTML. We can keep that
   (broadcast a `view()->render()` string and let htmx swap it) or send JSON and
   render client-side. Keeping HTML preserves the "no JS bundle" property and
   keeps htmx useful — that is the plan, and it is a defensible choice to explain.
2. **Authorization.** Go's WS routes were only as protected as the handler made
   them. Laravel forces you to declare each private channel in
   `routes/channels.php` — a genuine security improvement over the current app,
   and worth calling out in the README.

---

## 6. Cart

`internal/cart/cart.go` keeps carts in process memory keyed by session id, merges
lines by `(productID, excluded set, extras set)` compared order-independently, and
is dropped on restart by design.

Laravel port: a `CartService` backed by Redis under key `cart:{sessionId}`, with
the same line-merge rule. The order-independent set comparison (`sameSet` in Go)
becomes: sort both arrays, compare — or hash the customization into a stable line
key. **Stock reservation** (an item in someone's cart is unavailable to others)
is the tricky part: in Go it's implicit in the single process. In Laravel it needs
either a Redis reservation with a TTL or a real `stock_reservations` table. Decide
this deliberately in the cart slice; it is the single most interesting piece of the
whole port to be able to talk about.

---

## 7. Templates

The 21 files in `templates/` map almost 1:1 to Blade. Files starting with `_` are
fragments (`_order_feed.html`, `_cart_summary.html`, …) — those become
`resources/views/partials/*.blade.php` and are exactly the things broadcast over
Reverb. The full pages get a shared `layouts/app.blade.php`, which Go didn't have.

Syntax translation is mechanical: `{{.Field}}` → `{{ $field }}`,
`{{range .Items}}` → `@foreach`, `{{if}}` → `@if`, `{{template "x" .}}` →
`@include('partials.x')`.

---

## 8. Build order

Each step is one branch, one PR, one working app at every point.

1. **Skeleton + this document** ← you are here
2. Schema: migrations, models, enums, factories, seeders (port `seed_ego.go`)
3. Staff auth: username login, `role`/`department` middleware, worker + admin shells
4. Guest table sessions: QR route, host locking, "table occupied" view
5. Menu: products, categories, ingredients, admin CRUD
6. Cart + stock reservation
7. Checkout + the order lifecycle across departments
8. Reverb: replace every `hub.Broadcast` with a broadcast event
9. Song requests + the DJ terminal
10. Admin stats, CSV export, settings
11. Docker (PHP-FPM, Nginx, MySQL, Redis)
12. CI (GitHub Actions: Pint, PHPUnit)
13. Deploy

Tests come with each slice, not at the end.
