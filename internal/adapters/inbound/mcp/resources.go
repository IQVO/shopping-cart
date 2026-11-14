package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mimeTypeJSON is the MIME type of every resource this server serves.
const mimeTypeJSON = "application/json"

const (
	// cartURIScheme is the fleet URI shape of this context's resources.
	cartURIScheme = "warehouse://shopping-cart/carts/"
	// cartURITemplate is the ONE resource this server offers (Agentic Standard
	// v2, AS-3): a scoped read model this context owns, not a dump of a table.
	// It answers "what does this cart hold, what would it cost, is it ready for
	// checkout?" for ONE cart, built from the same GetCart read use case the
	// get_cart tool and GET /carts/{cartId} use. Bounded by construction: one
	// cart, the REST cart document's fixed fields, JSON text.
	cartURITemplate = cartURIScheme + "{cartId}"
)

// registerResources adds the resource surface (read-only by nature, L1; the
// charter keeps resources context data an agent can load without a tool
// call). No resource ever writes: Deps holds read use cases only (ADR 0009).
func (d Deps) registerResources(server *mcp.Server) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: cartURITemplate,
		Name:        "cart",
		Description: "One cart: its lines, destination postal prefix, delivery quote, the shopper's selections, the labelled non-binding estimate in minor units and the checkout readiness. The same document as get_cart and GET /carts/{cartId}. An unknown cart is an error.",
		MIMEType:    mimeTypeJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		cartID, err := parseCartURI(req.Params.URI)
		if err != nil {
			return nil, err
		}
		view, err := d.readCart(ctx, cartID)
		if err != nil {
			return nil, err
		}
		return resourceResult(req.Params.URI, view)
	})
}

// resourceResult marshals one read model into the resource's text contents.
func resourceResult(uri string, view any) (*mcp.ReadResourceResult, error) {
	body, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: mimeTypeJSON,
			Text:     string(body),
		}},
	}, nil
}

// parseCartURI extracts the cartId from a warehouse://shopping-cart/carts/{cartId}
// URI. The id is model-supplied and therefore untrusted: a malformed URI or an
// empty id is rejected rather than silently defaulted.
func parseCartURI(uri string) (string, error) {
	rest, ok := strings.CutPrefix(uri, cartURIScheme)
	if !ok {
		return "", fmt.Errorf("resource uri %q must start with %q", uri, cartURIScheme)
	}
	if rest == "" || strings.Contains(rest, "/") {
		return "", fmt.Errorf("resource uri %q must carry exactly one non-empty cartId after %q", uri, cartURIScheme)
	}
	return rest, nil
}
