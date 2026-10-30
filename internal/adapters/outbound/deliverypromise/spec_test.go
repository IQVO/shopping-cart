package deliverypromise_test

import (
	"encoding/json"
	"os"
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
	Paths map[string]map[string]struct {
		OperationID string `yaml:"operationId"`
	} `yaml:"paths"`
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
	if op := s.Paths["/delivery-offers:quote"]["post"].OperationID; op != "quoteDeliveryOffers" {
		t.Fatalf("operationId = %q", op)
	}
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
