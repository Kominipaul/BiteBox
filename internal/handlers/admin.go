package handlers

import (
	"html/template"
	"net/http"
	"strconv"

	"bitebox/internal/db"

	"github.com/go-chi/chi/v5"
)

func AdminHome(w http.ResponseWriter, r *http.Request) {
	products, _ := db.GetAllProducts()
	tables, _ := db.GetAllTables()
	revenue, _ := db.GetTodayRevenue()
	orderCount, _ := db.GetTodayOrderCount()

	tmpl := template.Must(template.ParseFiles("templates/admin_dashboard.html"))
	tmpl.Execute(w, map[string]interface{}{
		"Products":   products,
		"Tables":     tables,
		"Revenue":    revenue,
		"OrderCount": orderCount,
	})
}

func renderProductList(w http.ResponseWriter) {
	products, _ := db.GetAllProducts()
	tmpl := template.Must(template.ParseFiles("templates/_product_list.html"))
	tmpl.Execute(w, map[string]interface{}{"Products": products})
}

func renderTableList(w http.ResponseWriter) {
	tables, _ := db.GetAllTables()
	tmpl := template.Must(template.ParseFiles("templates/_table_list.html"))
	tmpl.Execute(w, map[string]interface{}{"Tables": tables})
}

func AdminCreateProduct(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	price, err := strconv.ParseFloat(r.FormValue("price"), 64)
	if name == "" || err != nil || price < 0 {
		http.Error(w, "Invalid product name or price", http.StatusBadRequest)
		return
	}
	if _, err := db.CreateProduct(name, price); err != nil {
		http.Error(w, "Failed to create product", http.StatusInternalServerError)
		return
	}
	renderProductList(w)
}

func AdminUpdateProduct(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid product id", http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	price, err := strconv.ParseFloat(r.FormValue("price"), 64)
	if name == "" || err != nil || price < 0 {
		http.Error(w, "Invalid product name or price", http.StatusBadRequest)
		return
	}
	if err := db.UpdateProduct(id, name, price); err != nil {
		http.Error(w, "Failed to update product", http.StatusInternalServerError)
		return
	}
	renderProductList(w)
}

func AdminToggleProduct(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid product id", http.StatusBadRequest)
		return
	}
	product, err := db.GetProductByID(id)
	if err != nil {
		http.Error(w, "Product not found", http.StatusNotFound)
		return
	}
	if err := db.SetProductAvailability(id, !product.IsAvailable); err != nil {
		http.Error(w, "Failed to update product", http.StatusInternalServerError)
		return
	}
	renderProductList(w)
}

func AdminCreateTable(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.Atoi(r.FormValue("number"))
	if err != nil || number <= 0 {
		http.Error(w, "Invalid table number", http.StatusBadRequest)
		return
	}
	if err := db.CreateTable(number); err != nil {
		http.Error(w, "Table number already exists", http.StatusConflict)
		return
	}
	renderTableList(w)
}

func AdminReleaseTable(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil {
		http.Error(w, "Invalid table number", http.StatusBadRequest)
		return
	}
	if err := db.ReleaseTable(number); err != nil {
		http.Error(w, "Failed to release table", http.StatusInternalServerError)
		return
	}
	renderTableList(w)
}

func AdminDeleteTable(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil {
		http.Error(w, "Invalid table number", http.StatusBadRequest)
		return
	}
	if err := db.DeleteTable(number); err != nil {
		if err == db.ErrTableOccupied {
			http.Error(w, "Cannot remove an occupied table", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to remove table", http.StatusInternalServerError)
		return
	}
	renderTableList(w)
}
