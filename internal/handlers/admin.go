package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"

	"bitebox/internal/db"
	"bitebox/internal/imageproc"
	"bitebox/internal/models"

	"github.com/go-chi/chi/v5"
)

// orderHistoryPageSize is how many rows the order-history panel loads at a
// time — the first page renders inline with the rest of AdminHome, each
// "Load more" click (AdminOrderHistoryPage) fetches the next one, so the
// panel scales to a venue's real all-time order count instead of the old
// flat 200-row cutoff.
const orderHistoryPageSize = 50

// Length caps on free-text admin input — not a business rule, just a floor
// against a multi-megabyte name/description bloating storage and breaking
// every layout that renders it (product cards, CSV export, the live menu
// pushed to every connected guest).
const (
	maxProductNameLen    = 100
	maxDescriptionLen    = 1000
	maxCategoryNameLen   = 60
	maxIngredientNameLen = 60
	maxImageURLLen       = 500
)

// localImagePathPrefixes are the two server-generated (never admin-typed)
// local paths a product's image_url can legitimately hold: an uploaded
// photo (writeProductPhoto, internal/handlers/photo.go) or a seed demo
// asset (seed_ego.go's demoBrunchPhoto). parseImageURL has to accept these,
// not just a freshly pasted http(s) URL — see its own doc comment for why.
var localImagePathPrefixes = []string{"/" + productPhotoDir + "/", "/static/demo/"}

// parseImageURL validates the product-photo URL field: blank is fine (no
// photo), a pasted http(s) URL is fine under the length cap, and so is
// either of localImagePathPrefixes' own paths round-tripping unchanged.
//
// That last case matters more than it looks: _product_list.html's hidden
// "image_url" input always carries the product's *current* value, and gets
// resubmitted on every Save — including a plain stock change that never
// touches the photo at all. Once a product has an uploaded photo (or the
// seeded demo one), that hidden field holds a local "/static/..." path, not
// an http(s) URL. Rejecting anything but http(s) here — which is what this
// function used to do, reasonably enough for a *pasted* URL — meant that
// *every* Save on *any* product with a photo failed validation and 400'd
// silently (htmx doesn't swap in an error response), including the stock
// stepper and the Unlimited toggle: nothing about that product could ever
// be changed again short of clearing its photo first. Real upload paths and
// the demo path are both entirely server-generated, never typed by an
// admin, so accepting them back verbatim isn't trusting new input — it's
// the same value this handler (or the seed) wrote in the first place.
func parseImageURL(r *http.Request) (string, bool) {
	url := strings.TrimSpace(r.FormValue("image_url"))
	if url == "" {
		return "", true
	}
	if len(url) > maxImageURLLen {
		return "", false
	}
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url, true
	}
	for _, prefix := range localImagePathPrefixes {
		if strings.HasPrefix(url, prefix) {
			return url, true
		}
	}
	return "", false
}

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
	// ThumbURL is the small (see imageproc.ThumbMaxDimension), separately-
	// stored variant of ImageURL — derived by filename convention
	// (thumbURLFor), not its own DB column. Templates use this for the
	// menu-list photo box, and ImageURL itself for anything that wants the
	// larger copy.
	ThumbURL    string
	Ingredients []models.Ingredient
	// RemovableIngredients/ExtraIngredients split Ingredients by kind so the
	// admin ingredient panel can render two clearly-labeled groups ("guests
	// can leave these out" vs. "optional add-ons") instead of one mixed
	// list — precomputed here rather than in the template since html/template
	// has no filter action of its own.
	RemovableIngredients []models.Ingredient
	ExtraIngredients     []models.Ingredient
	// HasCustomizableIngredient is true if this product has *any* ingredient
	// tag (removable or extra) — gates whether the guest menu shows a
	// "Customize" link at all.
	HasCustomizableIngredient bool
}

