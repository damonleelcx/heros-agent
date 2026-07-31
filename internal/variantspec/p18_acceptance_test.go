package variantspec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/registry"
)

// P18 §8 — the acceptance gate.
//
// Six suites, each pinning a promise the phase made rather than re-testing a unit. Plus the evidence
// audit P17 established: a task marked `[x]` with a `(Test: TestSomething)` pointer is a CLAIM, and it is
// a claim a green build cannot check — if the named test was never written, nothing fails, the proof
// simply never runs, and the task reads as done forever.

// p18TasksPath is the plan under audit.
const p18TasksPath = "openspec/changes/p18-harness-strategy-optimization/tasks.md"

// TestHashParticipationSuite — task 8.1. The three hash facts, together, because each is only meaningful
// beside the others: a field that never moves the hash is decorative, one that always moves it breaks
// every golden vector, and the identity must be indistinguishable from absence.
func TestHashParticipationSuite(t *testing.T) {
	ctx := context.Background()
	regs := newFakeRegistries()
	regs.addHarness(t, "identity", "baseline", registry.StrategySingleShot, `{}`)
	regs.addHarness(t, "loop", "revise", "reflexion",
		`{"max_turns":3,"stop_condition":"max-turns","reflection_prompt":"redo"}`)

	hash := func(ref string) string {
		t.Helper()
		got, err := Resolve(ctx, resolveHarnessSpec(ref), testIR(), regs)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", ref, err)
		}
		return got.ConfigHash
	}

	bare, identity, loop := hash(""), hash("identity"), hash("loop")

	// 1. A harness-only change moves the hash.
	if loop == bare {
		t.Error("a reflexion harness hashes the same as no harness; two variants differing only in " +
			"scaffold would be one configuration, and the platform could never compare them")
	}
	// 2. A no-harness config is byte-identical to its pre-P18 form.
	if strings.Contains(mustCanonical(t, ctx, regs, ""), `"harness"`) {
		t.Error("a no-harness config emits a harness key; an always-present field changes the canonical " +
			"bytes of EVERY node in EVERY existing config and orphans every keyed row")
	}
	// 3. `single-shot` with no params is a no-op on the hash.
	if identity != bare {
		t.Errorf("an explicit single-shot changed the hash (%s vs %s); a user could not back out of an "+
			"authored harness change with no residue", identity, bare)
	}
}

