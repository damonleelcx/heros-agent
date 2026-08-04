// Command proposals drives the P5.5 proposal + verification surface against a REAL repository
// (built for github.com/nousresearch/hermes-agent, the same target as cmd/proof/contracts): it discovers the
// IR, emits change-operator candidates on REAL discovered call sites, and runs each candidate's
// deterministic AST-level codemod (internal/transform) to produce a REAL source diff against the
// repo's Python. It NEVER executes the target — it parses it (invariant I1) — so it is safe to point at
// any checkout.
//
// What is real here: the repository, the discovered IR (node ids, files, line ranges, symbols), and
// the source diffs (the codemod's byte-splice output against the actual source). What is stubbed — and
// labelled as such — is the DIAGNOSIS input (in production it comes from the P4.5 eval/attribution
// engine) and the VERIFICATION deltas (in production they come from real eval runs through a provider);
// here the gate runs for real over canned deltas, so the verdict logic is the shipped path.
//
//	go run ./cmd/proof/proposals -repo /path/to/hermes-agent [-dump] [-addr 127.0.0.1:8487]
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sourcerev"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
	"github.com/heros-foreal/agentd/internal/verification"
)

// pinnedSHA is the commit this demo's documented output was produced from. It is VERIFIED against the
// checkout rather than trusted: labelling the IR with a commit the tree is not at would put a false
// source_revision on every number below, and source_revision is half the reproducibility key.
const pinnedSHA = "de5ece994415276d215976836161f871f1d6d8f5"

// commitSHA is the revision this run ACTUALLY parsed. It is a var, not a const, because the pin above is
// verified against the checkout rather than assumed — main() sets it from sourcerev.Resolve, which fails
// loudly instead of letting the output carry a provenance nothing can check.
var commitSHA string

// target is one real discovered node we propose a change for, spanning the WHOLE operator catalog
// (not just model): each carries an illustrative diagnosis, the operator, the dimension its codemod
// touches, and the from/to shown in the Variant-Spec diff.
type target struct {
	symbolContains string // pick the real node by its enclosing symbol
	operator       proposal.OperatorKind
	code           string // illustrative diagnosis label for the card
	pattern        string
	dim            string // "model" | "prompt" | "skills" | "context" | "prune"
	from, to       string // Variant-Spec diff display
	payload        string // the concrete new value the codemod would apply (model id / prompt body / skill / policy)
	// verification outcome to demonstrate (real gate, canned delta):
	outcome string // "good" | "notheld" | "regress" | "noise"
}

// targets covers six operator families across the catalog, each on a REAL discovered hermes node.
func targets() []target {
	return []target{
		{"async_call_llm", proposal.OpModelUpgrade, "model_capability_mismatch", "tool_use",
			"model", "(discovered)", "gpt-5", "gpt-5", "good"},
		{"_generate_summary", proposal.OpPromptRewrite, "prompt_format_drift", "prompt_chaining",
			"prompt", "prompt://summary@v1", "prompt://summary@v2 (+schema)", "Summarize the trajectory. Return JSON {summary, key_steps}.", "good"},
		{"handle_max_iterations", proposal.OpContextPolicy, "context_overflow", "exception_handling_recovery",
			"context", "full-window", "summarization", "summarization", "notheld"},
		{"call_llm", proposal.OpAddSkill, "tool_schema_mismatch", "tool_use",
			"skills", "N tools", "N+1 tools (+web_search)", "web_search", "good"},
		{"run_task", proposal.OpAddRerank, "retrieval_miss", "retrieval_rag",
			"skills", "retriever", "retriever + rerank", "cohere-rerank", "regress"},
		{"_dispatch_nonstreaming_api_request", proposal.OpPrune, "redundant_node", "routing",
			"prune", "in graph", "removed + rewired", "", "noise"},

		// P13 — the deeper prompt operators and model selection under the held-out guardrail. Added
		// here because the P13 operators are catalog rows like any other, so the surface this demo
		// serves should span them too. The downgrade uses the "notheld" outcome deliberately: its
		// admissibility is decided by the held-out quality guardrail, not by a headline delta.
		{"_generate_summary", proposal.OpPromptCompress, "prompt_format_drift", "prompt_chaining",
			"prompt", "prompt://summary@v2", "prompt://summary@v3 (compressed)",
			"Summarize the trajectory. Return JSON {summary, key_steps}.", "good"},
		{"call_llm", proposal.OpInstructionHarden, "prompt_format_drift", "tool_use",
			"prompt", "prompt://tool@v1", "prompt://tool@v2 (hardened)",
			"Call the tool. Follow every instruction exactly and completely.", "good"},
		{"_query_model", proposal.OpModelDowngrade, "cost_bottleneck", "routing",
			"model", "(discovered)", "gpt-4o-mini", "gpt-4o-mini", "notheld"},
	}
}

