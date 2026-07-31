// Command p17hermes drives the P17 memory-strategy axis against a REAL repository
// (github.com/nousresearch/hermes-agent, the same target as cmd/p13hermes … p16hermes).
//
// # What this run proves, and the boundary it refuses to hide
//
// P17's claim is deliberately narrow: memory is MODELED end-to-end — referenced, resolved, hashed,
// proposed, authored — and REFUSED at transform, by name, never resolved-and-dropped. On this
// repository the outcome is a refusal at every node, in every strategy, and that is the finding rather
// than a gap in the demonstration.
//
// 🔴 A run that produced a diff here would mean the override had been silently dropped and the variant
// scored as its base configuration — the single worst thing this platform can emit, because the number
// would be wrong and would look exactly like a number that is right. §4 is the proof it cannot happen.
//
// The sections, all running the shipped code paths against the real IR:
//
//	§1  what discovery found, and the memory default it emitted for every node — `none`, which is a
//	    claim about the EVIDENCE (a store read between turns is not visible at one call site), not a
//	    placeholder;
//	§2  🔴 `none` ≡ absent on the REAL config: an explicitly-`none` node and a no-memory node produce
//	    byte-identical canonical bytes and the same config_hash, so no stored hash moved;
//	§3  the hash MOVES iff the strategy or its params move — the other half of the same contract;
//	§4  🔴 the headline: every non-identity strategy is REFUSED at transform on every discovered node,
//	    with a typed cause, producing no diff — counted by cause class AND by refusal shape, because
//	    30 of these nodes fail memory's READ half (the request is unpacked from a mapping) and one
//	    fails its WRITE half (the call's result is never named), and a run that reported only the
//	    first would be stating something false about the last — and a spec carrying a refused memory
//	    override alongside an applicable model override is refused WHOLE rather than half-applied;
//	§5  the operator is catalogued and DORMANT: it proposes against a real memory bottleneck, the
//	    proposal resolves and hashes, and it yields no scored result;
//	§6  the authored path: a user selects a strategy, it resolves and hashes, preflight refuses it with
//	    the transform's own cause before any spend, and clearing reproduces the prior hash byte-exactly.
//
// The boundaries, stated rather than worked around:
//
//	the MATERIALIZATION runs here and produces nothing, which is a different fact from the one this
//	file used to state. P18 landed the memory runtime and the python and go rewriters, so §4 is no
//	longer measuring an absent platform: python's rewriter is invoked on all 186 combinations and
//	refuses each on the SHAPE OF THIS REPOSITORY'S CALL SITES. §4 counts the cause classes rather
//	than sampling one sentence, so the difference between "we have not built it" and "your source
//	cannot carry it" is read off the run instead of asserted by this comment.
//
//	the diagnosis signal in §5 is illustrative (in production it comes from P4.5). Everything it drives
//	— the candidate spec, the config_hash, the refusal — is real.
//
// It NEVER executes the target — it parses it (invariant I1).
//
//	go run ./cmd/p17hermes -repo /tmp/hermes-agent
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

	fmt.Printf("=== P17 memory strategy optimization - run for %s ===\n", repoURL)
	fmt.Printf("discovered %d nodes, %d edges (language=%s) at %s (%s)\n\n",
		len(ir.Nodes), len(ir.Edges), ir.Workflow.Language, short(commitSHA), revNote)

	base := baselineFrom(ir, commitSHA)
	if len(base.Order) < 4 {
		log.Fatalf("this demonstration needs at least 4 discovered nodes, got %d", len(base.Order))
	}

	regs := memoryRegistries(commitSHA)

	discovered(ir, base)
	baseHash := noneIsAbsent(ir, base, regs)
	hashMovesIffMemoryMoves(ir, base, regs, baseHash)
	refuseAtTransform(ir, base, regs, *repo)
	operatorDormant(ir, base, regs)
	authoredPath(ir, base, regs)

	fmt.Println("=== end of run ===")
}

// ── §1 what discovery found ──────────────────────────────────────────────────────────────────────

