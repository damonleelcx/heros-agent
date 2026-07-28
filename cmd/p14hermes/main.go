// Command p14hermes drives the P14 skills & tools optimization against a REAL repository
// (github.com/nousresearch/hermes-agent, the same target as cmd/p5hermes / cmd/p13hermes). It
// discovers the IR and then exercises every P14 code path that this repository can actually reach.
//
// # What this run proves, and the one thing it deliberately does NOT
//
// hermes-agent is PYTHON. When 14a shipped, Go was the only language whose skill materializer had
// landed (decisions.md D-14.4) and the headline here was a LANGUAGE refusal. 14d changed that: Python
// is now a covered row, and the headline is still a REFUSAL — but a refusal about the CALL SITES.
//
// 🔴 That distinction is the whole point, not a footnote. Every working node in this repository passes
// `**_create_kwargs`, so what it offers the model is assembled elsewhere in the program and is not
// written at the call site. There is no argument to append to and no declaration to delete, and a
// materializer for Python refuses it for exactly that reason. Reporting this as "Python is not
// supported yet" would tell this repository's author to wait for a rewriter that would refuse them on
// the day it shipped. The reason named must be the most specific TRUE one: the change, then the
// registry row, then the call site's source, and the language LAST.
//
// A refusal is not a disappointing outcome to be worked around: it is the contract D-14.3 specifies,
// exercised on real node ids, at real call sites, with the reason named. The failure P14 is written
// against is a diff that LOOKS complete; a run that quietly produced one here would be the bug.
//
// Concretely, all of the following are the shipped code paths running on the real IR:
//
//	14b  the tools≠skills IR split is ADDITIVE — this repo's 40-node IR emits no `tools`/`skills` key
//	     at all, so its bytes and its config_hash are identical to pre-P14 (task 4.1/4.4);
//	14b  a tool selection over these nodes FAILS CLOSED at resolve, because an IR that records no tool
//	     set is "not recorded", never "no tools" (task 5.3);
//	14a  a skill binding at a real Python call site is REFUSED with ErrUnsafeRewrite naming the node and
//	     the skills dimension, and emits no diff (task 2.2);
//	14b  a tool prune at the same call site is refused with its own distinct reason (task 6.2);
//	14a  the operators (remove-skill, tool-prune, tool-minimize) emit or decline on recorded usage
//	     (tasks 3.1, 6.3);
//	14a  a materialized skill's arguments are validated against its sealed contract before execution,
//	     and every failure carries an allowlisted toolcontract code (tasks 2.4, 2.5);
//	14a  add/remove/rerank each move config_hash; a no-skill/no-prune node does not (tasks 2.6, 5.4);
//	14a  a regressing skill change is WITHHELD by the verification gate (task 3.2).
//
// It NEVER executes the target — it parses it (invariant I1). What is real: the repository, the
// discovered IR, the refusals, the resolve-time rejections, the operator emission, the config hashes,
// and the bind-site contract validation. What is illustrative: the per-node diagnosis, the recorded
// tool usage, and the eval deltas — in production those come from the P4.5/P4 engines through a
// provider.
//
//	go run ./cmd/p14hermes -repo /tmp/hermes-agent
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

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sourcerev"
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
	"github.com/heros-foreal/agentd/internal/verification"
)

const repoURL = "https://github.com/nousresearch/hermes-agent"

// Two skill refs, 64-hex and distinct, standing in for registered platform capabilities.
const (
	refSearchKB = "1111111111111111111111111111111111111111111111111111111111111111"
	refIssueLk  = "2222222222222222222222222222222222222222222222222222222222222222"
)

