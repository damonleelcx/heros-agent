// Command nodewiring drives the P15 workflow / node-wiring optimization against a REAL repository
// (github.com/nousresearch/hermes-agent, the same target as cmd/proof/contracts / p13hermes / p14hermes). It
// discovers the IR and then exercises every P15 code path this repository can actually reach.
//
// # What this run proves, and the two things it deliberately does NOT
//
// The wiring axis is the one axis that needs NOTHING new to be modeled: `VariantSpec.Order` and
// `Edges` are already identity-bearing in `config_hash`, so every result below is a real hash over a
// real 40-node graph, computed by the shipped resolver. What P15 adds is the operators that produce
// candidates in that space, one gate they all pass through, and an honest refusal for the source
// materialization that does not exist yet.
//
// Concretely, all of the following are the shipped code paths running on the real IR:
//
//	§1   the Dimension enum is unchanged — the axis added no dimension, kind, field, or table;
//	§2   mergeOp fuses a real ADJACENT pair, drops the absorbed node, rewires its edges (D-1);
//	§3   reorderOp proposes free reordering of data-independent neighbours, bounded and deterministic;
//	§3   pruneOp drops a real node and rewires its neighbours;
//	§2/3 every candidate resolves to a REAL config_hash that differs from the baseline's;
//	§5   an ordering that puts a data consumer before its producer is REJECTED at compile — no runnable
//	     spec, so no diff, codemod, or PR can exist;
//	§4   🔴 the headline: a wiring-differing spec is REFUSED at transform, by name, with no diff;
//	§6   an unverified merge is WITHHELD by the verification gate, and a verified one is recommended.
//
// The two boundaries, stated rather than worked around:
//
//	the discovered IR carries NO edges. hermes-agent's Python frontend is syntactic — it records call
//	sites, not data flow — so every adjacent pair is data-independent and the graph has nothing to
//	parallelize. The §5 rejection therefore uses a data edge the SPEC declares between two REAL nodes,
//	which is exactly how a re-arrangement authors one; the nodes, their contracts, and the verdict are
//	real, the edge is authored.
//
//	the adapted verdict is UNREACHABLE here. Every node's io_contract is the permissive `{"type":
//	"object"}` the syntactic frontend can honestly state, so no producer→consumer mismatch exists for
//	the catalog to bridge. That path is exercised in the unit suite (TestAdaptedVerdictRecordsAdapter,
//	TestAdapterIsInReviewableDiff) and is reported here as not-applicable rather than simulated.
//
// It NEVER executes the target — it parses it (invariant I1). What is real: the repository, the
// discovered IR, the operators, the gate verdicts, the config hashes, and the transform refusal. What
// is illustrative: the redundancy/lost-in-middle signals (in production those come from P4.5) and the
// eval deltas in §6 (in production those come from P4 through a provider).
//
//	go run ./cmd/proof/nodewiring -repo /tmp/hermes-agent
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sourcerev"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/typedcontract"
	"github.com/heros-foreal/agentd/internal/variantspec"
	"github.com/heros-foreal/agentd/internal/verification"
)

const repoURL = "https://github.com/nousresearch/hermes-agent"

