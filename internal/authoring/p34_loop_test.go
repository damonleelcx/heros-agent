package authoring

import (
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P34 task 3.7 / FR9 — new authoring writes a loop entry and never a loop-bearing harness entry.

// TestNewAuthoringCannotCreateALoopBearingHarnessEntry is the fence, and it reads the SURFACES rather
// than the intentions.
//
// 🔴 The claim it defends is structural: there must be no control on any authoring surface whose result
// is a harness entry naming one of the five loop strategies. Not "we agreed not to" — no path. So the
// test asserts the two option lists are DISJOINT, and that the harness axis offers exactly the envelope.
//
// If this goes red, the two vocabularies have started to overlap somewhere, and the day after that a user
// authors a legacy entry through a surface built after the legacy shape was retired.
func TestNewAuthoringCannotCreateALoopBearingHarnessEntry(t *testing.T) {
	envelopeStrategies := map[string]bool{}
	for _, o := range EnvelopeOptions() {
		envelopeStrategies[o.Strategy] = true
	}
	if len(envelopeStrategies) != 1 || !envelopeStrategies[registry.StrategyEnvelope] {
		t.Fatalf("the harness axis offers %v; it must offer exactly the execution envelope, because every "+
			"other member of that vocabulary is the LEGACY loop-bearing shape", envelopeStrategies)
	}
	for _, o := range HarnessStrategyOptions() {
		if envelopeStrategies[o.Strategy] {
			t.Errorf("the control-loop picker offers %q, which is also offered on the harness axis; the "+
				"two vocabularies must be disjoint at the point of authoring or a user can reach the legacy "+
				"shape by picking the wrong list", o.Strategy)
		}
		if !registry.IsLoopStrategy(o.Strategy) {
			t.Errorf("the control-loop picker offers %q, which is not a loop strategy", o.Strategy)
		}
	}
	// And every loop strategy is reachable — otherwise the disjointness above could be satisfied by a
	// picker that offers nothing at all.
	offered := map[string]bool{}
	for _, o := range HarnessStrategyOptions() {
		offered[o.Strategy] = true
	}
	for _, n := range registry.LoopStrategyNames() {
		if !offered[n] {
			t.Errorf("loop strategy %q is not offered by any picker; a strategy a spec can reference and "+
				"nobody can author is a vocabulary entry with no way in", n)
		}
	}
}

// TestLoopEditTouchesOnlyTheLoopDimension — an edit that reported the wrong dimension would file the
// authoring record under the wrong axis, which is the one thing the split exists to get right.
func TestLoopEditTouchesOnlyTheLoopDimension(t *testing.T) {
	dims := LoopEdit("l-1").Dimensions()
	if len(dims) != 1 || dims[0] != variantspec.DimLoop {
		t.Fatalf("LoopEdit touches %v, want exactly [loop]", dims)
	}
	dims = EnvelopeEdit("h-1").Dimensions()
	if len(dims) != 1 || dims[0] != variantspec.DimHarness {
		t.Fatalf("EnvelopeEdit touches %v, want exactly [harness]", dims)
	}
	if LoopDimension != "loop" || EnvelopeDimension != "harness" {
		t.Fatalf("the exported dimension labels are %q/%q", LoopDimension, EnvelopeDimension)
	}
}

// TestClearingALoopLeavesNoResidue — FR43's rule, applied to the new axis. A select-then-clear must
// return to the pre-selection bytes, or a user who changed their mind carries a permanent hash change.
func TestClearingALoopLeavesNoResidue(t *testing.T) {
	before := variantspec.NodeOverride{ModelRef: "m-1"}
	set := applyEdit(before, LoopEdit("l-1"))
	if set.LoopRef != "l-1" {
		t.Fatalf("the edit did not set loop_ref: %+v", set)
	}
	cleared := applyEdit(set, ClearLoopEdit())
	if !reflect.DeepEqual(cleared, before) {
		t.Fatalf("clearing left residue:\n  before: %+v\n   after: %+v\n"+
			"`loop_ref` is omitempty, so the empty string must remove the key rather than write an empty "+
			"one — otherwise the cleared spec hashes differently from the one that never selected",
			before, cleared)
	}
}

// TestEnvelopeOptionStatesWhatItImposes — the surface must say that an envelope imposes rather than
// chooses, because a reader who thinks it chooses will look for their turn count on the wrong page.
func TestEnvelopeOptionStatesWhatItImposes(t *testing.T) {
	o := EnvelopeOptions()[0]
	if o.Title == "" || o.Description == "" || o.PolicyNote == "" {
		t.Fatal("the envelope option has no human labels; a surface would have to invent them")
	}
	if !strings.Contains(strings.ToLower(o.PolicyNote), "impose") {
		t.Errorf("the policy note does not say the envelope IMPOSES: %s", o.PolicyNote)
	}
	// The three blast-radius statements are required, read off the schema rather than re-listed.
	want := map[string]bool{"sandbox_posture": true, "turn_ceiling": true, "spend_ceiling_usd": true}
	for _, r := range o.Required {
		delete(want, r)
	}
	if len(want) != 0 {
		t.Errorf("the surface does not mark %v required, but the registry rejects an envelope without "+
			"them — so a user would fill the form, submit, and be refused by a layer they cannot see", want)
	}
}

// TestEnvelopeSummaryDistinguishesZeroFromAbsent — "retry_budget: 0" and "no retry budget declared" are
// different statements, and a reviewer reading the first would believe a policy exists that does not.
func TestEnvelopeSummaryDistinguishesZeroFromAbsent(t *testing.T) {
	ceiling := 4
	bare := EnvelopeSummary(registry.Envelope{SandboxPosture: "no-network", TurnCeiling: &ceiling})
	if strings.Contains(bare, "concurrent") {
		t.Errorf("an envelope declaring no concurrency limit rendered one: %s", bare)
	}
	limit := 2
	withLimit := EnvelopeSummary(registry.Envelope{
		SandboxPosture: "no-network", TurnCeiling: &ceiling, ConcurrencyLimit: &limit})
	if !strings.Contains(withLimit, "at most 2 concurrent") {
		t.Errorf("a declared concurrency limit is not rendered: %s", withLimit)
	}
}
