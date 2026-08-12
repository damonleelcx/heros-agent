package patternclassifier

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// P30 workstream 8 — the customer surfaces' read model.

// 🔴 THE COMPOSITION IS NOT A DISPATCHER (task 8.1, spec scenario "The composition is not a dispatcher").
//
// # Why this is a source scan and not a behavioural test
//
// The property is a NEGATIVE over the whole tree — "no code path reads a Composition to select a metric
// set, a failure taxonomy or an improvement operator" — and a behavioural test can only demonstrate
// that the paths it happens to exercise do not. The one that would matter is the one somebody adds next
// year, three packages away, because dispatching off "the workflow's pattern" is genuinely convenient
// and reads like a simplification.
//
// The classifier refuses a workflow-level label because the label IS the metric-set dispatcher, and a
// graph containing both a router and a RAG pipeline needs two metric sets. A composition that became a
// dispatcher would reintroduce exactly the single-label collapse the refusal exists to prevent — and it
// would do it silently, because the composition looks like a summary.
func TestTheCompositionIsNotADispatcher(t *testing.T) {
	root := repoRootFromClassifier(t)
	// The functions that DISPATCH. A Composition reaching any of them is the failure.
	dispatchers := []string{"MetricSetFor", "FailureTaxonomyFor", "ImprovementOperatorsFor", "OperatorsFor"}

	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() {
				switch info.Name() {
				case "node_modules", ".git", "vendor", ".claude":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, src, 0)
		if perr != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			}
			isDispatch := false
			for _, d := range dispatchers {
				if name == d {
					isDispatch = true
				}
			}
			if !isDispatch {
				return true
			}
			// Its argument must not come off a Composition.
			for _, arg := range call.Args {
				if mentionsComposition(arg) {
					rel, _ := filepath.Rel(root, path)
					offenders = append(offenders, rel+":"+fset.Position(call.Pos()).String()+" -> "+name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("a Composition is being used to DISPATCH:\n  %s\n\n"+
			"  The classifier refuses a workflow-level label because the label IS the metric-set "+
			"dispatcher, and a graph containing both a router and a RAG pipeline needs two metric sets. "+
			"A composition that dispatches reintroduces that collapse — silently, because it looks like "+
			"a summary. The per-region labels remain the only dispatcher.",
			strings.Join(offenders, "\n  "))
	}
}

// mentionsComposition reports whether an expression reads off a Composition value.
func mentionsComposition(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.SelectorExpr:
			if v.Sel.Name == "Composition" || v.Sel.Name == "Patterns" {
				// `x.Composition.…` or a `.Patterns` read, which on this surface only a Composition has.
				found = true
			}
		case *ast.Ident:
			if strings.Contains(strings.ToLower(v.Name), "composition") {
				found = true
			}
		}
		return true
	})
	return found
}

