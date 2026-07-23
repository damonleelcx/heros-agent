package proposal

import (
	"context"
	"errors"

	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/worktree"
)

// WorktreeBuildChecker is the PRODUCTION build gate (§1b.1, §5.5): it applies a candidate's diff to an
// isolated git worktree/branch — never the user's working tree — and runs the language's build gate.
// The worktree is checked out from a bare clone with no ambient credentials (internal/worktree.Pool),
// which is the "P3 sandbox / no ambient creds" isolation §5.5 requires.
//
// It wraps internal/worktree.Applier, translating its two-channel result (an *Applied describing the
// terminal build state PLUS a typed error) into the proposal engine's BuildResult. Crucially, it
// preserves worktree's load-bearing distinction: a MISSING TOOLCHAIN is an ops failure, returned as an
// error, and must NEVER be laundered into "your diff does not build" — so an unavailable toolchain
// propagates rather than marking the candidate build_failed.
type WorktreeBuildChecker struct {
	Applier *worktree.Applier
}

// Check applies the patch on an isolated worktree and builds it. A genuine build rejection is data
// (Builds=false + the compiler log); a toolchain/baseline failure is an error (not a verdict about the
// candidate).
func (w WorktreeBuildChecker) Check(ctx context.Context, patch *transform.Patch) (BuildResult, error) {
	if w.Applier == nil {
		return BuildResult{}, errors.New("proposal: WorktreeBuildChecker requires a worktree.Applier")
	}
	applied, err := w.Applier.Apply(ctx, patch)
	if err != nil {
		// A build rejection carries BOTH an *Applied and ErrBuildRejected — that is a real "does not
		// build" verdict, not an ops failure. Everything else (missing toolchain, baseline failure) is
		// an ops error that must not be reported as the candidate's fault.
		var rej *worktree.BuildRejection
		if errors.As(err, &rej) || errors.Is(err, worktree.ErrBuildRejected) {
			log := ""
			if applied != nil {
				log = applied.BuildLog
			} else if rej != nil {
				log = rej.Log
			}
			return BuildResult{Builds: false, Log: log}, nil
		}
		return BuildResult{}, err // ops failure: propagate, never mark the candidate build_failed
	}
	if applied == nil {
		return BuildResult{}, errors.New("proposal: worktree.Apply returned no result and no error")
	}
	return BuildResult{Builds: applied.Status == worktree.StatusBuilt, Log: applied.BuildLog}, nil
}