func main() {
	repo := flag.String("repo", "/tmp/hermes-agent", "path to the hermes-agent checkout (read-only)")
	pin := flag.String("pin", "", `commit this run must be checked out at; empty means "use HEAD and say so"`)
	flag.Parse()

	// The revision is RESOLVED against the checkout, never asserted: source_revision is half of the
	// reproducibility key, and a SHA that does not match the tree would put a false one on every hash
	// printed below.
	commitSHA, revNote, err := sourcerev.Resolve(*repo, *pin)
	if err != nil {
		log.Fatalf("%v", err)
	}

	res, err := discovery.Run(discovery.Options{
		Repo:      *repo,
		CommitSHA: commitSHA,
		RepoURL:   repoURL,
		Frontends: []discovery.LanguageFrontend{discovery.NewPythonFrontend()},
	})
	if err != nil {
		log.Fatalf("discovery: %v", err)
	}
	ir := &res.IR

	fmt.Printf("=== P15 workflow / node-wiring optimization - run for %s ===\n", repoURL)
	fmt.Printf("discovered %d nodes, %d edges (language=%s) at %s (%s)\n\n",
		len(ir.Nodes), len(ir.Edges), ir.Workflow.Language, short(commitSHA), revNote)

	base := baselineFrom(ir, commitSHA)
	if len(base.Order) < 4 {
		log.Fatalf("this demonstration needs at least 4 discovered nodes, got %d", len(base.Order))
	}
	fmt.Printf("baseline graph: %d ordered nodes, %d edges\n", len(base.Order), len(base.Edges))
	for i, id := range base.Order[:4] {
		fmt.Printf("  [%d] %s  %s\n", i, short(id), siteOf(ir, id))
	}
	fmt.Printf("  ... %d more\n\n", len(base.Order)-4)

	noNewModeling()
	baseHash := hashOf(ir, base, "baseline")
	cands := operators(ir, base, baseHash)
	rejectAtCompile(ir, base)
	adapterBoundary(ir)
	refuseAtTransform(ir, base, cands, *repo)
	determinism(ir, base, baseHash)
	verificationGate()
	swapSurvey(ir, base, *repo)
	notDeclined(ir, *repo)

	fmt.Printf("\n=== P15 run complete on the real hermes-agent IR ===\n")
	fmt.Printf("The axis is PRODUCED, GATED, HASHED, VERIFICATION-GATED and — for one shape, an exchange of two\n")
	fmt.Printf("adjacent sibling statements — MATERIALIZED as source. No pair in this repository is that shape,\n")
	fmt.Printf("and every refusal above names the condition that blocked it rather than reporting a bare no.\n")
}

// ── §1 — the axis added nothing to model ─────────────────────────────────────────────────────────

func noNewModeling() {
	fmt.Printf("-- §1 - the wiring axis added NO dimension, kind, field, or table (task 1.4) --\n")
	names := make([]string, 0, len(variantspec.Dimensions()))
	for _, d := range variantspec.Dimensions() {
		names = append(names, string(d))
	}
	fmt.Printf("  Dimension enum: [%s]\n", strings.Join(names, " "))
	fmt.Printf("  -> the axis is VariantSpec.Order/Edges, which config_hash already covers. A `wiring`\n")
	fmt.Printf("     dimension would be a SECOND representation of the same thing, and every rewriter\n")
	fmt.Printf("     would answer \"no rewriter for this dimension\" for a change the spec already carries.\n\n")
}

// ── §2 / §3 — the operators, on real nodes ───────────────────────────────────────────────────────