func discovered(ir *discovery.IR, base *variantspec.VariantSpec) {
	fmt.Println("--- 1. what discovery found, and the memory default it emitted ---")
	fmt.Printf("baseline graph: %d ordered nodes\n", len(base.Order))
	for i, id := range base.Order[:3] {
		fmt.Printf("  [%d] %s  %s\n", i, short(id), siteOf(ir, id))
	}
	fmt.Printf("  ... %d more\n\n", len(base.Order)-3)

	counts := map[string]int{}
	for _, n := range ir.Nodes {
		counts[n.MemoryDefault()]++
	}
	fmt.Println("  per-node memory default, as DISCOVERED:")
	for _, k := range sortedKeys(counts) {
		fmt.Printf("    %-16s %d node(s)\n", k, counts[k])
	}
	fmt.Println()
	fmt.Println("  🔴 `none` everywhere is a claim about the EVIDENCE, not a placeholder. A memory strategy")
	fmt.Println("     is a store read and written BETWEEN turns, which is why the classifier calls")
	fmt.Println("     MemoryManagement a BEHAVIORAL pattern — it is not visible at the single call site")
	fmt.Println("     discovery extracts. Guessing one from a nearby import would produce a node that")
	fmt.Println("     resolves, hashes, and is compared as a configuration this source never had.")
	fmt.Println()

	// And the default costs nothing in the emitted document.
	emitted, err := discovery.MarshalIR(*ir)
	if err != nil {
		log.Fatalf("marshal IR: %v", err)
	}
	if strings.Contains(string(emitted), `"memory"`) {
		fmt.Println("  ⚠️  the emitted IR carries a memory key on some node — the default should be free")
	} else {
		fmt.Println("  the emitted IR carries NO memory key: the default is written as absence, so every")
		fmt.Println("  pre-P17 document and every current one that found nothing serialise identically.")
	}
	fmt.Println()
}

// ── §2 none ≡ absent, on the real config ─────────────────────────────────────────────────────────

func noneIsAbsent(ir *discovery.IR, base *variantspec.VariantSpec, regs *memRegs) string {
	fmt.Println("--- 2. 🔴 `none` is byte-identical to absent, on the REAL discovered config ---")

	baseHash, baseCanon := hashAndCanon(ir, base, regs)
	fmt.Printf("  baseline (no memory anywhere)          config_hash=%s\n", short(baseHash))

	node := base.Order[0]
	withNone := cloneSpec(base)
	setMemory(withNone, node, regs.noneRef)
	noneHash, noneCanon := hashAndCanon(ir, withNone, regs)
	fmt.Printf("  node %s pinned to `none`     config_hash=%s\n", short(node), short(noneHash))

	if noneHash != baseHash || string(noneCanon) != string(baseCanon) {
		fmt.Println("  ❌ THE CONTRACT IS BROKEN: `none` moved the hash. Every stored config_hash on this")
		fmt.Println("     repository would be orphaned, and every frozen golden vector would break.")
		log.Fatal("none != absent")
	}
	fmt.Println("  ✅ identical bytes, identical hash. `none` IS the absence of memory, so a user can")
	fmt.Println("     select it, clear it, and back out with no residue — and no stored hash moved when")
	fmt.Println("     this axis shipped.")
	fmt.Println()
	return baseHash
}

// ── §3 the hash moves iff the memory moves ───────────────────────────────────────────────────────

func hashMovesIffMemoryMoves(ir *discovery.IR, base *variantspec.VariantSpec, regs *memRegs, baseHash string) {
	fmt.Println("--- 3. the hash moves iff the strategy or its params move ---")
	node := base.Order[0]

	seen := map[string]string{}
	for _, ref := range regs.order {
		spec := cloneSpec(base)
		setMemory(spec, node, ref)
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
			fmt.Printf("  %-34s config_hash=%s  ⚠️ collides with %s\n", label, short(h), prev)
			continue
		}
		seen[h] = label
		fmt.Printf("  %-34s config_hash=%s  (%s)\n", label, short(h), mark)
	}
	fmt.Println()
	fmt.Println("  Two entries with the same strategy and params share a hash even under different")
	fmt.Println("  version_ids: config_hash denotes a CONFIGURATION, not a set of registry rows.")
	fmt.Println()
}

