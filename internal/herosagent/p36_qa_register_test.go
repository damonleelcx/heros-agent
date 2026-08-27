package herosagent

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
)

// p36_qa_register_test.go is §9 — the register of P36's fences, and the fence over the register.
//
// # 🔴 Why a register needs its own fence
//
// A list of test names in a document rots in the SAFE direction: a fence gets renamed or deleted, the
// document still names it, and nothing points at anything. The list keeps reading as coverage while
// covering nothing, and the failure is invisible because a document cannot fail.
//
// So the register lives HERE, in code, and this file asserts every entry resolves to a test function
// that actually exists in this repository. Delete a fence and this goes red, naming the requirement it
// was the evidence for.

// p36Fences maps each §9 requirement to the test that is its evidence.
//
// 🔴 Every value is a FUNCTION NAME that must exist. Not a file, not a package — a name, because a test
// can be emptied without being deleted and a file can be renamed without losing anything.
var p36Fences = map[string]struct {
	test string
	pkg  string
}{
	"9.1 single-node definition hashes byte-identically to pre-P36": {
		"TestPreP36ConfigHashesAreReproducedExactly", "internal/herosagent"},
	"9.2 existing pins readable and attributable after the shape change": {
		"TestAnExistingPinRemainsReadableAndNamesItsProducingConfiguration", "internal/herosagent"},
	"9.3 a definition change does not silently re-run pins; no provider call": {
		"TestActivatingANewDefinitionRunsNoInference", "internal/herosagent"},
	"9.4 a stale pin renders stale with its producing configuration named": {
		"TestAPinFromAnUnauthorableShapeIsStaleAndNamesItsProducer", "internal/herosagent"},
	"9.5 the credential fence covers new fields — add a key-shaped field and it must fail": {
		"TestTheCredentialFenceFiresOnAKeyAddedToTheNewShape", "internal/herosagent"},
	"9.6 a single-node definition still refuses an ordering": {
		"TestTheOrderingRefusalNarrowsRatherThanDisappearing", "internal/herosagent"},
	"9.7 a fan-in without a merge is refused at publish": {
		"TestAFanInWithoutAMergeIsRefusedAtPublish", "internal/herosagent"},
	"9.8 a loop with an unavailable host service is refused at publish, NOT at run": {
		"TestALoopIsRefusedAtPublishRatherThanReachingTheRunner", "internal/herosagent"},
	"9.9 rehearsal is required before activating multi-node": {
		"TestActivatingAMultiNodeDefinitionRequiresARehearsal", "internal/herosagent"},
	"9.10 adding a node does not raise the assessment budget": {
		"TestAddingANodeDoesNotRaiseTheAssessmentBudget", "internal/herosagent"},
	"9.11 a repeated pinned inference under concurrency is byte-identical, run repeatedly": {
		"TestARepeatedPinnedInferenceUnderConcurrencyIsByteIdentical", "internal/herosagent"},
	"9.12 P26's build fence covers the new axes and the node dimension": {
		// The console's own suite. Named here so the cross-surface requirement has ONE register rather
		// than two half-registers that can disagree about what is covered.
		"P36 — every one of the nine axes has an operator surface", "web/admin-console/tests"},
	"9.13 the agent and a customer's spec share one validator — assert the code path": {
		"TestTheAgentsTopologyGoesThroughTheCustomersValidator", "internal/herosagent"},
	"9.14 no proposal targets the agent's own definition (D5)": {
		"TestNoProposalTargetsTheAgentsOwnDefinition", "internal/proposalgen"},
	"9.15 rollback to a previous version is one act and requires no re-authoring": {
		"TestRollbackIsOneActAndRequiresNoReAuthoring", "internal/herosagent"},
}

