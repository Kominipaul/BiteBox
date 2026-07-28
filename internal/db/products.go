package db

import "bitebox/internal/models"

// GetProducts returns only available products, for the guest-facing menu.
func GetProducts() ([]models.Product, error) {
	rows, err := DB.Query("SELECT id, name, price, stock, is_available FROM products WHERE is_available = 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []models.Product
	for rows.Next() {
		var p models.Product
		if err := rows.Scan(&p.ID, &p.Name, &p.Price, &p.Stock, &p.IsAvailable); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, nil
}

// GetAllProducts returns every product regardless of availability, for admin management.
func GetAllProducts() ([]models.Product, error) {
	rows, err := DB.Query("SELECT id, name, price, stock, is_available FROM products ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []models.Product
	for rows.Next() {
		var p models.Product
		if err := rows.Scan(&p.ID, &p.Name, &p.Price, &p.Stock, &p.IsAvailable); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, nil
}

func GetProductByID(id int) (models.Product, error) {
	var p models.Product
	err := DB.QueryRow("SELECT id, name, price, stock, is_available FROM products WHERE id = ?", id).
		Scan(&p.ID, &p.Name, &p.Price, &p.Stock, &p.IsAvailable)
	return p, err
}

func CreateProduct(name string, price float64) (int, error) {
	res, err := DB.Exec("INSERT INTO products (name, price) VALUES (?, ?)", name, price)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func UpdateProduct(id int, name string, price float64) error {
	_, err := DB.Exec("UPDATE products SET name = ?, price = ? WHERE id = ?", name, price, id)
	return err
}

func SetProductAvailability(id int, available bool) error {
	_, err := DB.Exec("UPDATE products SET is_available = ? WHERE id = ?", available, id)
	return err
}
