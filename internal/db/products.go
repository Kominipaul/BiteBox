package db

import (
	"strings"

	"bitebox/internal/models"
)

// subcategory isn't selected here — it's a retired column (see
// models.Product's Category doc comment), left in the table unread.
const productColumns = "id, name, price, stock, is_available, category, description, image_url, vat_rate, COALESCE(recommended, 0), COALESCE(avail_start, -1), COALESCE(avail_end, -1)"

func scanProduct(p *models.Product, scan func(...interface{}) error) error {
	return scan(&p.ID, &p.Name, &p.Price, &p.Stock, &p.IsAvailable, &p.Category, &p.Description, &p.ImageURL, &p.VATRate, &p.Recommended, &p.AvailStart, &p.AvailEnd)
}

// GetProducts returns only available products, for the guest-facing menu.
func GetProducts() ([]models.Product, error) {
	rows, err := DB.Query("SELECT " + productColumns + " FROM products WHERE is_available = 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []models.Product
	for rows.Next() {
		var p models.Product
		if err := scanProduct(&p, rows.Scan); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, nil
}

// GetAllProducts returns every product regardless of availability, for admin management.
func GetAllProducts() ([]models.Product, error) {
	rows, err := DB.Query("SELECT " + productColumns + " FROM products ORDER BY category, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []models.Product
	for rows.Next() {
		var p models.Product
		if err := scanProduct(&p, rows.Scan); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, nil
}

func GetProductByID(id int) (models.Product, error) {
	var p models.Product
	row := DB.QueryRow("SELECT "+productColumns+" FROM products WHERE id = ?", id)
	err := scanProduct(&p, row.Scan)
	return p, err
}

// CreateProduct inserts a product with a stock count, where -1 means
// unlimited/untracked stock (the column's default, kept for products that
// never need a count, e.g. drinks poured to order). description/imageURL
// are optional display-only fields — pass "" when unset. category should be
// an existing categories.name (see CreateCategory) — the caller validates
// that, not this function. vatRate is a percentage (24/13/6 — see
// models.Product.VATRate); demo-receipt structure only, not validated
// against real Greek VAT category rules for specific goods.
func CreateProduct(name string, price float64, stock int, category, description, imageURL string, vatRate float64) (int, error) {
	res, err := DB.Exec("INSERT INTO products (name, price, stock, category, description, image_url, vat_rate) VALUES (?, ?, ?, ?, ?, ?, ?)",
		name, price, stock, category, description, imageURL, vatRate)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func UpdateProduct(id int, name string, price float64, stock int, category, description, imageURL string, vatRate float64) error {
	_, err := DB.Exec("UPDATE products SET name = ?, price = ?, stock = ?, category = ?, description = ?, image_url = ?, vat_rate = ? WHERE id = ?",
		name, price, stock, category, description, imageURL, vatRate, id)
	return err
}

func SetProductAvailability(id int, available bool) error {
	_, err := DB.Exec("UPDATE products SET is_available = ? WHERE id = ?", available, id)
	return err
}

// DeleteProduct permanently removes a product and its ingredient tags.
// Deliberately does *not* touch order_items: those rows snapshot name/price
// at order time and carry no foreign key back to products (see
// productColumns' own doc comment on the retired subcategory column for the
// same "this table doesn't lean on products for historical data" idea) —
// deleting a discontinued item never rewrites or breaks past order history.
// The caller (AdminDeleteProduct) is responsible for cleaning up any
// uploaded photo files first; this only ever touches rows, never disk.
func DeleteProduct(id int) error {
	if _, err := DB.Exec("DELETE FROM product_ingredients WHERE product_id = ?", id); err != nil {
		return err
	}
	_, err := DB.Exec("DELETE FROM products WHERE id = ?", id)
	return err
}

// ProductCategories maps product id → category, so the staff app can tell
// a kitchen item from a bar item on a mixed order without loading whole
// products.
func ProductCategories() map[int]string {
	out := map[int]string{}
	rows, err := DB.Query("SELECT id, category FROM products")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var c string
		if rows.Scan(&id, &c) == nil {
			out[id] = c
		}
	}
	return out
}

// ProductsInCategories lists products in the given categories (all of them
// when cats is nil), by category then name, for the sold-out board.
func ProductsInCategories(cats []string) ([]models.Product, error) {
	q := "SELECT id, name, price, stock, is_available, category FROM products"
	var args []interface{}
	if cats != nil {
		if len(cats) == 0 {
			return nil, nil
		}
		q += " WHERE category IN (" + strings.TrimSuffix(strings.Repeat("?,", len(cats)), ",") + ")"
		for _, c := range cats {
			args = append(args, c)
		}
	}
	rows, err := DB.Query(q+" ORDER BY category, name", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Product
	for rows.Next() {
		var p models.Product
		if err := rows.Scan(&p.ID, &p.Name, &p.Price, &p.Stock, &p.IsAvailable, &p.Category); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
