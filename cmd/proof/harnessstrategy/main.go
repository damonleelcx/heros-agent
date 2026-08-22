// Command harnessstrategy drives the P18 harness-strategy axis against a REAL repository
// (github.com/nousresearch/hermes-agent, the same target as cmd/proof/promptmodel … p17hermes).
//
// # What this run proves, and the boundary it refuses to hide
//
// P18's claim has three parts, and this run separates them because they are answered by three different
// people:
//
//	the axis is MODELED end-to-end — referenced, resolved, hashed, proposed, authored;
//	it MATERIALIZES where the runtime can both DRIVE the call and DECIDE whether to run again;
//	and it REFUSES by name everywhere else, with a cause that says whose the gap is.
//
// 🔴 On this repository the run does BOTH, and the split is the finding. Three of the five strategies
// refuse for a reason that is true in EVERY language and will stay true after every rewriter lands (a
// call site has nowhere to inject a tool executor, a planner, or a critic). The fourth — `reflexion` —
// materializes on the call sites that write their own message list, and refuses on the ones that assemble
// the request elsewhere. Not one refusal is waiting on us.
//
// 🔴 That makes this the FIRST axis since model and prompt to write real source into this repository, and
// §4 counts it as its own row rather than folding it in with the identity's empty diff. Reporting "N
// materialized" over both would let one run claim a loop was written when none was, and the next claim
// none was written when one had been.
//
// The sections, all running the shipped code paths against the real IR:
//
//	§1  what discovery found, and the harness default it emitted for every node — `single-shot`, which is
//	    a claim about the EVIDENCE. This section counts the nodes discovery recorded as sitting inside a
//	    loop and shows they are single-shot anyway: loop DEPTH is not evidence of an agent loop, and
//	    treating it as such would report a fan-out as a scaffold;
//	§2  🔴 `single-shot` ≡ absent on the REAL config: byte-identical canonical bytes and the same
//	    config_hash, so no stored hash moved when this axis shipped;
//	§3  the hash MOVES iff the strategy or its turn ceiling moves — the other half of the same contract;
//	§4  🔴 the headline: every (node × strategy) combination through the REAL transform, counted by cause
//	    class AND by refusal shape, because "we have not built it", "no call site can do this", and "this
//	    call site cannot do this" send the reader to three different places;
//	§5  the operator, and the gate that can say no: a scaffold swap is proposed against a real signal,
//	    and the admissibility gate is run over held-out measurements in both directions;
//	§6  the authored path: a user selects a strategy, the per-cell boundary is stated BEFORE the choice,
//	    a schema violation is rejected before sealing, and clearing reproduces the prior hash byte-exactly.
//
// The boundaries, stated rather than worked around:
//
//	§5's diagnosis signal is illustrative (in production it comes from P4.5), and its held-out
//	measurements are constructed — this run parses a repository, it does not evaluate one. Everything
//	they drive is real: the candidate spec, the config_hash, the operator's rationale, and the gate's
//	arithmetic and its verdicts.
//
// It NEVER executes the target — it parses it (invariant I1).
//
//	go run ./cmd/proof/harnessstrategy -repo /tmp/hermes-agent
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/authoring"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/harnessruntime"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sourcerev"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

const repoURL = "https://github.com/nousresearch/hermes-agent"

func main() {
	repo := flag.String("repo", "/tmp/hermes-agent", "path to the hermes-agent checkout (read-only)")
	pin := flag.String("pin", "", `commit this run must be checked out at; empty means "use HEAD and say so"`)
	flag.Parse()

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

	fmt.Printf("=== P18 harness strategy optimization - run for %s ===\n", repoURL)
	fmt.Printf("discovered %d nodes, %d edges (language=%s) at %s (%s)\n\n",
		len(ir.Nodes), len(ir.Edges), ir.Workflow.Language, short(commitSHA), revNote)

	base := baselineFrom(ir, commitSHA)
	if len(base.Order) < 4 {
		log.Fatalf("this demonstration needs at least 4 discovered nodes, got %d", len(base.Order))
	}

	regs := harnessRegistries()

	discovered(ir, base)
	baseHash := identityIsAbsent(ir, base, regs)
	hashMovesIffHarnessMoves(ir, base, regs, baseHash)
	transformOutcome(ir, base, regs, *repo)
	operatorAndGate(ir, base, regs)
	authoredPath(ir, base, regs)

	fmt.Println("=== end of run ===")
}

// ── §1 what discovery found ──────────────────────────────────────────────────────────────────────

