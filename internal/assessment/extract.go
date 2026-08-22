package assessment

import (
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
)

// extract.go is the structural pass (task 2.1): one extractor per axis, over the IR and the discovery
// report, each returning `observed` or `not_measured` with a NAMED missing input.
//
// # 🔴 No extractor returns a default
//
// That sentence is the whole task and it is easy to satisfy in letter and break in spirit. The two
// ways it breaks here, both live in this file:
//
//  1. **A zero read as a measurement.** `len(ir.Edges) == 0` is "no topology" only if the frontend that
//     produced this IR can emit topology at all. From a syntactic frontend it means nobody looked, and
//     reporting it as a flat graph states a property of the TOOL as a property of the SUBJECT (D6).
//  2. **A FLOOR read as an observation.** `discovery` emits `memory: none` and `harness: single-shot`
//     for every node and documents both as *"a statement about the EVIDENCE, not a placeholder ... the
//     honest floor"*. An extractor that reported those as findings would be the same inversion as (1),
//     arriving on the two axes a customer most wants an answer about. Both report `not_measured` with
//     `not_visible_in_static_ir`, and both are the residue inference exists to resolve.
//
// # Why nine functions and not one switch
//
// Each axis reads a different part of the IR and fails for a different reason. A single function with
// a nine-armed switch would share a fallthrough, and a shared fallthrough is where a default comes
// from. Nine functions have nine explicit endings.

// Subject is everything the structural pass is allowed to see.
//
// 🔴 There is no field for the source TEXT. §7.4: prompt text and source are inputs to a computation
// on the platform's side of the boundary and are not stored by this phase, and an extractor with
// nowhere to put a source line cannot put one in a claim.
type Subject struct {
	WorkflowID string
	// IR is discovery's output at the revision under assessment. nil is a real state — no snapshot —
	// and every extractor reports `no_source_snapshot` for it rather than treating it as empty.
	IR *discovery.IR
	// Report is discovery's run report. It carries the FRONTENDS, which is the only thing that can
	// answer "why does this graph have no edges" (D6).
	Report discovery.DiscoveryReport
}

// Evidence returns the reference every structural finding points at: the workflow's own graph.
//
// One helper rather than nine literals, because an evidence reference that is *nearly* right — a
// locator with a stray prefix, say — fails at the write boundary (task 2.4) with an error naming the
// axis, and debugging nine copies of one mistake is nine times the work.
func (s Subject) Evidence() EvidenceRef {
	return EvidenceRef{Surface: SurfaceGraph, Locator: s.WorkflowID}
}

// Extractor is one axis's structural pass.
type Extractor interface {
	Axis() Axis
	Extract(Subject) (Finding, error)
}

type extractorFunc struct {
	axis Axis
	fn   func(Subject) (Finding, error)
}

func (e extractorFunc) Axis() Axis                         { return e.axis }
func (e extractorFunc) Extract(s Subject) (Finding, error) { return e.fn(s) }

// Extractors returns one extractor per axis, in report order.
//
// 🔴 `TestThereIsOneExtractorPerAxis` asserts the set equality. An axis added to `Axes()` without an
// extractor would otherwise produce a report that is nine findings short of nine — and the assembler
// would have to invent something for it, which is where a default comes from.
func Extractors() []Extractor {
	return []Extractor{
		extractorFunc{AxisModel, extractModel},
		extractorFunc{AxisPrompt, extractPrompt},
		extractorFunc{AxisSkills, extractSkills},
		extractorFunc{AxisContext, extractContext},
		extractorFunc{AxisTools, extractTools},
		extractorFunc{AxisMemory, extractMemory},
		extractorFunc{AxisHarness, extractHarness},
		extractorFunc{AxisLoop, extractLoop},
		extractorFunc{AxisGraph, extractGraph},
	}
}

