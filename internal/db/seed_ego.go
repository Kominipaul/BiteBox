package db

import (
	"log"

	"bitebox/internal/models"
)

// seed_ego.go seeds BiteBox's first real client: EGO, an all-day exclusive
// bar in Kalamata. Product prices/descriptions/category groupings come
// straight off their printed Main/Cocktails/Wine/Cafe menus.
//
// Stock is deliberately NOT uniform: -1 (unlimited/untracked) is used only
// where it's actually true to how the item is served — poured to order from
// a keg, an espresso machine, a brewed pot, or the house-pour carafe wines —
// everything else carries a real, varying count reflecting how that item is
// actually stocked (a handful of prestige bottles vs. cases of soda vs.
// daily-prepped food portions). See CreateProduct's doc comment for the -1
// convention.
type seedIngredient struct {
	Name string
	Kind string
}

type seedProduct struct {
	Name        string
	Price       float64
	Category    string
	Description string
	Stock       int
	Ingredients []seedIngredient
}

// removable is a small helper for the common case: every ingredient tag on
// EGO's menu is a "leave this out" one (no add-on/extra flow is used here).
func removable(names ...string) []seedIngredient {
	out := make([]seedIngredient, len(names))
	for i, n := range names {
		out[i] = seedIngredient{Name: n, Kind: models.IngredientRemovable}
	}
	return out
}

