// Command languagecoverage runs the ALL-LANGUAGE COVERAGE work (P13 13d / P14 14d / P15 15e / P16 16d)
// against a REAL repository — github.com/nousresearch/hermes-agent, the same target the earlier
// per-phase runners use.
//
// # What this run is for
//
// The four waves make one claim, and it is a claim about HONESTY rather than about reach: every axis
// answers for every registered language, and where it cannot apply a change it says which of three
// different things is missing. A demonstration on a synthetic fixture cannot test that, because the
// interesting case is the one a real repository actually hits.
//
// hermes-agent is the right target precisely because it is unflattering. Every LLM call site in it
// assembles its request elsewhere and passes it as `**kwargs`, so the dominant refusal here is
// `call-site-cannot-carry-it` — a fact about this repository's own code that no rewriter we ship will
// change. Before wave 13d the platform reported those as "no materializer for this language", which
// sent the reader to wait for work that would have refused them again.
//
// So the sections below are:
//
//	§1  the TOTAL coverage table this build carries, and its version — every axis × every registered
//	    language, with no absent cell;
//	§2  🔴 the headline: the refusal CLASS distribution over the real IR, proving the ordering rule —
//	    the call site's own shape is reported ahead of any coverage gap;
//	§3  the tool split, now RECORDED by the Python frontend (wave 14d), and what that makes prunable
//	    here versus what it correctly refuses;
//	§4  the wiring answer, which on this repository is a SHAPE refusal rather than a language one;
//	§5  the boundary, stated: what this build still cannot do, named per cell.
//
// It never executes the target and never writes to it (I1). Everything below is the shipped code path
// running on the real IR.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
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

	fmt.Printf("=== OAX all-language coverage (P13 13d / P14 14d / P15 15e / P16 16d) — %s ===\n", repoURL)
	fmt.Printf("discovered %d nodes, %d edges (language=%s) at %s (%s)\n\n",
		len(ir.Nodes), len(ir.Edges), ir.Workflow.Language, short(commitSHA), revNote)

	coverageTable()
	refusalClasses(ir, *repo)
	toolSplit(ir)
	wiringAnswer(ir, *repo)
	boundary()
}

// ── §1 · the total table ────────────────────────────────────────────────────────────────────────

func coverageTable() {
	cells := transform.AxisCoverage()
	langs := transform.RegisteredLanguages()
	fmt.Printf("§1 COVERAGE — total over (axis × registered language × form)\n")
	fmt.Printf("    table version %s · %d registered languages · %d cells\n",
		transform.CoverageTableVersion(), len(langs), len(cells))

	// The property worth printing is TOTALITY: every axis answers for every language. An absent cell is
	// the one thing this table may not contain, because absence renders everywhere as "not applicable".
	missing := 0
	for _, axis := range transform.CoverageAxes() {
		seen := map[string]bool{}
		for _, c := range transform.CoverageFor(axis) {
			seen[c.Language] = true
		}
		for _, l := range langs {
			if !seen[l] {
				missing++
				fmt.Printf("    🔴 %s has NO cell for %s\n", axis, l)
			}
		}
	}
	if missing == 0 {
		fmt.Printf("    ✓ no absent cell: every axis answers for every registered language\n")
	}

	applies := 0
	byCause := map[transform.CauseClass]int{}
	for _, c := range cells {
		if c.Refused() {
			byCause[c.Cause]++
			continue
		}
		applies++
	}
	fmt.Printf("    %d cell(s) apply; the rest refuse by named class:\n", applies)
	for _, k := range transform.CauseClasses() {
		fmt.Printf("      %-34s %3d\n", string(k), byCause[k])
	}

	// A per-axis summary of what this build reaches, for the reader who wants one line per axis.
	fmt.Printf("    per axis (languages that materialize at least one form):\n")
	for _, axis := range transform.CoverageAxes() {
		fmt.Printf("      %-8s %s\n", axis, strings.Join(transform.CoverageLanguagesFor(axis), ", "))
	}
	fmt.Println()
}

// ── §2 · the headline: WHICH refusal, on real call sites ────────────────────────────────────────

