package api

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// p29_surfaces_test.go is P29 §6 — the workflow surfaces.

func matrixWithReportedStructure(t *testing.T) (*Server, *linkingest.MemWorkflowIRStore) {
	t.Helper()
	irStore := linkingest.NewMemWorkflowIRStore()
	if err := irStore.Put(linkingest.WorkflowIR{
		TenantID: "t-1", WorkflowID: "wf", SourceRevision: "rev", IRVersion: "v1",
		ReceivedAt: time.Now().UTC(),
		Nodes: []runlink.WireIRNode{
			{NodeID: "n_1", Symbol: "TrajectoryCompressor.summarise", File: "compress.py",
				Provider: "openai", ModelID: "gpt-4o", Language: "python"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	s := New(nil, config.Config{})
	s.MountWorkflowIR(irStore)
	// The matrix, mounted the way a real deployment does: a model store and NO process-local catalog.
	s.MountStudioMatrix(StudioMatrix{Store: stubModelStore{}})
	return s, irStore
}

// stubModelStore satisfies StudioModelStore so the matrix mounts. It is deliberately inert: what these
// tests exercise is where the COLUMNS come from and how a credential-free deployment refuses, neither of
// which reads a model.
type stubModelStore struct{}

func (stubModelStore) ModelCatalog(context.Context) ([]registry.ModelCatalogEntry, error) {
	return nil, nil
}
func (stubModelStore) ResolveModel(context.Context, string) (*registry.ModelEntry, error) {
	return nil, registry.ErrNotFound
}
func (stubModelStore) StudioRender(context.Context, string, map[string]string) (string, error) {
	return "", nil
}

// postWithTenant drives a POST as an authenticated principal.
func postWithTenant(t *testing.T, s *Server, path, body, tenant string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := auth.WithPrincipal(req.Context(), auth.Principal{TenantID: tenant})
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// 🔴 §6.1 — the matrix's COLUMNS are this organization's own reported nodes, named.
//
// Before this, they came from `studio.WorkflowCatalog`, a process-local map filled only by `cmd/demo`
// and `cmd/proof`. On every real deployment the route answered 404 for every workflow a customer had,
// and the matrix had no columns at all — with the screen saying "no such workflow is loaded", which
// reads as a wrong identifier and is not one.
func TestTheStudioMatrixColumnsAreTheTenantsReportedNodes(t *testing.T) {
	s, _ := matrixWithReportedStructure(t)

	rec := enumRequest(t, s, "/api/v1/workflows/wf/nodes", "t-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("matrix columns answered %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		State string         `json:"state"`
		Nodes []studioColumn `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Nodes) != 1 {
		t.Fatalf("got %d column(s), want 1: %s", len(body.Nodes), rec.Body.String())
	}
	col := body.Nodes[0]
	if col.Symbol != "TrajectoryCompressor.summarise" {
		t.Errorf("the column carries no symbol (%q). A matrix whose columns are opaque hashes is a "+
			"matrix nobody can use: the question it answers is \"which of MY call sites should this "+
			"model go to\", and a hash does not tell a reader which call site they are looking at.",
			col.Symbol)
	}
	if col.ModelID != "gpt-4o" || col.Provider != "openai" {
		t.Errorf("the column carries no current model (%q/%q). Every cell in a column is a change FROM "+
			"the node's current binding, and a column that does not state it has hidden the baseline.",
			col.Provider, col.ModelID)
	}
}

// An UNREPORTED workflow is distinct from a nonexistent one and from a read failure (§6.4).
func TestAnUnreportedWorkflowIsNotReportedRatherThanNotFound(t *testing.T) {
	s, _ := matrixWithReportedStructure(t)

	rec := enumRequest(t, s, "/api/v1/workflows/never-sent/nodes", "t-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("an unreported workflow answered %d. 404 reads as \"no such workflow\" and sends the "+
			"reader to check an id that is correct; the truth is that the platform was never told this "+
			"workflow's shape.", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["state"] != "not-reported" {
		t.Errorf("state = %v, want not-reported", body["state"])
	}
	if body["fill_with"] == nil {
		t.Error("the response names no command that would fill it. A screen that says a page is empty " +
			"and not how to fill it is the screen this phase is replacing.")
	}
}

// 🔴 §6.2 — a cell action needing a provider credential is refused BY NAME, and does not imply a plan
// would change it.
func TestAStudioRunIsRefusedByNameAndNeverImpliesAPlanWouldFixIt(t *testing.T) {
	s, _ := matrixWithReportedStructure(t)

	rec := postWithTenant(t, s, "/api/v1/studio/run",
		`{"model_version_id":"m","prompt_version_id":"p"}`, "t-1")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["reason_code"] != "no_customer_provider_credential" {
		t.Errorf("reason_code = %v — the console branches on the identifier and renders its own copy, "+
			"so a refusal without one has no treatment to select", body["reason_code"])
	}
	msg, _ := body["error"].(string)
	for _, must := range []string{"holds no provider", "never will", "heros author"} {
		if !strings.Contains(msg, must) {
			t.Errorf("the refusal does not say %q:\n  %s", must, msg)
		}
	}
	if body["plan_would_fix"] != false {
		t.Errorf("plan_would_fix = %v. It must be an explicit FALSE: the old sentence — \"studio "+
			"test-run is not available on this deployment\" — reads as a capability somebody switched "+
			"off, so the reader's next move is to ask which plan turns it on. There is no such plan.",
			body["plan_would_fix"])
	}
	if body["local_command"] == nil {
		t.Error("the refusal names no local command. The work IS possible — with the customer's own key, " +
			"on their own machine — and a refusal that does not say so reads as a dead end.")
	}
}

// 🔴 §6.3 — THERE IS NO SECOND APPLY PATH.
//
// A hosted binding must travel the same preflight → resolve → gate → transform spine every other change
// does. The failure this prevents is the one that makes a refusal meaningless: a console action that
// reaches `transform.Generate` by its own route would be a change the gates never saw, and "a refused
// change is refused identically from the console" would be false without anything failing.
func TestThereIsNoSecondApplyPath(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 🔴 ONE allowlisted call, with its justification written down — the same discipline
	// `publicroutes.go` and the post-P26 migration ledger use, and for the same reason: a fence with no
	// exception mechanism is a fence somebody deletes the first time it is inconvenient.
	//
	// `grapheditor.go`'s commit IS a spine, not a second one. It is the P5 graph-editor path, it runs the
	// contract verdict BEFORE reaching the engine (a `rejected` reorder never gets here), and the engine's
	// own wiring check refuses a rearrangement it cannot materialise — which this handler surfaces as
	// `rejected_transform` rather than as a diff. What P29 must not add is a route that skips those.
	allowed := map[string]bool{"grapheditor.go": true}

	files := 0
	for _, p := range pkgs {
		for name, f := range p.Files {
			files++
			if allowed[filepath.Base(name)] {
				continue
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok || pkgIdent.Name != "transform" {
					return true
				}
				if sel.Sel.Name == "Generate" || sel.Sel.Name == "GenerateTransform" {
					t.Errorf("%s:%d calls transform.%s directly from the API layer.\n"+
						"  🔴 Every change travels ONE spine — preflight, resolve, gate, transform — and a "+
						"second route to the engine is a change the gates never saw. A refusal has to be "+
						"identical from the console and from the CLI, and that is only true while there "+
						"is one path.",
						filepath.Base(name), fset.Position(call.Pos()).Line, sel.Sel.Name)
				}
				return true
			})
		}
	}
	if files < 20 {
		t.Fatalf("the scan read %d file(s) — it is not reading this package", files)
	}

	// And the STUDIO BIND path — the one P29 adds a hosted action to — reaches the engine through
	// nothing of its own. Asserted separately from the scan above so that removing the allowlist entry
	// above cannot silently remove this check too.
	src, err := os.ReadFile("studiomatrix.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "transform.Generate") {
		t.Error("studiomatrix.go reaches the transform engine directly. A hosted binding must travel the " +
			"existing preflight → resolve → gate → transform spine, or a refusal is not identical from " +
			"the console and from the CLI.")
	}
}

// 🔴 §6.7 — graph regions stay `unclassified`, carried as DATA, and no pattern label is inferred from a
// symbol name.
//
// The temptation is concrete: a node called `rerank_results` is obviously a reranker, and one line of
// string matching would label the graph beautifully. It would also label `route_request` as routing on a
// repository where that function does something else entirely — and a pattern label on a graph is read
// as a finding, not as a guess. `unclassified` is the honest answer and it is a value, not a blank.
func TestNoPatternLabelIsInferredFromASymbolName(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The string operations that would implement a name-based guess.
	guessy := map[string]bool{"Contains": true, "HasPrefix": true, "HasSuffix": true, "EqualFold": true}
	checked := 0
	for _, p := range pkgs {
		for name, f := range p.Files {
			base := filepath.Base(name)
			if base != "patterngraph.go" && base != "studiomatrix.go" && base != "axisprojection.go" {
				continue
			}
			checked++
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) < 2 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok || pkgIdent.Name != "strings" || !guessy[sel.Sel.Name] {
					return true
				}
				// Is the FIRST argument a symbol or a node name?
				arg, ok := call.Args[0].(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if arg.Sel.Name == "Symbol" || arg.Sel.Name == "NodeID" || arg.Sel.Name == "File" {
					t.Errorf("%s:%d matches on a node's %s with strings.%s.\n"+
						"  🔴 A pattern label inferred from a symbol NAME is a guess rendered as a finding. "+
						"`rerank_results` is obviously a reranker until it is a repository where that "+
						"function does something else — and the reader has no way to tell which they are "+
						"looking at. `unclassified` is the honest value, and it is a value rather than a "+
						"blank precisely so the screen can say so.",
						filepath.Base(name), fset.Position(call.Pos()).Line, arg.Sel.Name, sel.Sel.Name)
				}
				return true
			})
		}
	}
	if checked == 0 {
		t.Fatal("the scan read none of the graph-rendering files — it would pass for the wrong reason")
	}
}
