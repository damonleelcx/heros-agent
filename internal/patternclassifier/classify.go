package patternclassifier

import (
	"context"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// Options configures one classification.
type Options struct {
	// Skills is the resolved Skill Registry snapshot. REQUIRED: without it the Tool Use detector
	// could not tell "bound to no tools" from "bound to tools we failed to look up", and would
	// report a workflow as tool-free — a silent false negative. Classify refuses rather than guess.
	Skills SkillResolver
	// SkillRoles declares which registered skills play a retrieval-pipeline role. May be empty;
	// the RAG detector then relies on the registered context-assembly policy alone.
	SkillRoles map[string]SkillRole

	// Fallback is the constrained LLM-as-classifier. OPTIONAL and nil by default: with no model,
	// the ambiguous residue stays honestly unclassified, which is a first-class state. There is
	// deliberately no built-in stub — a default that returns plausible labels is indistinguishable
	// from a working classifier and is exactly how mock output reaches production.
	Fallback FallbackModel
	// FallbackConfig is recorded on every fallback run for reproducibility. PromptVersion and
	// TaxonomyVersion are overwritten with derived values, so they cannot be set to a lie.
	FallbackConfig FallbackConfig
}

// Result is one classification: the labels, the regions they name, and everything that was rejected
// along the way.
type Result struct {
	// Labels are per-subgraph, sorted by LabelLess — byte-identical across runs on the same IR.
	Labels []Label `json:"labels"`
	// Subgraphs are the region definitions the labels reference. A node-scoped label references a
	// node_id and has no entry here; a region-scoped label always has one.
	Subgraphs []Subgraph `json:"subgraphs"`
	// Diagnostics record everything rejected or dropped. Never empty-by-omission: if a label was
	// turned away, it is here.
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	// Residue lists the subgraphs no rule detector covered — the ambiguous regions, and the ONLY
	// input the LLM fallback is ever given.
	Residue []Subgraph `json:"residue,omitempty"`
	// LLMRuns are the reproducibility records for the fallback invocations, keyed by config_hash.
	LLMRuns []LLMRun `json:"llm_runs,omitempty"`
	// LLMCalls counts fallback invocations. Asserted to be 0 on a fully rule-covered IR: this is the
	// determinism guarantee made countable rather than merely claimed.
	LLMCalls int `json:"llm_calls"`
}

// LabelsFor returns the labels attached to one subgraph_ref, in stable order.
func (r Result) LabelsFor(ref string) []Label {
	var out []Label
	for _, l := range r.Labels {
		if l.SubgraphRef == ref {
			out = append(out, l)
		}
	}
	return out
}

// Classify partitions an IR into subgraphs and labels each with the agentic pattern(s) it
// implements: rule detectors first, then (in the LLM layer) only the residue they did not cover.
//
// The RULE layer is a pure function of (IR, Options): no clock, no randomness, no ordered-map
// dependence, and the registry lookup it needs happens before it is called. ctx exists only for the
// optional LLM fallback; with Options.Fallback nil, Classify performs no I/O at all.
func Classify(ctx context.Context, ir *discovery.IR, opts Options) (Result, error) {
	if ir == nil {
		return Result{}, fmt.Errorf("patternclassifier: nil IR")
	}
	if opts.Skills == nil {
		return Result{}, fmt.Errorf("patternclassifier: Options.Skills is required " +
			"(an unavailable registry must not be reported as a workflow with no tool use)")
	}
	var diags diagSink
	g := newGraph(ir)
	env := &detectEnv{skills: opts.Skills, skillRoles: opts.SkillRoles, diags: &diags}

	var proposals []RegionProposal
	for _, d := range detectors() {
		proposals = append(proposals, d.detect(g, env)...)
	}
	regions := resolve(proposals, &diags)

	res := Result{}
	covered := map[string]bool{}
	seenSubgraph := map[string]bool{}
	for _, r := range regions {
		label := Label{
			Pattern: r.Pattern, Confidence: r.Confidence, Source: SourceRule,
			SubgraphRef: r.SubgraphID, DetectorID: r.DetectorID,
			TaxonomyVersion: TaxonomyVersion, Candidate: r.Candidate,
		}
		// EVERY label passes the same write-time gate, whatever produced it. A detector cannot ship a
		// label the contract forbids just because it is "one of ours".
		if err := label.Validate(); err != nil {
			diags.rejectLabel(label, err)
			continue
		}
		res.Labels = append(res.Labels, label)
		for _, id := range r.NodeIDs {
			covered[id] = true
		}
		if r.Scope == ScopeRegion && !seenSubgraph[r.SubgraphID] {
			seenSubgraph[r.SubgraphID] = true
			res.Subgraphs = append(res.Subgraphs, Subgraph{SubgraphID: r.SubgraphID, NodeIDs: r.NodeIDs})
		}
	}

	// The ambiguous residue: what the rules did not cover. RULES-FIRST PRECEDENCE IS STRUCTURAL, not
	// a policy checked after the fact — a rule-covered subgraph is simply never in this list, so the
	// LLM cannot be asked about it and therefore cannot override it. A fully rule-covered IR yields
	// an empty residue and, consequently, ZERO LLM calls.
	for _, comp := range g.residueComponents(covered) {
		res.Residue = append(res.Residue, Subgraph{SubgraphID: SubgraphIDFor(comp), NodeIDs: comp})
	}

	llmLabels, runs, calls, ferr := runFallback(ctx, opts.Fallback, opts.FallbackConfig, g, res.Residue, seenSubgraph, &diags)
	if ferr != nil {
		return Result{}, ferr
	}
	res.Labels = append(res.Labels, llmLabels...)
	res.LLMRuns = runs
	res.LLMCalls = calls
	// A residue subgraph the fallback labeled is a real region now, so it gets an entry like any
	// other. Without this, an llm-sourced label would reference a subgraph nothing defines.
	for _, l := range llmLabels {
		if seenSubgraph[l.SubgraphRef] {
			continue
		}
		for _, sg := range res.Residue {
			if sg.SubgraphID == l.SubgraphRef {
				seenSubgraph[l.SubgraphRef] = true
				res.Subgraphs = append(res.Subgraphs, sg)
				break
			}
		}
	}

	sort.SliceStable(res.Labels, func(i, j int) bool { return LabelLess(res.Labels[i], res.Labels[j]) })
	sort.SliceStable(res.Subgraphs, func(i, j int) bool { return res.Subgraphs[i].SubgraphID < res.Subgraphs[j].SubgraphID })
	res.Diagnostics = diags.sorted()
	return res, nil
}