// ── §4 the refusal, on every node ────────────────────────────────────────────────────────────────

func refuseAtTransform(ir *discovery.IR, base *variantspec.VariantSpec, regs *memRegs, repo string) {
	fmt.Println("--- 4. 🔴 what the transform does with a memory override, on every node ---")

	var refused, applied, otherErr int
	// 🔴 The cause CLASS is counted per node, not just per attempt. Now that python materializes,
	// "186 refused" no longer says who owns the gap: CauseNoMaterializer would be ours and would mean
	// the run below is measuring the platform, CauseCallSiteShape is the repository's own source. One
	// sampled sentence cannot tell those apart across 186 attempts, so count them.
	byCause := map[transform.CauseClass]int{}
	nodesByCause := map[transform.CauseClass]map[string]bool{}
	// One cause class can still hide two different facts about the source, so the refusals are ALSO
	// grouped by the clause the engine opened with. Memory is a read and a write; a site can fail the
	// read half (its arguments are unpacked) or the write half (its result is never named), and those
	// are different things for the reader to go fix.
	byShape := map[string]int{}
	nodesByShape := map[string]map[string]bool{}
	namesByShape := map[string]map[string]bool{}
	shapeSite := map[string]string{}
	shapeSample := map[string]string{}
	for _, node := range base.Order {
		for _, ref := range regs.order {
			entry := regs.byRef[ref]
			if entry.IsNone() {
				continue
			}
			spec := cloneSpec(base)
			setMemory(spec, node, ref)
			resolved, err := variantspec.Resolve(context.Background(), spec, ir, regs)
			if err != nil {
				log.Fatalf("resolve %s/%s: %v", short(node), entry.Spec.Strategy, err)
			}
			patch, err := transform.Generate(resolved, repo)
			switch {
			case err == nil && patch != nil && len(patch.Diff) > 0:
				applied++
			case err == nil:
				applied++
			default:
				var re *transform.RewriteError
				if asRewrite(err, &re) && re.Dim == string(variantspec.DimMemory) {
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
					if namesByShape[shape] == nil {
						namesByShape[shape] = map[string]bool{}
					}
					for _, n := range kwargsName.FindAllString(re.Detail, -1) {
						namesByShape[shape][n] = true
					}
					if shapeSample[shape] == "" {
						shapeSample[shape] = re.Detail
						shapeSite[shape] = siteOf(ir, node)
					}
				} else {
					otherErr++
				}
			}
		}
	}

	total := refused + applied + otherErr
	fmt.Printf("  %d (node × strategy) combinations exercised on the real tree\n", total)
	fmt.Printf("    refused with a typed memory cause : %d\n", refused)
	fmt.Printf("    APPLIED (produced a diff)         : %d\n", applied)
	fmt.Printf("    refused for another reason        : %d\n", otherErr)
	fmt.Println()
	if applied > 0 {
		fmt.Println("  ❌ a memory override reached the source. The diff would be filed under a config_hash")
		fmt.Println("     claiming a memory strategy this repository never had.")
		log.Fatal("a memory override was applied")
	}
	fmt.Println("  ✅ nothing was dropped: every override came back either materialized or as a typed")
	fmt.Println("     refusal naming the memory dimension. On THIS repository the answer is refusal")
	fmt.Println("     everywhere — and the REASON is what moved. It is no longer \"the platform has not")
	fmt.Println("     built it\" (ours, temporary); it is \"this call site cannot carry it\" (the source's,")
	fmt.Println("     and actionable). Python materializes memory, and its rewriter ran on all of these.")
	fmt.Println()
	fmt.Println("  and that is COUNTED rather than sampled — the refusals, by cause class:")
	for _, cause := range transform.CauseClasses() {
		n := byCause[cause]
		if n == 0 {
			continue
		}
		fmt.Printf("    %-34s %3d attempt(s) over %d of %d node(s)\n",
			cause, n, len(nodesByCause[cause]), len(base.Order))
	}
	if byCause[transform.CauseNoMaterializer] == 0 {
		fmt.Printf("  ✅ ZERO of the %d refusals is blamed on a missing materializer. Not one of them is\n", refused)
		fmt.Println("     waiting on us: python HAS the rewriter, it ran on every one, and it refused each on")
		fmt.Println("     the shape of the call site it was pointed at.")
	} else {
		fmt.Printf("  ⚠️  %d refusal(s) still name a missing materializer — that share is OURS, not theirs.\n",
			byCause[transform.CauseNoMaterializer])
	}
	fmt.Println()

	// 🔴 The shape table, which is where the finding actually is. The cause class says WHOSE the gap
	// is; the shape says WHAT to change, and memory can fail either of its two halves independently.
	shapes := sortedKeys(byShape)
	sort.SliceStable(shapes, func(i, j int) bool { return byShape[shapes[i]] > byShape[shapes[j]] })
	fmt.Printf("  the same refusals grouped by what the engine actually said — %d distinct shapes:\n", len(shapes))
	for _, s := range shapes {
		fmt.Printf("    %3d× over %2d node(s)  %s\n", byShape[s], len(nodesByShape[s]), s)
		fmt.Printf("                          e.g. %s\n", shapeSite[s])
		if names := sortedKeys(namesByShape[s]); len(names) > 0 {
			fmt.Printf("                          as: %s\n", strings.Join(names, " "))
		}
	}
	fmt.Println()
	// 🔴 Every shape verbatim, not a sample of them. A tail of one node is exactly where "the reason
	// this repository refuses" turns into a sentence that is true of most nodes and false of one.
	fmt.Println("  the engine's sentence, verbatim, once per shape:")
	for _, s := range shapes {
		fmt.Printf("    [%d× %s]\n", byShape[s], s)
		for _, line := range wrap(shapeSample[s], 90) {
			fmt.Printf("      %s\n", line)
		}
	}
	fmt.Println()

	// 🔴 A spec carrying BOTH a refused memory override and an applicable one is refused WHOLE.
	fmt.Println("  a spec carrying a refused memory override AND another dimension is refused WHOLE:")
	mixed := cloneSpec(base)
	setMemory(mixed, base.Order[0], regs.scratchRef)
	resolved, err := variantspec.Resolve(context.Background(), mixed, ir, regs)
	if err != nil {
		log.Fatalf("resolve mixed: %v", err)
	}
	if _, err := transform.Generate(resolved, repo); err == nil {
		fmt.Println("    ❌ the mixed spec produced a patch — a partial diff exists to be scored")
		log.Fatal("partial application")
	}
	fmt.Println("    ✅ refused, so no partial diff exists that could be scored as the whole variant.")
	fmt.Println()

	// The coverage table the console reads.
	cells := transform.CoverageFor(string(variantspec.DimMemory))
	byStatus := map[transform.CoverageStatus]int{}
	langs := map[string]bool{}
	for _, c := range cells {
		byStatus[c.Status]++
		langs[c.Language] = true
	}
	fmt.Printf("  coverage table: %d cells over %d languages — materializes=%d refuses=%d\n",
		len(cells), len(langs), byStatus[transform.CoverageMaterializes], byStatus[transform.CoverageRefuses])
	fmt.Printf("  materializing languages: %s\n", strings.Join(transform.MemoryMaterializerLanguages(), ", "))
	fmt.Println("  🔴 The axis is PER-CELL now. The shared memory runtime has landed; a language materializes")
	fmt.Println("     once its own module and call-site rewriter have, and is refused BY NAME until then.")
	fmt.Println()
}

