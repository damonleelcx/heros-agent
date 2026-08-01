package proposal

import (
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalstats"

	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P16 §5/§6 — the drop-tolerance gate and retrieval tuning.
//
// The gate's entire value is being EARLY. A lossy policy that removes the answer will eventually show
// up as a task_success regression; the point of refusing it here is that the regression costs a
// multi-seed eval run to discover and the refusal costs nothing. So every test below asserts not only
// the verdict but WHERE it happened: at emission, with no candidate surfaced.

const (
	refCtxSummarize = "aaaa111111111111111111111111111111111111111111111111111111111111"
	refCtxWindow    = "aaaa222222222222222222222222222222222222222222222222222222222222"
	refCtxTopK      = "aaaa333333333333333333333333333333333333333333333333333333333333"
	refCtxTopKBig   = "aaaa444444444444444444444444444444444444444444444444444444444444"
	refCtxChunk     = "aaaa555555555555555555555555555555555555555555555555555555555555"
	refCtxEmbed     = "aaaa666666666666666666666666666666666666666666666666666666666666"
)

// contextMenu extends the shared test menu with context entries carrying the loss metadata the drop
// gate reads. `summarization` is lossy and estimated to drop most of the context; `sliding_window` here
// is a gentle one; the top-k entries are retrieval augmentation and lossless.
func contextMenu() Menu {
	m := testMenu()
	m.ContextPolicies = []ContextChoice{
		{Ref: refCtxSummarize, Policy: "summarization", Lossy: true, ExpectedDrop: 0.85},
		{Ref: refCtxWindow, Policy: "sliding_window", Lossy: true, ExpectedDrop: 0.15},
		{Ref: refCtxTopK, Policy: "topk", TopK: 5},
		{Ref: refCtxTopKBig, Policy: "topk", TopK: 20},
		{Ref: refCtxChunk, Policy: "topk", TopK: 5, ChunkSize: 256},
		{Ref: refCtxEmbed, Policy: "topk", TopK: 5, EmbeddingModel: "text-embedding-3-large"},
	}
	return m
}

// ── task 5.3 / 7.4 🔴 — a proposal past the node's tolerance is inadmissible, before transform ────

func TestProposalPastDropToleranceInadmissible(t *testing.T) {
	base := baseSpec()
	// The node's job cannot afford to lose much: it reasons over the conversation it is given.
	base.Nodes["rag"] = variantspec.NodeOverride{ModelRef: refWeakModel, ContextDropTolerance: ptrFloat(0.25)}

	e := Engine{Menu: contextMenu(), Base: base}
	em := e.Propose([]Target{
		{Diagnosis: diag("rag", diagnosis.CauseContextOverflow, "c1"), Pattern: patternclassifier.Reflection},
	})

	// The summarization candidate (85% drop) must not be on the board at all…
	for _, c := range em.Candidates {
		if o := c.Spec.Nodes[c.NodeID]; o.ContextPolicy == refCtxSummarize {
			t.Fatalf("a policy estimated to drop 85%% of a node that tolerates 25%% was SURFACED as a "+
				"candidate: %s. It would now consume a transform and a multi-seed eval run to prove what the "+
				"gate already knows", c.Rationale)
		}
	}
	// …and its rejection is RECORDED, with a reason a human can act on. A silent filter would look
	// identical to an operator that simply never fired.
	var refusal string
	for _, r := range em.Refusals {
		if strings.Contains(r.Reason, "summarization") {
			refusal = r.Reason
		}
	}
	if refusal == "" {
		t.Fatalf("the over-dropping proposal was not recorded as a refusal; refusals=%+v", em.Refusals)
	}
	for _, want := range []string{"85%", "25%", "before any transform", "before any eval spend"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("the refusal should state %q so a reader knows what was rejected and when:\n%s", want, refusal)
		}
	}

	// The gentler policy on the same node is still admitted — the gate refuses a MEASUREMENT, not the
	// whole axis. A gate that rejected every context change would be indistinguishable from disabling
	// the operator.
	var sawWindow bool
	for _, c := range em.Candidates {
		if c.Spec.Nodes[c.NodeID].ContextPolicy == refCtxWindow {
			sawWindow = true
		}
	}
	if !sawWindow {
		t.Error("a 15-percent-drop policy on a node tolerating 25 percent was also rejected; the gate must " +
			"judge the number, not the dimension")
	}
}

