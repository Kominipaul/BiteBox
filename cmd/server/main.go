package main

import (
	"bufio"
	"context"
	"errors"
	"log"
	"net"
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
		case strings.HasPrefix(r.URL.Path, "uploads/"):
			// Product photos and brand images (logos, login photo) are
			// named by content hash. writeProductPhoto (photo.go) names every
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
		case r.URL.Query().Get("v") != "":
			// Our own CSS/JS linked as ?v=<assetVersion>: that URL changes
			// with every server start (deploy), so the browser can keep
			// it without ever asking again.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			// Our own CSS/JS (site.css, staff.css, floor.js, ...) change with
			// the code. Without a header browsers guess a cache lifetime and
			// keep serving an old stylesheet after an update; no-cache makes
			// them check first (a cheap 304 via Last-Modified when unchanged).
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

// htmlByDefault labels responses that never set a Content-Type as HTML
// (our template handlers), at the first write — Compress only gzips a
// response whose type is known by then. Anything that sets its own type
// (JSON, CSV, http.ServeFile) is left alone.
func htmlByDefault(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(htmlWriter{w}, r)
	})
}

type htmlWriter struct{ http.ResponseWriter }

func (w htmlWriter) label() {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
}
func (w htmlWriter) WriteHeader(code int) { w.label(); w.ResponseWriter.WriteHeader(code) }
func (w htmlWriter) Write(b []byte) (int, error) {
	w.label()
	return w.ResponseWriter.Write(b)
}
func (w htmlWriter) Flush() { http.NewResponseController(w.ResponseWriter).Flush() }
func (w htmlWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}
func (w htmlWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// redirectTo permanently sends an old URL to its new home.
func redirectTo(url string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, url, http.StatusMovedPermanently)
	}
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
	handlers.StartMenuScheduleWatcher()
	handlers.StartPopularityRanker()
	handlers.StartBookingSweep()
	go func() {
		for {
			db.Maintain()
			time.Sleep(24 * time.Hour)
		}
	}()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// gzip text responses (pages, htmx fragments, CSS/JS, JSON, CSV): cuts
	// them to ~1/5 on the wire. Images are already compressed.
	r.Use(middleware.Compress(5, "text/html", "text/css", "text/plain", "text/javascript",
		"application/javascript", "application/json", "text/csv", "image/svg+xml"))
	r.Use(htmlByDefault)
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
	r.Get("/privacy", handlers.Privacy)
	r.Get("/book", handlers.Booking)
	// The venue's look (colours, fonts, logo) — linked by every page.
	r.Get("/brand.css", handlers.BrandCSS)
	r.Get("/brand/icon", handlers.BrandIconPNG) // legacy path
	r.Get("/brand/icon.png", handlers.BrandIconPNG)
	r.Get("/brand/apple-touch-icon.png", handlers.BrandAppleIcon)
	r.Get("/book/availability", handlers.BookingAvailability)
	r.Post("/book/hold", handlers.BookingHold)
	r.Post("/book/confirm", handlers.BookingConfirm)
	r.Post("/book/release", handlers.BookingRelease)

	// Phone-notification plumbing: served at the origin root (not under
	// /static/*) — see handlers.ServiceWorker's doc comment on why that
	// matters for a service worker's control scope.
	r.Get("/sw.js", handlers.ServiceWorker)
	r.Get("/manifest.json", handlers.WebManifest)
	r.Get("/manifest-site.json", handlers.SiteManifest)
	// Guest PWA manifest — per-table start_url via ?table=N, see
	// handlers.GuestWebManifest's doc comment for why that matters on iOS.
	r.Get("/manifest-guest.json", handlers.GuestWebManifest)

	// Guest table access. /t/{token} is what a table's printed QR code
	// opens (and the only way to claim a table); /table/{number} is the
	// host's own page after that.
	r.Get("/t/{token}", table.Scan)
	r.Get("/table/{number}", table.View)
	r.Post("/table/{number}/leave", table.Leave)
	r.Get("/table/{number}/ws", handlers.TableStatusWS)
	// {number} in the URL is cosmetic — RequestAssistance resolves the
	// actual table from the caller's own guest session, same as Leave.
	r.Post("/table/{number}/assist", handlers.RequestAssistance)
	r.Post("/table/{number}/assist/cancel", handlers.CancelAssistance)
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
	r.Get("/orders/{id}/receipt", handlers.GuestOrderReceipt)

	// Guest-facing live menu
	r.Get("/menu/ws", handlers.MenuWS)

	// DJ song requests
	r.Post("/dj/request", handlers.DJRequest)

	// Staff auth
	r.Get("/login", handlers.LoginPage)
	r.With(handlers.LoginRateLimitMiddleware).Post("/login", handlers.LoginSubmit)
	r.With(handlers.LoginRateLimitMiddleware).Post("/login/code", handlers.LoginCodeSubmit)
	r.Post("/logout", handlers.Logout)

	// Back office (/admin/*): every page needs its own access (see
	// models.User.Can: admins open everything, other accounts their role's
	// defaults or the custom pages set on the Staff page).
	r.Group(func(r chi.Router) {
		r.Use(handlers.RequireRole(models.RoleAdmin, models.RoleWorker))
		r.Get("/admin/guide", handlers.AdminGuide) // every account: how the software works
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequirePage("dashboard"))
			r.Get("/admin", handlers.AdminHome)
			r.Get("/admin/stats", handlers.AdminStatsPeriod)
			r.Get("/admin/stats/ws", handlers.AdminStatsWS)
		})
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequirePage("bookings"))
			r.Get("/worker/bookings", handlers.WorkBookings) // Bookings inside the staff app (work mode)
			r.Get("/worker/bookings/floor", redirectTo("/worker/floor"))
			r.Get("/admin/bookings", handlers.StaffBookings)
			r.Get("/admin/bookings/rows", handlers.StaffBookingRows)
			r.Get("/admin/bookings/new", handlers.StaffBookingNew)
			r.Post("/admin/bookings", handlers.StaffBookingCreate)
			r.Get("/admin/bookings/{id}/edit", handlers.StaffBookingEdit)
			r.Post("/admin/bookings/{id}", handlers.StaffBookingUpdate)
			r.Post("/admin/bookings/{id}/status", handlers.StaffBookingStatus)
		})
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequirePage("floor"))
			r.Get("/admin/floor", handlers.StaffFloor)
			r.Get("/worker/floor", handlers.WorkFloor) // Floor inside the staff app (work mode)
			r.Get("/admin/floor/live", handlers.StaffFloorData)
			r.Get("/admin/floor/ws", handlers.FloorWS)
			r.Post("/admin/tables/{number}/release", handlers.StaffReleaseTable)
			r.Post("/admin/tables/{number}/service", handlers.StaffTableService)
		})
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequirePage("menu"))
			r.Get("/admin/products", handlers.AdminProductList)
			r.Get("/admin/menu/ws", handlers.AdminMenuWS)
			r.Post("/admin/products", handlers.AdminCreateProduct)
			r.Get("/admin/products/new", handlers.AdminProductNew)
			r.Get("/admin/products/{id}/edit", handlers.AdminProductEditor)
			r.Post("/admin/products/{id}/stock", handlers.AdminProductStock)
			r.Post("/admin/products/{id}", handlers.AdminUpdateProduct)
			r.Post("/admin/products/{id}/toggle", handlers.AdminToggleProduct)
			r.Delete("/admin/products/{id}", handlers.AdminDeleteProduct)
			r.Post("/admin/products/{id}/ingredients", handlers.AdminAddIngredient)
			r.Delete("/admin/ingredients/{id}", handlers.AdminDeleteIngredient)
			r.Post("/admin/categories", handlers.AdminCreateCategory)
			r.Get("/admin/menu", handlers.AdminMenuPage)
			r.Get("/admin/menu/categories", handlers.AdminCategoriesPage)
			// Menus & hours: scheduled menus, kitchen/bar switches, guest suggestions.
			r.Get("/admin/menu/hours", handlers.AdminHoursPage)
			r.Get("/admin/menu/hours/ws", handlers.AdminHoursWS)
			r.Get("/admin/menu/hours/preview", handlers.AdminHoursPreview)
			r.Post("/admin/products/{id}/recommend", handlers.AdminToggleRecommended)
			r.Get("/admin/menu/hours/new", handlers.AdminServiceMenuForm)
			r.Get("/admin/menu/hours/{id}/edit", handlers.AdminServiceMenuForm)
			r.Post("/admin/menu/hours", handlers.AdminServiceMenuSave)
			r.Post("/admin/menu/hours/suggestions", handlers.AdminGuestSuggestions)
			r.Post("/admin/menu/hours/rerank", handlers.AdminRerankPopularity)
			r.Post("/admin/menu/hours/{id}", handlers.AdminServiceMenuSave)
			r.Post("/admin/menu/hours/{id}/toggle", handlers.AdminServiceMenuToggle)
			r.Post("/admin/menu/hours/{id}/delete", handlers.AdminServiceMenuDelete)
			r.Post("/admin/stations/{dept}", handlers.AdminSetStation)
			r.Post("/admin/stations/{dept}/hours", handlers.AdminSetStationHours)
			r.Post("/admin/stations/{dept}/busy", handlers.AdminSetBusy)
			r.Post("/admin/areas/pause", handlers.AdminSetArea)
			r.Get("/admin/menu/hours/special/new", handlers.AdminSpecialDayForm)
			r.Get("/admin/menu/hours/special/{id}/edit", handlers.AdminSpecialDayForm)
			r.Post("/admin/menu/hours/special", handlers.AdminSpecialDaySave)
			r.Post("/admin/menu/hours/special/{id}", handlers.AdminSpecialDaySave)
			r.Post("/admin/menu/hours/special/{id}/delete", handlers.AdminSpecialDayDelete)
		})
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequirePage("map"))
			r.Post("/admin/tables/{number}/layout", handlers.AdminTableLayout)
			r.Post("/admin/tables/{number}/position", handlers.AdminTablePosition)
			r.Post("/admin/map/preset", handlers.AdminFloorPreset)
			r.Get("/admin/map", handlers.AdminFloorEditor)
			r.Get("/admin/map/qr", handlers.AdminQRSheet)
			r.Get("/admin/tables/{number}/qr.png", handlers.AdminTableQRPNG)
			r.Post("/admin/tables/{number}/qr/rotate", handlers.AdminRotateTableQR)
			r.Get("/admin/map/data", handlers.AdminFloorData)
			r.Post("/admin/map/spots", handlers.AdminAddSpot)
			r.Post("/admin/map/spots/{number}/delete", handlers.AdminDeleteSpot)
		})
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequirePage("orders"))
			r.Get("/admin/orders/rows", handlers.AdminOrderRows)
			r.Get("/admin/orders/{id}", handlers.AdminOrderDetail)
			r.Post("/admin/orders/{id}/payment", handlers.AdminOrderPayment)
			r.Post("/admin/orders/{id}/status", handlers.AdminOrderStatus)
			r.Post("/admin/orders/{id}/cancel", handlers.AdminOrderCancel)
			r.Get("/admin/orders/{id}/receipt", handlers.AdminOrderReceipt)
			r.Get("/admin/orders", handlers.AdminOrdersPage)
		})
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequirePage("logs"))
			r.Get("/admin/logs/rows", handlers.AdminLogRows)
			r.Get("/admin/logs", handlers.AdminLogsPage)
		})
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequirePage("staff"))
			r.Post("/admin/staff", handlers.AdminCreateStaff)
			r.Get("/admin/staff/new", handlers.AdminStaffNew)
			r.Post("/admin/staff/{id}", handlers.AdminUpdateStaff)
			r.Post("/admin/staff/{id}/deactivate", handlers.AdminDeactivateStaff)
			r.Post("/admin/staff/{id}/activate", handlers.AdminActivateStaff)
		})
		// Staff help: an admin, or a manager helping the floor staff. The
		// handlers themselves refuse anything a manager may not touch (see
		// mayHelp): admins, other managers, themselves.
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequirePage("team"))
			r.Get("/admin/team", handlers.AdminTeamPage)
			r.Get("/admin/staff", handlers.AdminStaffList)
			r.Get("/admin/staff/{id}/edit", handlers.AdminStaffEdit)
			r.Post("/admin/staff/{id}/reset-password", handlers.AdminResetStaffPassword)
			r.Post("/admin/staff/{id}/signout", handlers.AdminStaffSignOut)
		})
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequirePage("settings"))
			r.Get("/admin/backup", handlers.AdminDownloadBackup)
			r.Get("/admin/export.csv", handlers.AdminExportCSV) // the whole sales history: owner only
			r.Post("/admin/settings/dj-requests/toggle", handlers.AdminToggleDJRequests)
			r.Post("/admin/settings/cash-orders/toggle", handlers.AdminToggleCashOrders)
			r.Post("/admin/settings/venue-name", handlers.AdminUpdateVenueName)
			r.Post("/admin/settings/business-details", handlers.AdminUpdateBusinessDetails)
			r.Post("/admin/settings/booking", handlers.AdminBookingSettings)
			r.Get("/admin/bookings/settings", handlers.AdminBookingSettingsPage)
			r.Get("/admin/settings", handlers.AdminSettingsPage)
		})
		// The staff app inside the admin area (admins only; everyone else
		// uses /worker).
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequirePage("live"))
			r.Get("/admin/live", handlers.AdminLive)
		})
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequirePage("system"))
			r.Get("/admin/system", handlers.AdminSystemPage)
			r.Post("/admin/system", handlers.AdminSystemSave)
			r.Post("/admin/system/preview", handlers.AdminSystemPreview)
			r.Post("/admin/system/upload", handlers.AdminSystemUpload)
		})
	})

	// Worker dashboard
	r.Group(func(r chi.Router) {
		r.Use(handlers.RequireRole(models.RoleAdmin, models.RoleWorker))
		r.Get("/worker", handlers.WorkerHome)

		// Change password — reachable both as a forced redirect (see
		// RequireRole above, and models.User.MustChangePassword) and as a
		// voluntary link from either dashboard's topbar. Registered here,
		// inside the same RequireRole group it must be exempted from, so it
		// covers every role that can ever carry the forced flag.
		r.Get("/change-password", handlers.ChangePasswordPage)
		r.Post("/change-password", handlers.ChangePasswordSubmit)
		// Two-step login: required for admins (RequireRole sends them here
		// until it's on), optional for everyone else.
		r.Get("/account/2fa", handlers.TwoFactorPage)
		r.Post("/account/2fa", handlers.TwoFactorEnable)
		r.Post("/account/2fa/off", handlers.TwoFactorDisable)

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
			// Sold-out board: bar/kitchen (their categories) and managers (all).
			r.Get("/worker/stock", handlers.WorkerStock)
			r.Post("/worker/stock/{id}/toggle", handlers.WorkerStockToggle)
			r.Post("/worker/stations/{dept}", handlers.WorkerSetStation) // close/reopen your own kitchen or bar
			r.Post("/worker/stations/{dept}/busy", handlers.WorkerSetBusy)
		})

		// Collect: waiters and managers settle tables.
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequireDepartment(models.DepartmentSuperworker, models.DepartmentWaiter))
			r.Get("/worker/pay", handlers.WorkerPayList)
			r.Post("/worker/pay/table/{number}/paid", handlers.WorkerPayTable)
		})

		// DJ terminal: the DJ, and managers (who cover every station).
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequireDepartment(models.DepartmentDJ, models.DepartmentSuperworker))
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
			log.Printf("🔒 Local HTTPS also listening on https://<this machine's LAN IP>%s (cert: %s)", tlsAddr, certFile) // #nosec G706 -- operator settings
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

	if t, err := db.GetTable(1); err == nil {
		log.Printf("🚀 BiteBox Go server running on http://localhost:8080 — table 1's QR code: http://localhost:8080/t/%s (all codes: /admin/map/qr)", t.QRToken)
	} else {
		log.Println("🚀 BiteBox Go server running on http://localhost:8080 (table QR codes: /admin/map/qr)")
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Server error: %v", err)
	}
	log.Println("✅ Server stopped cleanly")
}
