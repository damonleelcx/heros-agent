// Package authoring is the second ORIGIN on the optimizer spine: changes a person makes directly,
// rather than ones a catalog operator nominates from a diagnosis (P13 13c, capability
// `authored-change`).
//
// # One spine, two origins
//
// The load-bearing property of this package is what it does NOT contain. There is no authoring-only
// resolve, no authoring-only codemod, and no authoring-only gate. An authored change becomes a
// `proposal.Candidate` and is handed to the SAME `proposal.Compiler` an operator candidate goes
// through, which resolves it, runs `transform.Generate`, and gates it on the build.
//
// That is a deliberate refusal of the cheaper design. A direct "edit and apply" path — build the
// override, emit the rewrite — is dramatically less code, and it is wrong at two levels above
// implementation cost. Every safety property this platform has is a property of that one pipeline: the
// un-apply refusal at resolve, the cross-provider refusal at transform, `GateReorder` before any
// codemod, the drop-tolerance gate before eval spend. A second path does not INHERIT them; it
// re-implements or omits them, and an omission is invisible until a user hits it. Two origins on one
// spine costs an Origin field and a preflight entry point. Two spines costs every future gate being
// added twice, forever.
//
// `TestSingleApplyPathAcrossOrigins` is that paragraph as a machine check: it reads this package's own
// source and fails if anything here reaches for the codemod directly.
//
// # What a user may author, and what they may not
//
// A user may author the CHANGE. A user may not author the EVIDENCE — case selection, held-out splits,
// and seeds stay platform-derived, and a classifier label is an input to what may be authored, never an
// output of it. Nothing in this package accepts any of those as a parameter; the absence is the
// enforcement.
package authoring

import (
	"context"
	"errors"

	"github.com/heros-foreal/agentd/internal/proposal"
)

// Re-exported so a caller works in one vocabulary. These are aliases, not copies: a second definition
// of "who authored this" is a second thing to keep in sync.
type (
	// Origin is who initiated a change (operator | user).
	Origin = proposal.Origin
	// Actor is the acting identity behind a user-originated change.
	Actor = proposal.Actor
)

const (
	OriginOperator = proposal.OriginOperator
	OriginUser     = proposal.OriginUser
)

// ErrNotAuthored guards the one thing Apply is for. It is not a defensive nicety: routing an
// operator-originated candidate through the authoring entry point would let an operator inherit
// authoring's `unverified`-apply affordance, which is precisely the bypass the verification gate
// exists to prevent.
var ErrNotAuthored = errors.New("authoring: Apply requires a user-originated candidate")

// Applier is the shared compile path — `proposal.Compiler` satisfies it. It is an interface only so a
// test can observe that Apply delegates rather than duplicates; production passes the real compiler
// every operator candidate also goes through.
type Applier interface {
	Compile(ctx context.Context, cand proposal.Candidate) (proposal.Compiled, error)
}

// Apply compiles an authored candidate through the shared pipeline.
//
// It is one line of delegation on purpose. Every gate, refusal, and hash an operator candidate meets,
// an authored one meets here — by going to the same place, not by re-listing them.
func Apply(ctx context.Context, c Applier, cand proposal.Candidate) (proposal.Compiled, error) {
	if !cand.Origin.IsUser() {
		return proposal.Compiled{}, ErrNotAuthored
	}
	return c.Compile(ctx, cand)
}
