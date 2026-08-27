package db

import "bitebox/internal/models"

const productColumns = "id, name, price, stock, is_available, category, subcategory, description"

func scanProduct(p *models.Product, scan func(...interface{}) error) error {
	return scan(&p.ID, &p.Name, &p.Price, &p.Stock, &p.IsAvailable, &p.Category, &p.Subcategory, &p.Description)
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
// never need a count, e.g. drinks poured to order). subcategory/description
// are both optional display-only fields — pass "" for either when unset.
func CreateProduct(name string, price float64, stock int, category, subcategory, description string) (int, error) {
	res, err := DB.Exec("INSERT INTO products (name, price, stock, category, subcategory, description) VALUES (?, ?, ?, ?, ?, ?)",
		name, price, stock, category, subcategory, description)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func UpdateProduct(id int, name string, price float64, stock int, category, subcategory, description string) error {
	_, err := DB.Exec("UPDATE products SET name = ?, price = ?, stock = ?, category = ?, subcategory = ?, description = ? WHERE id = ?",
		name, price, stock, category, subcategory, description, id)
	return err
}

func SetProductAvailability(id int, available bool) error {
	_, err := DB.Exec("UPDATE products SET is_available = ? WHERE id = ?", available, id)
	return err
}
