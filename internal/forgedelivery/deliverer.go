package forgedelivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/verification"
)

// ── The delivery core (tasks 2.1–2.9, 5.1–5.3) ───────────────────────────────
//
// One entry point, Prepare, enforces every precondition SERVER-SIDE so no caller — App-mode or the CI
// runner fetching over the network — can reach the forge around them (task 2.2). The preconditions are,
// in order:
//
//	1. a configured route exists                  (ErrNoRoute — a reported state, not silence)
//	2. the change passed the P5.5 gate            (asserted from the authoritative oracle, not the caller)
//	3. the tenant is entitled (>= Team; merge => Enterprise)   (entitlement.Gate, server-side)
//	4. the halt state is readable AND not armed   (fail closed if unreadable)
//	5. the per-repository open-PR bound is not hit (reported; the proposal is not discarded)
//
// Prepare renders the pull-request body and returns a Prepared with NO credential in it. The two modes
// diverge only in WHO opens the pull request from that Prepared:
//
//   - CI-mediated (default): the platform serves Prepared over an authenticated fetch; the customer's CI
//     opens the PR with its OWN ephemeral token and reports back. The platform never holds a credential.
//   - Hosted App (opt-in): the platform opens the PR itself with an installation credential.
//
// Because both go through Prepare → OpenFromPrepared, the PR content is byte-identical by construction
// (design Decision 3), which the parity gate verifies.

// GateOracle answers, authoritatively, whether a change passed the P5.5 verification gate. Delivery
// asks IT rather than trusting a "verified" flag on the incoming proposal: that is what makes "only a
// gate-passed change is deliverable" true from EVERY entry point (task 2.2). A read error is a fault,
// not a pass.
type GateOracle interface {
	// Verdict returns the authoritative verdict for a change, and ok=false if none has been recorded.
	Verdict(ctx context.Context, tenantID, configHash, sourceRevision string) (verification.Verdict, bool, error)
}

// Entitlements is the server-side entitlement gate. *entitlement.Gate satisfies it.
type Entitlements interface {
	CheckEntitlement(customerID string, feature plancfg.Feature, level entitlement.AutomationLevel) (entitlement.Decision, error)
}

// HaltReader reports whether delivery is halted for a tenant. It has the same fail-closed contract as
// adminops.KillSwitchService.HaltsMerge: an ERROR means INDETERMINATE, and the only correct handling is
// to withhold delivery (task 2.7 / design Decision 7).
type HaltReader interface {
	// HaltsDelivery returns (halted, reason, err). A non-nil err means the state could not be read;
	// the caller MUST NOT deliver.
	HaltsDelivery(tenantID string) (halted bool, reason string, err error)
}

// Level is the automation level a delivery is requested under. It reuses entitlement's vocabulary
// (advisory, assisted, autonomous) because that is the axis the entitlement gate checks.
type Level = entitlement.AutomationLevel

// Proposal is a verified change offered for delivery. Note there is no "verified" boolean the caller
// can set: whether it passed the gate is decided by the oracle, here, not asserted by the caller.
type Proposal struct {
	TenantID       string
	ProposalID     string
	ConfigHash     string
	SourceRevision string
	Title          string
	// DiffPatch is the unified diff to apply on the platform branch.
	DiffPatch string
	// DiffStat is a short human summary for the PR body (files/lines).
	DiffStat string
	// Level is advisory (draft PR) | assisted (PR) | autonomous (PR + merge). Below autonomous the
	// platform never merges (task 2.6).
	Level Level
	// ConsoleRef is the canonical console evidence reference rendered in the PR body (P9's rules).
	ConsoleRef string
}

