package telemetry

import (
	"sort"
	"strings"
	"testing"
)

// A catalogue that drifts from the emitted taxonomy documents a metric nobody produces, or omits one
// somebody is already charting. Both are asserted here rather than reviewed.

func TestCatalogCoversExactlyTheEmittedTaxonomy(t *testing.T) {
	emitted := map[string]bool{}
	for _, name := range OperationalMetricNames {
		emitted[name] = true
	}
	for _, name := range RunScopedMetricNames {
		emitted[name] = true
	}

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
		switch d.Scope {
		case "call", "run", "billing":
		default:
			t.Errorf("%s has scope %q, which is not one of call | run | billing", d.Name, d.Scope)
		}
	}
}