// precondition returns the finding every axis shares when there is nothing to read at all, or nil.
//
// Two states, and they are DIFFERENT: no snapshot means we never saw the code; no call sites means we
// saw it and it contains no LLM calls we recognise. A reader does different things about each — supply
// source, or tell us what framework they use — and collapsing them into one message tells them to do
// neither.
func precondition(axis Axis, s Subject) *Finding {
	switch {
	case s.IR == nil:
		f := must(NotMeasured(axis, MissingSourceRevision,
			"no source snapshot is held for the revision under assessment, so nothing was read", s.Evidence()))
		return &f
	case len(s.IR.Nodes) == 0 && unsupportedLanguage(s.Report) != "":
		// 🔴 REFUSED, not `not_measured`, and the distinction is D1's: "we could not" and "this build
		// cannot" are different sentences with different owners. A repository written in a language no
		// frontend in this build handles is not a repository we failed to read — it is one we cannot
		// read, and only we can change that. Telling the customer to check their code would send them
		// to look at something that is fine.
		f := must(Refused(axis, RefusalLanguage, fmt.Sprintf(
			"this build has no frontend for %s, so nothing was read from this repository at all. That is "+
				"a gap on our side; the language support is what would close it", unsupportedLanguage(s.Report)),
			s.Evidence()))
		return &f
	case len(s.IR.Nodes) == 0:
		f := must(NotMeasured(axis, MissingNoNodes,
			"discovery found no LLM call sites in this repository, so this surface has no subject yet — "+
				"if the repository does call a model, the framework it uses is not one this build recognises",
			s.Evidence()))
		return &f
	}
	return nil
}

// unsupportedLanguage names a language present in the tree that no frontend in this build handles, or
// "" when there is none.
//
// 🔴 Read from discovery's own `LANGUAGE_UNSUPPORTED` diagnostic rather than from a list of languages
// held here. A list here would be a second copy of the frontend registry, and the copy is what goes
// stale the day a frontend is added — reporting "we have no frontend for Kotlin" on the release that
// shipped one.
func unsupportedLanguage(rep discovery.DiscoveryReport) string {
	langs := []string{}
	seen := map[string]bool{}
	for _, d := range rep.FileDiagnostics {
		// 🚫 The LANGUAGE comes from `Symbol`, never from the message. A regex over a human sentence is
		// a contract nobody declared: it breaks the day somebody improves the wording, silently, and
		// the symptom is a refusal that names no language — which is the one thing the refusal is for.
		if d.Code != discovery.CodeLanguageUnsupported || d.Symbol == "" || seen[d.Symbol] {
			continue
		}
		seen[d.Symbol] = true
		langs = append(langs, d.Symbol)
	}
	sort.Strings(langs)
	return phraseOrEmpty(langs)
}

func phraseOrEmpty(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return phrase(items)
}

// must panics on a constructor error. It is used ONLY for findings whose arguments are literals in
// this file, where a failure is a programming error in a line above it rather than a runtime
// condition — the same reason `regexp.MustCompile` exists. Every extractor's PUBLIC path returns an
// error, so a caller never receives a panic from a value it supplied.
func must(f Finding, err error) Finding {
	if err != nil {
		panic("assessment: a literal finding in extract.go is invalid: " + err.Error())
	}
	return f
}

// ── model ────────────────────────────────────────────────────────────────────────────────────────

func extractModel(s Subject) (Finding, error) {
	if f := precondition(AxisModel, s); f != nil {
		return *f, nil
	}
	resolved := map[string]int{}
	unresolved := []string{}
	for _, n := range s.IR.Nodes {
		if n.Model.ModelID == discovery.UnresolvedSentinel || n.Model.Provider == discovery.UnresolvedSentinel {
			unresolved = append(unresolved, where(n))
			continue
		}
		resolved[n.Model.Provider+"/"+n.Model.ModelID]++
	}
	// 🔴 ANY unresolved node makes the axis not_measured, rather than reporting the resolved majority.
	// "Three of your four call sites use gpt-4o-mini" invites the reader to conclude something about
	// the fourth, and the fourth is the one we could not read.
	if len(unresolved) > 0 {
		return NotMeasured(AxisModel, MissingUnresolvedField, fmt.Sprintf(
			"%d of %d call sites name a model discovery could not resolve to a literal (%s). "+
				"The model is chosen at runtime from a value the frontend cannot follow across a statement",
			len(unresolved), len(s.IR.Nodes), sample(unresolved)), s.Evidence())
	}
	return Observed(AxisModel, fmt.Sprintf(
		"%d call sites use %s", len(s.IR.Nodes), phrase(keysOf(resolved))), s.Evidence())
}