func discovered(ir *discovery.IR, base *variantspec.VariantSpec) {
	fmt.Println("--- 1. what discovery found, and the harness default it emitted ---")
	fmt.Printf("baseline graph: %d ordered nodes\n", len(base.Order))
	for i, id := range base.Order[:3] {
		fmt.Printf("  [%d] %s  %s\n", i, short(id), siteOf(ir, id))
	}
	fmt.Printf("  ... %d more\n\n", len(base.Order)-3)

	counts := map[string]int{}
	inLoop := 0
	for _, n := range ir.Nodes {
		counts[n.HarnessDefault()]++
		if n.InvocationSemantics.Type == "loop" {
			inLoop++
		}
	}
	fmt.Println("  per-node harness default, as DISCOVERED:")
	for _, k := range sortedKeys(counts) {
		fmt.Printf("    %-16s %d node(s)\n", k, counts[k])
	}
	fmt.Println()

	// 🔴 The argument for the floor, MEASURED against this repository rather than asserted.
	fmt.Printf("  🔴 %d of %d node(s) sit inside a loop, and every one of them is still `single-shot`.\n",
		inLoop, len(ir.Nodes))
	fmt.Println("     That is the discipline, not an omission. `invocationFor` already records `loop` when a")
	fmt.Println("     call sits inside one, and reaching for that signal here is the obvious move — but a")
	fmt.Println("     `for` over a list of items fires ONE node many times with NO scaffold, while an agent")
	fmt.Println("     loop is the MODEL choosing to take another turn. Loop depth cannot tell them apart,")
	fmt.Println("     and emitting `react-loop` for a `for` would hash a configuration nobody authored.")
	fmt.Println()

	emitted, err := discovery.MarshalIR(*ir)
	if err != nil {
		log.Fatalf("marshal IR: %v", err)
	}
	if strings.Contains(string(emitted), `"harness"`) {
		fmt.Println("  ⚠️  the emitted IR carries a harness key on some node — the default should be free")
	} else {
		fmt.Println("  the emitted IR carries NO harness key: the default is written as absence, so every")
		fmt.Println("  pre-P18 document and every current one that proved no loop serialise identically.")
	}
	fmt.Println()
}

// ── §2 single-shot ≡ absent, on the real config ──────────────────────────────────────────────────

func identityIsAbsent(ir *discovery.IR, base *variantspec.VariantSpec, regs *harnessRegs) string {
	fmt.Println("--- 2. 🔴 `single-shot` is byte-identical to absent, on the REAL discovered config ---")

	baseHash, baseCanon := hashAndCanon(ir, base, regs)
	fmt.Printf("  baseline (no harness anywhere)             config_hash=%s\n", short(baseHash))

	node := base.Order[0]
	withIdentity := cloneSpec(base)
	setHarness(withIdentity, node, regs.identityRef)
	idHash, idCanon := hashAndCanon(ir, withIdentity, regs)
	fmt.Printf("  node %s pinned to `single-shot`  config_hash=%s\n", short(node), short(idHash))

	if idHash != baseHash || string(idCanon) != string(baseCanon) {
		fmt.Println("  ❌ THE CONTRACT IS BROKEN: `single-shot` moved the hash. Every stored config_hash on")
		fmt.Println("     this repository would be orphaned, and every frozen golden vector would break.")
		log.Fatal("single-shot != absent")
	}
	fmt.Println("  ✅ identical bytes, identical hash. `single-shot` IS the absence of a scaffold, so a user")
	fmt.Println("     can select it, clear it, and back out with no residue — and no stored hash moved when")
	fmt.Println("     this axis shipped.")
	fmt.Println()
	return baseHash
}

// ── §3 the hash moves iff the harness moves ──────────────────────────────────────────────────────

func hashMovesIffHarnessMoves(ir *discovery.IR, base *variantspec.VariantSpec, regs *harnessRegs, baseHash string) {
	fmt.Println("--- 3. the hash moves iff the strategy or its turn ceiling moves ---")
	node := base.Order[0]

	seen := map[string]string{}
	for _, ref := range regs.order {
		spec := cloneSpec(base)
		setHarness(spec, node, ref)
		h, _ := hashAndCanon(ir, spec, regs)
		e := regs.byRef[ref]
		label := e.Spec.Strategy
		if s := paramSummary(e); s != "" {
			label += " " + s
		}
		mark := "different"
		if h == baseHash {
			mark = "SAME as baseline"
		}
		if prev, dup := seen[h]; dup {
			fmt.Printf("  ❌ %s collides with %s\n", label, prev)
			log.Fatal("two different configurations share a hash")
		}
		seen[h] = label
		fmt.Printf("    %-52s config_hash=%s  (%s)\n", label, short(h), mark)
	}
	fmt.Println()
	fmt.Println("  🔴 note the two `reflexion` rows: same strategy, different `max_turns`, different hash.")
	fmt.Println("     A loop that may run five turns is not the same configuration as one that may run")
	fmt.Println("     three, and it does not cost the same — so the platform scores them separately.")
	fmt.Println()
}

