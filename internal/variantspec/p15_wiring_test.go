package variantspec

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/typedcontract"
)

// P15 §1.4 — 🚫 the wiring axis adds NO new modeled thing.
//
// The whole P15 design rests on one already-true fact: the structural axis is `VariantSpec.Order` /
// `Edges` / `InsertedAdapters`, and those are already identity-bearing in `config_hash`. So a wiring
// change needs no new `Dimension`, no new registry `Kind`, no new `NodeOverride` field, and no new DB
// table — and if a later change adds one, the axis quietly acquires a SECOND representation, which is
// the split source-of-truth every one of those four checks exists to prevent. A second representation
// is not a tidiness problem: `Dimension` is iterated to decide what the transform engine rewrites, and
// a `wiring` member would make every rewriter answer "no rewriter for this dimension" for a change the
// spec already carries in `Order`.
//
// This guard is deliberately structural rather than a comment: three of the four are checked against
// the actual declarations (reflection / AST / migration SQL), so the test goes red on the addition
// itself, not on someone remembering to update it.

// contentDimensions is the closed set of CONTENT dimensions — what a node IS, as opposed to how nodes
// are WIRED. Wiring is not among them and must never be.
//
// Note it lists five, not the four `spec.go` carried when the P15 tasks were written: P14 landed
// `DimTools` in between (tool SELECTION — still a per-node content property, still not wiring). The
// invariant this test pins is not the COUNT, which legitimately grows with new content dimensions; it
// is that no member of the enum names the wiring axis.
var contentDimensions = []Dimension{DimModel, DimPrompt, DimSkills, DimContext, DimTools}

// wiringWords are the spellings a wiring dimension/field/kind/table would plausibly use. Matching on
// the vocabulary rather than one exact name is what makes the guard survive a differently-named
// addition ("graph", "topology", "rewire" all mean the same mistake).
var wiringWords = []string{"wiring", "order", "edge", "graph", "topolog", "rewire", "parallel", "merge"}

// observationTables are the pre-P15 tables whose names contain a wiring word but which store an
// OBSERVATION of a run, not a CONFIGURATION. `recon_edge` (P5 tracing, migration 0011) records the
// edges one run was observed to take, tagged static/runtime_only — it is evidence about an execution,
// and evidence is exactly what the wiring axis is scored against. The distinction this guard protects
// is "no second place where a spec's DESIRED wiring lives", so an observation table is not a violation
// of it and is named here rather than silently matched away by a laxer regex.
var observationTables = map[string]bool{"recon_edge": true}

func mentionsWiring(s string) string {
	low := strings.ToLower(s)
	for _, w := range wiringWords {
		if strings.Contains(low, w) {
			return w
		}
	}
	return ""
}

func TestNoNewDimensionForWiring(t *testing.T) {
	// 1. The Dimension enum stays CLOSED over content dimensions.
	got := Dimensions()
	if len(got) != len(contentDimensions) {
		t.Fatalf("Dimensions() returned %v; P15 adds no dimension — the wiring axis is Order/Edges, "+
			"already hashed. Want exactly the content dimensions %v", got, contentDimensions)
	}
	for i, want := range contentDimensions {
		if got[i] != want {
			t.Fatalf("Dimensions()[%d] = %q, want %q", i, got[i], want)
		}
	}
	for _, d := range got {
		if w := mentionsWiring(string(d)); w != "" {
			t.Fatalf("dimension %q names the wiring axis (%q): the axis lives in VariantSpec.Order/Edges, "+
				"which config_hash already covers; a Dimension member would be a second representation of it", d, w)
		}
	}

	// 2. NodeOverride grows no wiring field. A per-node override is a CONTENT delta by construction —
	// wiring is a property of the graph, not of one node, so a wiring field here would let two nodes
	// disagree about one edge.
	rt := reflect.TypeOf(NodeOverride{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if w := mentionsWiring(f.Name); w != "" {
			t.Fatalf("NodeOverride.%s names the wiring axis (%q); the axis is VariantSpec.Order/Edges", f.Name, w)
		}
		if w := mentionsWiring(f.Tag.Get("json")); w != "" {
			t.Fatalf("NodeOverride.%s serialises as %q, which names the wiring axis (%q)", f.Name, f.Tag.Get("json"), w)
		}
	}

	// 3. The registry grows no wiring Kind. A wiring change references no registry entry at all: it
	// selects nothing to resolve, it rearranges nodes that already exist.
	kinds := registryKinds(t)
	wantKinds := []string{"context", "model", "prompt", "skill"}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("registry Kind consts = %v, want %v — P15 registers no wiring kind (a rearrangement "+
			"resolves no ref)", kinds, wantKinds)
	}

	// 4. No wiring table. `Order`/`Edges` are columns of the variant spec document that already
	// persists; a side table would make a spec's wiring reachable without the spec.
	for _, stmt := range createdTables(t) {
		if observationTables[stmt] {
			continue
		}
		if w := mentionsWiring(stmt); w != "" {
			t.Fatalf("migration creates table %q, which names the wiring axis (%q); wiring persists inside "+
				"the variant spec document, not beside it", stmt, w)
		}
	}
}