// A MEASURED drop beats the menu's estimate. This is the half that makes the gate improve as the
// platform learns: a policy the estimate called gentle, and the node measured as brutal, is rejected.
func TestMeasuredDropBeatsTheEstimate(t *testing.T) {
	base := baseSpec()
	base.Nodes["rag"] = variantspec.NodeOverride{ModelRef: refWeakModel, ContextDropTolerance: ptrFloat(0.30)}

	e := Engine{
		Menu: contextMenu(), Base: base,
		DropTolerance: DropGate{Observed: map[DropKey]float64{
			// The estimate says 0.15; this node actually lost 0.70 under it last run.
			{NodeID: "rag", Policy: "sliding_window"}: 0.70,
		}},
	}
	em := e.Propose([]Target{
		{Diagnosis: diag("rag", diagnosis.CauseContextOverflow, "c1"), Pattern: patternclassifier.Reflection},
	})
	for _, c := range em.Candidates {
		if c.Spec.Nodes[c.NodeID].ContextPolicy == refCtxWindow {
			t.Fatal("the gate used the menu's optimistic estimate over this node's own measurement; a " +
				"measurement of THIS node under THIS policy is strictly better evidence than a table")
		}
	}
	var found bool
	for _, r := range em.Refusals {
		if strings.Contains(r.Reason, "measured on a previous run") {
			found = true
		}
	}
	if !found {
		t.Errorf("the refusal must say the number was measured, not estimated: %+v", em.Refusals)
	}
}

// 🔴 The gate refuses on EVIDENCE, never on ignorance. A policy nothing has measured and the menu does
// not describe is admitted, and verification decides — otherwise "we have no data" would come to mean
// "no", freezing the board on whatever happened to be measured first.
func TestUnmeasuredPolicyIsAdmittedNotRefused(t *testing.T) {
	base := baseSpec()
	base.Nodes["rag"] = variantspec.NodeOverride{ModelRef: refWeakModel, ContextDropTolerance: ptrFloat(0.0)}

	menu := testMenu()                                                                      // no ContextPolicies at all: the gate knows nothing about any entry
	menu.ContextPolicies = []ContextChoice{{Ref: refCtxSummarize, Policy: "summarization"}} // no Lossy, no estimate

	e := Engine{Menu: menu, Base: base}
	em := e.Propose([]Target{
		{Diagnosis: diag("rag", diagnosis.CauseContextOverflow, "c1"), Pattern: patternclassifier.Reflection},
	})
	if len(em.Candidates) == 0 {
		t.Fatal("a node tolerating zero drop rejected a policy with NO measurement and NO estimate; the " +
			"gate must not refuse on ignorance")
	}
}

// An authored tolerance of 0 is the strictest possible statement, not an unset field. The pointer type
// exists precisely so those two can be told apart.
func TestAuthoredZeroToleranceIsNotUnset(t *testing.T) {
	zero := variantspec.NodeOverride{ContextDropTolerance: ptrFloat(0)}
	got, authored := ToleranceFor(zero, patternclassifier.RetrievalRAG)
	if !authored || got != 0 {
		t.Errorf("an authored 0 tolerance read as (%v, authored=%v); replacing it with the pattern default "+
			"silently discards the strictest thing an author can say", got, authored)
	}

	// And an ABSENT one falls back to the node's job, which is what the pattern names. A RAG node
	// tolerates more than a reflection node, because augmentation is its normal behavior.
	unset := variantspec.NodeOverride{}
	rag, ragAuthored := ToleranceFor(unset, patternclassifier.RetrievalRAG)
	reflect, _ := ToleranceFor(unset, patternclassifier.Reflection)
	if ragAuthored {
		t.Error("a node declaring nothing reported an authored tolerance")
	}
	if !(rag > reflect) {
		t.Errorf("a Retrieval node's default tolerance (%v) must exceed a Reflection node's (%v): a RAG "+
			"node reshapes what it carries by design, a reflection node reasons over it", rag, reflect)
	}
}

// The gate judges CONTEXT changes only. A model swap does not touch what the node assembles, so gating
// it on a context number would refuse a change for a property it cannot affect.
func TestDropGateIgnoresNonContextCandidates(t *testing.T) {
	base := baseSpec()
	base.Nodes["router"] = variantspec.NodeOverride{ModelRef: refWeakModel, ContextDropTolerance: ptrFloat(0)}

	e := Engine{Menu: contextMenu(), Base: base}
	em := e.Propose([]Target{
		{Diagnosis: diag("router", diagnosis.CauseModelCapabilityGap, "c1"), Pattern: patternclassifier.Routing},
	})
	if len(em.Candidates) == 0 {
		t.Fatalf("a zero drop tolerance blocked a MODEL upgrade, which changes nothing about context "+
			"assembly; refusals=%+v", em.Refusals)
	}
}

