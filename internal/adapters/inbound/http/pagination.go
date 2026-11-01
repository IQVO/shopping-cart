package http

import (
	"fmt"
	"net/http"
	"time"

	"github.com/IQVO/shopping-cart/internal/adapters/inbound/http/page"
	"github.com/IQVO/shopping-cart/internal/application/pagination"
	"github.com/IQVO/shopping-cart/internal/application/views"
)

// pageParams reads the `limit` and `cursor` query parameters of a collection
// GET. The cursor is the opaque page.Position whose sort key is the created-at
// instant of the last returned cart (RFC 3339, nanoseconds) and whose id is
// its cartId; a cursor that does not decode to that is invalid-cursor.
func pageParams(r *http.Request) (limit int, after *pagination.Keyset, err error) {
	if limit, err = page.Limit(r); err != nil {
		return 0, nil, err
	}
	pos, err := page.After(r)
	if err != nil || pos == nil {
		return limit, nil, err
	}
	at, perr := time.Parse(time.RFC3339Nano, pos.Sort)
	if perr != nil {
		return 0, nil, fmt.Errorf("%w: %v", page.ErrInvalidCursor, perr)
	}
	return limit, &pagination.Keyset{CreatedAt: at, ID: pos.ID}, nil
}

// cartPageOut renders one use case page as the {items,nextCursor} envelope.
func cartPageOut(items []views.Cart, next *pagination.Keyset) page.Page[cartBody] {
	out := page.Page[cartBody]{Items: make([]cartBody, len(items))}
	for i, v := range items {
		out.Items[i] = cartOut(v)
	}
	if next != nil {
		out.NextCursor = page.Encode(page.Position{Sort: next.CreatedAt.UTC().Format(time.RFC3339Nano), ID: next.ID})
	}
	return out
}