var egoMenu = []seedProduct{
	// ---- BRUNCH ----
	{"Omelette", 13, "Brunch", "With mushrooms, peppers and chicken", 30, removable("Mushrooms", "Peppers", "Chicken")},
	{"Poached Eggs", 13, "Brunch", "Stuffed bagel with Philadelphia cheese, turkey, prosciutto chips and paprika oil", 25, removable("Philadelphia Cheese", "Turkey", "Prosciutto Chips", "Paprika Oil")},
	{"Kagianas", 13.5, "Brunch", "Traditional Greek scrambled eggs with smoked pork (syglino from Mani), feta cheese and oven-roasted tomato, served on sourdough", 20, removable("Smoked Pork", "Feta Cheese", "Oven-Roasted Tomato")},
	{"Egg White Scramble", 13.5, "Brunch", "On black bread with avocado cream and smoked salmon", 20, removable("Avocado Cream", "Smoked Salmon")},
	{"Vegan Mushroom & Hummus", 13, "Brunch", "Mushrooms and hummus with caramelized red cabbage on sourdough, roasted cashews (V)", 18, removable("Mushrooms", "Hummus", "Red Cabbage", "Cashews")},
	{"Club Sandwich", 14, "Brunch", "Chicken fillet, tomato, bacon, mayonnaise, Edam cheese, lettuce and French fries", 35, removable("Chicken Fillet", "Tomato", "Bacon", "Mayonnaise", "Edam Cheese", "Lettuce")},
	{"Sando Chicken", 13, "Brunch", "Brioche bread with chicken fillet, cheddar, tomato, crispy onions, BBQ mayonnaise, bacon and lettuce", 30, removable("Chicken Fillet", "Cheddar", "Tomato", "Crispy Onions", "BBQ Mayonnaise", "Bacon", "Lettuce")},

	// ---- SWEET BREAKFAST ----
	{"Pancakes", 11, "Sweet Breakfast", "With hazelnut praline, Oreo cookie and fresh fruits", 25, removable("Hazelnut Praline", "Oreo Cookie", "Fresh Fruits")},
	{"Strained Yogurt", 9.5, "Sweet Breakfast", "With fresh seasonal fruits", 40, removable("Seasonal Fruits")},
	{"Yogurt with Açaí", 11, "Sweet Breakfast", "With pasteli, chocolate drops, cranberries, goji berry, linseed and honey", 25, removable("Chocolate Drops", "Cranberries", "Goji Berry", "Linseed", "Honey")},
	{"Waffle", 11.5, "Sweet Breakfast", "With hazelnut praline, fresh fruits, Oreo cookie, chocolate syrup and vanilla ice cream", 22, removable("Hazelnut Praline", "Fresh Fruits", "Oreo Cookie", "Chocolate Syrup", "Vanilla Ice Cream")},
	{"Orange Cake", 10, "Sweet Breakfast", "With vanilla ice cream", 18, removable("Vanilla Ice Cream")},

	// ---- STARTERS ----
	{"Baked Goat Cheese", 18, "Starters", "Goat cheese log, yellow pumpkin jam with Chios mastic, prosciutto chips and rocket pesto with Aegina pistachios", 15, removable("Prosciutto Chips", "Pistachios")},
	{"Octopus with Santorini Fava", 21, "Starters", "Grilled octopus, fava cream, cuttlefish ink, pepper chutney and fresh herb sauce", 12, removable("Pepper Chutney", "Herb Sauce")},
	{"Greek Ceviche", 17, "Starters", "Marinated anchovy fillets with sea salt flakes and apple vinegar, vegetable brunoise, beetroot aioli, sea fennel and white tarama cream", 14, removable("Beetroot Aioli", "Sea Fennel", "Tarama Cream")},
	{"Carpaccio Black Angus", 18, "Starters", "Thinly sliced Black Angus beef with parmesan sauce, caramelized pearl onions, pickled cucumber, croutons, nduja and roasted cherry tomatoes", 12, removable("Pearl Onions", "Pickled Cucumber", "Croutons", "Nduja", "Cherry Tomatoes")},

	// ---- SALADS ----
	{"Greek Salad", 13.5, "Salads", "Tomato, cucumber, green pepper, pickled onion, feta cream, carob rusk, olives, capers and extra virgin olive oil", 40, removable("Pickled Onion", "Feta Cream", "Carob Rusk", "Olives", "Capers")},
	{"Chicken Greens Salad", 14, "Salads", "Tender greens with chicken fillet, kefalograviera, cherry tomatoes and tortilla croutons", 30, removable("Chicken Fillet", "Kefalograviera", "Cherry Tomatoes", "Croutons")},
	{"Quinoa with Cranberries & Cashews", 13.5, "Salads", "Tricolor quinoa marinated with soy and sweet chilli, pear, mint, cranberries, toasted cashews and grilled manouri cheese", 22, removable("Cranberries", "Cashews", "Manouri Cheese")},
	{"Caesar's", 14, "Salads", "Little gem and iceberg lettuce with parmesan dressing, bacon, chicken fillet and croutons", 28, removable("Bacon", "Chicken Fillet", "Croutons")},

	// ---- PASTA ----
	{"Capelletti di Tartufo", 15, "Pasta", "Fresh stuffed pasta with ricotta, buttery Pecorino Romano sauce and fresh truffle", 16, removable("Fresh Truffle")},
	{"Rigatoni Pesto", 15.5, "Pasta", "Rigatoni with pesto sauce, roasted cherry tomatoes, poppy seeds, parmesan flakes and feta", 25, removable("Cherry Tomatoes", "Parmesan Flakes", "Feta")},
	{"Pappardelle Ragù", 19, "Pasta", "Pappardelle with beef ragù cooked with Marsala wine, fresh tomato and Pecorino Romano", 20, removable("Pecorino Romano")},
	{"Linguine with Shrimp", 20, "Pasta", "Linguine with shrimp in shellfish sauce, flavored with Greek brandy and star anise, finished with butter and parmesan", 14, removable("Shrimp", "Parmesan")},
	{"Rigatoni with Chicken", 15.5, "Pasta", "Rigatoni with chicken fillet, mushrooms, peppers, parmesan sauce with truffle oil, fresh herbs and kefalograviera", 25, removable("Mushrooms", "Peppers", "Kefalograviera")},

	// ---- MAINS ----
	{"Chicken with Quinoa", 17, "Main Courses", "Chicken fillet with tricolor quinoa, pear, roasted broccoli, pea cream, aromatic yogurt and lemon-oil dressing", 20, removable("Broccoli", "Pea Cream", "Yogurt")},
	{"Sea Bass with Greens", 22, "Main Courses", "Sea bass fillet with wild greens and lemon sauce", 12, removable("Wild Greens")},
	{"Lamb Shank with Trahanas", 25, "Main Courses", "Slow-cooked lamb shank with mavrodaphne wine, sour Mani trahanas, fresh tomato and kefalograviera", 10, removable("Kefalograviera")},
	{"Pork Shoulder Steak 400g", 19, "Main Courses", "Served with espresso-flavored BBQ sauce and baby oven potatoes", 18, removable("BBQ Sauce")},
	{"Ribeye Black Angus 330g", 45, "Main Courses", "Served with espresso-flavored BBQ sauce and baby oven potatoes", 8, removable("BBQ Sauce")},

	// ---- TO SHARE ----
	{"Meat Platter (4 people)", 58, "To Share", "Chicken fillet, pork mini steaks, chicken and pork patties, sausage with leek and orange, french fries, pitas, BBQ sauce and smoked mayonnaise", 8, removable("BBQ Sauce", "Smoked Mayonnaise")},
	{"Cold Cuts & Cheese Platter (4 people)", 32, "To Share", "A selection of cured meats and cheeses", 10, nil},
	{"Fresh Seasonal Fruit Platter (4 people)", 22, "To Share", "Chef's selection of fresh seasonal fruit", 10, nil},

	// ---- DESSERTS ----
	{"Tiramisu", 12, "Desserts", "Cheese cream flavored with espresso, cinnamon cookies, homemade Kahlúa, citrus aromas and chocolate", 20, removable("Cinnamon Cookies", "Kahlúa")},
	{"Galaktoboureko", 11.5, "Desserts", "Semolina custard cream, crispy filo pastry, fresh blueberries and strawberries", 18, removable("Blueberries", "Strawberries")},
	{"New York Cheesecake", 10, "Desserts", "With tropical fruit sauce", 20, removable("Tropical Fruit Sauce")},
	{"Chocolate Soufflé", 10, "Desserts", "Served with vanilla ice cream", 15, removable("Vanilla Ice Cream")},

	// ---- COFFEE (machine/pot, made to order) ----
	{"Espresso", 3, "Coffee", "", -1, nil},
	{"Double Espresso", 3.6, "Coffee", "", -1, nil},
	{"Cappuccino", 4.8, "Coffee", "", -1, nil},
	{"Double Cappuccino", 5.3, "Coffee", "", -1, nil},
	{"Freddo Espresso", 4.8, "Coffee", "", -1, nil},
	{"Freddo Cappuccino", 5, "Coffee", "", -1, nil},
	{"Latte (Hot / Cold)", 5, "Coffee", "", -1, nil},
	{"Nescafé (Hot / Cold)", 4.8, "Coffee", "", -1, nil},
	{"Greek Coffee Single", 3, "Coffee", "", -1, nil},
	{"Greek Coffee Double", 3.5, "Coffee", "", -1, nil},

	// ---- FRESH JUICES (fresh-squeezed, limited by fruit on hand) ----
	{"Orange Juice (300ml)", 5, "Fresh Juices", "", 45, nil},
	{"Mixed Juice (400ml)", 5.5, "Fresh Juices", "", 35, nil},

	// ---- COLD BEVERAGES ----
	{"Hot Chocolate (400ml)", 5.5, "Cold Beverages", "", -1, nil},
	{"Strawberry Granita (400ml)", 5.5, "Cold Beverages", "", 30, nil},
	{"Mixed Berry Granita (400ml)", 5.5, "Cold Beverages", "", 30, nil},

	// ---- MILKSHAKES (ice-cream based, limited) ----
	{"Milkshake Chocolate (400ml)", 6, "Milkshakes", "", 35, nil},
	{"Milkshake Vanilla (400ml)", 6, "Milkshakes", "", 35, nil},
	{"Milkshake Mixed (400ml)", 6, "Milkshakes", "", 30, nil},
	{"Milkshake Red Velvet (400ml)", 6.5, "Milkshakes", "", 25, nil},
	{"Milkshake Bueno (400ml)", 6.5, "Milkshakes", "", 25, nil},

	// ---- SMOOTHIES (fresh fruit, limited) ----
	{"Wild Forest (400ml)", 5.5, "Smoothies", "Strawberry, blueberry, blackcurrant, cranberry, red grape & black cherry", 25, nil},
	{"Pink Paradise (400ml)", 5.5, "Smoothies", "Strawberry, raspberry, mango", 25, nil},
	{"Yellow Sunrise (400ml)", 5.5, "Smoothies", "Mango, peach, pineapple", 25, nil},

	// ---- TEA (brewed to order) ----
	{"Half & Half Iced Green Tea + Watermelon (500ml)", 5, "Tea", "", -1, nil},
	{"Iced Green Tea Lemon Ginger (500ml)", 5, "Tea", "", -1, nil},
	{"Iced Tea Blueberry Ginger (500ml)", 5, "Tea", "", -1, nil},
	{"Herbal Tea Rose (500ml)", 5, "Tea", "", -1, nil},
	{"Earl Grey Iced Tea Lemon (500ml)", 5, "Tea", "", -1, nil},

	// ---- HOMEMADE SOFT DRINKS (house-made batches) ----
	{"Lemonade (500ml)", 5, "Homemade Soft Drinks", "", 40, nil},
	{"Cucumber & Mint (500ml)", 5, "Homemade Soft Drinks", "", 35, nil},
	{"Peach & Ginger (500ml)", 5, "Homemade Soft Drinks", "", 30, nil},
	{"Mastiha & Yuzu (500ml)", 5, "Homemade Soft Drinks", "", 25, nil},
	{"Cranberry & Pomegranate (500ml)", 5, "Homemade Soft Drinks", "", 30, nil},

	// ---- ENERGY DRINKS (canned, case stock) ----
	{"Red Bull Original (250ml)", 6, "Energy Drinks", "", 60, nil},
	{"Red Bull Yellow Edition Tropical (250ml)", 6, "Energy Drinks", "", 45, nil},
	{"Red Bull Apricot Edition (250ml)", 6, "Energy Drinks", "", 40, nil},

	// ---- BEERS ----
	{"Draft Beer Mamos (400ml)", 5, "Beers", "Greek Pilsner", -1, nil}, // keg, poured to order
	{"SOL", 7, "Beers", "Exotic Lager", 60, nil},
	{"Heineken", 7, "Beers", "Premium Lager", 90, nil},
	{"Alfa Beer Lager", 4.5, "Beers", "", 100, nil},
	{"Nymfi", 7, "Beers", "Hoppe Lager", 40, nil},
	{"Sura", 7, "Beers", "Messinian Lager", 40, nil},
	{"Kalamata Lager", 7, "Beers", "Messinian Lager", 50, nil},
	{"Fischer", 7, "Beers", "Premium Pilsener", 45, nil},
	{"Erdinger", 7, "Beers", "Weissbier", 35, nil},
	{"Heineken 0.0", 7, "Beers", "Alcohol Free", 50, nil},
	{"Amstel Radler Pink", 7, "Beers", "Low Alcohol 0.9%", 40, nil},
	{"Alfa", 5.5, "Beers", "", 80, nil},
	{"Kalamata Brewery Beer", 7, "Beers", "Messinian Lager", 50, nil},

	// ---- CIDER ----
	{"Mikloftis", 6, "Cider", "Greek Cider", 40, nil},
	{"Strongbow", 6, "Cider", "Gold & Red Berries", 45, nil},

	// ---- SPIRITS (poured from bottle) ----
	{"Ouzo Kotsifali (50ml)", 4.5, "Spirits — Ouzo & Tsipouro", "", 80, nil},
	{"Ouzo Kotsifali (200ml)", 9.5, "Spirits — Ouzo & Tsipouro", "", 20, nil},
	{"Ouzo Stafylis (200ml)", 9.5, "Spirits — Ouzo & Tsipouro", "", 18, nil},
	{"Ouzo Kalalikonis (200ml)", 9.5, "Spirits — Ouzo & Tsipouro", "", 15, nil},
	{"Tsipouro Kotsifali (50ml)", 4.5, "Spirits — Ouzo & Tsipouro", "", 80, nil},
	{"Tsipouro Kotsifali (200ml)", 9.5, "Spirits — Ouzo & Tsipouro", "", 20, nil},
	{"Tsipouro Stafylis (200ml)", 9.5, "Spirits — Ouzo & Tsipouro", "", 18, nil},
	{"Tsipouro Kalalikonis (200ml)", 9.5, "Spirits — Ouzo & Tsipouro", "", 15, nil},
	{"Tsipouro Patapios (50ml)", 5, "Spirits — Ouzo & Tsipouro", "", 60, nil},
	{"Tsipouro Patapios (200ml)", 10, "Spirits — Ouzo & Tsipouro", "", 15, nil},
	{"Tsipouro Primarolia (200ml)", 9.5, "Spirits — Ouzo & Tsipouro", "", 12, nil},

	// ---- WATER ----
	{"Bottled Water (500ml)", 0.5, "Water", "", 150, nil},
	{"Bottled Water (1L)", 1.5, "Water", "", 100, nil},
	{"Sparkling Water Zaros (250ml)", 3.8, "Water", "", 70, nil},
	{"Sparkling Water Zaros (750ml)", 5.3, "Water", "", 40, nil},
	{"Souroti (250ml)", 3.8, "Water", "", 70, nil},
	{"San Pellegrino (700ml)", 5.3, "Water", "", 35, nil},

	// ---- SOFT DRINKS (case-stocked bottles) ----
	{"Coca-Cola (330ml)", 3.8, "Soft Drinks", "", 90, nil},
	{"Coca-Cola Zero (330ml)", 3.8, "Soft Drinks", "", 80, nil},
	{"Coca-Cola Light (330ml)", 3.8, "Soft Drinks", "", 60, nil},
	{"Orangeade (330ml)", 3.8, "Soft Drinks", "", 60, nil},
	{"Lemonade Soda (330ml)", 3.8, "Soft Drinks", "", 60, nil},
	{"Orange Soda (330ml)", 3.8, "Soft Drinks", "", 55, nil},
	{"Pink Grapefruit Soda (330ml)", 3.8, "Soft Drinks", "", 50, nil},
	{"Lemon Soda (330ml)", 3.8, "Soft Drinks", "", 55, nil},
	{"Blood Orange Soda (330ml)", 3.8, "Soft Drinks", "", 45, nil},
	{"Cranberry Soda (330ml)", 3.8, "Soft Drinks", "", 45, nil},

	// ---- SIGNATURE COCKTAILS (bar-made, limited by fresh garnish prep) ----
	{"Jungle Trouble", 13, "Signature Cocktail List", "Rum infusion banana & coffee, passion fruit, falernum, vanilla, lime, allspice liqueur", 30, nil},
	{"Fig Negroni", 13, "Signature Cocktail List", "Gin, bitter liqueur, sweet vermouth, fig leaf, fresh thyme", 35, nil},
	{"Basil Garden", 13, "Signature Cocktail List", "Gin, mastiha liqueur, lemon, basil cordial, orgeat, herbal liqueur", 30, nil},
	{"Bees of Japan", 14, "Signature Cocktail List", "Japanese gin, yuzu, orange, honey", 25, nil},
	{"Crystal Tale (clarified)", 14, "Signature Cocktail List", "Rum, lime, vanilla, tonka, peach", 15, nil}, // batch-clarified, labor-intensive
	{"Mango Diablo", 13, "Signature Cocktail List", "Tequila blanco, lime, agave syrup, mango soda, tajin rim", 30, nil},
	{"Canella Rosa", 12, "Signature Cocktail List", "Vodka infusion vanilla, lemon, cordial rose and cinnamon, strawberry", 25, nil},
	{"Milky Way", 12, "Signature Cocktail List", "Vodka infusion cacao, mango, coconut, vanilla, lime", 25, nil},
	{"Kon-Tiki", 13, "Signature Cocktail List", "Rum pineapple, bourbon whiskey, lime, falernum, pear liqueur, tiki bitter", 20, nil},
	{"Tropical Zombie", 15, "Signature Cocktail List", "Rum, rum, rum, overproof rum 69%, lime, lemon, pineapple, passion fruit, creole bitter", 18, nil},

	// ---- VIRGIN LIST ----
	{"Virgin Paloma", 9, "Virgin List", "Blue agave spirit blanco 0% Alc, agave syrup, lime, grapefruit soda", 30, nil},
	{"Zero Spritz", 9, "Virgin List", "Aperitivo alcohol free, grapefruit soda, flowers water", 30, nil},
	{"Detox Green", 9, "Virgin List", "Gin 0% Alc, cucumber, ginger, lemon, mint, sparkling water", 30, nil},

	// ---- SPRITZ & APERITIVO ----
	{"Aztec Americano", 9, "Spritz & Aperitivo", "Bitter liqueur cacao, sweet vermouth, mandarin & bergamot soda", 35, nil},
	{"Napoli Spritz", 9, "Spritz & Aperitivo", "Italian flower liqueur, bianco vermouth, lemonade soda", 35, nil},
	{"Local Spritz", 9, "Spritz & Aperitivo", "Mataroa happy, mandarin & bergamot soda, chamomile tea", 35, nil},
	{"Cocospritz", 9, "Spritz & Aperitivo", "Coconut liqueur, tropical soda, tiki bitter", 30, nil},
	{"Our Spritz", 9, "Spritz & Aperitivo", "Aperitivo liqueur, sweet vermouth, grapefruit soda, flower spray", 35, nil},

	// ---- WHITE WINE ----
	{"FARE Moschofilero", 6, "White Wine", "Roditis, Tripoli", -1, nil}, // house carafe pour
	{"Mati Fortuna Moschofilero", 28, "White Wine", "Chardonnay, Astir X Winery", 18, nil},
	{"Astakos Mosxato", 33, "White Wine", "Chardonnay, Astir X Winery", 15, nil},
	{"Mantinia", 36, "White Wine", "Moschofilero, Tselepos Estate", 14, nil},
	{"Assyrtiko Malagouzia", 30, "White Wine", "Charoulis Estate", 16, nil},
	{"Malagouzia Papagiannakos", 39, "White Wine", "Papagiannakos Estate", 12, nil},
	{"Émigré", 42, "White Wine", "Assyrtiko, Vidiano, Mavrotragano — Mitravelas Estate", 10, nil},
	{"Alpha Estate Chardonnay", 58, "White Wine", "Alpha Estate", 6, nil},
	{"Oracle 2024", 42, "White Wine", "Assyrtiko, Greece / Mantinia — Troupis Winery", 10, nil},
	{"Chardonnay Lefkes Grapes", 34, "White Wine", "Charoulis Estate", 14, nil},

	// ---- ROSÉ WINE ----
	{"FARE Rosé Agiorgitiko", 6, "Rosé Wine", "Grenache Rouge, Tripoli", -1, nil}, // house carafe pour
	{"Tris Mageses", 35, "Rosé Wine", "Agiorgitiko, Syrah, Moschofilero — Barafakas Winery", 12, nil},
	{"Rosé de Xinomavro", 45, "Rosé Wine", "Single-varietal Xinomavro — Thymiopoulos Estate", 8, nil},
	{"Idylle d'Achinos", 53, "Rosé Wine", "Rosé, La Tour Melas", 6, nil},
	{"Miraval", 80, "Rosé Wine", "Cinsault | Grenache Rouge | Rolle | Syrah — Château Miraval", 4, nil},
	{"Nova Rosé", 32, "Rosé Wine", "Rosé Blend | Astir X Winery", 15, nil},
	{"Cube Rosé", 32, "Rosé Wine", "Rosé Blend | Papagiannakos Estate", 15, nil},
	{"Syrah Rosé", 38, "Rosé Wine", "Rosé Blend | Charoulis Estate", 10, nil},
	{"Mati Fortuna Agiorgitiko", 26, "Rosé Wine", "Merlot, Astir X Winery", 18, nil},

	// ---- RED WINE ----
	{"FARE Agiorgitiko", 7, "Red Wine", "Merlot, Tripoli", -1, nil}, // house carafe pour
	{"Naoussa Xinomavro", 30, "Red Wine", "Kir-Yianni Estate", 15, nil},
	{"Nemea Agiorgitiko", 33, "Red Wine", "Palivou Estate", 14, nil},
	{"Syrah", 42, "Red Wine", "Avantis Estate", 10, nil},
	{"Astakos Erythros Xerros", 38, "Red Wine", "Merlot & Tempranillo, Peloponnesian — Astir X", 10, nil},
	{"Pinot Noir", 48, "Red Wine", "Papagiannakos Estate", 8, nil},
	{"1827 Cabernet Sauvignon", 52, "Red Wine", "Navarino Vineyards", 6, nil},

	// ---- SWEET WINE ----
	{"Mavrodaphne", 7, "Sweet Wine", "Achaia Clauss", 30, nil},
	{"Melissea", 8, "Sweet Wine", "Sweet Wine, Achaia Clauss", 25, nil},
	{"Sangria", 7, "Sweet Wine", "", 25, nil}, // jug-made

	// ---- SPARKLING WINE ----
	{"Kisses", 34, "Sparkling Wine", "Charoulis Estate", 12, nil},
	{"Paraga Sparkling", 35, "Sparkling Wine", "Xinomavro | Chardonnay | Roditis — Kir-Yianni Estate", 10, nil},
	{"Santero Moscato d'Asti (glass)", 7, "Sparkling Wine", "", 30, nil},
	{"Santero Moscato d'Asti (bottle)", 26, "Sparkling Wine", "", 10, nil},
	{"Serenello Prosecco (glass)", 7, "Sparkling Wine", "", 30, nil},
	{"Serenello Prosecco (bottle)", 26, "Sparkling Wine", "", 10, nil},
	{"Moët & Chandon Ice", 190, "Sparkling Wine", "", 6, nil},
	{"Moët & Chandon Rosé", 190, "Sparkling Wine", "", 6, nil},
	{"Dom Pérignon", 490, "Sparkling Wine", "", 3, nil},
	{"Dom Pérignon Luminous", 780, "Sparkling Wine", "", 2, nil},
}

