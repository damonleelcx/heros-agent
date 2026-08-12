package patternclassifier

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// composition.go is P30 §8.1: what IS this workflow, answered without breaking the thing that made the
// question hard.
//
// # Why the classifier refuses a workflow-level label, and why this is not one
//
// The classifier emits per-subgraph labels and never one label for the whole workflow, because the
// label is the METRIC-SET DISPATCHER: a graph containing both a router and a RAG pipeline needs two
// metric sets, and a single "this is a RAG workflow" would silently pick one. That refusal is correct
// and it left a real question unanswered — a reader opening the page still wants to know what they are
// looking at.
//
// A composition answers it by ENUMERATING rather than collapsing. It reports every pattern present with
// the nodes it covers and the remainder nothing covered, so a two-pattern workflow reads as two
// patterns. A single-pattern workflow reports one pattern and its coverage, which is a composition of
// one — 🚫 NOT a workflow-level label restated, and the distinction is load-bearing rather than
// pedantic: the moment something dispatches off "the workflow's pattern", the two-metric-set case
// silently loses one.
//
// 🔴 NOTHING READS THIS TO DISPATCH. `TestTheCompositionIsNotADispatcher` asserts it structurally: no
// metric set, failure taxonomy or improvement operator is selected from a Composition anywhere in the
// tree. The per-region labels remain the only dispatcher.

// FactState is the closed vocabulary every P30 customer surface uses for "how do we know this"
// (task 8.5).
//
// 🔴 FOUR VALUES, and the two that look alike are the reason it exists. `not_analysed` is "we have not
// looked"; `unavailable` is "we looked and could not". Both render as an absence and they are opposite
// facts about the platform — one is work outstanding, the other is a fault or a policy — and a reader
// deciding whether to wait or to ask somebody needs to know which. Collapsing them was the P29 failure
// this vocabulary is modelled on: `not-reported` and `not-applicable` had to stay distinct because one
// is a claim about the customer's code and the other is a claim about our own coverage.
type FactState string

const (
	// StateMeasured: a language frontend or a rule detector established this by reading the source. The
	// strongest thing any of these surfaces can say.
	StateMeasured FactState = "measured"
	// StateInferred: the analysis agent proposed it and it cleared the confidence floor. A HYPOTHESIS
	// that survived a threshold — never rendered with the same weight as a measured fact.
	StateInferred FactState = "inferred"
	// StateNotAnalysed: nothing has looked. On a fresh deployment this is every workflow, because Q2
	// makes `disabled` the default placement, and it must not read as a finding.
	StateNotAnalysed FactState = "not_analysed"
	// StateUnavailable: something looked and could not answer — the agent failed, its credential does
	// not resolve, a cap was reached. 🚫 Never collapsed into `not_analysed`: one is work outstanding
	// and the other is a fault, and they send a reader to different people.
	StateUnavailable FactState = "unavailable"
)

// factStateSentences is what a reader gets for each state. Resolved here, like every other sentence in
// this package, so two consoles cannot word them differently and a test can assert the exact string.
var factStateSentences = map[FactState]string{
	StateMeasured: "Established by reading the source. A language frontend or a rule detector produced " +
		"this, and it is the strongest statement this platform makes.",
	StateInferred: "Proposed by the analysis agent and above its confidence floor. It is a hypothesis " +
		"about your workflow, not a reading of it — treat it as a lead rather than a finding.",
	StateNotAnalysed: "Nothing has looked at this. Analysis is off for this organization, which is the " +
		"default, so the absence is a setting rather than a result.",
	StateUnavailable: "Something looked and could not answer. This is a fault or a limit on our side, " +
		"not a statement about your workflow — and it is deliberately not shown as `nothing found`.",
}

// SentenceForState returns the reader-facing sentence for a state, or "" for an unknown one.
//
// 🚫 No generic fallback, for the reason SentenceFor has none: an unrecognised state rendering as a
// plausible paragraph is how a fifth state ships looking like one of the four.
func SentenceForState(s FactState) string { return factStateSentences[s] }

// FactStates returns the closed set, so a consumer's switch can be proved exhaustive.
func FactStates() []FactState {
	return []FactState{StateMeasured, StateInferred, StateNotAnalysed, StateUnavailable}
}

