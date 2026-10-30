package architecture

// A2A inbound adapter fitness (ADR 0006). The adapter is additive, never
// load-bearing, and READ-ONLY:
//
//   - TestA2AAdapterDependencyRule: internal/adapters/inbound/a2a depends only
//     on the application and domain layers, and nothing else in internal/
//     depends on it (cmd/a2a wires it and nothing else touches it).
//   - TestA2ASurfaceIsReadOnly: an ALLOWLIST (not a denylist of write names, so
//     a new write use case is refused by default) of the use cases and ports
//     the adapter and cmd/a2a may reference, and no import of a write-side or
//     sibling-calling package.
//   - TestA2ASensorBites proves the detector flags each violation shape from
//     code fixtures, so the sensor cannot pass vacuously.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

func TestA2AAdapterDependencyRule(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)

	t.Run("a2a adapter depends only on application and domain", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.adapters.inbound.a2a.**",
					ShouldOnlyDependsOn: &configuration.Dependencies{
						Internal: []string{
							"**.internal.adapters.inbound.a2a.**",
							"**.internal.application.**",
							"**.internal.domain.**",
						},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})

	t.Run("nothing else depends on the a2a adapter", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.**",
					ShouldNotDependsOn: &configuration.Dependencies{
						Internal: []string{"**.internal.adapters.inbound.a2a.**"},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})
}

// a2aAllowedRefs is the whole set of `usecases.X` and `ports.X` identifiers the
// A2A sources may reference: the two read use cases the REST GETs call, the Deps
// bundle that carries their ports (data, not behaviour) and the not-found
// sentinel the problem mapping reads. Everything else (every write use case,
// the outbox, the unit of work, the quoter, the id generator, the inbox) is
// refused until this list is deliberately widened by an ADR.
var a2aAllowedRefs = map[string]bool{
	"usecases.GetCart":   true,
	"usecases.ListCarts": true,
	"usecases.Deps":      true,
	"ports.ErrNotFound":  true,
}

// a2aForbiddenImports are packages the A2A sources (adapter and composition
// root) must never import: an outbound A2A client (no calls to a sibling
// context), the write-side and event adapters, and the other inbound adapters.
var a2aForbiddenImports = []string{
	"a2a-go/v2/a2aclient",
	"/internal/adapters/outbound/kafka",
	"/internal/adapters/outbound/outbox",
	"/internal/adapters/outbound/deliverypromise",
	"/internal/adapters/outbound/memory",
	"/internal/adapters/inbound/kafka",
	"/internal/adapters/inbound/scheduler",
	"/internal/adapters/inbound/http",
}

// a2aViolations parses one Go source and returns every reference outside the
// allowlist and every forbidden import. Parsing without comments means a doc
// comment that merely names a write use case is not a violation.
func a2aViolations(filename string, src any) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		for _, bad := range a2aForbiddenImports {
			if strings.HasSuffix(path, bad) || strings.Contains(path, bad+"/") {
				out = append(out, fmt.Sprintf("%s imports %s", fset.Position(imp.Pos()), path))
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || (pkg.Name != "usecases" && pkg.Name != "ports") {
			return true
		}
		if ref := pkg.Name + "." + sel.Sel.Name; !a2aAllowedRefs[ref] {
			out = append(out, fmt.Sprintf("%s references %s", fset.Position(sel.Pos()), ref))
		}
		return true
	})
	sort.Strings(out)
	return out, nil
}

func TestA2ASurfaceIsReadOnly(t *testing.T) {
	var files []string
	for _, root := range []string{"../adapters/inbound/a2a", "../../cmd/a2a"} {
		files = append(files, goFilesUnder(t, root, false)...)
	}
	if len(files) < 4 {
		t.Fatalf("found %d A2A sources %v; the sensor is looking in the wrong place", len(files), files)
	}
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		violations, err := a2aViolations(path, src)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, v := range violations {
			t.Errorf("A2A surface must stay read-only (ADR 0006): %s", v)
		}
	}
}

func TestA2ASensorBites(t *testing.T) {
	cases := map[string]string{
		"a write use case":             "package a2a\nimport \"x/internal/application/usecases\"\nvar _ = usecases.AddLine{}\n",
		"another write use case":       "package a2a\nimport \"x/internal/application/usecases\"\nvar _ usecases.CreateCart\n",
		"a consumer":                   "package a2a\nimport \"x/internal/application/usecases\"\nvar _ usecases.Consumer\n",
		"the unit of work":             "package a2a\nimport \"x/internal/application/ports\"\nvar _ ports.UnitOfWork\n",
		"the outbox port":              "package a2a\nimport \"x/internal/application/ports\"\nvar _ ports.EventOutbox\n",
		"the quoter port":              "package a2a\nimport \"x/internal/application/ports\"\nvar _ ports.QuoteOffers\n",
		"an outbound a2a client":       "package a2a\nimport _ \"github.com/a2aproject/a2a-go/v2/a2aclient\"\n",
		"the kafka writer":             "package main\nimport _ \"x/internal/adapters/outbound/kafka\"\n",
		"the outbox relay":             "package main\nimport _ \"x/internal/adapters/outbound/outbox\"\n",
		"the delivery-promise gateway": "package main\nimport _ \"x/internal/adapters/outbound/deliverypromise\"\n",
		"the memory doubles":           "package main\nimport _ \"x/internal/adapters/outbound/memory\"\n",
		"the REST adapter":             "package main\nimport _ \"x/internal/adapters/inbound/http\"\n",
		"a scheduler":                  "package main\nimport _ \"x/internal/adapters/inbound/scheduler\"\n",
	}
	for name, src := range cases {
		got, err := a2aViolations("fixture.go", src)
		if err != nil || len(got) == 0 {
			t.Errorf("the sensor does not flag %s (violations %v, err %v)", name, got, err)
		}
	}

	clean := "package a2a\n// usecases.AddLine in a comment is not a reference.\n" +
		"import (\n\t\"x/internal/application/ports\"\n\t\"x/internal/application/usecases\"\n)\n" +
		"var _ = usecases.GetCart{}\nvar _ usecases.ListCarts\nvar _ usecases.Deps\nvar _ = ports.ErrNotFound\n"
	if got, err := a2aViolations("clean.go", clean); err != nil || len(got) != 0 {
		t.Errorf("false positive on the read use cases: %v (err %v)", got, err)
	}
}