func ptrFloat(f float64) *float64 { return &f }

// ── task 6.2 — the retrieval knobs are top-k, chunk size, AND embedding model ─────────────────────

func TestRAGTuneProposesChunkAndEmbedding(t *testing.T) {
	base := baseSpec()
	// The node currently pins a top-5 retrieval entry, so a candidate's delta is measured against it.
	base.Nodes["rag"] = variantspec.NodeOverride{ModelRef: refWeakModel, ContextPolicy: refCtxTopK}

	e := Engine{Menu: contextMenu(), Base: base}
	em := e.Propose([]Target{
		{Diagnosis: diag("rag", diagnosis.CauseRetrievalMiss, "c1"), Pattern: patternclassifier.RetrievalRAG},
	})

	byRef := map[string]Candidate{}
	for _, c := range em.Candidates {
		if c.Operator == OpRAGTune {
			byRef[c.Spec.Nodes[c.NodeID].ContextPolicy] = c
		}
	}
	for _, want := range []struct{ ref, knob string }{
		{refCtxTopKBig, "top-k 5→20"},
		{refCtxChunk, "chunk size (unset)→256"},
		{refCtxEmbed, "embedding (unset)→text-embedding-3-large"},
	} {
		c, ok := byRef[want.ref]
		if !ok {
			t.Errorf("OpRAGTune proposed no variant for entry %s; top-k, chunk size and embedding model are "+
				"the three knobs that decide what a retriever returns, and a phase that tuned only top-k "+
				"would leave two of them unmeasured", want.ref[:8])
			continue
		}
		// 🔴 The rationale names WHICH knob moved and from what. Verification attributes a delta to a
		// candidate, and "something about retrieval helped" is not a finding anyone can act on.
		if !strings.Contains(c.Rationale, want.knob) {
			t.Errorf("the rationale for %s does not name the knob that changed (want %q):\n%s",
				want.ref[:8], want.knob, c.Rationale)
		}
	}

	// The entry the node ALREADY pins is not re-proposed: a no-op candidate would spend a verification
	// slot proving nothing.
	if _, ok := byRef[refCtxTopK]; ok {
		t.Error("OpRAGTune proposed the entry the node already pins; that candidate can only ever tie")
	}
}

// ── task 6.3 / 7.5 🔴 — verified on a HELD-OUT set; an overlap is refused ─────────────────────────

func TestRetrievalVerifiedOnHeldoutSet(t *testing.T) {
	cases := caseIDs(40)
	cfg := evalstats.DefaultConfig()

	// (1) The intended path: the split is derived from config_hash + case ids and is disjoint BY
	// CONSTRUCTION — every case lands in exactly one half.
	split := DeriveRetrievalSplit("cfg-retrieval", cases)
	if len(split.Overlap()) != 0 {
		t.Fatalf("the derived split is not disjoint: %v", split.Overlap())
	}
	if len(split.HeldOut) < MinHeldOutCases || len(split.Tuning) == 0 {
		t.Fatalf("degenerate split: tuning=%d held-out=%d", len(split.Tuning), len(split.HeldOut))
	}

	// A genuine improvement on the held-out cases is a verified delta.
	weak := successSeries("base", cases, []int64{1, 2, 3, 4, 5}, 0.4)
	strong := successSeries("cand", cases, []int64{1, 2, 3, 4, 5}, 0.9)
	got, err := VerifyRetrievalChange(split, weak, strong, cfg)
	if err != nil {
		t.Fatalf("VerifyRetrievalChange: %v", err)
	}
	if !got.Verified {
		t.Errorf("a separated improvement on held-out cases must verify: %s", got.Reason)
	}

	// (2) 🔴 The refusal. A caller-supplied split whose halves share cases has NO honest verdict, and the
	// verification says so instead of computing one. Quietly de-duplicating the overlap would still
	// report a number, and the number would be exactly the overfit one.
	overlapping := RetrievalSplit{
		Tuning:  cases[:30],
		HeldOut: cases[20:], // 10 cases in both halves
	}
	if _, err := VerifyRetrievalChange(overlapping, weak, strong, cfg); !errors.Is(err, ErrOverlappingSplit) {
		t.Fatalf("an overlapping split must be REFUSED, got %v", err)
	} else if !strings.Contains(err.Error(), "case-020") {
		t.Errorf("the refusal must name the offending cases so the split can be fixed: %v", err)
	}

	// (3) A tie is not a win. Overlapping intervals mean the platform cannot tell the two apart on unseen
	// cases — which is a different answer from "it got better", and reporting it as a win would be the
	// overfit claim in a different disguise.
	same := successSeries("cand-same", cases, []int64{1, 2, 3, 4, 5}, 0.4)
	tie, err := VerifyRetrievalChange(split, weak, same, cfg)
	if err != nil {
		t.Fatalf("VerifyRetrievalChange: %v", err)
	}
	if tie.Verified {
		t.Errorf("an indistinguishable change was reported as a verified delta: %s", tie.Reason)
	}

	// (4) And retrieval tuning is the operator this rule applies to, stated as a predicate rather than
	// left implicit at the call site.
	if !RequiresHeldOutVerification(OpRAGTune) {
		t.Error("OpRAGTune must require held-out verification: its knobs are searchable, so a win on the " +
			"set they were searched over carries no information about anything else")
	}
	if RequiresHeldOutVerification(OpModelUpgrade) {
		t.Error("held-out verification is retrieval's rule; applying it to every operator would make it noise")
	}
}