// Composition is what this workflow is made of.
type Composition struct {
	// Patterns is every pattern present, with what it covers. Sorted by ordinal so the page is stable.
	Patterns []CompositionPattern `json:"patterns"`

	// NodesTotal is every node in the graph — the denominator every figure below is a part of.
	NodesTotal int `json:"nodes_total"`
	// NodesCovered is how many nodes carry at least one pattern label.
	NodesCovered int `json:"nodes_covered"`
	// 🔴 NodesCoveredInferred is the part of NodesCovered that only an INFERRED label covers (task 8.4).
	//
	// "Only": a node covered by both a rule label and an agent label is measured, and counting it here
	// would overstate what the agent contributed. The page renders `12 of 19 (3 inferred)`, never a
	// single undifferentiated number — a mixed count read as measured is the specific way a hypothesis
	// gets promoted to a fact by arithmetic.
	NodesCoveredInferred int `json:"nodes_covered_inferred"`
	// UnlabelledRemainder is NodesTotal - NodesCovered, carried explicitly rather than left as a
	// subtraction. The remainder is the number a reader actually acts on, and a page that computes it
	// gets to compute it differently from the platform.
	UnlabelledRemainder int `json:"unlabelled_remainder"`

	// EdgesTotal and EdgesInferred are the same split for topology (task 8.4).
	EdgesTotal    int `json:"edges_total"`
	EdgesInferred int `json:"edges_inferred"`
}

// CompositionPattern is one pattern present in the workflow, and what it covers.
type CompositionPattern struct {
	Pattern Pattern `json:"pattern"`
	Ordinal int     `json:"ordinal"`
	Title   string  `json:"title"`
	Group   Group   `json:"group"`

	// Regions is how many labelled subgraphs carry this pattern. Two regions of one pattern is a real
	// and different shape from one — a workflow with two independent routers is not a workflow with one.
	Regions int `json:"regions"`
	// Nodes is how many distinct nodes this pattern's regions cover.
	Nodes int `json:"nodes"`

	// Provenance is every exact producer that contributed a label of this pattern — detector ids and
	// model run refs. Per-pattern rather than per-label, because the composition's question is "who says
	// this workflow contains a router", and a pattern established by three detectors and one established
	// by a single model run are different answers.
	Provenance []string `json:"provenance"`
	// Authors is who wrote them, from discovery.FactAuthor. 🔴 NOT derivable from Provenance or from
	// Source: P30's agent emits through the rule layer by design (D3, one arbitration path), so a heros
	// label reads `source: rule` and looks exactly like a detector's from every other field.
	Authors []discovery.FactAuthor `json:"authors"`
	// State is how this pattern is known, resolved from Authors. `measured` when any author established
	// it by reading source; `inferred` only when EVERY label of this pattern came from the agent.
	State FactState `json:"state"`
	// Candidate marks a pattern where every contributing label is a behavioral candidate structure could
	// not confirm. A candidate rendered like a confirmed label is a false claim about what is known.
	Candidate bool `json:"candidate"`
}

