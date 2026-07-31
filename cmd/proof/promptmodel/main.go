// Command promptmodel drives the P13 prompt & model optimization operators against a REAL repository
// (github.com/nousresearch/hermes-agent, the same target as cmd/proof/contracts / cmd/proof/proposals). It discovers
// the IR, then exercises the P13 additions on REAL discovered nodes:
//
//   - Wave 13a — the four deeper prompt operators (instruction_harden, few_shot_curate, prompt_compress,
//     redundancy_remove): each grounded-or-silent, each publishing a NEW content-addressed prompt
//     version (never mutating one), each carrying the cases it addresses.
//   - Wave 13b — model selection under the held-out quality guardrail: a downgrade is admissible ONLY
//     when its task-success CI overlaps the incumbent's on held-out cases, reported as an
//     equal-quality-cheaper TIE (cost win, quality tie — never a quality win); plus parameter tuning
//     materialized in bound apply mode.
//
// It NEVER executes the target — it parses it (invariant I1). What is real: the repository, the
// discovered IR (node ids, files, symbols), the operator emission, the immutable version ids, the
// guardrail verdicts, and the bound-mode binding document. What is illustrative: the per-node diagnosis
// and the held-out eval deltas (in production these come from the P4.5/P4 engines through a provider).
//
//	go run ./cmd/proof/promptmodel -repo /tmp/hermes-agent
package main