func main() {
	repo := flag.String("repo", ".", "path to the hermes-agent checkout (read-only)")
	addr := flag.String("addr", "127.0.0.1:8487", "listen address")
	level := flag.String("level", "assisted", "automation level: advisory | assisted")
	dump := flag.Bool("dump", false, "print the real source diffs to stdout and exit")
	scan := flag.Bool("scan", false, "scan every discovered node for a rewritable model call site and exit")
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
	log.Printf("discovered %d nodes (language=%s) at %s", len(ir.Nodes), ir.Workflow.Language, commitSHA[:12])

	// Map node id → its real IR call site (file, line, symbol), so we can name a target by its symbol.
	siteByNode := map[string]discovery.IRCallSite{}
	for _, n := range ir.Nodes {
		siteByNode[n.NodeID] = n.CallSite
	}

	if *scan {
		// Try a provider-matched model rewrite on EVERY node; report which call sites are rewritable.
		var rewritable, refused int
		for _, n := range ir.Nodes {
			mid := "gpt-4o"
			if n.Model.Provider == "anthropic" {
				mid = "claude-sonnet-5"
			}
			diff, _ := realDiff(*repo, n.NodeID, target{dim: "model", payload: mid})
			cs := n.CallSite
			if diff != "" {
				rewritable++
				fmt.Printf("REWRITABLE  %-52s %s:%d\n", cs.Symbol, cs.File, cs.LineStart)
			} else {
				refused++
			}
		}
		fmt.Printf("\n%d/%d nodes rewritable, %d refused (behavior-preserving safety gate)\n", rewritable, len(ir.Nodes), refused)
		return
	}

	cards, dumped := buildCards(ir, *repo, siteByNode, verification.AutomationLevel(*level))
	if *dump {
		for _, d := range dumped {
			fmt.Printf("\n═══ %s  (%s:%d)  op=%s ═══\n", d.symbol, d.file, d.line, d.op)
			if d.diff == "" {
				fmt.Printf("  (no automated source change: %s)\n", d.note)
			} else {
				fmt.Println(indent(d.diff))
			}
		}
		return
	}

	var recs, withheld []api.Card
	for _, c := range cards {
		if c.State == "verified" {
			recs = append(recs, c)
		} else {
			withheld = append(withheld, c)
		}
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].Delta > recs[j].Delta })

	trend := verification.BuildTrend([]verification.TrendPoint{
		{VariantID: "hermes@" + commitSHA[:7], Iteration: 1, OverallSuccess: 0.58, ClusterSizes: map[string]int{"format": 6, "tool": 2}},
		{VariantID: "iter-2", Iteration: 2, OverallSuccess: 0.71, ClusterSizes: map[string]int{"format": 2, "tool": 1}},
	})
	state := "ready"
	if len(recs) == 0 {
		state = "empty"
	}
	src := hermesSource{surface: api.Surface{
		WorkflowID: "nousresearch/hermes-agent", AutomationLevel: *level, State: state,
		Recommendations: recs, Withheld: withheld, Trend: trend,
	}}

	// Register the console's credential → tenant, so the P9 console's BFF can read this surface over a
	// real network hop (the same convention cmd/proof/customerconsole documents). AuthMode "required" is what wires
	// the auth middleware and puts a tenant PRINCIPAL in the request context; without it a tenant-scoped
	// read answers 401 even for a valid key.
	cfg := config.Config{
		AuthMode: "required",
		TenantCredentials: []config.TenantCredential{{
			TenantID: "nousresearch/hermes-agent", APIKey: "p55hermes-demo-credential-do-not-ship",
			Role: "member", KeyID: "p55hermes",
		}},
	}
	s := api.New(nil, cfg)
	s.MountProposals(src)
	fmt.Printf("P5.5 on hermes-agent:  http://%s/recommendations?workflow=hermes\n", *addr)
	fmt.Printf("surface JSON:          http://%s/api/v1/workflows/hermes/proposals\n", *addr)
	log.Fatal(http.ListenAndServe(*addr, s.Handler))
}

