package transform

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// The interim refusal for an un-materializable harness (P18 §5, decisions.md D-4)
// ───────────────────────────────────────────────────────────────────────────────
//
// The harness axis is fully MODELED and fully HASHED: selecting a scaffold produces a Variant Spec that
// resolves to a different `config_hash`. What a call site can be given is a bounded loop around the call
// the author already wrote — and only when the generated runtime can both DRIVE the call and DECIDE
// whether to run again (harnessmaterialize.go, decisions.md D-9). Everywhere else the honest outcome is a
// REFUSAL.
//
// 🔴 The alternative is the worst failure this system has. Silently no-op'ing a harness override would
// let the engine "succeed": the diff would rewrite the node CONTENTS, the build would pass, the eval
// would run — and the score would be attributed to a `config_hash` claiming a scaffold the source never
// had. That is a FALSE MEASUREMENT, not a missing feature, and it would poison the verified-delta ledger
// every later decision reads.
//
// This is the same refuse-until-safe shape as refuseSkills (P14) and refuseContext (P16): modeled,
// resolvable, hashable, materialization stated. What P18 §11 changed is the SCOPE of the refusal, not its
// existence — it is narrowed per `(language, strategy, call-shape)` cell and never lifted wholesale
// (decisions.md D-11).

// refuseHarness is the typed refusal for a node-scoped harness override this cell cannot materialize.
//
// It names the STRATEGY, because "a harness was refused" sends the reader nowhere: which loop was asked
// for is the first thing they need, and the second is whether the missing piece is ours or theirs — which
// is why every caller reaches for a classified constructor rather than this one when it knows.
func refuseHarness(nodeID, language string, o variantspec.ResolvedOverride) error {
	strategy := "the resolved strategy"
	if o.Harness != nil {
		strategy = strconv.Quote(o.Harness.Spec.Strategy)
	}
	return refuseNoMaterializer(nodeID, string(variantspec.DimHarness),
		"a harness is a control LOOP around this call, not an argument to it — materializing strategy %s "+
			"means emitting a bounded turn loop with a stop condition, which is code generation rather than "+
			"the value replacement this engine performs. No %s harness materializer has landed, so the "+
			"override is REFUSED rather than dropped: applying it as the base configuration would score a "+
			"scaffold that never ran (covered today: %s)",
		strategy, languageDisplay(language), harnessMaterializerDisplay())
}

// refuseGroupHarness is the typed refusal for a harness scoped to an ORDERED EDGE SET (P18 task 5.3).
//
// 🔴 It names the edge set, and that is not decoration. A group harness is refused for a strictly larger
// reason than a node one — the loop spans calls in (potentially) several files, so the emitted control
// flow would have to move code between them — and a reader who is told only "your harness was refused"
// cannot tell which of their two harnesses it was. The edge set is the group's identity, so it is what
// the sentence carries.
//
// 🚫 The refusal is NOT "we cannot rewire that". The group composes with P15's wiring and never reorders
// (decisions.md D-5); what is refused is the LOOP over edges the spec already declares.
func refuseGroupHarness(g variantspec.ResolvedGroupOverride) error {
	strategy := "the resolved strategy"
	if g.Entry != nil {
		strategy = strconv.Quote(g.Entry.Spec.Strategy)
	}
	return refuseNoMaterializer(firstNodeOf(g), string(variantspec.DimHarness),
		"harness strategy %s wraps the ordered edge set %s. Materializing a group harness means emitting a "+
			"control loop AROUND several calls — code that spans the statements between them, and often "+
			"several files — which is strictly more structural than the single-call loop this engine "+
			"materializes. It is REFUSED rather than dropped: a no-op would let this spec's config_hash, "+
			"which already records the group, be scored against source that was never wrapped. 🚫 This is "+
			"not a refusal to REWIRE — the edge set is the one the spec already declares, and a harness "+
			"never reorders (P15 owns wiring)",
		strategy, edgeSetDisplay(g.Edges))
}

// firstNodeOf names a node for the refusal's NodeID field. A group spans several, so the first edge's
// source is used — the RewriteError shape carries one node, and the edge set in the detail is what
// actually identifies the group.
func firstNodeOf(g variantspec.ResolvedGroupOverride) string {
	if len(g.Edges) == 0 {
		return ""
	}
	return g.Edges[0].FromNodeID
}

// edgeSetDisplay renders an ordered edge set the way the author wrote it, in order. Order is preserved
// rather than sorted because it is identity-bearing: a loop over a→b→c is not a loop over c→b→a, and a
// refusal that sorted them would name a set the author never declared.
func edgeSetDisplay(edges []variantspec.ResolvedEdge) string {
	if len(edges) == 0 {
		return "(no edges)"
	}
	parts := make([]string, 0, len(edges))
	for _, e := range edges {
		parts = append(parts, fmt.Sprintf("%s->%s", e.FromNodeID, e.ToNodeID))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// checkGroupHarness refuses every group harness a resolved spec carries.
//
// It runs in generate() rather than in the per-node loop because a group is not a node's fact: its scope
// is a set of edges, and dispatching it from whichever node happened to be first would make the refusal's
// identity depend on iteration order. Placed immediately after the wiring check, so a refused group can
// never leave a partial diff behind — nothing has been read or rewritten at that point.
func checkGroupHarness(r *variantspec.Resolved) error {
	for _, g := range r.HarnessGroups {
		return refuseGroupHarness(g)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// The materializer set — the one table the refusal, the coverage read and the rewriters all read
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// harnessMaterializers is the set of languages with an emitted harness module and a call-site rewriter.
//
// 🔴 It is a READ, never a second table: the coverage read (coverage.go) and both dispatch tables derive
// from this map, so a claim about what harness can do and the engine's behaviour cannot disagree. A
// hand-maintained copy would be exactly the drift the coverage file was written to end — and the copy is
// always the optimistic one.
var harnessMaterializers = map[string]bool{
	// Python materializes the loop itself. Go is here too, and its entry means something narrower that
	// the per-cell read states precisely: Go's engine ANSWERS for the harness dimension — it materializes
	// the identity and refuses every multi-turn strategy with a PERMANENT cause — rather than having no
	// answer at all.
	//
	// 🔴 The distinction matters because of which cause class each produces. A language absent from this
	// table refuses with `CauseNoMaterializer`, which says the platform owes work. Go's refusal owes
	// nothing: deciding whether to take another turn means reading the ANSWER's text, and a Go response
	// is the customer's own static type, so a generated module would have to import their SDK to read a
	// field off it. Listing Go here is what routes its cells to the permanent cause instead of a promise.
	"python": true,
	"go":     true,
}

// HasHarnessMaterializer reports whether this language has an emitted harness module and a rewriter.
func HasHarnessMaterializer(language string) bool {
	return harnessMaterializers[strings.ToLower(strings.TrimSpace(language))]
}

// HarnessMaterializerLanguages lists the covered languages, sorted, for a refusal that tells the reader
// what WOULD have worked.
func HarnessMaterializerLanguages() []string {
	out := make([]string, 0, len(harnessMaterializers))
	for l := range harnessMaterializers {
		out = append(out, l)
	}
	sortStrings(out)
	return out
}

func harnessMaterializerDisplay() string {
	got := HarnessMaterializerLanguages()
	if len(got) == 0 {
		return "none"
	}
	return strings.Join(got, ", ")
}
