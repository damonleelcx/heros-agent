package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/transform"
)

// These tests drive REAL git and a REAL `go build`. That is deliberate: every requirement here is
// about what happens to a repository on disk, and a mocked git would only prove that the mock agrees
// with itself. The one thing this package must guarantee — the user's tree is never touched — is not
// observable without a real tree to not touch.

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required: %v", err) // fail, never skip
	}
}

// newSourceRepo builds a buildable Go repo, committed, and returns its path and HEAD.
//
// It is a real, compiling module because the build gate is the thing under test: a fixture that does
// not build would make every transform "build-rejected" and every gate test vacuously green.
func newSourceRepo(t *testing.T) (dir, rev string) {
	t.Helper()
	requireGit(t)
	dir = t.TempDir()
	write(t, dir, "go.mod", "module example.com/target\n\ngo 1.22\n")
	write(t, dir, "pipeline.go", `package target

// Model is the pinned model at this call site.
const Model = "claude-opus-4-8"

func Classify() string { return Model }
`)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "base"}} {
		if out, err := git(context.Background(), dir, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	out, err := git(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v\n%s", err, out)
	}
	return dir, strings.TrimSpace(out)
}

// write creates a file at a repo-relative path, making its parent directories. Shared with
// verify_test.go, whose fixtures are nested (src/lib.rs, node_modules/.bin/tsc).
func write(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// patchFor builds a Patch that rewrites the model constant to newModel.
func patchFor(t *testing.T, src, rev, configHash, newModel string) *transform.Patch {
	t.Helper()
	orig, err := os.ReadFile(filepath.Join(src, "pipeline.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := strings.Replace(string(orig), `"claude-opus-4-8"`, `"`+newModel+`"`, 1)
	sum := sha256.Sum256([]byte(out))
	return &transform.Patch{
		ConfigHash: configHash, SourceRevision: rev,
		Files:    map[string][]byte{"pipeline.go": []byte(out)},
		Diff:     []byte(fmt.Sprintf("--- a/pipeline.go\n+++ b/pipeline.go\n-const Model = \"claude-opus-4-8\"\n+const Model = %q\n", newModel)),
		DiffHash: hex.EncodeToString(sum[:]),
		Touched:  []transform.TouchedDimension{{NodeID: "n_a", Dim: "model", File: "pipeline.go", Line: 4}},
	}
}

func newApplier(t *testing.T, src string) *Applier {
	t.Helper()
	root := t.TempDir()
	pool, err := NewPool(context.Background(), src, filepath.Join(root, "pool"))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	cache, err := NewCache(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	return NewApplier(pool, GoVerifier{GoBin: goBin()}, cache)
}

func goBin() string {
	if p, err := exec.LookPath("go"); err == nil {
		return p
	}
	return "go"
}

const hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const hashC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

// ── 3.4: isolated application ────────────────────────────────────────────────────────────────────

func TestApply_TransformsAnIsolatedWorktreeOnAVariantBranch(t *testing.T) {
	src, rev := newSourceRepo(t)
	a := newApplier(t, src)

	got, err := a.Apply(context.Background(), patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Status != StatusBuilt {
		t.Fatalf("Status = %q, want built. log:\n%s", got.Status, got.BuildLog)
	}
	if got.Dir == src {
		t.Fatal("the transform was applied to the user's own tree")
	}
	if got.Branch != BranchFor(hashA) {
		t.Errorf("Branch = %q, want %q", got.Branch, BranchFor(hashA))
	}
	applied, err := os.ReadFile(filepath.Join(got.Dir, "pipeline.go"))
	if err != nil {
		t.Fatalf("read applied: %v", err)
	}
	if !strings.Contains(string(applied), `"claude-sonnet-5"`) {
		t.Errorf("the worktree does not contain the transform:\n%s", applied)
	}
	if got.Commit == "" {
		t.Error("no variant commit; rollback needs exactly one commit to revert")
	}
}

// The requirement ADR-001 calls out and PRD §13 makes an exit criterion: "the user's original working
// tree at source_revision is byte-for-byte unchanged."
//
// Asserted by hashing every file in the source repo before and after a full apply+build, rather than
// by checking `git status` — a build that dropped an artifact into the tree would leave git clean if
// it were ignored, and would still be a mutation of the user's directory.
func TestApply_UsersWorkingTreeIsByteForByteUnchanged(t *testing.T) {
	src, rev := newSourceRepo(t)
	a := newApplier(t, src)
	before := hashTree(t, src)

	if _, err := a.Apply(context.Background(), patchFor(t, src, rev, hashA, "claude-sonnet-5")); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	after := hashTree(t, src)
	if before != after {
		t.Errorf("the user's working tree changed:\n before: %s\n after:  %s", before, after)
	}
	if out, err := git(context.Background(), src, "status", "--porcelain"); err != nil || strings.TrimSpace(out) != "" {
		t.Errorf("the user's repo is no longer clean: %q (%v)", out, err)
	}
}

// The pool clones once and shares the object store; a second variant is a checkout, not a copy. If
// this regressed to a clone per variant, P4's fan-out over hundreds of variants would copy the repo
// hundreds of times.
func TestApply_TwoVariantsShareOneMirrorAndGetSeparateWorktrees(t *testing.T) {
	src, rev := newSourceRepo(t)
	a := newApplier(t, src)
	ctx := context.Background()

	one, err := a.Apply(ctx, patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err != nil {
		t.Fatalf("Apply A: %v", err)
	}
	two, err := a.Apply(ctx, patchFor(t, src, rev, hashB, "claude-haiku-4-5"))
	if err != nil {
		t.Fatalf("Apply B: %v", err)
	}
	if one.Dir == two.Dir {
		t.Error("two variants shared a worktree; they would overwrite each other")
	}
	if one.Branch == two.Branch {
		t.Error("two variants shared a branch")
	}
	// Each worktree holds its own variant, simultaneously.
	for _, tc := range []struct{ dir, want string }{{one.Dir, "claude-sonnet-5"}, {two.Dir, "claude-haiku-4-5"}} {
		b, err := os.ReadFile(filepath.Join(tc.dir, "pipeline.go"))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !strings.Contains(string(b), tc.want) {
			t.Errorf("%s should contain %s:\n%s", tc.dir, tc.want, b)
		}
	}
}

// ── 3.5: the build-preserving gate ───────────────────────────────────────────────────────────────

// FR5b: "A transformation that fails to build the target SHALL be rejected before it is proposed or
// run." The patch here is syntactically valid Go that does not compile — a model constant rewritten
// to an undefined identifier, which is exactly what a bad codemod produces.
func TestApply_BuildBreakingTransformIsRejected(t *testing.T) {
	src, rev := newSourceRepo(t)
	a := newApplier(t, src)

	bad := patchFor(t, src, rev, hashA, "x")
	bad.Files["pipeline.go"] = []byte(`package target

const Model = undefinedIdentifier

func Classify() string { return Model }
`)
	got, err := a.Apply(context.Background(), bad)
	if err == nil {
		t.Fatal("a transform that does not compile was accepted")
	}
	if !errors.Is(err, ErrBuildRejected) {
		t.Fatalf("want ErrBuildRejected, got %v", err)
	}
	if got == nil || got.Status != StatusBuildRejected {
		t.Fatalf("the rejection must still produce a record for the UI to render; got %+v", got)
	}
	if got.BuildLog == "" {
		t.Error("a rejection with no build log leaves the user nothing to act on")
	}
	if !strings.Contains(got.BuildLog, "undefinedIdentifier") {
		t.Errorf("the build log should carry the compiler's reason:\n%s", got.BuildLog)
	}
}

// FR5b: the error names the node and dimension whose rewrite failed to build.
func TestApply_BuildRejectionNamesTheNodeAndDimensionTheCompilerBlamed(t *testing.T) {
	src, rev := newSourceRepo(t)
	a := newApplier(t, src)

	bad := patchFor(t, src, rev, hashA, "x")
	// The undefined identifier is on line 4 — the line Touched says the model rewrite landed on.
	bad.Files["pipeline.go"] = []byte(`package target

// Model is the pinned model at this call site.
const Model = undefinedIdentifier

func Classify() string { return Model }
`)
	_, err := a.Apply(context.Background(), bad)
	var rej *BuildRejection
	if !errors.As(err, &rej) {
		t.Fatalf("want a *BuildRejection, got %T: %v", err, err)
	}
	if rej.NodeID != "n_a" || rej.Dim != "model" {
		t.Errorf("rejection names node=%q dim=%q, want n_a/model — the compiler pointed at the "+
			"rewritten line, so attribution should be certain.\nlog:\n%s", rej.NodeID, rej.Dim, rej.Log)
	}
}

// The other half of honest attribution: when the compiler blames a line we never touched, say so
// rather than blaming the only override in the spec because it is the only candidate. A confidently
// wrong attribution sends the user to edit a dimension that was never the problem.
func TestApply_BuildRejectionDoesNotGuessWhenTheFailureIsElsewhere(t *testing.T) {
	src, rev := newSourceRepo(t)
	a := newApplier(t, src)

	bad := patchFor(t, src, rev, hashA, "x")
	// Line 4 (the rewritten line) is fine; the breakage is on line 6, which we never touched.
	bad.Files["pipeline.go"] = []byte(`package target

// Model is the pinned model at this call site.
const Model = "claude-sonnet-5"

func Classify() string { return somethingElseEntirely }
`)
	_, err := a.Apply(context.Background(), bad)
	var rej *BuildRejection
	if !errors.As(err, &rej) {
		t.Fatalf("want a *BuildRejection, got %T: %v", err, err)
	}
	if rej.NodeID != "" || rej.Dim != "" {
		t.Errorf("attribution guessed node=%q dim=%q for a failure on an untouched line", rej.NodeID, rej.Dim)
	}
	if !strings.Contains(rej.Log, "somethingElseEntirely") {
		t.Errorf("the log should still carry the real reason:\n%s", rej.Log)
	}
}

// ── 3.6: the build cache ─────────────────────────────────────────────────────────────────────────

// Task 3.6: "a cache hit skips regeneration + rebuild (supports P4 fan-out)". Proved with a verifier
// that counts, because "it was faster" is not evidence.
type countingVerifier struct {
	inner Verifier
	calls int
}

func (b *countingVerifier) Verify(ctx context.Context, dir string) (Verification, error) {
	b.calls++
	return b.inner.Verify(ctx, dir)
}

func TestApply_CacheHitSkipsTheRebuild(t *testing.T) {
	src, rev := newSourceRepo(t)
	root := t.TempDir()
	pool, err := NewPool(context.Background(), src, filepath.Join(root, "pool"))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	cache, err := NewCache(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	b := &countingVerifier{inner: GoVerifier{GoBin: goBin()}}
	a := NewApplier(pool, b, cache)
	ctx := context.Background()

	first, err := a.Apply(ctx, patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if b.calls != 1 || first.CacheHit {
		t.Fatalf("first apply: builds=%d cacheHit=%v, want 1/false", b.calls, first.CacheHit)
	}

	second, err := a.Apply(ctx, patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err != nil {
		t.Fatalf("Apply (cached): %v", err)
	}
	if b.calls != 1 {
		t.Errorf("the second apply rebuilt (builds=%d); a cache hit must skip the build", b.calls)
	}
	if !second.CacheHit {
		t.Error("CacheHit = false on a repeat variant")
	}
	if second.Dir != first.Dir || second.Commit != first.Commit {
		t.Errorf("the cached hit points somewhere else: %+v vs %+v", second, first)
	}
}

// The cache key includes source_revision, not just config_hash. The same configuration can target two
// commits (source_revision is deliberately not in the hash), and serving one commit's build for the
// other's variant would make P4 score a build that never came from the code it claims to.
func TestCache_KeyIncludesSourceRevisionNotJustConfigHash(t *testing.T) {
	c, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	if err := c.Put(&CacheEntry{ConfigHash: hashA, SourceRevision: "rev1", Dir: t.TempDir(), Status: StatusBuilt}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if got := c.Get(hashA, "rev1"); got == nil {
		t.Fatal("Get missed its own entry")
	}
	if got := c.Get(hashA, "rev2"); got != nil {
		t.Errorf("the same config_hash at a DIFFERENT revision hit the cache: %+v", got)
	}
}

// The cache is evictable (PRD §7), so a recorded worktree can vanish — to a cleanup job, a full disk,
// an operator. Trusting the record would hand the executor a path that is not there.
func TestCache_HitRequiresTheWorktreeToStillExist(t *testing.T) {
	c, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	gone := filepath.Join(t.TempDir(), "evicted")
	if err := c.Put(&CacheEntry{ConfigHash: hashA, SourceRevision: "rev1", Dir: gone, Status: StatusBuilt}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if got := c.Get(hashA, "rev1"); got != nil {
		t.Errorf("a record whose worktree is gone was served as a hit: %+v", got)
	}
}

// A cached rejection stays a rejection: rediscovering that a transform does not compile burns P4's
// fan-out budget to reach the same answer.
func TestApply_CachedRejectionIsStillRejectedWithoutRebuilding(t *testing.T) {
	src, rev := newSourceRepo(t)
	root := t.TempDir()
	pool, _ := NewPool(context.Background(), src, filepath.Join(root, "pool"))
	cache, _ := NewCache(filepath.Join(root, "cache"))
	b := &countingVerifier{inner: GoVerifier{GoBin: goBin()}}
	a := NewApplier(pool, b, cache)
	ctx := context.Background()

	bad := patchFor(t, src, rev, hashA, "x")
	bad.Files["pipeline.go"] = []byte("package target\n\nconst Model = undefinedIdentifier\n")

	if _, err := a.Apply(ctx, bad); !errors.Is(err, ErrBuildRejected) {
		t.Fatalf("want ErrBuildRejected, got %v", err)
	}
	// Two, not one, and the second one is the point: a rejection now also verifies the PRISTINE tree
	// before it blames the diff (ErrBaselineFails). This count was 1 when a rejection was asserted
	// rather than attributed. The test's subject — that a CACHED rejection re-runs nothing — is
	// unchanged and is asserted below.
	const rejectionVerifications = 2 // transformed tree + baseline attribution
	if b.calls != rejectionVerifications {
		t.Fatalf("builds = %d, want %d (transformed + baseline)", b.calls, rejectionVerifications)
	}
	got, err := a.Apply(ctx, bad)
	if !errors.Is(err, ErrBuildRejected) {
		t.Fatalf("a cached rejection must stay rejected, got %v", err)
	}
	if b.calls != rejectionVerifications {
		t.Errorf("the cached rejection was rebuilt (builds=%d, want %d)", b.calls, rejectionVerifications)
	}
	if !got.CacheHit {
		t.Error("CacheHit = false on a cached rejection")
	}
}

// ── 3.10: reviewable diff and clean rollback ─────────────────────────────────────────────────────

// PRD §13: "a single `git revert` of the variant commit restores source_revision byte-for-byte" and
// "no residual edits remain".
func TestRevert_RestoresSourceRevisionByteForByteWithNoResidue(t *testing.T) {
	src, rev := newSourceRepo(t)
	a := newApplier(t, src)
	ctx := context.Background()

	applied, err := a.Apply(ctx, patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	transformed, err := os.ReadFile(filepath.Join(applied.Dir, "pipeline.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(transformed), "claude-opus-4-8") {
		t.Fatal("test bug: the transform did not take")
	}

	if err := a.Revert(ctx, applied); err != nil {
		t.Fatalf("Revert: %v", err)
	}

	// Byte-for-byte back to source_revision.
	reverted, err := os.ReadFile(filepath.Join(applied.Dir, "pipeline.go"))
	if err != nil {
		t.Fatalf("read reverted: %v", err)
	}
	original, err := os.ReadFile(filepath.Join(src, "pipeline.go"))
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if string(reverted) != string(original) {
		t.Errorf("revert did not restore the original bytes:\n got:\n%s\nwant:\n%s", reverted, original)
	}
	// No residue: the worktree is clean, and the tree matches source_revision exactly.
	if out, err := git(ctx, applied.Dir, "status", "--porcelain"); err != nil || strings.TrimSpace(out) != "" {
		t.Errorf("residual edits after revert: %q", out)
	}
	out, err := git(ctx, applied.Dir, "diff", rev, "HEAD", "--stat")
	if err != nil {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("the reverted branch still differs from source_revision:\n%s", out)
	}
}

// One commit, because rollback is "a single git revert". Two would make a rollback two reverts, and a
// partially-reverted variant is a configuration nobody described.
func TestApply_ProducesExactlyOneCommitAboveSourceRevision(t *testing.T) {
	src, rev := newSourceRepo(t)
	a := newApplier(t, src)

	applied, err := a.Apply(context.Background(), patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	out, err := git(context.Background(), applied.Dir, "rev-list", "--count", rev+"..HEAD")
	if err != nil {
		t.Fatalf("rev-list: %v\n%s", err, out)
	}
	if n := strings.TrimSpace(out); n != "1" {
		t.Errorf("the variant branch has %s commits above source_revision, want exactly 1", n)
	}
}

// ADR-001 makes git history the audit trail. That only pays off if the history says what happened.
func TestApply_CommitMessageIsALegibleAuditTrail(t *testing.T) {
	src, rev := newSourceRepo(t)
	a := newApplier(t, src)

	applied, err := a.Apply(context.Background(), patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	msg, err := git(context.Background(), applied.Dir, "log", "-1", "--pretty=%B")
	if err != nil {
		t.Fatalf("log: %v\n%s", err, msg)
	}
	for _, want := range []string{hashA, rev, "node n_a", "dimension model", "pipeline.go:4"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the commit message should record %q so `git log` explains the change:\n%s", want, msg)
		}
	}
}

// A spec with no overrides is the baseline P4 compares against. It must still produce a branch, a
// commit, and a (empty) diff, so every consumer treats it like any other variant.
func TestApply_BaselineWithNoEditsStillProducesAVariantCommit(t *testing.T) {
	src, rev := newSourceRepo(t)
	a := newApplier(t, src)

	applied, err := a.Apply(context.Background(), &transform.Patch{
		ConfigHash: hashB, SourceRevision: rev, Files: map[string][]byte{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Commit == "" {
		t.Error("the baseline got no commit; every consumer would need a special case for it")
	}
	if applied.Status != StatusBuilt {
		t.Errorf("Status = %q, want built (the untransformed baseline compiles)", applied.Status)
	}
}

// A patch path is derived from a discovered call site, not user input — but a traversal would write
// outside the isolated worktree, which is the one thing this package exists to prevent.
func TestApply_RejectsAPatchPathThatEscapesTheWorktree(t *testing.T) {
	src, rev := newSourceRepo(t)
	a := newApplier(t, src)

	evil := &transform.Patch{ConfigHash: hashA, SourceRevision: rev,
		Files: map[string][]byte{"../../escaped.go": []byte("package x")}}
	if _, err := a.Apply(context.Background(), evil); err == nil {
		t.Fatal("a patch writing outside the worktree was applied")
	} else if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("want an escape error, got %v", err)
	}
}

// hashTree hashes every file's path and content under dir, excluding .git (whose loose object and
// index bytes legitimately churn on read).
func hashTree(t *testing.T, dir string) string {
	t.Helper()
	var files []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", rel, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ── 6.2: bounded eviction ────────────────────────────────────────────────────────────────────────

// Every cached variant holds a full worktree. P4 scores hundreds of variants of one base, so an
// unbounded cache is a slow disk-full — and a disk-full mid-fan-out fails runs that have nothing
// wrong with them.
func TestPrune_BoundsTheCacheEvictingLeastRecentlyUsedFirst(t *testing.T) {
	c, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	hashes := []string{hashA, hashB, hashC}
	for _, h := range hashes {
		if err := c.Put(&CacheEntry{ConfigHash: h, SourceRevision: "rev1", Dir: t.TempDir(), Status: StatusBuilt}); err != nil {
			t.Fatalf("Put: %v", err)
		}
		time.Sleep(10 * time.Millisecond) // distinct mtimes, so LRU order is well-defined
	}

	// Touch the OLDEST so it is now the most recently used. If Prune evicted by age it would drop
	// this one — which is exactly the variant P4 is about to ask for again.
	if got := c.Get(hashA, "rev1"); got == nil {
		t.Fatal("Get missed a live entry")
	}

	evicted, err := c.Prune(2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(evicted) != 1 {
		t.Fatalf("evicted %d entries, want 1 (3 cached, bound 2)", len(evicted))
	}
	if evicted[0].ConfigHash != hashB {
		t.Errorf("evicted %s; want %s, the least RECENTLY USED (hashA was just read)",
			evicted[0].ConfigHash[:8], hashB[:8])
	}
	if c.Get(hashB, "rev1") != nil {
		t.Error("the evicted entry is still served")
	}
	for _, h := range []string{hashA, hashC} {
		if c.Get(h, "rev1") == nil {
			t.Errorf("%s was evicted but should have survived the bound", h[:8])
		}
	}
}

// Under the bound, Prune does nothing. An eviction policy that evicts when it does not need to is
// just a rebuild nobody asked for.
func TestPrune_UnderTheBoundEvictsNothing(t *testing.T) {
	c, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	if err := c.Put(&CacheEntry{ConfigHash: hashA, SourceRevision: "rev1", Dir: t.TempDir(), Status: StatusBuilt}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	evicted, err := c.Prune(5)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(evicted) != 0 {
		t.Errorf("evicted %d entries while under the bound", len(evicted))
	}
}

// Nothing is lost by evicting: every entry is reproducible from {config_hash, source_revision}. An
// eviction is a rebuild, never a wrong answer — this is the property that makes the whole cache safe
// to bound.
func TestPrune_AnEvictedVariantIsSimplyRebuilt(t *testing.T) {
	src, rev := newSourceRepo(t)
	root := t.TempDir()
	pool, err := NewPool(context.Background(), src, filepath.Join(root, "pool"))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	cache, err := NewCache(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	b := &countingVerifier{inner: GoVerifier{GoBin: goBin()}}
	a := NewApplier(pool, b, cache)
	ctx := context.Background()

	if _, err := a.Apply(ctx, patchFor(t, src, rev, hashA, "claude-sonnet-5")); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := cache.Prune(0); err != nil { // evict everything
		t.Fatalf("Prune: %v", err)
	}
	got, err := a.Apply(ctx, patchFor(t, src, rev, hashA, "claude-sonnet-5"))
	if err != nil {
		t.Fatalf("Apply after eviction: %v", err)
	}
	if got.CacheHit {
		t.Error("an evicted variant reported a cache hit")
	}
	if b.calls != 2 {
		t.Errorf("builds = %d, want 2 — an evicted variant must rebuild", b.calls)
	}
	if got.Status != StatusBuilt {
		t.Errorf("the rebuilt variant is %q, want built — eviction must cost a rebuild, not a result", got.Status)
	}
}
