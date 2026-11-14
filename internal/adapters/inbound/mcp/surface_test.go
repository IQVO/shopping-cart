// The MCP surface beyond tools (Agentic Standard v2, AS-3): ONE resource
// template (the scoped cart read model) and ONE prompt (this context's
// operator procedure). Pinned through the same in-memory client the tool
// tests use.
package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const cartURIPrefix = "warehouse://shopping-cart/carts/"

// The resource template is advertised with the fleet URI shape and reads the
// same read model as the get_cart tool.
func TestSurface_ResourceTemplateListedAndRead(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()

	listed, err := h.session.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	if len(listed.ResourceTemplates) != 1 {
		t.Fatalf("resource templates = %+v, want exactly one", listed.ResourceTemplates)
	}
	if rt := listed.ResourceTemplates[0]; rt.URITemplate != cartURIPrefix+"{cartId}" || rt.MIMEType != "application/json" {
		t.Fatalf("template = %q (%s), want %s{cartId} as application/json", rt.URITemplate, rt.MIMEType, cartURIPrefix)
	}

	uri := cartURIPrefix + h.world.ready
	res, err := h.session.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(res.Contents) != 1 || res.Contents[0].URI != uri {
		t.Fatalf("contents = %+v, want one entry for %s", res.Contents, uri)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &doc); err != nil {
		t.Fatalf("resource text is not the cart JSON: %v (%s)", err, res.Contents[0].Text)
	}
	if want := h.ok(t, "get_cart", map[string]any{"cartId": h.world.ready}); !equalJSON(t, doc, want) {
		t.Errorf("resource = %v, want the get_cart document %v", doc, want)
	}
}

func equalJSON(t *testing.T, a, b any) bool {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(x) == string(y)
}

// A malformed URI and an unknown cart are errors, never silent defaults.
func TestSurface_ResourceRejectsMalformedAndUnknown(t *testing.T) {
	h := newHarness(t)
	for _, uri := range []string{
		"staffing://shopping-cart/carts/" + h.world.ready,         // wrong scheme
		"warehouse://shopping-cart/shoppers/" + h.world.ready,     // wrong model
		"warehouse://shopping-cart/carts/",                        // empty cartId
		"warehouse://shopping-cart/carts/" + h.world.ready + "/x", // trailing segment
		"warehouse://shopping-cart/carts/cart-nobody",             // unknown cart
	} {
		if _, err := h.session.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: uri}); err == nil {
			t.Errorf("read resource %q: expected an error", uri)
		}
	}
}

// The prompt is advertised with its typed argument and returns the procedure.
func TestSurface_PromptListedAndGet(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()

	listed, err := h.session.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(listed.Prompts) != 1 || listed.Prompts[0].Name != "review_cart" {
		t.Fatalf("prompts = %+v, want exactly review_cart", listed.Prompts)
	}
	if args := listed.Prompts[0].Arguments; len(args) != 1 || args[0].Name != "cartId" || !args[0].Required {
		t.Fatalf("prompt arguments = %+v, want one required cartId", args)
	}

	res, err := h.session.GetPrompt(ctx, &sdk.GetPromptParams{Name: "review_cart", Arguments: map[string]string{"cartId": h.world.ready}})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(res.Messages))
	}
	body := res.Messages[0].Content.(*sdk.TextContent).Text
	for _, needle := range []string{"get_cart", "list_shopper_carts", "estimateComplete", "checkoutReadiness", h.world.ready} {
		if !strings.Contains(body, needle) {
			t.Errorf("procedure does not mention %q", needle)
		}
	}

	// A missing argument degrades to the placeholder, never to a hidden call.
	noArg, err := h.session.GetPrompt(ctx, &sdk.GetPromptParams{Name: "review_cart"})
	if err != nil {
		t.Fatalf("get prompt without arguments: %v", err)
	}
	if plain := noArg.Messages[0].Content.(*sdk.TextContent).Text; !strings.Contains(plain, "<cartId>") {
		t.Errorf("procedure without an argument should carry the <cartId> placeholder")
	}
}