// registryKinds parses internal/registry for the `Kind` constants, sorted. Parsing the declaration is
// the point: a hand-maintained list here would drift from the enum it claims to guard.
func registryKinds(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join("..", "registry", "registry.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse internal/registry/registry.go: %v", err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		gd, ok := n.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			return true
		}
		for _, s := range gd.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok {
				continue
			}
			id, ok := vs.Type.(*ast.Ident)
			if !ok || id.Name != "Kind" {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				out = append(out, s)
			}
		}
		return true
	})
	sort.Strings(out)
	return out
}

var createTableRe = regexp.MustCompile(`(?i)create\s+table\s+(?:if\s+not\s+exists\s+)?([a-z0-9_."]+)`)

// createdTables returns every table name the migrations create.
func createdTables(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join("..", "..", "db", "migrations", "postgres")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var out []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range createTableRe.FindAllStringSubmatch(string(b), -1) {
			out = append(out, strings.Trim(m[1], `"`))
		}
	}
	if len(out) == 0 {
		t.Fatal("no CREATE TABLE found in the migrations; the guard would pass vacuously")
	}
	return out
}

// ── P15 §5.3 / §5.6 — the adapted verdict, recorded explicitly and deterministically ─────────────

// adapterIR is the fixture both tests share: A emits `answer`, B requires `response` — a rename the
// catalog can bridge — and C is an unconstrained tail node.
func adapterIR() *discovery.IR {
	return irFor(map[string]discovery.IRIOContract{
		"A": {InputSchema: map[string]any{"type": "object"}, OutputSchema: objSchema(map[string]any{"answer": strField()})},
		"B": {InputSchema: objSchema(map[string]any{"response": strField()}, "response"), OutputSchema: map[string]any{"type": "object"}},
		"C": {InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}},
	})
}

func adapterParent() *VariantSpec {
	return &VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev1",
		Order: []string{"A", "B", "C"}, Nodes: map[string]NodeOverride{},
		Edges: []Edge{{FromNodeID: "A", ToNodeID: "B", Kind: "data"}},
	}
}