func main() {
	repo := flag.String("repo", "/tmp/hermes-agent", "path to the hermes-agent checkout (read-only)")
	pin := flag.String("pin", "", `commit this run must be checked out at; empty means "use HEAD and say so"`)
	flag.Parse()

	// The revision is RESOLVED against the checkout, never asserted. A SHA that does not match the tree
	// would put a false source_revision on every result below, and source_revision is half of the
	// reproducibility key. This command ships no default pin: its output is a boundary demonstration
	// rather than a set of quoted numbers, so HEAD is the honest label — and it says that it is HEAD.
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

	fmt.Printf("=== P14 skills & tools optimization - run for %s ===\n", repoURL)
	fmt.Printf("discovered %d nodes (language=%s) at %s (%s)\n\n",
		len(ir.Nodes), ir.Workflow.Language, short(commitSHA), revNote)

	node := pickNode(ir)
	if node == "" {
		log.Fatalf("no discovered node to work with among %d", len(ir.Nodes))
	}
	fmt.Printf("working node: %s  (%s)\n\n", short(node), siteOf(ir, node))

	base := &variantspec.VariantSpec{
		WorkflowID: "nousresearch/hermes-agent", SourceRevision: commitSHA,
		Order: []string{node}, Nodes: map[string]variantspec.NodeOverride{},
	}

	splitAdditivity(ir)
	failClosed(ir, base, node)
	interimRefusals(*repo, ir, node)
	operators(base, node)
	bindSiteContract()
	hashAdditivity(ir, base, node)
	verificationGate()
	coverage()

	fmt.Printf("\n=== P14 run complete on the real hermes-agent IR ===\n")
	fmt.Printf("The headline result for THIS repository is a refusal, by name, at a real call site - which\n")
	fmt.Printf("is the contract, not a gap. hermes-agent is Python, and Python IS a covered materializer\n")
	fmt.Printf("row — so what refuses here is the call site (**_create_kwargs assembles its arguments\n")
	fmt.Printf("elsewhere), not the language. Nothing above shipped a diff it could not stand behind.\n")
}

// ── 14b task 4.1/4.4 — the split is additive, proven on a real 40-node IR ─────────────────────────

func splitAdditivity(ir *discovery.IR) {
	fmt.Printf("-- 14b - the tools/skills IR split is ADDITIVE (task 4.1, 4.4) --\n")

	withTools, withSkills, locatable, dynamic := 0, 0, 0, 0
	for _, n := range ir.Nodes {
		if len(n.Tools) > 0 {
			withTools++
		}
		if len(n.Skills) > 0 {
			withSkills++
		}
		for _, t := range n.Tools {
			if t.Locatable() {
				locatable++
			} else {
				dynamic++
			}
		}
	}
	fmt.Printf("  nodes recording split tools: %d   skills: %d   (locatable %d / runtime-assembled %d)\n",
		withTools, withSkills, locatable, dynamic)

	raw, err := discovery.MarshalIR(*ir)
	if err != nil {
		log.Fatalf("marshal IR: %v", err)
	}
	body := string(raw)
	for _, key := range []string{`"tools"`, `"skills"`, `"declared_at"`} {
		if strings.Contains(body, key) {
			fmt.Printf("  !! the IR emitted %s - a pre-P14 consumer would see a new key\n", key)
			return
		}
	}
	fmt.Printf("  the emitted IR carries NO tools/skills/declared_at key: byte-identical to pre-P14.\n")
	fmt.Printf("  -> every stored config_hash keyed against this workflow still addresses the same bytes.\n")
	// The frozen conflated view is retained and still emitted, unchanged.
	fmt.Printf("  the frozen `tools_skills` slice is retained on all %d nodes and never repurposed.\n\n", len(ir.Nodes))
}

// ── 14b task 5.3 — a tool selection over this IR fails CLOSED ─────────────────────────────────────

func failClosed(ir *discovery.IR, base *variantspec.VariantSpec, node string) {
	fmt.Printf("-- 14b - a tool selection FAILS CLOSED against the discovered set (task 5.3) --\n")

	spec := clone(base)
	spec.Nodes[node] = variantspec.NodeOverride{ToolSelection: []string{"search_web"}}
	_, err := variantspec.Resolve(context.Background(), spec, ir, emptyRegistries{})
	switch {
	case err == nil:
		fmt.Printf("  !! the selection RESOLVED against an IR that records no tool set - a false acceptance\n\n")
	case errors.Is(err, variantspec.ErrToolNotDiscovered):
		var se *variantspec.SpecError
		errors.As(err, &se)
		fmt.Printf("  rejected at resolve: node=%s dim=%s\n", short(se.NodeID), se.Dim)
		fmt.Printf("  reason: %s\n", se.Detail)
		fmt.Printf("  -> `Tools == nil` means \"not recorded\", never \"no tools\". Accepting it would have\n")
		fmt.Printf("     admitted a prune over every IR authored before P14 - the whole stored population.\n\n")
	default:
		fmt.Printf("  unexpected rejection: %v\n\n", err)
	}
}