// ── prompt ───────────────────────────────────────────────────────────────────────────────────────

func extractPrompt(s Subject) (Finding, error) {
	if f := precondition(AxisPrompt, s); f != nil {
		return *f, nil
	}
	unresolved := []string{}
	withVars, inline := 0, 0
	for _, n := range s.IR.Nodes {
		if n.Prompt.Inline == discovery.UnresolvedSentinel {
			unresolved = append(unresolved, where(n))
			continue
		}
		inline++
		if len(n.Prompt.Variables) > 0 {
			withVars++
		}
	}
	if len(unresolved) > 0 {
		return NotMeasured(AxisPrompt, MissingUnresolvedField, fmt.Sprintf(
			"%d of %d call sites build their prompt from a value discovery could not resolve (%s)",
			len(unresolved), len(s.IR.Nodes), sample(unresolved)), s.Evidence())
	}
	return Observed(AxisPrompt, fmt.Sprintf(
		"all %d prompts are written at the call site; %d interpolate declared variables", inline, withVars),
		s.Evidence())
}

// ── skills ───────────────────────────────────────────────────────────────────────────────────────

func extractSkills(s Subject) (Finding, error) {
	if f := precondition(AxisSkills, s); f != nil {
		return *f, nil
	}
	bound := map[string]int{}
	for _, n := range s.IR.Nodes {
		for _, sk := range n.Skills {
			bound[sk]++
		}
	}
	if len(bound) == 0 {
		// 🔴 An ABSENCE, reported as an observation (PRD §14 A3). A total scan of every node found no
		// binding, which is checkable — the reader can grep. The OPINION that they should bind one is
		// a proposal and belongs to P35.
		return Observed(AxisSkills,
			"no platform skills are bound at any of the discovered call sites", s.Evidence())
	}
	return Observed(AxisSkills, fmt.Sprintf(
		"%d skills are bound across the discovered call sites: %s", len(bound), phrase(keysOf(bound))),
		s.Evidence())
}

// ── context ──────────────────────────────────────────────────────────────────────────────────────

func extractContext(s Subject) (Finding, error) {
	if f := precondition(AxisContext, s); f != nil {
		return *f, nil
	}
	policies := map[string]int{}
	unresolved := []string{}
	for _, n := range s.IR.Nodes {
		p := strings.TrimSpace(n.ContextAssembly.Policy)
		if p == "" || p == discovery.UnresolvedSentinel {
			unresolved = append(unresolved, where(n))
			continue
		}
		policies[p]++
	}
	if len(unresolved) > 0 {
		return NotMeasured(AxisContext, MissingUnresolvedField, fmt.Sprintf(
			"%d of %d call sites assemble their message list from a value discovery could not resolve (%s)",
			len(unresolved), len(s.IR.Nodes), sample(unresolved)), s.Evidence())
	}
	return Observed(AxisContext, fmt.Sprintf(
		"context assembly is %s across %d call sites", phrase(keysOf(policies)), len(s.IR.Nodes)), s.Evidence())
}

// ── tools ────────────────────────────────────────────────────────────────────────────────────────

