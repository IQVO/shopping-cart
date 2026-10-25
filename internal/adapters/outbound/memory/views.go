package memory

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/application/readmodel"
	"github.com/IQVO/shopping-cart/internal/domain/estimate"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

var (
	_ ports.ItemRepository     = (*ItemRepository)(nil)
	_ ports.ScheduleRepository = (*ScheduleRepository)(nil)
	_ ports.TaxTableRepository = (*TaxTableRepository)(nil)
)

// ItemRepository is the in-memory ports.ItemRepository.
type ItemRepository struct{ s *Store }

// Get implements ports.ItemRepository.
func (r *ItemRepository) Get(ctx context.Context, id string) (readmodel.Item, error) {
	defer r.s.enter(ctx)()
	it, ok := r.s.items[id]
	if !ok {
		return readmodel.Item{}, fmt.Errorf("item %s: %w", id, ports.ErrNotFound)
	}
	return it, nil
}

// GetMany implements ports.ItemRepository.
func (r *ItemRepository) GetMany(ctx context.Context, ids []string) (map[string]readmodel.Item, error) {
	defer r.s.enter(ctx)()
	out := map[string]readmodel.Item{}
	for _, id := range ids {
		if it, ok := r.s.items[id]; ok {
			out[id] = it
		}
	}
	return out, nil
}

func (r *ItemRepository) upsert(id string, at time.Time, change func(it *readmodel.Item)) {
	it := r.s.items[id]
	it.CatalogItemID = id
	change(&it)
	it.UpdatedAt = shared.UTC(at)
	r.s.items[id] = it
}

// MarkCatalogued implements ports.ItemRepository.
func (r *ItemRepository) MarkCatalogued(ctx context.Context, id string, at time.Time) error {
	defer r.s.enter(ctx)()
	r.upsert(id, at, func(it *readmodel.Item) { it.Catalogued = true })
	return nil
}

// MarkDiscontinued implements ports.ItemRepository.
func (r *ItemRepository) MarkDiscontinued(ctx context.Context, id string, at time.Time) error {
	defer r.s.enter(ctx)()
	r.upsert(id, at, func(it *readmodel.Item) { it.Discontinued = true })
	return nil
}

// ApplyPrice implements ports.ItemRepository.
func (r *ItemRepository) ApplyPrice(ctx context.Context, id, currency string, minor, version int64, at time.Time) error {
	defer r.s.enter(ctx)()
	if it, ok := r.s.items[id]; ok && version <= it.PriceVersion {
		return nil
	}
	r.upsert(id, at, func(it *readmodel.Item) {
		it.Priced, it.PriceMinor, it.Currency, it.PriceVersion = true, minor, currency, version
	})
	return nil
}

// ApplyWithdrawn implements ports.ItemRepository.
func (r *ItemRepository) ApplyWithdrawn(ctx context.Context, id string, version int64, at time.Time) error {
	defer r.s.enter(ctx)()
	if it, ok := r.s.items[id]; ok && version <= it.PriceVersion {
		return nil
	}
	r.upsert(id, at, func(it *readmodel.Item) {
		it.Priced, it.PriceMinor, it.Currency, it.PriceVersion = false, 0, "", version
	})
	return nil
}

// ScheduleRepository is the in-memory ports.ScheduleRepository.
type ScheduleRepository struct{ s *Store }

// Current implements ports.ScheduleRepository.
func (r *ScheduleRepository) Current(ctx context.Context) (estimate.Schedule, error) {
	defer r.s.enter(ctx)()
	if r.s.schedule == nil {
		return estimate.Schedule{}, fmt.Errorf("charge schedule: %w", ports.ErrNotFound)
	}
	return cloneSchedule(*r.s.schedule), nil
}

// Replace implements ports.ScheduleRepository.
func (r *ScheduleRepository) Replace(ctx context.Context, sc estimate.Schedule) (bool, error) {
	defer r.s.enter(ctx)()
	if r.s.schedule != nil && sc.Version <= r.s.schedule.Version {
		return false, nil
	}
	c := cloneSchedule(sc)
	r.s.schedule = &c
	return true, nil
}

func cloneSchedule(sc estimate.Schedule) estimate.Schedule {
	return estimate.Schedule{Version: sc.Version, Tiers: cloneMap(sc.Tiers)}
}

// TaxTableRepository is the in-memory ports.TaxTableRepository.
type TaxTableRepository struct{ s *Store }

// Current implements ports.TaxTableRepository.
func (r *TaxTableRepository) Current(ctx context.Context) (estimate.TaxTable, error) {
	defer r.s.enter(ctx)()
	if r.s.taxTable == nil {
		return estimate.TaxTable{}, fmt.Errorf("tax table: %w", ports.ErrNotFound)
	}
	return estimate.TaxTable{Version: r.s.taxTable.Version, Bands: slices.Clone(r.s.taxTable.Bands)}, nil
}

// Replace implements ports.TaxTableRepository.
func (r *TaxTableRepository) Replace(ctx context.Context, t estimate.TaxTable) (bool, error) {
	defer r.s.enter(ctx)()
	if r.s.taxTable != nil && t.Version <= r.s.taxTable.Version {
		return false, nil
	}
	c := estimate.TaxTable{Version: t.Version, Bands: slices.Clone(t.Bands)}
	r.s.taxTable = &c
	return true, nil
}