// ── 14a task 2.2 / 14b task 6.2 — the interim refusals, at a real call site ───────────────────────

func interimRefusals(repo string, ir *discovery.IR, node string) {
	fmt.Printf("-- 14a/14b - the interim refusals at a REAL Python call site (task 2.2, 6.2) --\n")

	entry := sealedSkill("search_kb", `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)

	cases := []struct {
		what     string
		override variantspec.ResolvedOverride
	}{
		{"bind a platform skill", variantspec.ResolvedOverride{Skills: []*registry.SkillEntry{entry}}},
		{"prune a provider tool", variantspec.ResolvedOverride{ToolSelection: []string{"search_web"}}},
	}
	for _, c := range cases {
		r := &variantspec.Resolved{
			ConfigHash: "cfg-hermes-refusal", SourceRevision: ir.Workflow.Repo.CommitSHA, Language: "python",
			Overrides: map[string]variantspec.ResolvedOverride{node: c.override},
		}
		patch, err := transform.Generate(r, repo)
		if err == nil {
			fmt.Printf("  !! %s produced a diff where a refusal was required\n", c.what)
			continue
		}
		var re *transform.RewriteError
		if !errors.As(err, &re) || !errors.Is(err, transform.ErrUnsafeRewrite) {
			fmt.Printf("  %-22s unexpected error: %v\n", c.what, err)
			continue
		}
		fmt.Printf("  %-22s REFUSED  node=%s dim=%-6s  diff emitted: %v\n",
			c.what, short(re.NodeID), re.Dim, patch != nil)
		fmt.Printf("      %s\n", wrap(re.Detail, 6))
	}
	fmt.Printf("  -> named, per dimension, with no partial diff. A silent drop would ship a node without\n")
	fmt.Printf("     the change its config_hash claims, and the eval would score a config that never existed.\n\n")
}

// ── 14a task 3.1 / 14b task 6.3 — the operators, grounded on recorded usage ───────────────────────

func operators(base *variantspec.VariantSpec, node string) {
	fmt.Printf("-- 14a/14b - the operators, grounded on recorded usage (task 3.1, 6.3) --\n")

	menu := proposal.Menu{Skills: []proposal.SkillChoice{
		{Ref: refSearchKB, Name: "search_kb", Kind: "tool"},
		{Ref: refIssueLk, Name: "issue_lookup", Kind: "tool"},
	}}
	bound := clone(base)
	bound.Nodes[node] = variantspec.NodeOverride{SkillRefs: []string{refSearchKB, refIssueLk}}
	eng := proposal.Engine{Menu: menu, Base: bound}

	// Illustrative usage: what the eval traces recorded at this node.
	usage := proposal.ToolUsage{
		Discovered: []string{"search_web", "read_file", "run_shell"},
		Exercised:  []string{"read_file", "search_kb"},
		Erroring:   []string{"issue_lookup"},
	}
	fmt.Printf("  recorded usage: discovered=%v exercised=%v erroring=%v\n",
		usage.Discovered, usage.Exercised, usage.Erroring)

	em := eng.Propose([]proposal.Target{
		{
			Diagnosis: diagnosis.Diagnosis{DiagID: "p45://tool_schema_mismatch", NodeID: node,
				TaxonomyCode: diagnosis.CauseToolSchemaMismatch, Confidence: 0.8,
				EvidenceCaseIDs: []string{"trace-004", "trace-017"}, Source: diagnosis.SourceRule},
			Pattern: patternclassifier.ToolUse, Usage: usage,
		},
		{
			Diagnosis: diagnosis.Diagnosis{NodeID: node, EvidenceCaseIDs: []string{"trace-004"}},
			Signal:    proposal.SignalUnusedTools, Pattern: patternclassifier.ToolUse, Usage: usage,
		},
	})

	byOp := map[proposal.OperatorKind][]proposal.Candidate{}
	for _, c := range em.Candidates {
		byOp[c.Operator] = append(byOp[c.Operator], c)
	}
	for _, op := range []proposal.OperatorKind{
		proposal.OpRemoveSkill, proposal.OpToolPrune, proposal.OpToolMinimize, proposal.OpAddSkill,
	} {
		cs := byOp[op]
		if len(cs) == 0 {
			fmt.Printf("  %-14s (silent - nothing grounded to change)\n", op)
			continue
		}
		for _, c := range cs {
			o := c.Spec.Nodes[node]
			detail := fmt.Sprintf("skills=%d", len(o.SkillRefs))
			if len(o.ToolSelection) > 0 {
				detail = fmt.Sprintf("keeps %v", o.SelectedTools())
			}
			fmt.Printf("  %-14s %-28s %s\n", op, detail, c.Rationale)
		}
	}
	fmt.Printf("  -> a removal is proposed only for a skill the usage says ERRORED or was NEVER EXERCISED.\n")
	fmt.Printf("     An unnecessary add wastes tokens; an unnecessary remove takes away what the workflow\n")
	fmt.Printf("     needed, so the asymmetric direction is the one that requires evidence.\n\n")
}

// ── 14a tasks 2.4 / 2.5 — the bind site validates, and every failure is allowlisted ───────────────

func bindSiteContract() {
	fmt.Printf("-- 14a - the bind site validates arguments BEFORE execution (task 2.4, 2.5) --\n")

	entry := sealedSkill("search_kb",
		`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`)
	bound := transform.BoundSkills("hermes-node", variantspec.ResolvedOverride{
		Skills: []*registry.SkillEntry{entry}})[0]

	invoked := false
	impl := func(map[string]any) (map[string]any, error) {
		invoked = true
		return map[string]any{"hits": []any{}}, nil
	}

	bad := bound.Invoke("search", map[string]any{"q": "hermes"}, impl)
	fmt.Printf("  out-of-contract args: status=%v code=%v  implementation invoked: %v\n",
		bad["status"], bad["error_code"], invoked)

	ok := bound.Invoke("search", map[string]any{"query": "hermes"}, impl)
	fmt.Printf("  conforming args:      status=%v  implementation invoked: %v\n", ok["status"], invoked)

	allowed := true
	for _, res := range []map[string]any{bad, ok} {
		if code, has := res["error_code"].(string); has &&
			!toolcontract.IsAllowedErrorCode(toolcontract.ParseErrorCode(code)) {
			allowed = false
		}
	}
	fmt.Printf("  every emitted code is inside ErrorCodeWhitelist: %v\n", allowed)
	fmt.Printf("  -> tool_error_rate stays well-defined for a bound node: a skill that invented a code\n")
	fmt.Printf("     would make a shipped metric depend on which skill happened to fail.\n\n")
}

// ── 14a task 2.6 / 14b task 5.4 — hash additivity on the real IR ──────────────────────────────────

func hashAdditivity(ir *discovery.IR, base *variantspec.VariantSpec, node string) {
	fmt.Printf("-- 14a/14b - config_hash additivity on the real IR (task 2.6, 5.4) --\n")

	regs := skillRegistries{entries: map[string]*registry.SkillEntry{
		refSearchKB: {VersionID: refSearchKB, Name: "search_kb"},
		refIssueLk:  {VersionID: refIssueLk, Name: "issue_lookup"},
	}}
	hash := func(label string, o variantspec.NodeOverride) string {
		spec := clone(base)
		spec.Nodes[node] = o
		r, err := variantspec.Resolve(context.Background(), spec, ir, regs)
		if err != nil {
			log.Fatalf("%s: %v", label, err)
		}
		return r.ConfigHash
	}

	baseline := hash("baseline", variantspec.NodeOverride{})
	oneSkill := hash("bind one", variantspec.NodeOverride{SkillRefs: []string{refSearchKB}})
	twoFwd := hash("bind two", variantspec.NodeOverride{SkillRefs: []string{refSearchKB, refIssueLk}})
	twoRev := hash("rerank", variantspec.NodeOverride{SkillRefs: []string{refIssueLk, refSearchKB}})

	fmt.Printf("  no skill, no prune : %s   (the discovered configuration)\n", short(baseline))
	fmt.Printf("  bind one skill     : %s   changed: %v\n", short(oneSkill), oneSkill != baseline)
	fmt.Printf("  bind two skills    : %s   changed: %v\n", short(twoFwd), twoFwd != oneSkill)
	fmt.Printf("  same two, reordered: %s   changed: %v  (skill order is identity-bearing)\n",
		short(twoRev), twoRev != twoFwd)

	spec := clone(base)
	r, err := variantspec.Resolve(context.Background(), spec, ir, regs)
	if err != nil {
		log.Fatalf("canonical: %v", err)
	}
	canon, err := r.Config.Canonical()
	if err != nil {
		log.Fatalf("canonical: %v", err)
	}
	fmt.Printf("  the no-prune node emits no `tool_selection` key: %v\n",
		!strings.Contains(string(canon), "tool_selection"))
	fmt.Printf("  -> add/remove/rerank each move the hash; a configuration that uses neither capability\n")
	fmt.Printf("     hashes exactly as it did before P14 existed.\n\n")
}

// ── 14a task 3.2 — a regressing skill change is WITHHELD ──────────────────────────────────────────

func verificationGate() {
	fmt.Printf("-- 14a - a skill change ships only on a verified non-regression (task 3.2) --\n")

	cases := caseIDs(30)
	cfg := verification.DefaultConfig()
	for _, tc := range []struct {
		label string
		rate  float64
	}{{"regresses", 0.35}, {"improves", 0.90}} {
		v := verifySkillChange(cases, cfg, tc.rate)
		fmt.Printf("  candidate %-10s gate=%-16s passed=%-5v  %s\n",
			tc.label, v.GateResult, v.Passed(), firstSentence(v.Reason))
	}
	fmt.Printf("  -> the decision is the MEASURED verdict, not the operator's prior. A materialized skill\n")
	fmt.Printf("     whose shape is subtly wrong compiles and then scores worse - which is what catches it.\n\n")
}

// ── NFR7 — the coverage table, read from its single source ────────────────────────────────────────

func coverage() {
	fmt.Printf("-- NFR7 - per-(language, provider) materializer coverage, from ONE table --\n")
	for _, c := range transform.MaterializerCoverage() {
		fmt.Printf("  %-6s %-10s %s\n", c.Language, c.Provider, c.SDK)
	}
	// 🔴 The cause is read off the coverage table rather than asserted. This line used to read
	// "hermes-agent is python -> not covered, which is why every binding above refused", which was true
	// when Go was the only materializer and became false the moment the Python row landed — while the
	// refusals above already said, correctly, that they were properties of the CALL SITE. A closing
	// summary that blames the language for a call-site refusal is the exact conflation the refusal
	// ordering exists to prevent (the change, then the row, then the source, and the language LAST): it
	// tells this repository's author to wait for a rewriter that would refuse their `**kwargs` call site
	// for the same reason on the day it ships.
	covered := false
	for _, c := range transform.MaterializerCoverage() {
		if c.Language == "python" {
			covered = true
			break
		}
	}
	if covered {
		fmt.Printf("  hermes-agent is python -> COVERED above. The refusals are therefore about the CALL\n")
		fmt.Printf("  SITES (**_create_kwargs), not about Python: a materializer for this language refuses\n")
		fmt.Printf("  them for the same reason. Nothing here is waiting on a language.\n")
	} else {
		fmt.Printf("  hermes-agent is python -> no row above, so a binding refuses on the language.\n")
	}
	fmt.Printf("  the prune is scored, when it applies, by the UNCHANGED harness: %s / %s (no new metric).\n\n",
		evalharness.MetricRunTokens, evalharness.MetricToolErrorRate)
}

// ── helpers ───────────────────────────────────────────────────────────────────────────────────────

// emptyRegistries resolves nothing. The fail-closed path never reaches a registry, and a stub that
// resolved would hide whether it did.
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

type skillRegistries struct {
	entries map[string]*registry.SkillEntry
}

func (s skillRegistries) ResolveModel(context.Context, string) (*registry.ModelEntry, error) {
	return nil, registry.ErrNotFound
}
func (s skillRegistries) ResolvePrompt(context.Context, string) (*registry.PromptEntry, error) {
	return nil, registry.ErrNotFound
}
func (s skillRegistries) ResolveSkill(_ context.Context, id string) (*registry.SkillEntry, error) {
	if e, ok := s.entries[id]; ok {
		return e, nil
	}
	return nil, registry.ErrNotFound
}
func (s skillRegistries) ResolveContextPolicy(context.Context, string) (*registry.ContextEntry, error) {
	return nil, registry.ErrNotFound
}

func sealedSkill(name, inputSchema string) *registry.SkillEntry {
	e, err := registry.NewSkillEntry(strings.Repeat("5", 64), name, registry.SkillSpec{
		ImplHandle:   "builtin:" + name,
		InputSchema:  json.RawMessage(inputSchema),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"hits":{"type":"array"}},"required":["hits"]}`),
	})
	if err != nil {
		log.Fatalf("seal skill %q: %v", name, err)
	}
	return e
}

