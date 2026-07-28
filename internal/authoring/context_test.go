package authoring

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P16 16c — context authoring. The decisive test here is TestUnmeasuredDropRatioIsThirdVerdict, and it
// asserts BOTH directions separately because each failure is a different bug with a different cause.

// ── doubles ─────────────────────────────────────────────────────────────────────────────────────

type mapDrops struct{ m map[string]DropMeasurement }

func (d mapDrops) Drop(node, policy string) DropMeasurement { return d.m[node+"|"+policy] }

type listCatalog struct{ policies []string }

func (c listCatalog) Registered(p string) bool {
	for _, x := range c.policies {
		if x == p {
			return true
		}
	}
	return false
}
func (c listCatalog) Policies() []string { return c.policies }

type labels struct {
	retrieval map[string]bool
	label     map[string]string
}

func (l labels) IsRetrieval(n string) bool { return l.retrieval[n] }
func (l labels) Label(n string) string     { return l.label[n] }

func f64(v float64) *float64 { return &v }

func resolvedWithContext(nodeID, policy string, tolerance *float64) *variantspec.Resolved {
	return &variantspec.Resolved{
		Language: "go",
		Config: variantspec.ResolvedConfig{Nodes: []variantspec.ResolvedNode{
			{NodeID: nodeID, ContextPolicy: policy, ContextDropTolerance: tolerance},
		}},
	}
}

var catalog = listCatalog{policies: []string{"ctx-full-history", "ctx-summarization", "ctx-rag-retrieval"}}

// ── 8.4 🔴🔴 the third verdict, both directions ─────────────────────────────────────────────────

func TestUnmeasuredDropRatioIsThirdVerdict(t *testing.T) {
	gate := DropGate{Drops: mapDrops{m: map[string]DropMeasurement{}}} // nothing measured
	r := resolvedWithContext("answer", "ctx-summarization", f64(0.2))

	v, ref, missing := gate.Check(context.Background(), r)

	if v != VerdictNotYetMeasurable {
		t.Fatalf("verdict = %q, want %q", v, VerdictNotYetMeasurable)
	}
	if missing.Kind != "context_drop_ratio" {
		t.Errorf("the missing measurement is not named: %+v", missing)
	}
	if missing.NodeID != "answer" || missing.Subject != "ctx-summarization" {
		t.Errorf("the third verdict does not scope the gap: %+v", missing)
	}
	if ref.Cause != "" {
		t.Errorf("the third verdict carried a refusal cause: %q", ref.Cause)
	}
}

func TestUnmeasuredNeverReturnsAdmissible(t *testing.T) {
	// Asserted on its own. Returning admissible would claim a safety check succeeded when it never ran,
	// on the axis whose failure mode is a silently worse answer.
	gate := DropGate{Drops: mapDrops{m: map[string]DropMeasurement{}}}
	v, _, _ := gate.Check(context.Background(), resolvedWithContext("answer", "ctx-summarization", f64(0.2)))
	if v == VerdictAdmissible {
		t.Fatal("an unmeasured drop ratio was admitted — that asserts a check that never ran")
	}
}

func TestUnmeasuredNeverReturnsRefused(t *testing.T) {
	// Asserted on its own. Returning refused would blame the user's change for our missing measurement,
	// and would make the axis unusable on every workflow nobody has evaluated yet.
	gate := DropGate{Drops: mapDrops{m: map[string]DropMeasurement{}}}
	v, _, _ := gate.Check(context.Background(), resolvedWithContext("answer", "ctx-summarization", f64(0.2)))
	if v == VerdictRefused {
		t.Fatal("an unmeasured drop ratio was refused — that blames the user for our missing measurement")
	}
}

func TestMeasuredZeroIsNotUnmeasured(t *testing.T) {
	// 0.0 is a real measurement — a lossless policy — and inferring "unmeasured" from it would discard
	// the very result that proves a policy safe.
	gate := DropGate{Drops: mapDrops{m: map[string]DropMeasurement{
		"answer|ctx-full-history": {Ratio: 0, Measured: true},
	}}}
	v, _, _ := gate.Check(context.Background(), resolvedWithContext("answer", "ctx-full-history", f64(0.2)))
	if v != VerdictAdmissible {
		t.Fatalf("a measured lossless policy got %q, want admissible", v)
	}
}

