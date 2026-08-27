package telemetry

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A catalogue that drifts from the emitted taxonomy documents a metric nobody produces, or omits one
// somebody is already charting. Both are asserted here rather than reviewed.

// declaredMetricNames reads every `Metric… = "…"` constant out of `attributes.go`.
//
// # 🔴 Why this parses the SOURCE instead of ranging over the name lists
//
// It used to range over `OperationalMetricNames` and `RunScopedMetricNames`, and it was green while the
// catalogue was missing THIRTEEN of the twenty-nine metrics this package emits — every context-assembly
// metric, every sandbox and tool metric, and the whole revenue taxonomy.
//
// The reason is worth stating plainly, because the shape recurs: those two lists are hand-maintained,
// and so is the catalogue. A test that compares one hand-maintained list against another cannot see a
// metric that was added to NEITHER — which is exactly what happens when somebody declares a constant,
// emits it, and never touches either registry. The fence was checking the registers against each other
// while the thing they both describe grew past both.
//
// The constants are the declaration of what exists, so they are what the catalogue is checked against.
//
// A constant declared and deliberately never emitted would now fail here. That is the right failure: it
// forces a decision — catalogue it, or delete it — instead of leaving a name in the tree that nothing
// can document and nothing produces.
func declaredMetricNames(t *testing.T) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "attributes.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing attributes.go: %v", err)
	}
	out := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if !strings.HasPrefix(name.Name, "Metric") || i >= len(spec.Values) {
				continue
			}
			lit, ok := spec.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			out[value] = true
		}
		return true
	})
	// A parser that found nothing would make every assertion below pass for the wrong reason — the
	// failure mode this whole function exists to close, arriving one level down.
	if len(out) < 20 {
		t.Fatalf("parsed only %d metric constants from attributes.go; the declaration shape has drifted "+
			"and this test would pass on an empty catalogue", len(out))
	}
	return out
}

func TestCatalogCoversExactlyTheEmittedTaxonomy(t *testing.T) {
	emitted := declaredMetricNames(t)

	catalogued := map[string]bool{}
	for _, d := range MetricCatalog() {
		catalogued[d.Name] = true
	}

	var missing, extra []string
	for name := range emitted {
		if !catalogued[name] {
			missing = append(missing, name)
		}
	}
	for name := range catalogued {
		if !emitted[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) > 0 {
		t.Errorf("emitted but not catalogued: %v\n"+
			"A metric with no catalogue entry cannot be documented, and `scan-metric` will refuse any page "+
			"that mentions it.", missing)
	}
	if len(extra) > 0 {
		t.Errorf("catalogued but never emitted: %v\nThe documentation would describe a number nobody produces.", extra)
	}
}

func TestEveryDefinitionCitesItsUnitAndItsComputationSite(t *testing.T) {
	for _, d := range MetricCatalog() {
		if d.Unit == "" {
			t.Errorf("%s has no unit — a value without one is an incomplete payload and is rejected at emission", d.Name)
		}
		if d.Computation == "" {
			t.Errorf("%s has no computation — the name is not the definition", d.Name)
		}
		if !strings.Contains(d.ComputedIn, "internal/") {
			t.Errorf("%s cites %q, which is not a site a reader can open", d.Name, d.ComputedIn)
		}
		// 🔴 The cited FILE must exist. A citation that points nowhere is the same defect as no citation,
		// and it is the one a reader only discovers when they try to check the sentence they were asked
		// to trust. Paths are repository-relative and this test runs in `internal/telemetry`.
		for _, cited := range strings.Split(d.ComputedIn, ",") {
			path := strings.TrimSpace(cited)
			if i := strings.Index(path, ":"); i >= 0 {
				path = path[:i]
			}
			if i := strings.Index(path, " "); i >= 0 {
				path = path[:i]
			}
			if !strings.HasPrefix(path, "internal/") {
				continue
			}
			if _, err := os.Stat(filepath.Join("..", "..", path)); err != nil {
				t.Errorf("%s cites %q, which does not exist: %v", d.Name, path, err)
			}
		}
		switch d.Scope {
		case "call", "node", "run", "billing":
		default:
			t.Errorf("%s has scope %q, which is not one of call | node | run | billing", d.Name, d.Scope)
		}
	}
}
