package herosagent

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// fixtures.go declares the pinned calibration set (task 5.1).
//
// # The two kinds, and why both are needed
//
// A fixture whose true graph is RICH measures whether the agent can find edges. A NEAR-MISS measures
// whether it discriminates — and an agent with no discriminative power at all passes the first kind
// perfectly by connecting everything it sees. The near-misses are named in the design for that reason:
// a linear chain that is not a router, a fan-out with no merge, and two calls in one file with no data
// dependency between them.
//
// # 🔴 Ground truth for Go is MEASURED (task 5.2)
//
// The Go frontend's real edge output is the only ground truth this platform already owns, which is why
// Go fixtures matter even though Go is the language that needs HEROS least. A Go fixture's TrueEdges
// are therefore left empty in this declaration and FILLED BY RUNNING THE FRONTEND — see
// DiskFixtures.Fixtures. Hand-writing them would replace a measurement with somebody's reading of it.

// fixtureDecl is the static half of a fixture: everything a human decides.
type fixtureDecl struct {
	name     string
	language string
	// dir is relative to the repository root.
	dir   string
	truth GroundTruth
	// trueEdges is the hand-declared answer. Ignored for TruthMeasured fixtures.
	trueEdges []Pair
	note      string
}

// calibrationSet is the pinned set.
//
// ⚠️ FIVE OF THE SEVEN SUPPORTED LANGUAGES CARRY ONLY THE RICH FIXTURE and no near-misses of their own,
// and that is a stated gap rather than a silent one — it is recorded in tasks.md §11.5. The near-misses
// are language-shaped in their SOURCE but not in what they test: "two calls with no data dependency"
// is a property of the graph, and an agent that connects them in Python connects them in Rust. Adding
// per-language near-misses is adding directories here, which is why the set is a table.
var calibrationSet = []fixtureDecl{
	// ── The MEASURED fixture. Ground truth is the Go frontend's own output. ──────────────────────────
	//
	// 🔴 It is a PURPOSE-BUILT tree, and the first draft of this set was not — it pointed at
	// `discovery/testdata/samplerepo`, which the Go frontend resolves ZERO edges for. That set loaded,
	// scored, and passed, and every one of its nine fixtures had an empty true graph: the gate could
	// measure whether the agent DISCRIMINATES and could not measure whether it finds an edge at all.
	// An all-near-miss calibration set is the same vacuity task 5.7 refuses, one level up, and it was
	// found by MEASURING the set rather than by reading it. See TestTheSetCanMeasureEdgeFinding.
	{
		name: "go_chain", language: "go",
		dir:   filepath.Join("internal", "herosagent", "testdata", "fixtures", "go_chain"),
		truth: TruthMeasured,
		note: "a real Go tree whose two-edge chain the typed frontend RESOLVES. Its true graph is a " +
			"measurement, not a reading of one — which is why this fixture is the anchor even though Go " +
			"is the language that needs HEROS least. It also carries an unrelated call in the same " +
			"package, so it measures discrimination and edge-finding at once",
	},

	// ── The three NEAR-MISSES, named in the design (D7 "Fixture design"). ────────────────────────────
	{
		name: "py_linear_chain", language: "python",
		dir:   filepath.Join("internal", "herosagent", "testdata", "fixtures", "py_linear_chain"),
		truth: TruthDeclared,
		trueEdges: []Pair{
			// extract → summarize → draft. A CHAIN. The edges are real; what is absent is a router.
			// Node ids are content-addressed by the frontend, so they are resolved at load — see
			// resolveDeclaredEdges.
			{From: "extract", To: "summarize"},
			{From: "summarize", To: "draft"},
		},
		note: "a linear chain that is NOT a router. Three calls in sequence, nothing choosing between " +
			"alternatives. An agent that reads sequence as routing has found a pattern that is not there",
	},
	{
		name: "py_fanout_no_merge", language: "python",
		dir:   filepath.Join("internal", "herosagent", "testdata", "fixtures", "py_fanout_no_merge"),
		truth: TruthDeclared,
		trueEdges: []Pair{
			{From: "plan", To: "wing_a"},
			{From: "plan", To: "wing_b"},
		},
		note: "a fan-out with NO merge. Parallelization needs a join; an agent that emits one has " +
			"invented the single thing that would make this a pattern",
	},
	{
		name: "py_independent_calls", language: "python",
		dir:   filepath.Join("internal", "herosagent", "testdata", "fixtures", "py_independent_calls"),
		truth: TruthDeclared,
		// 🔴 EMPTY, and that is the answer. Two calls in one file that share nothing. Proximity is not
		// topology, and an agent that connects whatever is nearby scores ZERO PRECISION here.
		trueEdges: nil,
		note: "two calls in one file with NO data dependency. The correct edge count is zero, and an " +
			"agent with no discriminative power fails this fixture and only this fixture",
	},

	// ── The remaining languages' rich fixtures, reusing the real trees discovery already ships. ──────
	{
		name: "python_triage", language: "python",
		dir:   filepath.Join("internal", "discovery", "testdata", "fixtures", "python"),
		truth: TruthDeclared, trueEdges: nil,
		note: "a real Python tree: `classify` and `agent` are independent, so the true edge count is " +
			"zero. It is simultaneously the language's rich fixture and a near-miss, which is what " +
			"makes it worth pinning",
	},
	{
		name: "typescript_svc", language: "typescript",
		dir:   filepath.Join("internal", "discovery", "testdata", "fixtures", "typescript"),
		truth: TruthDeclared, trueEdges: nil,
		note: "a real TypeScript tree with no established dependency between its call sites",
	},
	{
		name: "rust_svc", language: "rust",
		dir:   filepath.Join("internal", "discovery", "testdata", "fixtures", "rust"),
		truth: TruthDeclared, trueEdges: nil,
		note: "a real Rust tree with no established dependency between its call sites",
	},
	{
		name: "java_svc", language: "java",
		dir:   filepath.Join("internal", "discovery", "testdata", "fixtures", "java"),
		truth: TruthDeclared, trueEdges: nil,
		note: "a real Java tree with no established dependency between its call sites",
	},
	{
		name: "kotlin_svc", language: "kotlin",
		dir:   filepath.Join("internal", "discovery", "testdata", "fixtures", "kotlin"),
		truth: TruthDeclared, trueEdges: nil,
		note: "a real Kotlin tree with no established dependency between its call sites",
	},
}