func operators(ir *discovery.IR, base *variantspec.VariantSpec, baseHash string) []proposal.Candidate {
	fmt.Printf("-- §2/§3 - merge, free reorder and prune on real discovered nodes --\n")

	absorbed := base.Order[2]
	survivor := base.Order[1] // the absorbed node's predecessor: D-1's adjacent pair
	fmt.Printf("  signal: redundant node %s (adjacent survivor %s)\n", short(absorbed), short(survivor))

	eng := proposal.Engine{
		Base: base, BaseVariantID: baseHash, IR: ir,
		Gate: proposal.NewTypedContractGate(ir), // the ONE gate every candidate routes through
	}
	em := eng.Propose([]proposal.Target{
		{
			Diagnosis: diagnosis.Diagnosis{NodeID: absorbed, EvidenceCaseIDs: []string{"case-hermes-1"}},
			Signal:    proposal.SignalRedundantNode,
			Pattern:   patternclassifier.PromptChaining,
		},
		{
			Diagnosis: diagnosis.Diagnosis{NodeID: base.Order[3], TaxonomyCode: diagnosis.CauseLostInMiddle,
				EvidenceCaseIDs: []string{"case-hermes-2"}, Confidence: 0.7},
			Pattern: patternclassifier.PromptChaining,
		},
	})
	proposal.SortCandidates(em.Candidates)

	for _, c := range em.Candidates {
		h := hashOf(ir, c.Spec, "")
		fmt.Printf("  %-8s node=%s  order %d->%d  parent=%s  config_hash=%s  %s\n",
			c.Operator, short(c.NodeID), len(base.Order), len(c.Spec.Order),
			short(c.Spec.ParentVariantID), short(h), sameOrDiff(h, baseHash))
	}
	if len(em.Refusals) > 0 {
		for _, r := range em.Refusals {
			fmt.Printf("  refused: %-8s %s\n", r.Operator, r.Reason)
		}
	}

	// 🚫 No silent cap. The reorder space over 40 nodes is factorial and the verification budget is not,
	// so the operator proposes a BOUNDED number of independent pairs — and says so in the rationale,
	// because "5 reorders proposed" on a 40-node graph would otherwise read as an exhausted space.
	for _, c := range em.Candidates {
		if c.Operator == proposal.OpReorder && strings.Contains(c.Rationale, "bounded") {
			fmt.Printf("  bound stated: %s\n", c.Rationale[strings.Index(c.Rationale, "(bounded"):])
			break
		}
	}

	// The merge shape, spelled out on the real node ids: the absorbed node is gone and nothing else moved.
	for _, c := range em.Candidates {
		if c.Operator != proposal.OpMerge {
			continue
		}
		fmt.Printf("  merge detail: %s absorbed into %s; order [%s %s %s ...] -> [%s %s ...]\n",
			short(absorbed), short(survivor),
			short(base.Order[0]), short(base.Order[1]), short(base.Order[2]),
			short(c.Spec.Order[0]), short(c.Spec.Order[1]))
		if indexOf(c.Spec.Order, absorbed) >= 0 {
			fmt.Printf("  !! the absorbed node is still in the order - the merge did not merge\n")
		}
	}
	fmt.Printf("  -> every candidate is DERIVED (ParentVariantID set) and the baseline is untouched: still\n")
	fmt.Printf("     %d ordered nodes after %d proposals.\n\n", len(base.Order), len(em.Candidates))
	return em.Candidates
}

// ── §5 — 🔴 reject-at-compile ────────────────────────────────────────────────────────────────────

func rejectAtCompile(ir *discovery.IR, base *variantspec.VariantSpec) {
	fmt.Printf("-- §5 - 🔴 an incoherent ordering yields NO runnable spec (task 5.2) --\n")

	// The nodes and their contracts are real; the data edge is one the SPEC declares, which is how a
	// re-arrangement authors one (the syntactic Python frontend records no edges - see the header).
	producer, consumer := base.Order[0], base.Order[1]
	incoherent := variantspec.Reorder(base, "", []string{consumer, producer},
		[]variantspec.Edge{{FromNodeID: producer, ToNodeID: consumer, Kind: "data"}})
	incoherent.Order = []string{consumer, producer}

	got, verdict := variantspec.GateReorder(ir, incoherent, typedcontract.DefaultCatalog())
	fmt.Printf("  declared edge %s -> %s (data), ordering puts the consumer first\n", short(producer), short(consumer))
	fmt.Printf("  verdict=%s  runnable spec: %v\n", verdict.Kind, got != nil)
	if got != nil {
		fmt.Printf("  !! a rejected ordering produced a runnable spec - the gate is not fail-closed\n\n")
		return
	}
	if len(verdict.Diagnostics) > 0 {
		d := verdict.Diagnostics[0]
		fmt.Printf("  diagnostic: %s\n", d.Message)
	}
	fmt.Printf("  -> the gate returns (nil, verdict), so a caller physically CANNOT hand this ordering to\n")
	fmt.Printf("     the transform engine. No codemod, no diff, no PR exists to be reviewed by mistake.\n\n")
}

// ── §5 — the adapter path, and why this repository cannot reach it ───────────────────────────────

