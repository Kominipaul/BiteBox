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

	// Guest cart & checkout
	r.Post("/cart/add", cartHandlers.Add)
	r.Post("/cart/remove", cartHandlers.Remove)
	r.Post("/cart/checkout", cartHandlers.Checkout)

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
		r.Post("/admin/products", handlers.AdminCreateProduct)
		r.Post("/admin/products/{id}", handlers.AdminUpdateProduct)
		r.Post("/admin/products/{id}/toggle", handlers.AdminToggleProduct)
		r.Post("/admin/tables", handlers.AdminCreateTable)
		r.Post("/admin/tables/{number}/release", handlers.AdminReleaseTable)
		r.Delete("/admin/tables/{number}", handlers.AdminDeleteTable)
	})

	// Worker dashboard
	r.Group(func(r chi.Router) {
		r.Use(handlers.RequireRole(models.RoleAdmin, models.RoleWorker))
		r.Get("/worker", handlers.WorkerHome)
		r.Get("/worker/orders/feed", handlers.WorkerOrdersFeed)
		r.Post("/worker/orders/{id}/status", handlers.WorkerUpdateOrderStatus)
		r.Post("/worker/orders/{id}/paid", handlers.WorkerMarkPaid)
		r.Get("/worker/dj/feed", handlers.WorkerDJFeed)
		r.Post("/worker/dj/{id}/accept", handlers.WorkerDJAccept)
		r.Post("/worker/dj/{id}/reject", handlers.WorkerDJReject)
	})

	log.Println("🚀 BiteBox Go server running on http://localhost:8080/table/1")
	http.ListenAndServe(":8080", r)
}