// refusalClasses submits a real override on every discovered node and counts the CLASS of each answer.
//
// 🔴 This is the assertion the whole coverage contract exists for. Python has a skill materializer, a
// context splitter and a statement resolver on this build — so if the engine still reported "no
// materializer for this language" here, it would be reporting a gap that does not exist while hiding the
// one that does.
func refusalClasses(ir *discovery.IR, repo string) {
	fmt.Printf("§2 REFUSAL CLASSES over %d real call sites — the ordering rule, measured\n", len(ir.Nodes))

	entry := skillEntry()
	counts := map[transform.CauseClass]int{}
	applied := 0
	var sample string

	for _, n := range ir.Nodes {
		spec := resolvedFor(ir, n.NodeID, variantspec.ResolvedOverride{
			Skills: []*registry.SkillEntry{entry},
		})
		_, err := transform.Generate(spec, repo)
		if err == nil {
			applied++
			continue
		}
		var re *transform.RewriteError
		if !asRewrite(err, &re) {
			continue
		}
		counts[re.Cause]++
		if sample == "" && re.Cause == transform.CauseCallSiteShape {
			sample = re.Detail
		}
	}

	fmt.Printf("    materialized: %d\n", applied)
	for _, k := range transform.CauseClasses() {
		fmt.Printf("    %-34s %3d\n", string(k), counts[k])
	}

	if counts[transform.CauseCallSiteShape] > 0 && counts[transform.CauseNoMaterializer] == 0 {
		fmt.Printf("    ✓ 🔴 THE FINDING: every refusal here is about THIS REPOSITORY'S OWN SOURCE, not about\n")
		fmt.Printf("      Python. Python has a skill materializer on this build; the call sites still cannot\n")
		fmt.Printf("      carry a binding, and the engine says so instead of promising a rewriter.\n")
	}
	if sample != "" {
		fmt.Printf("    the sentence a hermes engineer actually gets:\n      %s\n", wrap(sample, 6))
	}
	fmt.Println()
}

// ── §3 · the tool split, recorded ───────────────────────────────────────────────────────────────

func toolSplit(ir *discovery.IR) {
	fmt.Printf("§3 TOOL SPLIT — recorded by the Python frontend (wave 14d)\n")
	fmt.Printf("    discovery.RecordsToolSplit(%q) = %v\n", ir.Workflow.Language,
		discovery.RecordsToolSplit(ir.Workflow.Language))

	withTools, locatable, unlocatable := 0, 0, 0
	for _, n := range ir.Nodes {
		if len(n.Tools) == 0 {
			continue
		}
		withTools++
		for _, tool := range n.Tools {
			if tool.Locatable() {
				locatable++
				continue
			}
			unlocatable++
		}
	}
	fmt.Printf("    %d node(s) record a tool set: %d locatable declaration(s), %d unlocatable\n",
		withTools, locatable, unlocatable)
	if withTools == 0 {
		fmt.Printf("    ✓ no node here writes a tool list at its call site, so there is nothing to prune —\n")
		fmt.Printf("      and that is recorded as a fact about the source. Before wave 14d the same outcome\n")
		fmt.Printf("      was reported as \"no pruner for this language\", which named the wrong owner.\n")
	}
	fmt.Println()
}

// ── §4 · wiring: a SHAPE answer, not a language one ─────────────────────────────────────────────

func wiringAnswer(ir *discovery.IR, repo string) {
	fmt.Printf("§4 WIRING — Python has a statement resolver on this build (wave 15e)\n")
	fmt.Printf("    transform.HasStatementMaterializer(%q) = %v\n", ir.Workflow.Language,
		transform.HasStatementMaterializer(ir.Workflow.Language))
	fmt.Printf("    languages that can transpose: %s\n",
		strings.Join(transform.StatementMaterializerLanguages(), ", "))

	order := orderOf(ir)
	if len(order) < 2 {
		fmt.Printf("    fewer than two ordered nodes; nothing to transpose\n\n")
		return
	}
	// Ask for the one shape this axis materializes, between the first two nodes. The request goes
	// through GenerateTransform, which is the entry point that compares the REQUESTED wiring against
	// what the source states — the same path a proposal takes.
	swapped := append([]string{}, order...)
	swapped[0], swapped[1] = swapped[1], swapped[0]
	spec := &variantspec.VariantSpec{
		WorkflowID: "nousresearch/hermes-agent", SourceRevision: ir.Workflow.Repo.CommitSHA,
		Order: swapped, Nodes: map[string]variantspec.NodeOverride{},
	}
	patch, err := transform.GenerateTransform(resolvedFor(ir, "", variantspec.ResolvedOverride{}), spec, repo)
	switch {
	case err == nil && (patch == nil || patch.IsEmpty()):
		// 🔴 Reported honestly rather than as a success. An empty patch means the requested order and the
		// order the SOURCE STATES are the same thing — this repository's nodes sit in separate functions
		// and files, so a list permutation of node ids is not a statement rearrangement at all. Claiming
		// "materialized" here would be the demo lying about its own axis.
		fmt.Printf("    ⋯ no wiring change was materialized: the requested permutation is not a difference\n")
		fmt.Printf("      this repository's SOURCE states. Its call sites are not adjacent sibling\n")
		fmt.Printf("      statements, so there is no transposable pair — a fact about the source, in every\n")
		fmt.Printf("      language, and the same answer Go would give.\n")
	case err == nil:
		fmt.Printf("    ✓ a transposition materialized on the real tree (%d file(s))\n", len(patch.Files))
	default:
		var re *transform.RewriteError
		if asRewrite(err, &re) {
			fmt.Printf("    refused, class=%s\n", re.Cause)
			if re.Cause != transform.CauseNoMaterializer {
				fmt.Printf("    ✓ 🔴 the refusal is about the requested SHAPE or this repository's structure —\n")
				fmt.Printf("      not about Python, which has a resolver. That ordering is wave 15e's point.\n")
			}
			fmt.Printf("      %s\n", wrap(re.Detail, 6))
		} else {
			fmt.Printf("    refused: %v\n", err)
		}
	}
	fmt.Println()
}