type dumpRow struct {
	symbol, file, op, diff, note string
	line                         int
}

func buildCards(ir *discovery.IR, root string, sites map[string]discovery.IRCallSite, level verification.AutomationLevel) ([]api.Card, []dumpRow) {
	var cards []api.Card
	var dumped []dumpRow

	for _, t := range targets() {
		nodeID, site, ok := findNode(sites, t.symbolContains)
		if !ok {
			log.Printf("target %q not found among discovered call sites; skipping", t.symbolContains)
			continue
		}
		// Build the candidate's REAL source diff by running the codemod for this dimension.
		diff, note := realDiff(root, nodeID, t)

		pres := proposal.Presentation{
			Operator:        t.operator,
			NodeID:          shortNode(nodeID),
			Pattern:         t.pattern,
			DiagID:          "p45://" + t.code,
			EvidenceCaseIDs: []string{"trace-" + shortNode(nodeID)},
			Rationale:       rationale(t, site),
			SourceDiff:      diff,
			SpecDiff:        []proposal.DimChange{{NodeID: shortNode(nodeID), Dimension: t.dim, From: t.from, To: t.to}},
			ConfigHash:      configHashFor(nodeID, t.dim+t.payload),
		}
		dumped = append(dumped, dumpRow{symbol: site.Symbol, file: site.File, line: site.LineStart, op: string(t.operator), diff: diff, note: note})

		verdict := gateVerdict(t, pres.ConfigHash)
		card := api.BuildCard(pres, "built", verdict, level)
		// On this repo every discovered call site assembles its args at runtime (**kwargs), so the
		// behavior-preserving codemod REFUSES to auto-rewrite (Q8 — advisory-only): show the real refusal
		// in the source-diff panel, and withdraw the one-click PR-open (there is no diff to carry).
		if diff == "" {
			card.SourceDiff = "⚠ no automated source change for this call site:\n" + firstSentence(note)
			card.CanOpenPR = false
			card.PRDisabledReason = "advisory-only — no automated source change for this call site"
		}
		cards = append(cards, card)
	}
	return cards, dumped
}

// realDiff runs the deterministic codemod for the target's dimension override on a real node and
// returns the unified diff (empty when the codemod cannot rewrite this call site — a real, honest
// outcome; the reason is carried in note). Prune is a structural graph edit with no single call site.
func realDiff(root, nodeID string, t target) (diff, note string) {
	if t.dim == "prune" {
		return "", "structural change: prune removes the node and rewires its neighbours — a graph edit, not a single call-site rewrite"
	}
	override, err := overrideFor(t)
	if err != nil {
		return "", err.Error()
	}
	resolved := &variantspec.Resolved{
		ConfigHash:     configHashFor(nodeID, t.dim+t.payload),
		SourceRevision: commitSHA,
		Language:       "python",
		Overrides:      map[string]variantspec.ResolvedOverride{nodeID: override},
	}
	patch, err := transform.Generate(resolved, root)
	if err != nil {
		return "", err.Error()
	}
	if patch.IsEmpty() {
		return "", "the discovered call site has no rewritable " + t.dim + " literal (assembled at runtime)"
	}
	return string(patch.Diff), ""
}

