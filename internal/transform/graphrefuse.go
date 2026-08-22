package transform

import (
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// THE GRAPH AXIS'S REFUSAL — by name, never by dropping (P34 task 5.8, FR16)
// ─────────────────────────────────────────────────────────────────────────
//
// A spec may declare that two calls run concurrently, that an edge is taken only when a predicate
// holds, and how a fan-in combines. All three are HASHED — `config_hash` already records the topology
// — and none of them has a call-site codemod in any language yet.
//
// 🔴 That gap must be a REFUSAL and never a silent no-op, and the reason is the same one `refuseWiring`
// gives one file over: a no-op would let this spec's `config_hash`, which already records the new
// topology, be scored against source that was never rewired. The number would be wrong, and it would
// look exactly like a number that is right — a false measurement rather than a missing feature.
//
// 🚫 It is deliberately NOT a resolve-time refusal. A graph declaration RESOLVES cleanly, HASHES
// stably, is validated against the typed contracts, and can be proposed and compared — all of which is
// entirely correct and none of which needs a codemod. Blocking at resolve would throw that away, which
// is the same correction the memory and harness axes each had to make once (P17 D4, P18 D-4).

// checkGraphTopology refuses a spec whose topology this language cannot materialize.
//
// It runs BEFORE any file is read or rewritten, for the reason `checkGroupHarness` does: a topology
// spans nodes, so dispatching it from whichever node came first in the edit loop would make the refusal
// depend on iteration order — and deciding early is what makes it impossible to leave a partial diff
// behind for a spec that was going to be refused.
func checkGraphTopology(r *variantspec.Resolved) error {
	forms := declaredGraphForms(r)
	if len(forms) == 0 {
		return nil
	}
	var unsupported []string
	for _, f := range forms {
		if !GraphFormMaterializesIn(r.Language, f) {
			unsupported = append(unsupported, f)
		}
	}
	if len(unsupported) == 0 {
		return nil
	}
	return refuseGraph(firstGraphNode(r), r.Language, unsupported)
}

// declaredGraphForms lists the topology forms this resolved configuration actually declares, sorted.
//
// 🔴 Read from the HASHED projection rather than from the authored spec, because the hash is what a
// measurement is filed under: if a form reaches `config_hash` it must reach this check, and reading the
// spec would let a form that was projected but not authored (or vice versa) slip past.
func declaredGraphForms(r *variantspec.Resolved) []string {
	seen := map[string]bool{}
	for _, g := range r.Config.GraphGroups {
		if g.Concurrent {
			seen["concurrent group"] = true
		}
		if g.Merge != nil {
			seen["merge"] = true
		}
	}
	for _, e := range r.Config.Edges {
		if e.Kind == variantspec.EdgeKindPredicate {
			seen["conditional edge"] = true
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// firstGraphNode names a node the refusal can anchor to. A topology is a property BETWEEN nodes, so
// there is no single owner — but a `RewriteError` with no node is one a reader cannot navigate to, so
// the earliest node in the declared order is used and the message says the scope is the group.
func firstGraphNode(r *variantspec.Resolved) string {
	for _, g := range r.Config.GraphGroups {
		if len(g.Nodes) > 0 {
			return g.Nodes[0]
		}
	}
	for _, e := range r.Config.Edges {
		if e.Kind == variantspec.EdgeKindPredicate {
			return e.FromNodeID
		}
	}
	return ""
}

// refuseGraph is the typed refusal: an ErrUnsafeRewrite naming the graph AXIS and the specific FORMS,
// so a reader learns which topology was asked for rather than only that something was refused.
//
// 🔴 `CauseNoMaterializer` is correct here and `CauseNotAtCallSite` would not be. Concurrency and
// conditional routing ARE expressible at a call site — that is exactly what makes them a codemod rather
// than a deployment policy — so this names work the platform owes, and the missing artifact is named.
// Borrowing the permanent cause would tell a reader that a thing which is merely unbuilt can never be
// built, and it would spend the taxonomy's only irreversible word.
func refuseGraph(nodeID, language string, forms []string) error {
	return refuseNoMaterializer(nodeID, graphRefusalDim,
		"this spec declares %s, and no %s rewriter materializes %s as source yet. The declaration is not "+
			"dropped: it RESOLVED, it is validated against your typed I/O contracts, and it is already part "+
			"of this configuration's config_hash — which is exactly why it cannot be silently applied as a "+
			"no-op. A no-op would let this hash, which records the new topology, be scored against source "+
			"that still runs the old one: a false measurement, not a missing feature. What is missing is %s",
		quoteJoinForms(forms), languageDisplay(language), plural(forms, "it", "them"),
		missingGraphArtifact(language, forms))
}

// missingGraphArtifact names what would close the gap, distinguishing which of the three things FR18
// requires it to distinguish — the frontend, the analysis, or the language support.
func missingGraphArtifact(language string, forms []string) string {
	missing, artifact, _ := graphGap(language)
	switch missing {
	case "frontend", "analysis":
		return artifact
	default:
		return "a " + language + " topology rewriter for " + quoteJoinForms(forms) + " (graphMaterializers)"
	}
}

func quoteJoinForms(forms []string) string {
	out := make([]string, len(forms))
	for i, f := range forms {
		out[i] = fmt.Sprintf("a %s", f)
	}
	if len(out) == 1 {
		return out[0]
	}
	return strings.Join(out[:len(out)-1], ", ") + " and " + out[len(out)-1]
}

func plural[T any](xs []T, one, many string) string {
	if len(xs) == 1 {
		return one
	}
	return many
}
