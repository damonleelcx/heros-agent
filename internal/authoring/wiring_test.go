package authoring

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P15 15d — the wiring authoring gates.
//
// The load-bearing one is TestRefusedWiringDraftIsNeverEnqueuedForEval. Every other test here protects a
// user's time; that one protects the ledger from a false measurement.

// ── test doubles ────────────────────────────────────────────────────────────────────────────────

type coherentGate struct{ adapters []InsertedAdapter }

func (g coherentGate) Check([]string, []WiringEdge) ([]CoherenceBreak, []InsertedAdapter) {
	return nil, g.adapters
}

type incoherentGate struct{ breaks []CoherenceBreak }

func (g incoherentGate) Check([]string, []WiringEdge) ([]CoherenceBreak, []InsertedAdapter) {
	return g.breaks, nil
}

// swapOnly mirrors the real boundary: one shape, one language.
type swapOnly struct{}

func (swapOnly) CanMaterialize(shape WiringShape, language string) (bool, string) {
	if shape != ShapeTransposition {
		return false, "no rewriter moves, fuses or deletes a call at the source yet"
	}
	if language != "go" {
		return false, "no statement materializer has landed for " + language
	}
	return true, ""
}

func swapDraft() WiringDraft {
	return WiringDraft{
		NodeIDs: []string{"a", "b"}, Order: []string{"b", "a"},
		Shape: ShapeTransposition, Language: "go", Independent: true,
	}
}

// ── 19.4 all three names ────────────────────────────────────────────────────────────────────────

func TestIncoherentAuthoredOrderingNamesConsumerProducerField(t *testing.T) {
	gate := incoherentGate{breaks: []CoherenceBreak{
		{Consumer: "summarize", Producer: "retrieve", Field: "passages",
			Detail: "summarize requires passages, which retrieve produces"},
	}}

	got := PreflightWiring(swapDraft(), gate, swapOnly{})

	if got.Verdict != VerdictRefused {
		t.Fatalf("verdict = %q, want refused", got.Verdict)
	}
	if len(got.Breaks) != 1 || !got.Breaks[0].Named() {
		t.Fatalf("the break does not name all three: %+v", got.Breaks)
	}
	// 🔴 "Invalid ordering" is the refusal this test exists to make impossible. The platform knows which
	// consumer, which producer and which field; withholding them leaves the author staring at a graph.
	for _, name := range []string{"summarize", "retrieve", "passages"} {
		if !strings.Contains(got.Refusal.Cause, name) {
			t.Errorf("the refusal does not name %q: %q", name, got.Refusal.Cause)
		}
	}
	if got.Refusal.NodeID != "summarize" || got.Refusal.Field != "passages" {
		t.Errorf("the refusal does not anchor to the consumer and field: %+v", got.Refusal)
	}
	// And a coherence break must never be scoreable.
	if got.Scoreable {
		t.Error("a graph that would not run was marked scoreable")
	}
}

func TestCoherenceIsCheckedBeforeMaterializability(t *testing.T) {
	// A draft that is BOTH incoherent and unmaterializable must report the coherence break: "this breaks
	// your graph" is more urgent than "we cannot apply this shape yet", and a reader told the second
	// would fix the wrong thing.
	d := swapDraft()
	d.Shape = ShapeMerge // also unmaterializable
	gate := incoherentGate{breaks: []CoherenceBreak{
		{Consumer: "c", Producer: "p", Field: "f", Detail: "d"}}}

	got := PreflightWiring(d, gate, swapOnly{})
	if len(got.Breaks) == 0 {
		t.Fatal("the materializability refusal masked the coherence break")
	}
	if strings.Contains(got.Refusal.Cause, "no rewriter") {
		t.Error("the refusal reported the shape limitation rather than the broken graph")
	}
}

// ── 19.5 the adapter is visible before submission ───────────────────────────────────────────────

func TestAdaptedPreflightReturnsInsertedAdapter(t *testing.T) {
	adapter := InsertedAdapter{NodeID: "adp_1", From: "retrieve", To: "summarize", Kind: "rename",
		Rationale: "a rename adapter reconciles retrieve -> summarize"}

	got := PreflightWiring(swapDraft(), coherentGate{adapters: []InsertedAdapter{adapter}}, swapOnly{})

	if got.Verdict != VerdictAdmissible {
		t.Fatalf("an adapted ordering was not admitted: %+v", got)
	}
	if len(got.Adapters) != 1 {
		t.Fatalf("the adapter did not reach the preflight result: %+v", got.Adapters)
	}
	// 🔴 The author must SEE the component before agreeing to it. An indirection never hides a value from
	// review, and "the gate proved it drops nothing required" does not mean they consented to it.
	a := got.Adapters[0]
	if a.NodeID == "" || a.From == "" || a.To == "" {
		t.Errorf("the adapter is not renderable as a node with edges: %+v", a)
	}
}

