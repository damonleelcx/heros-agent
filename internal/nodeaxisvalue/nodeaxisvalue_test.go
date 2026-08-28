package nodeaxisvalue

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// nodeaxisvalue_test.go is P37 §5.1 and §5.3, as failing tests rather than review notes.
//
// The claim this package makes is narrow and load-bearing: it tells an editor what the reader's node
// does TODAY, so the reader can see what they are changing FROM. Every test below is about the one way
// that claim can be false — a value that was not read but supplied.

func node(mutate func(*runlink.WireIRNode)) runlink.WireIRNode {
	n := runlink.WireIRNode{
		NodeID: "answer", Symbol: "handleAnswer", File: "agent.py", Language: "python",
		Provider: "anthropic", ModelID: "claude-sonnet-4-5", ContextPolicy: "sliding-window", ToolCount: 3,
	}
	if mutate != nil {
		mutate(&n)
	}
	return n
}

func valueOf(t *testing.T, row NodeValues, axis assessment.Axis) Value {
	t.Helper()
	for _, v := range row.Values {
		if v.Axis == axis {
			return v
		}
	}
	t.Fatalf("no value for axis %q — every axis is present for every node, always", axis)
	return Value{}
}

// 🔴 §5.3, and the reason the package exists. A node whose model could not be resolved must NOT be
// rendered as a node bound to a model called "unresolved".
//
// This is not hypothetical: `discovery/extract.go` writes `UnresolvedSentinel` into BOTH `Provider` and
// `ModelID` for a detect-only declaration, and a `!= ""` check reads that as a value.
func TestTheUnresolvedSentinelIsNeverRenderedAsAValue(t *testing.T) {
	row := ForNode(node(func(n *runlink.WireIRNode) {
		n.Provider = discovery.UnresolvedSentinel
		n.ModelID = discovery.UnresolvedSentinel
		n.ContextPolicy = discovery.UnresolvedSentinel
	}))

	for _, axis := range []assessment.Axis{assessment.AxisModel, assessment.AxisContext} {
		v := valueOf(t, row, axis)
		if v.State != assessment.StateNotMeasured {
			t.Fatalf("%s is %s with current %q — the sentinel was rendered as this node's value",
				axis, v.State, v.Current)
		}
		if strings.Contains(v.Current, discovery.UnresolvedSentinel) {
			t.Fatalf("%s renders the sentinel in the value position: %q", axis, v.Current)
		}
		if v.MissingInput != assessment.MissingUnresolvedField {
			t.Errorf("%s names %q as its missing input, want %q",
				axis, v.MissingInput, assessment.MissingUnresolvedField)
		}
	}
}

// §5.3 — a `not_measured` value ALWAYS names what is missing, and always says why in a sentence.
//
// A `not_measured` that names a machine identifier and nothing else is an apology rather than an action
// (assessment design D1). Both halves are required, on every axis, for every node.
func TestEveryNotMeasuredValueNamesItsMissingInputAndSaysWhy(t *testing.T) {
	for _, n := range []runlink.WireIRNode{
		node(nil),
		node(func(n *runlink.WireIRNode) { *n = runlink.WireIRNode{NodeID: "bare"} }),
		node(func(n *runlink.WireIRNode) { n.ContextPolicy = ""; n.ModelID = "" }),
	} {
		row := ForNode(n)
		for _, v := range row.Values {
			if v.State != assessment.StateNotMeasured {
				continue
			}
			if !v.MissingInput.Valid() {
				t.Errorf("node %q axis %s: missing input %q is not a member of the closed enum",
					row.NodeID, v.Axis, v.MissingInput)
			}
			if strings.TrimSpace(v.Because) == "" {
				t.Errorf("node %q axis %s: not_measured with no sentence saying why", row.NodeID, v.Axis)
			}
			if v.Current != "" {
				t.Errorf("node %q axis %s: not_measured AND carries a current value %q — exactly one of the "+
					"two is ever set", row.NodeID, v.Axis, v.Current)
			}
		}
	}
}

// §5.3 — the inverse, and the one that catches a regression toward "helpful" defaults: an axis WITH a
// populated wire field must resolve, and must resolve to that field's own value.
func TestAnAxisWithAPopulatedWireFieldResolvesToThatField(t *testing.T) {
	row := ForNode(node(nil))

	model := valueOf(t, row, assessment.AxisModel)
	if model.State != assessment.StateObserved || model.Current != "claude-sonnet-4-5" {
		t.Fatalf("model is %s/%q, want observed/claude-sonnet-4-5", model.State, model.Current)
	}
	if model.Detail != "anthropic" {
		t.Errorf("the provider is the clause a reader needs beside a model id; got %q", model.Detail)
	}

	ctx := valueOf(t, row, assessment.AxisContext)
	if ctx.State != assessment.StateObserved || ctx.Current != "sliding-window" {
		t.Fatalf("context is %s/%q, want observed/sliding-window", ctx.State, ctx.Current)
	}

	tools := valueOf(t, row, assessment.AxisTools)
	if tools.State != assessment.StateObserved || tools.Current != "3 tools" {
		t.Fatalf("tools is %s/%q, want observed/\"3 tools\"", tools.State, tools.Current)
	}
}