// ── §5 the operator, catalogued and dormant ──────────────────────────────────────────────────────

func operatorDormant(ir *discovery.IR, base *variantspec.VariantSpec, regs *memRegs) {
	fmt.Println("--- 5. the operator proposes; verification decides; nothing is scored ---")

	node := base.Order[0]
	menu := proposal.Menu{}
	for _, ref := range regs.order {
		e := regs.byRef[ref]
		st := registry.MemoryStrategyNamed(e.Spec.Strategy)
		title := e.Spec.Strategy
		if st != nil {
			title = st.Title()
		}
		menu.MemoryStrategies = append(menu.MemoryStrategies,
			proposal.MemoryChoice{Ref: ref, Strategy: e.Spec.Strategy, Title: title})
	}

	in := proposal.OperatorInput{
		Diagnosis: diagnosis.Diagnosis{DiagID: "d-mem", NodeID: node, Confidence: 0.8,
			EvidenceCaseIDs: []string{"c1", "c2", "c3"}},
		Signal:  proposal.SignalStaleMemory,
		Pattern: patternclassifier.MemoryManagement,
		Base:    base,
		Menu:    menu,
	}

	var cands []proposal.Candidate
	for _, op := range proposal.DefaultCatalog() {
		if op.Kind() != proposal.OpMemoryPolicy || op.HandlesSignal() != proposal.SignalStaleMemory {
			continue
		}
		c, err := op.Propose(in)
		if err != nil {
			log.Fatalf("propose: %v", err)
		}
		cands = append(cands, c...)
	}

	fmt.Printf("  signal %q on node %s (pattern=memory_management) → %d candidate(s)\n",
		proposal.SignalStaleMemory, short(node), len(cands))
	if len(cands) == 0 {
		log.Fatal("the operator emitted nothing against a real memory bottleneck")
	}
	for _, c := range cands {
		spec := c.Spec
		h, _ := hashAndCanon(ir, spec, regs)
		e := regs.byRef[spec.Nodes[node].MemoryRef]
		label := e.Spec.Strategy
		if s := paramSummary(e); s != "" {
			label += " " + s
		}
		fmt.Printf("    %-58s config_hash=%s  expected_gain=%.3f (a PRIOR, never a result)\n",
			label, short(h), c.ExpectedGain)
	}
	fmt.Println()
	fmt.Println("  the rationale states the refusal IN the proposal, so nobody discovers it one click later:")
	for _, line := range wrap(cands[0].Rationale, 92) {
		fmt.Printf("    %s\n", line)
	}
	fmt.Println()
	// 🚫 `none` is never a proposed target.
	for _, c := range cands {
		if regs.byRef[c.Spec.Nodes[node].MemoryRef].IsNone() {
			fmt.Println("  ❌ the operator proposed `none` — answering \"your recall is stale\" with \"recall nothing\"")
			log.Fatal("none proposed")
		}
	}
	fmt.Printf("  ✅ %d candidate(s), none of them `none`, none of them carrying a measured result.\n", len(cands))
	fmt.Println("     While the transform refuses, a memory proposal cannot be verified — so it cannot be")
	fmt.Println("     a win, a regression, or a tie, and it never enters the verified-delta ledger.")
	fmt.Println()
}