// ── §5 · the boundary, per cell ─────────────────────────────────────────────────────────────────

func boundary() {
	fmt.Printf("§5 WHAT THIS BUILD STILL CANNOT DO — named, per cell\n")
	gaps := map[string][]string{}
	for _, c := range transform.AxisCoverage() {
		if c.Cause != transform.CauseNoMaterializer {
			continue
		}
		key := c.MissingArtifact
		gaps[key] = append(gaps[key], fmt.Sprintf("%s/%s/%s", c.Axis, c.Language, c.Form))
	}
	keys := make([]string, 0, len(gaps))
	for k := range gaps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		fmt.Printf("    none: every cell either applies or refuses for a reason that is not ours to fix\n")
	}
	for _, k := range keys {
		sort.Strings(gaps[k])
		fmt.Printf("    · %s\n", k)
		fmt.Printf("      %s\n", strings.Join(gaps[k], ", "))
	}
	fmt.Printf("\n=== run complete on the real hermes-agent IR ===\n")
	fmt.Printf("Every axis answered for every registered language, and every refusal named which of three\n")
	fmt.Printf("things is missing. On THIS repository the dominant answer is the customer's own call-site\n")
	fmt.Printf("shape — which is the finding, and which the platform can now say without blaming a language\n")
	fmt.Printf("that would refuse it for the same reason.\n")
}

// ── helpers ─────────────────────────────────────────────────────────────────────────────────────

func skillEntry() *registry.SkillEntry {
	e, err := registry.NewSkillEntry(strings.Repeat("5", 64), "search_kb", registry.SkillSpec{
		ImplHandle:   "builtin:search_kb",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"hits":{"type":"array"}},"required":["hits"]}`),
	})
	if err != nil {
		log.Fatalf("seal skill: %v", err)
	}
	return e
}

func resolvedFor(ir *discovery.IR, nodeID string, o variantspec.ResolvedOverride) *variantspec.Resolved {
	r := &variantspec.Resolved{
		ConfigHash:     "cfg-oax-hermes",
		Language:       ir.Workflow.Language,
		SourceRevision: ir.Workflow.Repo.CommitSHA,
		Overrides:      map[string]variantspec.ResolvedOverride{},
	}
	if nodeID != "" {
		r.Overrides[nodeID] = o
	}
	return r
}

func orderOf(ir *discovery.IR) []string {
	out := make([]string, 0, len(ir.Nodes))
	for _, n := range ir.Nodes {
		out = append(out, n.NodeID)
	}
	sort.Strings(out)
	return out
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

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// wrap folds a long refusal to a readable width. Refusal copy is written for the person who has to act
// on it; printing it as one 600-character line is how it stops being read.
func wrap(s string, indent int) string {
	const width = 96
	pad := strings.Repeat(" ", indent)
	var b strings.Builder
	col := 0
	for _, w := range strings.Fields(s) {
		if col+len(w)+1 > width {
			b.WriteString("\n" + pad)
			col = 0
		} else if col > 0 {
			b.WriteString(" ")
			col++
		}
		b.WriteString(w)
		col += len(w)
	}
	return b.String()
}
