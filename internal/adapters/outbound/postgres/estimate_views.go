package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/estimate"
)

type tierJSON struct {
	ChargeMinor   int64 `json:"chargeMinor"`
	FreeOverMinor int64 `json:"freeOverMinor"`
}

type bandJSON struct {
	From            string `json:"from"`
	To              string `json:"to"`
	RateBasisPoints int    `json:"rateBasisPoints"`
}

// ScheduleRepository is the Postgres ports.ScheduleRepository over rm_current_schedule.
type ScheduleRepository struct{ pool *pgxpool.Pool }

var _ ports.ScheduleRepository = (*ScheduleRepository)(nil)

// NewScheduleRepository constructs a ScheduleRepository over pool.
func NewScheduleRepository(pool *pgxpool.Pool) *ScheduleRepository {
	return &ScheduleRepository{pool: pool}
}

// Current implements ports.ScheduleRepository.
func (r *ScheduleRepository) Current(ctx context.Context) (estimate.Schedule, error) {
	var (
		s   estimate.Schedule
		raw []byte
	)
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT version, tiers FROM rm_current_schedule`).Scan(&s.Version, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return estimate.Schedule{}, fmt.Errorf("charge schedule: %w", ports.ErrNotFound)
	}
	if err != nil {
		return estimate.Schedule{}, err
	}
	var in map[string]tierJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return estimate.Schedule{}, fmt.Errorf("decode charge schedule: %w", err)
	}
	s.Tiers = map[cart.Tier]estimate.TierCharge{}
	for k, v := range in {
		s.Tiers[cart.Tier(k)] = estimate.TierCharge(v)
	}
	return s, nil
}

// Replace implements ports.ScheduleRepository.
func (r *ScheduleRepository) Replace(ctx context.Context, sc estimate.Schedule) (bool, error) {
	out := map[string]tierJSON{}
	for k, v := range sc.Tiers {
		out[string(k)] = tierJSON(v)
	}
	raw, err := jsonOf(out)
	if err != nil {
		return false, err
	}
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO rm_current_schedule (version, tiers) VALUES ($1, $2)
		ON CONFLICT (singleton) DO UPDATE SET version = EXCLUDED.version, tiers = EXCLUDED.tiers
		WHERE rm_current_schedule.version < EXCLUDED.version`, sc.Version, raw)
	if err != nil {
		return false, fmt.Errorf("replace charge schedule: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// TaxTableRepository is the Postgres ports.TaxTableRepository over rm_current_tax_table.
type TaxTableRepository struct{ pool *pgxpool.Pool }

var _ ports.TaxTableRepository = (*TaxTableRepository)(nil)

// NewTaxTableRepository constructs a TaxTableRepository over pool.
func NewTaxTableRepository(pool *pgxpool.Pool) *TaxTableRepository {
	return &TaxTableRepository{pool: pool}
}

// Current implements ports.TaxTableRepository.
func (r *TaxTableRepository) Current(ctx context.Context) (estimate.TaxTable, error) {
	var (
		t   estimate.TaxTable
		raw []byte
	)
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT version, bands FROM rm_current_tax_table`).Scan(&t.Version, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return estimate.TaxTable{}, fmt.Errorf("tax table: %w", ports.ErrNotFound)
	}
	if err != nil {
		return estimate.TaxTable{}, err
	}
	var in []bandJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return estimate.TaxTable{}, fmt.Errorf("decode tax table: %w", err)
	}
	for _, b := range in {
		t.Bands = append(t.Bands, estimate.TaxBand(b))
	}
	return t, nil
}

// Replace implements ports.TaxTableRepository.
func (r *TaxTableRepository) Replace(ctx context.Context, t estimate.TaxTable) (bool, error) {
	out := make([]bandJSON, len(t.Bands))
	for i, b := range t.Bands {
		out[i] = bandJSON(b)
	}
	raw, err := jsonOf(out)
	if err != nil {
		return false, err
	}
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO rm_current_tax_table (version, bands) VALUES ($1, $2)
		ON CONFLICT (singleton) DO UPDATE SET version = EXCLUDED.version, bands = EXCLUDED.bands
		WHERE rm_current_tax_table.version < EXCLUDED.version`, t.Version, raw)
	if err != nil {
		return false, fmt.Errorf("replace tax table: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