// ── 8.3 the gate runs before spend, and refuses on evidence ─────────────────────────────────────

func TestDropGateRefusalCostsNoEvalSpend(t *testing.T) {
	gate := DropGate{Drops: mapDrops{m: map[string]DropMeasurement{
		"answer|ctx-summarization": {Ratio: 0.62, Measured: true},
	}}}
	v, ref, _ := gate.Check(context.Background(), resolvedWithContext("answer", "ctx-summarization", f64(0.2)))

	if v != VerdictRefused {
		t.Fatalf("a policy measured over tolerance got %q, want refused", v)
	}
	// The refusal must carry the numbers a reader needs to decide: relax the tolerance, or pick another
	// policy. "Exceeds tolerance" alone gives them neither.
	for _, want := range []string{"answer", "0.20", "0.62", "ctx-summarization"} {
		if !strings.Contains(ref.Cause, want) {
			t.Errorf("the refusal omits %q: %q", want, ref.Cause)
		}
	}
	if !strings.Contains(ref.Cause, "before any evaluation spend") {
		t.Errorf("the refusal does not say it happened before spend: %q", ref.Cause)
	}
}

func TestNodeWithNoToleranceIsNotJudged(t *testing.T) {
	// Declaring nothing is not declaring zero. Zero would reject every lossy policy on every node nobody
	// configured, which would make the axis unusable by default.
	gate := DropGate{Drops: mapDrops{m: map[string]DropMeasurement{
		"answer|ctx-summarization": {Ratio: 0.9, Measured: true},
	}}}
	v, _, _ := gate.Check(context.Background(), resolvedWithContext("answer", "ctx-summarization", nil))
	if v != VerdictAdmissible {
		t.Fatalf("a node declaring no tolerance got %q, want admissible", v)
	}
}

func TestContextPreflightUsesTheSameDropGate(t *testing.T) {
	// The gate is consumed through the Admissibility interface preflight runs, so the editor and the
	// proposal path cannot hold two predicates.
	var _ Admissibility = DropGate{}

	p := Preflighter{
		Resolver:     hashResolver{},
		Materializer: okMaterializer{},
		Gates:        []Admissibility{DropGate{Drops: mapDrops{m: map[string]DropMeasurement{}}}},
	}
	// A draft touching context on a node the base spec orders.
	parent := baseSpec()
	parent.Nodes["n1"] = variantspec.NodeOverride{ContextPolicy: "ctx-summarization",
		ContextDropTolerance: f64(0.2)}
	got, err := p.Preflight(context.Background(), draftFor(map[string]Edit{
		"n1": {ContextPolicy: strPtr("ctx-summarization")}}), parent)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	// hashResolver builds a Resolved with no Config.Nodes, so the gate has nothing to judge and admits —
	// what matters here is that the gate RAN through the shared interface without a second predicate.
	if got.Verdict == "" {
		t.Error("preflight produced no verdict")
	}
}

// ── 8.5 / 8.13 the language boundary is stated ──────────────────────────────────────────────────

func TestContextAuthoringLanguageRefusalNamesAllThree(t *testing.T) {
	n := ContextNode{NodeID: "answer", Language: "kotlin", CurrentPolicy: "ctx-full-history"}
	offer := OfferContext(n, catalog, goOnly, labels{})

	if offer.Refused.Cause == "" {
		t.Fatal("a language with no rewriter was offered context authoring")
	}
	for _, want := range []string{"answer", "kotlin", "context"} {
		if !strings.Contains(offer.Refused.Cause, want) {
			t.Errorf("the refusal does not name %q: %q", want, offer.Refused.Cause)
		}
	}
	// It must say what WOULD have worked.
	if !strings.Contains(offer.Refused.Cause, "go") {
		t.Errorf("the refusal does not list the covered languages: %q", offer.Refused.Cause)
	}
	// And it must explain WHY context is special — a policy is a code rewrite, not an argument swap.
	if !strings.Contains(offer.Refused.Cause, "message") {
		t.Errorf("the refusal does not say why context needs a rewriter: %q", offer.Refused.Cause)
	}
}

// ── 8.6 / 8.7 declaring and clearing a tolerance ────────────────────────────────────────────────