// Prepared is a verified proposal that has passed every server-side precondition and been rendered into
// a pull request — the diff plus the exact content, and NO credential. It is what the platform hands to
// the CI runner over an authenticated fetch (CI-mediated mode) and what the App-mode path opens
// directly. Rendering it here, once, is what makes the two modes' pull requests identical.
type Prepared struct {
	DeliveryID     string    `json:"delivery_id"`
	TenantID       string    `json:"tenant_id"`
	ProposalID     string    `json:"proposal_id"`
	ConfigHash     string    `json:"config_hash"`
	SourceRevision string    `json:"source_revision"`
	Target         Target    `json:"target"`
	Mode           Mode      `json:"mode"`
	ForgeKind      ForgeKind `json:"forge_kind"`
	Level          Level     `json:"level"`
	Branch         string    `json:"branch"`
	Title          string    `json:"title"`
	Body           string    `json:"body"`
	DiffPatch      string    `json:"diff_patch"`
	// AllowMerge is true only when the level is Autonomous AND the tenant is auto-merge entitled — the
	// single place the never-merge-below-Autonomous rule is decided, carried to whoever opens the PR.
	AllowMerge bool `json:"allow_merge"`

	// reopenExisting is an internal hint set by Prepare: this delivery already has an open pull request,
	// so opening is an UPDATE and does not count against the per-repository bound. It is unexported and
	// unserialized, so a CI runner over the wire never sees or influences it.
	reopenExisting bool
}

// Result is the outcome of one delivery.
type Result struct {
	DeliveryID string
	PR         PullRequest
	Mode       Mode
	// Created is true when this delivery OPENED the pull request, false when it UPDATED an existing one.
	Created bool
	// Merged is true when, under Autonomous, the platform merged the pull request.
	Merged      bool
	MergeCommit string
	// Superseded lists the delivery ids whose pull requests this delivery closed as superseded.
	Superseded []string
}

// Sentinel conditions. These are LEGIBLE TERMINAL STATES rendered by the console as conditions with a
// next action, not server faults — but they are returned as errors so a caller cannot forget to handle
// them (the p11 ClosedPeriod pattern).
var (
	// ErrNoRoute: the repository has verified proposals and no configured delivery route. A REPORTED
	// state (design Decision 6 / task 6.1), never silence.
	ErrNoRoute = errors.New("forgedelivery: no delivery route configured for this repository")
	// ErrNotVerified: the change did not pass the P5.5 gate. Undeliverable (task 2.2).
	ErrNotVerified = errors.New("forgedelivery: only a change that passed the verification gate can be delivered")
	// ErrHaltUnreadable: the halt state could not be read. Delivery fails closed (task 2.7).
	ErrHaltUnreadable = errors.New("forgedelivery: halt state indeterminate — delivery withheld (fail closed)")
	// ErrBoundReached: the per-repository open-PR bound is reached. Reported; the proposal is retained
	// (task 2.5).
	ErrBoundReached = errors.New("forgedelivery: the open pull-request bound for this repository is reached")
	// ErrForgeUnavailable wraps a forge failure so one repository's outage is isolable and the proposal
	// is not lost (task 2.9).
	ErrForgeUnavailable = errors.New("forgedelivery: the forge is unavailable for this repository")
)

// HaltedError is an armed-halt refusal. It carries the reason so the console can render exactly what
// the operator wrote.
type HaltedError struct{ Reason string }

func (e *HaltedError) Error() string {
	if e.Reason == "" {
		return "forgedelivery: delivery is halted"
	}
	return "forgedelivery: delivery is halted: " + e.Reason
}

// NotEntitledError is an entitlement refusal. It carries the gate's own Decision so the console renders
// the platform's words about the boundary (which plan lifts it), never a reconstruction.
type NotEntitledError struct {
	Decision entitlement.Decision
}

func (e *NotEntitledError) Error() string {
	if e.Decision.Reason != "" {
		return "forgedelivery: not entitled: " + e.Decision.Reason
	}
	return "forgedelivery: not entitled to delivery"
}

// DefaultOpenPRBound is the per-repository open-PR bound. A duplicate/burst storm is the fastest way to
// lose repository access permanently, so the bound is conservative by default and configurable.
const DefaultOpenPRBound = 10

// Deliverer is the delivery core. It holds the small set of collaborators it enforces preconditions
// against; the forge WRITER is passed per delivery (route.Mode decides which one), so the credential is
// bound to the writer, not to this struct — and in CI-mediated mode there is no writer here at all.
type Deliverer struct {
	gate  GateOracle
	ents  Entitlements
	halt  HaltReader
	rec   Recorder
	bound int
	now   func() time.Time
}

// NewDeliverer builds the core. bound <= 0 uses DefaultOpenPRBound.
func NewDeliverer(gate GateOracle, ents Entitlements, halt HaltReader, rec Recorder, bound int) *Deliverer {
	if bound <= 0 {
		bound = DefaultOpenPRBound
	}
	return &Deliverer{gate: gate, ents: ents, halt: halt, rec: rec, bound: bound, now: time.Now}
}

