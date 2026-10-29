package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/application/readmodel"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// ItemRepository is the Postgres ports.ItemRepository over rm_items.
type ItemRepository struct{ pool *pgxpool.Pool }

var _ ports.ItemRepository = (*ItemRepository)(nil)

// NewItemRepository constructs an ItemRepository over pool.
func NewItemRepository(pool *pgxpool.Pool) *ItemRepository { return &ItemRepository{pool: pool} }

const itemColumns = `catalog_item_id, catalogued, discontinued, priced, price_minor, currency, price_version, updated_at`

func scanItem(row pgx.Row) (readmodel.Item, error) {
	var it readmodel.Item
	err := row.Scan(&it.CatalogItemID, &it.Catalogued, &it.Discontinued, &it.Priced, &it.PriceMinor, &it.Currency, &it.PriceVersion, &it.UpdatedAt)
	it.UpdatedAt = it.UpdatedAt.UTC()
	return it, err
}

// Get implements ports.ItemRepository.
func (r *ItemRepository) Get(ctx context.Context, id string) (readmodel.Item, error) {
	it, err := scanItem(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+itemColumns+` FROM rm_items WHERE catalog_item_id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return readmodel.Item{}, fmt.Errorf("item %s: %w", id, ports.ErrNotFound)
	}
	return it, err
}

// GetMany implements ports.ItemRepository.
func (r *ItemRepository) GetMany(ctx context.Context, ids []string) (map[string]readmodel.Item, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+itemColumns+` FROM rm_items WHERE catalog_item_id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("read items: %w", err)
	}
	defer rows.Close()
	out := map[string]readmodel.Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out[it.CatalogItemID] = it
	}
	return out, rows.Err()
}

func (r *ItemRepository) exec(ctx context.Context, what, sql string, args ...any) error {
	if _, err := queryFor(ctx, r.pool).Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// MarkCatalogued implements ports.ItemRepository.
func (r *ItemRepository) MarkCatalogued(ctx context.Context, id string, at time.Time) error {
	return r.exec(ctx, "mark item catalogued", `
		INSERT INTO rm_items (catalog_item_id, catalogued, updated_at) VALUES ($1, true, $2)
		ON CONFLICT (catalog_item_id) DO UPDATE SET catalogued = true, updated_at = EXCLUDED.updated_at`, id, shared.UTC(at))
}

// MarkDiscontinued implements ports.ItemRepository.
func (r *ItemRepository) MarkDiscontinued(ctx context.Context, id string, at time.Time) error {
	return r.exec(ctx, "mark item discontinued", `
		INSERT INTO rm_items (catalog_item_id, discontinued, updated_at) VALUES ($1, true, $2)
		ON CONFLICT (catalog_item_id) DO UPDATE SET discontinued = true, updated_at = EXCLUDED.updated_at`, id, shared.UTC(at))
}

// ApplyPrice implements ports.ItemRepository.
func (r *ItemRepository) ApplyPrice(ctx context.Context, id, currency string, minor, version int64, at time.Time) error {
	return r.exec(ctx, "apply item price", `
		INSERT INTO rm_items (catalog_item_id, priced, price_minor, currency, price_version, updated_at) VALUES ($1, true, $2, $3, $4, $5)
		ON CONFLICT (catalog_item_id) DO UPDATE SET priced = true, price_minor = EXCLUDED.price_minor, currency = EXCLUDED.currency,
			price_version = EXCLUDED.price_version, updated_at = EXCLUDED.updated_at
		WHERE rm_items.price_version < EXCLUDED.price_version`, id, minor, currency, version, shared.UTC(at))
}

// ApplyWithdrawn implements ports.ItemRepository.
func (r *ItemRepository) ApplyWithdrawn(ctx context.Context, id string, version int64, at time.Time) error {
	return r.exec(ctx, "apply item withdrawal", `
		INSERT INTO rm_items (catalog_item_id, priced, price_version, updated_at) VALUES ($1, false, $2, $3)
		ON CONFLICT (catalog_item_id) DO UPDATE SET priced = false, price_minor = 0, currency = '',
			price_version = EXCLUDED.price_version, updated_at = EXCLUDED.updated_at
		WHERE rm_items.price_version < EXCLUDED.price_version`, id, version, shared.UTC(at))
}

func jsonOf(v any) ([]byte, error) { return json.Marshal(v) }
