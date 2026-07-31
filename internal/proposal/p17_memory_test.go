package proposal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P17 §8 — the memory operator (Step 8 of the "add an axis" checklist).
//
// The hardest thing to get right here is not the emission — it is the CLAIM. At M20 the transform
// refuses every memory rewrite, so a memory candidate can never be verified, so it can never be a win.
// Half this file exists to make that impossible to accidentally undo.

const (
	refMemScratch = "1111111111111111111111111111111111111111111111111111111111111111"
	refMemSummary = "2222222222222222222222222222222222222222222222222222222222222222"
	refMemVector  = "3333333333333333333333333333333333333333333333333333333333333333"
	refMemNone    = "4444444444444444444444444444444444444444444444444444444444444444"
)

func memoryMenu() Menu {
	return Menu{
		MemoryStrategies: []MemoryChoice{
			{Ref: refMemScratch, Strategy: "scratchpad", Title: "Scratchpad"},
			{Ref: refMemSummary, Strategy: "summary-buffer", Title: "Rolling summary"},
			{Ref: refMemVector, Strategy: "vector-recall", Title: "Vector recall"},
			{Ref: refMemNone, Strategy: "none", Title: "No memory"},
		},
	}
}

func memoryBase() *variantspec.VariantSpec {
	return &variantspec.VariantSpec{
		WorkflowID: "wf-mem", SourceRevision: "rev1",
		Order: []string{"recall", "answer"},
		Nodes: map[string]variantspec.NodeOverride{
			"recall": {MemoryRef: refMemScratch},
		},
	}
}

func memoryInput(signal Signal) OperatorInput {
	return OperatorInput{
		Diagnosis: diagnosis.Diagnosis{DiagID: "d1", NodeID: "recall", Confidence: 0.8,
			EvidenceCaseIDs: []string{"c1", "c2"}, Source: diagnosis.SourceRule},
		Signal:  signal,
		Pattern: patternclassifier.MemoryManagement,
		Base:    memoryBase(),
		Menu:    memoryMenu(),
	}
}

// TestOpMemoryPolicyKindStable — task 8.1. The kind is the wire value stored on a proposal row, so it
// is a stable string. Renaming it orphans every stored proposal.
func TestOpMemoryPolicyKindStable(t *testing.T) {
	if OpMemoryPolicy != "memory_policy_switch" {
		t.Fatalf("OpMemoryPolicy = %q, want memory_policy_switch: the kind is stored on proposal rows, so "+
			"a rename orphans every row already written", OpMemoryPolicy)
	}
	// It is its OWN kind, not a mode of the context switch (decisions.md D2).
	if OpMemoryPolicy == OpContextPolicy {
		t.Fatal("the memory and context operators share a kind; a consumer could not tell a cross-invocation " +
			"change from a within-call one")
	}
	// The signal spellings are the classifier's own failure modes, lifted verbatim. If these drifted, the
	// vocabulary that DETECTS a memory problem and the one that DRIVES a proposal would need a translation
	// layer, and a translation layer is a thing that can be wrong.
	ms, ok := patternclassifier.MetricSetFor(patternclassifier.MemoryManagement)
	if !ok {
		t.Fatal("MemoryManagement has no metric set; the axis would have nothing to be scored against")
	}
	modes := ms.FailureModes
	for _, sig := range []Signal{SignalStaleMemory, SignalContradictoryMemory} {
		found := false
		for _, m := range modes {
			if m == string(sig) {
				found = true
			}
		}
		if !found {
			t.Errorf("signal %q is not one of the classifier's MemoryManagement failure modes %v; the two "+
				"vocabularies must be the same vocabulary", sig, modes)
		}
	}
	// And the local `none` spelling matches the registry's, so memoryStrategiesExcept cannot silently
	// stop excluding the identity strategy.
	if memoryStrategyNone != registry.StrategyNone {
		t.Fatalf("memoryStrategyNone = %q but registry.StrategyNone = %q; the target-exclusion rule would "+
			"stop firing", memoryStrategyNone, registry.StrategyNone)
	}
}

