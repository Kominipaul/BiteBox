package cart

import (
	"sort"
	"sync"

	"bitebox/internal/models"
)

// Store is an in-memory, session-keyed cart store. Carts are ephemeral
// pre-checkout state — checkout drains a cart into real orders/order_items
// rows and clears it. Not persisted, since restarting the venue server mid-
// order is an acceptable loss for a not-yet-placed cart.
type Store struct {
	mu    sync.RWMutex
	carts map[string]*models.Cart
}

func NewStore() *Store {
	return &Store{carts: make(map[string]*models.Cart)}
}

// Get returns a snapshot of the session's cart (an empty Cart if none exists yet).
func (s *Store) Get(sessionID string) models.Cart {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.carts[sessionID]
	if !ok {
		return models.Cart{}
	}
	return *c
}

// sameSet compares two string sets order-independently — used for both
// excluded and extra ingredients. The same product with a different
// customization (either set differs) is a different cart line (e.g.
// "Burger, no onion" and "Burger, no pickles", or "Burger" and "Burger,
// +extra cheese", don't merge).
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa, sb := append([]string{}, a...), append([]string{}, b...)
	sort.Strings(sa)
	sort.Strings(sb)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}

func sameCustomization(item models.OrderItem, excluded, extras []string) bool {
	return sameSet(item.Excluded, excluded) && sameSet(item.Extras, extras)
}

// Add increments the quantity for the (productID, excluded, extras) line,
// creating it from the given name/price snapshot if that exact
// customization isn't already in the cart. Returns the updated cart snapshot.
func (s *Store) Add(sessionID string, productID int, name string, price float64, excluded, extras []string) models.Cart {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.getOrCreate(sessionID)
	for i := range c.Items {
		if c.Items[i].ProductID == productID && sameCustomization(c.Items[i], excluded, extras) {
			c.Items[i].Quantity++
			recalcTotal(c)
			return *c
		}
	}
	c.Items = append(c.Items, models.OrderItem{
		ProductID: productID,
		Name:      name,
		Price:     price,
		Quantity:  1,
		Excluded:  append([]string{}, excluded...),
		Extras:    append([]string{}, extras...),
	})
	recalcTotal(c)
	return *c
}

// Remove decrements the quantity for the exact (productID, excluded, extras)
// line by 1, dropping it once it reaches zero. Returns the updated cart snapshot.
func (s *Store) Remove(sessionID string, productID int, excluded, extras []string) models.Cart {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.getOrCreate(sessionID)
	for i := range c.Items {
		if c.Items[i].ProductID == productID && sameCustomization(c.Items[i], excluded, extras) {
			c.Items[i].Quantity--
			if c.Items[i].Quantity <= 0 {
				c.Items = append(c.Items[:i], c.Items[i+1:]...)
			}
			break
		}
	}
	recalcTotal(c)
	return *c
}

// Clear empties the session's cart, e.g. after checkout or leaving the table.
func (s *Store) Clear(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.carts, sessionID)
}

// ReservedQuantity sums how many units of a product currently sit in any
// active cart, across every session and every customization variant — not
// yet checked out (and so not yet decremented from products.stock), but no
// longer available for someone else to add, either. Used to compute a live
// "available" count that reflects what's sitting in other guests' carts,
// not just the DB column.
func (s *Store) ReservedQuantity(productID int) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := 0
	for _, c := range s.carts {
		for _, item := range c.Items {
			if item.ProductID == productID {
				total += item.Quantity
			}
		}
	}
	return total
}

func (s *Store) getOrCreate(sessionID string) *models.Cart {
	c, ok := s.carts[sessionID]
	if !ok {
		c = &models.Cart{}
		s.carts[sessionID] = c
	}
	return c
}

// recalcTotal always recomputes TotalAmount from Items — a client never
// supplies a trusted total.
func recalcTotal(c *models.Cart) {
	total := 0.0
	for _, item := range c.Items {
		total += item.Price * float64(item.Quantity)
	}
	c.TotalAmount = total
}
