package hostdiscovery

import (
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

// provenance.go derives the row-level author index migration 0045 stores (P30 tasks 2.1, 2.3).
//
// 🔴 What this is NOT: the authoritative record of who wrote a fact. That is `author` on each edge and
// each label inside the view, per D4 — "a run-level boolean cannot answer 'who authored THIS edge',
// which is the only question an incident asks". This is an INDEX over those facts so the question
// "which graphs contain something the agent wrote?" is a WHERE clause instead of a full scan of
// customer documents.
//
// It is derived in ONE place, at write time, from the document being written. A summary computed
// anywhere else — or maintained by hand as facts are added — is a summary that eventually disagrees
// with what it summarises, and a disagreeing index is worse than no index because it is believed.

// ProvenanceOf returns the canonical set of authors present in a view's facts.
//
// The empty string means the view carries NO authored fact, which is what a graph discovered before
// P30 looks like. It is stored as SQL NULL and read back as `legacy` — see AuthorOf. A view with no
// facts at all (no edges, no labels) also returns "", and that is correct: there is nothing to
// attribute, and claiming `frontend` would attribute an empty set to a frontend.
func ProvenanceOf(v patternclassifier.GraphView) string {
	seen := map[discovery.FactAuthor]bool{}
	for _, e := range v.Edges {
		if discovery.Authored(e.Author) {
			seen[discovery.AuthorOf(e.Author)] = true
		}
	}
	collect := func(regions []patternclassifier.ViewRegion) {
		for _, r := range regions {
			for _, l := range r.Labels {
				if discovery.Authored(string(l.Author)) {
					seen[l.Author] = true
				}
			}
		}
	}
	collect(v.Regions)
	collect(v.Unclassified)
	for _, n := range v.Nodes {
		for _, l := range n.Labels {
			if discovery.Authored(string(l.Author)) {
				seen[l.Author] = true
			}
		}
	}

	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, string(a))
	}
	// Sorted: the stored value is compared against a re-derivation in a fence, and an order that
	// depended on map iteration would make that fence fail at random.
	sort.Strings(out)
	return strings.Join(out, ",")
}

// HasAuthor reports whether a stored provenance index names a given author.
//
// It exists so a caller never writes `strings.Contains(p, "heros")`, which is true of a hypothetical
// future author called `heros-lite` as well. Membership is over the comma-separated SET.
func HasAuthor(stored string, a discovery.FactAuthor) bool {
	if stored == "" {
		return false
	}
	for _, part := range strings.Split(stored, ",") {
		if part == string(a) {
			return true
		}
	}
	return false
}