// ── §4 what the transform does, counted ──────────────────────────────────────────────────────────

func transformOutcome(ir *discovery.IR, base *variantspec.VariantSpec, regs *harnessRegs, repo string) {
	fmt.Println("--- 4. 🔴 every (node × strategy) combination, through the REAL transform ---")

	var identityNoOp, emitted, refused, otherErr int
	var emittedSites []string
	byCause := map[transform.CauseClass]int{}
	nodesByCause := map[transform.CauseClass]map[string]bool{}
	byShape := map[string]int{}
	nodesByShape := map[string]map[string]bool{}
	shapeSite := map[string]string{}
	shapeSample := map[string]string{}
	shapeCause := map[string]transform.CauseClass{}

	for _, node := range base.Order {
		for _, ref := range regs.order {
			entry := regs.byRef[ref]
			spec := cloneSpec(base)
			setHarness(spec, node, ref)
			resolved, err := variantspec.Resolve(context.Background(), spec, ir, regs)
			if err != nil {
				log.Fatalf("resolve %s/%s: %v", short(node), entry.Spec.Strategy, err)
			}
			patch, err := transform.Generate(resolved, repo)
			if err == nil {
				// 🔴 Two very different outcomes both arrive here, and collapsing them would be the false
				// claim this run exists to avoid. The identity emits NOTHING (no diff is the correct diff
				// for it); a covered multi-turn cell emits a REAL loop. Counting them together would let
				// this run report "31 materialized" while a loop had been written into someone's source —
				// or, on a different day, the reverse.
				if patch != nil && len(patch.Diff) > 0 {
					emitted++
					// 🔴 The ENTRY name, not just the strategy. Two entries of one strategy differing only
					// in `max_turns` are two configurations and two costs, and a line that named only
					// "reflexion" twice would read as one finding printed twice.
					emittedSites = append(emittedSites,
						fmt.Sprintf("%-12s %-30s at %s", entry.Spec.Strategy,
							entry.Name+" "+paramSummary(entry), siteOf(ir, node)))
				} else {
					identityNoOp++
				}
			} else {
				var re *transform.RewriteError
				if asRewrite(err, &re) && re.Dim == string(variantspec.DimHarness) {
					refused++
					byCause[re.Cause]++
					if nodesByCause[re.Cause] == nil {
						nodesByCause[re.Cause] = map[string]bool{}
					}
					nodesByCause[re.Cause][node] = true

					shape := openingClause(re.Detail, entry.Spec.Strategy)
					byShape[shape]++
					if nodesByShape[shape] == nil {
						nodesByShape[shape] = map[string]bool{}
					}
					nodesByShape[shape][node] = true
					if shapeSample[shape] == "" {
						shapeSample[shape] = re.Detail
						shapeSite[shape] = siteOf(ir, node)
						shapeCause[shape] = re.Cause
					}
				} else {
					otherErr++
				}
			}
		}
	}

	total := identityNoOp + emitted + refused + otherErr
	fmt.Printf("  %d (node × strategy) combinations exercised on the real tree\n", total)
	fmt.Printf("    the IDENTITY, emitting nothing     : %d\n", identityNoOp)
	fmt.Printf("    a LOOP written into the source     : %d\n", emitted)
	fmt.Printf("    refused with a typed harness cause : %d\n", refused)
	fmt.Printf("    refused for another reason         : %d\n", otherErr)
	fmt.Println()
	fmt.Println("  ✅ nothing was dropped: every override came back as one of those three, never as a")
	fmt.Println("     silent success. The identity row is a no-op by construction — one turn is exactly the")
	fmt.Println("     un-rewritten call site, so no diff is the CORRECT diff for it, not a missing one.")
	fmt.Println()
	if emitted > 0 {
		// 🔴 The headline for this repository, and it is NOT the memory axis's result. Memory refused
		// all 186 of its combinations here; harness writes a real bounded loop into some of them.
		fmt.Printf("  🔴 %d combination(s) produced a REAL DIFF — a bounded loop written into this\n", emitted)
		fmt.Println("     repository's own source, with the generated module beside it in the same patch:")
		for _, s := range emittedSites {
			fmt.Printf("       %s\n", s)
		}
		fmt.Println("     Those are the call sites that write their message list, which is what a loop needs")
		fmt.Println("     to take another turn. It is the first axis to reach this repository's source since")
		fmt.Println("     the model and prompt axes.")
		fmt.Println()
	} else {
		fmt.Println("  no combination produced a diff on this repository. That is a finding about ITS call")
		fmt.Println("  sites, counted below rather than asserted.")
		fmt.Println()
	}

	fmt.Println("  the refusals, by cause class — because three different people are being told three")
	fmt.Println("  different things:")
	for _, cause := range transform.CauseClasses() {
		n := byCause[cause]
		if n == 0 {
			continue
		}
		fmt.Printf("    %-34s %3d attempt(s) over %d of %d node(s)\n",
			cause, n, len(nodesByCause[cause]), len(base.Order))
	}
	fmt.Println()
	if byCause[transform.CauseNoMaterializer] == 0 {
		fmt.Printf("  ✅ ZERO of the %d refusals is blamed on a missing materializer. Not one is waiting on\n", refused)
		fmt.Println("     us: Python HAS the harness rewriter, it ran on every one of these, and it refused")
		fmt.Println("     each on a fact about the strategy or about the call site it was pointed at.")
	} else {
		fmt.Printf("  ⚠️  %d refusal(s) still name a missing materializer — that share is OURS, not theirs.\n",
			byCause[transform.CauseNoMaterializer])
	}
	fmt.Println()

	// 🔴 The shape table, which is where the finding actually is. The cause class says WHOSE the gap is;
	// the shape says WHAT to change — and a harness can fail for two entirely different reasons.
	shapes := sortedKeys(byShape)
	sort.SliceStable(shapes, func(i, j int) bool { return byShape[shapes[i]] > byShape[shapes[j]] })
	fmt.Printf("  the same refusals grouped by what the engine actually said — %d distinct shapes:\n", len(shapes))
	for _, s := range shapes {
		fmt.Printf("    %3d× over %2d node(s)  [%s]\n", byShape[s], len(nodesByShape[s]), shapeCause[s])
		fmt.Printf("                          %s\n", s)
		fmt.Printf("                          e.g. %s\n", shapeSite[s])
	}
	fmt.Println()
	fmt.Println("  the engine's sentence, verbatim, once per shape:")
	for _, s := range shapes {
		fmt.Printf("    [%d× %s]\n", byShape[s], shapeCause[s])
		for _, line := range wrap(shapeSample[s], 92) {
			fmt.Printf("      %s\n", line)
		}
		fmt.Println()
	}

	// 🔴 And the coverage read agrees with what just happened, cell for cell.
	fmt.Println("  the coverage table's claim for this language, checked against the run above:")
	for _, c := range transform.CoverageFor(string(variantspec.DimLoop)) {
		if !strings.EqualFold(c.Language, ir.Workflow.Language) {
			continue
		}
		verdict := "refuses"
		if c.Status == transform.CoverageMaterializes {
			verdict = "materializes"
		}
		extra := ""
		if c.Cause != "" {
			extra = "  cause=" + string(c.Cause)
		}
		if c.MissingArtifact != "" {
			extra += "  owes=" + c.MissingArtifact
		}
		fmt.Printf("    %-14s %-13s%s\n", c.Form, verdict, extra)
	}
	fmt.Println()
	fmt.Println("  🔴 `reflexion` says `materializes` for Python and still refuses on most nodes here, and")
	fmt.Println("     both are true: a `materializes` cell is a claim about the (language, strategy) PAIR,")
	fmt.Println("     not a promise about every call site in it. Most of this repository assembles its")
	fmt.Println("     requests elsewhere, which the shape table above says in the engine's own words.")
	fmt.Println()
}

