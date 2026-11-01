package memory

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/IQVO/shopping-cart/internal/application/pagination"
	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

var _ ports.CartRepository = (*CartRepository)(nil)

// CartRepository is the in-memory ports.CartRepository.
type CartRepository struct{ s *Store }

// Get implements ports.CartRepository.
func (r *CartRepository) Get(ctx context.Context, id cart.ID) (*cart.Cart, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.carts[id]
	if !ok {
		return nil, fmt.Errorf("cart %s: %w", id, ports.ErrNotFound)
	}
	return cart.Restore(snap)
}

// Save implements ports.CartRepository.
func (r *CartRepository) Save(ctx context.Context, c *cart.Cart) error {
	defer r.s.enter(ctx)()
	stored, exists := r.s.carts[c.ID()]
	if c.LoadedVersion() == 0 && exists || c.LoadedVersion() > 0 && (!exists || stored.Version != c.LoadedVersion()) {
		return fmt.Errorf("cart %s loaded at version %d: %w", c.ID(), c.LoadedVersion(), ports.ErrConcurrentUpdate)
	}
	r.s.carts[c.ID()] = c.Snapshot()
	c.MarkPersisted()
	return nil
}

// ListByShopper implements ports.CartRepository: (created-at, id) descending,
// strictly after the keyset, at most limit rows.
func (r *CartRepository) ListByShopper(ctx context.Context, shopperID string, state cart.State, after *pagination.Keyset, limit int) ([]*cart.Cart, error) {
	defer r.s.enter(ctx)()
	var snaps []cart.Snapshot
	for _, s := range r.s.carts {
		if listed(s, shopperID, state, after) {
			snaps = append(snaps, s)
		}
	}
	slices.SortFunc(snaps, func(a, b cart.Snapshot) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return -compareIDs(a.ID, b.ID)
	})
	if limit >= 0 && len(snaps) > limit {
		snaps = snaps[:limit]
	}
	return restoreAll(snaps)
}

// listed reports whether the snapshot belongs to the shopper's (optionally
// state-filtered) list and sorts after the keyset.
func listed(s cart.Snapshot, shopperID string, state cart.State, after *pagination.Keyset) bool {
	if shopperID == "" || s.ShopperID != shopperID || (state != "" && s.State != state) {
		return false
	}
	return isAfter(s, after)
}

// isAfter reports whether the snapshot sorts strictly after the keyset in the
// (created-at, id) descending order; a nil keyset admits every snapshot.
func isAfter(s cart.Snapshot, k *pagination.Keyset) bool {
	if k == nil {
		return true
	}
	if c := s.CreatedAt.Compare(k.CreatedAt); c != 0 {
		return c < 0
	}
	return compareIDs(s.ID, cart.ID(k.ID)) < 0
}

func compareIDs(a, b cart.ID) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func restoreAll(snaps []cart.Snapshot) ([]*cart.Cart, error) {
	out := make([]*cart.Cart, 0, len(snaps))
	for _, s := range snaps {
		c, err := cart.Restore(s)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// ListIdleOpen implements ports.CartRepository.
func (r *CartRepository) ListIdleOpen(ctx context.Context, cutoff time.Time, limit int) ([]*cart.Cart, error) {
	defer r.s.enter(ctx)()
	var snaps []cart.Snapshot
	for _, s := range r.s.carts {
		if s.State == cart.StateOpen && !s.LastChangedAt.After(cutoff) {
			snaps = append(snaps, s)
		}
	}
	slices.SortFunc(snaps, func(a, b cart.Snapshot) int {
		if c := a.LastChangedAt.Compare(b.LastChangedAt); c != 0 {
			return c
		}
		return compareIDs(a.ID, b.ID)
	})
	if len(snaps) > limit {
		snaps = snaps[:limit]
	}
	return restoreAll(snaps)
}

// DeleteClosedBefore implements ports.CartRepository.
func (r *CartRepository) DeleteClosedBefore(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	defer r.s.enter(ctx)()
	var ids []cart.ID
	for id, s := range r.s.carts {
		if s.State != cart.StateOpen && !s.LastChangedAt.After(cutoff) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	for _, id := range ids {
		delete(r.s.carts, id)
	}
	return len(ids), nil
}
