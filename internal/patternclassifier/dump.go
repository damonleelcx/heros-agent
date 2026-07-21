package patternclassifier

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// Dump runs a classification and prints EVERY intermediate stage for one IR: the topology as the
// detectors see it, each detector's raw proposals, what arbitration did to them, the labels that
// survived write-time validation, the residue, and every diagnostic.
//
// It exists because aggregate green tests hide per-sample bugs. A suite can pass while a real
// workflow produces a label nobody would defend, and the only way to see that is to look at one
// sample end to end, stage by stage. So the debug path is IN THE TREE and part of the package, not a
// throwaway script: it is what a reviewer runs before believing a classification, and what the demo
// harness prints.
//
// It is read-only and side-effect free; it re-derives everything from the same pure functions
// Classify uses, so what it prints is what Classify did — not a reconstruction.
func Dump(ctx context.Context, ir *discovery.IR, opts Options) (string, error) {
	if ir == nil {
		return "", fmt.Errorf("patternclassifier: nil IR")
	}
	if opts.Skills == nil {
		return "", fmt.Errorf("patternclassifier: Options.Skills is required")
	}
	var b strings.Builder
	var diags diagSink
	g := newGraph(ir)
	env := &detectEnv{skills: opts.Skills, skillRoles: opts.SkillRoles, diags: &diags}

	fmt.Fprintf(&b, "=== 1. TOPOLOGY (what the detectors actually see) ===\n")
	fmt.Fprintf(&b, "workflow=%s ir_version=%s nodes=%d edges=%d taxonomy=%s\n",
		ir.Workflow.ID, ir.IRVersion, len(ir.Nodes), len(ir.Edges), TaxonomyVersion)
	for _, id := range g.order {
		n := g.nodes[id]
		roles := roleOf(n, env)
		roleNames := make([]string, 0, len(roles))
		for r := range roles {
			roleNames = append(roleNames, string(r))
		}
		sort.Strings(roleNames)
		fmt.Fprintf(&b, "  %-14s model=%-28s policy=%-16s sem=%-11s tools=%v roles=%v\n",
			id, modelKey(n), n.ContextAssembly.Policy, n.InvocationSemantics.Type, n.ToolsSkills, roleNames)
		fmt.Fprintf(&b, "        data:  in=%v out=%v\n", g.dataIn[id], g.dataOut[id])
		fmt.Fprintf(&b, "        ctrl:  in=%v out=%v\n", g.controlIn[id], g.controlOut[id])
	}

	fmt.Fprintf(&b, "\n=== 2. RULE DETECTORS (raw proposals, before arbitration) ===\n")
	var proposals []RegionProposal
	for _, d := range detectors() {
		got := d.detect(g, env)
		proposals = append(proposals, got...)
		if len(got) == 0 {
			fmt.Fprintf(&b, "  %-46s -\n", d.id())
			continue
		}
		for _, p := range got {
			fmt.Fprintf(&b, "  %-46s %-26s scope=%-6s conf=%.2f candidate=%v nodes=%v\n",
				d.id(), p.Pattern, p.Scope, p.Confidence, p.Candidate, normalizeNodeIDs(p.NodeIDs))
		}
	}

	fmt.Fprintf(&b, "\n=== 3. ARBITRATION (region identity + overlap precedence) ===\n")
	regions := resolve(proposals, &diags)
	for _, r := range regions {
		fmt.Fprintf(&b, "  %-16s %-26s scope=%-6s nodes=%v\n", r.SubgraphID, r.Pattern, r.Scope, r.NodeIDs)
	}
	if len(regions) == 0 {
		fmt.Fprintf(&b, "  (none — every node is ambiguous residue)\n")
	}

	res, err := Classify(ctx, ir, opts)
	if err != nil {
		return b.String(), err
	}

	fmt.Fprintf(&b, "\n=== 4. LABELS (after write-time validation) ===\n")
	for _, l := range res.Labels {
		cand := ""
		if l.Candidate {
			cand = "  [CANDIDATE — behavioral confirmation deferred to P5]"
		}
		ord := 0
		if info, ok := Info(l.Pattern); ok {
			ord = info.Ordinal
		}
		fmt.Fprintf(&b, "  %-16s #%-2d %-26s conf=%.2f source=%-4s provenance=%s%s\n",
			l.SubgraphRef, ord, l.Pattern, l.Confidence, l.Source, l.DetectorID+l.LLMRunRef, cand)
		// The dispatch, shown at the point of use: this is what the label actually DOES.
		if ms, ok := MetricSetFor(l.Pattern); ok {
			fmt.Fprintf(&b, "        dispatches metric-set: primary=%s all=%v\n", ms.Primary, ms.Metrics)
		} else {
			fmt.Fprintf(&b, "        dispatches metric-set: NONE — no mapping for this pattern\n")
		}
	}
	if len(res.Labels) == 0 {
		fmt.Fprintf(&b, "  (none)\n")
	}

	fmt.Fprintf(&b, "\n=== 5. AMBIGUOUS RESIDUE (the only input the LLM fallback ever sees) ===\n")
	for _, sg := range res.Residue {
		fmt.Fprintf(&b, "  %-16s nodes=%v\n", sg.SubgraphID, sg.NodeIDs)
	}
	if len(res.Residue) == 0 {
		fmt.Fprintf(&b, "  (none — fully rule-covered, so ZERO LLM calls)\n")
	}
	fmt.Fprintf(&b, "  llm_calls=%d\n", res.LLMCalls)

	fmt.Fprintf(&b, "\n=== 6. DIAGNOSTICS (everything rejected or dropped) ===\n")
	for _, d := range res.Diagnostics {
		fmt.Fprintf(&b, "  %s\n", d)
	}
	if len(res.Diagnostics) == 0 {
		fmt.Fprintf(&b, "  (none)\n")
	}
	return b.String(), nil
}
