package evalharness

import (
	"reflect"
	"strings"
	"testing"
)

// P13 §4.3 — the eval harness stays axis-agnostic: it consumes only config_hash + Trace, and NO
// operator label reaches it. A P13 candidate is scored by the same harness as any other change, which
// is what makes its verdict comparable. This is also guaranteed structurally — evalharness cannot import
// internal/proposal (that would be an import cycle, since proposal depends on evalharness) — but this
// test pins the intent: no evaluator input surfaces an operator/proposal/candidate label, and the
// standard metric family names no axis.

func TestEvalRemainsAxisAgnostic(t *testing.T) {
	// The standard family the harness computes from a trace names no operator or axis.
	for _, m := range StandardFamily {
		low := strings.ToLower(m)
		for _, banned := range []string{"operator", "proposal", "candidate", "prompt_rewrite", "downgrade", "param_tune"} {
			if strings.Contains(low, banned) {
				t.Errorf("standard metric %q names an axis/operator (%q); the eval must stay axis-agnostic", m, banned)
			}
		}
	}

	// No evaluator input type carries an operator/proposal/candidate field. Compute(ctx, Trace, Case,
	// Target) is the whole surface an evaluator sees, so scanning these three types covers it.
	for _, typ := range []reflect.Type{reflect.TypeOf(Trace{}), reflect.TypeOf(Case{}), reflect.TypeOf(Target{})} {
		assertNoAxisField(t, typ, map[reflect.Type]bool{})
	}
}

// assertNoAxisField recursively checks that no field name in a struct type references a P5.5 axis
// concept (operator/proposal/candidate/diagnosis). Guards against cycles via the visited set.
func assertNoAxisField(t *testing.T, typ reflect.Type, visited map[reflect.Type]bool) {
	t.Helper()
	if typ == nil || visited[typ] {
		return
	}
	visited[typ] = true
	switch typ.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		assertNoAxisField(t, typ.Elem(), visited)
		return
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			low := strings.ToLower(f.Name)
			// "candidate" is deliberately NOT banned here: an eval Case can carry a gold-label candidate
			// ANSWER, which is unrelated to a P5.5 proposal candidate. The axis concepts that would betray
			// an operator label leaking in are these:
			for _, banned := range []string{"operator", "proposal", "diagnosis"} {
				if strings.Contains(low, banned) {
					t.Errorf("%s.%s references axis concept %q — an operator label reached the eval harness", typ.Name(), f.Name, banned)
				}
			}
			assertNoAxisField(t, f.Type, visited)
		}
	}
}

// ── P34 task 6.5 / G6 — the split added no eval, scorer, oracle or metric ───────────────────────

// TestP34AddedNoEvalSurface is task 6.5, and it is a fence rather than a sentence in a document.
//
// PRD G6 says the eval harness must not learn that `loop`, `harness` or `graph` exist, and §7.4 gives
// the reason in one line: **an axis needing a bespoke oracle is designed wrong.** A metric that scored
// "loop-ness" would be a number only comparable to itself, so a loop change and a prompt change could
// never be ranked against each other — which is the whole job of having one harness.
//
// The way this requirement fails is not a decision anybody records. It is a `MetricLoopTurns` added one
// afternoon because the number was easy to compute and somebody wanted it on a dashboard. So the ban is
// on the VOCABULARY, and it is checked against the shipped metric family and the shipped oracle set.
func TestP34AddedNoEvalSurface(t *testing.T) {
	// The three axis names, plus the words that would carry them into a metric under another spelling.
	banned := []string{
		"loop", "harness", "graph", "topology", "concurrent", "concurrency",
		"scaffold", "turn_count", "max_turns", "predicate", "fan_in", "envelope",
	}
	for _, m := range StandardFamily {
		low := strings.ToLower(m)
		for _, b := range banned {
			if strings.Contains(low, b) {
				t.Errorf("standard metric %q names the P34 concept %q. G6: the eval harness does not learn "+
					"that these axes exist, and §7.4 says why — an axis needing a bespoke oracle is designed "+
					"wrong, and a metric only comparable to itself makes a loop change and a prompt change "+
					"unrankable against each other.", m, b)
			}
		}
	}

	// 🔴 The reduction a loop or topology change produces must show up in the metrics that ALREADY
	// exist. If these three ever stop being in the family, P34's claim that its wins are visible in the
	// existing metric family stops being true and nothing else would say so.
	for _, want := range []string{MetricTaskSuccess} {
		found := false
		for _, m := range StandardFamily {
			if m == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is not in StandardFamily; P34 reports its wins through the existing metrics, and "+
				"this is one of them", want)
		}
	}
}

// TestP34AddedNoOracle — the same ban, applied to the oracle set. An oracle is where a bespoke scorer
// would actually live, and one named for an axis is the shape §7.4 rejects.
func TestP34AddedNoOracle(t *testing.T) {
	for _, e := range Builtins() {
		name := e.Name()
		low := strings.ToLower(name + " " + e.Metric())
		for _, b := range []string{"loop", "harness", "graph", "topology", "concurrent", "scaffold"} {
			if strings.Contains(low, b) {
				t.Errorf("oracle %q names the P34 concept %q; an axis needing a bespoke oracle is designed "+
					"wrong (§7.4)", name, b)
			}
		}
	}
}
