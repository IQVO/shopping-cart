package mcp_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// update regenerates testdata/tool_registry.golden.json:
//
//	go test ./internal/adapters/inbound/mcp -run TestToolRegistryGolden -update
var update = flag.Bool("update", false, "rewrite the MCP tool registry golden file")

// wantTools is the curated, READ-ONLY tool set (docs/adr/0009): one tool per
// read GET of apis/openapi.yaml.
var wantTools = []string{"get_cart", "list_shopper_carts"}

// maxTools is the tool budget. Raising it is a deliberate, reviewed act (an
// amendment of ADR 0009): TestToolSurface still pins the exact curated set.
const maxTools = 2

// writeVerbs are the verbs of this service's write use cases (create a cart,
// add, set or remove a line, set the destination, quote, select, merge,
// accept requoted offers) and the generic mutators. A tool whose name
// contains one of them as a word is a write tool, which the read-only MCP
// surface must never expose: writes go through REST, the transactional outbox
// and the events.
var writeVerbs = []string{
	"create", "add", "set", "remove", "quote", "select", "merge", "accept", "checkout", "abandon",
	"update", "delete", "put", "post", "apply", "record", "register", "declare", "activate",
	"retire", "assign", "define", "open", "close", "confirm", "cancel", "purge", "sweep",
}

// writeVerbViolations returns one message per tool name carrying a write
// verb as one of its snake_case words.
func writeVerbViolations(names []string) []string {
	var out []string
	for _, name := range names {
		for _, word := range strings.Split(name, "_") {
			for _, verb := range writeVerbs {
				if word == verb {
					out = append(out, name+": contains the write verb "+verb+" (the MCP surface is read-only, docs/adr/0009)")
				}
			}
		}
	}
	return out
}

// writeSeams are the identifiers the adapter must never reference: every
// write use case of the application, the consumers and policies, and the
// transactional seams they run through. Importing `ports` itself is fine
// (the read error sentinel lives there); these names are not (docs/adr/0009).
var writeSeams = []string{
	"CreateCart", "AddLine", "SetLineQuantity", "RemoveLine", "SetDestination", "QuoteDelivery",
	"SelectDelivery", "MergeInto", "AcceptRequotedOffers", "AbandonInactive", "PurgeAbandonedCarts",
	"UnitOfWork", "EventOutbox", "Outbox", "Inbox", "QuoteOffers", "IDGenerator",
	"IdempotencyStore", "NewUnitOfWork", "NewOutbox",
}

// seamViolations returns one message per forbidden identifier source finds in
// src.
func seamViolations(src, name string) []string {
	var out []string
	for _, seam := range writeSeams {
		if regexp.MustCompile(`\b` + seam + `\b`).MatchString(src) {
			out = append(out, name+" references "+seam+", a write seam (the MCP surface is read-only, docs/adr/0009)")
		}
	}
	return out
}

// adapterSources returns every non-test Go source of the adapter.
func adapterSources(t *testing.T) []struct{ name, src string } {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []struct{ name, src string }
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, struct{ name, src string }{e.Name(), string(src)})
	}
	if len(out) == 0 {
		t.Fatal("no non-test sources found in the adapter: the sensor is looking in the wrong place")
	}
	return out
}

// The MCP governance charter's mechanical gate: the advertised tool set is
// exactly the curated read-only one, within the budget, snake_case verb_noun
// with NO write verb, annotated read-only, described, and every argument is
// lowerCamelCase (like the REST contract) and documented.
func TestToolSurface(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) > maxTools {
		t.Fatalf("advertised %d tools, over the budget of %d", len(res.Tools), maxTools)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		checkToolMetadata(t, tool)
		checkToolArguments(t, tool)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(wantTools, ",") {
		t.Fatalf("advertised tools %v, want exactly %v", names, wantTools)
	}
	for _, v := range writeVerbViolations(names) {
		t.Error(v)
	}
}