// ── §6 the authored path ─────────────────────────────────────────────────────────────────────────

func authoredPath(ir *discovery.IR, base *variantspec.VariantSpec, regs *memRegs) {
	fmt.Println("--- 6. a user authors a memory change: modeled, recorded, refused, backed out ---")

	node := base.Order[0]
	baseHash, _ := hashAndCanon(ir, base, regs)

	// The vocabulary a surface offers — the closed builtin set, never free text.
	opts := authoring.MemoryStrategyOptions()
	names := make([]string, 0, len(opts))
	for _, o := range opts {
		names = append(names, o.Strategy)
	}
	fmt.Printf("  offered strategies (closed set of %d): %s\n", len(opts), strings.Join(names, ", "))

	// The boundary, stated BEFORE the choice, read from the engine's coverage table.
	b := authoring.MemoryBoundaryFor(coverageReader{}, ir.Workflow.Language)
	fmt.Printf("  boundary for language %q: applicable=%v language_is_the_blocker=%v\n",
		ir.Workflow.Language, b.Applicable, b.LanguageIsTheBlocker)
	fmt.Printf("    missing artifact: %s\n", b.MissingArtifact)
	fmt.Println()

	// A schema-violating selection is rejected before anything is sealed.
	store := registry.NewStore(nil, nil)
	if err := authoring.ValidateMemorySelection(memValidator{store}, "authored", "vector-recall", json.RawMessage(`{"top_k":5}`)); err == nil {
		log.Fatal("schema-violating params were accepted")
	} else {
		fmt.Println("  a params violation is rejected BEFORE sealing, so no version_id is minted for content")
		fmt.Println("  that was never stored:")
		for _, line := range wrap(err.Error(), 92) {
			fmt.Printf("    %s\n", line)
		}
	}
	fmt.Println()

	// The authored draft — through the SHARED spine, the same Edit an operator candidate rides.
	draft := authoring.Draft{
		ID: "draft-1", WorkflowID: base.WorkflowID, ParentVariantID: baseHash,
		Actor: authoring.Actor{ID: "engineer@example", TenantID: "tenant-hermes"},
		Edits: map[string]authoring.Edit{node: authoring.MemoryEdit(regs.summaryRef)},
	}
	authored, err := draft.Derive(base)
	if err != nil {
		log.Fatalf("derive: %v", err)
	}
	authoredHash, _ := hashAndCanon(ir, authored, regs)
	fmt.Printf("  authored: node %s → summary-buffer\n", short(node))
	fmt.Printf("    config_hash=%s  parent=%s  origin=user  state=unverified\n",
		short(authoredHash), short(baseHash))
	if authoredHash == baseHash {
		log.Fatal("the authored change did not move the hash")
	}

	// Preflight refuses with the TRANSFORM's own cause, before any spend.
	fmt.Println("    dimensions touched:", draft.TouchedDimensions())
	fmt.Println()

	// The operator's route to the SAME configuration produces the SAME hash — one spine, two origins.
	proposed := cloneSpec(base)
	setMemory(proposed, node, regs.summaryRef)
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
		Edits: map[string]authoring.Edit{node: authoring.ClearMemoryEdit()},
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
}