// ── §5 the operator, and the gate that can say no ────────────────────────────────────────────────

func operatorAndGate(ir *discovery.IR, base *variantspec.VariantSpec, regs *harnessRegs) {
	fmt.Println("--- 5. the operator proposes; the admissibility gate decides ---")

	node := base.Order[0]
	menu := proposal.Menu{}
	for _, ref := range regs.order {
		e := regs.byRef[ref]
		st := registry.HarnessStrategyNamed(e.Spec.Strategy)
		menu.HarnessStrategies = append(menu.HarnessStrategies, proposal.HarnessChoice{
			Ref: ref, Strategy: e.Spec.Strategy, Title: st.Title(), MaxTurns: ceilingOf(e),
		})
	}

	in := proposal.OperatorInput{
		Diagnosis: diagnosis.Diagnosis{
			DiagID: "d-scaffold", NodeID: node, Confidence: 0.8,
			EvidenceCaseIDs: []string{"case-14", "case-27", "case-31"}, Source: diagnosis.SourceRule,
		},
		Signal:  proposal.SignalScaffoldMismatch,
		Pattern: patternclassifier.Reflection,
		Base:    base,
		Menu:    menu,
	}

	var cands []proposal.Candidate
	for _, op := range proposal.DefaultCatalog() {
		if op.Kind() != proposal.OpHarnessStrategy {
			continue
		}
		c, err := op.Propose(in)
		if err != nil {
			log.Fatalf("propose: %v", err)
		}
		cands = append(cands, c...)
	}
	fmt.Printf("  signal %q on node %s (pattern=reflection) → %d candidate(s)\n",
		proposal.SignalScaffoldMismatch, short(node), len(cands))
	if len(cands) == 0 {
		log.Fatal("the operator emitted nothing against a real scaffold mismatch")
	}
	for _, c := range cands {
		spec := c.Spec
		h, _ := hashAndCanon(ir, spec, regs)
		e := regs.byRef[spec.Nodes[node].HarnessRef]
		label := e.Spec.Strategy
		if s := paramSummary(e); s != "" {
			label += " " + s
		}
		fmt.Printf("    %-52s config_hash=%s  expected_gain=%.3f (a PRIOR, never a result)\n",
			label, short(h), c.ExpectedGain)
	}
	fmt.Println()
	fmt.Println("  the rationale states the TRADE-OFF and claims no outcome in either direction:")
	for _, line := range wrap(cands[0].Rationale, 92) {
		fmt.Printf("    %s\n", line)
	}
	fmt.Println()

	// 🔴 The gate, run in both directions. A gate that cannot reject is not a gate.
	fmt.Println("  🔴 the admissibility gate, over HELD-OUT measurements (constructed here — this run")
	fmt.Println("     parses a repository, it does not evaluate one; the arithmetic and the verdicts are real):")
	baseline := proposal.HarnessMeasurement{
		TaskSuccess: 0.70, CostUSD: 0.010, LatencyMS: 1000, MaxTurns: 1,
		CaseIDs: []string{"held-1", "held-2", "held-3"},
	}
	tuning := []string{"case-14", "case-27", "case-31"}
	for _, c := range []struct {
		label     string
		candidate proposal.HarnessMeasurement
		tuning    []string
	}{
		{"a 3-turn loop that bought 18 points for 2.5× the cost",
			proposal.HarnessMeasurement{TaskSuccess: 0.88, CostUSD: 0.025, LatencyMS: 2400, MaxTurns: 3,
				CaseIDs: []string{"held-1", "held-2", "held-3"}}, tuning},
		{"a 9-turn loop that bought 2 points for 9× the cost",
			proposal.HarnessMeasurement{TaskSuccess: 0.72, CostUSD: 0.090, LatencyMS: 9000, MaxTurns: 9,
				CaseIDs: []string{"held-1", "held-2", "held-3"}}, tuning},
		{"a 4-turn loop that cost 4× and answered no better",
			proposal.HarnessMeasurement{TaskSuccess: 0.70, CostUSD: 0.040, LatencyMS: 4000, MaxTurns: 4,
				CaseIDs: []string{"held-1", "held-2"}}, tuning},
		{"a superb result measured partly on the tuning cases",
			proposal.HarnessMeasurement{TaskSuccess: 0.99, CostUSD: 0.011, LatencyMS: 1100, MaxTurns: 2,
				CaseIDs: []string{"held-1", "case-27"}}, tuning},
	} {
		v := proposal.AdmitHarnessSwap(proposal.HarnessAdmissibility{
			Baseline: baseline, Candidate: c.candidate, TuningCaseIDs: c.tuning,
		})
		mark := "REJECTED"
		if v.Admitted {
			mark = "ADMITTED"
		}
		fmt.Printf("    %-8s %s\n", mark, c.label)
		for _, line := range wrap(v.Reason, 88) {
			fmt.Printf("             %s\n", line)
		}
	}
	fmt.Println()
	fmt.Println("  ✅ the gate goes both ways, and the last row is the one that matters most: a candidate")
	fmt.Println("     with the BEST numbers on the table is refused for an EVIDENCE failure rather than a")
	fmt.Println("     quality one, because part of its measurement came from the cases that produced the")
	fmt.Println("     proposal. A win measured on its own tuning set is overfitting with a confidence")
	fmt.Println("     interval.")
	fmt.Println()
}

