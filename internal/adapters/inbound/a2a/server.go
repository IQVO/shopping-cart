// Package a2a is the inbound Agent2Agent adapter: it exposes this bounded
// context to the fleet's agent ecosystem as a third driving adapter (beside
// REST and Kafka) over the SAME application-layer read use cases the REST GETs
// call. It is built on the official A2A Go SDK (a2aproject/a2a-go) and speaks
// JSON-RPC 2.0 over HTTP, with the Agent Card served at the well-known path
// (ADR 0006).
//
// A2A is the DELEGATION channel between bounded contexts: an agent client
// sends a goal-shaped skill request as a structured message part and this
// agent answers from its own state through its own use cases. It is not a
// data-sync channel: facts between contexts still travel exclusively as
// CloudEvents on Kafka, published through the transactional outbox (ADR 0004).
//
// This agent is READ-ONLY. shopping-cart has no MCP surface to derive skills
// from, so the two skills mirror the two REST GETs (GET /carts/{cartId} and GET
// /carts?shopperId=&state=) and expose exactly what those expose: the cart
// wire shape of apis/openapi.yaml and nothing REST withholds. A cart is
// customer data (a shopperId reference, quantities, a delivery postal prefix),
// so the surface never widens past the REST GETs. This package never writes,
// never publishes an event, never calls another bounded context and contains
// no outbound A2A client.
//
// This package depends inward on the application layer (use cases, views) and
// the domain only, never on an outbound adapter or the REST adapter; the
// composition root (cmd/a2a) wires the concrete repositories into the use
// cases. Domain structs never leak across the message boundary.
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/IQVO/shopping-cart/internal/application/usecases"
)

// Config carries the deployment-specific identity of this agent: the absolute
// URL clients use to reach its JSON-RPC endpoint (advertised on the Agent
// Card) and the card version. It is supplied by the composition root from
// A2A_AGENT_URL; the card MUST advertise a URL the fleet's agent clients can
// actually dial (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/).
type Config struct {
	AgentURL string
	Version  string
}

// Deps is everything the A2A skills need, injected by the composition root. It
// carries the SAME read use cases the REST GETs use; the adapter never
// constructs an outbound adapter itself, and never writes.
type Deps struct {
	// GetCart is the existing read use case behind GET /carts/{cartId}.
	GetCart usecases.GetCart
	// ListCarts is the existing read use case behind GET /carts.
	ListCarts usecases.ListCarts
}

// Card builds this agent's Agent Card (ADR 0006). The surface is two curated,
// goal-shaped READ skills mirroring the two REST GETs. The card declares no
// security schemes on purpose: this surface is unauthenticated by the same
// fleet-wide decision as REST (2026-09-11); schemes are added here, not in a
// redesign, if that decision is ever reversed.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "shopping-cart",
		Description: "Shopping Cart agent for the storefront: a read-only view of a shopper's carts. It answers what a cart holds (its lines, destination postal prefix, delivery quote, the shopper's delivery selections, the labelled non-binding cost estimate and whether the cart is ready for checkout) and which carts a shopper has. It exposes exactly what the REST GET endpoints expose, never changes a cart and never places an order.",
		Version:     cfg.Version,
		Provider: &a2a.AgentProvider{
			Org: "IQVO",
			URL: "https://github.com/IQVO/shopping-cart",
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:          skillGetCart,
				Name:        "Look up one cart",
				Description: "Read one cart by its cartId: lines, destinationPostalPrefix, deliveryQuote, selections, the labelled estimate (minor units, estimateComplete says whether every line is priced) and checkoutReadiness. The same document GET /carts/{cartId} returns, for a cart of any state. Answers a 404 problem when no cart has that id. Persists nothing.",
				Tags:        []string{"cart", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"get_cart","arguments":{"cartId":"cart-123"}}`,
				},
			},
			{
				ID:          skillListShopperCarts,
				Name:        "List a shopper's carts",
				Description: "Read the carts of one shopper, newest first, optionally of one state (OPEN, CHECKED_OUT, ABANDONED or MERGED). Each entry is the same document get_cart returns. The same answer as GET /carts?shopperId=&state=. A shopper with no carts gets an empty list, not a problem. Persists nothing.",
				Tags:        []string{"cart", "shopper", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"list_shopper_carts","arguments":{"shopperId":"shopper-42"}}`,
					`{"skill":"list_shopper_carts","arguments":{"shopperId":"shopper-42","state":"OPEN"}}`,
				},
			},
		},
	}
}

// NewExecutor returns the AgentExecutor that maps an incoming message to a
// skill and answers with a single agent Message (a message-only agent in A2A
// terms): both curated skills are transactional request/response reads, so
// there is no long-running Task lifecycle to track in this first A2A surface.
// The response carries the caller's contextId (and taskId when the client
// referenced one) so a delegating saga can group its interactions.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wires the executor into the transport-agnostic A2A request handler
// and wraps it in the JSON-RPC 2.0 HTTP handler, the only transport this agent
// ships (ADR 0006).
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
