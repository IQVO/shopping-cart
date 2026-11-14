package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// Deps is everything the MCP tools need, injected by the composition root. It
// holds the SAME two read use cases the REST GETs (GET /carts/{cartId} and
// GET /carts) and the A2A skills use, and nothing that can write
// (docs/adr/0009): the composition root (cmd/mcp) wires no write seam at all.
type Deps struct {
	GetCart   usecases.GetCart
	ListCarts usecases.ListCarts
}

const (
	// maxCartIDLength mirrors the REST adapter's bound on a path identifier: a
	// longer cartId cannot name a cart, so it is answered not-found like REST
	// does.
	maxCartIDLength = 128
	// defaultListLimit and maxListLimit bound list_shopper_carts: ONE page of
	// the shopper's carts, narrower than REST (whose default is 100 and cap
	// 500) because every entry is a full cart document.
	defaultListLimit = 20
	maxListLimit     = 100
)

// --- inputs -------------------------------------------------------------------

// getCartInput mirrors GET /carts/{cartId}: the path parameter.
type getCartInput struct {
	CartID string `json:"cartId" jsonschema:"the cart id, e.g. cart-123"`
}

// listShopperCartsInput mirrors GET /carts: the required shopperId and the
// optional state and limit query parameters (no cursor: one page, newest first).
type listShopperCartsInput struct {
	ShopperID string `json:"shopperId" jsonschema:"the shopper id whose carts are listed, e.g. shopper-42"`
	State     string `json:"state,omitempty" jsonschema:"optional state filter: OPEN, CHECKED_OUT, ABANDONED or MERGED; omitted means every state"`
	Limit     int    `json:"limit,omitempty" jsonschema:"optional page size from 1 to 100; omitted means 20"`
}

// --- registry -----------------------------------------------------------------

// registerTools adds the two read-only tools, one per read GET of
// apis/openapi.yaml. Every tool is annotated read-only, idempotent and
// closed-world (the annotation literal is inlined per tool so the fleet's
// static authority sensor can prove ReadOnlyHint) and declares its authority
// level L1 under com.warehouse/authority (Agentic Standard v2, ADR 0009): each
// is a pure read of this context's own state. TestToolSurface pins the set and
// fails the build on any write-verb name (docs/adr/0009).
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false

	addTool(server, &mcp.Tool{
		Name: "get_cart",
		Description: "Read one cart by its cartId: the lines (catalog item and quantity), the destinationPostalPrefix, the deliveryQuote (offer groups and unavailable tiers), " +
			"the shopper's selections, the labelled estimate in minor units (estimateComplete says whether every line is priced; it is a non-binding estimate) and " +
			"checkoutReadiness. The same document GET /carts/{cartId} returns, for a cart of any state. shopperId is null for a guest cart. " +
			"Read-only; an unknown cartId fails with not-found, a blank one with validation-failed.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.getCart)

	addTool(server, &mcp.Tool{
		Name: "list_shopper_carts",
		Description: "List the carts of one shopper, newest first, optionally of one state (OPEN, CHECKED_OUT, ABANDONED or MERGED). Each entry is the same document get_cart returns. " +
			"Answers ONE page of at most limit carts (1 to 100, default 20) and hasMore says an older cart exists; narrow with state to reach it. A shopper with no carts gets an " +
			"empty list, not an error. Read-only; a missing or invalid shopperId, an unknown state or a limit out of range fails with validation-failed.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.listShopperCarts)
}

// addTool registers one tool. A handler error is returned to the SDK as the
// handler's error, which the SDK turns into an isError tool result (never a
// transport/protocol failure), carrying the error text.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}

// --- handlers -----------------------------------------------------------------

// readCart is the single-cart read shared by the get_cart tool and the cart
// resource: the same input checks as the A2A get_cart skill, then GetCart.
func (d Deps) readCart(ctx context.Context, cartID string) (cartView, error) {
	if strings.TrimSpace(cartID) == "" {
		return cartView{}, invalid("cartId is required")
	}
	// The same answers REST gives a path that can never name a resource.
	if strings.ContainsRune(cartID, 0) || len(cartID) > maxCartIDLength {
		return cartView{}, toolError("not-found", "no cart exists with that cartId")
	}
	v, err := d.GetCart.Execute(ctx, cartID)
	if err != nil {
		return cartView{}, mapError(ctx, err)
	}
	return toCart(v), nil
}

func (d Deps) getCart(ctx context.Context, in getCartInput) (cartView, error) {
	return d.readCart(ctx, in.CartID)
}

// listShopperCarts is the shopper read, through the same ListCarts use case
// GET /carts uses, with the same input checks as that handler.
func (d Deps) listShopperCarts(ctx context.Context, in listShopperCartsInput) (cartListView, error) {
	if strings.ContainsRune(in.ShopperID, 0) || strings.ContainsRune(in.State, 0) {
		return cartListView{}, invalid("a NUL character (U+0000) is not allowed in an argument")
	}
	if in.ShopperID == "" || !shared.ValidID(in.ShopperID) {
		return cartListView{}, invalid("shopperId is required")
	}
	state := cart.State(in.State)
	if state != "" && !state.Valid() {
		return cartListView{}, invalid("state must be OPEN, CHECKED_OUT, ABANDONED or MERGED")
	}
	limit := in.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit < 1 || limit > maxListLimit {
		return cartListView{}, invalid("limit must be between 1 and 100")
	}
	page, err := d.ListCarts.Execute(ctx, in.ShopperID, state, nil, limit)
	if err != nil {
		return cartListView{}, mapError(ctx, err)
	}
	out := cartListView{Carts: make([]cartView, len(page.Items)), HasMore: page.Next != nil}
	for i, v := range page.Items {
		out.Carts[i] = toCart(v)
	}
	return out, nil
}