// ── §6 the authored path ─────────────────────────────────────────────────────────────────────────

func authoredPath(ir *discovery.IR, base *variantspec.VariantSpec, regs *harnessRegs) {
	fmt.Println("--- 6. a user authors a harness change: modeled, recorded, refused, backed out ---")

	node := base.Order[0]
	baseHash, _ := hashAndCanon(ir, base, regs)

	opts := authoring.HarnessStrategyOptions()
	fmt.Printf("  offered strategies (closed set of %d), with the ceiling each one may reach:\n", len(opts))
	for _, o := range opts {
		fmt.Printf("    %-14s ceiling=%2d  %s\n", o.Strategy, o.MaxTurnCeiling, o.Title)
	}
	fmt.Println()

	// 🔴 The boundary, stated BEFORE the choice, PER CELL, read from the engine's coverage table.
	fmt.Printf("  the per-cell boundary for language %q, stated before the choice:\n", ir.Workflow.Language)
	for _, a := range authoring.HarnessApplicabilityFor(harnessCoverageReader{}, ir.Workflow.Language) {
		verdict := "refused"
		if a.Applicable {
			verdict = "applies"
		}
		perm := ""
		if a.Permanent {
			perm = "  (permanent — not a backlog item)"
		}
		owes := ""
		if a.MissingArtifact != "" {
			owes = "  owes=" + a.MissingArtifact
		}
		fmt.Printf("    %-14s %-8s%s%s\n", a.Strategy, verdict, perm, owes)
	}
	fmt.Println()

	// The cost, stated before the choice.
	for _, o := range opts {
		if o.CostWarning == "" {
			continue
		}
		fmt.Printf("  the cost sentence a user reads before selecting %s:\n", o.Strategy)
		for _, line := range wrap(o.CostWarning, 92) {
			fmt.Printf("    %s\n", line)
		}
		fmt.Println()
		break
	}

	// A schema-violating selection is rejected before anything is sealed.
	store := registry.NewStore(nil, nil)
	if err := authoring.ValidateHarnessSelection(harnessValidator{store}, "authored", "react-loop",
		json.RawMessage(`{"max_turns":99,"stop_condition":"no-tool-call"}`)); err == nil {
		log.Fatal("a turn ceiling above the cap was accepted")
	} else {
		fmt.Println("  a ceiling above the cap is rejected BEFORE sealing, so no version_id is minted for")
		fmt.Println("  content that was never stored:")
		for _, line := range wrap(err.Error(), 92) {
			fmt.Printf("    %s\n", line)
		}
	}
	fmt.Println()

	// The authored draft — through the SHARED spine, the same Edit an operator candidate rides.
	draft := authoring.Draft{
		ID: "draft-1", WorkflowID: base.WorkflowID, ParentVariantID: baseHash,
		Actor: authoring.Actor{ID: "engineer@example", TenantID: "tenant-hermes"},
		Edits: map[string]authoring.Edit{node: authoring.HarnessEdit(regs.reflexion3Ref)},
	}
	authored, err := draft.Derive(base)
	if err != nil {
		log.Fatalf("derive: %v", err)
	}
	authoredHash, _ := hashAndCanon(ir, authored, regs)
	fmt.Printf("  authored: node %s → reflexion (max_turns=3)\n", short(node))
	fmt.Printf("    config_hash=%s  parent=%s  origin=user  state=unverified\n",
		short(authoredHash), short(baseHash))
	if authoredHash == baseHash {
		log.Fatal("the authored change did not move the hash")
	}
	fmt.Println("    dimensions touched:", draft.TouchedDimensions())
	fmt.Println()

	// One spine, two origins.
	proposed := cloneSpec(base)
	setHarness(proposed, node, regs.reflexion3Ref)
	proposedHash, _ := hashAndCanon(ir, proposed, regs)
	if proposedHash != authoredHash {
		fmt.Printf("    ❌ the same configuration hashed differently by origin: %s vs %s\n",
			short(authoredHash), short(proposedHash))
		log.Fatal("origin forked identity")
	}
	fmt.Printf("  ✅ the operator's route to the same configuration hashes identically (%s):\n", short(proposedHash))
	fmt.Println("     one spine, two origins. Origin is recorded on the candidate and never hashed.")
	fmt.Println()

	// Clearing reproduces the prior hash byte-exactly.
	clearDraft := authoring.Draft{
		ID: "draft-2", WorkflowID: base.WorkflowID, ParentVariantID: authoredHash,
		Actor: draft.Actor,
		Edits: map[string]authoring.Edit{node: authoring.ClearHarnessEdit()},
	}
	cleared, err := clearDraft.Derive(authored)
	if err != nil {
		log.Fatalf("derive clear: %v", err)
	}
	clearedHash, _ := hashAndCanon(ir, cleared, regs)
	fmt.Printf("  cleared:  config_hash=%s\n", short(clearedHash))
	if clearedHash != baseHash {
		fmt.Println("    ❌ clearing left residue: the user cannot fully back out of an authored change")
		log.Fatal("clear is not byte-exact")
	}
	fmt.Println("  ✅ byte-exact back-out. The key disappears from the node, so the configuration returns")
	fmt.Println("     to exactly the bytes it had before the selection.")
	fmt.Println()

	// And the runtime the authored strategy would drive is bounded, on the same params.
	p, err := harnessruntime.DecodeParams(regs.byRef[regs.reflexion3Ref].Spec.Params)
	if err != nil {
		log.Fatalf("decode params: %v", err)
	}
	turns := 0
	out, err := harnessruntime.Run(
		harnessruntime.Config{Strategy: "reflexion", Params: p}, harnessruntime.Hosts{},
		[]harnessruntime.Message{{Role: "user", Content: "q"}},
		func([]harnessruntime.Message) (string, error) { turns++; return "still working", nil })
	if err != nil {
		log.Fatalf("runtime: %v", err)
	}
	fmt.Printf("  the runtime this configuration would drive, against a stop condition never satisfied:\n")
	fmt.Printf("    turns executed=%d (ceiling=%d)  stop=%s  trace records=%d\n",
		out.Turns, p.MaxTurns, out.Stop, len(out.Trace))
	if out.Turns != p.MaxTurns || turns != p.MaxTurns {
		log.Fatal("the loop exceeded or undershot its declared ceiling")
	}
	fmt.Println("  ✅ bounded by construction, terminated at its ceiling, and RECORDED that it stopped")
	fmt.Println("     there — distinguishably from a run whose stop condition was satisfied.")
	fmt.Println()
}

