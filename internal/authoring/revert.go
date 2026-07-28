package authoring

import (
	"context"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// REVERSAL (task 9.5, FR31, NFR15).
//
// Reverting re-DERIVES from the recorded parent. It does not restore a variant in place, and it does
// not "undo the edits" by applying their inverse.
//
// The distinction is not pedantic. Applying an inverse edit is how an undo quietly becomes a THIRD
// configuration: the inverse of "set the model to X" is "set it back to Y", and if anything else moved
// in between — or if the original edit cleared a field whose prior value nobody kept — the result
// resembles the starting point without being it. Re-deriving from the parent that was recorded at
// submission time cannot drift, because the parent is immutable and it is the thing being returned to.
//
// The acceptance criterion is therefore BYTE-IDENTICAL, not "equivalent". An equivalence check would
// accept exactly the near-miss this design exists to prevent.

// ErrNothingToRevert: no submitted change with that id. Distinct from a refusal — the remedy is a
// different id, not a different change.
var ErrNothingToRevert = errors.New("authoring: no submitted authored change with that id")

// ParentSource returns the spec a recorded variant id denotes. Production reads the variant store;
// the interface keeps reversal testable without one.
type ParentSource interface {
	SpecFor(ctx context.Context, variantID string) (*variantspec.VariantSpec, error)
}

// Reversal is the outcome of an undo: a NEW variant whose hash is the pre-edit one.
type Reversal struct {
	ChangeID string `json:"change_id"`
	// ConfigHash is the hash the revert lands on. It must equal the parent's, byte for byte.
	ConfigHash string `json:"config_hash"`
	// RevertedFrom is the change being undone.
	RevertedFrom string `json:"reverted_from"`
	Entry        Entry  `json:"entry"`
}

// Reverter undoes an authored change by re-deriving its parent.
type Reverter struct {
	// Record is where the change's parent was written down. Reversal reads the RECORDED parent rather
	// than the workflow's current head: undoing a change means returning to what that change departed
	// from, not to wherever the workflow has since travelled.
	Record Recorder
	// Parents resolves a recorded parent variant id back to its spec.
	Parents ParentSource
	// Resolver recomputes the hash, so the byte-identity claim is checked rather than assumed.
	Resolver Resolver
}

// Revert produces the reversal of an authored change and records it.
//
// It VERIFIES the byte-identity claim before recording. A revert that landed on a different hash than
// the parent would be a new configuration wearing the word "undo", and it is refused rather than
// recorded — the one case where failing loudly is better than an approximate restore.
func (rv Reverter) Revert(ctx context.Context, changeID string, actor Actor) (Reversal, error) {
	if rv.Record == nil || rv.Parents == nil || rv.Resolver == nil {
		return Reversal{}, errors.New("authoring: revert requires a record, a parent source, and a resolver")
	}
	history, err := rv.Record.History(ctx, changeID)
	if err != nil {
		return Reversal{}, err
	}
	var submitted *Entry
	for i := range history {
		if history[i].Action == ActionSubmitted {
			submitted = &history[i]
			break
		}
	}
	if submitted == nil {
		return Reversal{}, fmt.Errorf("%w: %s", ErrNothingToRevert, changeID)
	}

	parentSpec, err := rv.Parents.SpecFor(ctx, submitted.ParentVariantID)
	if err != nil {
		return Reversal{}, fmt.Errorf("authoring: reading the recorded parent %q: %w", submitted.ParentVariantID, err)
	}
	// Re-derive: the parent, unchanged, is the reversal. Cloned so the recorded parent is not handed out
	// by reference to something that might edit it.
	reverted := cloneSpec(parentSpec)

	resolved, err := rv.Resolver.Resolve(reverted)
	if err != nil {
		return Reversal{}, fmt.Errorf("authoring: resolving the reverted spec: %w", err)
	}
	if resolved.ConfigHash != submitted.ParentVariantID {
		return Reversal{}, fmt.Errorf(
			"authoring: revert landed on %s but the recorded parent is %s — refusing to record an approximate undo",
			resolved.ConfigHash, submitted.ParentVariantID)
	}

	entry := Entry{
		ChangeID: changeID, Action: ActionReverted,
		TenantID: actor.TenantID, ActorID: actor.ID,
		WorkflowID: submitted.WorkflowID, ParentVariantID: submitted.ParentVariantID,
		ConfigHash: resolved.ConfigHash, Axis: submitted.Axis,
		Origin: string(OriginUser), VerificationState: submitted.VerificationState,
		RevertOf: changeID,
	}
	if err := rv.Record.Append(ctx, entry); err != nil {
		return Reversal{}, fmt.Errorf("authoring: recording the reversal: %w", err)
	}
	return Reversal{ChangeID: changeID, ConfigHash: resolved.ConfigHash, RevertedFrom: changeID, Entry: entry}, nil
}