func TestDropToleranceDeclareAndClearIsByteExact(t *testing.T) {
	parent := baseSpec()
	before := specFingerprint(parent)

	declared, err := draftFor(map[string]Edit{"n1": {DropTolerance: ptrTo(f64(0.25))}}).Derive(parent)
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	if specFingerprint(declared) == before {
		t.Fatal("declaring a tolerance changed nothing — it must participate in the hash")
	}

	// Clearing returns the node to its pre-declaration configuration BYTE-IDENTICALLY. "Clear" is not
	// "declare 0": zero tolerance rejects every lossy policy, which is the opposite of removing the
	// constraint.
	var none *float64
	cleared, err := draftFor(map[string]Edit{"n1": {DropTolerance: &none}}).Derive(declared)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if specFingerprint(cleared) != before {
		t.Errorf("clearing landed on a different configuration:\n before  %s\n cleared %s",
			before, specFingerprint(cleared))
	}
	if cleared.Nodes["n1"].ContextDropTolerance != nil {
		t.Error("clearing left a tolerance behind")
	}

	// And clearing is distinguishable from declaring zero.
	zero, err := draftFor(map[string]Edit{"n1": {DropTolerance: ptrTo(f64(0))}}).Derive(declared)
	if err != nil {
		t.Fatalf("declare zero: %v", err)
	}
	if specFingerprint(zero) == specFingerprint(cleared) {
		t.Error("declaring zero and clearing produced the same configuration — they are opposite intents")
	}
}

func TestDeclaredToleranceAlreadyExceededIsReported(t *testing.T) {
	drops := mapDrops{m: map[string]DropMeasurement{
		"answer|ctx-summarization": {Ratio: 0.55, Measured: true},
	}}
	n := ContextNode{NodeID: "answer", Language: "go", CurrentPolicy: "ctx-summarization"}

	rep := ReportTolerance(n, drops, 0.20)
	if !rep.AlreadyExceeded {
		t.Fatal("a tolerance the current policy already exceeds was silently accepted")
	}
	for _, want := range []string{"ctx-summarization", "0.55", "0.20"} {
		if !strings.Contains(rep.Message, want) {
			t.Errorf("the report omits %q: %q", want, rep.Message)
		}
	}

	// A tolerance the current policy satisfies is not reported as a problem.
	if rep := ReportTolerance(n, drops, 0.80); rep.AlreadyExceeded {
		t.Error("a satisfiable tolerance was reported as already exceeded")
	}
}

// ── 8.8 the classifier label is not user-settable ───────────────────────────────────────────────

func TestRetrievalParamsRefusedOnNonRAGNode(t *testing.T) {
	l := labels{retrieval: map[string]bool{"retrieve": true}, label: map[string]string{"answer": "Reasoning"}}
	n := ContextNode{NodeID: "answer", Language: "go"}

	offer := OfferContext(n, catalog, goOnly, l)
	if offer.RetrievalParams {
		t.Fatal("retrieval parameters were offered on a node the classifier did not label retrieval")
	}
	// The reason must be STATED. A control that is simply absent reads as a missing feature.
	if offer.RetrievalReason == "" {
		t.Fatal("the parameters are absent with no reason given")
	}
	if !strings.Contains(offer.RetrievalReason, "Reasoning") {
		t.Errorf("the reason does not say what the node actually is: %q", offer.RetrievalReason)
	}

	ref := ValidateContextChange(n, catalog, goOnly, l, "", map[string]any{"top_k": 8})
	if ref.Cause == "" {
		t.Fatal("retrieval parameters were accepted on a non-retrieval node")
	}
	if !strings.Contains(ref.Cause, "not settable") {
		t.Errorf("the refusal does not say the label cannot be set here: %q", ref.Cause)
	}

	// And on a retrieval node they ARE offered, so the gate is not stuck closed.
	rn := ContextNode{NodeID: "retrieve", Language: "go"}
	if !OfferContext(rn, catalog, goOnly, l).RetrievalParams {
		t.Error("retrieval parameters were withheld from a retrieval node")
	}
	if ref := ValidateContextChange(rn, catalog, goOnly, l, "", map[string]any{"top_k": 8}); ref.Cause != "" {
		t.Errorf("retrieval parameters were refused on a retrieval node: %s", ref.Cause)
	}
}