// ── the registry double ──────────────────────────────────────────────────────────────────────────

// harnessRegs is a Registries whose harness entries are validated by the real registry path, so the
// params this run uses are ones the production seal path would accept. The other five dimensions
// resolve nothing: this run pins no model, prompt, skill, context, or memory.
type harnessRegs struct {
	byRef         map[string]*registry.HarnessEntry
	order         []string
	identityRef   string
	reflexion3Ref string
}

func harnessRegistries() *harnessRegs {
	m := &harnessRegs{byRef: map[string]*registry.HarnessEntry{}}
	add := func(name, strategy, params string) string {
		st := registry.HarnessStrategyNamed(strategy)
		if st == nil {
			log.Fatalf("%q is not a builtin strategy", strategy)
		}
		if _, _, err := registry.NewStore(nil, nil).ValidateHarnessParams(name, strategy, json.RawMessage(params)); err != nil {
			log.Fatalf("fixture %q is not schema-valid: %v", name, err)
		}
		ref := fmt.Sprintf("%064x", len(m.order)+1)
		m.byRef[ref] = &registry.HarnessEntry{
			VersionID: ref, Name: name,
			Spec:     registry.HarnessSpec{Strategy: strategy, Params: json.RawMessage(params)},
			Strategy: st,
		}
		m.order = append(m.order, ref)
		return ref
	}

	m.identityRef = add("one-shot", "single-shot", `{}`)
	m.reflexion3Ref = add("revise-3", "reflexion",
		`{"max_turns":3,"stop_condition":"max-turns","reflection_prompt":"find the error and fix it"}`)
	add("revise-5", "reflexion",
		`{"max_turns":5,"stop_condition":"answer-marker","answer_marker":"FINAL","reflection_prompt":"find the error and fix it"}`)
	add("react-6", "react-loop", `{"max_turns":6,"stop_condition":"no-tool-call","retry_budget":1}`)
	add("plan-4", "plan-execute", `{"max_turns":4,"stop_condition":"plan-complete"}`)
	add("critic-3", "critic-loop", fmt.Sprintf(`{"max_turns":3,"critic_model_ref":%q}`, strings.Repeat("c", 64)))
	return m
}

