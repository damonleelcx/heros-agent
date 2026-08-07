package transform

import (
	"errors"
	"sort"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// probe.go asks the engine "what would you do at this call site for this dimension?" for MANY
// (node, dimension) pairs, indexing the tree ONCE.
//
// # Why this exists, and it is not a micro-optimisation
//
// The obvious way to answer per-node applicability is to call `Generate` once per (node, axis) with a
// one-node spec. It is correct, it is what the first implementation did, and on a real repository it is
// unusable: `Generate` indexes the whole tree on every call, so 27 nodes × 7 axes is 189 full-tree
// parses. Against nousresearch/hermes-agent — 8200 files — `heros link --with-ir` had not finished after
// ten minutes and was killed.
//
// That is not a level-8 implementation-cost problem to be traded away. A command that hangs is a level-3
// user-complexity failure and a level-2 stability one: a developer who has to abandon `--with-ir` gets
// the same empty console this phase exists to fix, and reaches it by a longer road.
//
// # What it does NOT do, deliberately
//
// It does not apply anything, gate anything, or produce a diff. `Generate` remains the ONE path that
// writes a patch, with its wiring check, its group-harness check, its minimality gate and its
// fail-closed behaviour intact. This function calls `site.rewrite` and throws the edits away: it answers
// "would there be an edit, or a refusal, and which class" and nothing else.
//
// 🔴 Sharing `site.rewrite` with `Generate` rather than reimplementing the question is the whole point.
// A second answer to "does this axis apply here" is a second thing that can be wrong, and it would be
// wrong in the direction that matters — a projection promising a change the engine then refuses.

// ProbeRequest is one question: what does `Dim` do at `NodeID`, given this override?
type ProbeRequest struct {
	NodeID   string
	Dim      string
	Override variantspec.ResolvedOverride
}

// ProbeOutcome is the answer.
type ProbeOutcome struct {
	NodeID string
	Dim    string
	// Applies is true when the engine produced at least one edit for this pair.
	Applies bool
	// Cause is set when the engine REFUSED — one of the three classes. Empty otherwise.
	Cause CauseClass
	// Undecided is true when the engine neither applied nor refused: the node is not in this tree, the
	// language has no engine, or the rewriter returned no edits without objecting (an identity strategy,
	// a selection that retained everything).
	//
	// 🔴 It is a THIRD outcome rather than a default, because collapsing it into either of the other two
	// is a fabricated claim. Reported as undecided, it becomes an ABSENT verdict, which renders
	// `not-reported` — the honest state.
	Undecided bool
	// Why names the undecided reason, for the caller's own diagnostics. It is never transmitted anywhere:
	// see internal/runlink, where a verdict carries an identifier and no sentence.
	Why string
}

// ProbeNodeDimensions answers every request against the tree at root, indexing once per call.
//
// The requests may name any number of nodes and dimensions. Results are returned in a stable order
// (node, then dimension) so two runs over one tree produce byte-comparable output.
func ProbeNodeDimensions(language, root string, reqs []ProbeRequest) ([]ProbeOutcome, error) {
	out := make([]ProbeOutcome, 0, len(reqs))
	eng, err := engineFor(language)
	if err != nil {
		// A language with no engine is not a refusal about anybody's call site. Every request is
		// undecided, named, and the caller transmits nothing for them.
		for _, r := range reqs {
			out = append(out, ProbeOutcome{NodeID: r.NodeID, Dim: r.Dim, Undecided: true,
				Why: "no-rewrite-engine-for-this-language"})
		}
		return out, nil
	}
	// ONCE. This is the line the whole file exists for.
	sites, err := eng.index(root)
	if err != nil {
		return nil, err
	}

	// The source of each touched file is read once too. A 27-node repository has its call sites spread
	// over a handful of files, and re-reading one per probe is the same waste one level down.
	srcCache := map[string][]byte{}

	sorted := append([]ProbeRequest(nil), reqs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].NodeID != sorted[j].NodeID {
			return sorted[i].NodeID < sorted[j].NodeID
		}
		return sorted[i].Dim < sorted[j].Dim
	})

	for _, req := range sorted {
		site, ok := sites[req.NodeID]
		if !ok {
			out = append(out, ProbeOutcome{NodeID: req.NodeID, Dim: req.Dim, Undecided: true,
				Why: "no-call-site-with-this-node-id-in-this-tree"})
			continue
		}
		src, ok := srcCache[site.fileRel]
		if !ok {
			b, rerr := readFile(root, site.fileRel)
			if rerr != nil {
				out = append(out, ProbeOutcome{NodeID: req.NodeID, Dim: req.Dim, Undecided: true,
					Why: "could-not-read-the-file-the-call-site-is-in"})
				continue
			}
			src = b
			srcCache[site.fileRel] = b
		}

		edits, rerr := site.rewrite(variantspec.Dimension(req.Dim), src, req.Override)
		if rerr != nil {
			var re *RewriteError
			if errors.As(rerr, &re) && errors.Is(re.Err, ErrUnsafeRewrite) && re.Cause.Valid() {
				out = append(out, ProbeOutcome{NodeID: req.NodeID, Dim: req.Dim, Cause: re.Cause})
				continue
			}
			// Anything that is not a classified refusal is this function failing to ask the question.
			out = append(out, ProbeOutcome{NodeID: req.NodeID, Dim: req.Dim, Undecided: true,
				Why: "engine-error-that-is-not-a-classified-refusal"})
			continue
		}
		if len(edits) == 0 {
			out = append(out, ProbeOutcome{NodeID: req.NodeID, Dim: req.Dim, Undecided: true,
				Why: "the-rewriter-produced-no-edit-and-did-not-object"})
			continue
		}
		out = append(out, ProbeOutcome{NodeID: req.NodeID, Dim: req.Dim, Applies: true})
	}
	return out, nil
}