// seedEGOMenu inserts EGO's full menu plus removable-ingredient tags for the
// items that actually have customizable components (mostly food — drinks
// are poured/mixed as listed, nothing to leave out). Only called when the
// products table is empty (see InitDB); errors are logged, not fatal, so one
// bad row doesn't stop the rest of a 140+ item seed from loading. Assumes
// seedDefaultCategories has already run — every Category value here is one
// of that list's names.
func seedEGOMenu() {
	for _, p := range egoMenu {
		id, err := CreateProduct(p.Name, p.Price, p.Stock, p.Category, p.Description)
		if err != nil {
			log.Printf("seed: failed to create product %q: %v", p.Name, err)
			continue
		}
		for _, ing := range p.Ingredients {
			// 0: every seeded tag is "removable" (see the removable() helper
			// above) — EGO's menu doesn't seed any priced "extra" add-ons yet,
			// those get added live from the admin dashboard.
			if _, err := CreateIngredient(id, ing.Name, ing.Kind, 0); err != nil {
				log.Printf("seed: failed to tag ingredient %q on %q: %v", ing.Name, p.Name, err)
			}
		}
	}
}

// seedDefaultCategories seeds the initial flat category list, in the same
// order EGO's own printed menu presents them, each tagged with the worker
// department that prepares it. Only called when the categories table is
// empty (see InitDB) — an admin renaming/adding to this list afterward is
// the whole point of categories being real rows instead of a fixed enum.
func seedDefaultCategories() {
	cats := []struct{ name, department string }{
		{"Brunch", models.DepartmentKitchen},
		{"Sweet Breakfast", models.DepartmentKitchen},
		{"Starters", models.DepartmentKitchen},
		{"Salads", models.DepartmentKitchen},
		{"Pasta", models.DepartmentKitchen},
		{"Main Courses", models.DepartmentKitchen},
		{"To Share", models.DepartmentKitchen},
		{"Desserts", models.DepartmentKitchen},
		{"Coffee", models.DepartmentBar},
		{"Fresh Juices", models.DepartmentBar},
		{"Cold Beverages", models.DepartmentBar},
		{"Milkshakes", models.DepartmentBar},
		{"Smoothies", models.DepartmentBar},
		{"Tea", models.DepartmentBar},
		{"Homemade Soft Drinks", models.DepartmentBar},
		{"Energy Drinks", models.DepartmentBar},
		{"Beers", models.DepartmentBar},
		{"Cider", models.DepartmentBar},
		{"Spirits — Ouzo & Tsipouro", models.DepartmentBar},
		{"Water", models.DepartmentBar},
		{"Soft Drinks", models.DepartmentBar},
		{"Signature Cocktail List", models.DepartmentBar},
		{"Virgin List", models.DepartmentBar},
		{"Spritz & Aperitivo", models.DepartmentBar},
		{"White Wine", models.DepartmentBar},
		{"Rosé Wine", models.DepartmentBar},
		{"Red Wine", models.DepartmentBar},
		{"Sweet Wine", models.DepartmentBar},
		{"Sparkling Wine", models.DepartmentBar},
		// Fallback bucket for anything with a blank/invalid category (see
		// models.CategoryOther) — arbitrarily tagged bar since it has to be
		// one or the other; not expected to ever hold real products.
		{models.CategoryOther, models.DepartmentBar},
	}
	for _, c := range cats {
		if _, err := CreateCategory(c.name, c.department); err != nil {
			log.Printf("seed: failed to create category %q: %v", c.name, err)
		}
	}
}