// DiskFixtures loads the calibration set from the repository.
type DiskFixtures struct {
	// Root is the repository root the fixture directories are relative to.
	Root string
	// Discover produces a fixture's IR. Required for the MEASURED fixtures, whose ground truth is the
	// Go frontend's own edge output, and for resolving declared symbol names to node ids.
	Discover Discoverer
}

// Fixtures loads and resolves the set.
//
// 🔴 Every failure is an ERROR, never a shorter list (task 5.7). A loader that skipped what it could
// not read would shrink the calibration set silently, and a gate measuring three fixtures instead of
// nine passes more easily — which is the failure mode nobody would notice.
func (d DiskFixtures) Fixtures() ([]Fixture, error) {
	if d.Discover == nil {
		return nil, fmt.Errorf("herosagent: a discoverer is required to load fixtures — the measured " +
			"fixture's ground truth IS the frontend's output, and the declared ones name symbols that " +
			"have to be resolved to node ids")
	}
	out := make([]Fixture, 0, len(calibrationSet))
	for _, decl := range calibrationSet {
		repo := filepath.Join(d.Root, decl.dir)
		if st, err := os.Stat(repo); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("herosagent: fixture %q is not readable at %s: the calibration set "+
				"must load WHOLE or fail — a shorter set is an easier gate nobody chose", decl.name, repo)
		}
		ir, _, err := d.Discover(repo)
		if err != nil {
			return nil, fmt.Errorf("herosagent: discovering fixture %q: %w", decl.name, err)
		}
		f := Fixture{Name: decl.name, Language: decl.language, Repo: repo, Truth: decl.truth, Note: decl.note}

		switch decl.truth {
		case TruthMeasured:
			// The frontend's REAL output. Task 5.2.
			for _, e := range ir.Edges {
				f.TrueEdges = append(f.TrueEdges, Pair{From: e.FromNodeID, To: e.ToNodeID})
			}
		default:
			resolved, rerr := resolveDeclaredEdges(ir, decl.trueEdges)
			if rerr != nil {
				return nil, fmt.Errorf("herosagent: fixture %q: %w", decl.name, rerr)
			}
			f.TrueEdges = resolved
		}
		out = append(out, f)
	}
	return out, nil
}

// resolveDeclaredEdges maps hand-declared SYMBOL names onto the content-addressed node ids the frontend
// actually emitted.
//
// 🔴 A declaration naming a symbol the frontend did not find is an ERROR, not a dropped edge. That is
// the anti-vacuity rule at fixture grain: an unresolvable ground-truth edge silently removed makes the
// fixture easier, and the agent then passes a test whose answer key was quietly edited.
func resolveDeclaredEdges(ir *discovery.IR, declared []Pair) ([]Pair, error) {
	if len(declared) == 0 {
		return nil, nil
	}
	bySymbol := map[string]string{}
	for _, n := range ir.Nodes {
		sym := n.CallSite.Symbol
		// The enclosing symbol is fully qualified; a declaration names the leaf, which is what a human
		// reads in the source.
		if i := lastDot(sym); i >= 0 {
			sym = sym[i+1:]
		}
		bySymbol[sym] = n.NodeID
	}
	out := make([]Pair, 0, len(declared))
	for _, p := range declared {
		from, okF := bySymbol[p.From]
		to, okT := bySymbol[p.To]
		if !okF || !okT {
			known := make([]string, 0, len(bySymbol))
			for s := range bySymbol {
				known = append(known, s)
			}
			return nil, fmt.Errorf("the declared true edge %s→%s names a symbol the frontend did not "+
				"emit a node for. Known symbols: %v. An unresolvable ground-truth edge dropped silently "+
				"makes the fixture easier and the agent then passes an answer key nobody edited on purpose",
				p.From, p.To, known)
		}
		out = append(out, Pair{From: from, To: to})
	}
	return out, nil
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}