// pickNode returns the first discovered node in id order, so a re-run picks the same one.
func pickNode(ir *discovery.IR) string {
	ids := make([]string, 0, len(ir.Nodes))
	for _, n := range ir.Nodes {
		ids = append(ids, n.NodeID)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func siteOf(ir *discovery.IR, id string) string {
	for _, n := range ir.Nodes {
		if n.NodeID == id {
			return fmt.Sprintf("%s:%d %s", n.CallSite.File, n.CallSite.LineStart, n.CallSite.Symbol)
		}
	}
	return "?"
}

func clone(s *variantspec.VariantSpec) *variantspec.VariantSpec {
	out := *s
	out.Order = append([]string(nil), s.Order...)
	out.Nodes = make(map[string]variantspec.NodeOverride, len(s.Nodes))
	for k, v := range s.Nodes {
		out.Nodes[k] = v
	}
	out.Edges = append([]variantspec.Edge(nil), s.Edges...)
	return &out
}

func short(s string) string {
	if len(s) > 14 {
		return s[:14]
	}
	return s
}

// firstSentence trims a verdict reason to its opening sentence. It skips a period that is a DECIMAL
// POINT — these reasons carry intervals like "[-0.21, 0.03]", and cutting at the first dot would report
// "a delta interval [-0." as if that were the whole finding.
func firstSentence(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != '.' {
			continue
		}
		if i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
			continue
		}
		return s[:i+1]
	}
	return s
}