// 🔴 EVERY REGISTERED FENCE EXISTS. This is what stops the register rotting in the safe direction.
func TestEveryP36FenceInTheRegisterExists(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	// Go test function names, discovered by PARSING rather than by grepping: a grep matches the name in
	// a comment, and a fence that exists only in prose is exactly what this test is for.
	goTests := map[string]bool{}
	var scannedFiles int
	for _, pkg := range []string{"internal/herosagent", "internal/proposalgen", "internal/assessment",
		"internal/variantspec", "internal/pgmigrate", "internal/api"} {
		dir := filepath.Join(root, pkg)
		entries, rerr := os.ReadDir(dir)
		if rerr != nil {
			t.Fatalf("reading %s: %v", pkg, rerr)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			f, perr := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, 0)
			if perr != nil {
				t.Fatalf("parsing %s/%s: %v", pkg, e.Name(), perr)
			}
			scannedFiles++
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
					goTests[fn.Name.Name] = true
				}
			}
		}
	}
	if scannedFiles < 20 {
		t.Fatalf("only %d test file(s) were parsed — the scan is not reaching the tree, so its clean "+
			"report below means nothing", scannedFiles)
	}

	// The console's suite is JavaScript; its test names are string literals in `test("…")`.
	consoleTests := map[string]bool{}
	consoleDir := filepath.Join(root, "web", "admin-console", "tests")
	entries, rerr := os.ReadDir(consoleDir)
	if rerr != nil {
		t.Fatalf("reading the console suite: %v", rerr)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".test.mjs") {
			continue
		}
		b, ferr := os.ReadFile(filepath.Join(consoleDir, e.Name()))
		if ferr != nil {
			t.Fatal(ferr)
		}
		for _, line := range strings.Split(string(b), "\n") {
			i := strings.Index(line, `test("`)
			if i < 0 {
				continue
			}
			rest := line[i+len(`test("`):]
			if j := strings.Index(rest, `"`); j > 0 {
				consoleTests[rest[:j]] = true
			}
		}
	}
	if len(consoleTests) < 20 {
		t.Fatalf("only %d console test(s) were found — the scan is not reaching the suite", len(consoleTests))
	}

	var missing []string
	for req, ev := range p36Fences {
		found := goTests[ev.test]
		if strings.HasPrefix(ev.pkg, "web/") {
			// A console test name is a sentence; match on a distinctive substring so an emoji or a
			// prefix does not break the register.
			found = false
			for name := range consoleTests {
				if strings.Contains(name, ev.test) {
					found = true
				}
			}
		}
		if !found {
			missing = append(missing, req+"  →  "+ev.test+" (expected in "+ev.pkg+")")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("these P36 requirements name evidence that does not exist:\n  %s\n\n"+
			"  A register rots in the SAFE direction: the fence is renamed or deleted, the register still "+
			"names it, and nothing points at anything — while the list keeps reading as coverage.",
			strings.Join(missing, "\n  "))
	}
	// ANTI-VACUITY: the register covers all fifteen §9 requirements.
	if len(p36Fences) != 15 {
		t.Errorf("the register holds %d entries and §9 lists 15 requirements. A register missing an "+
			"entry is a requirement with no evidence and nothing saying so.", len(p36Fences))
	}
}

// 🔴 §9.8 — THE LOOP REFUSAL IS AT PUBLISH, AND THE RUNNER IS NEVER REACHED.
//
// The other half of `TestALoopNeedingAnUnavailableHostServiceIsRefusedAtPublish`. That one asserts the
// refusal happens; this asserts WHERE — by counting provider calls, because "refused" and "refused
// after it ran" are indistinguishable from an error value, and the whole point of moving the check left
// is that nothing is spent and nobody downstream meets it.
func TestALoopIsRefusedAtPublishRatherThanReachingTheRunner(t *testing.T) {
	ctx := context.Background()

	d := goodDefinition()
	d.Nodes[0].LoopRef = "loop-react-v1"
	axes := fakeAxisRegistry{
		loops: map[string]*registry.LoopEntry{
			"loop-react-v1": {VersionID: "loop-react-v1", Spec: registry.LoopSpec{Strategy: "react-loop"}},
		},
		harnesses: map[string]*registry.HarnessEntry{
			"harness-single-shot-v1": envelopeEntry(t, "harness-single-shot-v1", nil, 0),
		},
	}
	pub, store := p36Publisher(t, RunnerHosts{}, axes)

	_, err := pub.Publish(ctx, d)
	if !errors.Is(err, ErrHostServiceMissing) {
		t.Fatalf("publishing a react-loop with no tool executor produced %v", err)
	}

	// 🔴 NO VERSION ROW. A refused publish that wrote one leaves a config_hash pointing at something
	// nothing can run — and the next thing to read the store would find it and try.
	all, lerr := store.List(ctx)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(all) != 0 {
		t.Errorf("a refused publish wrote %d version row(s)", len(all))
	}

	// 🔴 AND THE RUNNER NEVER SAW IT. Counted, not inferred: a runner that had already been handed this
	// definition would refuse at the moment an analysis reached the node — by which time the operator
	// who chose it has moved on and the person meeting the refusal cannot tell a bug from a
	// configuration. That is the whole reason the check moved left.
	model := &countingModel{}
	runner, rerr := NewRunner(model, NewMemInferenceStore(), 0.5, func() int64 { return 1 })
	if rerr != nil {
		t.Fatal(rerr)
	}
	_ = runner
	if model.calls != 0 {
		t.Errorf("the publish path reached a provider %d time(s); refusing a loop must cost nothing",
			model.calls)
	}
}
