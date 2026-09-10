package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"bitebox/internal/cart"
	"bitebox/internal/db"
	"bitebox/internal/handlers"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// neuteredFileSystem wraps an http.FileSystem to refuse directory listings —
// http.FileServer serves one by default for any directory without its own
// index.html, which is exactly the /static/vendor and /static/fonts case
// here: nothing sensitive lives under static/, but there's no reason to
// hand a crawler an itemized list of every vendored file and its exact
// version either.
type neuteredFileSystem struct {
	fs http.FileSystem
}

func (nfs neuteredFileSystem) Open(path string) (http.File, error) {
	f, err := nfs.fs.Open(path)
	if err != nil {
		return nil, err
	}
	s, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if s.IsDir() {
		index := strings.TrimSuffix(path, "/") + "/index.html"
		if _, err := nfs.fs.Open(index); err != nil {
			f.Close()
			return nil, os.ErrNotExist
		}
	}
	return f, nil
}

// staticCacheHeaders sets a Cache-Control appropriate to how each static
// path actually changes, so a phone on a venue's flaky wifi isn't
// re-fetching the same menu photo or vendored library on every page view.
// http.FileServer already sends Last-Modified/ETag and honors conditional
// requests on its own — this only adds the header that decides whether the
// browser bothers asking again at all.
func staticCacheHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "uploads/products/"):
			// writeProductPhoto (internal/handlers/photo.go) names every
			// upload "<productID>-<nanosecond timestamp>.jpg" — a new photo
			// is always a new URL, so the URL's *content* never changes and
			// this can be cached as aggressively as a browser allows.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case strings.HasPrefix(r.URL.Path, "vendor/"), strings.HasPrefix(r.URL.Path, "fonts/"),
			strings.HasPrefix(r.URL.Path, "demo/"), strings.HasPrefix(r.URL.Path, "icons/"):
			// Vendored libraries, self-hosted font files, demo seed assets,
			// and the PWA icon set (scripts/gen-icons.go) all change only on
			// a deploy, not per-request — a day is long enough to cut
			// repeat-visit traffic without risking a stale file surviving
			// long past its own deploy.
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		next.ServeHTTP(w, r)
	})
}