// overrideFor builds the ResolvedOverride the codemod acts on for this dimension.
func overrideFor(t target) (variantspec.ResolvedOverride, error) {
	switch t.dim {
	case "model":
		return variantspec.ResolvedOverride{Model: modelEntry(t.payload)}, nil
	case "prompt":
		tmpl, err := registry.ParseTemplate(t.payload)
		if err != nil {
			return variantspec.ResolvedOverride{}, err
		}
		return variantspec.ResolvedOverride{Prompt: &registry.PromptEntry{
			VersionID: strings.Repeat("b", 64), Name: "summary", Template: tmpl,
			Spec: registry.PromptSpec{BodyBlobHash: strings.Repeat("c", 64), Slots: tmpl.Slots()}}}, nil
	case "skills":
		return variantspec.ResolvedOverride{Skills: []*registry.SkillEntry{{
			VersionID: strings.Repeat("d", 64), Name: t.payload}}}, nil
	case "context":
		return variantspec.ResolvedOverride{Context: &registry.ContextEntry{
			VersionID: strings.Repeat("e", 64), Name: t.payload,
			Spec: registry.ContextSpec{Policy: t.payload}}}, nil
	default:
		return variantspec.ResolvedOverride{}, fmt.Errorf("unknown dimension %q", t.dim)
	}
}

func modelEntry(modelID string) *registry.ModelEntry {
	prov := "openai"
	if strings.HasPrefix(modelID, "claude") {
		prov = "anthropic"
	}
	return &registry.ModelEntry{VersionID: strings.Repeat("a", 64), Name: modelID,
		Spec: registry.ModelSpec{Provider: prov, ModelID: modelID}}
}

// gateVerdict runs the REAL verification gate over canned per-case deltas selected by the demo outcome.
func gateVerdict(t target, configHash string) verification.Verdict {
	evalSet := []string{"g1", "g2", "g3", "h1", "h2", "h3", "h4", "h5", "h6"}
	base := configHash + "-base"
	gen := []string{"g1", "g2", "g3"}
	p := verification.Proposal{ProposalID: proposalID(t), CandidateConfigHash: configHash,
		BaselineConfigHash: base, SourceRevision: commitSHA, DiffHash: configHash[:16], GeneratingCaseIDs: gen}

	var baseSucc, candSucc map[string]float64
	var candCost, candLat float64
	cfg := verification.DefaultConfig()
	switch t.outcome {
	case "good":
		baseSucc, candSucc, candCost, candLat = succ(evalSet, 0.35, nil), succ(evalSet, 0.9, nil), 0.012, 640
	case "notheld":
		p.GeneratingCaseIDs = evalSet // no held-out split
		baseSucc, candSucc, candCost, candLat = succ(evalSet, 0.4, nil), succ(evalSet, 0.82, nil), 0.004, 300
	case "regress":
		clB := []string{"b1", "b2", "b3", "b4"}
		p.TargetClusterID = "A"
		p.Clusters = []attribution.FailureCluster{
			{ClusterID: "A", Label: "format", MemberCaseIDs: []string{"h1", "h2", "h3", "h4", "h5", "h6"}},
			{ClusterID: "B", Label: "tool-use", MemberCaseIDs: clB}}
		baseSucc = succ(evalSet, 0.3, map[string]float64{"b1": 0.9, "b2": 0.9, "b3": 0.9, "b4": 0.9})
		candSucc = succ(evalSet, 0.92, map[string]float64{"b1": 0.1, "b2": 0.1, "b3": 0.1, "b4": 0.1})
		candCost, candLat = 0.02, 900
	default: // noise
		baseSucc, candSucc, candCost, candLat = succ(evalSet, 0.5, nil), succ(evalSet, 0.5, nil), 0.003, 250
	}
	runner := cannedRunner{byConfig: map[string]runData{
		configHash: {succ: candSucc, cost: candCost, lat: candLat},
		base:       {succ: baseSucc, cost: 0.01, lat: 500},
	}}
	v, err := verification.Verify(context.Background(), runner, p, evalSet, cfg)
	if err != nil {
		log.Fatalf("verify %s: %v", t.symbolContains, err)
	}
	return v
}

// ── stubbed runner (the ONLY stub — no provider) ─────────────────────────────────────────────────

type runData struct {
	succ map[string]float64
	cost float64
	lat  float64
}

type cannedRunner struct{ byConfig map[string]runData }

