package variantspec

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// P16 §7 — the acceptance gate.
//
// Two things this file does that no individual suite can:
//
//  1. It pins the ONE-WAY DOOR the whole phase promised not to open: the dimension enum is still
//     {model, prompt, skills, context, tools}. Retrieval is a policy with params, not a `DimRetrieval`.
//  2. It checks that every test the task plan names as its EVIDENCE actually exists in the tree. A plan
//     whose evidence column points at a function nobody wrote is a plan that documents work rather than
//     records it, and the failure is invisible — the named test does not fail, it simply never runs.

// ── task 7.7 — no new Dimension ──────────────────────────────────────────────────────────────────

func TestP16AddsNoDimension(t *testing.T) {
	// P16's claim is that RETRIEVAL did not become a dimension — it is a policy with params. That claim
	// is tested by the retrieval-name check below; the prefix check pins that P16 disturbed none of the
	// five that existed when it landed.
	//
	// 🔴 Not a length equality. P17 appends DimMemory through the same eight-step checklist, on an axis
	// P16 has no opinion about — memory persists ACROSS invocations, context is within one call
	// (P17 decisions.md D2) — and a count assertion would have reported that as a P16 violation.
	want := []Dimension{DimModel, DimPrompt, DimSkills, DimContext, DimTools}
	got := Dimensions()
	if len(got) < len(want) {
		t.Fatalf("the dimension enum has %d members (%v), want at least the %d P16 froze (%v); P16 models "+
			"retrieval as a rag-retrieval POLICY with params precisely so this enum does not have to open "+
			"for retrieval (decisions.md D-1)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dimension %d is %q, want %q", i, got[i], want[i])
		}
	}
	for _, d := range got {
		if strings.Contains(strings.ToLower(string(d)), "retriev") {
			t.Errorf("a retrieval dimension %q exists; retrieval is a context policy, and a second enum "+
				"member would split one axis's identity across two", d)
		}
	}
}

// ── the evidence manifest ────────────────────────────────────────────────────────────────────────

// p16NamedTests is the evidence column of openspec/changes/archive/2026-07-29-p16-context-strategy-optimization/tasks.md,
// transcribed. Each entry is a test the plan claims proves a task; this asserts each one exists.
//
// 🚫 It deliberately does NOT check that they pass — `go test ./...` does that, and a manifest that
// re-ran them would be a second, weaker test runner. What it catches is the failure a green build
// cannot: a task marked done whose named proof was never written.
var p16NamedTests = map[string]string{
	// §1 — the additive attribute
	"TestDropToleranceIsAdditiveToHash": "1.5 / 7.7",
	// §2 — Go materialization
	"TestGoContextMaterializes":                 "2.2",
	"TestContextChangeAppearsInDiff":            "2.3",
	"TestContextOverrideNeverSilentlyDropped":   "2.4 / 7.1",
	"TestGoContextMaterializationDeterministic": "2.5 / 7.3",
	"TestSummarizerRunsHostSideOnly":            "2.6 / 7.3",
	// §3 — the interim refusal
	"TestRefusalNamesOwningPhase":          "3.2 / 7.2",
	"TestSpanEngineContextRefusesNotDrops": "3.3 / 7.2",
	"TestInterimRefusalIsLoudNotSilent":    "3.4 / 7.1",
	// §4 — new policies behind the interface
	"TestHierarchicalSummaryPolicyAddedNoSchemaChange": "4.2",
	"TestStructuredExtractionDropMeasured":             "4.3",
	// §5 — drop as a scored, gated signal
	"TestMaterializedDropRecordedAsSignal":             "5.2",
	"TestProposalPastDropToleranceInadmissible":        "5.3 / 7.4",
	"TestContextReductionLowersEvalTokensNoRegression": "5.4 / 7.6",
	// §6 — retrieval tuning
	"TestRAGTuneProposesChunkAndEmbedding":           "6.2",
	"TestRetrievalVerifiedOnHeldoutSet":              "6.3 / 7.5",
	"TestRetrievalMeasurementDeterministic":          "6.4",
	"TestRetrievalTunePastDropToleranceInadmissible": "6.5 / 7.4",
	"TestAugmentationIsNotDrop":                      "6.6",
}

func TestP16NamedEvidenceExists(t *testing.T) {
	root := filepath.Join("..", "..")
	found := map[string]string{}

	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, m := range regexp.MustCompile(`(?m)^func (Test\w+)\(`).FindAllStringSubmatch(string(b), -1) {
			if _, want := p16NamedTests[m[1]]; want {
				found[m[1]] = path
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}

	for name, task := range p16NamedTests {
		if _, ok := found[name]; !ok {
			t.Errorf("task %s names %s as its evidence, but no such test exists. A named proof that was "+
				"never written does not fail — it simply never runs, so the task reads as done and is not",
				task, name)
		}
	}
}
