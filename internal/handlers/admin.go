package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"bitebox/internal/db"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
)

// orderHistoryLimit caps the all-time history panel — it's not pagination,
// just a sane bound so the page doesn't have to load every order ever placed.
const orderHistoryLimit = 200

// lowStockThreshold: an available, stock-tracked product at or below this
// count (but not 0 — that's "out of stock", already obvious from its own
// badge) surfaces in the dashboard's low-stock alert banner.
const lowStockThreshold = 5

// productView adds a live "available" count to a product: raw DB stock
// minus whatever's currently sitting in any guest's not-yet-checked-out
// cart. Reservations aren't written to the DB (only checkout decrements
// products.stock, atomically) — this is purely a display-time overlay, kept
// separate from the raw Stock field so admin edits (the stepper/Save) never
// accidentally write a reservation-adjusted number back as real inventory.
type productView struct {
	models.Product
	AvailableStock int
	Reserved       int // Stock - AvailableStock, i.e. currently sitting in someone's cart
	Ingredients    []models.Ingredient
	// HasCustomizableIngredient is true if this product has *any* ingredient
	// tag (removable or extra) — gates whether the guest menu shows a
	// "Customize" link at all.
	HasCustomizableIngredient bool
}

func buildProductView(p models.Product, ingredients []models.Ingredient) productView {
	hasCustomizable := len(ingredients) > 0

	if p.Stock == -1 {
		return productView{Product: p, AvailableStock: -1, Ingredients: ingredients, HasCustomizableIngredient: hasCustomizable}
	}
	reserved := 0
	if CartStore != nil {
		reserved = CartStore.ReservedQuantity(p.ID)
	}
	avail := p.Stock - reserved
	if avail < 0 {
		avail = 0
	}
	return productView{Product: p, AvailableStock: avail, Reserved: p.Stock - avail, Ingredients: ingredients, HasCustomizableIngredient: hasCustomizable}
}

// buildProductViews batch-fetches every product's ingredient tags in one
// query rather than one-per-product, since this feeds both the admin
// product list and (via buildGuestProductViews) the guest menu — the exact
// hot path flagged as slow.
func buildProductViews(products []models.Product) []productView {
	ids := make([]int, len(products))
	for i, p := range products {
		ids[i] = p.ID
	}
	byProduct, _ := db.GetIngredientsByProductIDs(ids)

	views := make([]productView, len(products))
	for i, p := range products {
		views[i] = buildProductView(p, byProduct[p.ID])
	}
	return views
}

type productCategoryGroup struct {
	Name     string
	Products []productView
}

// groupProductsByCategory buckets products into the fixed category order
// (Drinks, Food, Other), skipping empty buckets. Any product with an
// unrecognized/blank category (e.g. from before the category column
// existed) is folded into Other rather than dropped.
func groupProductsByCategory(products []productView) []productCategoryGroup {
	byCategory := make(map[string][]productView)
	for _, p := range products {
		cat := p.Category
		if !models.IsValidCategory(cat) {
			cat = models.CategoryOther
		}
		byCategory[cat] = append(byCategory[cat], p)
	}
	groups := make([]productCategoryGroup, 0, len(models.ProductCategories))
	for _, cat := range models.ProductCategories {
		if items, ok := byCategory[cat]; ok {
			groups = append(groups, productCategoryGroup{Name: cat, Products: items})
		}
	}
	return groups
}

// findLowStockProduct returns the tightest available (reservation-aware),
// stock-tracked product at or below lowStockThreshold (but above zero), for
// the alert banner.
func findLowStockProduct(products []productView) (productView, bool) {
	var found productView
	ok := false
	for _, p := range products {
		if !p.IsAvailable || p.AvailableStock == -1 || p.AvailableStock <= 0 || p.AvailableStock > lowStockThreshold {
			continue
		}
		if !ok || p.AvailableStock < found.AvailableStock {
			found = p
			ok = true
		}
	}
	return found, ok
}

type trendBarView struct {
	db.DayRevenue
	Percent int
}

// buildTrendBars converts raw daily revenue into bar-chart heights (0-100,
// floored at 4 so a zero-revenue day still renders a visible sliver).
func buildTrendBars(days []db.DayRevenue) []trendBarView {
	max := 0.0
	for _, d := range days {
		if d.Revenue > max {
			max = d.Revenue
		}
	}
	views := make([]trendBarView, len(days))
	for i, d := range days {
		pct := 4
		if max > 0 {
			pct = int(d.Revenue / max * 100)
			if pct < 4 {
				pct = 4
			}
		}
		views[i] = trendBarView{DayRevenue: d, Percent: pct}
	}
	return views
}