func (c cannedRunner) Run(_ context.Context, req verification.RunRequest) (verification.RunResult, error) {
	d, ok := c.byConfig[req.ConfigHash]
	if !ok {
		d = runData{succ: map[string]float64{}, cost: 0.01, lat: 500}
	}
	mk := func(vals map[string]float64) evalstats.Series {
		var s evalstats.Series
		for _, id := range req.CaseIDs {
			for _, seed := range req.Seeds {
				s.Obs = append(s.Obs, evalstats.Observation{CaseID: id, Seed: seed, Value: vals[id]})
			}
		}
		return s
	}
	cost := func(v float64) evalstats.Series {
		var s evalstats.Series
		for _, id := range req.CaseIDs {
			for _, seed := range req.Seeds {
				s.Obs = append(s.Obs, evalstats.Observation{CaseID: id, Seed: seed, Value: v})
			}
		}
		return s
	}
	return verification.RunResult{Quality: mk(d.succ), Cost: cost(d.cost), Latency: cost(d.lat)}, nil
}

type hermesSource struct{ surface api.Surface }

func (h hermesSource) Surface(_, _ string) (api.Surface, bool) { return h.surface, true }
func (h hermesSource) OpenPR(_, _, pid string) (api.PRResult, error) {
	return api.PRResult{ProposalID: pid, Branch: "optimizer/" + pid,
		URL:      "https://github.com/nousresearch/hermes-agent/compare/optimizer/" + pid,
		Rollback: "git revert <merge-commit>"}, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

func succ(ids []string, v float64, over map[string]float64) map[string]float64 {
	m := map[string]float64{}
	for _, id := range ids {
		m[id] = v
	}
	for k, val := range over {
		m[k] = val
	}
	return m
}

func findNode(sites map[string]discovery.IRCallSite, symbolContains string) (string, discovery.IRCallSite, bool) {
	// Deterministic: pick the lowest node id whose symbol matches.
	var ids []string
	for id := range sites {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if strings.Contains(sites[id].Symbol, symbolContains) {
			return id, sites[id], true
		}
	}
	return "", discovery.IRCallSite{}, false
}

func rationale(t target, s discovery.IRCallSite) string {
	at := fmt.Sprintf("%s (%s:%d)", s.Symbol, s.File, s.LineStart)
	switch t.operator {
	case proposal.OpModelUpgrade:
		return "capability gap at " + at + " → upgrade model to " + t.to
	case proposal.OpModelDowngrade:
		return "cost bottleneck at " + at + " → downgrade model to " + t.to
	case proposal.OpPromptRewrite:
		return "output-contract violation at " + at + " → grounded prompt rewrite + format constraint"
	case proposal.OpContextPolicy:
		return "context overflow at " + at + " → switch context policy to " + t.to
	case proposal.OpAddSkill:
		return "tool schema mismatch at " + at + " → add skill " + t.payload + " from registry"
	case proposal.OpAddRerank:
		return "retrieval miss at " + at + " → add a rerank stage (" + t.payload + ")"
	case proposal.OpPrune:
		return "redundant node at " + at + " → prune and rewire neighbours"
	default:
		return string(t.operator) + " at " + at
	}
}

// proposalID derives a UNIQUE id for a target. The operator is part of it because two operators can
// legitimately target the SAME call site — P13's prompt_compress and the original prompt_rewrite both
// act on _generate_summary — and keying only on the symbol collides: the surface would carry two cards
// with one id, the console would render duplicate React keys, and the detail route would resolve every
// such id to whichever card sorted first, making the others unreachable by URL.
func proposalID(t target) string {
	return "p-" + t.symbolContains + "-" + string(t.operator)
}

func shortNode(id string) string {
	if len(id) > 14 {
		return id[:14]
	}
	return id
}

func configHashFor(nodeID, modelID string) string {
	sum := sha256.Sum256([]byte(nodeID + "\x00" + modelID))
	return hex.EncodeToString(sum[:])
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}

// firstSentence trims the transform's long refusal down to its first clause for the card.
func firstSentence(s string) string {
	s = strings.TrimPrefix(s, "transform: cannot rewrite this call site safely: ")
	if i := strings.Index(s, ". "); i > 0 && i < 240 {
		return s[:i+1]
	}
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}
