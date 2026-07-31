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
