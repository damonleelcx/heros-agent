package discovery

import (
	"testing"
)

// P18 §4 — the discovered harness default (Step 6 of the "add an axis" checklist).
//
// The through-line is the same one the memory field next door follows: the IR records a CONCRETE base for
// every node so the resolver never merges an override onto an unknown, and the default costs no bytes so
// a pre-P18 document keeps reproducing. What differs is the argument for the floor — see
// TestDiscoveredHarnessDefaultsSingleShot's last subtest, which is the whole reason this field says
// `single-shot` everywhere today.

// TestIRHarnessFieldAdditive — task 4.2. Both directions of the default: a pre-P18 node with no key must
// read as `single-shot`, and a node with no harness must emit no key.
func TestIRHarnessFieldAdditive(t *testing.T) {
	t.Run("an absent key reads as the identity", func(t *testing.T) {
		var n IRNode
		if got := n.HarnessDefault(); got != "single-shot" {
			t.Fatalf("HarnessDefault() on a node with no harness key = %q, want single-shot. A pre-P18 IR "+
				"has no such key, and a resolver that read %q as 'unknown' would have no base to merge onto",
				got, got)
		}
	})

	t.Run("a stated strategy survives", func(t *testing.T) {
		n := IRNode{Harness: "react-loop"}
		if got := n.HarnessDefault(); got != "react-loop" {
			t.Fatalf("HarnessDefault() = %q, want react-loop — the field must be live rather than "+
				"decorative the day a detector can populate it", got)
		}
	})

	t.Run("the identity emits no key", func(t *testing.T) {
		ir := BuildIR(IRWorkflow{ID: "wf", Language: "go", Repo: IRRepo{URL: "u", CommitSHA: "c"}},
			[]ExtractedNode{{NodeID: "n_a", Harness: "single-shot"}}, nil)
		b, err := MarshalIR(ir)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if contains(string(b), `"harness"`) {
			t.Fatalf("a single-shot node emitted a harness key:\n%s\nThe default is written as ABSENCE so a "+
				"pre-P18 document and a current one that proved no loop serialise byte-identically; emitting "+
				"it on every node would churn every stored IR to say what HarnessDefault() already says", b)
		}
	})

	t.Run("a non-default strategy is emitted", func(t *testing.T) {
		b, err := MarshalIR(BuildIR(IRWorkflow{ID: "wf", Language: "go", Repo: IRRepo{URL: "u", CommitSHA: "c"}},
			[]ExtractedNode{{NodeID: "n_a", Harness: "plan-execute"}}, nil))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !contains(string(b), `"harness": "plan-execute"`) {
			t.Fatalf("a non-default strategy was not emitted:\n%s", b)
		}
	})

	t.Run("the accessor and its inverse agree", func(t *testing.T) {
		// 🔴 If one learned a new default and the other did not, a node would round-trip into a different
		// scaffold than it was emitted with — silently, and a silently-changed scaffold is a
		// silently-changed bill.
		for _, s := range []string{"single-shot", "react-loop", "plan-execute", "reflexion", "critic-loop"} {
			n := IRNode{Harness: omitDefaultHarness(s)}
			if got := n.HarnessDefault(); got != s {
				t.Errorf("round trip of %q produced %q", s, got)
			}
		}
	})
}

// TestDiscoveredHarnessDefaultsSingleShot — task 4.1. Every emitted node reads as a concrete scaffold, and
// the frontend's floor is the identity rather than a guess.
func TestDiscoveredHarnessDefaultsSingleShot(t *testing.T) {
	t.Run("every emitted node resolves to a concrete strategy", func(t *testing.T) {
		ir := BuildIR(
			IRWorkflow{ID: "wf", Language: "go", Repo: IRRepo{URL: "u", CommitSHA: "c"}},
			[]ExtractedNode{
				{NodeID: "n_a", Harness: "single-shot"},
				// A node whose frontend derived nothing at all. It must still read as the identity — the
				// resolver never merges an override onto an unknown base.
				{NodeID: "n_b"},
				{NodeID: "n_c", Harness: "reflexion"},
			},
			nil,
		)
		want := map[string]string{"n_a": "single-shot", "n_b": "single-shot", "n_c": "reflexion"}
		for _, n := range ir.Nodes {
			if got := n.HarnessDefault(); got != want[n.NodeID] {
				t.Errorf("node %s reads harness %q, want %q", n.NodeID, got, want[n.NodeID])
			}
		}
	})

	// 🔴 The argument for the floor, asserted rather than left in a comment. The tempting signal is right
	// there — InvocationSemantics already records `loop` when the call sits inside one — and it is the
	// WRONG signal: a `for` over a list of tickets fires one node many times with no scaffold at all,
	// while an agent loop is the MODEL choosing to take another turn. Loop depth cannot tell them apart,
	// and emitting `react-loop` because a call sat in a `for` would hash a configuration nobody authored.
	t.Run("a call inside a loop is still single-shot", func(t *testing.T) {
		src := `package svc

func run(items []string) {
	for range items {
		_ = call()
	}
}

func call() string { return "" }
`
		pf, err := parseSingle("github.com/acme/app/internal/svc", "svc.go", src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		n := extractOne(DetectedCallSite{NodeID: "n_loop", DetectOnly: true, File: pf}, callMeta{loopDepth: 1})
		if n.Invocation.Type != "loop" {
			t.Fatalf("test setup: the call was not recorded as being inside a loop (%q)", n.Invocation.Type)
		}
		if n.Harness != "single-shot" {
			t.Fatalf("a call site inside a `for` derived harness %q; loop DEPTH is not evidence of an "+
				"agent loop, and treating it as such would report a fan-out as a scaffold — a configuration "+
				"the source never had", n.Harness)
		}
	})

	t.Run("the frontend derives the identity, not a guess", func(t *testing.T) {
		for _, n := range []ExtractedNode{
			{NodeID: "n", ToolsSkills: []string{"search_kb"}},
			{NodeID: "n", Skills: []string{"plan"}},
			{NodeID: "n", Invocation: InvocationSemantics{Type: "loop", VariableAtRuntime: true}},
			{NodeID: "n"},
		} {
			if got := deriveHarness(n); got != "single-shot" {
				t.Errorf("deriveHarness returned %q; a model-decided turn is not visible from the static "+
					"shape of a single call site, so anything but the identity here is a guess", got)
			}
		}
	})

	t.Run("determinism", func(t *testing.T) {
		// The IR's core invariant (I3), and a new field is a new way to break it.
		nodes := []ExtractedNode{{NodeID: "n_a"}, {NodeID: "n_b", Harness: "reflexion"}}
		first, err := MarshalIR(BuildIR(IRWorkflow{ID: "wf", Language: "go", Repo: IRRepo{URL: "u", CommitSHA: "c"}}, nodes, nil))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for i := 0; i < 5; i++ {
			again, err := MarshalIR(BuildIR(IRWorkflow{ID: "wf", Language: "go", Repo: IRRepo{URL: "u", CommitSHA: "c"}}, nodes, nil))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(again) != string(first) {
				t.Fatalf("the same nodes emitted different IR on run %d", i)
			}
		}
	})
}