func adapterBoundary(ir *discovery.IR) {
	fmt.Printf("-- §5 - the `adapted` verdict is NOT REACHABLE on this repository, and that is reported --\n")

	typed := 0
	for _, n := range ir.Nodes {
		if len(properties(n.IOContract.OutputSchema)) > 0 || len(properties(n.IOContract.InputSchema)) > 0 {
			typed++
		}
	}
	fmt.Printf("  nodes declaring any field-level io_contract: %d of %d\n", typed, len(ir.Nodes))
	fmt.Printf("  -> the Python frontend is syntactic: it states `{\"type\":\"object\"}`, which is the honest\n")
	fmt.Printf("     contract for a call site nobody type-resolved. With no field-level mismatch there is\n")
	fmt.Printf("     nothing for the catalog to bridge, so no adapter is inserted here. The adapter path is\n")
	fmt.Printf("     covered by the unit suite (TestAdaptedVerdictRecordsAdapter, TestAdapterIsInReviewableDiff)\n")
	fmt.Printf("     rather than simulated against a contract this repository does not declare.\n\n")
}

// ── §4 — 🔴 the headline refusal, at the real transform ──────────────────────────────────────────

func refuseAtTransform(ir *discovery.IR, base *variantspec.VariantSpec, cands []proposal.Candidate, repo string) {
	fmt.Printf("-- §4 - 🔴 what the transform REFUSES, and what it has nothing to refuse (task 4.2/4.3) --\n")

	resolved := &variantspec.Resolved{
		ConfigHash: "cfg-hermes-wiring", SourceRevision: ir.Workflow.Repo.CommitSHA,
		Language: ir.Workflow.Language,
		// What the SOURCE wires, read off the discovered IR - the evidence the refusal compares against.
		DiscoveredWiring: variantspec.WiringOf(ir),
	}

	refused, inert := 0, 0
	for _, c := range cands {
		if c.Operator != proposal.OpMerge && c.Operator != proposal.OpPrune && c.Operator != proposal.OpReorder {
			continue
		}
		patch, err := transform.GenerateTransform(resolved, c.Spec, repo)
		var re *transform.RewriteError
		switch {
		case errors.As(err, &re) && errors.Is(err, transform.ErrUnsafeRewrite) && re.Dim == "wiring":
			refused++
			fmt.Printf("  %-8s REFUSED  node=%s  patch emitted: %v\n", c.Operator, short(re.NodeID), patch != nil)
		case err != nil:
			fmt.Printf("  %-8s unexpected error: %v\n", c.Operator, err)
		case patch != nil && len(patch.Files) == 0:
			inert++
			fmt.Printf("  %-8s no refusal, EMPTY patch — the source states no order between these calls\n", c.Operator)
		default:
			fmt.Printf("  %-8s MATERIALIZED %d file(s)\n", c.Operator, len(patch.Files))
		}
	}

	// The control: the baseline wires exactly what the source wires, so it is NOT refused. A gate that
	// refused everything would pass the assertions above and be useless.
	if _, err := transform.GenerateTransform(resolved, base, repo); err != nil {
		fmt.Printf("  !! the UNCHANGED baseline was refused too: %v\n", err)
	} else {
		fmt.Printf("  baseline   accepted (its wiring is the source's wiring)\n")
	}

	fmt.Printf("  %d refused (a merge or a prune: the source still CONTAINS the call the spec dropped, so\n", refused)
	fmt.Printf("     scoring that config_hash would be a false measurement), %d inert.\n", inert)
	fmt.Printf("  -> the inert ones are the honest boundary, recorded in the PRD ledger as NOT MODELLED:\n")
	fmt.Printf("     these calls sit in different functions, so the SOURCE states no order between them.\n")
	fmt.Printf("     Their config_hash differs and their behaviour does not, so the harness scores a tie —\n")
	fmt.Printf("     wasteful, but not a false win. Refusing them instead would break every spec ever\n")
	fmt.Printf("     authored (twelve pre-existing e2e specs did break, which is how this was found) while\n")
	fmt.Printf("     preventing no false measurement at all.\n\n")
}

// ── §3.4 — determinism, on the real graph ────────────────────────────────────────────────────────

