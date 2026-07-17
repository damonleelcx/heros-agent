package worktree

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// A rejection must be EARNED, not asserted (ErrBaselineFails)
//
// These tests exist because of a real failure against a real repository, not a hypothetical. The
// worker's default python3 was 3.9; the target used 3.10+ `match`; py_compile failed on a file the
// diff never touched; and the pipeline told the user "this transform does not build, so it was never
// proposed and never run" while pointing at valid Python. rejected_node_id was empty, because there
// was no node to blame.
//
// Nothing in the fixture suite could catch that: every fixture here is a tiny tree the worker's own
// toolchain parses trivially, so the gate never fails for a reason that isn't the diff. That is the
// blind spot these tests close — the "本地跑通 ≠ 交付闭环" shape, where both halves are correct and the
// seam between them is not.
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// staleGateVerifier models a gate that cannot parse the repository at all — an interpreter older than
// the language the source is written in. It rejects EVERY tree it is handed, pristine or transformed,
// which is exactly what makes it indistinguishable from "the diff is broken" to a caller that never
// asks about the baseline.
//
// It reports a plausible, specific-looking diagnostic naming a file the patch never touches, because
// that is what made the real bug so convincing: the rejection came with evidence, and the evidence was
// about somebody else's line.
type staleGateVerifier struct {
	tool  string
	calls int
}

func (v *staleGateVerifier) Verify(_ context.Context, _ string) (Verification, error) {
	v.calls++
	return Verification{
		Strength: StrengthSyntaxChecked,
		Tool:     v.tool,
		Log:      "  File \"tools/untouched_by_the_diff.py\", line 1891\n    match kind:\n          ^\nSyntaxError: invalid syntax",
	}, ErrBuildRejected
}

// TestApply_AGateThatRejectsThePristineTreeDoesNotBlameTheDiff is the F2 regression.
//
// The gate rejects. The old code recorded build-rejected and stopped. The only question that separates
// "your spec is wrong" from "our worker cannot read your repository" is whether the UNMODIFIED source
// passes — so that question now gets asked.
func TestApply_AGateThatRejectsThePristineTreeDoesNotBlameTheDiff(t *testing.T) {
	src, rev := newSourceRepo(t)
	root := t.TempDir()
	pool, err := NewPool(context.Background(), src, filepath.Join(root, "pool"))
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	cache, err := NewCache(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	v := &staleGateVerifier{tool: "python3.9 -m py_compile"}
	a := NewApplier(pool, v, cache)
	ctx := context.Background()

	// A patch that is perfectly fine. The gate will reject it anyway — for reasons that have nothing to
	// do with it.
	p := patchFor(t, src, rev, hashA, "claude-sonnet-4-5")

	out, err := a.Apply(ctx, p)

	var bf *BaselineFailure
	if !errors.As(err, &bf) {
		t.Fatalf("want *BaselineFailure, got %T: %v", err, err)
	}
	if errors.Is(err, ErrBuildRejected) {
		t.Error("the diff was blamed for a gate that rejects the unmodified source too")
	}
	if !errors.Is(err, ErrBaselineFails) {
		t.Error("BaselineFailure must unwrap to ErrBaselineFails so callers can match on the kind")
	}
	if out != nil {
		t.Errorf("a run that could not be judged must yield no Applied record, got %+v", out)
	}

	// The evidence has to travel with the claim: an operator reading this needs the pristine tree's own
	// output, because that output is the proof the diff is not at fault.
	if !strings.Contains(bf.BaselineLog, "untouched_by_the_diff.py") {
		t.Errorf("BaselineLog does not carry the gate's output on the pristine tree: %q", bf.BaselineLog)
	}
	if bf.Tool != v.tool {
		t.Errorf("Tool = %q, want %q — the operator checks the version of THAT", bf.Tool, v.tool)
	}
	if bf.SourceRevision != rev || bf.ConfigHash != hashA {
		t.Errorf("BaselineFailure does not identify the run: %+v", bf)
	}

	// Nothing may be cached. A cached "rejection" here would make the wrong verdict permanent: every
	// later Apply of this config_hash would short-circuit to a rejection that was never true, and fixing
	// the worker's toolchain would not clear it.
	if e := cache.Get(hashA, rev); e != nil {
		t.Errorf("an unjudgeable run was cached as %q — a wrong verdict must not outlive the run", e.Status)
	}
}

// TestApply_ARealBuildRejectionSurvivesTheBaselineCheck is the red-check's twin, and the more important
// of the two.
//
// The baseline check's failure mode is that it swallows genuine rejections — the gate goes quiet and
// broken codemods ship as `built`. That is silent; the bug it fixes is loud. So the guard only earns
// its place if a real rejection still gets through it: pristine PASSES, transformed FAILS, verdict is
// unchanged from before the guard existed.
func TestApply_ARealBuildRejectionSurvivesTheBaselineCheck(t *testing.T) {
	src, rev := newSourceRepo(t)
	root := t.TempDir()
	pool, err := NewPool(context.Background(), src, filepath.Join(root, "pool"))
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	cache, err := NewCache(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	a := NewApplier(pool, GoVerifier{GoBin: goBin()}, cache)
	ctx := context.Background()

	bad := patchFor(t, src, rev, hashB, "x")
	bad.Files["pipeline.go"] = []byte("package target\n\nconst Model = undefinedIdentifier\n")

	out, err := a.Apply(ctx, bad)

	if !errors.Is(err, ErrBuildRejected) {
		t.Fatalf("a genuinely build-breaking diff must still be rejected, got %T: %v", err, err)
	}
	if errors.Is(err, ErrBaselineFails) {
		t.Fatal("the baseline check swallowed a real rejection — the gate has gone silent")
	}
	if out == nil || out.Status != StatusBuildRejected {
		t.Fatalf("want a build-rejected record, got %+v", out)
	}
	// Deliberately not asserted here: the node attribution. This fixture replaces the file wholesale, so
	// the compiler's line falls outside patch.Touched and attribute() correctly declines to guess — that
	// is its own behaviour, tested elsewhere, and pinning it here would make this test fail for a reason
	// that has nothing to do with the baseline check.
	if out.Strength != StrengthTypeChecked {
		t.Errorf("Strength = %q, want %q — a real rejection still records what the gate proved",
			out.Strength, StrengthTypeChecked)
	}
}

// TestApply_AMissingToolchainStaysAToolchainErrorThroughTheBaselineCheck.
//
// Both errors mean "no verdict", so they are easy to collapse — and collapsing them costs the operator
// the only actionable sentence in either. "Install pyright" is a fix; "this revision may not pass its
// own gate" is a shrug. The baseline check must not downgrade the specific into the vague.
func TestApply_AMissingToolchainStaysAToolchainErrorThroughTheBaselineCheck(t *testing.T) {
	src, rev := newSourceRepo(t)
	root := t.TempDir()
	pool, err := NewPool(context.Background(), src, filepath.Join(root, "pool"))
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	cache, err := NewCache(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	// A Go gate pointed at an interpreter that does not exist: the tool is absent, not stale.
	a := NewApplier(pool, GoVerifier{GoBin: "definitely-not-a-real-go-binary-9f8e7d"}, cache)

	_, err = a.Apply(context.Background(), patchFor(t, src, rev, hashA, "claude-sonnet-4-5"))

	if !errors.Is(err, ErrToolchainUnavailable) {
		t.Fatalf("want ErrToolchainUnavailable, got %T: %v", err, err)
	}
	if errors.Is(err, ErrBaselineFails) {
		t.Error("a missing toolchain was relabelled a baseline failure, replacing 'install X' with a shrug")
	}
}