func buildProductView(p models.Product, ingredients []models.Ingredient) productView {
	hasCustomizable := len(ingredients) > 0
	var removable, extra []models.Ingredient
	for _, ing := range ingredients {
		if ing.Kind == models.IngredientExtra {
			extra = append(extra, ing)
		} else {
			removable = append(removable, ing)
		}
	}

	thumbURL := thumbURLFor(p.ImageURL)

	if p.Stock == -1 {
		return productView{Product: p, AvailableStock: -1, ThumbURL: thumbURL, Ingredients: ingredients, RemovableIngredients: removable, ExtraIngredients: extra, HasCustomizableIngredient: hasCustomizable}
	}
	reserved := 0
	if CartStore != nil {
		reserved = CartStore.ReservedQuantity(p.ID)
	}
	avail := p.Stock - reserved
	if avail < 0 {
		avail = 0
	}
	return productView{Product: p, AvailableStock: avail, Reserved: p.Stock - avail, ThumbURL: thumbURL, Ingredients: ingredients, RemovableIngredients: removable, ExtraIngredients: extra, HasCustomizableIngredient: hasCustomizable}
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

// groupProductsByCategory buckets products by category, in categoryNames'
// order (the order admin-managed categories were created in — see
// db.GetCategories), skipping any category with nothing in it. Any product
// whose category isn't in categoryNames at all (legacy data, or a category
// that's since been renamed/removed at the DB level — there's no delete/
// rename UI yet, but nothing stops editing the table directly) is folded
// into Other rather than silently dropped from the menu.
//
// Used for both the guest and admin menu views — they used to differ (the
// guest view further split each category into Subcategory sub-groups), but
// categories are flat now: what used to be a Subcategory (e.g. "Brunch",
// "Red Wine") *is* the category, so there's nothing left to split.
func groupProductsByCategory(products []productView, categoryNames []string) []productCategoryGroup {
	valid := make(map[string]bool, len(categoryNames))
	for _, c := range categoryNames {
		valid[c] = true
	}
	byCategory := make(map[string][]productView)
	for _, p := range products {
		cat := p.Category
		if !valid[cat] {
			cat = models.CategoryOther
		}
		byCategory[cat] = append(byCategory[cat], p)
	}
	groups := make([]productCategoryGroup, 0, len(categoryNames))
	for _, cat := range categoryNames {
		if items, ok := byCategory[cat]; ok {
			groups = append(groups, productCategoryGroup{Name: cat, Products: items})
		}
	}
	return groups
}

// categoryNames extracts just the names, in the same order, for callers
// that only care about membership/ordering (grouping, the add-product
// category dropdown) and not each category's department.
func categoryNames(cats []models.Category) []string {
	names := make([]string, len(cats))
	for i, c := range cats {
		names[i] = c.Name
	}
	return names
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
	categories, _ := db.GetCategories()
	history, _ := db.GetOrderHistory(orderHistoryPageSize, 0)
	historyTotal, _ := db.GetOrderHistoryCount()
	trend, _ := db.GetRevenueTrend(7)
	prevWeek, _ := db.GetRevenueForRange(13, 7)
	settings, _ := db.GetSettings()

	productViews := buildProductViews(products)
	lowStock, hasLowStock := findLowStockProduct(productViews)
	// CategoryTabs drives the menu panel's category filter bar — built from
	// the same data #menuList itself renders from (GetAllProducts, every
	// product regardless of availability), so the counts shown on each tab
	// always match what's actually there, and a category with nothing in it
	// simply doesn't get a tab (same "skip empty buckets" rule as the list).
	categoryTabs := groupProductsByCategory(productViews, categoryNames(categories))

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
		"Categories":        categories,
		"CategoryTabs":      categoryTabs,
		"History":           history,
		"HasMoreHistory":    historyTotal > len(history),
		"NextHistoryOffset": len(history),
		"Trend":             buildTrendBars(trend),
		"TrendTotal":        trendTotal,
		"HasTrendDelta":     hasTrendDelta,
		"TrendDeltaPercent": trendDeltaPercent,
		"LowStock":          lowStock,
		"HasLowStock":       hasLowStock,
		"DJRequestsEnabled": settings.DJRequestsEnabled,
		"AllowCashOrders":   settings.AllowCashOrders,
		"BusinessVATNumber": settings.BusinessVATNumber,
		"TaxOffice":         settings.TaxOffice,
		"VenueName":         settings.VenueName,
	})
}

