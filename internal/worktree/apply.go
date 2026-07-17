package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Applier turns a generated Patch into a built, reviewable change on an isolated branch
// (tasks 3.4, 3.5, 3.6, 3.10).
type Applier struct {
	pool     *Pool
	verifier Verifier
	cache    *Cache
}

// NewApplier wires the pool, the gate, and the cache.
//
// The verifier is the LANGUAGE's gate — see VerifierFor, which maps a Discovery language label to it.
// It is a constructor argument rather than something Apply derives, because "which language is this
// workflow" is Discovery's answer (workflow.language), and re-deriving it here from marker files in
// the worktree would be a second source of truth for a question that already has one.
func NewApplier(pool *Pool, verifier Verifier, cache *Cache) *Applier {
	return &Applier{pool: pool, verifier: verifier, cache: cache}
}

// Applied is a transform that reached a terminal build state.
type Applied struct {
	ConfigHash     string
	SourceRevision string
	// Dir is the built worktree. The Executor (task 3.7) runs THIS, which is the whole point of
	// ADR-001: the code under measurement is the code that would ship.
	Dir    string
	Branch string
	// Commit is the variant commit. Rollback is `git revert` of exactly this (task 3.10).
	Commit string
	// Diff is the review artifact and DiffHash its content address (task 2.3).
	Diff     []byte
	DiffHash string
	Status   BuildStatus
	// Strength is what the gate PROVED (ADR-003 decision 2) — `type-checked` or `syntax-checked`. It is
	// carried on the applied change, persisted on the transform row, and surfaced in the review UI,
	// because it is not derivable after the fact: nothing in the diff says whether a compiler stood
	// behind it. Zero (invalid) only when no gate ran, which is not a recordable outcome.
	Strength Strength
	// VerifierTool names exactly what ran — "go build ./...", "pyright", "node --check". Evidence
	// travels with the claim.
	VerifierTool string
	BuildLog     string
	// CacheHit reports that this skipped regeneration and rebuild (task 3.6). Surfaced rather than
	// hidden: P4 needs to know whether a variant's timing includes a build.
	CacheHit bool
}

// BranchFor is the variant branch name for a configuration. Exported because the UI and any PR
// tooling need to name the same branch this package creates.
func BranchFor(configHash string) string {
	return "variant/" + variantspec.Display(configHash)
}

