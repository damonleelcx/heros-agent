package discovery

import (
	"encoding/json"
	"testing"
)

// P17 §6 — Step 6 of the "add an axis" checklist: the IR's per-node memory default.
//
// The axis's floor. Everything above it — resolve, hash, propose, refuse — merges an override onto what
// this file guarantees is always a CONCRETE base, so the resolver never has to invent one.

// TestIRNodeMemoryDefaultsToNone — task 6.1. Both directions of the default: a pre-P17 node with no key
// reads as `none`, and the field is additive so such a node serialises byte-identically.
func TestIRNodeMemoryDefaultsToNone(t *testing.T) {
	t.Run("an absent key reads as none", func(t *testing.T) {
		var n IRNode
		if got := n.MemoryDefault(); got != "none" {
			t.Fatalf("MemoryDefault() on a node with no memory key = %q, want none. A pre-P17 IR has no "+
				"key at all, and a caller comparing against \"\" would treat a current IR as unrecorded "+
				"while one comparing against \"none\" would treat an old one as unrecorded — hence one accessor", got)
		}
	})

	t.Run("a stated strategy is returned verbatim", func(t *testing.T) {
		n := IRNode{Memory: "summary-buffer"}
		if got := n.MemoryDefault(); got != "summary-buffer" {
			t.Fatalf("MemoryDefault() = %q, want summary-buffer", got)
		}
	})

	t.Run("the field is additive", func(t *testing.T) {
		// A hand-built node with no memory strategy must emit NO memory key, so a pre-P17 document and a
		// hand-constructed one round-trip identically. (The EMITTER writes a concrete `none` — see
		// TestDiscoveryEmitsMemoryDefault — which is a different guarantee: what discovery FOUND.)
		b, err := json.Marshal(IRNode{NodeID: "n", Kind: "static_definition"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if contains(string(b), `"memory"`) {
			t.Fatalf("a node with no memory strategy emitted a memory key: %s", b)
		}
	})
}

// TestDiscoveryEmitsMemoryDefault — task 6.2. The emitter writes a CONCRETE value into every node, so no
// node in an emitted IR is silent about memory and the resolver merges onto a stated base.
func TestDiscoveryEmitsMemoryDefault(t *testing.T) {
	t.Run("every emitted node resolves to a concrete strategy", func(t *testing.T) {
		ir := BuildIR(
			IRWorkflow{ID: "wf", Language: "go", Repo: IRRepo{URL: "u", CommitSHA: "c"}},
			[]ExtractedNode{
				{NodeID: "n_a", Memory: "none"},
				// A node whose frontend derived nothing at all. It must still read as `none` — the
				// resolver never merges an override onto an unknown base.
				{NodeID: "n_b"},
				// A stated non-default strategy survives verbatim, so the field is live rather than
				// decorative the day a detector can populate it.
				{NodeID: "n_c", Memory: "summary-buffer"},
			},
			nil,
		)
		if len(ir.Nodes) != 3 {
			t.Fatalf("BuildIR emitted %d nodes, want 3", len(ir.Nodes))
		}
		want := map[string]string{"n_a": "none", "n_b": "none", "n_c": "summary-buffer"}
		for _, n := range ir.Nodes {
			if got := n.MemoryDefault(); got != want[n.NodeID] {
				t.Errorf("node %s reads memory %q, want %q", n.NodeID, got, want[n.NodeID])
			}
		}
	})

	t.Run("the default costs no bytes", func(t *testing.T) {
		// 🔴 The golden IR fixture is the guard this protects: `none` is written as ABSENCE, so a pre-P17
		// document and a current one that found no memory strategy serialise identically. Emitting
		// `"memory":"none"` on every node would churn every stored IR to say something MemoryDefault()
		// already says, and would make the golden diff noise rather than signal.
		ir := BuildIR(IRWorkflow{ID: "wf", Language: "go", Repo: IRRepo{URL: "u", CommitSHA: "c"}},
			[]ExtractedNode{{NodeID: "n_a", Memory: "none"}}, nil)
		b, err := MarshalIR(ir)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if contains(string(b), `"memory"`) {
			t.Fatalf("a `none` node emitted a memory key:\n%s", b)
		}

		nonDefault, err := MarshalIR(BuildIR(IRWorkflow{ID: "wf", Language: "go", Repo: IRRepo{URL: "u", CommitSHA: "c"}},
			[]ExtractedNode{{NodeID: "n_a", Memory: "vector-recall"}}, nil))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !contains(string(nonDefault), `"memory": "vector-recall"`) {
			t.Fatalf("a non-default strategy was not emitted:\n%s", nonDefault)
		}
	})

	t.Run("the frontend derives none, not a guess", func(t *testing.T) {
		// 🚫 The honest floor. A frontend that guessed `vector-recall` from a nearby import would produce
		// a node that resolves, hashes, and is compared as a configuration the source never had.
		for _, n := range []ExtractedNode{
			{NodeID: "n", ToolsSkills: []string{"search_kb"}},
			{NodeID: "n", Skills: []string{"recall"}},
			{NodeID: "n"},
		} {
			if got := deriveMemory(n); got != "none" {
				t.Errorf("deriveMemory returned %q; a cross-invocation store is not visible at a single "+
					"call site, so anything but `none` here is a guess", got)
			}
		}
	})

	t.Run("a detect-only node states none rather than unresolved", func(t *testing.T) {
		// Its context is "unresolved" because context IS statically resolvable and was skipped. Memory is
		// `none` because it was never statically resolvable at all — a different fact, spelled differently.
		pf, err := parseSingle("github.com/acme/app/internal/svc", "svc.go", "package svc\n")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		n := extractOne(DetectedCallSite{NodeID: "n_d", DetectOnly: true, File: pf}, callMeta{})
		if n.Memory != "none" {
			t.Errorf("a detect-only node's memory = %q, want none. `unresolved` would imply a resolvable "+
				"fact was skipped, and would leave the resolver without a concrete base", n.Memory)
		}
		if n.Context.Policy != "unresolved" {
			t.Errorf("a detect-only node's context policy = %q, want unresolved — the two axes report "+
				"differently here on purpose", n.Context.Policy)
		}
	})
}

// TestMemoryDefaultDeterministic — task 6.3. The same target at the same revision emits the same memory
// defaults. Determinism is the IR's core invariant (I3), and a new field is a new way to break it.
func TestMemoryDefaultDeterministic(t *testing.T) {
	nodes := []ExtractedNode{{NodeID: "n_b"}, {NodeID: "n_a", Memory: "none"}, {NodeID: "n_c"}}
	wf := IRWorkflow{ID: "wf", Language: "go", Repo: IRRepo{URL: "u", CommitSHA: "c"}}

	first, err := MarshalIR(BuildIR(wf, nodes, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := MarshalIR(BuildIR(wf, nodes, nil))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(again) != string(first) {
			t.Fatalf("run %d emitted different bytes than run 0; the IR must be byte-stable across runs "+
				"of the same target at the same revision (invariant I3)", i+1)
		}
	}

	// And it is stable against the ORDER the frontend happened to find the nodes in — BuildIR sorts, so a
	// different traversal order is not a different document.
	shuffled := []ExtractedNode{{NodeID: "n_c"}, {NodeID: "n_a", Memory: "none"}, {NodeID: "n_b"}}
	other, err := MarshalIR(BuildIR(wf, shuffled, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(other) != string(first) {
		t.Fatalf("a different extraction order produced different IR bytes; node order is normalized, so " +
			"the memory defaults must not reintroduce order-dependence")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