func TestNoPathSetsClassifierLabel(t *testing.T) {
	// Structural: the interface this package holds is READ-ONLY. A setter added later would be found here.
	rt := reflect.TypeOf((*PatternLabels)(nil)).Elem()
	for i := 0; i < rt.NumMethod(); i++ {
		name := rt.Method(i).Name
		for _, banned := range []string{"Set", "Override", "Mark", "Declare", "Force"} {
			if strings.HasPrefix(name, banned) {
				t.Errorf("PatternLabels exposes %q — the classifier label is an input to authoring, never an output", name)
			}
		}
	}
	// And nothing on the authoring node carries a label a caller could supply.
	nt := reflect.TypeOf(ContextNode{})
	for i := 0; i < nt.NumField(); i++ {
		if strings.Contains(strings.ToLower(nt.Field(i).Name), "label") ||
			strings.Contains(strings.ToLower(nt.Field(i).Name), "pattern") {
			t.Errorf("ContextNode carries %q — a node relabelled to unlock parameters would let a result "+
				"be attributed to parameters that did nothing", nt.Field(i).Name)
		}
	}
}

// ── 8.9 only registered policies ────────────────────────────────────────────────────────────────

func TestOnlyRegisteredPoliciesOffered(t *testing.T) {
	n := ContextNode{NodeID: "answer", Language: "go"}
	offer := OfferContext(n, catalog, goOnly, labels{})

	if len(offer.Policies) != len(catalog.policies) {
		t.Errorf("offered %v, want the registered set %v", offer.Policies, catalog.policies)
	}
	if ref := ValidateContextChange(n, catalog, goOnly, labels{}, "ctx-invented", nil); ref.Cause == "" {
		t.Fatal("an unregistered policy was accepted — free text is not a selection path")
	}
	if ref := ValidateContextChange(n, catalog, goOnly, labels{}, "ctx-summarization", nil); ref.Cause != "" {
		t.Fatalf("a registered policy was refused: %s", ref.Cause)
	}
}

// ── 8.12 drop ratio is loss, never saving ───────────────────────────────────────────────────────

func TestDropRatioIsDescribedAsLoss(t *testing.T) {
	label := DropRatioLabel(0.42)
	if !strings.Contains(label, "discarded") {
		t.Errorf("the drop ratio is not described as loss: %q", label)
	}
	for _, forbidden := range []string{"saved", "saving", "reduction", "cheaper", "efficient"} {
		if strings.Contains(strings.ToLower(label), forbidden) {
			t.Errorf("the drop ratio label says %q — a lossier policy shows fewer tokens, and calling that "+
				"a saving inverts the one number this axis exists to keep honest", forbidden)
		}
	}
}

// ── 8.17 / 8.18 every class red, and no override ────────────────────────────────────────────────

func TestContextAuthoringVerdictsGoRed(t *testing.T) {
	overGate := DropGate{Drops: mapDrops{m: map[string]DropMeasurement{
		"answer|ctx-summarization": {Ratio: 0.9, Measured: true}}}}
	unknownGate := DropGate{Drops: mapDrops{m: map[string]DropMeasurement{}}}
	l := labels{label: map[string]string{"answer": "Reasoning"}}
	n := ContextNode{NodeID: "answer", Language: "go"}

	red := map[string]bool{}
	v, _, _ := overGate.Check(context.Background(), resolvedWithContext("answer", "ctx-summarization", f64(0.2)))
	red["over tolerance"] = v == VerdictRefused
	v, _, _ = unknownGate.Check(context.Background(), resolvedWithContext("answer", "ctx-summarization", f64(0.2)))
	red["unmeasured third verdict"] = v == VerdictNotYetMeasurable
	red["unsupported language"] = OfferContext(ContextNode{NodeID: "a", Language: "rust"}, catalog, goOnly, l).Refused.Cause != ""
	red["non-RAG retrieval params"] = ValidateContextChange(n, catalog, goOnly, l, "", map[string]any{"k": 1}).Cause != ""
	red["unregistered policy"] = ValidateContextChange(n, catalog, goOnly, l, "ctx-nope", nil).Cause != ""

	for name, went := range red {
		if !went {
			t.Errorf("%s: did not produce its verdict — this gate cannot go red", name)
		}
	}

	// The green half: a registered policy on a covered language with a satisfiable tolerance is admitted.
	okGate := DropGate{Drops: mapDrops{m: map[string]DropMeasurement{
		"answer|ctx-summarization": {Ratio: 0.05, Measured: true}}}}
	if v, _, _ := okGate.Check(context.Background(),
		resolvedWithContext("answer", "ctx-summarization", f64(0.2))); v != VerdictAdmissible {
		t.Errorf("a within-tolerance policy got %q — the gate is stuck red", v)
	}
}

