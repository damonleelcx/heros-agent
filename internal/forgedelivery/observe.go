package forgedelivery

import (
	"context"
	"fmt"
	"time"
)

// observe.go is the merge-observation mechanism (task 1.3 / 4.4 / contract §3). A merge is recorded
// from an EXPLICIT OBSERVATION — CI-reported by default, the hosted App's webhook when installed —
// NEVER inferred from a pull request closing. The two are distinct facts with distinct next actions:
// a merged change is billable and shipped; a closed-without-merge change is neither.
//
// Gainshare timeliness therefore tracks the customer's own CI/merge signal, which is consistent with
// the credential posture (the default mode needs no platform-held credential to learn a merge
// happened). The failure mode is stated in the contract: a merge with no CI run on the target branch is
// not observed until the App webhook or a reconcile catches it — never guessed.

// MergeObserver appends observed lifecycle events to the delivery record. It performs NO inference: a
// caller that observed a merge calls ObserveMerge; a caller that observed a close-without-merge calls
// ObserveClose. There is no method that turns a close into a merge.
type MergeObserver struct {
	rec Recorder
	now func() time.Time
}

// NewMergeObserver builds an observer over the record.
func NewMergeObserver(rec Recorder) *MergeObserver {
	return &MergeObserver{rec: rec, now: time.Now}
}

// SetClock injects a deterministic clock (tests).
func (o *MergeObserver) SetClock(now func() time.Time) { o.now = now }

// ObserveMerge records that a delivered pull request was merged into the target branch. mergeCommit is
// the observed commit — REQUIRED, because a merged state with no commit would be an inference rather
// than an observation (the schema's CHECK enforces the same). source names where the observation came
// from (e.g. "ci", "app-webhook"), recorded as the actor so an audit can weigh it.
func (o *MergeObserver) ObserveMerge(ctx context.Context, deliveryID, mergeCommit, source string) error {
	if mergeCommit == "" {
		return fmt.Errorf("forgedelivery: a merge observation must carry the merge commit; a merge with no commit is an inference, not an observation")
	}
	head, ok, err := o.rec.Head(ctx, deliveryID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("forgedelivery: no delivery %q to record a merge against", deliveryID)
	}
	return o.rec.Append(ctx, entryFrom(head, StateMerged, o.now(), source, "", mergeCommit))
}

// ObserveClose records that a delivered pull request was closed WITHOUT merging. It records 'closed',
// never 'merged' — the whole point of a distinct method (task 4.4).
func (o *MergeObserver) ObserveClose(ctx context.Context, deliveryID, source, reason string) error {
	head, ok, err := o.rec.Head(ctx, deliveryID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("forgedelivery: no delivery %q to record a close against", deliveryID)
	}
	return o.rec.Append(ctx, entryFrom(head, StateClosed, o.now(), source, reason, ""))
}

// ObserveRevert records that a merged change was subsequently reverted. It is a FURTHER state: the
// 'merged' row stays, so a disputed billed period is answerable as a sequence (task 4.5). revertCommit
// is carried in the reason so the record names what reverted it.
func (o *MergeObserver) ObserveRevert(ctx context.Context, deliveryID, revertCommit, source string) error {
	head, ok, err := o.rec.Head(ctx, deliveryID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("forgedelivery: no delivery %q to record a revert against", deliveryID)
	}
	reason := "reverted"
	if revertCommit != "" {
		reason = "reverted by " + revertCommit
	}
	return o.rec.Append(ctx, entryFrom(head, StateReverted, o.now(), source, reason, ""))
}

// entryFrom builds an appendable entry that carries a delivery's identity from its head into a new
// state, so a state change never has to re-supply the config/target/mode it already established.
func entryFrom(h DeliveryHead, state State, at time.Time, actor, reason, mergeCommit string) Entry {
	return Entry{
		DeliveryID: h.DeliveryID, TenantID: h.TenantID, ConfigHash: h.ConfigHash,
		SourceRevision: h.SourceRevision, Target: h.Target, ForgeRef: h.ForgeRef, Mode: h.Mode,
		State: state, Actor: actor, Reason: reason, MergeCommit: mergeCommit, At: at.UTC(),
	}
}