func determinism(ir *discovery.IR, base *variantspec.VariantSpec, baseHash string) {
	fmt.Printf("-- §3.4 - the same base + signal produces the same candidate, twice (task 3.4) --\n")

	run := func() []proposal.Candidate {
		eng := proposal.Engine{Base: base, BaseVariantID: baseHash, IR: ir, Gate: proposal.NewTypedContractGate(ir)}
		em := eng.Propose([]proposal.Target{{
			Diagnosis: diagnosis.Diagnosis{NodeID: base.Order[2], EvidenceCaseIDs: []string{"case-hermes-1"}},
			Signal:    proposal.SignalRedundantNode, Pattern: patternclassifier.PromptChaining,
		}})
		proposal.SortCandidates(em.Candidates)
		return em.Candidates
	}
	a, b := run(), run()
	if len(a) != len(b) {
		fmt.Printf("  !! candidate count differs across runs: %d vs %d\n\n", len(a), len(b))
		return
	}
	identical := true
	for i := range a {
		ja, _ := json.Marshal(a[i].Spec)
		jb, _ := json.Marshal(b[i].Spec)
		if string(ja) != string(jb) || hashOf(ir, a[i].Spec, "") != hashOf(ir, b[i].Spec, "") {
			identical = false
			fmt.Printf("  !! candidate %d (%s) differs between runs\n", i, a[i].Operator)
		}
	}
	if identical {
		fmt.Printf("  %d candidate(s), byte-identical specs and identical config hashes across two runs.\n", len(a))
		fmt.Printf("  -> nothing here iterates a map: the candidate is a pure function of (base, signal).\n\n")
	}
}

// ── §6 — verification decides, not the operator ──────────────────────────────────────────────────

func verificationGate() {
	fmt.Printf("-- §6 - a merge that reads redundant but scores WORSE is withheld (task 6.3) --\n")

	cases := []struct {
		what              string
		baseQ, candQ      float64
		baseCost, candCst float64
	}{
		{"the second call was correcting the first", 0.80, 0.55, 0.02, 0.01},
		{"the fusion genuinely subsumed both", 0.50, 0.90, 0.02, 0.01},
	}
	for _, c := range cases {
		v, err := verification.Verify(context.Background(), fixedRunner{
			quality: map[string]float64{"base": c.baseQ, "cand": c.candQ},
			cost:    map[string]float64{"base": c.baseCost, "cand": c.candCst},
		}, verification.Proposal{
			ProposalID: "merge-hermes", CandidateConfigHash: "cand", BaselineConfigHash: "base",
			SourceRevision: "rev", GeneratingCaseIDs: []string{"g1", "g2"},
		}, []string{"g1", "g2", "h1", "h2", "h3", "h4", "h5", "h6"}, verification.DefaultConfig())
		if err != nil {
			fmt.Printf("  verify: %v\n", err)
			continue
		}
		surfaced := len(verification.Recommendations([]verification.Verdict{v})) == 1
		fmt.Printf("  %-42s quality %.2f->%.2f  cheaper  gate=%-17s recommended: %v\n",
			c.what, c.baseQ, c.candQ, v.GateResult, surfaced)
	}
	fmt.Printf("  -> both candidates are cheaper and both remove a call. Only measurement separates them,\n")
	fmt.Printf("     which is why a produced wiring candidate is EXPLORATORY until a held-out delta says so.\n\n")
}

// ── 15c — the call-site rewriter, surveyed over every adjacent pair ─────────────────────────────

