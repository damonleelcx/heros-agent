package authoring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// SUBMIT — the moment a draft becomes a change (tasks 9.4, 9.6, 9.8; FR25, FR28, FR30).
//
// Three things happen here and nowhere else: the concurrency check, the compile through the SHARED
// pipeline, and the append-only record. Everything else a submission appears to do is delegation.

var (
	// ErrStaleDraft: the parent moved while this draft was being edited. It NAMES the parent, because
	// "conflict" without a subject leaves the author guessing which of their edits is now questionable.
	//
	// 🚫 Never resolved by overwriting. Two people editing one parent produce two variants; a lost
	// update is indistinguishable, from the outside, from the platform discarding somebody's work.
	ErrStaleDraft = errors.New("authoring: the parent variant advanced while this draft was open")
	// ErrNotAdmissible: submission was attempted for a draft preflight did not admit. Submitting past a
	// refusal is exactly the bypass this feature must not have.
	ErrNotAdmissible = errors.New("authoring: draft is not admissible")
	// ErrNotEntitled / ErrNotPermitted: the plan does not carry authoring, or this identity may not
	// author. They are SEPARATE errors because they are separate remedies — one is a purchase, the other
	// is an administrator — and collapsing them sends half of users to the wrong place.
	ErrNotEntitled  = errors.New("authoring: this plan does not include authoring")
	ErrNotPermitted = errors.New("authoring: this identity may not author changes")
)

// HeadSource reports the current head variant of a workflow — what the parent looks like NOW, as
// against what it looked like when the draft was started.
type HeadSource interface {
	Head(ctx context.Context, workflowID string) (variantID string, err error)
}

// Authorizer answers whether this actor, on this plan, may author at all. It is consulted BEFORE a
// draft is submitted, so an unentitled identity never creates a draft, a variant, or a diff.
//
// 🚫 It is not consulted about REFUSALS, and it has no way to affect one. Authorization decides whether
// you may author; it never decides whether what you authored can be materialized.
type Authorizer interface {
	MayAuthor(ctx context.Context, actor Actor) error
}

// Submission is the outcome of a successful submit.
type Submission struct {
	ChangeID   string            `json:"change_id"`
	ConfigHash string            `json:"config_hash"`
	Compiled   proposal.Compiled `json:"-"`
	Entry      Entry             `json:"entry"`
}

// Submitter turns an admissible draft into a recorded, compiled authored change.
//
// 🚫 As with Preflighter: no Force, no Override, no SkipPreflight. The absence is the enforcement.
type Submitter struct {
	// Preflight is re-run at submit. Yes, again — the surface already ran it, and the surface's answer
	// is a UI convenience computed at some earlier moment. Trusting it here would make the gate a
	// client-side check, which is not a gate.
	Preflight Preflighter
	// Applier is the shared compile path (proposal.Compiler).
	Applier Applier
	// Head reports the workflow's current head, for the concurrency check.
	Head HeadSource
	// Record is the append-only history. Required: an authored change nobody recorded is an
	// unattributable edit, which is the state this feature exists to end.
	Record Recorder
	// Auth gates who may author. Required.
	Auth Authorizer
}

// Submit runs the checks in the order whose failures cost least, and stops at the first.
//
// Authorization first (it needs no work to decide), then concurrency (a cheap comparison that
// invalidates everything downstream), then preflight, then the shared compile, then the record. A
// submission that fails leaves nothing behind — no draft promoted, no variant, no diff, no row.
func (s Submitter) Submit(ctx context.Context, d Draft, parent *variantspec.VariantSpec) (Submission, error) {
	if s.Record == nil {
		return Submission{}, errors.New("authoring: submit requires an append-only record")
	}
	if s.Auth == nil {
		return Submission{}, errors.New("authoring: submit requires an authorizer")
	}
	if err := s.Auth.MayAuthor(ctx, d.Actor); err != nil {
		return Submission{}, err
	}

	// Concurrency. The draft's token is what the head looked like when editing began.
	if s.Head != nil {
		head, err := s.Head.Head(ctx, d.WorkflowID)
		if err != nil {
			return Submission{}, fmt.Errorf("authoring: reading the workflow head: %w", err)
		}
		if head != d.ConcurrencyToken {
			return Submission{}, fmt.Errorf("%w: parent %q is now %q", ErrStaleDraft, d.ConcurrencyToken, head)
		}
	}

	res, err := s.Preflight.Preflight(ctx, d, parent)
	if err != nil {
		return Submission{}, err
	}
	if !res.Admissible() {
		return Submission{}, fmt.Errorf("%w: %s (%s)", ErrNotAdmissible, res.Verdict, res.Refusal.Cause)
	}

	spec, err := d.Derive(parent)
	if err != nil {
		return Submission{}, err
	}

	// The SHARED path. Not a copy of it, not a subset of it — the same compiler an operator candidate
	// goes through, reached through the one delegation point in this package.
	cand := d.ToCandidate(spec, primaryNode(d), res.Dimensions)
	compiled, err := Apply(ctx, s.Applier, cand)
	if err != nil {
		return Submission{}, err
	}

	changeID := ChangeID(d.WorkflowID, res.ConfigHash, d.Actor.ID)
	entry := Entry{
		ChangeID: changeID, Action: ActionSubmitted,
		TenantID: d.Actor.TenantID, ActorID: d.Actor.ID,
		WorkflowID: d.WorkflowID, ParentVariantID: d.ParentVariantID,
		ConfigHash: res.ConfigHash, Axis: strings.Join(res.Dimensions, ","),
		DiffRef: compiled.DiffHash, Origin: string(OriginUser),
		ForkedFromProposal: d.ForkedFromProposal,
		// 🔴 Unverified until the harness runs. This is the default and there is no parameter that
		// changes it — a submission cannot assert its own quality.
		VerificationState: StateUnverified,
	}
	if err := s.Record.Append(ctx, entry); err != nil {
		return Submission{}, fmt.Errorf("authoring: recording the change: %w", err)
	}

	return Submission{ChangeID: changeID, ConfigHash: res.ConfigHash, Compiled: compiled, Entry: entry}, nil
}

// ChangeID is the deterministic identity of one authored change: same workflow, same resulting
// configuration, same author → same id. Deterministic rather than random so a retried submission after a
// dropped response collides with the row it already wrote instead of creating a second one.
func ChangeID(workflowID, configHash, actorID string) string {
	sum := sha256.Sum256([]byte(workflowID + "\x00" + configHash + "\x00" + actorID))
	return "ac_" + hex.EncodeToString(sum[:])[:24]
}

func primaryNode(d Draft) string {
	nodes := d.TouchedNodes()
	if len(nodes) == 1 {
		return nodes[0]
	}
	// A multi-node draft has no single subject, and naming one arbitrarily would make the record read as
	// though the others were untouched.
	return ""
}