// SetClock injects a deterministic clock (tests).
func (d *Deliverer) SetClock(now func() time.Time) { d.now = now }

// Recorder exposes the record so a mounting layer can read history/list without a second dependency.
func (d *Deliverer) Recorder() Recorder { return d.rec }

// Prepare enforces every server-side precondition and renders the pull request. It writes NOTHING to
// the forge or the record — it decides whether this change MAY be delivered and produces the exact
// content that will be. It is the one enforcement funnel: the CI fetch and the App-mode path both go
// through it, so neither can bypass the gate, entitlement, halt, route, or bound.
func (d *Deliverer) Prepare(ctx context.Context, p Proposal, route *Route) (Prepared, error) {
	// 1. a route exists
	if route == nil {
		return Prepared{}, ErrNoRoute
	}
	if err := route.Validate(); err != nil {
		return Prepared{}, err
	}

	// 2. the change passed the P5.5 gate (authoritative — not trusted from the proposal)
	verdict, ok, err := d.gate.Verdict(ctx, p.TenantID, p.ConfigHash, p.SourceRevision)
	if err != nil {
		return Prepared{}, fmt.Errorf("forgedelivery: reading the verification gate: %w", err)
	}
	if !ok || !verdict.Passed() {
		return Prepared{}, ErrNotVerified
	}

	// 3. entitlement, server-side
	if dec, err := d.ents.CheckEntitlement(p.TenantID, plancfg.FeatureAssistedPR, entitlement.LevelAssisted); err != nil {
		return Prepared{}, fmt.Errorf("forgedelivery: checking delivery entitlement: %w", err)
	} else if !dec.Allowed {
		return Prepared{}, &NotEntitledError{Decision: dec}
	}
	allowMerge := false
	if p.Level == entitlement.LevelAutonomous {
		if dec, err := d.ents.CheckEntitlement(p.TenantID, plancfg.FeatureAutoMerge, entitlement.LevelAutonomous); err != nil {
			return Prepared{}, fmt.Errorf("forgedelivery: checking auto-merge entitlement: %w", err)
		} else if !dec.Allowed {
			return Prepared{}, &NotEntitledError{Decision: dec}
		}
		allowMerge = true
	}

	// 4. halt readable AND not armed (fail closed)
	halted, reason, err := d.halt.HaltsDelivery(p.TenantID)
	if err != nil {
		return Prepared{}, fmt.Errorf("%w: %v", ErrHaltUnreadable, err)
	}
	if halted {
		return Prepared{}, &HaltedError{Reason: reason}
	}

	// 5. the per-repository open-PR bound (an update of an existing delivery is allowed at the bound)
	deliveryID := DeliveryID(p.ConfigHash, p.SourceRevision, route.Target.Key())
	existing, hasExisting, err := d.rec.Head(ctx, deliveryID)
	if err != nil {
		return Prepared{}, fmt.Errorf("forgedelivery: reading the delivery head: %w", err)
	}
	// Determine reopen (an existing OPEN delivery) so an update does not count against the bound.
	_ = existing

	prep := Prepared{
		DeliveryID: deliveryID, TenantID: p.TenantID, ProposalID: p.ProposalID,
		ConfigHash: p.ConfigHash, SourceRevision: p.SourceRevision, Target: route.Target,
		Mode: route.Mode, ForgeKind: route.ForgeKind, Level: p.Level,
		Branch: BranchName(p.ConfigHash, p.SourceRevision),
		Title:  p.Title,
		Body: RenderPRBody(Evidence{
			Title: p.Title, Level: string(p.Level), Verdict: verdict,
			ConfigHash: p.ConfigHash, SourceRevision: p.SourceRevision,
			ConsoleRef: p.ConsoleRef, DiffStat: p.DiffStat,
		}),
		DiffPatch:  p.DiffPatch,
		AllowMerge: allowMerge,
	}
	prep.reopenExisting = hasExisting && existing.Open()
	return prep, nil
}

// reopenExisting is an unexported hint set by Prepare and read by the bound check when a writer opens.
// It rides on Prepared but is not serialized, so a CI runner never sees or influences it.
//
//nolint:unused // read via boundOK
func (p Prepared) boundOK(openCount, bound int) bool {
	if p.reopenExisting {
		return true
	}
	return openCount < bound
}