func repoRootFromClassifier(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// A single-pattern workflow is still a COMPOSITION — one pattern with its coverage and the remainder —
// and 🚫 not a workflow-level label restated. The distinction is what keeps the two-metric-set case
// from silently losing one the day somebody reads "the workflow's pattern".
func TestASinglePatternWorkflowIsACompositionOfOne(t *testing.T) {
	gv := viewWithLabels(t,
		labelled("sg1", []string{"a", "b"}, Routing, discovery.AuthorDetector, "det.router"),
	)
	gv.Nodes = append(gv.Nodes, ViewNode{NodeID: "c"})

	c := buildComposition(gv)
	if len(c.Patterns) != 1 {
		t.Fatalf("patterns are %+v, want exactly one", c.Patterns)
	}
	if c.Patterns[0].Nodes != 2 || c.Patterns[0].Regions != 1 {
		t.Errorf("the one pattern reports %d nodes over %d regions, want 2 over 1",
			c.Patterns[0].Nodes, c.Patterns[0].Regions)
	}
	// The REMAINDER is the point: a workflow-level label would have nothing to say about node `c`.
	if c.UnlabelledRemainder != 1 {
		t.Errorf("remainder is %d, want 1 — a composition names what was NOT covered, which is what a "+
			"workflow-level label cannot do", c.UnlabelledRemainder)
	}
}

// 🔴 TASK 8.4 — a mixed count reports the TOTAL and the INFERRED PORTION.
func TestAMixedCompositionReportsBothNumbers(t *testing.T) {
	gv := viewWithLabels(t,
		labelled("sg1", []string{"a", "b"}, Routing, discovery.AuthorDetector, "det.router"),
		labelled("sg2", []string{"c"}, ToolUse, discovery.AuthorHEROS, "heros-agent"),
	)
	gv.Edges = []ViewEdge{
		{From: "a", To: "b", Kind: "data"},
		{From: "b", To: "c", Kind: "data", Author: string(discovery.AuthorHEROS), Confidence: 0.9},
	}

	c := buildComposition(gv)
	if c.NodesCovered != 3 {
		t.Errorf("nodes covered is %d, want 3", c.NodesCovered)
	}
	if c.NodesCoveredInferred != 1 {
		t.Errorf("inferred portion is %d, want 1 — a page rendering only the total would present a "+
			"model's proposal as a reading of the source", c.NodesCoveredInferred)
	}
	if c.EdgesTotal != 2 || c.EdgesInferred != 1 {
		t.Errorf("edges are %d total / %d inferred, want 2 / 1", c.EdgesTotal, c.EdgesInferred)
	}
}

// 🔴 A node covered by BOTH a rule label and an agent label is measured, and is not counted as
// inferred. Counting it in both would make the parts sum past the whole — which is the arithmetic a
// reader uses to decide how much of the page to trust.
func TestANodeCoveredByBothIsMeasuredNotInferred(t *testing.T) {
	gv := viewWithLabels(t,
		labelled("sg1", []string{"a"}, Routing, discovery.AuthorDetector, "det.router"),
		labelled("sg2", []string{"a"}, ToolUse, discovery.AuthorHEROS, "heros-agent"),
	)
	c := buildComposition(gv)
	if c.NodesCovered != 1 {
		t.Errorf("nodes covered is %d, want 1 — the same node twice is one node", c.NodesCovered)
	}
	if c.NodesCoveredInferred != 0 {
		t.Errorf("inferred portion is %d, want 0 — a rule detector read this node out of the source, "+
			"and the agent's agreement does not make it a hypothesis", c.NodesCoveredInferred)
	}
	if c.NodesCovered+c.UnlabelledRemainder != c.NodesTotal {
		t.Errorf("covered %d + remainder %d != total %d — the parts must sum to the whole",
			c.NodesCovered, c.UnlabelledRemainder, c.NodesTotal)
	}
}

// A pattern is `inferred` only when EVERY contributing label came from the agent. One rule detector
// among three agent proposals means a detector read the source and found this pattern, and calling the
// whole thing inferred would understate what is established.
func TestAPatternIsInferredOnlyWhenEveryAuthorIsTheAgent(t *testing.T) {
	only := buildComposition(viewWithLabels(t,
		labelled("sg1", []string{"a"}, ToolUse, discovery.AuthorHEROS, "heros-agent"),
	))
	if only.Patterns[0].State != StateInferred {
		t.Errorf("a pattern only the agent proposed is %q, want %q", only.Patterns[0].State, StateInferred)
	}

	mixed := buildComposition(viewWithLabels(t,
		labelled("sg1", []string{"a"}, ToolUse, discovery.AuthorHEROS, "heros-agent"),
		labelled("sg2", []string{"b"}, ToolUse, discovery.AuthorDetector, "det.rag"),
	))
	if mixed.Patterns[0].State != StateMeasured {
		t.Errorf("a pattern one detector established is %q, want %q — the asymmetry runs toward the "+
			"stronger claim being harder to lose, not easier to make",
			mixed.Patterns[0].State, StateMeasured)
	}
}

// An absent author reads as `legacy` and is NEVER promoted to `frontend`. That would assert something
// about labels nobody examined, and it is the distinction discovery/author.go exists to create.
func TestAnAbsentAuthorReadsAsLegacyAndIsNotPromoted(t *testing.T) {
	gv := viewWithLabels(t, labelled("sg1", []string{"a"}, Routing, "", "det.router"))
	c := buildComposition(gv)
	authors := c.Patterns[0].Authors
	if len(authors) != 1 || authors[0] != discovery.AuthorLegacy {
		t.Errorf("authors are %v, want [%s]", authors, discovery.AuthorLegacy)
	}
	// It is still MEASURED: legacy is "we did not record who", not "a model wrote it".
	if c.Patterns[0].State != StateMeasured {
		t.Errorf("a legacy-authored pattern is %q, want %q", c.Patterns[0].State, StateMeasured)
	}
}

// 🔴 TASK 8.5 — four states, four sentences, and none of them is a fallback for another.
func TestTheFourFactStatesAreDistinctAndEachCarriesItsOwnSentence(t *testing.T) {
	states := FactStates()
	if len(states) != 4 {
		t.Fatalf("there are %d fact states, want 4", len(states))
	}
	seen := map[string]bool{}
	for _, s := range states {
		sentence := SentenceForState(s)
		if sentence == "" {
			t.Errorf("%s has no sentence — a state with no sentence is a chip a reader has to interpret", s)
		}
		if seen[sentence] {
			t.Errorf("%s reuses another state's sentence", s)
		}
		seen[sentence] = true
	}
	// 🚫 No generic fallback: an unrecognised state must render NOTHING rather than a plausible
	// paragraph, which is how a fifth state ships looking like one of the four.
	if SentenceForState("something_else") != "" {
		t.Error("an unknown state resolved to a sentence")
	}
	// The two that look alike must not read alike. `not_analysed` is work outstanding; `unavailable`
	// is a fault, and a reader deciding whether to wait or to ask somebody needs to know which.
	if !strings.Contains(SentenceForState(StateNotAnalysed), "default") {
		t.Error("`not_analysed` does not say that the absence is a setting")
	}
	if !strings.Contains(SentenceForState(StateUnavailable), "our side") {
		t.Error("`unavailable` does not say the fault is ours")
	}
}

// The two constructors keep state, action and reason consistent. A panel reading `unavailable` while
// offering the analyse action is the inconsistency they exist to prevent.
func TestTheAgentPanelConstructorsCannotDisagreeWithThemselves(t *testing.T) {
	bad := AgentUnavailable("the store was unreachable")
	if bad.State != StateUnavailable || bad.Action != ActionNone || bad.ActionReason == "" {
		t.Errorf("an unavailable panel is %+v", bad)
	}
	if bad.Failure == "" {
		t.Error("an unavailable panel carries no failure to render")
	}
	// 🚫 And it never carries a narrative: prose from an agent that could not be reached is prose from
	// somewhere else.
	if bad.Narrative != "" {
		t.Error("an unavailable panel carries a narrative")
	}

	off := AgentNotAnalysed("disabled", "an operator has not enabled analysis")
	if off.State != StateNotAnalysed || off.Failure != "" {
		t.Errorf("a not-analysed panel is %+v — the DEFAULT state must not carry a failure, or every "+
			"customer's first visit reports a deliberate configuration as a problem", off)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

func labelled(subgraphID string, nodeIDs []string, p Pattern, author discovery.FactAuthor, provenance string) ViewRegion {
	return ViewRegion{
		SubgraphID: subgraphID, NodeIDs: nodeIDs,
		Labels: []ViewLabel{{
			Pattern: p, Confidence: 0.9, Source: SourceRule,
			Provenance: provenance, Author: author,
			Ordinal: ordinalOf(p), Title: titleOf(p),
		}},
	}
}

func ordinalOf(p Pattern) int {
	if info, ok := Info(p); ok {
		return info.Ordinal
	}
	return 0
}

func titleOf(p Pattern) string {
	if info, ok := Info(p); ok {
		return info.Title
	}
	return ""
}

// viewWithLabels builds a minimal GraphView whose node set is the union of the regions' nodes.
func viewWithLabels(t *testing.T, regions ...ViewRegion) GraphView {
	t.Helper()
	gv := GraphView{Regions: regions, Nodes: []ViewNode{}, Edges: []ViewEdge{}}
	seen := map[string]bool{}
	for _, r := range regions {
		for _, id := range r.NodeIDs {
			if !seen[id] {
				seen[id] = true
				gv.Nodes = append(gv.Nodes, ViewNode{NodeID: id})
			}
		}
	}
	return gv
}