func TestAuthoredAdapterIdentityDeterministic(t *testing.T) {
	adapter := InsertedAdapter{NodeID: "adp_1", From: "p", To: "c", Kind: "rename"}
	gate := coherentGate{adapters: []InsertedAdapter{adapter}}

	first := PreflightWiring(swapDraft(), gate, swapOnly{})
	second := PreflightWiring(swapDraft(), gate, swapOnly{})

	if first.Adapters[0].NodeID != second.Adapters[0].NodeID {
		t.Error("the same reordering produced two adapter identities — the preview and the diff would disagree")
	}
}

// ── 19.6 the shape is named ─────────────────────────────────────────────────────────────────────

func TestUnmaterializableShapeRefusedByName(t *testing.T) {
	for _, shape := range []WiringShape{ShapeMerge, ShapePrune, ShapeEdgeChange, ShapeNonAdjacent, ShapeMultiSwap} {
		t.Run(string(shape), func(t *testing.T) {
			d := swapDraft()
			d.Shape = shape

			got := PreflightWiring(d, coherentGate{}, swapOnly{})
			if got.Verdict != VerdictRefused {
				t.Fatalf("a %s was admitted", shape)
			}
			if got.Shape != shape {
				t.Errorf("the verdict reports shape %q, want %q", got.Shape, shape)
			}
			if !strings.Contains(got.Refusal.Cause, string(shape)) {
				t.Errorf("the refusal does not name the shape: %q", got.Refusal.Cause)
			}
			if got.Scoreable {
				t.Errorf("a refused %s was marked scoreable", shape)
			}
		})
	}

	t.Run("an unsupported language refuses by name", func(t *testing.T) {
		d := swapDraft()
		d.Language = "kotlin"
		got := PreflightWiring(d, coherentGate{}, swapOnly{})
		if got.Verdict != VerdictRefused {
			t.Fatal("a transposition was admitted on a language with no statement materializer")
		}
		if !strings.Contains(got.Refusal.Cause, "kotlin") {
			t.Errorf("the refusal does not name the language: %q", got.Refusal.Cause)
		}
	})

	t.Run("the one supported shape IS admitted — so the gate is not stuck red", func(t *testing.T) {
		got := PreflightWiring(swapDraft(), coherentGate{}, swapOnly{})
		if got.Verdict != VerdictAdmissible {
			t.Fatalf("an adjacent transposition on Go was refused: %s", got.Refusal.Cause)
		}
		if !got.Scoreable {
			t.Error("an applicable wiring change is not scoreable")
		}
	})
}

// ── 19.7 🔴🔴 a refused draft is not a scoreable variant ────────────────────────────────────────

func TestRefusedWiringDraftIsNeverEnqueuedForEval(t *testing.T) {
	// This is the assertion that keeps a false measurement structurally impossible rather than merely
	// unlikely. Every refusal path, checked through the ONE predicate every scheduler must consult.
	refused := []WiringVerdict{
		PreflightWiring(shapeDraft(ShapeMerge), coherentGate{}, swapOnly{}),
		PreflightWiring(shapeDraft(ShapePrune), coherentGate{}, swapOnly{}),
		PreflightWiring(shapeDraft(ShapeEdgeChange), coherentGate{}, swapOnly{}),
		PreflightWiring(shapeDraft(ShapeNonAdjacent), coherentGate{}, swapOnly{}),
		PreflightWiring(shapeDraft(ShapeMultiSwap), coherentGate{}, swapOnly{}),
		PreflightWiring(swapDraft(), incoherentGate{breaks: []CoherenceBreak{
			{Consumer: "c", Producer: "p", Field: "f"}}}, swapOnly{}),
		PreflightWiring(unprovableParallel(), coherentGate{}, swapOnly{}),
	}
	for i, v := range refused {
		if v.Scoreable {
			t.Errorf("refusal %d (%s) was marked scoreable", i, v.Shape)
		}
		if MayEnqueueEvaluation(v) {
			t.Errorf("refusal %d (%s) may be enqueued for evaluation — this is the false measurement", i, v.Shape)
		}
	}

	// The control: the one applicable shape MAY be enqueued, so this is a filter rather than a constant no.
	if !MayEnqueueEvaluation(PreflightWiring(swapDraft(), coherentGate{}, swapOnly{})) {
		t.Error("an applicable transposition cannot be evaluated — the gate is stuck closed")
	}
}

