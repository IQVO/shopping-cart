// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a driving adapter over the same
// application-layer read use cases the REST adapter and the A2A agent use. It
// is built on the official MCP Go SDK and served over Streamable HTTP only.
//
// Per docs/adr/0009-read-only-mcp-server.md the surface is READ-ONLY: every
// tool reads cart data (get_cart, list_shopper_carts); creating a cart,
// changing its lines, setting its destination, quoting delivery, choosing an
// offer, merging and accepting requoted offers stay on REST and on the outbox,
// and governance_test.go fails the build on a write-verb tool name and on a
// write seam referenced from this package. A cart is customer data (a
// shopperId reference, quantities, a delivery postal prefix), so the surface
// exposes exactly what the REST GETs expose and nothing REST withholds. This
// package depends inward on the application layer and the domain only, never
// on an outbound adapter or the REST adapter. The composition root (cmd/mcp)
// wires concrete repositories into the use cases. There is no auth of any kind
// (fleet-wide revert 2026-09-11): access control is the in-cluster ClusterIP
// boundary.
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "shopping-cart-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Shopping Cart of the storefront (read-only): the shopper's pre-checkout basket. " +
	"Read one cart with get_cart: its lines (catalog item and quantity), the destination postal prefix, the delivery quote and the shopper's chosen offers, " +
	"the labelled non-binding cost estimate in minor units (estimateComplete says whether every line is priced) and checkoutReadiness. " +
	"Find a shopper's carts with list_shopper_carts (newest first, optionally of one state OPEN, CHECKED_OUT, ABANDONED or MERGED). " +
	"The cart resource (warehouse://shopping-cart/carts/{cartId}) loads one cart without a tool call, and the review_cart prompt hands you this context's " +
	"operator procedure. Nothing here changes data: creating a cart, adding, changing or removing a line, setting the destination, quoting delivery, " +
	"choosing an offer, merging carts and checkout are REST operations of shopping-cart and checkout."

// NewServer builds the MCP server for this bounded context with every tool,
// resource and workflow prompt registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	deps.registerResources(server)
	deps.registerPrompts(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	// Stateless (Agentic Standard v2, AS-4): no Mcp-Session-Id is issued, so
	// the server also answers the 2026-07-28 revision's per-request
	// handshake (server/discover) while legacy initialize clients keep
	// working. Nothing here relies on cross-call session state.
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
}
