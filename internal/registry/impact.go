package registry

import (
	"fmt"
	"sort"
)

// Impact analysis for a proposed prompt edit (task 2.6).
//
// # What it answers, and when
//
// Before a user publishes an edited prompt body, they need to learn — while still in the editor —
// that an added variable would leave a node unable to transform, rather than discovering it at codemod
// time after the version is already published and a spec already submitted (proposal §"a trap"). So
// this runs on a PROPOSED body, before publish, and reports which nodes pinning the prompt would fail
// to transform under the proposed slot set, and why.
//
// # It names what it could not analyze
//
// A node the platform cannot decide about is reported as UNANALYZED with a reason — never omitted.
// An absent entry would read as a clean bill of health, and "silence is not a clean bill of health"
// (proposal). This is the load-bearing honesty of the feature: the failure mode to avoid is telling a
// user zero nodes are blocked when the truth is "zero that I could see."
//
// # Why the rule here is exactly the transform's rule
//
// The decision "can this node transform under this slot set?" is the same one internal/transform's
// promptExprFor makes: every slot must be satisfied exactly once (by an explicit binding or an
// identically-spelled call-site expression), and every call-site operand feeding the prompt must be
// claimed by some slot — an unclaimed operand is a runtime value a rewrite would silently drop. This
// function reproduces that rule over recorded facts so the answer it gives before publish matches the
// answer the engine gives at transform time.

// ImpactNode is one node currently pinning the prompt, described by exactly what could satisfy the
// proposed slot set.
type ImpactNode struct {
	NodeID string
	// CallSiteExprs are the identically-spelled expressions the call site feeds into its prompt (the
	// operands promptExprFor matches slots against). These both satisfy slots and, if left unclaimed,
	// block the transform.
	CallSiteExprs []string
	// BoundSlots are slot names satisfied by an explicit binding on this node's override (P10 §3). A
	// bound slot needs no call-site operand. Empty until variable-bindings ships; additive here.
	BoundSlots []string
	// Analyzable is false when the platform lacks the information to decide this node — e.g. the IR
	// recorded no call-site operands for it. Such a node is reported unanalyzed, not assumed safe.
	Analyzable bool
	// UnanalyzableWhy is the reason, required when Analyzable is false.
	UnanalyzableWhy string
}

// ImpactBlocked names a node that would fail to transform and why.
type ImpactBlocked struct {
	NodeID string `json:"node_id"`
	Reason string `json:"reason"`
}

// ImpactUnanalyzed names a node the analysis could not decide, and why. Reported, never omitted.
type ImpactUnanalyzed struct {
	NodeID string `json:"node_id"`
	Why    string `json:"why"`
}

// PromptImpactAnalysis is the result: the proposed slot set, the nodes that would be blocked, and the
// nodes that could not be analyzed.
type PromptImpactAnalysis struct {
	ProposedSlots []string           `json:"proposed_slots"`
	Blocked       []ImpactBlocked    `json:"blocked"`
	Unanalyzed    []ImpactUnanalyzed `json:"unanalyzed"`
}

// AnalyzeImpact reports which of the given nodes would fail to transform under the proposed body's slot
// set (task 2.6). A malformed proposed body is rejected the same way publish rejects it — naming the
// offending position — because a body that cannot be parsed has no slot set to analyze against.
//
// The nodes are supplied by the caller (the studio, which holds the spec being edited and the IR), so
// this stays a pure function over recorded facts and needs no global scan of stored specs. The result
// is deterministic: Blocked and Unanalyzed are ordered by node id.
func AnalyzeImpact(proposedBody string, nodes []ImpactNode) (*PromptImpactAnalysis, error) {
	tmpl, err := ParseTemplate(proposedBody)
	if err != nil {
		return nil, fmt.Errorf("impact analysis: proposed body does not parse: %w", err)
	}
	proposed := tmpl.Slots() // sorted, deduplicated
	proposedSet := map[string]bool{}
	for _, s := range proposed {
		proposedSet[s] = true
	}

	out := &PromptImpactAnalysis{
		ProposedSlots: proposed,
		Blocked:       []ImpactBlocked{},
		Unanalyzed:    []ImpactUnanalyzed{},
	}

	for _, n := range nodes {
		if !n.Analyzable {
			why := n.UnanalyzableWhy
			if why == "" {
				why = "the platform has no recorded call-site information for this node"
			}
			out.Unanalyzed = append(out.Unanalyzed, ImpactUnanalyzed{NodeID: n.NodeID, Why: why})
			continue
		}
		if reason := blockReason(proposed, proposedSet, n); reason != "" {
			out.Blocked = append(out.Blocked, ImpactBlocked{NodeID: n.NodeID, Reason: reason})
		}
	}

	sort.Slice(out.Blocked, func(i, j int) bool { return out.Blocked[i].NodeID < out.Blocked[j].NodeID })
	sort.Slice(out.Unanalyzed, func(i, j int) bool { return out.Unanalyzed[i].NodeID < out.Unanalyzed[j].NodeID })
	return out, nil
}

// blockReason returns a non-empty reason iff the node would fail to transform under the proposed slot
// set. It applies promptExprFor's two rules: every proposed slot must be satisfiable, and every
// call-site operand must be claimed by a proposed slot.
func blockReason(proposed []string, proposedSet map[string]bool, n ImpactNode) string {
	bound := map[string]bool{}
	for _, s := range n.BoundSlots {
		bound[s] = true
	}
	callSite := map[string]bool{}
	for _, e := range n.CallSiteExprs {
		callSite[e] = true
	}

	// 1. Every proposed slot must be satisfied — by an explicit binding, or by an identically-spelled
	//    call-site expression.
	var unsatisfied []string
	for _, s := range proposed {
		if !bound[s] && !callSite[s] {
			unsatisfied = append(unsatisfied, s)
		}
	}

	// 2. Every call-site operand must be claimed by some proposed slot; an operand no slot claims is a
	//    runtime value the rewrite would silently drop, which promptExprFor refuses.
	var unclaimed []string
	for _, e := range n.CallSiteExprs {
		if !proposedSet[e] {
			unclaimed = append(unclaimed, e)
		}
	}
	sort.Strings(unsatisfied)
	sort.Strings(unclaimed)

	switch {
	case len(unsatisfied) > 0 && len(unclaimed) > 0:
		return fmt.Sprintf("slot(s) %v have no binding or matching call-site expression, and call-site value(s) %v are claimed by no slot",
			unsatisfied, unclaimed)
	case len(unsatisfied) > 0:
		return fmt.Sprintf("slot(s) %v have no binding and no identically-spelled call-site expression", unsatisfied)
	case len(unclaimed) > 0:
		return fmt.Sprintf("call-site value(s) %v would be claimed by no slot and silently dropped", unclaimed)
	default:
		return ""
	}
}