func TestRefusedWiringDraftIsNotAVariant(t *testing.T) {
	v := PreflightWiring(shapeDraft(ShapeMerge), coherentGate{}, swapOnly{})
	// A refused draft has no configuration hash to submit, and the verdict carries none.
	if v.Verdict != VerdictRefused {
		t.Fatal("expected a refusal")
	}
	intent, ok := IntentFor(shapeDraft(ShapeMerge), v)
	if !ok {
		t.Fatal("a refused draft produced no recorded intent")
	}
	if intent.Applicable || intent.Scoreable {
		t.Errorf("a recorded intent claims to be applicable or scoreable: %+v", intent)
	}
	if intent.Reason == "" {
		t.Error("a recorded intent with no reason is a to-do nobody can act on")
	}
}

// ── 19.8 a recorded intent is not a variant, and is never 'pending' ─────────────────────────────

func TestRecordedIntentIsNotAVariant(t *testing.T) {
	t.Run("an admissible draft cannot become a recorded intent", func(t *testing.T) {
		// An applicable change belongs in the variant list, not in a list of things we could not do.
		v := PreflightWiring(swapDraft(), coherentGate{}, swapOnly{})
		if _, ok := IntentFor(swapDraft(), v); ok {
			t.Error("an admissible draft was turned into a recorded intent")
		}
	})

	t.Run("the intent never describes itself as pending or queued", func(t *testing.T) {
		v := PreflightWiring(shapeDraft(ShapePrune), coherentGate{}, swapOnly{})
		intent, _ := IntentFor(shapeDraft(ShapePrune), v)
		for _, forbidden := range []string{"pending", "queued", "awaiting", "in progress", "scheduled"} {
			if strings.Contains(strings.ToLower(intent.Reason), forbidden) {
				t.Errorf("the intent's reason says %q, which implies somebody is working on it: %q",
					forbidden, intent.Reason)
			}
		}
	})
}

// ── 19.9 parallelization needs PROVEN independence ──────────────────────────────────────────────

func TestAuthoredParallelizeRefusesUnprovableIndependence(t *testing.T) {
	got := PreflightWiring(unprovableParallel(), coherentGate{}, swapOnly{})

	if got.Verdict != VerdictRefused {
		t.Fatal("nodes whose independence is unproven were marked parallelizable")
	}
	if !strings.Contains(got.Refusal.Cause, "shared cache write") {
		t.Errorf("the refusal does not name the blocking dependency: %q", got.Refusal.Cause)
	}
	// Absence of proof is not permission — the conservative posture the statement materializer takes.
	if !strings.Contains(got.Refusal.Cause, "not proven") {
		t.Errorf("the refusal does not say the independence is unproven: %q", got.Refusal.Cause)
	}

	t.Run("proven independence is admitted, where the shape can be materialized", func(t *testing.T) {
		d := unprovableParallel()
		d.Independent, d.Dependency = true, ""
		d.Shape = ShapeTransposition // parallelization itself has no materializer
		if got := PreflightWiring(d, coherentGate{}, swapOnly{}); got.Verdict != VerdictAdmissible {
			t.Errorf("a proven-independent transposition was refused: %s", got.Refusal.Cause)
		}
	})
}

// ── 19.16 / 19.17 every class red, and no override ──────────────────────────────────────────────

func TestWiringAuthoringRefusalsGoRed(t *testing.T) {
	classes := map[string]WiringVerdict{
		"incoherent ordering": PreflightWiring(swapDraft(),
			incoherentGate{breaks: []CoherenceBreak{{Consumer: "c", Producer: "p", Field: "f"}}}, swapOnly{}),
		"unprovable independence": PreflightWiring(unprovableParallel(), coherentGate{}, swapOnly{}),
		"merge":                   PreflightWiring(shapeDraft(ShapeMerge), coherentGate{}, swapOnly{}),
		"prune":                   PreflightWiring(shapeDraft(ShapePrune), coherentGate{}, swapOnly{}),
		"edge change":             PreflightWiring(shapeDraft(ShapeEdgeChange), coherentGate{}, swapOnly{}),
		"non-adjacent":            PreflightWiring(shapeDraft(ShapeNonAdjacent), coherentGate{}, swapOnly{}),
		"multi-swap":              PreflightWiring(shapeDraft(ShapeMultiSwap), coherentGate{}, swapOnly{}),
		"unsupported language":    PreflightWiring(langDraft("rust"), coherentGate{}, swapOnly{}),
	}
	for name, v := range classes {
		if v.Verdict != VerdictRefused {
			t.Errorf("%s: did not refuse — this gate cannot go red", name)
		}
		if v.Refusal.Cause == "" {
			t.Errorf("%s: refused without saying why", name)
		}
	}
	// The green half.
	if got := PreflightWiring(swapDraft(), coherentGate{}, swapOnly{}); got.Verdict != VerdictAdmissible {
		t.Errorf("the applicable shape was refused — the suite would pass against a gate that refuses everything")
	}
}