// 🔴 §5.3 — the four axes with NO wire field report `not_measured`, NEVER the vocabulary's identity
// element.
//
// This is the test that would go red if somebody "helpfully" rendered `memory: none` — which is what
// `discovery` emits for every node, and which `discovery/emit.go` itself calls *"a statement about the
// EVIDENCE, not a placeholder"*. Rendering it would put a strategy on the reader's screen that their
// node does not have, and they would author a change from a baseline that never existed.
func TestTheAxesWithNoWireFieldAreNotMeasuredAndNotDefaulted(t *testing.T) {
	row := ForNode(node(nil))
	for _, axis := range []assessment.Axis{
		assessment.AxisMemory, assessment.AxisHarness, assessment.AxisLoop,
		assessment.AxisPrompt, assessment.AxisSkills, assessment.AxisGraph,
	} {
		v := valueOf(t, row, axis)
		if v.State != assessment.StateNotMeasured {
			t.Fatalf("%s is %s with current %q — the wire carries no field for this axis, so any value "+
				"here was supplied rather than read", axis, v.State, v.Current)
		}
		if v.MissingInput != assessment.MissingNotVisibleStatically {
			t.Errorf("%s names %q; an axis the wire has no field for is `not_visible_in_static_ir`, which "+
				"is a different investigation from a field that failed to resolve",
				axis, v.MissingInput)
		}
		for _, identity := range []string{"none", "single-shot", "full", "full-history"} {
			if v.Current == identity {
				t.Fatalf("%s rendered the vocabulary's identity element %q as this node's value", axis, identity)
			}
		}
	}
}

// §5.1 / FR8 — every node's row is EVERY axis, always, in the shared report order.
//
// A row that omitted its unresolved axes would make absence invisible, which is precisely what
// `not_measured` exists to prevent.
func TestEveryNodeCarriesEveryAxisInTheSharedOrder(t *testing.T) {
	row := ForNode(node(nil))
	want := assessment.Axes()
	if len(row.Values) != len(want) {
		t.Fatalf("row has %d axes, the shared enum has %d", len(row.Values), len(want))
	}
	for i, axis := range want {
		if row.Values[i].Axis != axis {
			t.Fatalf("axis %d is %q, want %q — the order is `assessment.Axes()`, not this package's own",
				i, row.Values[i].Axis, axis)
		}
	}
}

// 🔴 FR8 — this package produces only two of the four states, and the other two stay unreachable.
//
// `measured` is a number an eval run produced, and reading a field out of the IR is not that.
// `refused` means THIS BUILD cannot assess the axis, which is a different owner from an absent wire
// field. Both exist in the shared enum; neither may be produced here.
func TestOnlyTwoStatesAreEverProduced(t *testing.T) {
	nodes := []runlink.WireIRNode{
		node(nil),
		node(func(n *runlink.WireIRNode) { *n = runlink.WireIRNode{NodeID: "bare"} }),
		node(func(n *runlink.WireIRNode) { n.ModelID = discovery.UnresolvedSentinel }),
		node(func(n *runlink.WireIRNode) { n.ToolCount = -1 }),
	}
	for _, n := range nodes {
		for _, v := range ForNode(n).Values {
			if v.State != assessment.StateObserved && v.State != assessment.StateNotMeasured {
				t.Fatalf("node %q axis %s produced %q; this package may only produce `observed` or "+
					"`not_measured`", n.NodeID, v.Axis, v.State)
			}
		}
	}
}

// §5.1 — Build reads every reported node and sorts, so two renders of the same structure agree.
func TestBuildCoversEveryReportedNodeDeterministically(t *testing.T) {
	ir := linkingest.WorkflowIR{
		WorkflowID: "wf", SourceRevision: "rev",
		Nodes: []runlink.WireIRNode{
			node(func(n *runlink.WireIRNode) { n.NodeID = "zeta" }),
			node(func(n *runlink.WireIRNode) { n.NodeID = "alpha" }),
		},
	}
	got := Build(ir)
	if len(got.Nodes) != 2 {
		t.Fatalf("Build covered %d of 2 reported nodes", len(got.Nodes))
	}
	if got.Nodes[0].NodeID != "alpha" || got.Nodes[1].NodeID != "zeta" {
		t.Fatalf("Build is not sorted: %q, %q", got.Nodes[0].NodeID, got.Nodes[1].NodeID)
	}
	if got.CoverageVersion == "" {
		t.Error("a report must state the coverage table version its picker vocabularies were bound to")
	}
}

// 🔴 §5.2 / FR17 — the live coverage read, and the one way it could lie.
//
// A language with no rewriter must return NOTHING rather than falling back to Go's rows. A coverage
// answer attributed to the wrong language is a claim about the reader's code drawn from a guess, and it
// would be wrong in the direction that wastes their afternoon: they would be told a policy applies.
func TestContextCoverageIsPerLanguageAndNeverFallsBack(t *testing.T) {
	if rows := ContextCoverageForLanguage("go"); len(rows) == 0 {
		t.Fatal("go has a context selection rewriter and must return its policy rows")
	}
	for _, absent := range []string{"", "  ", "cobol", "typescript-but-misspelled"} {
		if rows := ContextCoverageForLanguage(absent); rows != nil {
			t.Fatalf("language %q returned %d rows — a language with no rewriter must return none, not "+
				"another language's answer", absent, len(rows))
		}
	}
	// Case and surrounding space are the reader's, not a different language.
	if len(ContextCoverageForLanguage("  GO  ")) != len(ContextCoverageForLanguage("go")) {
		t.Error("a language name differing only in case or padding is the same language")
	}
}

// §5.2 — the read is the ENGINE's table, not a copy of it. Asserted by comparing against the languages
// the engine itself reports as covered, so this test goes red when a rewriter lands and nothing here
// was updated — which is the drift the removed transcription fence used to catch.
func TestCoveredLanguagesAreTheEnginesOwn(t *testing.T) {
	langs := CoveredLanguages()
	if len(langs) == 0 {
		t.Fatal("no language has a context selection rewriter — the engine says otherwise")
	}
	for _, lang := range langs {
		if len(ContextCoverageForLanguage(lang)) == 0 {
			t.Errorf("%q is reported as covered and has no policy rows", lang)
		}
	}
}
