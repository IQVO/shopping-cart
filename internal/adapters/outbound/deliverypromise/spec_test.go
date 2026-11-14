package deliverypromise_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type spec struct {
	Components struct {
		Schemas map[string]struct {
			Required   []string       `yaml:"required"`
			Properties map[string]any `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
	Paths map[string]pathItem `yaml:"paths"`
}

type operation struct {
	OperationID string `yaml:"operationId"`
	Parameters  []struct {
		Ref string `yaml:"$ref"`
	} `yaml:"parameters"`
}

// pathItem keeps only the HTTP-method operations of a path. A path item may also
// carry `servers` or `parameters` lists (the producer's /reports paths do), which
// are not operations.
type pathItem map[string]operation

func (p *pathItem) UnmarshalYAML(n *yaml.Node) error {
	var raw map[string]yaml.Node
	if err := n.Decode(&raw); err != nil {
		return err
	}
	*p = pathItem{}
	for key, node := range raw {
		switch key {
		case "get", "put", "post", "delete", "patch", "head", "options", "trace":
			var op operation
			if err := node.Decode(&op); err != nil {
				return err
			}
			(*p)[key] = op
		}
	}
	return nil
}

func loadSpec(t *testing.T) spec {
	t.Helper()
	raw, err := os.ReadFile("testdata/delivery-promise.openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var s spec
	if err := yaml.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func requireKeys(t *testing.T, s spec, schema string, got map[string]any) {
	t.Helper()
	def, ok := s.Components.Schemas[schema]
	if !ok {
		t.Fatalf("schema %s is not in the spec copy", schema)
	}
	for _, k := range def.Required {
		if _, present := got[k]; !present {
			t.Errorf("%s lacks the required field %q", schema, k)
		}
	}
	for k := range got {
		if _, declared := def.Properties[k]; !declared {
			t.Errorf("%s carries %q, which the spec does not declare", schema, k)
		}
	}
}

func TestTheSpecCopyDeclaresTheRouteWeCall(t *testing.T) {
	s := loadSpec(t)
	if op := s.Paths["/delivery-quotes"]["post"].OperationID; op != "quoteDeliveryOffers" {
		t.Fatalf("operationId = %q", op)
	}
}

// The gateway refuses to send a keyless POST (fleet REST guideline); that is only
// right while the contract still requires the header on this route.
func TestTheSpecCopyRequiresTheIdempotencyKeyOnTheQuoteRoute(t *testing.T) {
	var raw struct {
		Components struct {
			Parameters map[string]struct {
				Name     string `yaml:"name"`
				In       string `yaml:"in"`
				Required bool   `yaml:"required"`
			} `yaml:"parameters"`
		} `yaml:"components"`
	}
	data, err := os.ReadFile("testdata/delivery-promise.openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, p := range loadSpec(t).Paths["/delivery-quotes"]["post"].Parameters {
		const prefix = "#/components/parameters/"
		def, ok := raw.Components.Parameters[strings.TrimPrefix(p.Ref, prefix)]
		if ok && def.Name == "Idempotency-Key" && def.In == "header" {
			if !def.Required {
				t.Fatal("the quote route no longer requires the Idempotency-Key header")
			}
			return
		}
	}
	t.Fatal("the quote route does not declare the Idempotency-Key header")
}

// The gateway reads window.timeZone (ADR 0007); the contract must declare it
// and keep it optional, so offers quoted before it existed stay valid.
func TestTheSpecCopyDeclaresTheOptionalWindowTimeZone(t *testing.T) {
	def := loadSpec(t).Components.Schemas["DeliveryWindow"]
	if _, ok := def.Properties["timeZone"]; !ok {
		t.Fatal("DeliveryWindow does not declare timeZone")
	}
	for _, k := range def.Required {
		if k == "timeZone" {
			t.Fatal("timeZone must stay optional")
		}
	}
}

func TestExampleSatisfiesTheSpec(t *testing.T) {
	s := loadSpec(t)
	var resp map[string]any
	if err := json.Unmarshal([]byte(example(t)), &resp); err != nil {
		t.Fatal(err)
	}
	requireKeys(t, s, "QuoteResponse", resp)
	for _, o := range resp["offers"].([]any) {
		offer := o.(map[string]any)
		requireKeys(t, s, "DeliveryOffer", offer)
		requireKeys(t, s, "DeliveryWindow", offer["window"].(map[string]any))
		for _, l := range offer["lines"].([]any) {
			requireKeys(t, s, "GroupLine", l.(map[string]any))
		}
	}
	for _, u := range resp["unavailable"].([]any) {
		requireKeys(t, s, "TierUnavailable", u.(map[string]any))
	}
}

func TestOurRequestSatisfiesTheSpec(t *testing.T) {
	g, got := serve(t, 200, "application/json", `{"offers":[],"unavailable":[]}`)
	if _, err := g.Quote(t.Context(), "k", "787", cartLines); err != nil {
		t.Fatal(err)
	}
	s := loadSpec(t)
	requireKeys(t, s, "QuoteRequest", got.body)
	for _, l := range got.body["lines"].([]any) {
		requireKeys(t, s, "GroupLine", l.(map[string]any))
	}
}