func TestNoOverrideForRefusedWiringShape(t *testing.T) {
	// Structural: nothing on the draft or the verdict could carry a bypass.
	for _, target := range []any{WiringDraft{}, WiringVerdict{}, RecordedIntent{}} {
		assertNoOverrideFields(t, target)
	}

	// Behavioural: the refusal holds for every draft variation a caller could try.
	for _, d := range []WiringDraft{
		shapeDraft(ShapeMerge),
		func() WiringDraft { x := shapeDraft(ShapeMerge); x.Independent = true; return x }(),
		func() WiringDraft { x := shapeDraft(ShapeMerge); x.NodeIDs = nil; return x }(),
	} {
		v := PreflightWiring(d, coherentGate{}, swapOnly{})
		if v.Verdict != VerdictRefused || v.Scoreable {
			t.Errorf("a merge reached %q (scoreable=%v)", v.Verdict, v.Scoreable)
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────────────────────────

func shapeDraft(s WiringShape) WiringDraft {
	d := swapDraft()
	d.Shape = s
	return d
}

func langDraft(l string) WiringDraft {
	d := swapDraft()
	d.Language = l
	return d
}

func unprovableParallel() WiringDraft {
	d := swapDraft()
	d.Shape = ShapeParallelize
	d.Independent = false
	d.Dependency = "both nodes perform a shared cache write"
	return d
}

// TestAuthoredTranspositionClaimsNothing (P15 15d task 19.18).
func TestAuthoredTranspositionClaimsNothing(t *testing.T) {
	v := PreflightWiring(swapDraft(), coherentGate{}, swapOnly{})
	if v.Verdict != VerdictAdmissible {
		t.Fatalf("the applicable shape was refused: %s", v.Refusal.Cause)
	}

	// An applied transposition is an authored change like any other: unverified, and contributing
	// nothing. The temptation on this axis is a latency claim — "these now run in a better order" — which
	// is exactly the kind of number nobody measured.
	entries := []Entry{{ChangeID: "swap-1", Axis: "wiring", VerificationState: StateUnverified}}
	for _, figure := range []string{"latency", "tokens", "quality"} {
		if total := CountableAggregate(entries, func(Entry) float64 { return 42 }); total != 0 {
			t.Errorf("%s: an unverified transposition contributed %v, want 0", figure, total)
		}
	}
	verified := append(entries, Entry{ChangeID: "swap-2", Axis: "wiring", VerificationState: StateVerified})
	if total := CountableAggregate(verified, func(Entry) float64 { return 42 }); total != 42 {
		t.Errorf("a verified transposition contributed %v, want 42", total)
	}
}

// TestWiringRevertReproducesParentHash (P15 15d task 19.20).
//
// Undoing a wiring change must land on the parent BYTE-IDENTICALLY, inserted adapters included. An
// adapter that survived a revert would leave a component in the graph that nothing asked for.
func TestWiringRevertReproducesParentHash(t *testing.T) {
	ctx := t.Context()
	parent := baseSpec()
	parentHash := specFingerprint(parent)
	rec := NewMemRecorder()

	s := Submitter{
		Preflight: Preflighter{Resolver: hashResolver{}, Materializer: okMaterializer{}},
		Applier:   &okApplier{}, Head: fixedHead{head: parentHash}, Record: rec, Auth: allowAll{},
	}
	// A wiring change is expressed on the spec's own Order/Edges, which the draft edits do not touch —
	// so this exercises the REVERT path over a spec that carries adapters, which is the case that can
	// drift. The adapters live on the parent, and the revert must reproduce them exactly.
	parent.InsertedAdapters = []variantspec.InsertedAdapter{
		{AdapterNodeID: "adp_1", FromNodeID: "n1", ToNodeID: "n2", CatalogKind: "rename"},
	}
	parentHash = specFingerprint(parent)

	d := draftFor(map[string]Edit{"n1": {ModelRef: strPtr("m-new")}})
	d.ParentVariantID, d.ConcurrencyToken = parentHash, parentHash
	sub, err := s.Submit(ctx, d, parent)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	rv := Reverter{Record: rec, Resolver: hashResolver{},
		Parents: mapParents{specs: map[string]*variantspec.VariantSpec{parentHash: parent}}}
	got, err := rv.Revert(ctx, sub.ChangeID, Actor{ID: "u1", TenantID: "t1"})
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if got.ConfigHash != parentHash {
		t.Fatalf("revert landed on %q, want byte-identical %q", got.ConfigHash, parentHash)
	}
	// The adapter survived the round trip rather than being dropped or duplicated.
	if len(parent.InsertedAdapters) != 1 {
		t.Errorf("the parent's adapters were mutated: %+v", parent.InsertedAdapters)
	}
}