func mustCanonical(t *testing.T, ctx context.Context, regs *fakeRegistries, ref string) string {
	t.Helper()
	got, err := Resolve(ctx, resolveHarnessSpec(ref), testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	b, err := got.Config.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	return string(b)
}

// TestHarnessScoredByExistingHarness — task 8.3. 🚫 P18 introduces no eval metric and no scoring change:
// the axis rides task_success / eval_cost_usd / eval_latency_ms unchanged.
//
// 🔴 Asserted as an ABSENCE, which is the only way to assert "we added nothing": the metric name set must
// contain the three the phase rides on and NOTHING harness-shaped. A phase that quietly added a
// `harness_turns` metric would be changing what the harness measures while claiming it did not.
func TestHarnessScoredByExistingHarness(t *testing.T) {
	for _, m := range []string{
		evalharness.MetricTaskSuccess, evalharness.MetricRunCostUSD, evalharness.MetricRunLatencyMS,
	} {
		if m == "" {
			t.Fatal("one of the three metrics the harness axis rides on is unnamed")
		}
	}
	if evalharness.MetricTaskSuccess != "task_success" ||
		evalharness.MetricRunCostUSD != "eval_cost_usd" ||
		evalharness.MetricRunLatencyMS != "eval_latency_ms" {
		t.Fatalf("a metric the admissibility gate reads was renamed: %s / %s / %s",
			evalharness.MetricTaskSuccess, evalharness.MetricRunCostUSD, evalharness.MetricRunLatencyMS)
	}

	// 🚫 No harness-specific metric was introduced. The source of truth is the metric-name file itself.
	b, err := os.ReadFile(filepath.Join("..", "evalharness", "metricnames.go"))
	if err != nil {
		t.Fatalf("read metricnames.go: %v", err)
	}
	for _, forbidden := range []string{"harness_", "scaffold_", "turn_count", "max_turns"} {
		if strings.Contains(string(b), `"`+forbidden) {
			t.Errorf("evalharness declares a metric containing %q; P18 promised no new metric and no "+
				"scoring change — the axis is scored because it lands in config_hash, not because the "+
				"harness learned about it", forbidden)
		}
	}
}

// TestHarnessComposesWithWiringNoReorder — task 8.6. The D-5 boundary, made mechanical: a group harness
// CONSUMES the wiring, so an edge the spec does not declare is rejected at validation. A harness that
// could invent an edge would be a second, divergent definition of what the executor walks.
func TestHarnessComposesWithWiringNoReorder(t *testing.T) {
	base := func() *VariantSpec {
		return &VariantSpec{
			SourceRevision: "rev1",
			Order:          []string{"n_a", "n_b"},
			Nodes:          map[string]NodeOverride{},
			Edges:          []Edge{{FromNodeID: "n_a", ToNodeID: "n_b", Kind: "data"}},
		}
	}

	t.Run("a group over a declared edge is valid", func(t *testing.T) {
		s := base()
		s.HarnessGroups = []HarnessGroup{{HarnessRef: "h1",
			Edges: []Edge{{FromNodeID: "n_a", ToNodeID: "n_b", Kind: "data"}}}}
		if err := s.Validate(); err != nil {
			t.Fatalf("a group over an edge the spec declares was rejected: %v", err)
		}
	})

	t.Run("a group that invents an edge is rejected", func(t *testing.T) {
		s := base()
		s.HarnessGroups = []HarnessGroup{{HarnessRef: "h1",
			Edges: []Edge{{FromNodeID: "n_b", ToNodeID: "n_a", Kind: "data"}}}}
		err := s.Validate()
		if err == nil {
			t.Fatal("a group harness declared an edge the graph does not have and was accepted. That edge " +
				"is a SECOND definition of what the executor walks, and it can drift from the wiring — " +
				"which is a rearrangement expressed through the wrong axis (P15 owns wiring)")
		}
		var se *SpecError
		if !errors.As(err, &se) || se.Dim != DimHarness {
			t.Fatalf("the rejection does not name the harness dimension: %v", err)
		}
	})

	t.Run("a group over no edges is rejected", func(t *testing.T) {
		s := base()
		s.HarnessGroups = []HarnessGroup{{HarnessRef: "h1"}}
		if err := s.Validate(); err == nil {
			t.Fatal("a group harness wrapping nothing was accepted; it would hash as a change that loops " +
				"over no edges")
		}
	})

	// 🚫 And a group never reorders: the ordering it validates against is the spec's own, and Validate
	// does not permit the group to state one.
	t.Run("a group cannot state an ordering", func(t *testing.T) {
		s := base()
		s.HarnessGroups = []HarnessGroup{{HarnessRef: "h1",
			Edges: []Edge{{FromNodeID: "n_a", ToNodeID: "n_b", Kind: "data"}}}}
		before := append([]string(nil), s.Order...)
		if err := s.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
		for i, id := range s.Order {
			if id != before[i] {
				t.Fatalf("validating a group harness changed the node ordering at %d (%q -> %q)", i, before[i], id)
			}
		}
	})
}

// TestP18AddsExactlyOneDimension pins the one-way door. The enum is closed; P18 opens it by exactly one,
// deliberately, through the eight-step checklist — and a second member would mean something else got in.
func TestP18AddsExactlyOneDimension(t *testing.T) {
	before := []Dimension{DimModel, DimPrompt, DimSkills, DimContext, DimTools, DimMemory}
	got := Dimensions()
	if len(got) != len(before)+1 {
		t.Fatalf("the dimension enum has %d members (%v); P18 adds exactly one (harness) to the %d that "+
			"existed. A different count means either the addition was missed or something else was added "+
			"without a decision record.", len(got), got, len(before))
	}
	for i, want := range before {
		if got[i] != want {
			t.Errorf("dimension %d is %q, want %q — P18 appends, it does not reorder", i, got[i], want)
		}
	}
	if got[len(got)-1] != DimHarness {
		t.Errorf("the last dimension is %q, want harness", got[len(got)-1])
	}
}

// TestP18HarnessFieldIsAdditiveEverywhere is the hash-compatibility door, checked at the STRUCT level
// rather than through a sample of values: every harness-carrying field must be omitempty, in all three
// shapes the axis touches. A tag typo (`omitempy`) reads correct and behaves wrong.
func TestP18HarnessFieldIsAdditiveEverywhere(t *testing.T) {
	cases := []struct{ what, tag string }{
		{"NodeOverride.HarnessRef", fieldTag(t, "NodeOverride", "HarnessRef")},
		{"ResolvedNode.Harness", fieldTag(t, "ResolvedNode", "Harness")},
		{"ResolvedConfig.HarnessGroups", fieldTag(t, "ResolvedConfig", "HarnessGroups")},
		{"VariantSpec.HarnessGroups", fieldTag(t, "VariantSpec", "HarnessGroups")},
	}
	for _, c := range cases {
		if !strings.Contains(c.tag, "omitempty") {
			t.Errorf("%s has json tag %q, which lacks omitempty. An always-present harness key changes the "+
				"canonical bytes of EVERY pre-P18 node, breaks every frozen golden vector, and orphans "+
				"every row keyed by a config_hash (decisions.md D-3).", c.what, c.tag)
		}
	}
}

// TestP18NamedEvidenceExists — the audit the memory of this repository asks for, applied to P18's plan.
//
// 🚫 It deliberately does NOT check that the named tests PASS. `go test ./...` does that, and a manifest
// that re-ran them would be a second, weaker test runner. What it catches is the one failure a green
// build cannot: a task marked done whose named proof was never written.
func TestP18NamedEvidenceExists(t *testing.T) {
	root := filepath.Join("..", "..")

	plan, err := os.ReadFile(filepath.Join(root, p18TasksPath))
	if err != nil {
		t.Fatalf("read the task plan: %v", err)
	}
	blocks := splitTasks(string(plan))
	if len(blocks) < 40 {
		t.Fatalf("parsed only %d task(s) from %s — parser drift, and every assertion below would pass "+
			"for the wrong reason", len(blocks), p18TasksPath)
	}

	declared := map[string]string{}
	err = filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, m := range funcDeclRe.FindAllStringSubmatch(string(b), -1) {
			declared[m[1]] = path
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}

	audited := 0
	for _, b := range blocks {
		if !b.done {
			// An OPEN task naming a test that does not exist is a plan, not a false claim.
			continue
		}
		for _, m := range goTestRefRe.FindAllStringSubmatch(b.text, -1) {
			name := m[1]
			audited++
			if _, ok := declared[name]; !ok {
				t.Errorf("task %s is marked DONE and names %s as its evidence, but no such test exists "+
					"under internal/.\nA named proof that was never written does not fail — it simply never "+
					"runs, so the task reads as done and is not.", b.id, name)
			}
		}
	}
	if audited < 15 {
		t.Errorf("only %d evidence pointer(s) were audited; the plan claims far more, so the pointer "+
			"pattern has drifted and this audit is no longer reading the plan", audited)
	}
}

// TestHarnessIdentityUntouchedSuite — task 13.4 🔴. The wave-18c gate's identity half: shipping a runtime
// and a rewriter changed what the transform EMITS, never what a configuration IS.
//
// 🔴 The conjunction it adds over TestHashParticipationSuite: it walks the WHOLE vocabulary, asserts each
// strategy's hash is stable across repeated resolution, that clearing any of them returns byte-exactly to
// the parent, and that no canonical form emits a harness key for a node that declares none. A phase that
// quietly started hashing a turn count or a trace would pass the earlier suite and fail this one.
func TestHarnessIdentityUntouchedSuite(t *testing.T) {
	ctx := context.Background()
	regs := newFakeRegistries()
	params := map[string]string{
		registry.StrategySingleShot: `{}`,
		"reflexion":                 `{"max_turns":3,"stop_condition":"max-turns","reflection_prompt":"redo"}`,
		"react-loop":                `{"max_turns":6,"stop_condition":"no-tool-call"}`,
		"plan-execute":              `{"max_turns":4,"stop_condition":"plan-complete"}`,
		"critic-loop":               `{"max_turns":3,"critic_model_ref":"deadbeef"}`,
	}
	for _, st := range registry.BuiltinHarnessStrategies() {
		regs.addHarness(t, "ref-"+st.Name(), st.Name(), st.Name(), params[st.Name()])
	}

	bare, err := Resolve(ctx, resolveHarnessSpec(""), testIR(), regs)
	if err != nil {
		t.Fatalf("resolve bare: %v", err)
	}

	for _, st := range registry.BuiltinHarnessStrategies() {
		t.Run(st.Name(), func(t *testing.T) {
			ref := "ref-" + st.Name()
			first, err := Resolve(ctx, resolveHarnessSpec(ref), testIR(), regs)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			// Stable across repeated resolution — no clock, no run state, nothing ambient.
			for i := 0; i < 5; i++ {
				again, err := Resolve(ctx, resolveHarnessSpec(ref), testIR(), regs)
				if err != nil {
					t.Fatalf("re-resolve: %v", err)
				}
				if again.ConfigHash != first.ConfigHash {
					t.Fatalf("the hash moved between two resolutions of one spec: %s -> %s",
						first.ConfigHash, again.ConfigHash)
				}
			}
			// The identity ≡ absent; everything else is its own configuration.
			isIdentity := st.Name() == registry.StrategySingleShot
			if isIdentity != (first.ConfigHash == bare.ConfigHash) {
				t.Fatalf("%s: hash-equals-bare is %v, want %v", st.Name(),
					first.ConfigHash == bare.ConfigHash, isIdentity)
			}
			// Clearing returns byte-exactly to the parent.
			cleared, err := Resolve(ctx, resolveHarnessSpec(""), testIR(), regs)
			if err != nil {
				t.Fatalf("resolve cleared: %v", err)
			}
			if cleared.ConfigHash != bare.ConfigHash {
				t.Fatalf("clearing left residue: %s vs %s", cleared.ConfigHash, bare.ConfigHash)
			}
			// 🚫 And no run-shaped fact reached the hashed projection.
			b, err := first.Config.Canonical()
			if err != nil {
				t.Fatalf("canonical: %v", err)
			}
			for _, forbidden := range []string{"turns", "stop_reason", "trace", "harness_ref"} {
				if strings.Contains(string(b), `"`+forbidden+`"`) {
					t.Errorf("the canonical bytes carry %q; turns, stop reasons and traces are properties of "+
						"a RUN, and hashing one gives a single configuration as many hashes as it had "+
						"outcomes:\n%s", forbidden, b)
				}
			}
		})
	}
}