// TestCatalogHasMemoryPolicyRow — task 8.2. A reserved constant with no catalog row is a vocabulary
// entry nothing can ever emit.
func TestCatalogHasMemoryPolicyRow(t *testing.T) {
	var rows []Operator
	for _, op := range DefaultCatalog() {
		if op.Kind() == OpMemoryPolicy {
			rows = append(rows, op)
		}
	}
	if len(rows) == 0 {
		t.Fatal("OpMemoryPolicy is not registered in DefaultCatalog(); a constant with no row can never emit " +
			"a candidate, which would make the whole operator decorative")
	}

	// One row per memory failure mode, because `fires` matches a single Signal per row.
	seen := map[Signal]bool{}
	for _, op := range rows {
		seen[op.HandlesSignal()] = true
		// 🔴 Gated to MemoryManagement. A memory swap on a node that keeps no memory is a change with no
		// mechanism: it would resolve, hash, occupy a verification slot, and measure nothing.
		pats := op.AdmissiblePatterns()
		if len(pats) != 1 || pats[0] != patternclassifier.MemoryManagement {
			t.Errorf("the memory operator's admissible patterns are %v, want exactly [memory_management]", pats)
		}
		// It is signal-driven, not taxonomy-driven: "the agent recalled something stale" is a property of
		// the strategy across turns, not of any one failing case.
		if len(op.Handles()) != 0 {
			t.Errorf("the memory operator claims taxonomy codes %v; it is signal-driven", op.Handles())
		}
	}
	for _, want := range []Signal{SignalStaleMemory, SignalContradictoryMemory} {
		if !seen[want] {
			t.Errorf("no catalog row handles signal %q; that memory failure mode would drive nothing", want)
		}
	}

	// And both rows actually FIRE on their signal against a MemoryManagement node.
	for _, sig := range []Signal{SignalStaleMemory, SignalContradictoryMemory} {
		fired := false
		for _, op := range DefaultCatalog() {
			if fires(op, Target{Signal: sig, Pattern: patternclassifier.MemoryManagement,
				Diagnosis: diagnosis.Diagnosis{NodeID: "recall"}}) && op.Kind() == OpMemoryPolicy {
				fired = true
			}
		}
		if !fired {
			t.Errorf("signal %q fires no memory operator", sig)
		}
	}
}

// TestMemoryPolicyPriorAndOrderHint — task 8.3. A prior and an order hint are ORDERING hints, never
// results. On this axis that distinction is permanent, not temporary: with the transform refusing, the
// prior will never be replaced by a measured verdict, so anything read off it is read off it forever.
func TestMemoryPolicyPriorAndOrderHint(t *testing.T) {
	prior, ok := operatorPrior[OpMemoryPolicy]
	if !ok {
		t.Fatal("OpMemoryPolicy has no operatorPrior; expectedGain would be 0 and every memory candidate " +
			"would sort last for a reason that has nothing to do with memory")
	}
	if prior <= 0 || prior >= 1 {
		t.Errorf("operatorPrior[OpMemoryPolicy] = %v, want a coarse fraction in (0,1)", prior)
	}

	hint, ok := verifyOrderHint[OpMemoryPolicy]
	if !ok {
		t.Fatal("OpMemoryPolicy has no verifyOrderHint")
	}
	if hint < 0 {
		t.Errorf("verifyOrderHint[OpMemoryPolicy] = %d, want a non-negative cost class", hint)
	}
	// 🔴 It is a COST class, so it must not be pushed to the back "because it will be refused anyway".
	// That would encode applicability in a cost hint — two different facts in one number.
	if hint > verifyOrderHint[OpPromptRewrite] {
		t.Errorf("the memory operator sorts after the most expensive operator (prompt rewrite); this table "+
			"answers \"how expensive to verify\", not \"can it be verified\" — the refusal belongs at the "+
			"transform, typed (got %d vs %d)", hint, verifyOrderHint[OpPromptRewrite])
	}
	if VerifyOrderHint(OpMemoryPolicy) != hint {
		t.Errorf("VerifyOrderHint disagrees with the table")
	}
}