// AdminHome renders the dashboard shell. The menu list, table grid, stats,
// and staff list are all deliberately left for their own lazy hx-get (same
// "start empty, fetch once" pattern used for every other live-updating panel
// in this app) rather than computed here — keeps this handler's own DB work
// down to what only the initial page load needs (history + trend, neither
// of which has a dedicated fragment endpoint of its own).
func AdminHome(w http.ResponseWriter, r *http.Request) {
	products, _ := db.GetAllProducts() // only for the low-stock alert
	history, _ := db.GetOrderHistory(orderHistoryLimit)
	trend, _ := db.GetRevenueTrend(7)
	prevWeek, _ := db.GetRevenueForRange(13, 7)
	settings, _ := db.GetSettings()

	lowStock, hasLowStock := findLowStockProduct(buildProductViews(products))

	var trendTotal float64
	for _, d := range trend {
		trendTotal += d.Revenue
	}
	hasTrendDelta := prevWeek > 0
	trendDeltaPercent := 0.0
	if hasTrendDelta {
		trendDeltaPercent = (trendTotal - prevWeek) / prevWeek * 100
	}

	tmpl := template.Must(template.ParseFiles("templates/admin_dashboard.html"))
	tmpl.Execute(w, map[string]interface{}{
		"Categories":        models.ProductCategories,
		"History":           history,
		"Trend":             buildTrendBars(trend),
		"TrendTotal":        trendTotal,
		"HasTrendDelta":     hasTrendDelta,
		"TrendDeltaPercent": trendDeltaPercent,
		"LowStock":          lowStock,
		"HasLowStock":       hasLowStock,
		"DJRequestsEnabled": settings.DJRequestsEnabled,
	})
}

// AdminToggleDJRequests flips the venue-wide DJ-requests feature toggle and
// broadcasts it live to every guest currently browsing the menu.
func AdminToggleDJRequests(w http.ResponseWriter, r *http.Request) {
	settings, err := db.GetSettings()
	if err != nil {
		http.Error(w, "Failed to load settings", http.StatusInternalServerError)
		return
	}
	if err := db.SetDJRequestsEnabled(!settings.DJRequestsEnabled); err != nil {
		http.Error(w, "Failed to update setting", http.StatusInternalServerError)
		return
	}
	renderDJToggle(w)
	BroadcastDJSection()
}

func renderDJToggle(w http.ResponseWriter) {
	settings, _ := db.GetSettings()
	tmpl := template.Must(template.ParseFiles("templates/_dj_toggle.html"))
	tmpl.Execute(w, map[string]interface{}{"DJRequestsEnabled": settings.DJRequestsEnabled})
}

