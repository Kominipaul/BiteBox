package cart

import (
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

// Add increments the quantity for productID, creating the line item from the
// given name/price snapshot if it isn't already in the cart. Returns the
// updated cart snapshot.
func (s *Store) Add(sessionID string, productID int, name string, price float64) models.Cart {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.getOrCreate(sessionID)
	for i := range c.Items {
		if c.Items[i].ProductID == productID {
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
	})
	recalcTotal(c)
	return *c
}

// Remove decrements the quantity for productID by 1, dropping the line item
// once it reaches zero. Returns the updated cart snapshot.
func (s *Store) Remove(sessionID string, productID int) models.Cart {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.getOrCreate(sessionID)
	for i := range c.Items {
		if c.Items[i].ProductID == productID {
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