// A held-out set too small to discriminate yields an explicit non-verdict, never a pass. "Not enough
// unseen data" and "measured no improvement" are different facts.
func TestRetrievalHeldoutFloorIsNotAPass(t *testing.T) {
	cases := caseIDs(4)
	split := DeriveRetrievalSplit("cfg-small", cases)
	got, err := VerifyRetrievalChange(split,
		successSeries("base", cases, []int64{1, 2, 3, 4, 5}, 0.1),
		successSeries("cand", cases, []int64{1, 2, 3, 4, 5}, 0.9), evalstats.DefaultConfig())
	if err != nil {
		t.Fatalf("VerifyRetrievalChange: %v", err)
	}
	if got.Verified {
		t.Error("a verdict was declared on a held-out set below the floor; a huge apparent win on three " +
			"cases is not evidence")
	}
	if got.Reason == "" {
		t.Error("the non-verdict must carry a reason, not a bare false")
	}
}

// ── task 6.5 🔴 — the drop gate applies to retrieval too ─────────────────────────────────────────

func TestRetrievalTunePastDropToleranceInadmissible(t *testing.T) {
	base := baseSpec()
	base.Nodes["rag"] = variantspec.NodeOverride{ModelRef: refWeakModel, ContextPolicy: refCtxTopK}

	menu := contextMenu()
	// A larger top-k with a LOSSY rerank: the retrieved chunks crowd out the conversation, so the node
	// keeps less of what it was given. That is a real way a retrieval "improvement" loses information,
	// and it is why FR13 applies the drop gate to retrieval and not only to summarization.
	menu.ContextPolicies = append(menu.ContextPolicies, ContextChoice{
		Ref:    "aaaa777777777777777777777777777777777777777777777777777777777777",
		Policy: "topk", TopK: 100, Lossy: true, ExpectedDrop: 0.90,
	})

	e := Engine{Menu: menu, Base: base}
	em := e.Propose([]Target{
		{Diagnosis: diag("rag", diagnosis.CauseRetrievalMiss, "c1"), Pattern: patternclassifier.RetrievalRAG},
	})
	for _, c := range em.Candidates {
		if c.Spec.Nodes[c.NodeID].ContextPolicy == "aaaa777777777777777777777777777777777777777777777777777777777777" {
			t.Fatal("a retrieval tune estimated to drop 90% of the node's context was surfaced; a RAG node " +
				"tolerates augmentation, not the loss of the conversation it is augmenting")
		}
	}
	var refused bool
	for _, r := range em.Refusals {
		if strings.Contains(r.Reason, "90%") && strings.Contains(r.Reason, "before any eval spend") {
			refused = true
		}
	}
	if !refused {
		t.Errorf("the over-dropping retrieval tune was not recorded as a refusal: %+v", em.Refusals)
	}

	// The ordinary top-k widening on the same node is still admitted — the gate judges the drop, not the
	// operator.
	var sawNormal bool
	for _, c := range em.Candidates {
		if c.Spec.Nodes[c.NodeID].ContextPolicy == refCtxTopKBig {
			sawNormal = true
		}
	}
	if !sawNormal {
		t.Error("a lossless top-k widening was rejected alongside the lossy one; retrieval augmentation is " +
			"not loss and must not be gated as if it were")
	}
}