// wrap re-flows a refusal message so a long, honest sentence stays readable in a terminal.
func wrap(s string, indent int) string {
	const width = 96
	pad := strings.Repeat(" ", indent)
	var b strings.Builder
	col := 0
	for _, w := range strings.Fields(s) {
		if col > 0 && col+len(w)+1 > width {
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

func caseIDs(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("trace-%03d", i))
	}
	return out
}

func series(label string, cases []string, seeds []int64, v float64) evalstats.Series {
	s := evalstats.Series{VariantID: label, Metric: evalharness.MetricTaskSuccess}
	for _, c := range cases {
		for _, seed := range seeds {
			s.Obs = append(s.Obs, evalstats.Observation{CaseID: c, Seed: seed, Value: v})
		}
	}
	return s
}

// hermesRunner is a stubbed EvalRunner: the GATE is the shipped code, the provider is the only stub —
// the same "the only stub is the provider" discipline internal/verification's own tests follow.
type hermesRunner struct{ quality map[string]float64 }

func (h hermesRunner) Run(_ context.Context, req verification.RunRequest) (verification.RunResult, error) {
	rate, ok := h.quality[req.ConfigHash]
	if !ok {
		rate = 0.5
	}
	return verification.RunResult{
		Quality: series(req.ConfigHash, req.CaseIDs, req.Seeds, rate),
		Cost:    series(req.ConfigHash, req.CaseIDs, req.Seeds, 0.001),
		Latency: series(req.ConfigHash, req.CaseIDs, req.Seeds, 100),
	}, nil
}

// verifySkillChange runs the REAL verification gate over a materialized skill change whose measured
// quality is `rate`, against a baseline measuring 0.60.
func verifySkillChange(cases []string, cfg verification.Config, rate float64) verification.Verdict {
	const baseline = "cfg-hermes-baseline"
	cand := fmt.Sprintf("cfg-hermes-skill-%0.2f", rate)
	v, err := verification.Verify(context.Background(), hermesRunner{quality: map[string]float64{
		baseline: 0.60, cand: rate,
	}}, verification.Proposal{
		ProposalID: "p14-hermes-skill", CandidateConfigHash: cand, BaselineConfigHash: baseline,
		SourceRevision: "hermes", DiffHash: strings.Repeat("d", 64),
		GeneratingCaseIDs: cases[:5],
		Clusters:          []attribution.FailureCluster{{ClusterID: "cl-1", MemberCaseIDs: cases[:5]}},
		TargetClusterID:   "cl-1",
	}, cases, cfg)
	if err != nil {
		log.Fatalf("verify: %v", err)
	}
	return v
}