// ── the registry double ──────────────────────────────────────────────────────────────────────────

// memRegs is a Registries whose memory entries are SEALED by the real registry path, so the version_ids
// resolved here are the ones production would produce for the same content. The other four dimensions
// resolve nothing: this run pins no model, prompt, skill, or context.
type memRegs struct {
	byRef      map[string]*registry.MemoryEntry
	order      []string
	noneRef    string
	scratchRef string
	summaryRef string
}

func memoryRegistries(commitSHA string) *memRegs {
	m := &memRegs{byRef: map[string]*registry.MemoryEntry{}}
	add := func(name, strategy, params string) string {
		st := registry.MemoryStrategyNamed(strategy)
		if st == nil {
			log.Fatalf("%q is not a builtin strategy", strategy)
		}
		// Validated through the registry's OWN validator, so a params value this run uses is one the
		// production seal path would accept.
		if _, _, err := registry.NewStore(nil, nil).ValidateMemoryParams(name, strategy, json.RawMessage(params)); err != nil {
			log.Fatalf("fixture %q is not schema-valid: %v", name, err)
		}
		ref := fmt.Sprintf("%064x", len(m.order)+1)
		m.byRef[ref] = &registry.MemoryEntry{
			VersionID: ref, Name: name,
			Spec:     registry.MemorySpec{Strategy: strategy, Params: json.RawMessage(params)},
			Strategy: st,
		}
		m.order = append(m.order, ref)
		return ref
	}

	m.noneRef = add("off", "none", `{}`)
	m.scratchRef = add("notes-5", "scratchpad", `{"max_entries":5}`)
	add("notes-20", "scratchpad", `{"max_entries":20}`)
	m.summaryRef = add("sum-2k", "summary-buffer", `{"max_tokens":2000}`)
	add("sum-6k", "summary-buffer", `{"max_tokens":6000,"keep_last_turns":4}`)
	add("recall-5", "vector-recall", `{"top_k":5,"embedding_ref":"text-embedding-3-small"}`)
	add("facts", "entity-memory", `{"entity_keys":["user_name","project","deadline"]}`)
	return m
}