// swapSurvey asks the question the rewriter exists to answer, for EVERY adjacent pair in the graph:
// could this transposition be materialized, and if not, which condition blocked it?
//
// A survey rather than one sample, because "does the rewriter apply here" is a proportion, and the
// per-pair reasons are the useful output — they say what a target repository would have to look like
// for the axis to become applicable, which is information a single "refused" line destroys.
func swapSurvey(ir *discovery.IR, base *variantspec.VariantSpec, repo string) {
	fmt.Printf("-- 15c - can any adjacent pair be MATERIALIZED as a source reorder? --\n")

	reasons := map[string]int{}
	applied := 0
	inert := 0
	var firstDiff string
	for i := 0; i+1 < len(base.Order); i++ {
		first, second := base.Order[i], base.Order[i+1]
		order := append([]string(nil), base.Order...)
		order[i], order[i+1] = order[i+1], order[i]

		r := &variantspec.Resolved{
			ConfigHash: "cfg-hermes-swap", SourceRevision: ir.Workflow.Repo.CommitSHA,
			Language:         ir.Workflow.Language,
			Config:           variantspec.ResolvedConfig{IRVersion: ir.IRVersion},
			DiscoveredWiring: variantspec.WiringOf(ir),
		}
		for _, id := range order {
			r.Config.Nodes = append(r.Config.Nodes, variantspec.ResolvedNode{NodeID: id})
		}
		patch, err := transform.Generate(r, repo)
		if err != nil {
			reasons[refusalShape(err)]++
			continue
		}
		// 🔴 A patch with NO diff is not a materialization. Both branches return nil error, and
		// counting them together printed "39 materialized" directly above the sentence "no pair in
		// this repository is a transposition of two adjacent sibling statements" — the survey
		// contradicting itself in the same block.
		//
		// The distinction is the one §4's correction turned on. These node pairs sit in different
		// functions, so the source ORDERS nothing between them: swapping them is a DECLARATION, not a
		// rewire, and emitting no diff is correct (it is why the gate does not refuse them). But it is
		// also the exact shape of the failure this axis exists to prevent — a config_hash recording a
		// rearranged graph, scored against source that was never rewired. Reporting it as "materialized"
		// is how that failure would be read as a success.
		if patch == nil || patch.IsEmpty() {
			inert++
			continue
		}
		applied++
		if firstDiff == "" {
			firstDiff = string(patch.Diff)
			fmt.Printf("  MATERIALIZED %s <-> %s\n", short(first), short(second))
		}
	}
	fmt.Printf("  %d adjacent pair(s) surveyed: %d materialized, %d inert (no diff), %d refused\n",
		len(base.Order)-1, applied, inert, len(base.Order)-1-applied-inert)
	if inert > 0 {
		fmt.Printf("    the inert ones are pairs the SOURCE states no order between (different functions),\n")
		fmt.Printf("    so the swap is a declaration rather than a rewire: correct to accept, and correct\n")
		fmt.Printf("    NOT to call materialized — nothing was written to the source.\n")
	}
	for _, k := range sortedReasons(reasons) {
		fmt.Printf("    %3d refused: %s\n", reasons[k], k)
	}
	if firstDiff != "" {
		for _, line := range strings.Split(strings.TrimRight(firstDiff, "\n"), "\n") {
			fmt.Printf("    %s\n", line)
		}
		return
	}
	fmt.Printf("  -> no pair in THIS repository is a transposition of two adjacent sibling statements, and\n")
	fmt.Printf("     each refusal above says which condition blocked it. That is a fact about hermes-agent's\n")
	fmt.Printf("     code — its %d LLM calls sit across many files, mostly as `return` statements inside\n", len(ir.Nodes))
	fmt.Printf("     branches — not a gap in the rewriter: the same engine materializes the swap on a tree\n")
	fmt.Printf("     whose calls ARE siblings (TestMaterializedReorderOnBothEntrypoints, and the diff the\n")
	fmt.Printf("     console's wiring surface shows).\n\n")
}

// ── the control: what this repository does NOT decline ───────────────────────────────────────────

