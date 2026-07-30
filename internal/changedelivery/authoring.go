package changedelivery

import (
	"encoding/json"
	"fmt"
)

// authoring.go — the gate a rollout must pass before it can be written into a customer's tree
// (P13 §23.11/23.12/23.16/23.17, FR67/FR68/FR72/FR73).
//
// # Why the gate is here and not at each axis
//
// Every axis has a reason a particular change must not become a rollout candidate, and every one of
// those reasons is a REFUSAL THAT ALREADY EXISTS somewhere else: the coherence gate (P15), the
// drop-tolerance gate (P16), the transform's typed refusal (P17/P18), the held-out guardrail (P13).
// The temptation is to re-implement each check here, which would produce a second place for every
// safety rule to be wrong — exactly the "second apply path" the authored-change contract forbids.
//
// 🔴 So this gate does not RE-DECIDE anything. It takes the verdicts that were already reached and
// refuses to author a rollout that contradicts any of them. One resolve-hash-gate spine, two origins,
// and now two delivery routes — but still one set of gates.

// HaltState is the platform's halt, read at authoring time.
//
// Readable is separate from Active because an UNREADABLE halt must fail closed (FR68). A halt state we
// cannot read is not permission; treating it as permission is how a halt becomes decorative exactly
// when it matters.
type HaltState struct {
	Active   bool
	Readable bool
}

// GuardrailVerdict is the held-out downgrade guardrail's answer (P13 FR9/FR10, FR72).
type GuardrailVerdict string

const (
	// GuardrailNotApplicable — the change is not a downgrade, so the guardrail has no opinion.
	GuardrailNotApplicable GuardrailVerdict = "not-applicable"
	// GuardrailAdmitted — a cost-win or a quality-tie.
	GuardrailAdmitted GuardrailVerdict = "admitted"
	// GuardrailRejected — decided against. 🚫 Not authorable as a rollout candidate.
	GuardrailRejected GuardrailVerdict = "rejected"
	// GuardrailUndecided — neither. Authorable, but the ambiguity travels WITH the rollout rather than
	// being dropped on the floor (FR72).
	GuardrailUndecided GuardrailVerdict = "undecided"
)

// AuthorRequest is everything the gate needs, with every verdict supplied rather than recomputed.
type AuthorRequest struct {
	Rollout     Rollout
	NodeIsBound bool
	// Entitled is the SERVER-SIDE entitlement answer (FR68). A client-side check is not an input here
	// because a client-side check is not a gate.
	Entitled bool
	Halt     HaltState
	// GateRejected and GateCause carry a prior safety gate's rejection (the coherence gate, the
	// drop-tolerance gate, a held-out overlap). When set, no route may deliver the change.
	GateRejected bool
	GateCause    string
	// TransformRefusalCause carries the transform's own typed cause when the axis refuses at transform
	// (P17 memory, P18 harness). A rollout candidate on such an axis is refused WITH THE SAME CAUSE, so
	// the two routes cannot disagree about why.
	TransformRefusalCause string
	Guardrail             GuardrailVerdict
	// AuthoredByUser marks a user-originated change (P13 authored-change). It is recorded, never
	// hashed, and it does not relax any check.
	AuthoredByUser bool
	// VerifiedDelta reports whether the candidate carries a verified-delta record.
	VerifiedDelta bool
	// ArmParams is the candidate arm's declared parameters, for an axis that carries any (P18 FR49).
	//
	// 🔴 Validate is supplied by the caller and MUST be the registry's own seal-time check. It is a
	// function rather than a re-implementation because a rollout must not become the one place a bound
	// can be removed — a second validator would drift, and it would drift toward permissive.
	ArmParams *ArmParams
	// DropToleranceUnknown carries P16's drop-tolerance gate verdict when tolerance for a dropped item
	// could not be determined.
	//
	// 🔴 It is NOT a rejection. The gate's standing rule is that it never refuses on ignorance — refusing
	// because nobody has said whether an item matters would block every change on a workflow nobody has
	// annotated, which is most of them. So the unknown is recorded and travels WITH the rollout, where a
	// reader can weigh it, rather than being resolved by guessing in either direction.
	DropToleranceUnknown bool
	CreatedAtUnixMs      int64
}

// ArmParams carries a candidate arm's parameters and the validation they must satisfy.
type ArmParams struct {
	Raw json.RawMessage
	// Validate is the SAME function the registry applies at seal. Nil means the axis declares no
	// parameter schema, which is different from "the parameters are fine".
	Validate func(json.RawMessage) error
}

// ErrRolloutRefused is every authoring refusal. One type with a stable Cause, because a surface must be
// able to branch on WHY without parsing prose.
type ErrRolloutRefused struct {
	Cause  string
	Detail string
}

func (e *ErrRolloutRefused) Error() string {
	return fmt.Sprintf("rollout authoring refused (%s): %s", e.Cause, e.Detail)
}

// Authoring refusal causes. Distinct from the eligibility causes above: those say why a CELL cannot be
// rolled out, these say why THIS ATTEMPT was refused.
const (
	RefusedNotEntitled       = "not-entitled"
	RefusedHalted            = "halted"
	RefusedHaltUnreadable    = "halt-unreadable"
	RefusedIneligible        = "route-ineligible"
	RefusedGate              = "gate-rejected"
	RefusedTransform         = "refused-at-transform"
	RefusedGuardrail         = "guardrail-rejected"
	RefusedInvalidRollout    = "invalid-rollout"
	RefusedIdentityCandidate = "identity-candidate"
	RefusedArmParams         = "arm-params-rejected"
)

