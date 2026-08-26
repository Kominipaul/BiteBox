package main

import (
	"log"
	"net/http"

	"bitebox/internal/cart"
	"bitebox/internal/db"
	"bitebox/internal/handlers"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	db.InitDB()

	cartStore := cart.NewStore()
	handlers.CartStore = cartStore
	table := &handlers.TableHandlers{Cart: cartStore}
	cartHandlers := &handlers.CartHandlers{Cart: cartStore}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Guest table access
	r.Get("/table/{number}", table.View)
	r.Get("/table/{number}/left", table.Left)
	r.Post("/table/{number}/leave", table.Leave)
	r.Get("/table/{number}/order-status", handlers.OrderStatusPoll)
	r.Get("/table/{number}/ws", handlers.TableStatusWS)

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
	r.Post("/login", handlers.LoginSubmit)
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
		r.Get("/admin/tables", handlers.AdminTableList)
		r.Get("/admin/tables/ws", handlers.AdminTablesWS)
		r.Post("/admin/tables", handlers.AdminCreateTable)
		r.Post("/admin/tables/{number}/release", handlers.AdminReleaseTable)
		r.Delete("/admin/tables/{number}", handlers.AdminDeleteTable)
		r.Get("/admin/staff", handlers.AdminStaffList)
		r.Post("/admin/staff", handlers.AdminCreateStaff)
		r.Post("/admin/staff/{id}/deactivate", handlers.AdminDeactivateStaff)
		r.Post("/admin/staff/{id}/activate", handlers.AdminActivateStaff)
		r.Post("/admin/settings/dj-requests/toggle", handlers.AdminToggleDJRequests)
	})

	// Worker dashboard
	r.Group(func(r chi.Router) {
		r.Use(handlers.RequireRole(models.RoleAdmin, models.RoleWorker))
		r.Get("/worker", handlers.WorkerHome)

		// Orders: everyone except the DJ department (bar/kitchen get a
		// category-filtered feed, staff/admin get everything).
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequireDepartment(models.DepartmentSuperworker, models.DepartmentWaiter, models.DepartmentBar, models.DepartmentKitchen))
			r.Get("/worker/orders/feed", handlers.WorkerOrdersFeed)
			r.Get("/worker/orders/ws", handlers.WorkerOrdersWS)
			r.Post("/worker/orders/{id}/status", handlers.WorkerUpdateOrderStatus)
			r.Post("/worker/orders/{id}/paid", handlers.WorkerMarkPaid)
			r.Post("/worker/orders/{id}/unpaid", handlers.WorkerMarkUnpaid)
			r.Post("/worker/orders/{id}/cancel", handlers.WorkerCancelOrder)
		})

		// DJ terminal: DJ department only.
		r.Group(func(r chi.Router) {
			r.Use(handlers.RequireDepartment(models.DepartmentDJ))
			r.Get("/worker/dj/feed", handlers.WorkerDJFeed)
			r.Get("/worker/dj/ws", handlers.WorkerDJWS)
			r.Post("/worker/dj/{id}/accept", handlers.WorkerDJAccept)
			r.Post("/worker/dj/{id}/reject", handlers.WorkerDJReject)
		})
	})

	log.Println("🚀 BiteBox Go server running on http://localhost:8080/table/1")
	http.ListenAndServe(":8080", r)
}
