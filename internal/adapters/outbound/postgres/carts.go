package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// CartRepository is the Postgres ports.CartRepository.
type CartRepository struct{ pool *pgxpool.Pool }

var _ ports.CartRepository = (*CartRepository)(nil)

// NewCartRepository constructs a CartRepository over pool.
func NewCartRepository(pool *pgxpool.Pool) *CartRepository { return &CartRepository{pool: pool} }

const cartColumns = `cart_id, shopper_id, state, destination, lines, quote, selections, created_at, last_changed_at, version`

// Get implements ports.CartRepository. Inside a unit of work the row is locked
// (FOR UPDATE), so concurrent writers of one cart serialise.
func (r *CartRepository) Get(ctx context.Context, id cart.ID) (*cart.Cart, error) {
	suffix := ""
	if inUnitOfWork(ctx) {
		suffix = " FOR UPDATE"
	}
	row := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+cartColumns+` FROM carts WHERE cart_id = $1`+suffix, string(id))
	c, err := scanCart(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("cart %s: %w", id, ports.ErrNotFound)
	}
	return c, err
}

func scanCart(row pgx.Row) (*cart.Cart, error) {
	var (
		s                 cart.Snapshot
		id, state         string
		shopper, dest     *string
		lines, quote, sel []byte
		created, changed  time.Time
	)
	if err := row.Scan(&id, &shopper, &state, &dest, &lines, &quote, &sel, &created, &changed, &s.Version); err != nil {
		return nil, err
	}
	s.ID, s.State, s.CreatedAt, s.LastChangedAt = cart.ID(id), cart.State(state), created.UTC(), changed.UTC()
	if shopper != nil {
		s.ShopperID = *shopper
	}
	if dest != nil {
		s.Destination = *dest
	}
	var err error
	if s.Lines, err = unmarshalLines(lines); err != nil {
		return nil, fmt.Errorf("decode cart %s lines: %w", id, err)
	}
	if s.Quote, err = unmarshalQuote(quote); err != nil {
		return nil, fmt.Errorf("decode cart %s quote: %w", id, err)
	}
	if s.Selections, err = unmarshalSelections(sel); err != nil {
		return nil, fmt.Errorf("decode cart %s selections: %w", id, err)
	}
	return cart.Restore(s)
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Save implements ports.CartRepository: a create (LoadedVersion 0) inserts and
// expects the id to be free; any other save updates the row at the loaded
// version. Zero affected rows is ErrConcurrentUpdate.
func (r *CartRepository) Save(ctx context.Context, c *cart.Cart) error {
	s := c.Snapshot()
	lines, err := marshalLines(s.Lines)
	if err != nil {
		return fmt.Errorf("encode cart %s: %w", s.ID, err)
	}
	quote, err := marshalQuote(s.Quote)
	if err != nil {
		return fmt.Errorf("encode cart %s: %w", s.ID, err)
	}
	sel, err := marshalSelections(s.Selections)
	if err != nil {
		return fmt.Errorf("encode cart %s: %w", s.ID, err)
	}
	var affected int64
	err = inTx(ctx, r.pool, func(q querier) error {
		var tag interface{ RowsAffected() int64 }
		var err error
		if c.LoadedVersion() == 0 {
			tag, err = q.Exec(ctx, `INSERT INTO carts (`+cartColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
				ON CONFLICT (cart_id) DO NOTHING`,
				string(s.ID), nullString(s.ShopperID), string(s.State), nullString(s.Destination), lines, quote, sel,
				s.CreatedAt, s.LastChangedAt, s.Version)
		} else {
			tag, err = q.Exec(ctx, `UPDATE carts SET state = $2, destination = $3, lines = $4, quote = $5, selections = $6,
				last_changed_at = $7, version = $8 WHERE cart_id = $1 AND version = $9`,
				string(s.ID), string(s.State), nullString(s.Destination), lines, quote, sel, s.LastChangedAt, s.Version, c.LoadedVersion())
		}
		if err == nil {
			affected = tag.RowsAffected()
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("save cart %s: %w", s.ID, err)
	}
	if affected != 1 {
		return fmt.Errorf("cart %s loaded at version %d: %w", s.ID, c.LoadedVersion(), ports.ErrConcurrentUpdate)
	}
	c.MarkPersisted()
	return nil
}