func extractTools(s Subject) (Finding, error) {
	if f := precondition(AxisTools, s); f != nil {
		return *f, nil
	}
	named := map[string]int{}
	runtimeAssembled := []string{}
	for _, n := range s.IR.Nodes {
		for _, t := range n.Tools {
			named[t.Name]++
			// 🔴 `DeclaredAt == nil` is discovery's own record that the tool set is ASSEMBLED AT
			// RUNTIME — its comment says so explicitly and calls the nil "load-bearing and not
			// unknown". Listing the names we happened to see from a runtime-assembled list as though
			// they were the list is a default in the shape of a fact.
			if !t.Locatable() {
				runtimeAssembled = append(runtimeAssembled, where(n))
			}
		}
	}
	if len(runtimeAssembled) > 0 {
		return NotMeasured(AxisTools, MissingUnresolvedField, fmt.Sprintf(
			"%s assembles its tool list at runtime, so what the model is offered is not visible in the "+
				"source; the names discovery saw are a sample of that list, not the list",
			sample(dedupe(runtimeAssembled))), s.Evidence())
	}
	if len(named) == 0 {
		return Observed(AxisTools,
			"no provider-native tools are offered at any of the discovered call sites", s.Evidence())
	}
	return Observed(AxisTools, fmt.Sprintf(
		"%d tools are offered across the discovered call sites: %s", len(named), phrase(keysOf(named))),
		s.Evidence())
}

// ── memory ───────────────────────────────────────────────────────────────────────────────────────

// extractMemory ALWAYS reports `not_measured`, and that is the correct structural answer today.
//
// 🔴 Read `discovery/emit.go`'s own comment on `IRNode.Memory` before changing this: *"Discovery emits
// `none` for every node today, and that is a statement about the EVIDENCE, not a placeholder. A memory
// strategy is a store read and written BETWEEN turns ... so it is not visible in the single call site
// P1 extracts."*
//
// So `n.MemoryDefault() == "none"` on every node in every repository. Reporting that as *"this
// repository has no memory strategy"* would be a sentence about our parser wearing the customer's
// name. This is the single place in the structural pass where the honest answer is the same for every
// input, and it stays that way until a frontend can see between turns.
func extractMemory(s Subject) (Finding, error) {
	if f := precondition(AxisMemory, s); f != nil {
		return *f, nil
	}
	// Guard rather than assume: if a frontend ever DOES emit a non-floor value, report it, and the
	// claim below stops being reachable for that repository.
	strategies := map[string]int{}
	for _, n := range s.IR.Nodes {
		if v := n.MemoryDefault(); v != "none" {
			strategies[v]++
		}
	}
	if len(strategies) > 0 {
		return Observed(AxisMemory, fmt.Sprintf(
			"the discovered call sites carry memory strategies: %s", phrase(keysOf(strategies))), s.Evidence())
	}
	return NotMeasured(AxisMemory, MissingNotVisibleStatically,
		"a memory strategy is a store read and written between turns, and static call-site extraction "+
			"sees one call at a time; nothing here says this repository has no memory — only that we "+
			"have not looked between its turns yet", s.Evidence())
}

// ── harness ──────────────────────────────────────────────────────────────────────────────────────

// extractHarness reports the EXECUTION ENVELOPE — what a node is allowed to do and inside what walls
// (P34 FR5). Since the axis split it no longer reports the control loop; that moved next door to
// `extractLoop`, which is where the `HarnessDefault()` read this function used to do now lives.
//
// 🔴 The envelope is `not_measured` in every repository, and that is a permanent property rather than a
// gap someone will close. Sandbox posture, spend ceilings, timeouts, guardrail bindings and approval
// gates are facts about how a node is DEPLOYED — they are not written at a call site in any language,
// so there is nothing in a source snapshot for a static reader to find. A finding here that said
// anything else would be inventing a policy the repository never states.
//
// 🚫 It is `not_measured`, not `refused`. "We could not" and "this build cannot" have different owners
// (D1), and this is neither: the subject genuinely is not in the source. `MissingNotVisibleStatically`
// is the cause that says so.
func extractHarness(s Subject) (Finding, error) {
	if f := precondition(AxisHarness, s); f != nil {
		return *f, nil
	}
	return NotMeasured(AxisHarness, MissingNotVisibleStatically,
		"the execution envelope — where this node may reach on the network, what it may spend, how long "+
			"it may run, which guardrail and approval gate it answers to — is a property of how the node "+
			"is DEPLOYED, not of the call your source writes. A snapshot of the code cannot contain it, so "+
			"this is a limit of static reading rather than a gap in your repository or in our frontend",
		s.Evidence())
}