func (m *harnessRegs) ResolveModel(context.Context, string) (*registry.ModelEntry, error) {
	return nil, registry.ErrNotFound
}
func (m *harnessRegs) ResolvePrompt(context.Context, string) (*registry.PromptEntry, error) {
	return nil, registry.ErrNotFound
}
func (m *harnessRegs) ResolveSkill(context.Context, string) (*registry.SkillEntry, error) {
	return nil, registry.ErrNotFound
}
func (m *harnessRegs) ResolveContextPolicy(context.Context, string) (*registry.ContextEntry, error) {
	return nil, registry.ErrNotFound
}
func (m *harnessRegs) ResolveMemory(context.Context, string) (*registry.MemoryEntry, error) {
	return nil, registry.ErrNotFound
}
func (m *harnessRegs) ResolveHarness(_ context.Context, id string) (*registry.HarnessEntry, error) {
	if e, ok := m.byRef[id]; ok {
		return e, nil
	}
	return nil, registry.ErrNotFound
}

// ResolveLoop answers for P34's loop registry. This proof exercises the LEGACY loop-bearing harness
// path, which P34 keeps resolvable indefinitely — so it publishes no loop entries and every loop_ref
// misses, which is the fail-closed answer rather than an empty one.
func (m *harnessRegs) ResolveLoop(context.Context, string) (*registry.LoopEntry, error) {
	return nil, registry.ErrNotFound
}

