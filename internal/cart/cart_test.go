package cart

import (
	"sync"
	"testing"
)

func TestAddCreatesLineThenIncrementsOnRepeatAdd(t *testing.T) {
	s := NewStore()
	c := s.Add("session-1", 1, "Burger", 9.5, nil, nil)
	if len(c.Items) != 1 || c.Items[0].Quantity != 1 {
		t.Fatalf("expected one line with quantity 1, got %+v", c.Items)
	}

	c = s.Add("session-1", 1, "Burger", 9.5, nil, nil)
	if len(c.Items) != 1 || c.Items[0].Quantity != 2 {
		t.Fatalf("expected the same line bumped to quantity 2, got %+v", c.Items)
	}
	if c.TotalAmount != 19.0 {
		t.Errorf("TotalAmount = %v, want 19.0", c.TotalAmount)
	}
}

func TestDifferentCustomizationsAreSeparateLines(t *testing.T) {
	s := NewStore()
	s.Add("session-1", 1, "Burger", 9.5, nil, nil)
	s.Add("session-1", 1, "Burger", 9.5, []string{"Onion"}, nil)
	c := s.Add("session-1", 1, "Burger", 10.5, nil, []string{"Extra cheese"})

	if len(c.Items) != 3 {
		t.Fatalf("expected 3 distinct lines (plain / no-onion / +cheese), got %d: %+v", len(c.Items), c.Items)
	}
}

func TestExcludedOrderIndependence(t *testing.T) {
	// "no onion, no pickles" and "no pickles, no onion" must merge into one
	// line, not two — sameSet has to compare order-independently.
	s := NewStore()
	s.Add("session-1", 1, "Burger", 9.5, []string{"Onion", "Pickles"}, nil)
	c := s.Add("session-1", 1, "Burger", 9.5, []string{"Pickles", "Onion"}, nil)

	if len(c.Items) != 1 {
		t.Fatalf("expected the two adds to merge into one line regardless of exclusion order, got %d lines: %+v", len(c.Items), c.Items)
	}
	if c.Items[0].Quantity != 2 {
		t.Errorf("Quantity = %d, want 2", c.Items[0].Quantity)
	}
}

func TestRemoveDecrementsThenDropsLine(t *testing.T) {
	s := NewStore()
	s.Add("session-1", 1, "Burger", 9.5, nil, nil)
	s.Add("session-1", 1, "Burger", 9.5, nil, nil)

	c := s.Remove("session-1", 1, nil, nil)
	if len(c.Items) != 1 || c.Items[0].Quantity != 1 {
		t.Fatalf("expected quantity decremented to 1, got %+v", c.Items)
	}

	c = s.Remove("session-1", 1, nil, nil)
	if len(c.Items) != 0 {
		t.Fatalf("expected the line to be dropped once quantity reaches 0, got %+v", c.Items)
	}
}

func TestRemoveOnEmptyCartIsANoOp(t *testing.T) {
	s := NewStore()
	c := s.Remove("nobody-here", 1, nil, nil)
	if len(c.Items) != 0 {
		t.Fatalf("expected an empty cart, got %+v", c.Items)
	}
}

func TestClearRemovesTheCartEntirely(t *testing.T) {
	s := NewStore()
	s.Add("session-1", 1, "Burger", 9.5, nil, nil)
	s.Clear("session-1")
	c := s.Get("session-1")
	if len(c.Items) != 0 {
		t.Fatalf("expected an empty cart after Clear, got %+v", c.Items)
	}
}

func TestReservedQuantitySumsAcrossEverySession(t *testing.T) {
	s := NewStore()
	s.Add("session-1", 42, "Mojito", 8, nil, nil)
	s.Add("session-1", 42, "Mojito", 8, nil, nil)
	s.Add("session-2", 42, "Mojito", 8, []string{"Mint"}, nil) // different customization, same product

	if got := s.ReservedQuantity(42); got != 3 {
		t.Errorf("ReservedQuantity(42) = %d, want 3 (2 from session-1 + 1 from session-2, across variants)", got)
	}
	if got := s.ReservedQuantity(999); got != 0 {
		t.Errorf("ReservedQuantity for an untouched product = %d, want 0", got)
	}
}

// TestConcurrentAddIsRace-safe guards the shared in-memory map against a
// data race under concurrent guest requests — run with `go test -race`.
func TestConcurrentAddIsRaceSafe(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Add("shared-session", 1, "Beer", 4.0, nil, nil)
		}(i)
	}
	wg.Wait()

	c := s.Get("shared-session")
	if len(c.Items) != 1 || c.Items[0].Quantity != 50 {
		t.Fatalf("expected 50 concurrent adds of the same line to sum to quantity 50, got %+v", c.Items)
	}
}