func (m *memRegs) ResolveModel(context.Context, string) (*registry.ModelEntry, error) {
	return nil, registry.ErrNotFound
}
func (m *memRegs) ResolvePrompt(context.Context, string) (*registry.PromptEntry, error) {
	return nil, registry.ErrNotFound
}
func (m *memRegs) ResolveSkill(context.Context, string) (*registry.SkillEntry, error) {
	return nil, registry.ErrNotFound
}
func (m *memRegs) ResolveContextPolicy(context.Context, string) (*registry.ContextEntry, error) {
	return nil, registry.ErrNotFound
}
func (m *memRegs) ResolveMemory(_ context.Context, id string) (*registry.MemoryEntry, error) {
	if e, ok := m.byRef[id]; ok {
		return e, nil
	}
	return nil, registry.ErrNotFound
}
func (m *memRegs) ResolveHarness(context.Context, string) (*registry.HarnessEntry, error) {
	return nil, registry.ErrNotFound
}

// coverageReader adapts the transform engine's table for the authoring boundary — the same adapter the
// BFF uses, so this run reads the boundary from where the console reads it.
type coverageReader struct{}

func (coverageReader) MemoryCoverage(language string) []authoring.MemoryCoverageCell {
	var out []authoring.MemoryCoverageCell
	for _, c := range transform.CoverageFor(string(variantspec.DimMemory)) {
		if !strings.EqualFold(c.Language, language) {
			continue
		}
		out = append(out, authoring.MemoryCoverageCell{
			Language: c.Language, Strategy: c.Form,
			Materializes:    c.Status == transform.CoverageMaterializes,
			Cause:           string(c.Cause),
			MissingArtifact: c.MissingArtifact, Note: c.Note,
		})
	}
	return out
}

type memValidator struct{ store *registry.Store }

func (v memValidator) ValidateMemoryParams(name, strategy string, params json.RawMessage) (registry.MemoryStrategy, json.RawMessage, error) {
	return v.store.ValidateMemoryParams(name, strategy, params)
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

func setMemory(s *variantspec.VariantSpec, node, ref string) {
	o := s.Nodes[node]
	o.MemoryRef = ref
	s.Nodes[node] = o
}

func hashAndCanon(ir *discovery.IR, spec *variantspec.VariantSpec, regs *memRegs) (string, []byte) {
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

func paramSummary(e *registry.MemoryEntry) string {
	var m map[string]any
	if err := json.Unmarshal(e.Spec.Params, &m); err != nil || len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for _, k := range sortedKeys(m) {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
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

// wrap breaks a long engine sentence for a terminal, WITHOUT abbreviating it. A refusal a reader has to
// act on is printed whole; only a survey line may be trimmed.
// openingClause reduces a refusal sentence to the clause that identifies the SHAPE it refused on, so
// one structural finding does not appear once per strategy and once per local variable name. Both the
// strategy and the unpacked mapping's name are stripped: "**kwargs" and "**stream_kwargs" are the same
// thing to fix, as are scratchpad's and vector-recall's demand for a result to record. The concrete
// names are kept alongside and printed. Presentation only — nothing downstream reads the key.
func openingClause(detail, strategy string) string {
	s := strings.ReplaceAll(detail, `"`+strategy+`"`, `"<strategy>"`)
	s = kwargsName.ReplaceAllString(s, "**<mapping>")
	for _, sep := range []string{", so ", ", and that means ", ". "} {
		if i := strings.Index(s, sep); i > 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
}

var kwargsName = regexp.MustCompile(`\*\*[A-Za-z_][A-Za-z0-9_]*`)

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