// envOrDefault reads an env var, falling back to a default when it's unset
// or blank.
func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// fileExists reports whether path names a regular, readable file — used to
// decide whether the optional local-HTTPS listener has something to serve
// (see main's TLS block) without ever treating a missing file as an error
// worth surfacing; its absence is the expected, common case.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func main() {
	db.InitDB()
	handlers.InitPush()

	cartStore := cart.NewStore()
	handlers.CartStore = cartStore
	table := &handlers.TableHandlers{Cart: cartStore}
	cartHandlers := &handlers.CartHandlers{Cart: cartStore}
	handlers.StartIdleTableSweep(cartStore)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(handlers.SecurityHeadersMiddleware)
	r.Use(handlers.CSRFProtectionMiddleware)
	r.NotFound(handlers.NotFound)

	// Vendored htmx + its ws extension, served locally so the guest/worker/
	// admin pages' ws-connect can fire the instant the page's own scripts
	// load, instead of blocking on a CDN (unpkg) round-trip on every visit —
	// that CDN fetch was the actual cause of "menu takes 2s to load": the
	// menu is an empty shell until htmx runs and opens /menu/ws, so any
	// latency fetching htmx itself shows up as the websocket looking slow.
	// neuteredFileSystem (see below) blocks the auto-generated directory
	// listing http.FileServer would otherwise serve for /static/vendor/ and
	// /static/fonts/ — neither has an index.html, so without it a plain GET
	// on either directory lists every file in it.
	r.Handle("/static/*", http.StripPrefix("/static/", staticCacheHeaders(http.FileServer(neuteredFileSystem{http.Dir("static")}))))

	r.Get("/", handlers.Landing)

	// Phone-notification plumbing: served at the origin root (not under
	// /static/*) — see handlers.ServiceWorker's doc comment on why that
	// matters for a service worker's control scope.
	r.Get("/sw.js", handlers.ServiceWorker)
	// TEMPORARY — see handlers.DebugPushClientState's doc comment.
	r.Post("/debug/push-client-state", handlers.DebugPushClientState)
	r.Get("/manifest.json", handlers.WebManifest)
	// Guest PWA manifest — per-table start_url via ?table=N, see
	// handlers.GuestWebManifest's doc comment for why that matters on iOS.
	r.Get("/manifest-guest.json", handlers.GuestWebManifest)

	// Guest table access
	r.Get("/table/{number}", table.View)
	r.Get("/table/{number}/left", table.Left)
	r.Post("/table/{number}/leave", table.Leave)
	r.Get("/table/{number}/ws", handlers.TableStatusWS)
	// {number} in the URL is cosmetic — RequestAssistance resolves the
	// actual table from the caller's own guest session, same as Leave.
	r.Post("/table/{number}/assist", handlers.RequestAssistance)
	// Guest-side phone alerts ("your order is ready") — same cosmetic
	// {number} caveat as above; both handlers resolve the real session
	// from the guest cookie.
	r.Post("/table/{number}/push/subscribe", handlers.GuestPushSubscribe)
	r.Post("/table/{number}/push/unsubscribe", handlers.GuestPushUnsubscribe)

	// Guest cart & checkout
	r.Get("/cart/summary", cartHandlers.Summary)
	r.Post("/cart/add", cartHandlers.Add)
	r.Post("/cart/remove", cartHandlers.Remove)
	r.Post("/cart/clear", cartHandlers.Clear)
	r.Post("/cart/checkout", cartHandlers.Checkout)

	// Guest order self-service (cancel if not yet prepared; request a
	// refund instead if already paid)
	r.Post("/orders/{id}/cancel", handlers.GuestCancelOrder)

	// Guest-facing live menu
	r.Get("/menu/ws", handlers.MenuWS)

	// DJ song requests
	r.Post("/dj/request", handlers.DJRequest)

	// Staff auth
	r.Get("/login", handlers.LoginPage)
	r.With(handlers.LoginRateLimitMiddleware).Post("/login", handlers.LoginSubmit)
	r.Post("/logout", handlers.Logout)

	// Admin dashboard
	r.Group(func(r chi.Router) {
		r.Use(handlers.RequireRole(models.RoleAdmin))
		r.Get("/admin", handlers.AdminHome)
		r.Get("/admin/stats", handlers.AdminStatsPeriod)
		r.Get("/admin/stats/ws", handlers.AdminStatsWS)
		r.Get("/admin/export.csv", handlers.AdminExportCSV)
		r.Get("/admin/products", handlers.AdminProductList)
		r.Get("/admin/menu/ws", handlers.AdminMenuWS)
		r.Post("/admin/products", handlers.AdminCreateProduct)
		r.Post("/admin/products/{id}", handlers.AdminUpdateProduct)
		r.Post("/admin/products/{id}/toggle", handlers.AdminToggleProduct)
		r.Post("/admin/products/{id}/ingredients", handlers.AdminAddIngredient)
		r.Delete("/admin/ingredients/{id}", handlers.AdminDeleteIngredient)
		r.Post("/admin/categories", handlers.AdminCreateCategory)
		r.Get("/admin/tables", handlers.AdminTableList)
		r.Get("/admin/tables/ws", handlers.AdminTablesWS)
		r.Post("/admin/tables", handlers.AdminCreateTable)
		r.Post("/admin/tables/{number}/release", handlers.AdminReleaseTable)
		r.Delete("/admin/tables/{number}", handlers.AdminDeleteTable)
		r.Get("/admin/staff", handlers.AdminStaffList)
		r.Post("/admin/staff", handlers.AdminCreateStaff)
		r.Post("/admin/staff/{id}", handlers.AdminUpdateStaff)
		r.Post("/admin/staff/{id}/reset-password", handlers.AdminResetStaffPassword)
		r.Post("/admin/staff/{id}/deactivate", handlers.AdminDeactivateStaff)
		r.Post("/admin/staff/{id}/activate", handlers.AdminActivateStaff)
		r.Get("/admin/audit-log", handlers.AdminAuditLog)
		r.Get("/admin/history/page", handlers.AdminOrderHistoryPage)
		r.Get("/admin/orders/{id}/receipt", handlers.AdminOrderReceipt)
		r.Get("/admin/backup", handlers.AdminDownloadBackup)
		r.Post("/admin/settings/dj-requests/toggle", handlers.AdminToggleDJRequests)
		r.Post("/admin/settings/cash-orders/toggle", handlers.AdminToggleCashOrders)
		r.Post("/admin/settings/venue-name", handlers.AdminUpdateVenueName)
		r.Post("/admin/settings/business-details", handlers.AdminUpdateBusinessDetails)
	})

	// Worker dashboard
	r.Group(func(r chi.Router) {
		r.Use(handlers.RequireRole(models.RoleAdmin, models.RoleWorker))
		r.Get("/worker", handlers.WorkerHome)

		// Phone-alert subscribe/unsubscribe — every department, including
		// DJ, can technically call these; DJ just never ends up in a
		// notifyDepartments() target list, so a DJ subscription would sit
		// unused rather than cause any harm.
		r.Post("/worker/push/subscribe", handlers.PushSubscribe)
		r.Post("/worker/push/unsubscribe", handlers.PushUnsubscribe)

		// Orders: everyone except the DJ department (bar/kitchen get a
		// category-filtered feed, staff/admin get everything).
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequireDepartment(models.DepartmentSuperworker, models.DepartmentWaiter, models.DepartmentBar, models.DepartmentKitchen))
			r.Get("/worker/orders/ws", handlers.WorkerOrdersWS)
			r.Post("/worker/orders/{id}/status", handlers.WorkerUpdateOrderStatus)
			r.Post("/worker/orders/{id}/paid", handlers.WorkerMarkPaid)
			r.Post("/worker/orders/{id}/unpaid", handlers.WorkerMarkUnpaid)
			r.Post("/worker/orders/{id}/cancel", handlers.WorkerCancelOrder)
			r.Get("/worker/assistance/ws", handlers.WorkerAssistanceWS)
			r.Post("/worker/tables/{number}/assist/clear", handlers.WorkerAcknowledgeAssistance)
		})

		// DJ terminal: DJ department only.
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequireDepartment(models.DepartmentDJ))
			r.Get("/worker/dj/ws", handlers.WorkerDJWS)
			r.Post("/worker/dj/{id}/accept", handlers.WorkerDJAccept)
			r.Post("/worker/dj/{id}/reject", handlers.WorkerDJReject)
		})
	})

	// Timeouts bound how long a slow or hostile client can tie up a
	// connection before its handler even runs — with none set, http.Server's
	// defaults are "no limit at all", which is exactly what a Slowloris-style
	// connection-exhaustion attack (trickling request headers a byte at a
	// time) needs. Deliberately NOT setting ReadTimeout/WriteTimeout here:
	// both arm a deadline on the raw connection before the handler starts,
	// and net/http only clears them for a hijacked connection's write side —
	// gorilla/websocket's Upgrade resets the write deadline post-handshake,
	// but never touches the read deadline, so every pumpWS connection
	// (this app's entire live-update mechanism) would silently die the
	// instant that ReadTimeout elapsed, live client or not. ReadHeaderTimeout
	// and IdleTimeout only ever apply before a request's handler runs, so
	// they're safe for the same reason and still close the actual Slowloris
	// vector.
	srv := &http.Server{
		Addr:              ":8080",
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Optional local HTTPS. This exists for one thing: testing phone-only
	// browser features (Web Push, "Add to Home Screen") on an actual device
	// before a real deployment exists. Those APIs refuse to work at all
	// over plain HTTP to anything but localhost — a phone on the same WiFi
	// hitting this machine's LAN IP over :8080 can't use them no matter
	// what, but a real (if only locally-trusted) cert on a separate HTTPS
	// port can.
	//
	// Deliberately NOT a tunnel to any third-party service, and deliberately
	// NOT baked-in cert generation — this just looks for two files,
	// defaulting to .dev-certs/dev-{cert,key}.pem (override with
	// TLS_CERT_FILE/TLS_KEY_FILE), and turns HTTPS on if they're both
	// there, so `go run ./cmd/server` alone is enough once you've generated
	// them once. The documented path (see README) is `mkcert`, a
	// well-known local-CA tool: it issues a real, cryptographically valid
	// certificate for this machine's LAN IP, signed by a CA that only
	// devices you've explicitly installed it on will trust (your phone,
	// via mkcert's exported rootCA.pem — a one-time step) — nothing leaves
	// this network, no third party is involved at all. No cert files on
	// disk (e.g. a fresh clone, or in Docker) just means plain :8080 only,
	// same as always — nothing here is required. Once there's a real
	// domain + VPS, this whole block goes unused: a reverse proxy
	// (Caddy/nginx) terminating Let's Encrypt TLS in front of the plain
	// :8080 above is the actual production answer, not this.
	var tlsSrv *http.Server
	certFile := envOrDefault("TLS_CERT_FILE", ".dev-certs/dev-cert.pem")
	keyFile := envOrDefault("TLS_KEY_FILE", ".dev-certs/dev-key.pem")
	if certExists, keyExists := fileExists(certFile), fileExists(keyFile); certExists && keyExists {
		tlsAddr := ":8443"
		if a := os.Getenv("TLS_ADDR"); a != "" {
			tlsAddr = a
		}
		tlsSrv = &http.Server{
			Addr:              tlsAddr,
			Handler:           r,
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		go func() {
			log.Printf("🔒 Local HTTPS also listening on https://<this machine's LAN IP>%s (cert: %s)", tlsAddr, certFile)
			if err := tlsSrv.ListenAndServeTLS(certFile, keyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("HTTPS listener error: %v", err)
			}
		}()
	}

	// Graceful shutdown: Ctrl+C (or a deploy's SIGTERM) stops the server from
	// accepting new connections and gives in-flight requests/websockets up to
	// 10s to finish on their own, instead of the OS just killing the process
	// mid-request. Any connection still open past that deadline is cut off.
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		log.Println("🛑 Shutting down — draining in-flight requests (up to 10s)...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Graceful shutdown didn't finish cleanly: %v", err)
		}
		if tlsSrv != nil {
			if err := tlsSrv.Shutdown(ctx); err != nil {
				log.Printf("HTTPS listener shutdown didn't finish cleanly: %v", err)
			}
		}
	}()

	log.Println("🚀 BiteBox Go server running on http://localhost:8080/table/1")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Server error: %v", err)
	}
	log.Println("✅ Server stopped cleanly")
}