// TestMemoryProposalClaimsNoOutcome — P18 §6.1, replacing P17's wording assertion.
//
// 🔴 P17 asserted the rationale SAYS "refused ... not applied ... no measured result". That was true of
// every memory candidate then, and is false of some of them now that P18 gave the axis materializers. A
// rationale asserting a stale outcome is worse than one asserting none — it is read as current.
//
// So the assertion inverts: the rationale must claim NO outcome, in either direction. Whether a given
// call site materializes is the compile step's answer, and the refused-not-scored guarantee is enforced
// by BuildStatus/Surfaceable rather than by wording (TestMemoryProposalRefusedNotScored below).
func TestMemoryProposalClaimsNoOutcome(t *testing.T) {
	op := memoryPolicyOp{signal: SignalStaleMemory}
	cands, err := op.Propose(memoryInput(SignalStaleMemory))
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(cands) == 0 {
		t.Fatal("the memory operator emitted nothing against a memory bottleneck with a populated menu")
	}

	for _, c := range cands {
		if c.Operator != OpMemoryPolicy {
			t.Errorf("candidate carries operator %q, want %q", c.Operator, OpMemoryPolicy)
		}
		if len(c.Dimensions) != 1 || c.Dimensions[0] != string(variantspec.DimMemory) {
			t.Errorf("candidate changes dimensions %v, want exactly [memory]", c.Dimensions)
		}
		ov := c.Spec.Nodes["recall"]
		if ov.MemoryRef == "" || ov.MemoryRef == refMemScratch {
			t.Errorf("candidate did not change the node's memory ref (got %q)", ov.MemoryRef)
		}
		// 🚫 `none` is never a proposed TARGET: answering "your recall is stale" with "recall nothing"
		// removes the capability being diagnosed.
		if ov.MemoryRef == refMemNone {
			t.Error("the operator proposed `none` as a fix for a memory bottleneck")
		}
		if memoryBase().Nodes["recall"].MemoryRef != refMemScratch {
			t.Error("Propose mutated the baseline spec")
		}

		low := strings.ToLower(c.Rationale)
		// 🚫 No outcome claimed in EITHER direction. Not a win …
		for _, word := range []string{"improve", "gain of", "reduces staleness", "win", "faster", "cheaper"} {
			if strings.Contains(low, word) {
				t.Errorf("the rationale claims a positive outcome (%q) that only verification can decide: %q",
					word, c.Rationale)
			}
		}
		// … and not a refusal either, because on a covered cell it is not refused.
		for _, word := range []string{"refused", "not applied", "no measured result"} {
			if strings.Contains(low, word) {
				t.Errorf("the rationale asserts a refusal (%q), which is stale on a cell that materializes. "+
					"The outcome belongs to Compiled.BuildStatus, which the operator cannot see: %q",
					word, c.Rationale)
			}
		}
		// It DOES name the precondition, which is a property of the change rather than of the outcome.
		if !strings.Contains(low, "read and a write") && !strings.Contains(low, "read AND a write") {
			t.Errorf("the rationale does not name the both-halves precondition: %q", c.Rationale)
		}
		if c.GuardrailRequired {
			t.Error("a memory candidate requires the held-out downgrade guardrail, which is about model " +
				"cost/quality ties and has no meaning here")
		}
	}
}

// TestMemoryProposalRefusedNotScored — task 6.2 🚫. The honesty contract SURVIVES the capability.
//
// 🔴 This is the test that matters most in §6. Waking the operator must not make a refused memory change
// scorable: on a cell with no materializer the candidate must come back BuildRefused, must not be
// Surfaceable, and must therefore never reach verification — so it cannot be a win, a regression, or a
// tie. That is enforced by the compile path's own machinery, not by anything the operator says.
func TestMemoryProposalRefusedNotScored(t *testing.T) {
	cands, err := memoryPolicyOp{signal: SignalStaleMemory}.Propose(memoryInput(SignalStaleMemory))
	if err != nil || len(cands) == 0 {
		t.Fatalf("Propose: %v (%d candidates)", err, len(cands))
	}

	// A language with no memory materializer at all.
	root := t.TempDir()
	c := Compiler{
		Resolver: memoryResolverFor(t, "rust"),
		Root:     root,
		Build:    alwaysBuilds{},
	}
	got, err := c.Compile(context.Background(), cands[0])
	if err != nil {
		t.Fatalf("Compile returned an error rather than a verdict: %v. A refusal is a verdict about the "+
			"candidate, not a failure of the compiler — returning an error would abort the whole batch "+
			"over one declined change", err)
	}
	if got.BuildStatus != BuildRefused {
		t.Fatalf("BuildStatus = %q, want %q on a cell with no materializer", got.BuildStatus, BuildRefused)
	}
	if got.Surfaceable() {
		t.Fatal("a refused memory candidate is Surfaceable, so it would be recommended and could be scored")
	}
	if !got.Refusal.Refused() {
		t.Error("the compiled candidate carries no refusal, so nobody can tell why it was declined")
	}
	if got.Refusal.Dimension != string(variantspec.DimMemory) {
		t.Errorf("the refusal names dimension %q, want memory", got.Refusal.Dimension)
	}
	if got.Refusal.Reason == "" {
		t.Error("the refusal carries no reason")
	}
	// 🚫 And it is not credited to the operator as an outcome, because it produced none.
	credits := OperatorCredits([]Candidate{got.Candidate}, func(Candidate) bool { return false })
	if credits[OpMemoryPolicy].Won != 0 {
		t.Error("a refused memory candidate was credited as a win")
	}
}

