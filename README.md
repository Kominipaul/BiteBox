# 🍽️ BiteBox

**A decentralized, multi-tenant POS, table-management, and interactive venue engagement platform.**

BiteBox turns any table into a QR-code ordering hub — guests scan, claim the table as "Host," order from a live menu, and even send paid song requests to the DJ. It's built to run anywhere: a Raspberry Pi tucked behind the bar, a local venue server, or the cloud.

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)
![SQLite](https://img.shields.io/badge/SQLite-embedded-003B57?style=flat&logo=sqlite)
![HTMX](https://img.shields.io/badge/HTMX-1.9.10-3D72D7?style=flat)
![License](https://img.shields.io/badge/license-MIT-green)

---

## ✨ Features

- **📱 QR Table Sessions** — scanning `/table/{number}` claims the table for that device as "Host"; every other device that scans sees a locked view until the host leaves.
- **🔒 Single-Host Locking** — no double-ordering. Host status lives in SQLite and is tied to an HTTP-only session cookie.
- **🎵 DJ Song Requests** — guests submit a track + tip straight from the table menu, rendered live via HTMX with no page reload.
- **🔑 Authentication & RBAC** — bcrypt-hashed passwords, server-side sessions, and role-protected routes for `Admin` and `Worker` (staff/bar/kitchen/DJ) accounts.
- **⚡ Single Binary** — compiles to one lightweight Go binary with an embedded SQLite database. No external services, no Docker required.

## 🧱 Tech Stack

| Layer | Choice |
|---|---|
| Language | [Go](https://go.dev/) |
| Database | [SQLite](https://www.sqlite.org/) via `mattn/go-sqlite3` |
| Router | [`go-chi/chi`](https://github.com/go-chi/chi) |
| Frontend | Server-rendered `html/template` + [Water.css](https://watercss.kognise.dev/) |
| Interactivity | [HTMX](https://htmx.org/) |
| Auth | `golang.org/x/crypto/bcrypt` |

## 📁 Project Structure

```
bitebox/
├── go.mod
├── bitebox.db                    # auto-generated SQLite database (gitignored)
├── cmd/
│   └── server/
│       └── main.go               # entry point, routes, handlers
├── internal/
│   ├── db/
│   │   ├── db.go                 # connection, schema, tables/products
│   │   └── auth.go                # users, sessions, bcrypt hashing
│   ├── middleware/
│   │   └── auth.go               # RequireAuth / RequireRole
│   └── models/
│       └── models.go             # shared structs
└── templates/
    ├── host_menu.html            # table host's ordering view
    ├── guest_menu.html           # locked view for non-host scans
    ├── table_left.html           # release confirmation screen
    ├── login.html                # staff/admin login
    ├── admin_dashboard.html      # admin-only, RBAC-protected
    └── worker_dashboard.html     # worker-only, RBAC-protected
```

## 🚀 Getting Started

### Prerequisites

- **Go 1.21+** — [install instructions](https://go.dev/doc/install)
- **A C compiler** (gcc/clang) — required because `go-sqlite3` uses CGO. On Debian/Ubuntu: `sudo apt install build-essential`. On macOS, Xcode Command Line Tools cover this.

### Installation

```bash
git clone https://github.com/<your-username>/bitebox.git
cd bitebox
go mod tidy
```

### Running the server

```bash
go run cmd/server/main.go
```

You should see:

```
🚀 BiteBox Go server running on http://localhost:8080/table/1
```

On first launch, BiteBox seeds 10 sample tables, a handful of menu items, and a **default admin account**. The generated password is printed once to the console:

```
========================================
Default admin account created:
   username: admin
   password: <randomly generated>
   Log in and change this immediately.
========================================
```

Copy that password now — it won't be shown again.

### Try it out

| What | URL |
|---|---|
| Scan a table (becomes Host on first visit) | `http://localhost:8080/table/1` |
| Staff/Admin login | `http://localhost:8080/login` |
| Admin dashboard (create worker accounts) | `http://localhost:8080/admin` |
| Worker dashboard | `http://localhost:8080/worker` |

Open `/table/1` in one browser and in an incognito window to see the host-lock behavior — the second "device" gets the read-only guest view instead of stealing the order session.

Log in as `admin` and use the **Create New Account** form to add a worker (bar/kitchen/DJ/staff) and confirm they land on `/worker` while being blocked from `/admin`.

### Building a binary

```bash
go build -o bitebox ./cmd/server
./bitebox
```

## 🗺️ Roadmap

- [x] **Priority 1 — Authentication & RBAC**: bcrypt-hashed logins, server-side sessions, role-protected `/admin` and `/worker` routes
- [ ] **Priority 2 — Admin Dashboard**: menu/inventory management, table configuration, revenue analytics
- [ ] **Priority 3 — Worker Dashboard**: live order feed (HTMX polling / WebSockets), order lifecycle updates, DJ request terminal
- [ ] **Priority 4 — Payments**: Stripe/Viva Wallet integration for Apple Pay, Google Pay, card, and cash-to-waiter flows

## 🔐 Security Notes

- Passwords are hashed with bcrypt — never stored or logged in plaintext.
- Auth sessions are opaque server-side tokens stored in SQLite (`auth_sessions`), separate from the anonymous table-host session, so logout and expiry are enforced on the server rather than trusted to the client.
- The seeded admin password is random per install and only ever printed once to the server console — there's no hardcoded default credential.

## 🤝 Contributing

Issues and PRs are welcome. Please open an issue describing the change before submitting larger PRs so we can align on approach.

## 📄 License

[MIT](LICENSE)