// harnessCoverageReader adapts the transform engine's table for the authoring boundary — the same
// adapter the BFF uses, so this run reads the boundary from where the console reads it.
type harnessCoverageReader struct{}

func (harnessCoverageReader) HarnessCoverage(language string) []authoring.HarnessCoverageCell {
	var out []authoring.HarnessCoverageCell
	for _, c := range transform.CoverageFor(string(variantspec.DimLoop)) {
		if !strings.EqualFold(c.Language, language) {
			continue
		}
		out = append(out, authoring.HarnessCoverageCell{
			Language: c.Language, Strategy: c.Form,
			Materializes:    c.Status == transform.CoverageMaterializes,
			Cause:           string(c.Cause),
			MissingArtifact: c.MissingArtifact, Note: c.Note,
		})
	}
	return out
}

type harnessValidator struct{ store *registry.Store }

func (v harnessValidator) ValidateHarnessParams(name, strategy string, params json.RawMessage) (registry.HarnessStrategy, json.RawMessage, error) {
	return v.store.ValidateHarnessParams(name, strategy, params)
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

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

func cloneSpec(s *variantspec.VariantSpec) *variantspec.VariantSpec {
	out := &variantspec.VariantSpec{
		WorkflowID: s.WorkflowID, ParentVariantID: s.ParentVariantID, SourceRevision: s.SourceRevision,
		Order: append([]string(nil), s.Order...),
		Nodes: make(map[string]variantspec.NodeOverride, len(s.Nodes)),
		Edges: append([]variantspec.Edge(nil), s.Edges...),
	}
	for k, v := range s.Nodes {
		out.Nodes[k] = v
	}
	return out
}

func setHarness(s *variantspec.VariantSpec, node, ref string) {
	o := s.Nodes[node]
	o.HarnessRef = ref
	s.Nodes[node] = o
}

func hashAndCanon(ir *discovery.IR, spec *variantspec.VariantSpec, regs *harnessRegs) (string, []byte) {
	r, err := variantspec.Resolve(context.Background(), spec, ir, regs)
	if err != nil {
		log.Fatalf("resolve: %v", err)
	}
	canon, err := r.Config.Canonical()
	if err != nil {
		log.Fatalf("canonical: %v", err)
	}
	return r.ConfigHash, canon
}

func ceilingOf(e *registry.HarnessEntry) int {
	p, err := harnessruntime.DecodeParams(e.Spec.Params)
	if err != nil || p.MaxTurns < 1 {
		return 1
	}
	return p.MaxTurns
}

func paramSummary(e *registry.HarnessEntry) string {
	var m map[string]any
	if err := json.Unmarshal(e.Spec.Params, &m); err != nil || len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for _, k := range sortedKeys(m) {
		v := fmt.Sprintf("%v", m[k])
		if len(v) > 18 {
			v = v[:15] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return "(" + strings.Join(parts, " ") + ")"
}

func asRewrite(err error, target **transform.RewriteError) bool {
	for e := err; e != nil; {
		if re, ok := e.(*transform.RewriteError); ok {
			*target = re
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
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

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// openingClause reduces a refusal sentence to the clause that identifies the SHAPE it refused on, so one
// structural finding does not appear once per strategy and once per local variable name. Presentation
// only — nothing downstream reads the key.
func openingClause(detail, strategy string) string {
	s := strings.ReplaceAll(detail, `"`+strategy+`"`, `"<strategy>"`)
	s = kwargsName.ReplaceAllString(s, "**<mapping>")
	for _, sep := range []string{", so ", ", and a call site ", ". "} {
		if i := strings.Index(s, sep); i > 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
}

var kwargsName = regexp.MustCompile(`\*\*[A-Za-z_][A-Za-z0-9_]*`)

// wrap breaks a long engine sentence for a terminal, WITHOUT abbreviating it. A refusal a reader has to
// act on is printed whole; only a survey line may be trimmed.
func wrap(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var out []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			out = append(out, line)
			line = w
			continue
		}
		line += " " + w
	}
	return append(out, line)
}