// TestMemoryProposalCompilesWhereMaterializable — task 6.1 🔴. The operator is awake.
//
// P17 catalogued OpMemoryPolicy DORMANT: every candidate it emitted was refused at transform, so none
// could reach an eval and none could be scored. P18 shipped the materializers, and this asserts the
// consequence end-to-end — a memory candidate on a COVERED cell compiles to a real diff carrying BOTH
// halves, builds, and is Surfaceable, which is what makes it eligible for verification.
//
// 🔴 It goes through the real Compiler and the real transform. A test that stubbed the codemod would
// prove the plumbing and not the capability, and the capability is the whole of §6.
func TestMemoryProposalCompilesWhereMaterializable(t *testing.T) {
	// A Python call site that writes its message list and assigns the call's result — both halves land.
	const src = `import openai

client = openai.OpenAI()


def chat(question):
    resp = client.chat.completions.create(
        model="gpt-4o",
        messages=[{"role": "user", "content": question}],
    )
    return resp
`
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pipeline.py"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	sites, err := discovery.IndexSpanCallSites(root, "python", nil)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	var nodeID string
	for id := range sites {
		nodeID = id
	}
	if nodeID == "" {
		t.Fatal("discovery found no python call site; the assertion below would pass for the wrong reason")
	}

	// The operator proposes against that node.
	in := memoryInput(SignalStaleMemory)
	in.Diagnosis.NodeID = nodeID
	in.Base = &variantspec.VariantSpec{
		WorkflowID: "wf-mem", SourceRevision: "rev1",
		Order: []string{nodeID},
		Nodes: map[string]variantspec.NodeOverride{nodeID: {MemoryRef: refMemScratch}},
	}
	cands, err := memoryPolicyOp{signal: SignalStaleMemory}.Propose(in)
	if err != nil || len(cands) == 0 {
		t.Fatalf("Propose: %v (%d candidates)", err, len(cands))
	}

	c := Compiler{Resolver: memoryResolverFor(t, "python"), Root: root, Build: alwaysBuilds{}}
	got, err := c.Compile(context.Background(), cands[0])
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if got.BuildStatus != BuildBuilt {
		t.Fatalf("BuildStatus = %q, want %q. The operator is supposed to be AWAKE on a covered cell: a "+
			"candidate that still cannot compile could never be verified, and the axis would remain "+
			"unscorable.\nrefusal: %+v", got.BuildStatus, BuildBuilt, got.Refusal)
	}
	if !got.Surfaceable() {
		t.Fatal("a materialized memory candidate is not Surfaceable, so it would never reach verification")
	}
	if got.Patch == nil || len(got.Patch.Diff) == 0 {
		t.Fatal("the compiled candidate carries no diff")
	}

	after := string(got.Patch.Files["pipeline.py"])
	// 🔴 BOTH halves in the emitted diff. A recall without a record reads a store nothing fills, which
	// behaves as `none` under this candidate's config_hash — and would then be SCORED as the strategy.
	if !strings.Contains(after, "agentmem.recall(") || !strings.Contains(after, "agentmem.record(") {
		t.Fatalf("the compiled diff does not carry both halves:\n%s", after)
	}
	if got.ConfigHash == "" {
		t.Error("the compiled candidate carries no config_hash to be scored under")
	}
	// And it IS creditable to the operator now — which is exactly what dormancy denied.
	if op, ok := CreditedOperator(got.Candidate); !ok || op != OpMemoryPolicy {
		t.Errorf("a materialized memory candidate is not credited to the memory operator (%q, %v)", op, ok)
	}
}

