package a2a

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"gopkg.in/yaml.v3"

	inboundhttp "github.com/IQVO/shopping-cart/internal/adapters/inbound/http"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
)

// restRouter is the REST adapter over the SAME read-only Deps, so the two
// surfaces can be compared byte for byte.
func (f *fixture) restRouter() http.Handler {
	return inboundhttp.NewRouter(&inboundhttp.Server{
		GetCart: usecases.GetCart{Deps: f.readOnly}, ListCarts: usecases.ListCarts{Deps: f.readOnly},
	}, nil, "")
}

func restGET(t *testing.T, h http.Handler, target string) any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", target, rec.Code, rec.Body.String())
	}
	var v any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func asAny(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// The exposure guard: for every kind of cart the document the skill answers is
// IDENTICAL to what the REST GET returns, so the A2A surface can neither add a
// field REST withholds (internal timestamps, anything customer-specific) nor
// drop one.
func TestGetCartAnswersExactlyWhatRESTGETReturns(t *testing.T) {
	f := newFixture(t)
	w := newWire(t, f.deps())
	rest := f.restRouter()

	for name, id := range map[string]string{"ready": f.ready, "open": f.open, "guest": f.guest} {
		t.Run(name, func(t *testing.T) {
			env, _ := w.skill(t, skillGetCart, map[string]any{"cartId": id})
			if env.Problem != nil || env.Data == nil {
				t.Fatalf("envelope = %+v", env)
			}
			got, want := asAny(t, env.Data.Cart), restGET(t, rest, "/carts/"+id)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("A2A cart differs from the REST GET:\n a2a: %v\nrest: %v", got, want)
			}
		})
	}
}

func TestListShopperCartsAnswersExactlyWhatRESTGETReturns(t *testing.T) {
	f := newFixture(t)
	w := newWire(t, f.deps())
	rest := f.restRouter()

	for name, tc := range map[string]struct {
		args  map[string]any
		query string
	}{
		"all":   {map[string]any{"shopperId": "shopper-1"}, "?shopperId=shopper-1"},
		"open":  {map[string]any{"shopperId": "shopper-1", "state": "OPEN"}, "?shopperId=shopper-1&state=OPEN"},
		"other": {map[string]any{"shopperId": "shopper-2"}, "?shopperId=shopper-2"},
		"none":  {map[string]any{"shopperId": "nobody"}, "?shopperId=nobody"},
	} {
		t.Run(name, func(t *testing.T) {
			env, _ := w.skill(t, skillListShopperCarts, tc.args)
			if env.Problem != nil || env.Data == nil || env.Data.Carts == nil {
				t.Fatalf("envelope = %+v", env)
			}
			got := map[string]any{"items": toAnySlice(t, env.Data.Carts.Carts)}
			if want := restGET(t, rest, "/carts"+tc.query); !reflect.DeepEqual(got, want) {
				t.Fatalf("A2A list differs from the REST GET:\n a2a: %v\nrest: %v", got, want)
			}
		})
	}
}

func toAnySlice(t *testing.T, raws []json.RawMessage) []any {
	t.Helper()
	out := make([]any, len(raws))
	for i, raw := range raws {
		out[i] = asAny(t, raw)
	}
	return out
}

// openAPIProps returns the property names of a components/schemas entry of
// apis/openapi.yaml.
func openAPIProps(t *testing.T, schema string) []string {
	t.Helper()
	raw, err := os.ReadFile("../../../../apis/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	s, ok := doc.Components.Schemas[schema]
	if !ok {
		t.Fatalf("schema %s not found in apis/openapi.yaml", schema)
	}
	return sortedKeys(s.Properties)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%v is not an object", v)
	}
	return m
}

func first(t *testing.T, v any) any {
	t.Helper()
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("%v is not a non-empty array", v)
	}
	return list[0]
}

// The wire pin against the published contract: every object the skills answer
// carries exactly the properties its apis/openapi.yaml schema declares. A field
// added to the A2A DTOs without the REST contract (or dropped from it) fails
// here.
func TestAnsweredObjectsCarryExactlyTheOpenAPIProperties(t *testing.T) {
	f := newFixture(t)
	w := newWire(t, f.deps())
	env, _ := w.skill(t, skillGetCart, map[string]any{"cartId": f.ready})
	cartDoc := asMap(t, asAny(t, env.Data.Cart))
	quote := asMap(t, cartDoc["deliveryQuote"])
	group := asMap(t, first(t, quote["groups"]))

	for schema, obj := range map[string]map[string]any{
		"Cart":              cartDoc,
		"CartLine":          asMap(t, first(t, cartDoc["lines"])),
		"Estimate":          asMap(t, cartDoc["estimate"]),
		"CheckoutReadiness": asMap(t, cartDoc["checkoutReadiness"]),
		"DeliverySelection": asMap(t, first(t, cartDoc["selections"])),
		"DeliveryQuote":     quote,
		"QuoteGroup":        group,
		"QuotedOffer":       asMap(t, first(t, group["offers"])),
		"TierUnavailable":   asMap(t, first(t, quote["unavailable"])),
	} {
		if got, want := sortedKeys(obj), openAPIProps(t, schema); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: A2A answers %v, apis/openapi.yaml declares %v", schema, got, want)
		}
	}
}

// Skill spans: one per KNOWN skill, carrying the a2a.skill attribute, none for
// an unknown skill.
func TestSkillSpans(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	f := newFixture(t)
	w := newWire(t, f.deps())
	w.skill(t, skillGetCart, map[string]any{"cartId": f.ready})
	w.skill(t, skillListShopperCarts, map[string]any{"shopperId": "shopper-1"})
	w.skill(t, "launch_rocket", map[string]any{})
	w.call(t, a2a.NewTextPart("not json"))

	var names []string
	for _, s := range rec.Ended() {
		names = append(names, s.Name())
		attrs := s.Attributes()
		if len(attrs) == 0 || string(attrs[0].Key) != "a2a.skill" {
			t.Errorf("span %q lacks the a2a.skill attribute: %v", s.Name(), attrs)
		}
	}
	want := []string{"a2a.skill get_cart", "a2a.skill list_shopper_carts"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("spans = %v, want %v (none for unknown skills or malformed requests)", names, want)
	}
}