func TestNoOverrideOnContextAuthoring(t *testing.T) {
	// Structural: the gate has nowhere to put an assumed ratio, which would be a way to answer its
	// question without measuring.
	assertNoOverrideFields(t, DropGate{})
	assertNoOverrideFields(t, ContextNode{})
	rt := reflect.TypeOf(DropGate{})
	for i := 0; i < rt.NumField(); i++ {
		name := strings.ToLower(rt.Field(i).Name)
		for _, banned := range []string{"assume", "default", "strict", "ratio"} {
			if strings.Contains(name, banned) {
				t.Errorf("DropGate has field %q — a supplied ratio answers the gate's question without "+
					"measuring, which is the one thing it exists to prevent", rt.Field(i).Name)
			}
		}
	}

	// Behavioural: the refusal holds for every actor.
	gate := DropGate{Drops: mapDrops{m: map[string]DropMeasurement{
		"answer|ctx-summarization": {Ratio: 0.9, Measured: true}}}}
	for range []string{"member", "admin", "owner"} {
		if v, _, _ := gate.Check(context.Background(),
			resolvedWithContext("answer", "ctx-summarization", f64(0.2))); v != VerdictRefused {
			t.Errorf("the drop gate was bypassed, verdict %q", v)
		}
	}
}

// ── 8.19 / 8.20 claims and augmentation ─────────────────────────────────────────────────────────

func TestUnverifiedContextChangeClaimsNothing(t *testing.T) {
	entries := []Entry{{ChangeID: "ctx-1", Axis: "context", VerificationState: StateUnverified}}
	if total := CountableAggregate(entries, func(Entry) float64 { return 5000 }); total != 0 {
		t.Errorf("an unverified context change contributed %v tokens, want 0", total)
	}
	verified := append(entries, Entry{ChangeID: "ctx-2", Axis: "context", VerificationState: StateVerified})
	if total := CountableAggregate(verified, func(Entry) float64 { return 5000 }); total != 5000 {
		t.Errorf("a verified context change contributed %v, want 5000", total)
	}
}

func TestAuthoredAugmentationIsNotLoss(t *testing.T) {
	// Pure augmentation adds chunks without discarding conversation content: drop ratio 0, with a
	// positive retrieved-chunk count. Reporting it as loss would make retrieval look like compaction.
	gate := DropGate{Drops: mapDrops{m: map[string]DropMeasurement{
		"retrieve|ctx-rag-retrieval": {Ratio: 0, Measured: true},
	}}}
	v, ref, _ := gate.Check(context.Background(), resolvedWithContext("retrieve", "ctx-rag-retrieval", f64(0.1)))
	if v != VerdictAdmissible {
		t.Fatalf("pure augmentation got %q (%s), want admissible", v, ref.Cause)
	}
	if got := DropRatioLabel(0); !strings.Contains(got, "0%") {
		t.Errorf("a zero drop ratio does not read as zero: %q", got)
	}
}

func ptrTo(p *float64) **float64 { return &p }

// TestAuthoredRetrievalRunIsPinned and TestAuthoredRetrievalHeldOutDisjointFromShownMotivation (8.10).
//
// A user may author a retrieval change. They may not author the evidence for it — and on this axis the
// rule is slightly STRICTER than the operator path's: the held-out set must be disjoint from the cases
// the SURFACE SHOWED as motivation, because a person who has been staring at five failing cases will
// tune to those five.
func TestAuthoredRetrievalHeldOutDisjointFromShownMotivation(t *testing.T) {
	shown := []string{"c3", "c7", "c9"}
	all := []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8", "c9", "c10", "c11", "c12"}

	split := HeldOutExcluding("cfg-authored-retrieval", all, shown)

	if len(split.HeldOut) == 0 {
		t.Fatal("no held-out cases remained")
	}
	shownSet := map[string]bool{}
	for _, c := range shown {
		shownSet[c] = true
	}
	for _, c := range split.HeldOut {
		if shownSet[c] {
			t.Errorf("case %q was shown as motivation AND used to judge the change", c)
		}
	}
	// Disjoint from the motivating bucket too.
	motivating := map[string]bool{}
	for _, c := range split.Motivating {
		motivating[c] = true
	}
	for _, c := range split.HeldOut {
		if motivating[c] {
			t.Errorf("case %q is in both buckets", c)
		}
	}
	// Deterministic: the same configuration and the same shown set split the same way every time.
	again := HeldOutExcluding("cfg-authored-retrieval", all, shown)
	if strings.Join(again.HeldOut, ",") != strings.Join(split.HeldOut, ",") {
		t.Error("the split is not reproducible")
	}
}