// AdminOrderHistoryPage serves one page of the order-history panel past the
// first (which AdminHome renders inline) — the "Load more" button's target,
// requesting the next offset each time. Renders the same row markup as the
// initial page (_order_history_rows.html, a `{{define}}` block shared with
// admin_dashboard.html) plus, when more remain, a fresh "Load more" button
// pointing at the next offset — when it doesn't, nothing replaces the old
// button, so it simply disappears from the page (see hx-swap="outerHTML" on
// the button itself in admin_dashboard.html).
func AdminOrderHistoryPage(w http.ResponseWriter, r *http.Request) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	history, err := db.GetOrderHistory(orderHistoryPageSize, offset)
	if err != nil {
		http.Error(w, "Failed to load order history", http.StatusInternalServerError)
		return
	}
	total, _ := db.GetOrderHistoryCount()
	tmpl := template.Must(template.ParseFiles("templates/admin_dashboard.html"))
	tmpl.ExecuteTemplate(w, "order-history-rows", map[string]interface{}{
		"History":           history,
		"HasMoreHistory":    total > offset+len(history),
		"NextHistoryOffset": offset + len(history),
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

// AdminToggleCashOrders flips whether guests can pay cash at checkout and
// broadcasts it live — reuses BroadcastAllMenus's existing cart-refresh
// nudge (see menu.go's cartRefreshTrigger) rather than a new broadcast
// path, so any guest with their checkout screen already open re-fetches
// #cart-summary (and therefore this setting) the same way it already does
// for a price/stock change. Card is never toggleable — see
// models.Settings' doc comment.
func AdminToggleCashOrders(w http.ResponseWriter, r *http.Request) {
	settings, err := db.GetSettings()
	if err != nil {
		http.Error(w, "Failed to load settings", http.StatusInternalServerError)
		return
	}
	if err := db.SetAllowCashOrders(!settings.AllowCashOrders); err != nil {
		http.Error(w, "Failed to update setting", http.StatusInternalServerError)
		return
	}
	renderCashToggle(w)
	BroadcastAllMenus()
}

func renderCashToggle(w http.ResponseWriter) {
	settings, _ := db.GetSettings()
	tmpl := template.Must(template.ParseFiles("templates/_cash_toggle.html"))
	tmpl.Execute(w, map[string]interface{}{"AllowCashOrders": settings.AllowCashOrders})
}

// AdminUpdateVenueName renames the venue. Responds with the venue-settings
// panel fragment (its own hx-target) plus an OOB fragment updating this same
// admin's own header h1 — both land in one plain HTTP response, no
// websocket needed for the actor's own page. BroadcastVenueName separately
// pushes the change to every guest already browsing the menu live.
func AdminUpdateVenueName(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "Venue name can't be empty", http.StatusBadRequest)
		return
	}
	if len(name) > 80 {
		name = name[:80]
	}
	if err := db.SetVenueName(name); err != nil {
		http.Error(w, "Failed to update venue name", http.StatusInternalServerError)
		return
	}
	renderVenuePanel(w)
	fmt.Fprintf(w, `<h1 class="venue-name" id="venue-name-admin" hx-swap-oob="true">%s</h1>`, template.HTMLEscapeString(name))
	BroadcastVenueName(name)
}

func renderVenuePanel(w http.ResponseWriter) {
	settings, _ := db.GetSettings()
	tmpl := template.Must(template.ParseFiles("templates/_venue_panel.html"))
	tmpl.Execute(w, map[string]interface{}{"VenueName": settings.VenueName})
}

// maxBusinessFieldLen bounds the demo ΑΦΜ/ΔΟΥ fields — same "floor against
// a pasted wall of text" reasoning as the other admin text-field caps, not
// a format validation (see models.Settings.BusinessVATNumber's doc comment
// on why this is demo-only and never validated as a real tax ID).
const maxBusinessFieldLen = 100