// The write-verb sensor must actually be able to fail: every write verb in a
// tool name is flagged, read names are not.
func TestWriteVerbSensorFailsOnWriteNames(t *testing.T) {
	for _, verb := range writeVerbs {
		for _, name := range []string{verb + "_cart", "bulk_" + verb + "_carts"} {
			if len(writeVerbViolations([]string{name})) == 0 {
				t.Errorf("write tool name %q was not flagged", name)
			}
		}
	}
	if got := writeVerbViolations(wantTools); len(got) != 0 {
		t.Errorf("read tool names were flagged: %v", got)
	}
	// Substrings of a word are not verbs: "selection" is not "select" and
	// "quotes" is not "quote".
	if got := writeVerbViolations([]string{"get_selection_state", "list_quotes_of"}); len(got) != 0 {
		t.Errorf("false positive on non-verb words: %v", got)
	}
}

// The write-seam sensor must be able to fail too, and must not flag the
// adapter's own read-only sources.
func TestWriteSeamSensorFailsOnWriteSeams(t *testing.T) {
	if got := seamViolations("d.UnitOfWork.Do(ctx", "fixture.go"); len(got) != 1 {
		t.Errorf("a transactional seam was not flagged: %v", got)
	}
	if got := seamViolations("_, err := usecases.AddLine{Deps: d}.Execute(ctx, id, sku, 1)", "fixture.go"); len(got) != 1 {
		t.Errorf("a write use case was not flagged: %v", got)
	}
	if got := seamViolations("err := ports.ErrNotFound", "errors.go"); len(got) != 0 {
		t.Errorf("the read sentinel import was flagged: %v", got)
	}
	for _, src := range adapterSources(t) {
		for _, v := range seamViolations(src.src, src.name) {
			t.Error(v)
		}
	}
}

var (
	toolNaming = regexp.MustCompile(`^[a-z]+(_[a-z]+)+$`)
	argNaming  = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)
)

func checkToolMetadata(t *testing.T, tool *sdk.Tool) {
	t.Helper()
	if !toolNaming.MatchString(tool.Name) {
		t.Errorf("tool %q is not snake_case verb_noun", tool.Name)
	}
	if tool.Description == "" {
		t.Errorf("tool %q has no description", tool.Name)
	}
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Errorf("tool %q is not annotated read-only: %+v", tool.Name, tool.Annotations)
		return
	}
	if tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint {
		t.Errorf("tool %q is annotated destructive", tool.Name)
	}
}

func checkToolArguments(t *testing.T, tool *sdk.Tool) {
	t.Helper()
	schema, isMap := tool.InputSchema.(map[string]any)
	if !isMap {
		t.Errorf("tool %q input schema = %T", tool.Name, tool.InputSchema)
		return
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		t.Errorf("tool %q has no arguments in its schema: every REST GET of this context takes an id or a shopper", tool.Name)
	}
	for arg, p := range props {
		if !argNaming.MatchString(arg) {
			t.Errorf("tool %q argument %q is not lowerCamelCase", tool.Name, arg)
		}
		if d, _ := p.(map[string]any)["description"].(string); d == "" {
			t.Errorf("tool %q argument %q has no description", tool.Name, arg)
		}
	}
}

// TestToolRegistryGolden pins the whole advertised registry (names,
// descriptions, annotations, authority _meta, input AND output schemas) byte
// for byte, so any change to what an MCP client sees shows up as a reviewed
// golden diff.
func TestToolRegistryGolden(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	sort.Slice(res.Tools, func(i, j int) bool { return res.Tools[i].Name < res.Tools[j].Name })
	got, err := json.MarshalIndent(res.Tools, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "tool_registry.golden.json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("the MCP tool registry drifted from %s; review the change and re-run with -update.\ngot:\n%s", path, got)
	}
}

// Every optional input is published as optional: a model that leaves the
// state filter or the limit out must not be rejected by schema validation
// (the cartId and the shopperId ARE required, like the REST path parameter
// and the REST query parameter).
func TestOptionalInputsAreNotRequired(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	want := map[string]string{"get_cart": "cartId", "list_shopper_carts": "shopperId"}
	for _, tool := range res.Tools {
		schema := tool.InputSchema.(map[string]any)
		req, _ := schema["required"].([]any)
		var names []string
		for _, r := range req {
			if s, ok := r.(string); ok {
				names = append(names, s)
			}
		}
		sort.Strings(names)
		if strings.Join(names, ",") != want[tool.Name] {
			t.Fatalf("%s requires %v; only %s is required", tool.Name, names, want[tool.Name])
		}
	}
}