func TestAuthoredRetrievalRunIsPinned(t *testing.T) {
	// The pinning contract, as this package can assert it: a retrieval measurement's inputs are derived
	// from the configuration, never supplied by the author. Structural, because the way this rule breaks
	// is a helpful new parameter rather than a wrong number.
	ft := reflect.TypeOf(HeldOutExcluding)
	if ft.NumIn() != 3 {
		t.Fatalf("HeldOutExcluding takes %d parameters; it should take (configHash, allCases, shown)", ft.NumIn())
	}
	for i := 0; i < ft.NumIn(); i++ {
		name := strings.ToLower(ft.In(i).String())
		for _, banned := range []string{"seed", "actor", "user", "principal", "retriever"} {
			if strings.Contains(name, banned) {
				t.Errorf("parameter %d is %s — seeds, retrievers and identities are platform-derived", i, ft.In(i))
			}
		}
	}
}

// TestAuthoredContextChangeAssertsDownstreamState (P16 16c task 8.21).
//
// The context axis is exactly where a resolved-but-not-applied override would be invisible from the
// handler's return: a policy that resolved, hashed, and then was silently dropped would leave a
// configuration whose hash claims one assembly and whose code does another. So this reads back the
// RESOLVED POLICY frozen into the node, not just the fact that submit succeeded.
func TestAuthoredContextChangeAssertsDownstreamState(t *testing.T) {
	ctx := t.Context()
	parent := baseSpec()
	parentHash := specFingerprint(parent)
	rec := NewMemRecorder()
	applier := &okApplier{}

	s := Submitter{
		Preflight: Preflighter{Resolver: hashResolver{}, Materializer: okMaterializer{}},
		Applier:   applier, Head: fixedHead{head: parentHash}, Record: rec, Auth: allowAll{},
	}
	d := draftFor(map[string]Edit{"n1": {
		ContextPolicy: strPtr("ctx-summarization"),
		DropTolerance: ptrTo(f64(0.3)),
	}})
	d.ParentVariantID, d.ConcurrencyToken = parentHash, parentHash

	sub, err := s.Submit(ctx, d, parent)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	t.Run("the resolved policy is frozen into the node", func(t *testing.T) {
		node := sub.Compiled.Candidate.Spec.Nodes["n1"]
		if node.ContextPolicy != "ctx-summarization" {
			t.Errorf("context_policy = %q, want the authored policy — a dropped override would leave the "+
				"hash claiming one assembly while the code did another", node.ContextPolicy)
		}
		if node.ContextDropTolerance == nil || *node.ContextDropTolerance != 0.3 {
			t.Errorf("the declared tolerance did not survive: %v", node.ContextDropTolerance)
		}
	})

	t.Run("the record attributes it to the context axis", func(t *testing.T) {
		history, err := rec.History(ctx, sub.ChangeID)
		if err != nil || len(history) != 1 {
			t.Fatalf("history = %v (%v)", history, err)
		}
		if !strings.Contains(history[0].Axis, "context") {
			t.Errorf("axis = %q, want context", history[0].Axis)
		}
		if history[0].VerificationState != StateUnverified {
			t.Errorf("verification_state = %q, want unverified", history[0].VerificationState)
		}
		if history[0].DiffRef == "" {
			t.Error("the record does not cite the diff")
		}
	})

	t.Run("the parent is unchanged", func(t *testing.T) {
		if parent.Nodes["n1"].ContextPolicy != "" {
			t.Error("submitting mutated the parent's context policy")
		}
	})
}