import (
	"flag"
	"fmt"
	"log"
	"sort"

	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/sourcerev"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// pinnedSHA is the commit this demo's documented output was produced from. It is VERIFIED against the
// checkout rather than trusted: labelling the IR with a commit the tree is not at would put a false
// source_revision on every number below, and source_revision is half the reproducibility key.
const pinnedSHA = "de5ece994415276d215976836161f871f1d6d8f5"

// commitSHA is the revision this run ACTUALLY parsed. It is a var, not a const, because the pin above is
// verified against the checkout rather than assumed — main() sets it from sourcerev.Resolve, which fails
// loudly instead of letting the output carry a provenance nothing can check.
var commitSHA string

// menu refs (64-hex, distinct). The param-tuned variant shares provider+model_id with the weak model.
const (
	refWeak      = "1111111111111111111111111111111111111111111111111111111111111111"
	refWeakTuned = "1111111111111111111111111111111111111111111111111111111111111112"
	refCheap     = "3333333333333333333333333333333333333333333333333333333333333333"
	refStrong    = "2222222222222222222222222222222222222222222222222222222222222222"
)

func menu() proposal.Menu {
	return proposal.Menu{Models: []proposal.ModelChoice{
		{Ref: refWeak, Provider: "anthropic", ModelID: "claude-haiku-4-5", Tier: 1, CostPerRun: 0.001, Params: map[string]any{"temperature": 0.7}},
		{Ref: refWeakTuned, Provider: "anthropic", ModelID: "claude-haiku-4-5", Tier: 1, CostPerRun: 0.001, Params: map[string]any{"temperature": 0.1}},
		{Ref: refCheap, Provider: "anthropic", ModelID: "claude-haiku-mini", Tier: 0, CostPerRun: 0.0004},
		{Ref: refStrong, Provider: "anthropic", ModelID: "claude-opus-4-8", Tier: 3, CostPerRun: 0.02},
	}}
}

func main() {
	repo := flag.String("repo", "/tmp/hermes-agent", "path to the hermes-agent checkout (read-only)")
	pin := flag.String("pin", pinnedSHA, "commit this run must be checked out at; empty means \"use HEAD and say so\"")
	flag.Parse()

	// Resolve the revision BEFORE discovery: the SHA labels the IR, and a label that does not match the
	// tree makes every number below describe a run that never happened.
	sha, note, err := sourcerev.Resolve(*repo, *pin)
	if err != nil {
		log.Fatalf("%v", err)
	}
	commitSHA = sha
	fmt.Printf("source_revision %s (%s)\n", commitSHA[:12], note)

	res, err := discovery.Run(discovery.Options{
		Repo:      *repo,
		CommitSHA: commitSHA,
		RepoURL:   "https://github.com/nousresearch/hermes-agent",
		Frontends: []discovery.LanguageFrontend{discovery.NewPythonFrontend()},
	})
	if err != nil {
		log.Fatalf("discovery: %v", err)
	}
	ir := &res.IR
	fmt.Printf("═══ P13 prompt & model optimization — run for github.com/nousresearch/hermes-agent ═══\n")
	fmt.Printf("discovered %d nodes (language=%s) at %s\n\n", len(ir.Nodes), ir.Workflow.Language, commitSHA[:12])

	promptNode, modelNode := pickNodes(ir)
	if promptNode == "" || modelNode == "" {
		log.Fatalf("could not find a prompt-bearing and a model-bearing node among %d discovered nodes", len(ir.Nodes))
	}
	fmt.Printf("prompt node: %s   model node: %s\n\n", short(promptNode), short(modelNode))

	base := &variantspec.VariantSpec{
		WorkflowID: "nousresearch/hermes-agent", SourceRevision: commitSHA,
		Order: []string{promptNode, modelNode},
		Nodes: map[string]variantspec.NodeOverride{modelNode: {ModelRef: refWeak}},
		Edges: []variantspec.Edge{{FromNodeID: promptNode, ToNodeID: modelNode, Kind: "data"}},
	}
	eng := proposal.Engine{Menu: menu(), Base: base, Optimizer: proposal.SelfRefineOptimizer{}}

	wave13aPromptOperators(eng, promptNode)
	wave13bModelSelection(eng, modelNode)
	paramTuneBoundMode(modelNode, *repo)

	fmt.Printf("\n✔ P13 run complete on the real hermes-agent IR. Diagnosis inputs and held-out deltas are\n")
	fmt.Printf("  illustrative; the operator emission, immutable version ids, guardrail verdicts, and the\n")
	fmt.Printf("  bound-mode binding document above are the shipped code paths.\n")
}

// ── Wave 13a: the four deeper prompt operators, grounded-or-silent, on a real node ─────────────────
func wave13aPromptOperators(eng proposal.Engine, node string) {
	fmt.Printf("── Wave 13a · deeper prompt operators (grounded, new immutable version each) ──\n")
	// A bloated + redundant + exemplar-carrying prompt body so every operator has something to do. In
	// production this is the discovered prompt; here it is illustrative (hermes assembles prompts at
	// runtime), while the grounding and immutable-publish logic below is the real shipped path.
	body := "Answer the user's question.\n" +
		"Answer the user's question.\n" + // redundant duplicate line
		"Example: Q->A\n" +
		"Example: Q->A\n" + // dead duplicate exemplar
		"Be concise.   \n\n\n\n" // trailing whitespace + blank runs to compress

	target := proposal.Target{
		Diagnosis: diagnosis.Diagnosis{
			DiagID: "p45://prompt_format_drift", NodeID: node, TaxonomyCode: diagnosis.CausePromptFormatDrift,
			Confidence: 0.8, EvidenceCaseIDs: []string{"trace-001", "trace-014"}, Source: diagnosis.SourceRule,
		},
		Pattern:        patternclassifier.PromptChaining,
		BasePromptBody: body,
		RequiredFields: []string{"answer"},
		Groundings: []proposal.FailingCaseGrounding{
			{CaseID: "trace-001", FailureReason: "under-specified: omitted required step", TraceRef: hash64('a')},
			{CaseID: "trace-014", FailureReason: "output missing field `answer`", TraceRef: hash64('b')},
		},
	}
	em := eng.Propose([]proposal.Target{target})

	found := map[proposal.OperatorKind]proposal.Candidate{}
	for _, c := range em.Candidates {
		found[c.Operator] = c
	}
	for _, op := range []proposal.OperatorKind{
		proposal.OpInstructionHarden, proposal.OpFewShotCurate, proposal.OpPromptCompress, proposal.OpRedundancyRemove,
	} {
		c, ok := found[op]
		if !ok {
			fmt.Printf("  %-20s (silent — nothing grounded to change)\n", op)
			continue
		}
		ref := c.Spec.Nodes[node].PromptRef
		var cases []string
		if c.Grounding != nil {
			for _, gc := range c.Grounding.Cases {
				cases = append(cases, gc.CaseID)
			}
		}
		fmt.Printf("  %-20s new version %s  grounded in %v\n", op, ref[:12]+"…", cases)
	}
	fmt.Printf("  → each emits a NEW content-addressed PromptRef; the parent stays resolvable (immutability).\n\n")
}

// ── Wave 13b: model downgrade under the held-out quality guardrail ─────────────────────────────────
func wave13bModelSelection(eng proposal.Engine, node string) {
	fmt.Printf("── Wave 13b · model downgrade under the held-out quality guardrail ──\n")
	em := eng.Propose([]proposal.Target{{
		Diagnosis: diagnosis.Diagnosis{NodeID: node, EvidenceCaseIDs: []string{"trace-002"}},
		Signal:    proposal.SignalCostBottleneck, Pattern: patternclassifier.Routing,
	}})
	var down *proposal.Candidate
	for i := range em.Candidates {
		if em.Candidates[i].Operator == proposal.OpModelDowngrade {
			down = &em.Candidates[i]
			break
		}
	}
	if down == nil {
		fmt.Printf("  (no downgrade candidate emitted)\n\n")
		return
	}
	fmt.Printf("  candidate: %s → %s   GuardrailRequired=%v\n", proposal.OpModelDowngrade,
		short(down.Spec.Nodes[node].ModelRef), down.GuardrailRequired)

	cfg := evalstats.DefaultConfig()
	cases := caseIDs(30)

	// (i) held-out CIs OVERLAP → admissible, equal-quality-cheaper tie.
	inc := series("incumbent", cases, 0.90)
	cheapTie := series("cheaper", cases, 0.90)
	r := proposal.EvaluateDowngradeGuardrail("cfg-hermes-overlap", inc, cheapTie, cfg)
	out := proposal.ClassifyDowngrade(r, 0.02, 0.0004)
	fmt.Printf("  guardrail (overlap):     verdict=%s  held_out=%d  → cost_win=%v quality_tie=%v quality_win=%v\n",
		r.Verdict, len(r.HeldOut), out.CostWin, out.QualityTie, out.QualityWin)

	// (ii) held-out CIs do NOT overlap → inadmissible, even though the model is far cheaper.
	cheapWorse := series("cheaper", cases, 0.45)
	r2 := proposal.EvaluateDowngradeGuardrail("cfg-hermes-nooverlap", inc, cheapWorse, cfg)
	out2 := proposal.ClassifyDowngrade(r2, 0.02, 0.0004)
	fmt.Printf("  guardrail (no overlap):  verdict=%s  held_out=%d  → admissible=%v (lower cost does not rescue it)\n",
		r2.Verdict, len(r2.HeldOut), out2.Admissible)
	fmt.Printf("  → a downgrade is a cost win and a quality TIE, never a quality win; a regressing one is refused.\n\n")
}

// ── param tuning materializes in bound apply mode; inline is refused ───────────────────────────────
func paramTuneBoundMode(node, repo string) {
	fmt.Printf("── Wave 13b · parameter tuning (bound apply mode) ──\n")

	// Bound: the tuned params materialize as data in the binding document.
	resolved := &variantspec.Resolved{
		SourceRevision: commitSHA, ConfigHash: "cfg-hermes-param", Language: "python",
		Config: variantspec.ResolvedConfig{IRVersion: "1.0.0", Nodes: []variantspec.ResolvedNode{{
			NodeID: node, ModelRef: "anthropic/claude-haiku-4-5",
			ProviderParams: map[string]any{"temperature": 0.1},
		}}},
		Overrides:  map[string]variantspec.ResolvedOverride{node: {ParamTune: true}},
		ApplyModes: map[string]variantspec.ApplyMode{node: variantspec.ApplyBound},
	}
	files, err := transform.GenerateBoundArtifacts(resolved)
	if err != nil {
		fmt.Printf("  bound materialization error: %v\n", err)
		return
	}
	if doc := files["agentcfg/bindings.json"]; doc != nil {
		fmt.Printf("  bound: temperature materialized as data in agentcfg/bindings.json (%d bytes)\n", len(doc))
	}

	// Inline: the same param tune has no call-site rewriter and is REFUSED (never silently dropped).
	inline := &variantspec.Resolved{
		SourceRevision: commitSHA, ConfigHash: "cfg-hermes-param-inline", Language: "python",
		Overrides: map[string]variantspec.ResolvedOverride{node: {ParamTune: true}},
	}
	// The repo under test, NOT a hard-coded path: a refusal produced against some other directory
	// would be a refusal about a tree this run never discovered, and on a machine where that path does
	// not exist it would "pass" for the wrong reason entirely — a vacuous demonstration of the gate.
	if _, err := transform.Generate(inline, repo); err != nil {
		fmt.Printf("  inline: refused with a named cause (expected) — %s\n", firstClause(err.Error()))
	} else {
		fmt.Printf("  inline: WARNING — expected a refusal for an inline param tune, got none\n")
	}
	fmt.Printf("  → a param tune hashes through provider_params and is carried where it can be applied honestly.\n")
}

// ── helpers ────────────────────────────────────────────────────────────────────────────────────────

// pickNodes returns the lowest-id node with a non-empty discovered prompt, and the lowest-id node with a
// model id — deterministic choices over the real IR.
func pickNodes(ir *discovery.IR) (promptNode, modelNode string) {
	ids := make([]string, 0, len(ir.Nodes))
	byID := map[string]discovery.IRNode{}
	for _, n := range ir.Nodes {
		ids = append(ids, n.NodeID)
		byID[n.NodeID] = n
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return "", ""
	}

	// Pick the prompt node FIRST and resolve its fallback immediately. On this repo no node has a
	// statically-resolved prompt (hermes assembles them at runtime), so the fallback is the normal
	// path — and it has to run BEFORE the model loop, or that loop's "not the prompt node" guard would
	// compare against an empty string, match nothing, and hand back the same node for both roles.
	for _, id := range ids {
		if byID[id].Prompt.Inline != "" {
			promptNode = id
			break
		}
	}
	if promptNode == "" {
		promptNode = ids[0]
	}

	// The model node is a DIFFERENT node, so the demo spec has two nodes and one real edge between
	// them — a prompt change and a model change on the same node would be one node's two dimensions,
	// which hides that the operators are independently admissible.
	for _, id := range ids {
		if id != promptNode && byID[id].Model.ModelID != "" {
			modelNode = id
			break
		}
	}
	if modelNode == "" {
		for _, id := range ids {
			if id != promptNode {
				modelNode = id
				break
			}
		}
	}
	return promptNode, modelNode
}

func series(variant string, cases []string, v float64) evalstats.Series {
	s := evalstats.Series{VariantID: variant, Metric: "task_success"}
	for _, c := range cases {
		for _, seed := range []int64{1, 2, 3, 4, 5} {
			s.Obs = append(s.Obs, evalstats.Observation{CaseID: c, Seed: seed, Value: v})
		}
	}
	return s
}

func caseIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("case-%03d", i)
	}
	return out
}

// short truncates an id for display. Discovered hermes node ids share a long common prefix, so the
// window has to be wide enough that two DIFFERENT nodes do not render as the same string.
func short(s string) string {
	if len(s) > 22 {
		return s[:22] + "…"
	}
	return s
}

func hash64(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func firstClause(s string) string {
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}