// buildComposition computes the composition from the view's own regions and edges.
//
// It takes the assembled view rather than the raw Result on purpose: everything it needs has already
// been resolved once (ordinals, titles, authors), and re-deriving them from `Result` would be the second
// implementation this package's header warns about — one that drifts the first time a label's author is
// resolved differently in the two places.
func buildComposition(gv GraphView) Composition {
	c := Composition{
		Patterns:   []CompositionPattern{},
		NodesTotal: len(gv.Nodes),
		EdgesTotal: len(gv.Edges),
	}
	for _, e := range gv.Edges {
		if e.Author == string(discovery.AuthorHEROS) {
			c.EdgesInferred++
		}
	}

	type acc struct {
		regions    int
		nodes      map[string]bool
		provenance map[string]bool
		authors    map[discovery.FactAuthor]bool
		candidate  bool
		anyLabel   bool
	}
	byPattern := map[Pattern]*acc{}

	// coveredBy tracks, per node, whether ANY non-agent label covers it. A node covered by both a rule
	// label and an agent label is measured — see NodesCoveredInferred.
	coveredMeasured := map[string]bool{}
	coveredInferred := map[string]bool{}

	for _, r := range gv.Regions {
		for _, l := range r.Labels {
			a := byPattern[l.Pattern]
			if a == nil {
				a = &acc{
					nodes: map[string]bool{}, provenance: map[string]bool{},
					authors: map[discovery.FactAuthor]bool{}, candidate: true,
				}
				byPattern[l.Pattern] = a
			}
			a.regions++
			a.anyLabel = true
			if !l.Candidate {
				a.candidate = false
			}
			if l.Provenance != "" {
				a.provenance[l.Provenance] = true
			}
			// An absent author reads as `legacy` — the fact predates authorship being recorded. It is
			// NEVER promoted to `frontend`: that would assert something about labels nobody examined, and
			// it is the distinction discovery/author.go exists to create.
			author := l.Author
			if author == "" {
				author = discovery.AuthorLegacy
			}
			a.authors[author] = true

			for _, id := range r.NodeIDs {
				a.nodes[id] = true
				if author == discovery.AuthorHEROS {
					coveredInferred[id] = true
				} else {
					coveredMeasured[id] = true
				}
			}
		}
	}

	c.NodesCovered = len(coveredMeasured)
	for id := range coveredInferred {
		// Only the nodes NOTHING measured covers count as inferred. A node carrying both a rule label
		// and an agent label is measured, and counting it in both would make the parts sum past the
		// whole — which is the arithmetic a reader uses to decide how much of this page to trust.
		if !coveredMeasured[id] {
			c.NodesCovered++
			c.NodesCoveredInferred++
		}
	}
	c.UnlabelledRemainder = c.NodesTotal - c.NodesCovered
	if c.UnlabelledRemainder < 0 {
		// Defensive, and it can only fire if a region names a node the graph does not carry — in which
		// case the remainder is not knowable and reporting a negative number would be worse than
		// reporting none.
		c.UnlabelledRemainder = 0
	}

	for pattern, a := range byPattern {
		cp := CompositionPattern{
			Pattern: pattern, Regions: a.regions, Nodes: len(a.nodes),
			Provenance: keysOf(a.provenance), Candidate: a.candidate && a.anyLabel,
		}
		if info, ok := Info(pattern); ok {
			cp.Title, cp.Group, cp.Ordinal = info.Title, info.Group, info.Ordinal
		}
		for author := range a.authors {
			cp.Authors = append(cp.Authors, author)
		}
		sort.Slice(cp.Authors, func(i, j int) bool { return cp.Authors[i] < cp.Authors[j] })
		cp.State = stateOfAuthors(a.authors)
		c.Patterns = append(c.Patterns, cp)
	}
	sort.SliceStable(c.Patterns, func(i, j int) bool {
		if c.Patterns[i].Ordinal != c.Patterns[j].Ordinal {
			return c.Patterns[i].Ordinal < c.Patterns[j].Ordinal
		}
		return c.Patterns[i].Pattern < c.Patterns[j].Pattern
	})
	return c
}

// stateOfAuthors resolves how a pattern is known.
//
// 🔴 `inferred` requires that EVERY contributing author is the agent. One rule detector among three
// agent proposals still means a detector read the source and found this pattern, and calling the whole
// thing inferred would understate what is actually established. The asymmetry is deliberate and runs
// toward the stronger claim being harder to lose, not easier to make.
func stateOfAuthors(authors map[discovery.FactAuthor]bool) FactState {
	if len(authors) == 0 {
		return StateNotAnalysed
	}
	for author := range authors {
		if author != discovery.AuthorHEROS {
			return StateMeasured
		}
	}
	return StateInferred
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RebuildComposition recomputes a view's composition after something changed its authorship.
//
// Exported for one caller — `cmd/proof/customerconsole`, which stamps a labelled synthetic inference so
// the P30 §8 treatments can be looked at. It exists so that stamping cannot leave the page drawing an
// inferred edge above a composition reporting nothing inferred, which is the exact inconsistency §8 is
// about. 🚫 It is not an alternative construction path: `BuildGraphView` still computes the composition
// for every real view, and this only recomputes one that is already built.
func RebuildComposition(gv GraphView) Composition { return buildComposition(gv) }