// AdminUpdateBusinessDetails sets the demo receipt's ΑΦΜ/ΔΟΥ fields — never
// guest-facing (only appears on the admin-generated receipt printout), so
// unlike the venue name this needs no live broadcast to anyone.
func AdminUpdateBusinessDetails(w http.ResponseWriter, r *http.Request) {
	vatNumber := strings.TrimSpace(r.FormValue("business_vat_number"))
	taxOffice := strings.TrimSpace(r.FormValue("tax_office"))
	if len(vatNumber) > maxBusinessFieldLen || len(taxOffice) > maxBusinessFieldLen {
		http.Error(w, "Business details are too long", http.StatusBadRequest)
		return
	}
	if err := db.SetBusinessDetails(vatNumber, taxOffice); err != nil {
		http.Error(w, "Failed to update business details", http.StatusInternalServerError)
		return
	}
	renderBusinessPanel(w)
}

func renderBusinessPanel(w http.ResponseWriter) {
	settings, _ := db.GetSettings()
	tmpl := template.Must(template.ParseFiles("templates/_business_panel.html"))
	tmpl.Execute(w, map[string]interface{}{
		"BusinessVATNumber": settings.BusinessVATNumber,
		"TaxOffice":         settings.TaxOffice,
	})
}

func renderAdminProductListHTML() ([]byte, error) {
	products, err := db.GetAllProducts()
	if err != nil {
		return nil, err
	}
	categories, err := db.GetCategories()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	tmpl := template.Must(template.ParseFiles("templates/_product_list.html"))
	if err := tmpl.Execute(&buf, map[string]interface{}{"MenuGroups": groupProductsByCategory(buildProductViews(products), categoryNames(categories))}); err != nil {
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

// parseCategory reads the "category" form field, falling back to Other for
// anything blank or that isn't an existing category (see db.CategoryExists)
// rather than rejecting the request — this is a display grouping, not a
// validated business rule, and the < 1% case this actually hits is a
// tampered request, not a normal admin picking from the real <select> this
// form renders.
func parseCategory(r *http.Request) string {
	cat := r.FormValue("category")
	if ok, err := db.CategoryExists(cat); err != nil || !ok {
		return models.CategoryOther
	}
	return cat
}

// validVATRates are Greece's three actual VAT rates — standard, reduced,
// super-reduced — the only choices the admin form's <select> offers.
// Demo-receipt structure only (see models.Product.VATRate); rejecting
// anything else here is just "pick from the real list," the same
// reasoning as parseCategory, not a tax-law validation.
var validVATRates = map[float64]bool{24: true, 13: true, 6: true}

func parseVATRate(r *http.Request) float64 {
	rate, err := strconv.ParseFloat(r.FormValue("vat_rate"), 64)
	if err != nil || !validVATRates[rate] {
		return 24 // standard rate — same "fall back to the sane default" reasoning as parseCategory
	}
	return rate
}

func AdminCreateProduct(w http.ResponseWriter, r *http.Request) {
	if err := allowMultipartUpload(w, r); err != nil {
		http.Error(w, "Upload too large or malformed", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	price, err := strconv.ParseFloat(r.FormValue("price"), 64)
	stock, stockOK := parseStock(r)
	description := strings.TrimSpace(r.FormValue("description"))
	imageURL, imageOK := parseImageURL(r)
	if name == "" || len(name) > maxProductNameLen || len(description) > maxDescriptionLen || err != nil || price < 0 || !stockOK || !imageOK {
		http.Error(w, "Invalid product name, price, stock, or image URL", http.StatusBadRequest)
		return
	}

	// Validate/process a photo (if one was attached) before touching the
	// DB at all — a bad upload should reject the whole request cleanly,
	// not create the product and silently drop the photo.
	var photo imageproc.Result
	var hasPhoto bool
	if file, _, ferr := r.FormFile("photo"); ferr == nil {
		defer file.Close()
		photo, err = imageproc.Process(file)
		if err != nil {
			http.Error(w, photoUploadErrorMessage(err), http.StatusBadRequest)
			return
		}
		hasPhoto = true
	}

	vatRate := parseVATRate(r)
	id, err := db.CreateProduct(name, price, stock, parseCategory(r), description, imageURL, vatRate)
	if err != nil {
		http.Error(w, "Failed to create product", http.StatusInternalServerError)
		return
	}

	// The uploaded file takes priority over a typed URL — filenames are
	// keyed by product ID, so this can only happen after the insert above.
	if hasPhoto {
		if url, werr := writeProductPhoto(id, photo); werr != nil {
			log.Printf("product %d: failed to save uploaded photo: %v", id, werr)
		} else if uerr := db.UpdateProduct(id, name, price, stock, parseCategory(r), description, url, vatRate); uerr != nil {
			log.Printf("product %d: failed to attach uploaded photo: %v", id, uerr)
		}
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
	if err := allowMultipartUpload(w, r); err != nil {
		http.Error(w, "Upload too large or malformed", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	price, err := strconv.ParseFloat(r.FormValue("price"), 64)
	stock, stockOK := parseStock(r)
	description := strings.TrimSpace(r.FormValue("description"))
	imageURL, imageOK := parseImageURL(r)
	if name == "" || len(name) > maxProductNameLen || len(description) > maxDescriptionLen || err != nil || price < 0 || !stockOK || !imageOK {
		http.Error(w, "Invalid product name, price, stock, or image URL", http.StatusBadRequest)
		return
	}

	existing, _ := db.GetProductByID(id) // best-effort — only used to clean up its old photo file, if any

	if file, _, ferr := r.FormFile("photo"); ferr == nil {
		defer file.Close()
		photo, perr := imageproc.Process(file)
		if perr != nil {
			http.Error(w, photoUploadErrorMessage(perr), http.StatusBadRequest)
			return
		}
		url, werr := writeProductPhoto(id, photo)
		if werr != nil {
			http.Error(w, "Failed to save photo", http.StatusInternalServerError)
			return
		}
		deleteLocalPhotoIfAny(existing.ImageURL)
		imageURL = url
	} else if r.FormValue("remove_photo") == "1" {
		deleteLocalPhotoIfAny(existing.ImageURL)
		imageURL = ""
	}

	if err := db.UpdateProduct(id, name, price, stock, parseCategory(r), description, imageURL, parseVATRate(r)); err != nil {
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

// AdminDeleteProduct permanently removes a product — the one thing
// AdminToggleProduct (hide/show) never does. A guest with this product
// already sitting in their cart is unaffected server-side: resolveCart
// (cart.go) already treats a missing product exactly like a hidden one
// (Unavailable, excluded from the total, blocks checkout until removed),
// the same handling a delete-out-from-under-a-cart case needs. Order
// history is untouched — see DeleteProduct's own doc comment.
func AdminDeleteProduct(w http.ResponseWriter, r *http.Request) {
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
	// See ProductHasActiveOrders' own doc comment: the bar/kitchen live feed
	// joins order_items to products to route by category, so deleting a
	// product still sitting in a pending/preparing order would silently
	// drop that whole order out of the kitchen/bar's view before it's ever
	// prepared — not a dangling reference, a vanished order. Hiding it
	// (AdminToggleProduct) still works fine mid-order; only permanent
	// deletion needs to wait.
	if active, err := db.ProductHasActiveOrders(id); err != nil {
		http.Error(w, "Failed to check the product's order history", http.StatusInternalServerError)
		return
	} else if active {
		http.Error(w, "This item is part of an order that hasn't been served yet — hide it instead, or wait until that order is served/completed/cancelled.", http.StatusConflict)
		return
	}
	if err := db.DeleteProduct(id); err != nil {
		http.Error(w, "Failed to delete product", http.StatusInternalServerError)
		return
	}
	deleteLocalPhotoIfAny(product.ImageURL)
	renderProductList(w)
	BroadcastAllMenus()
}

// isValidCategoryDepartment is deliberately narrower than
// models.IsValidDepartment — a category only ever routes to whichever of
// the two departments actually run a product order feed (see
// db.CategoryNamesForDepartment); waiter/superworker/DJ aren't meaningful
// answers here.
func isValidCategoryDepartment(d string) bool {
	return d == models.DepartmentKitchen || d == models.DepartmentBar
}

// AdminCreateCategory adds a new admin-named menu category. Responds with
// HX-Refresh rather than re-rendering a fragment: the new category needs to
// show up in two places that are otherwise only ever rendered once, at page
// load (the add-product <select> and the category-tab filter bar — see
// AdminHome's own comment on why tabs aren't live-pushed), so a full reload
// is simpler and more correct here than wiring up two more OOB targets for
// what's an infrequent admin action.
func AdminCreateCategory(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	department := r.FormValue("department")
	if name == "" || len(name) > maxCategoryNameLen {
		http.Error(w, "Category name required (max 60 characters)", http.StatusBadRequest)
		return
	}
	if !isValidCategoryDepartment(department) {
		department = models.DepartmentBar
	}
	if exists, err := db.CategoryExists(name); err != nil {
		http.Error(w, "Failed to check category", http.StatusInternalServerError)
		return
	} else if exists {
		http.Error(w, "A category with that name already exists", http.StatusConflict)
		return
	}
	if _, err := db.CreateCategory(name, department); err != nil {
		http.Error(w, "Failed to create category", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Refresh", "true")
}

// AdminAddIngredient tags a product with an ingredient — "removable" ones
// (the default kind) are what the guest customize panel lets someone
// exclude, always free; "extra" ones are optional add-ons a guest can opt
// into, at the given price (see templates/_product_list.html's ingredient-
// add-form, which only shows the price field once "Extra" is selected).
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
	if name == "" || len(name) > maxIngredientNameLen {
		http.Error(w, "Ingredient name required (max 60 characters)", http.StatusBadRequest)
		return
	}
	// Price only ever applies to an "extra" — a removable tag is always
	// free, whatever a client happened to send for it.
	price := 0.0
	if kind == models.IngredientExtra {
		if raw := r.FormValue("price"); raw != "" {
			p, err := strconv.ParseFloat(raw, 64)
			if err != nil || p < 0 {
				http.Error(w, "Invalid price", http.StatusBadRequest)
				return
			}
			price = p
		}
	}
	if _, err := db.CreateIngredient(productID, name, kind, price); err != nil {
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

// sanitizeCSVField neutralizes formula injection: a field starting with a
// character a spreadsheet app (Excel, Sheets, LibreOffice) treats as a
// formula prefix gets opened as a live formula the moment the exported CSV
// is double-clicked, which can exfiltrate data or run commands via
// functions like DDE/HYPERLINK/WEBSERVICE. Every one of these fields
// (order status/payment method/served_by, and product names on order_items)
// ultimately traces back to admin-controlled input, but "admin-controlled"
// isn't "admin-typed" — product names in particular are exactly the kind of
// value someone might paste from an untrusted source. Prefixing with a
// leading apostrophe forces spreadsheet apps to treat the cell as plain
// text without changing the value CSV consumers that don't do formula
// evaluation actually see.
func sanitizeCSVField(s string) string {
	if len(s) > 0 {
		switch s[0] {
		case '=', '+', '-', '@', '\t', '\r':
			return "'" + s
		}
	}
	return s
}

// AdminExportCSV downloads the full (uncapped) order history as CSV.
func AdminExportCSV(w http.ResponseWriter, r *http.Request) {
	orders, err := db.GetOrderHistory(-1, 0) // SQLite: LIMIT -1 means no limit
	if err != nil {
		http.Error(w, "Failed to export orders", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="bitebox-orders.csv"`)

	fmt.Fprintln(w, "order_id,table,created_at,status,payment_method,payment_status,served_by,total,tip,items")
	for _, o := range orders {
		items := ""
		for i, it := range o.Items {
			if i > 0 {
				items += "; "
			}
			items += fmt.Sprintf("%dx %s", it.Quantity, sanitizeCSVField(it.Name))
		}
		fmt.Fprintf(w, "%d,%d,%s,%s,%s,%s,%s,%.2f,%.2f,%q\n",
			o.ID, o.TableNumber, o.CreatedAt.Local().Format("2006-01-02 15:04"),
			sanitizeCSVField(o.Status), sanitizeCSVField(o.PaymentMethod), sanitizeCSVField(o.PaymentStatus),
			sanitizeCSVField(o.ServedBy), o.TotalAmount, o.TipAmount, items)
	}
}
