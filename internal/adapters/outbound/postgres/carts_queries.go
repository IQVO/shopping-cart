package postgres

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/IQVO/shopping-cart/internal/application/pagination"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

func collectCarts(rows pgx.Rows) ([]*cart.Cart, error) {
	defer rows.Close()
	var out []*cart.Cart
	for rows.Next() {
		c, err := scanCart(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListByShopper implements ports.CartRepository: (created_at, cart_id)
// descending, strictly after the keyset, at most limit rows. The row
// comparison walks the carts_shopper_idx index.
func (r *CartRepository) ListByShopper(ctx context.Context, shopperID string, state cart.State, after *pagination.Keyset, limit int) ([]*cart.Cart, error) {
	where, args := `shopper_id = $1 AND ($2 = '' OR state = $2)`, []any{shopperID, string(state)}
	if after != nil {
		where += ` AND (created_at, cart_id) < ($3, $4)`
		args = append(args, after.CreatedAt.UTC(), after.ID)
	}
	args = append(args, limit)
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT `+cartColumns+` FROM carts WHERE `+where+`
		ORDER BY created_at DESC, cart_id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("list carts of %s: %w", shopperID, err)
	}
	return collectCarts(rows)
}

// ListIdleOpen implements ports.CartRepository.
func (r *CartRepository) ListIdleOpen(ctx context.Context, cutoff time.Time, limit int) ([]*cart.Cart, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT `+cartColumns+` FROM carts WHERE state = 'OPEN' AND last_changed_at <= $1
		ORDER BY last_changed_at, cart_id LIMIT $2`, cutoff.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("list idle carts: %w", err)
	}
	return collectCarts(rows)
}

// DeleteClosedBefore implements ports.CartRepository.
func (r *CartRepository) DeleteClosedBefore(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
		DELETE FROM carts WHERE cart_id IN (
			SELECT cart_id FROM carts WHERE state <> 'OPEN' AND last_changed_at <= $1
			ORDER BY cart_id LIMIT $2 FOR UPDATE SKIP LOCKED)`, cutoff.UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("purge closed carts: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
