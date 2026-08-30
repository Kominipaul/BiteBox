package db

import "bitebox/internal/models"

// GetCategories returns every menu category in creation order — the order
// an admin sees them in the "add product" dropdown and the order guest/
// admin menu views group products in (see handlers.groupProductsByCategory).
// A brand-new category an admin adds always lands at the end, which is the
// simplest sane default without a dedicated ordering UI.
func GetCategories() ([]models.Category, error) {
	rows, err := DB.Query("SELECT id, name, department FROM categories ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cats []models.Category
	for rows.Next() {
		var c models.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Department); err != nil {
			return nil, err
		}
		cats = append(cats, c)
	}
	return cats, nil
}

// CategoryExists reports whether name is an existing category, exact match
// — used to validate a product's category on create/update without pulling
// the whole list just to check membership.
func CategoryExists(name string) (bool, error) {
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM categories WHERE name = ?", name).Scan(&count)
	return count > 0, err
}

// CreateCategory adds a new admin-named category. department must be
// "kitchen" or "bar" (the caller validates that — see
// handlers.AdminCreateCategory) since that's what routes a product in this
// category to the right worker order feed. name must be unique; the
// categories.name UNIQUE constraint is the actual backstop, this just
// surfaces that as a returned error instead of a raw SQLite one.
func CreateCategory(name, department string) (int, error) {
	res, err := DB.Exec("INSERT INTO categories (name, department) VALUES (?, ?)", name, department)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

// CategoryNamesForDepartment returns every category name tagged to the
// given department, in the same creation order as GetCategories — used to
// filter a bar/kitchen worker's order feed to only their own categories
// (see GetActiveOrdersByCategories). Only ever called with "kitchen" or
// "bar"; waiter/superworker/DJ feeds aren't category-filtered at all.
func CategoryNamesForDepartment(department string) ([]string, error) {
	rows, err := DB.Query("SELECT name FROM categories WHERE department = ? ORDER BY id", department)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}