// TestMemorySignalUsesExistingMetricSet — task 8.5. No new metric, no taxonomy change. The improvement
// signal, when it is finally scorable, is the classifier's existing MemoryManagement metric set.
func TestMemorySignalUsesExistingMetricSet(t *testing.T) {
	ms, ok := patternclassifier.MetricSetFor(patternclassifier.MemoryManagement)
	if !ok {
		t.Fatal("MemoryManagement has no metric set")
	}

	if ms.Primary != "memory_hit_rate" {
		t.Errorf("the MemoryManagement primary metric is %q, want memory_hit_rate; P17 adds no metric and "+
			"must not have moved this one", ms.Primary)
	}
	want := []string{"memory_hit_rate", "recall_precision", "staleness", "write_amplification"}
	if len(ms.Metrics) != len(want) {
		t.Fatalf("the MemoryManagement metric set is %v, want %v — P17 adds NO new metric; a number invented "+
			"to flatter the axis is exactly what the phase declines", ms.Metrics, want)
	}
	for i := range want {
		if ms.Metrics[i] != want[i] {
			t.Errorf("metric %d is %q, want %q", i, ms.Metrics[i], want[i])
		}
	}

	// 🚫 And the taxonomy is untouched: memory was already pattern 8, and P17 neither adds a pattern nor
	// bumps the version.
	if patternclassifier.TaxonomySize != 20 {
		t.Errorf("TaxonomySize is %d, want 20; P17 CONSUMES the existing MemoryManagement pattern and adds "+
			"none", patternclassifier.TaxonomySize)
	}
	if !patternclassifier.InTaxonomy(patternclassifier.MemoryManagement) {
		t.Error("MemoryManagement left the taxonomy")
	}
}

// TestMemoryOperatorDeclinesWithoutAMenu is the grounded-or-silent rule this catalog applies everywhere:
// with nothing to swap TO, the operator emits nothing rather than inventing a strategy.
func TestMemoryOperatorDeclinesWithoutAMenu(t *testing.T) {
	in := memoryInput(SignalStaleMemory)
	in.Menu = Menu{}
	cands, err := memoryPolicyOp{signal: SignalStaleMemory}.Propose(in)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("the operator emitted %d candidate(s) with an empty menu; a strategy it invented would be "+
			"a ref that resolves to nothing", len(cands))
	}

	// A menu holding ONLY the strategy the node already binds also yields nothing — proposing the current
	// configuration as a change is a candidate that costs a verification slot and can differ from the
	// baseline in nothing at all.
	in.Menu = Menu{MemoryStrategies: []MemoryChoice{{Ref: refMemScratch, Strategy: "scratchpad"}}}
	cands, err = memoryPolicyOp{signal: SignalStaleMemory}.Propose(in)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("the operator proposed the strategy the node already binds (%d candidates)", len(cands))
	}
}

// ── compile-path doubles ─────────────────────────────────────────────────────────────────────────

// memoryResolverFor resolves a candidate to a config whose LANGUAGE decides which memory cell it lands
// in. That is the only fact the compile path needs from resolution here — the refusal it exercises is
// the transform's, not the registry's.
func memoryResolverFor(t *testing.T, language string) Resolver {
	t.Helper()
	return memResolver{t: t, language: language}
}

type memResolver struct {
	t        *testing.T
	language string
}

func (r memResolver) Resolve(spec *variantspec.VariantSpec) (*variantspec.Resolved, error) {
	st := registry.MemoryStrategyNamed("summary-buffer")
	if st == nil {
		r.t.Fatal("summary-buffer is not a builtin strategy")
	}
	out := &variantspec.Resolved{
		ConfigHash: strings.Repeat("c", 64), SourceRevision: "rev1", Language: r.language,
		Overrides: map[string]variantspec.ResolvedOverride{},
	}
	for nodeID, ov := range spec.Nodes {
		if ov.MemoryRef == "" {
			continue
		}
		out.Overrides[nodeID] = variantspec.ResolvedOverride{
			Memory: &registry.MemoryEntry{
				VersionID: ov.MemoryRef, Name: "m",
				Spec:     registry.MemorySpec{Strategy: "summary-buffer", Params: json.RawMessage(`{"max_tokens":2000}`)},
				Strategy: st,
			},
		}
	}
	return out, nil
}

// alwaysBuilds stands in for the build gate. The refusal under test happens BEFORE the build, so a
// checker that always passes proves the refusal is the transform's rather than a build failure wearing
// its name.
type alwaysBuilds struct{}

func (alwaysBuilds) Check(context.Context, *transform.Patch) (BuildResult, error) {
	return BuildResult{Builds: true}, nil
}