// TestAdaptedVerdictRecordsAdapter: the adapter is an explicit node carrying its OWN io_contract, the
// edge is rewired producer→adapter→consumer, and the adapter sits after its producer in the order.
//
// The alternative posture — reconcile the mismatch in a runtime shim — is what this test forbids. A
// coercion applied at run time is a data transformation the reviewer approving the reorder never saw,
// and "less diff noise" (an L3 convenience) cannot buy down an L1 review guarantee (decisions.md D-2).
func TestAdaptedVerdictRecordsAdapter(t *testing.T) {
	cand := Reorder(adapterParent(), "parent-hash", []string{"A", "B", "C"},
		[]Edge{{FromNodeID: "A", ToNodeID: "B", Kind: "data"}})
	got, verdict := GateReorder(adapterIR(), cand, typedcontract.DefaultCatalog())
	if verdict.Kind != typedcontract.VerdictAdapted || got == nil {
		t.Fatalf("want an adapted verdict with a runnable spec, got %s / %+v", verdict.Kind, got)
	}
	if len(got.InsertedAdapters) != 1 {
		t.Fatalf("the adapter must be recorded on the spec, got %+v", got.InsertedAdapters)
	}
	a := got.InsertedAdapters[0]
	if a.FromNodeID != "A" || a.ToNodeID != "B" {
		t.Errorf("the adapter must name the edge it bridges, got %s→%s", a.FromNodeID, a.ToNodeID)
	}
	if len(a.InSchema) == 0 || len(a.OutSchema) == 0 {
		t.Errorf("an inserted adapter carries its OWN io_contract, got in=%v out=%v", a.InSchema, a.OutSchema)
	}
	if a.CatalogKind == "" || len(a.Params) == 0 {
		t.Errorf("the adapter must record WHAT it does (kind + params), got %q %v", a.CatalogKind, a.Params)
	}

	// Edges: the direct A→B is gone, replaced by A→adapter→B.
	var toAdapter, fromAdapter bool
	for _, e := range got.Edges {
		if e.FromNodeID == "A" && e.ToNodeID == "B" {
			t.Error("the direct producer→consumer edge must be replaced by the adapter hop")
		}
		if e.FromNodeID == "A" && e.ToNodeID == a.AdapterNodeID {
			toAdapter = true
		}
		if e.FromNodeID == a.AdapterNodeID && e.ToNodeID == "B" {
			fromAdapter = true
		}
	}
	if !toAdapter || !fromAdapter {
		t.Fatalf("edges must be rewired producer→adapter→consumer, got %+v", got.Edges)
	}
	// Order: the adapter runs after its producer and before its consumer, or the graph it describes
	// could not execute.
	ai, pi, ci := indexIn(got.Order, a.AdapterNodeID), indexIn(got.Order, "A"), indexIn(got.Order, "B")
	if ai < 0 || !(pi < ai && ai < ci) {
		t.Fatalf("the adapter must be ordered between its producer and consumer, got %v", got.Order)
	}
	// The parent is untouched: gating derives, it does not edit in place.
	if len(adapterParent().InsertedAdapters) != 0 {
		t.Error("the gate must not write adapters back onto the parent")
	}
}

// TestAdapterIdentityDeterministic: the same reorder yields the same adapter ids and the same
// config_hash on every evaluation. Adapter identity is derived from (from, to, kind) and the catalog
// match order is fixed, so nothing about the result depends on when it was computed.
func TestAdapterIdentityDeterministic(t *testing.T) {
	gate := func() *VariantSpec {
		cand := Reorder(adapterParent(), "parent-hash", []string{"A", "B", "C"},
			[]Edge{{FromNodeID: "A", ToNodeID: "B", Kind: "data"}})
		got, v := GateReorder(adapterIR(), cand, typedcontract.DefaultCatalog())
		if v.Kind != typedcontract.VerdictAdapted || got == nil {
			t.Fatalf("want adapted, got %s", v.Kind)
		}
		return got
	}
	first, second := gate(), gate()

	if first.InsertedAdapters[0].AdapterNodeID != second.InsertedAdapters[0].AdapterNodeID {
		t.Fatalf("adapter identity is not stable: %q vs %q",
			first.InsertedAdapters[0].AdapterNodeID, second.InsertedAdapters[0].AdapterNodeID)
	}
	fb, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(fb) != string(sb) {
		t.Fatalf("the gated spec is not byte-identical across evaluations:\n %s\n %s", fb, sb)
	}

	// And the identity that matters downstream: the config_hash over the adapted wiring is the same on
	// both evaluations, so a re-run scores the same configuration rather than forking it in two.
	hash := func(s *VariantSpec) string {
		cfg := ResolvedConfig{IRVersion: discovery.IRVersion}
		for _, id := range s.Order {
			cfg.Nodes = append(cfg.Nodes, ResolvedNode{NodeID: id})
		}
		for _, e := range s.Edges {
			cfg.Edges = append(cfg.Edges, ResolvedEdge(e))
		}
		h, err := cfg.Hash()
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	if hash(first) != hash(second) {
		t.Fatalf("the adapted arrangement hashed to two different configurations: %s vs %s", hash(first), hash(second))
	}
}

func indexIn(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}