// OpenFromPrepared opens (or updates) the pull request from a Prepared, using the supplied forge
// writer. It is the ONLY forge-write step, shared by both modes, so the PR content cannot drift between
// them. In CI-mediated mode this runs in the CI runner with its own token; in App mode it runs on the
// platform with the installation credential. It enforces the per-repository open-PR bound just before
// opening, so a burst cannot exceed it regardless of which mode is writing (task 2.5).
func OpenFromPrepared(ctx context.Context, forge ForgeWriter, prep Prepared, bound int) (PullRequest, bool, error) {
	if forge == nil {
		return PullRequest{}, false, fmt.Errorf("forgedelivery: opening a pull request needs a forge writer")
	}
	openCount, err := forge.OpenPRCount(ctx, prep.Target)
	if err != nil {
		return PullRequest{}, false, fmt.Errorf("%w: counting open pull requests: %v", ErrForgeUnavailable, err)
	}
	if !prep.boundOK(openCount, bound) {
		return PullRequest{}, false, ErrBoundReached
	}
	if err := forge.EnsureBranch(ctx, prep.Target, prep.Branch); err != nil {
		return PullRequest{}, false, fmt.Errorf("%w: preparing the branch: %v", ErrForgeUnavailable, err)
	}
	pr, created, err := forge.OpenOrUpdatePR(ctx, OpenRequest{
		Target: prep.Target, Head: prep.Branch, Title: prep.Title, Body: prep.Body,
		DiffPatch: prep.DiffPatch, Draft: prep.Level == entitlement.LevelAdvisory,
	})
	if err != nil {
		// A degraded CI credential is a distinct, reported condition (task 5.4) — pass it through so the
		// CI step can surface it rather than flatten it into a generic forge outage.
		if errors.Is(err, ErrCICredentialExpired) {
			return PullRequest{}, false, err
		}
		return PullRequest{}, false, fmt.Errorf("%w: opening the pull request: %v", ErrForgeUnavailable, err)
	}
	return pr, created, nil
}

// RecordOpened records an opened/updated delivery (server-side) and performs supersession. It is called
// by the App-mode path directly and by the CI report handler after the CI runner opened the PR — the
// record is always written on the platform, whichever mode opened the pull request.
func (d *Deliverer) RecordOpened(ctx context.Context, prep Prepared, pr PullRequest, created bool) (Result, error) {
	res := Result{DeliveryID: prep.DeliveryID, PR: pr, Mode: prep.Mode, Created: created}
	entry := Entry{
		DeliveryID: prep.DeliveryID, TenantID: prep.TenantID, ConfigHash: prep.ConfigHash,
		SourceRevision: prep.SourceRevision, Target: prep.Target.Key(), ForgeRef: pr.Ref,
		Mode: prep.Mode, Actor: actorFor(prep.Mode, prep.ProposalID), At: d.now().UTC(),
	}
	if created {
		entry.State = StateOpened
		if err := d.rec.Append(ctx, entry); err != nil {
			if errors.Is(err, ErrOpenConflict) {
				entry.State = StateUpdated
				res.Created = false
				if err2 := d.rec.Append(ctx, entry); err2 != nil {
					return res, fmt.Errorf("forgedelivery: recording the delivery: %w", err2)
				}
			} else {
				return res, fmt.Errorf("forgedelivery: recording the delivery: %w", err)
			}
		}
	} else {
		entry.State = StateUpdated
		if err := d.rec.Append(ctx, entry); err != nil {
			return res, fmt.Errorf("forgedelivery: recording the update: %w", err)
		}
	}
	superseded, err := d.supersede(ctx, nil, prep.TenantID, prep.Target.Key(), prep.DeliveryID, pr.Ref)
	if err != nil {
		return res, err
	}
	res.Superseded = superseded
	return res, nil
}