// notDeclined exists because a run whose every line says REFUSED is indistinguishable, to a reader,
// from a platform that does not work.
//
// The wiring axis declines on this repository because there is no rewriter that moves, fuses, or
// deletes a call. The CONTENT axes are a different question and get a different answer: a model
// override is a value replacement at an expression the call site already wrote, which is exactly what
// this engine does — so it produces a real diff, against these files, at this commit.
//
// Printing both in one run is the honest shape. "Declined" is a statement about ONE axis and its
// missing materializer, not about the platform, and a reader is entitled to see the difference rather
// than take it on trust.
func notDeclined(ir *discovery.IR, repo string) {
	fmt.Printf("-- the control: what this repository does NOT decline --\n")

	entry := &registry.ModelEntry{
		VersionID: "0000000000000000000000000000000000000000000000000000000000000001",
		Name:      "gpt-4o-mini",
		Spec:      registry.ModelSpec{Provider: "openai", ModelID: "gpt-4o-mini"},
	}

	// Try a MODEL override at every discovered call site and count the two outcomes. A survey rather
	// than one sample, because the answer to "does anything work here" is a proportion, and one
	// unlucky pick would misreport it in either direction.
	var built, refused int
	var firstDiff string
	var firstNode, firstSite string
	reasons := map[string]int{}
	for _, n := range ir.Nodes {
		r := &variantspec.Resolved{
			ConfigHash: "cfg-hermes-model", SourceRevision: ir.Workflow.Repo.CommitSHA,
			Language:  ir.Workflow.Language,
			Overrides: map[string]variantspec.ResolvedOverride{n.NodeID: {Model: entry}},
			// The spec's wiring IS the source's wiring here — only a node's CONTENT changes — so the P15
			// refusal correctly does not fire.
			DiscoveredWiring: variantspec.WiringOf(ir),
		}
		patch, err := transform.Generate(r, repo)
		if err != nil {
			refused++
			reasons[refusalShape(err)]++
			continue
		}
		built++
		if firstDiff == "" && len(patch.Diff) > 0 {
			firstDiff = string(patch.Diff)
			firstNode = n.NodeID
			firstSite = fmt.Sprintf("%s:%d %s", n.CallSite.File, n.CallSite.LineStart, n.CallSite.Symbol)
		}
	}
	fmt.Printf("  model override tried at all %d discovered call sites: %d produced a diff, %d refused\n",
		len(ir.Nodes), built, refused)
	for _, k := range sortedReasons(reasons) {
		fmt.Printf("    %3d refused: %s\n", reasons[k], k)
	}

	if firstDiff == "" {
		fmt.Printf("  -> every call site in THIS repository refuses a model rewrite, each for a stated reason\n")
		fmt.Printf("     about that call site (runtime-assembled kwargs, no written argument). That is a fact\n")
		fmt.Printf("     about hermes-agent's code, not about the wiring axis — and it is why P15 is scored\n")
		fmt.Printf("     against config_hash rather than against a diff this tree can accept.\n\n")
		return
	}
	fmt.Printf("  first real diff - node %s\n  at %s\n", short(firstNode), firstSite)
	for _, line := range strings.Split(strings.TrimRight(firstDiff, "\n"), "\n") {
		fmt.Printf("    %s\n", line)
	}
	fmt.Printf("  -> so \"declined\" is a fact about the WIRING axis and its missing call-site rewriter, not\n")
	fmt.Printf("     about this platform: the same engine, the same tree, the same commit produces a real\n")
	fmt.Printf("     reviewable diff for a content change and refuses a rearrangement.\n\n")
}

// refusalShape collapses a refusal to its distinguishing clause, so the survey above reports the KINDS
// of refusal rather than N copies of one paragraph.
func refusalShape(err error) string {
	var re *transform.RewriteError
	if !errors.As(err, &re) {
		return err.Error()
	}
	d := re.Detail
	// A refusal's DISTINGUISHING clause is the one after "but" — everything before it is the shared
	// preamble that says what the engine can do in general. Reporting the preamble would collapse six
	// different reasons into one row and destroy the only useful output of a survey.
	if i := strings.Index(d, " but "); i >= 0 {
		d = strings.TrimSpace(d[i+len(" but "):])
	} else if i := strings.Index(d, ":"); i > 0 && i < 120 {
		d = d[:i]
	}
	if i := strings.Index(d, ". It is REFUSED"); i > 0 {
		d = d[:i]
	}
	if len(d) > 150 {
		d = d[:150] + "…"
	}
	return fmt.Sprintf("[%s] %s", re.Dim, d)
}

