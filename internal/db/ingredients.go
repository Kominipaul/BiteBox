package db

import "bitebox/internal/models"

// GetIngredientsByProductIDs batch-fetches every ingredient tag for a set
// of products in one query, grouped by product id — used by both the guest
// menu and the admin product list so rendering N products never costs N+1
// queries.
func GetIngredientsByProductIDs(ids []int) (map[int][]models.Ingredient, error) {
	result := make(map[int][]models.Ingredient)
	if len(ids) == 0 {
		return result, nil
	}

	placeholders := make([]byte, 0, len(ids)*3)
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args[i] = id
	}

	rows, err := DB.Query(
		"SELECT id, product_id, name, kind FROM product_ingredients WHERE product_id IN ("+string(placeholders)+") ORDER BY id",
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var ing models.Ingredient
		if err := rows.Scan(&ing.ID, &ing.ProductID, &ing.Name, &ing.Kind); err != nil {
			return nil, err
		}
		result[ing.ProductID] = append(result[ing.ProductID], ing)
	}
	return result, nil
}

func CreateIngredient(productID int, name, kind string) (int, error) {
	res, err := DB.Exec("INSERT INTO product_ingredients (product_id, name, kind) VALUES (?, ?, ?)", productID, name, kind)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func DeleteIngredient(id int) error {
	_, err := DB.Exec("DELETE FROM product_ingredients WHERE id = ?", id)
	return err
}
