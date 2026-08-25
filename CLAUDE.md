# CLAUDE.md — BiteBox

Guidance for AI assistants working in this repository.

## What this is

BiteBox is a single-binary Go web app for venue point-of-sale and table
management. Guests scan a QR code that points at `/table/{number}`, the first
device to scan claims the table as **host**, and the host orders from a live
menu and sends paid song requests to the DJ. Staff and admins log in at
`/login` and work from role-protected dashboards. Everything is
server-rendered `html/template` plus HTMX; there is no JS build step, no
external services, and no Docker.

Module name: `bitebox` (see `go.mod`, Go 1.25).

## Layout

```
cmd/server/main.go      entry point: DB init, cart store, chi router, all route wiring
internal/auth/          bcrypt hashing, session-ID generation, auth cookie helpers
internal/cart/          in-memory, session-keyed cart store (mutex-guarded)
internal/db/            SQLite access, split by domain
  db.go                 connection, schema/migrations, seeding, users + auth_sessions
  tables.go             tables: claim/release/lookup by host session
  products.go           menu items
  orders.go             orders + order_items (transactional), status/payment updates
  songs.go              DJ song requests
internal/handlers/      HTTP handlers, one file per surface
  middleware.go         RequireRole, request-context user
  table.go              guest table view/leave, order-status polling
  cart.go               add/remove/checkout
  dj.go                 guest song request
  auth.go               login page/submit, logout
  admin.go              product + table management
  worker.go             order feed, status transitions, DJ queue
internal/models/        shared structs and status constants
templates/              full pages, plus `_`-prefixed HTMX partials
bitebox.db              SQLite file, created on first run (currently tracked in git)
```

Note: the `README.md` "Project Structure" section is out of date (it predates
`internal/handlers/`, `internal/cart/`, and the split `db` files). Trust the
tree above and the source.

## Running and building

```bash
go mod tidy
go run ./cmd/server        # http://localhost:8080/table/1
go build -o bitebox ./cmd/server
```

- `go-sqlite3` needs **CGO**, so a C compiler must be present. Don't set
  `CGO_ENABLED=0`.
- Templates are read from `templates/` at request time with a **relative**
  path, so the server must be started from the repository root.
- There are no tests in the repo yet. Verify changes by running the server and
  exercising routes with `curl` (cookie jars via `-c`/`-b`) or a browser —
  `.claude/settings.local.json` already allowlists that workflow.
- Always run `go build ./...` (or `go vet ./...`) before committing.

## Seed data and accounts

`db.InitDB()` runs on every start: it creates tables if missing, applies the
`orders.payment_status` column via `addColumnIfMissing` (SQLite has no
`ADD COLUMN IF NOT EXISTS`), seeds table 1, seeds four sample products if the
products table is empty, and seeds users if the users table is empty.

Seeded logins are **`admin`/`admin123`** and **`staff`/`staff123`** (roles
`admin` and `worker`). These are development credentials — the README's claim
of a randomly generated admin password no longer matches the code. Do not
introduce new hardcoded credentials, and keep the console warning that tells
operators to change them.

To reset state, stop the server and delete `bitebox.db`.

## Key conventions

**Two independent session cookies.** `bitebox_session` (in `handlers`) is the
anonymous guest/table session; `bitebox_auth` (in `auth`) is the staff login
session, backed by rows in `auth_sessions` and expired server-side after 24h.
Never conflate them. Both are `HttpOnly` + `SameSite=Lax`.

**Never trust client-supplied identity or money.** Cart mutations resolve the
caller's table via `resolveHostedTable` (host session → table), not from a
posted table number. Cart totals are always recomputed from line items in
`recalcTotal`. Keep new handlers to this pattern.

**Authorization is route-group middleware.** `handlers.RequireRole(...)` wraps
route groups in `main.go`: unauthenticated → redirect to `/login`,
wrong role → 403. Add protected routes inside the existing `r.Group` blocks
rather than checking roles inside handlers. `/admin` is admin-only; `/worker`
allows admin and worker.

**Status strings live in `models`.** Use `OrderStatus*`, `PaymentStatus*`,
`SongRequestStatus*`, `Role*` constants, and advance orders through
`models.NextOrderStatus` / validate with `IsValidOrderStatus`. Don't inline
literals like `"preparing"`.

**Orders snapshot their items.** `db.CreateOrder` writes the order and its
`order_items` in one transaction, copying name and price at order time so
later menu edits don't rewrite history. Preserve that when touching checkout.

**Carts are deliberately in-memory.** `cart.Store` is a mutex-guarded map
created once in `main` and injected into `TableHandlers`/`CartHandlers`.
Losing a not-yet-placed cart on restart is accepted; don't persist carts
without a reason.

**SQL is hand-written with `?` placeholders.** No ORM. Always use parameter
binding; the only `fmt.Sprintf`-built SQL is `addColumnIfMissing`, whose
arguments are internal literals.

**HTMX partials.** Templates prefixed with `_` render fragments swapped into
a page (`_cart_summary`, `_order_feed`, `_order_status`, `_product_list`,
`_table_list`, `_dj_feed`). Handlers that mutate state generally respond with
the refreshed partial rather than redirecting; full-page redirects from HTMX
use the `HX-Redirect` header (see `TableHandlers.Leave`). Water.css and HTMX
load from CDNs — there are no local static assets.

**Templates are parsed per request** with `template.Must(template.ParseFiles(...))`.
This is intentional for development convenience; match the surrounding style
unless asked to change it.

## Git workflow

Default branch is `main`. Commit with clear messages and push to the branch
you were assigned; do not open a pull request unless explicitly asked.