func renderAdminProductListHTML() ([]byte, error) {
	products, err := db.GetAllProducts()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_product_list.html"))
	if err := tmpl.Execute(&buf, map[string]interface{}{"MenuGroups": groupProductsByCategory(buildProductViews(products))}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func renderProductList(w http.ResponseWriter) {
	b, err := renderAdminProductListHTML()
	if err != nil {
		http.Error(w, "Failed to load menu", http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

// AdminProductList serves the #menuList fragment for its initial hx-get load.
func AdminProductList(w http.ResponseWriter, r *http.Request) {
	renderProductList(w)
}

func renderTableListHTML() ([]byte, error) {
	tables, err := db.GetAllTables()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_table_list.html"))
	if err := tmpl.Execute(&buf, map[string]interface{}{"Tables": tables}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BroadcastAdminTables pushes a fresh table grid to every admin dashboard —
// called whenever any table's occupancy changes, from any direction (a
// guest claiming/leaving it, or an admin creating/force-releasing/removing
// one).
func BroadcastAdminTables() {
	b, err := renderTableListHTML()
	if err != nil {
		return
	}
	Hub.Broadcast(topicAdminTables, oobWrap("table-grid", b))
}

func renderTableList(w http.ResponseWriter) {
	b, err := renderTableListHTML()
	if err != nil {
		http.Error(w, "Failed to load tables", http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

// AdminTableList serves the #table-grid fragment for its initial hx-get load.
func AdminTableList(w http.ResponseWriter, r *http.Request) {
	renderTableList(w)
}

// parseStock reads the optional "stock" form field: blank means -1
// (unlimited/untracked, the products.stock column default), anything else
// must be a non-negative integer.
func parseStock(r *http.Request) (int, bool) {
	raw := r.FormValue("stock")
	if raw == "" {
		return -1, true
	}
	stock, err := strconv.Atoi(raw)
	if err != nil || stock < -1 {
		return 0, false
	}
	return stock, true
}

// parseCategory reads the "category" form field, falling back to "Other"
// for anything blank or unrecognized rather than rejecting the request —
// this is a display grouping, not a validated business rule.
func parseCategory(r *http.Request) string {
	cat := r.FormValue("category")
	if !models.IsValidCategory(cat) {
		return models.CategoryOther
	}
	return cat
}

func AdminCreateProduct(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	price, err := strconv.ParseFloat(r.FormValue("price"), 64)
	stock, stockOK := parseStock(r)
	if name == "" || err != nil || price < 0 || !stockOK {
		http.Error(w, "Invalid product name, price, or stock", http.StatusBadRequest)
		return
	}
	if _, err := db.CreateProduct(name, price, stock, parseCategory(r)); err != nil {
		http.Error(w, "Failed to create product", http.StatusInternalServerError)
		return
	}
	renderProductList(w)
	BroadcastAllMenus()
}

func AdminUpdateProduct(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid product id", http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	price, err := strconv.ParseFloat(r.FormValue("price"), 64)
	stock, stockOK := parseStock(r)
	if name == "" || err != nil || price < 0 || !stockOK {
		http.Error(w, "Invalid product name, price, or stock", http.StatusBadRequest)
		return
	}
	if err := db.UpdateProduct(id, name, price, stock, parseCategory(r)); err != nil {
		http.Error(w, "Failed to update product", http.StatusInternalServerError)
		return
	}
	renderProductList(w)
	BroadcastAllMenus()
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
	BroadcastAllMenus()
}

// AdminAddIngredient tags a product with an ingredient — "removable" ones
// (the default kind) are what the guest customize panel lets someone
// exclude; "extra" ones are stored for admin management but not yet
// surfaced anywhere on the guest side (no add-on/pricing flow exists for
// them yet).
func AdminAddIngredient(w http.ResponseWriter, r *http.Request) {
	productID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid product id", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	kind := r.FormValue("kind")
	if !models.IsValidIngredientKind(kind) {
		kind = models.IngredientRemovable
	}
	if name == "" {
		http.Error(w, "Ingredient name required", http.StatusBadRequest)
		return
	}
	if _, err := db.CreateIngredient(productID, name, kind); err != nil {
		http.Error(w, "Failed to add ingredient", http.StatusInternalServerError)
		return
	}
	renderProductList(w)
	BroadcastAllMenus()
}

func AdminDeleteIngredient(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Invalid ingredient id", http.StatusBadRequest)
		return
	}
	if err := db.DeleteIngredient(id); err != nil {
		http.Error(w, "Failed to remove ingredient", http.StatusInternalServerError)
		return
	}
	renderProductList(w)
	BroadcastAllMenus()
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
	BroadcastAdminTables()
}

// AdminReleaseTable force-releases an occupied table (e.g. a guest walked
// off without hitting "Leave Table"). Also clears whatever cart that guest's
// session had — otherwise its reserved stock would stay locked away from
// everyone else forever, since nothing else would ever clear it.
func AdminReleaseTable(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil {
		http.Error(w, "Invalid table number", http.StatusBadRequest)
		return
	}
	table, err := db.GetTable(number)
	if err != nil {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}
	if err := db.ReleaseTable(number); err != nil {
		http.Error(w, "Failed to release table", http.StatusInternalServerError)
		return
	}
	if table.HostSessionID != "" && CartStore != nil {
		CartStore.Clear(table.HostSessionID)
	}
	renderTableList(w)
	BroadcastAdminTables()
	BroadcastAllMenus()
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
	BroadcastAdminTables()
}

// AdminExportCSV downloads the full (uncapped) order history as CSV.
func AdminExportCSV(w http.ResponseWriter, r *http.Request) {
	orders, err := db.GetOrderHistory(-1) // SQLite: LIMIT -1 means no limit
	if err != nil {
		http.Error(w, "Failed to export orders", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="bitebox-orders.csv"`)

	fmt.Fprintln(w, "order_id,table,created_at,status,payment_method,payment_status,served_by,total,items")
	for _, o := range orders {
		items := ""
		for i, it := range o.Items {
			if i > 0 {
				items += "; "
			}
			items += fmt.Sprintf("%dx %s", it.Quantity, it.Name)
		}
		fmt.Fprintf(w, "%d,%d,%s,%s,%s,%s,%s,%.2f,%q\n",
			o.ID, o.TableNumber, o.CreatedAt.Local().Format("2006-01-02 15:04"),
			o.Status, o.PaymentMethod, o.PaymentStatus, o.ServedBy, o.TotalAmount, items)
	}
}