func sortedReasons(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ── plumbing ─────────────────────────────────────────────────────────────────────────────────────

// baselineFrom builds the baseline Variant Spec from the discovered graph: the IR's own node order and
// its own edges. Nothing is overridden — this is the configuration the repository already is.
func baselineFrom(ir *discovery.IR, commitSHA string) *variantspec.VariantSpec {
	spec := &variantspec.VariantSpec{
		WorkflowID: "nousresearch/hermes-agent", SourceRevision: commitSHA,
		Nodes: map[string]variantspec.NodeOverride{},
	}
	for _, n := range ir.Nodes {
		spec.Order = append(spec.Order, n.NodeID)
	}
	for _, e := range ir.Edges {
		spec.Edges = append(spec.Edges, variantspec.Edge{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: e.Kind})
	}
	return spec
}

// hashOf resolves a spec against the real IR and returns its config_hash. The registries are empty
// because a wiring change references NOTHING to resolve — which is the whole point of the axis living
// in Order/Edges — so a resolution that needed a registry entry here would itself be the finding.
func hashOf(ir *discovery.IR, spec *variantspec.VariantSpec, label string) string {
	r, err := variantspec.Resolve(context.Background(), spec, ir, emptyRegistries{})
	if err != nil {
		log.Fatalf("resolve %s: %v", label, err)
	}
	if label != "" {
		fmt.Printf("baseline config_hash: %s\n\n", r.ConfigHash)
	}
	return r.ConfigHash
}

func sameOrDiff(h, base string) string {
	if h == base {
		return "(SAME as baseline - not a new configuration)"
	}
	return "(differs from baseline)"
}

func properties(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	p, _ := schema["properties"].(map[string]any)
	return p
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

// fixedRunner returns a constant per-config quality/cost, so the gate's behaviour is proven against a
// known delta without a provider. It is the only stub in this command.
type fixedRunner struct {
	quality map[string]float64
	cost    map[string]float64
}

func (f fixedRunner) Run(_ context.Context, req verification.RunRequest) (verification.RunResult, error) {
	var q, c, l evalstats.Series
	for _, id := range req.CaseIDs {
		for _, seed := range req.Seeds {
			q.Obs = append(q.Obs, evalstats.Observation{CaseID: id, Seed: seed, Value: f.quality[req.ConfigHash]})
			c.Obs = append(c.Obs, evalstats.Observation{CaseID: id, Seed: seed, Value: f.cost[req.ConfigHash]})
			l.Obs = append(l.Obs, evalstats.Observation{CaseID: id, Seed: seed, Value: 500})
		}
	}
	return verification.RunResult{Quality: q, Cost: c, Latency: l}, nil
}

type emptyRegistries struct{}

func (emptyRegistries) ResolveModel(context.Context, string) (*registry.ModelEntry, error) {
	return nil, registry.ErrNotFound
}
func (emptyRegistries) ResolvePrompt(context.Context, string) (*registry.PromptEntry, error) {
	return nil, registry.ErrNotFound
}
func (emptyRegistries) ResolveSkill(context.Context, string) (*registry.SkillEntry, error) {
	return nil, registry.ErrNotFound
}
func (emptyRegistries) ResolveContextPolicy(context.Context, string) (*registry.ContextEntry, error) {
	return nil, registry.ErrNotFound
}

// ResolveMemory completes variantspec.Registries (P17). It fails closed like its siblings: this
// harness pins no memory strategy, so a memory_ref here names nothing and must not resolve to something.
func (emptyRegistries) ResolveMemory(context.Context, string) (*registry.MemoryEntry, error) {
	return nil, registry.ErrNotFound
}

func (emptyRegistries) ResolveHarness(context.Context, string) (*registry.HarnessEntry, error) {
	return nil, registry.ErrNotFound
}

func siteOf(ir *discovery.IR, id string) string {
	for _, n := range ir.Nodes {
		if n.NodeID == id {
			return fmt.Sprintf("%s:%d %s", n.CallSite.File, n.CallSite.LineStart, n.CallSite.Symbol)
		}
	}
	return "?"
}

func short(s string) string {
	if len(s) > 14 {
		return s[:14]
	}
	return s
}