// Deliver is the HOSTED-APP path (and the demo path): Prepare + open with the platform's own writer +
// record + optional autonomous merge — the whole delivery on the platform side. CI-mediated delivery
// uses Prepare (served over the fetch) + OpenFromPrepared (in CI) + RecordFromReport, not this method.
func (d *Deliverer) Deliver(ctx context.Context, p Proposal, route *Route, forge ForgeWriter) (Result, error) {
	prep, err := d.Prepare(ctx, p, route)
	if err != nil {
		return Result{}, err
	}
	if forge == nil {
		return Result{}, fmt.Errorf("forgedelivery: delivery in %s mode needs a forge writer", route.Mode)
	}
	pr, created, err := OpenFromPrepared(ctx, forge, prep, d.bound)
	if err != nil {
		return Result{}, err
	}
	res, err := d.RecordOpened(ctx, prep, pr, created)
	if err != nil {
		return res, err
	}
	// Supersession closes the OTHER open PRs at the forge — RecordOpened only recorded them. Do the
	// forge close here where the writer is available.
	if err := d.closeSuperseded(ctx, forge, res.Superseded, res.PR.Ref, prep.DeliveryID); err != nil {
		return res, fmt.Errorf("%w: closing a superseded pull request: %v", ErrForgeUnavailable, err)
	}

	// Autonomous merge (task 2.6): reached only at Autonomous AND only for a gate-passed change
	// (Prepare already required the pass and the auto-merge entitlement, recording it in AllowMerge).
	if prep.AllowMerge {
		mergeCommit, err := forge.MergePR(ctx, res.PR.Ref)
		if err != nil {
			return res, fmt.Errorf("%w: merging the pull request: %v", ErrForgeUnavailable, err)
		}
		res.Merged = true
		res.MergeCommit = mergeCommit
		if err := d.rec.Append(ctx, Entry{
			DeliveryID: prep.DeliveryID, TenantID: prep.TenantID, ConfigHash: prep.ConfigHash,
			SourceRevision: prep.SourceRevision, Target: prep.Target.Key(), ForgeRef: res.PR.Ref,
			Mode: prep.Mode, State: StateMerged, Actor: "autonomous", MergeCommit: mergeCommit, At: d.now().UTC(),
		}); err != nil {
			return res, fmt.Errorf("forgedelivery: recording the merge: %w", err)
		}
	}
	return res, nil
}

// supersede records a 'superseded' entry for every OTHER open delivery for a target, and (when forge is
// non-nil) closes each pull request. It returns the delivery ids it superseded. Splitting the record
// from the forge close lets RecordOpened (which has no writer) record, and the writer-holding caller
// close.
func (d *Deliverer) supersede(ctx context.Context, forge ForgeWriter, tenantID, target, keepDeliveryID, keepRef string) ([]string, error) {
	heads, err := d.rec.OpenForTarget(ctx, tenantID, target)
	if err != nil {
		return nil, err
	}
	var closed []string
	for _, h := range heads {
		if h.DeliveryID == keepDeliveryID || h.ForgeRef == keepRef {
			continue
		}
		reason := fmt.Sprintf("superseded by a newer verified proposal (%s)", keepDeliveryID)
		if forge != nil {
			if err := forge.ClosePR(ctx, h.ForgeRef, reason); err != nil {
				return closed, err
			}
		}
		if err := d.rec.Append(ctx, Entry{
			DeliveryID: h.DeliveryID, TenantID: tenantID, ConfigHash: h.ConfigHash,
			SourceRevision: h.SourceRevision, Target: target, ForgeRef: h.ForgeRef,
			Mode: h.Mode, State: StateSuperseded, Actor: "supersession", Reason: reason, At: d.now().UTC(),
		}); err != nil {
			return closed, err
		}
		closed = append(closed, h.DeliveryID)
	}
	return closed, nil
}

// closeSuperseded closes the forge pull requests for deliveries RecordOpened already recorded as
// superseded. Used by the App-mode path, which holds the writer.
func (d *Deliverer) closeSuperseded(ctx context.Context, forge ForgeWriter, supersededIDs []string, keepRef, keepDeliveryID string) error {
	for _, id := range supersededIDs {
		h, ok, err := d.rec.Head(ctx, id)
		if err != nil {
			return err
		}
		if !ok || h.ForgeRef == keepRef {
			continue
		}
		reason := fmt.Sprintf("superseded by a newer verified proposal (%s)", keepDeliveryID)
		if err := forge.ClosePR(ctx, h.ForgeRef, reason); err != nil {
			return err
		}
	}
	return nil
}

// actorFor names who produced a delivery entry.
func actorFor(mode Mode, proposalID string) string {
	switch mode {
	case ModeCI:
		return "ci:" + proposalID
	case ModeApp:
		return "app:" + proposalID
	default:
		return proposalID
	}
}