// AuthorRollout runs the gate. Order is deliberate: the checks that are about PERMISSION come first,
// then the ones about whether the change may exist at all, then the ones about the rollout's own shape.
// A caller that sees `not-entitled` learns nothing about the change, which is correct.
func AuthorRollout(req AuthorRequest) error {
	// ── 1. permission, server-side, and fail-closed on an unreadable halt
	if !req.Entitled {
		return &ErrRolloutRefused{Cause: RefusedNotEntitled,
			Detail: "authoring a rollout is entitlement-gated on the server; the caller's plan does not carry it"}
	}
	if !req.Halt.Readable {
		return &ErrRolloutRefused{Cause: RefusedHaltUnreadable,
			Detail: "the halt state could not be read, so authoring fails closed — an unreadable halt is not permission"}
	}
	if req.Halt.Active {
		return &ErrRolloutRefused{Cause: RefusedHalted,
			Detail: "a halt is active; no new rollout is authored while it is (running rollouts continue to expire and guard themselves locally, because the platform does not reach into a customer's process)"}
	}

	// ── 2. may this change exist at all? Every verdict here was reached elsewhere.
	if req.GateRejected {
		return &ErrRolloutRefused{Cause: RefusedGate,
			Detail: fmt.Sprintf("a safety gate rejected this change (%s), so it has no runnable spec — a rollout is not an alternative route around a gate", nonEmpty(req.GateCause, "unnamed gate"))}
	}
	if req.TransformRefusalCause != "" {
		return &ErrRolloutRefused{Cause: RefusedTransform,
			Detail: fmt.Sprintf("the transform refuses this change (%s); the runtime route refuses it with the same cause, so no document carrying it is written", req.TransformRefusalCause)}
	}
	if req.Guardrail == GuardrailRejected {
		return &ErrRolloutRefused{Cause: RefusedGuardrail,
			Detail: "the held-out downgrade guardrail decided against this change; it is not authorable as a rollout candidate"}
	}

	// ── 3. is the cell eligible? Read from the SAME table every surface reads.
	elig, err := RuntimeEligibility(req.Rollout.Change, req.NodeIsBound)
	if err != nil {
		return err
	}
	if !elig.Eligible {
		return &ErrRolloutRefused{Cause: RefusedIneligible,
			Detail: fmt.Sprintf("%s (%s): %s", elig.Cause, elig.Cause.Owner(), elig.Note)}
	}

	// ── 4. the rollout's own shape
	if err := req.Rollout.Validate(req.CreatedAtUnixMs); err != nil {
		return &ErrRolloutRefused{Cause: RefusedInvalidRollout, Detail: err.Error()}
	}

	// ── 5. the arm's parameters, against the registry's OWN schema check
	if req.ArmParams != nil && req.ArmParams.Validate != nil {
		if err := req.ArmParams.Validate(req.ArmParams.Raw); err != nil {
			return &ErrRolloutRefused{Cause: RefusedArmParams,
				Detail: fmt.Sprintf("the candidate arm's parameters were refused by the same check the registry applies at seal: %v", err)}
		}
	}
	return nil
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// RecordedOnRollout is what authoring stamps onto an accepted rollout — the facts that must travel with
// it rather than being inferred later.
//
// 🔴 `Unverified` is the ADR-004 H3 mark applied to a stronger act. H3 permits-and-marks a RESOLUTION
// with no verified delta; a rollout exposes production traffic, so the mark is carried on the rollout
// itself and rendered wherever it is. 🚫 And an authored change accumulating rollout evidence is still
// `unverified` (FR73): a rollout must not launder a user's own edit into a platform result.
type RecordedOnRollout struct {
	Origin             string `json:"origin"`
	Unverified         bool   `json:"unverified"`
	GuardrailUndecided bool   `json:"guardrail_undecided,omitempty"`
	// DropToleranceUnknown travels with the rollout rather than being resolved by guessing (P16 FR56).
	DropToleranceUnknown bool `json:"drop_tolerance_unknown,omitempty"`
}

// Stamp returns what is recorded on an accepted rollout.
func Stamp(req AuthorRequest) RecordedOnRollout {
	origin := "operator"
	if req.AuthoredByUser {
		origin = "user"
	}
	return RecordedOnRollout{
		Origin:               origin,
		Unverified:           !req.VerifiedDelta,
		GuardrailUndecided:   req.Guardrail == GuardrailUndecided,
		DropToleranceUnknown: req.DropToleranceUnknown,
	}
}

// PinnedResolve is what a measurement run uses instead of Resolve (FR67).
//
// During evaluation and verification the resolver is PINNED: the configuration under measurement is the
// one the run requested, and an active rollout does not alter it. This is ADR-004's existing rule
// ("override sources are disabled in the sandbox entirely") extended to a second override source.
//
// 🔴 A verified delta must never be produced from a partially exposed configuration. If a rollout could
// tilt a measurement run, the ledger would carry deltas measured against a blend of two arms, and no
// consumer of that number could tell.
func PinnedResolve(requestedConfigHash string) Assignment {
	return Assignment{
		Arm:        ArmParent,
		ConfigHash: requestedConfigHash,
		Reason:     ReasonAssigned,
		StableKey:  true,
	}
}

// RolloutEvidenceIsVerifiedDelta is deliberately a function that always returns false, so that a caller
// asking "does this evidence count?" gets a compiled answer rather than a convention someone can forget.
//
// Rollout evidence never enters the verified-delta ledger and is never counted as a win, a regression,
// or a tie (FR67).
func RolloutEvidenceIsVerifiedDelta() bool { return false }
