package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// cartPlaceholder is the token of reviewCartProcedure that the cartId argument
// replaces; without an argument it stays visible as the placeholder.
const cartPlaceholder = "<cartId>"

// reviewCartProcedure is the operator procedure the review_cart prompt hands to
// the model (Agentic Standard v2, AS-3). Per the charter, prompts encode how to
// interpret results, when to escalate and what "done" means; this one names
// tools but NEVER calls them, and it cannot write: every tool it names is
// read-only (ADR 0009) and every write of this context is a REST operation.
const reviewCartProcedure = `You are reviewing one shopper's cart in the storefront. Use only the MCP tools of shopping-cart; never assume state — read it.

Procedure:
1. Call get_cart(cartId = <cartId>). It returns the cart exactly as this service holds it: state, lines (catalogItemId and quantity), destinationPostalPrefix (null until a destination is set), deliveryQuote (offer groups, each with its offers and expiry, and the tiers that were unavailable), selections (the offer chosen per group), the estimate and checkoutReadiness. shopperId is null for a guest cart.
2. When you do not have the cart id, call list_shopper_carts(shopperId, state) first: it answers one page, newest first (hasMore says an older cart exists; narrow with state to reach it). Every entry is the same document get_cart returns.

Interpretation:
- state is OPEN, CHECKED_OUT, ABANDONED or MERGED. Only an OPEN cart can still change; the other three are closed and stay as they are, though they can still be read.
- The estimate is a labelled, NON-BINDING estimate in minor units of its currency (itemsMinor, deliveryMinor, taxMinor, totalMinor). When estimateComplete is false at least one line has no price yet: never read totalMinor as a final amount, and never state it as the price the shopper will pay.
- checkoutReadiness.ready is true only when every offer group has a selection (allGroupsSelected) and no selected offer has expired (noOfferExpired). A cart without a destination or a quote is simply not ready; say which part is missing instead of guessing.
- An unknown cart id is a not-found answer, not evidence that the shopper never had a cart.

Escalate to the human operator when the cart is not ready and the reason is an expired offer or a missing quote or destination: re-quoting, choosing an offer, setting the destination, changing lines, merging carts and checkout are REST operations of shopping-cart and checkout. Nothing in this server can perform them, so do not promise them.

Done means: you have reported the cart's state, its lines, its estimate with whether it is complete, and what checkoutReadiness says and why, and you have named the escalation when the cart is not ready.`

// registerPrompts adds the workflow prompt (the operator procedure of this
// context). Read-only by construction: a prompt returns text.
func (d Deps) registerPrompts(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "review_cart",
		Description: "Operator procedure for reviewing one cart: read it, interpret the non-binding estimate and the checkout readiness, and know what to escalate to a human because nothing here can change a cart.",
		Arguments: []*mcp.PromptArgument{{
			Name:        "cartId",
			Description: "the cart id to review, e.g. cart-123",
			Required:    true,
		}},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		cartID := strings.TrimSpace(req.Params.Arguments["cartId"])
		if cartID == "" {
			cartID = cartPlaceholder
		}
		return &mcp.GetPromptResult{
			Description: "How to review a cart: read it, interpret the estimate and the checkout readiness, escalate a cart that is not ready to a human.",
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: strings.ReplaceAll(reviewCartProcedure, cartPlaceholder, cartID)},
			}},
		}, nil
	})
}