// Apply checks out an isolated worktree at the patch's source_revision, applies the codemod, commits
// it on a variant branch, and runs the build gate.
//
// Order matters and is not arbitrary:
//
//  1. Cache first — a repeat variant is a lookup, not a rebuild (3.6).
//  2. Isolated checkout — from the bare clone, never the user's tree (3.4).
//  3. Apply + commit — so the change is reviewable and revertible as one commit (3.10).
//  4. Verify — and a failure REJECTS the transform before it is proposed or run (3.5).
//
// A build-rejected transform returns a *BuildRejection AND an Applied describing the rejection. Both,
// deliberately: the error is what stops the caller from running it, and the record is what the UI
// renders as its own first-class state (PRD §9: a build-rejected transform must be legible, not an
// unexplained failure).
//
// A MISSING TOOLCHAIN is not that, and the difference is the safety property of this whole function.
// It returns (nil, err) — no Applied, no rejection, no cache entry, no row. "Your transform does not
// build" is a claim about the user's spec, and making it because our container lacks pyright would
// send them to fix a codemod that was never the problem. ADR-003: an ops failure must never be
// laundered into a verdict about the customer's code, and must never degrade into a weaker gate.
func (a *Applier) Apply(ctx context.Context, patch *transform.Patch) (*Applied, error) {
	if patch == nil {
		return nil, fmt.Errorf("worktree: Apply requires a patch")
	}
	if patch.SourceRevision == "" {
		return nil, fmt.Errorf("worktree: patch has no source_revision to check out")
	}
	branch := BranchFor(patch.ConfigHash)

	if e := a.cache.Get(patch.ConfigHash, patch.SourceRevision); e != nil {
		// A cached rejection is still a rejection: re-running the build to rediscover that a transform
		// does not compile burns P4's fan-out budget to reach the same answer.
		out := &Applied{
			ConfigHash: e.ConfigHash, SourceRevision: e.SourceRevision, Dir: e.Dir, Branch: e.Branch,
			Commit: e.Commit, Diff: patch.Diff, DiffHash: e.DiffHash, Status: e.Status,
			Strength: e.Strength, VerifierTool: e.VerifierTool, CacheHit: true,
		}
		if e.Status == StatusBuildRejected {
			return out, &BuildRejection{ConfigHash: e.ConfigHash, Log: "(cached rejection)"}
		}
		return out, nil
	}

	wt, err := a.pool.Acquire(ctx, patch.SourceRevision, branch)
	if err != nil {
		return nil, err
	}

	if err := writePatch(wt.Dir, patch); err != nil {
		return nil, err
	}

	commit, err := a.commit(ctx, wt, patch)
	if err != nil {
		return nil, err
	}

	out := &Applied{
		ConfigHash: patch.ConfigHash, SourceRevision: patch.SourceRevision,
		Dir: wt.Dir, Branch: wt.Branch, Commit: commit, Diff: patch.Diff, DiffHash: patch.DiffHash,
	}

	v, verifyErr := a.verifier.Verify(ctx, wt.Dir)

	// The toolchain check comes before ANY use of the verdict. An unavailable toolchain produced no
	// verdict at all — v is zero — and every line below this point would otherwise treat that zero as
	// an answer: `build-rejected` with an empty strength, cached, recorded, rendered.
	if errors.Is(verifyErr, ErrToolchainUnavailable) {
		return nil, fmt.Errorf("worktree: cannot verify %s at %s: %w",
			patch.ConfigHash, patch.SourceRevision, verifyErr)
	}
	// Defence in depth for a verifier that returns neither a valid strength nor a toolchain error. It
	// is unreachable through the seven gates in this package, and it is checked anyway: the DB's CHECK
	// would catch it later, but as a constraint name on an INSERT four steps away, long after the
	// worktree that could have explained it was reused.
	if !v.Strength.Valid() {
		return nil, fmt.Errorf("worktree: the %T gate returned no verification strength for %s "+
			"(a gate must state what it proved; err=%v)", a.verifier, patch.ConfigHash, verifyErr)
	}
	out.Strength, out.VerifierTool = v.Strength, v.Tool
	out.BuildLog = trimLog(v.Log)

	if verifyErr != nil {
		// Before blaming the diff: does the gate reject the UNMODIFIED source at this revision too?
		//
		// A rejection is the only output of this package that accuses the user's spec of being wrong, so
		// it is the one that has to earn it. The gate failing proves the transformed tree does not pass;
		// it does NOT prove the diff is why. Attributing without asking is how the pipeline came to tell
		// a real user "this transform does not build" while pointing at a file the diff never touched —
		// see ErrBaselineFails.
		//
		// Only on the failure path, deliberately. A passing gate has nothing to attribute, so the common
		// case pays nothing; a rejection is rare and already expensive (a build ran), and being right
		// about who broke it is worth one more checkout of a revision the mirror already has.
		if base, baseErr := a.baselineVerdict(ctx, patch.SourceRevision); baseErr != nil {
			return nil, fmt.Errorf("worktree: %s at %s was rejected, and establishing whether the "+
				"unmodified source is at fault failed: %w", patch.ConfigHash, patch.SourceRevision, baseErr)
		} else if base != nil {
			return nil, &BaselineFailure{
				ConfigHash: patch.ConfigHash, SourceRevision: patch.SourceRevision,
				Tool: base.Tool, BaselineLog: trimLog(base.Log),
			}
		}

		out.Status = StatusBuildRejected
		rej := &BuildRejection{ConfigHash: patch.ConfigHash, Log: out.BuildLog}
		rej.NodeID, rej.Dim = attribute(patch, v.Log)
		// The rejection is cached so a fan-out does not rebuild a known-broken variant, and the
		// worktree is kept so a human can look at what failed — a rejection nobody can inspect is a
		// dead end for whoever has to fix the spec.
		_ = a.cache.Put(&CacheEntry{
			ConfigHash: patch.ConfigHash, SourceRevision: patch.SourceRevision, Dir: wt.Dir,
			Branch: wt.Branch, Commit: commit, DiffHash: patch.DiffHash, Status: StatusBuildRejected,
			Strength: v.Strength, VerifierTool: v.Tool,
		})
		return out, rej
	}

	out.Status = StatusBuilt
	if err := a.cache.Put(&CacheEntry{
		ConfigHash: patch.ConfigHash, SourceRevision: patch.SourceRevision, Dir: wt.Dir,
		Branch: wt.Branch, Commit: commit, DiffHash: patch.DiffHash, Status: StatusBuilt,
		Strength: v.Strength, VerifierTool: v.Tool,
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// writePatch writes the transformed files into the worktree.
//
// It writes the patch's FILES rather than shelling out to `git apply` on the diff. The two are
// equivalent — internal/transform proves it, by asserting that applying the diff with real git
// reproduces these exact bytes — and writing the bytes we already hold cannot fail on a whitespace
// nit in a context line. The diff's job is to be read by a human; the bytes' job is to be built.
func writePatch(dir string, patch *transform.Patch) error {
	for rel, content := range patch.Files {
		// A patch path is derived from a discovered call site, not from user input, but a traversal
		// here would write outside the isolated worktree — the one thing this package exists to
		// prevent — so it is checked rather than assumed.
		clean := filepath.Clean(rel)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("worktree: patch path %q escapes the worktree", rel)
		}
		full := filepath.Join(dir, clean)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			return fmt.Errorf("worktree: create %s: %w", filepath.Dir(clean), err)
		}
		if err := os.WriteFile(full, content, 0o600); err != nil {
			return fmt.Errorf("worktree: write %s: %w", clean, err)
		}
	}
	return nil
}

// commit records the codemod as ONE commit on the variant branch.
//
// One commit, because task 3.10's rollback is "a single `git revert`". Two commits would make a
// rollback two reverts, and a partially-reverted variant is a configuration nobody described.
//
// The message names the config_hash and the dimensions it changed, so `git log` on the variant branch
// is a legible audit trail — ADR-001 makes git history the audit trail, and that only pays off if the
// history says what happened.
func (a *Applier) commit(ctx context.Context, wt *Worktree, patch *transform.Patch) (string, error) {
	if out, err := git(ctx, wt.Dir, "add", "-A"); err != nil {
		return "", fmt.Errorf("worktree: stage: %w\n%s", err, out)
	}
	msg := commitMessage(patch)
	// --allow-empty: a spec with no overrides legitimately produces no edits, and it must still get a
	// variant commit so the branch, the diff (empty), and the revert all exist uniformly. The
	// alternative is a special case in every consumer for "the baseline has no commit".
	out, err := git(ctx, wt.Dir, "-c", "user.email=heros@localhost", "-c", "user.name=heros-agent",
		"commit", "--allow-empty", "-q", "-m", msg)
	if err != nil {
		return "", fmt.Errorf("worktree: commit: %w\n%s", err, out)
	}
	sha, err := git(ctx, wt.Dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("worktree: rev-parse: %w\n%s", err, sha)
	}
	return strings.TrimSpace(sha), nil
}

func commitMessage(patch *transform.Patch) string {
	var b strings.Builder
	fmt.Fprintf(&b, "variant %s: apply configuration\n\n", variantspec.Display(patch.ConfigHash))
	fmt.Fprintf(&b, "config_hash: %s\nsource_revision: %s\n", patch.ConfigHash, patch.SourceRevision)
	if len(patch.Touched) == 0 {
		b.WriteString("\nNo overrides: this is the baseline, applied unchanged.\n")
		return b.String()
	}
	b.WriteString("\nRewritten call sites:\n")
	for _, t := range patch.Touched {
		fmt.Fprintf(&b, "  %s:%d  node %s  dimension %s\n", t.File, t.Line, t.NodeID, t.Dim)
	}
	return b.String()
}

// Revert rolls an applied change back with a single `git revert` (task 3.10).
//
// A revert rather than a reset or a branch delete: ADR-001 makes git history the audit trail, and
// "this variant was tried and undone" is part of that history. A reset would erase the evidence that
// it ever happened, which is exactly what an audit trail is for.
func (a *Applier) Revert(ctx context.Context, applied *Applied) error {
	if applied == nil || applied.Commit == "" {
		return fmt.Errorf("worktree: Revert requires an applied change with a commit")
	}
	out, err := git(ctx, applied.Dir, "-c", "user.email=heros@localhost", "-c", "user.name=heros-agent",
		"revert", "--no-edit", applied.Commit)
	if err != nil {
		return fmt.Errorf("worktree: revert %s: %w\n%s", applied.Commit, err, out)
	}
	// The reverted variant is no longer built, and a cache hit would hand the executor a worktree
	// whose contents no longer match the config_hash it is filed under.
	_ = a.cache.Evict(applied.ConfigHash, applied.SourceRevision)
	return nil
}

// baselineVerdict runs the SAME gate against the unmodified source at rev.
//
// Returns (nil, nil) when the pristine tree PASSES — the gate works here, so a rejection of the
// transformed tree really is the diff's fault and the caller may say so. Returns (v, nil) when the
// pristine tree FAILS, which exonerates the diff. Returns (nil, err) when the question could not be
// answered at all; the caller must not guess an answer from a failure to ask.
//
// 🔴 The default branch of this function is "blame the diff", and that is on purpose. A bug here that
// makes baselines look like they FAIL would suppress every genuine build rejection — the gate would go
// quiet and broken codemods would sail through as `built`. A bug that makes them look like they PASS
// only restores the previous, noisier behaviour. When one direction fails loudly and the other fails
// silently, the silent one must be the one that needs to be earned.
//
// The baseline gets its own branch (`baseline/<rev>`) rather than reusing the variant's: the variant
// worktree already holds the patch, and checking it out again would mean trusting a `git checkout` to
// undo an edit we are in the middle of judging. A separate tree cannot be contaminated by the thing it
// exists to rule out.
func (a *Applier) baselineVerdict(ctx context.Context, rev string) (*Verification, error) {
	wt, err := a.pool.Acquire(ctx, rev, "baseline/"+sanitize(rev))
	if err != nil {
		return nil, fmt.Errorf("check out pristine %s: %w", rev, err)
	}
	// Released unconditionally: a baseline tree is pure evidence-gathering and holds nothing anyone
	// needs afterwards, unlike a rejected variant worktree (kept so a human can inspect what failed).
	defer func() { _ = wt.Release(ctx) }()

	v, verifyErr := a.verifier.Verify(ctx, wt.Dir)
	switch {
	case errors.Is(verifyErr, ErrToolchainUnavailable):
		// The gate could not run at all. That is already the loudest, most specific error available —
		// re-labelling it a baseline failure would replace "install pyright" with "something is wrong
		// with this revision", which is strictly less actionable and not true.
		return nil, verifyErr
	case verifyErr != nil:
		return &v, nil // pristine fails -> the diff is exonerated
	default:
		return nil, nil // pristine passes -> the gate is trustworthy here
	}
}

// Release removes an applied change's worktree and branch, and drops its cache record.
func (a *Applier) Release(ctx context.Context, applied *Applied) error {
	if applied == nil {
		return nil
	}
	_ = a.cache.Evict(applied.ConfigHash, applied.SourceRevision)
	a.pool.mu.Lock()
	defer a.pool.mu.Unlock()
	return a.pool.remove(ctx, applied.Dir, applied.Branch)
}