// ── loop ─────────────────────────────────────────────────────────────────────────────────────────

// extractLoop reports the ITERATION POLICY: which control loop a node runs, and what stops it.
//
// 🔴 This is the read `extractHarness` used to do, moved rather than duplicated. P34 split one axis into
// two and the DISCOVERED fact — `HarnessDefault()`, the scaffold a call site already implements — is an
// iteration policy, so it belongs here. Leaving a copy behind would have given the report two rows
// making the same claim, which is worse than either row alone.
//
// It has memory's shape and memory's reason. `discovery` emits `single-shot` for every node, and its
// comment names the trap precisely: `InvocationSemantics` records `loop` when a call sits inside one,
// and *"a `for` loop over a list of tickets fires the node many times with no scaffold at all, while an
// agent loop is the MODEL deciding to take another turn; the two are indistinguishable from loop
// depth."*
//
// 🚫 Do not read `InvocationSemantics.Type` here. It is the tempting signal and it is the wrong one.
func extractLoop(s Subject) (Finding, error) {
	if f := precondition(AxisLoop, s); f != nil {
		return *f, nil
	}
	strategies := map[string]int{}
	for _, n := range s.IR.Nodes {
		if v := n.HarnessDefault(); v != registry.StrategySingleShot {
			strategies[v]++
		}
	}
	if len(strategies) > 0 {
		return Observed(AxisLoop, fmt.Sprintf(
			"the discovered call sites run inside: %s", phrase(keysOf(strategies))), s.Evidence())
	}
	return NotMeasured(AxisLoop, MissingNotVisibleStatically,
		"the control loop around a call — how many turns it runs and under what stop condition — is a "+
			"property of the loop that drives it, not of the call; a source loop around a call site is "+
			"not evidence of an agent loop, so this build declines to read one as the other", s.Evidence())
}

// ── graph ────────────────────────────────────────────────────────────────────────────────────────

// extractGraph applies the P34 gate and then defers to the real topology extractor.
//
// 🔴 TWO FUNCTIONS, and the split is task 9.2's "stated rather than discovered" made structural.
//
// PRD §3 puts `graph` behind P34: *"P33 may report on them only once P34 has landed, or it names axes
// the configuration layer does not have."* So the shipped report refuses it. But the topology
// extraction below is COMPLETE, correct, and the subject of tasks 2.2 and 7.2 — and folding the gate
// into it would make those tests unreachable, which is how a gate comes to hide a broken extractor
// for two quarters and then lifts onto one nobody has run.
//
// `TestTheGatedGraphExtractorStillWorks` runs the inner one directly, so the day `P34Pending` returns
// false the analysis behind it has been exercised the whole time.
// 🔴 The gate this function applied is now open. P33 wrote it as TWO functions on purpose — "folding the
// gate into it would make those tests unreachable, which is how a gate comes to hide a broken extractor
// for two quarters and then lifts onto one nobody has run" — and `TestTheGatedGraphExtractorStillWorks`
// exercised the inner one the whole time, so the day it opened the analysis behind it was already run.
// The wrapper is kept rather than inlined so that the seam, and the reason for it, survive the lifting.
func extractGraph(s Subject) (Finding, error) {
	if AxisGraph.P34Pending() {
		return Refused(AxisGraph, RefusalAnalysis,
			"this build does not report topology as an assessment axis yet. The analysis exists — we read "+
				"your graph and render it — but `graph` becomes a surface you can CONFIGURE with P34, and "+
				"reporting on it before then would name an axis the configuration layer does not have",
			s.Evidence())
	}
	return extractGraphTopology(s)
}

// extractGraphTopology is task 2.2 and design D6, and it is the extractor most likely to be quietly wrong.
//
// 🔴 The rule: zero edges is a claim about the REPOSITORY only when every frontend that contributed to
// this IR can emit edges at all. `discovery.AnalysisSyntactic`'s own doc says a syntactic frontend
// *"emits NODES AND NO EDGES"* and that zero edges from one *"says nothing at all about the code"*.
//
// The frontends are read from the discovery report rather than from a list here, because that report
// records only CONTRIBUTING frontends — naming the Rust frontend on a Python repository would be a true
// sentence about the wrong thing, which reads as a false one.
func extractGraphTopology(s Subject) (Finding, error) {
	if f := precondition(AxisGraph, s); f != nil {
		return *f, nil
	}
	if len(s.IR.Edges) > 0 {
		return Observed(AxisGraph, fmt.Sprintf(
			"%d nodes connected by %d edges", len(s.IR.Nodes), len(s.IR.Edges)), s.Evidence())
	}

	var blind []discovery.FrontendRun
	for _, fr := range s.Report.Frontends {
		if fr.AnalysisKind == discovery.AnalysisSyntactic {
			blind = append(blind, fr)
		}
	}
	if len(blind) > 0 {
		names := make([]string, 0, len(blind))
		for _, fr := range blind {
			names = append(names, fr.Language)
		}
		sort.Strings(names)
		return NotMeasured(AxisGraph, MissingFrontendEdges, fmt.Sprintf(
			"no topology was read, and that is a limit of our %s frontend rather than a fact about this "+
				"repository: it is a syntactic analyser, so it enumerates call sites and emits no edges "+
				"at all. Your calls may well be connected — we cannot yet see it",
			phrase(names)), s.Evidence())
	}
	if len(s.Report.Frontends) == 0 {
		// No contributing frontend is recorded, so nothing can be said about why the edge list is
		// empty. Reporting "the calls are independent" from here would be asserting a property of the
		// code from the absence of a record about the tool.
		return NotMeasured(AxisGraph, MissingFrontendEdges,
			"no topology was read and the discovery run recorded no contributing frontend, so we cannot "+
				"say whether the calls are independent or whether nothing looked", s.Evidence())
	}
	// Every contributing frontend is typed. `AnalysisTyped`'s own doc: *"Zero edges from a typed
	// frontend is a fact about the code."* This is the one branch where an empty edge list is a
	// finding about the repository, and it is reachable only after the two above have been ruled out.
	return Observed(AxisGraph, fmt.Sprintf(
		"%d call sites with no data or control flow between them: each is invoked independently", len(s.IR.Nodes)),
		s.Evidence())
}

// ── small helpers ────────────────────────────────────────────────────────────────────────────────

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// phrase joins a list the way a sentence does. Sorted input in, deterministic sentence out — FR15's
// identical findings has to survive a map iteration.
func phrase(items []string) string {
	switch len(items) {
	case 0:
		return "nothing"
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}

// sample names at most three places and then counts the rest. A claim listing forty is a claim nobody
// reads; one naming three and counting thirty-seven is a claim and a next step.
//
// 🔴 It takes LOCATIONS, not node ids, and the difference was found by running this against a real
// repository. On `nousresearch/hermes-agent` the model axis reported *"28 of 28 call sites name a model
// discovery could not resolve (n_0017914833d3d240, n_008b903482b33087 and n_0110783593788f45 and 25
// more)"* — every word true, and the three things it named were opaque hashes. A reader cannot open
// `n_0017914833d3d240`. They can open `chat/inference.py:212`.
//
// The node id is not lost: it is what the graph evidence link addresses. What changes is the SENTENCE,
// which is the part somebody reads without following a link.
func sample(locations []string) string {
	sorted := dedupe(locations)
	if len(sorted) <= 3 {
		return phrase(sorted)
	}
	return fmt.Sprintf("%s and %d more", phrase(sorted[:3]), len(sorted)-3)
}

// where renders a node as the place a reader would open it: `path/to/file.py:212`.
//
// Falls back to the node id when the IR carries no call site — which is a real state for a node a
// framework reader contributed, and naming the id is better than naming nothing.
func where(n discovery.IRNode) string {
	if n.CallSite.File == "" {
		return n.NodeID
	}
	if n.CallSite.LineStart > 0 {
		return fmt.Sprintf("%s:%d", n.CallSite.File, n.CallSite.LineStart)
	}
	return n.CallSite.File
}
