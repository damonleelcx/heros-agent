package changedelivery

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// rollout.go — the runtime route's mechanics (P13 §23.6–23.12, FR61–FR65, ADR-010).
//
// # Everything here is a PURE FUNCTION, and that is a requirement rather than a style
//
// The code this file describes does not run on our machines. It runs inside the CUSTOMER'S PROCESS, in
// the accessor ADR-004 generates into their repository. That single fact decides the shape of every
// function below:
//
//   - No clock is read here — `now` is a parameter, so a replay reproduces exactly (FR62/NFR24).
//   - No random source, no process id, no replica-local state. Two replicas must agree with NO
//     coordination, because there is nowhere to coordinate: they are the customer's processes, and we
//     are not in the request path (FR61/NFR25).
//   - No network call. Assignment, expiry and guard evaluation all work with the platform unreachable,
//     because ADR-004 H4 already forbade a startup dependency for the far weaker case of fetching one
//     value. A rollout that had to reach us to protect itself would make our availability theirs.
//
// # The share is integer basis points, and floats are not an oversight
//
// A float share invites a float comparison, and a float comparison is where determinism goes to die
// quietly across architectures and compiler versions. `ShareBasisPoints` is 0..10000 of the invocation
// space; the assignment compares two integers.

// Arm is which side of a rollout an invocation resolved to.
type Arm string

const (
	// ArmParent — what runs today. Also the answer on expiry and after a guard trip: reverting moves
	// toward the configuration that was already running and already reviewed, which is the safe
	// direction and the only one automated here.
	ArmParent Arm = "parent"
	// ArmCandidate — the accepted change under evaluation.
	ArmCandidate Arm = "candidate"
)

// MaxRolloutLifetimeMs bounds how long a rollout may live (FR64). Thirty days: long enough for a real
// exposure window, short enough that a forgotten rollout cannot quietly become the durable
// configuration while the customer's repository says something else.
const MaxRolloutLifetimeMs int64 = 30 * 24 * 60 * 60 * 1000

// ShareDenominator is the basis-point space assignment is computed over.
const ShareDenominator = 10000

// GuardKind is a condition the candidate arm is watched against, evaluated IN-PROCESS.
type GuardKind string

const (
	// GuardErrorRate — the candidate's observed error rate over its own invocations.
	GuardErrorRate GuardKind = "error-rate"
	// GuardExceptionClass — a named exception class thrown by the candidate arm.
	GuardExceptionClass GuardKind = "exception-class"
	// GuardLatencyMs — the candidate's observed latency ceiling.
	GuardLatencyMs GuardKind = "latency-ms"
)

// Guard is one declared abort condition. `Threshold` is an integer for the same determinism reason the
// share is: a guard that trips on one replica and not another is worse than no guard.
type Guard struct {
	Kind      GuardKind `json:"kind"`
	Threshold int64     `json:"threshold"`
	// Class is the exception class for GuardExceptionClass; empty otherwise.
	Class string `json:"class,omitempty"`
}

// Rollout is a two-armed binding document entry: what runs, what is being tried, for what share of
// invocations, until when, and what aborts it.
type Rollout struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflow_id"`
	NodeID     string `json:"node_id"`
	// ParentConfigHash and CandidateConfigHash are COMPLETE resolved configurations, never deltas
	// against one another (FR71) — a reviewer must be able to read both arms' effective values out of
	// the diff without composing anything.
	ParentConfigHash    string `json:"parent_config_hash"`
	CandidateConfigHash string `json:"candidate_config_hash"`
	// Change is what the candidate alters, so eligibility can be re-checked at authoring time against
	// the same table every surface reads.
	Change           ChangeKind `json:"change"`
	ShareBasisPoints int        `json:"share_basis_points"`
	// ExpiresAtUnixMs is fixed when the rollout is written and is not extendable except by a new
	// document change — which is a diff, a pull request, and a human.
	ExpiresAtUnixMs int64   `json:"expires_at_unix_ms"`
	Guards          []Guard `json:"guards,omitempty"`
	// VerifiedDelta records whether the candidate carries a verified-delta record. ADR-004 H3
	// permits-and-marks rather than forbids; a rollout is a stronger act, so the mark travels with it.
	VerifiedDelta bool `json:"verified_delta"`
	// GuardrailUndecided carries a held-out guardrail verdict that was neither a cost-win nor a
	// quality-tie (FR72). Authoring is permitted, but the ambiguity is recorded rather than hidden.
	GuardrailUndecided bool `json:"guardrail_undecided,omitempty"`
}

// ErrInvalidRollout is a rollout that could not be authored. It names the field, because "invalid" with
// no offender is the error message that sends someone reading the whole document.
type ErrInvalidRollout struct {
	Field  string
	Reason string
}

func (e *ErrInvalidRollout) Error() string {
	return fmt.Sprintf("rollout rejected: %s — %s", e.Field, e.Reason)
}

// Validate checks the bounds a rollout must satisfy before it can be written into a customer's tree.
//
// createdAtUnixMs is the authoring time, passed rather than read, so this stays replayable.
func (r Rollout) Validate(createdAtUnixMs int64) error {
	if strings.TrimSpace(r.ID) == "" {
		return &ErrInvalidRollout{Field: "id", Reason: "a rollout's identity is the input to arm assignment, so it cannot be empty"}
	}
	if strings.TrimSpace(r.ParentConfigHash) == "" || strings.TrimSpace(r.CandidateConfigHash) == "" {
		return &ErrInvalidRollout{Field: "arms", Reason: "each arm references a COMPLETE resolved configuration; an arm with no config_hash cannot be attributed to (FR63)"}
	}
	if r.ParentConfigHash == r.CandidateConfigHash {
		return &ErrInvalidRollout{Field: "arms", Reason: "both arms resolve to the same configuration, so the rollout would produce evidence about nothing"}
	}
	if r.ShareBasisPoints <= 0 || r.ShareBasisPoints >= ShareDenominator {
		return &ErrInvalidRollout{Field: "share_basis_points",
			Reason: fmt.Sprintf("share must be inside (0, %d) — a 0%% rollout is not a rollout and a 100%% rollout is a deploy wearing one's clothes", ShareDenominator)}
	}
	if r.ExpiresAtUnixMs <= createdAtUnixMs {
		return &ErrInvalidRollout{Field: "expires_at_unix_ms", Reason: "every rollout carries a bounded lifetime fixed when it is written (FR64); an absent or past expiry would let a forgotten rollout become the durable configuration"}
	}
	if r.ExpiresAtUnixMs-createdAtUnixMs > MaxRolloutLifetimeMs {
		return &ErrInvalidRollout{Field: "expires_at_unix_ms",
			Reason: fmt.Sprintf("lifetime exceeds the %d ms ceiling; permanence costs a codemod, a pull request and a merge, not a longer expiry", MaxRolloutLifetimeMs)}
	}
	for _, g := range r.Guards {
		if g.Kind == GuardExceptionClass && strings.TrimSpace(g.Class) == "" {
			return &ErrInvalidRollout{Field: "guards", Reason: "an exception-class guard with no class would never trip, which is a guard that looks present and is not"}
		}
	}
	return nil
}

// AssignmentKey is the caller-supplied stable key an arm is derived from.
//
// 🔴 Supplied is recorded, never inferred, and a missing key is NOT silently replaced with a synthesized
// one (FR62). A synthesized key would make the weaker guarantee invisible — the rollout would look like
// it had sticky per-session assignment when it actually re-rolled on every call, and the evidence it
// produced would be quietly about a different experiment than the one someone thought they ran.
type AssignmentKey struct {
	// Value is the stable unit key (a session id, a tenant id, a conversation id) when Supplied is
	// true; a per-invocation nonce from the caller when it is false.
	Value string
	// Supplied reports whether Value is a STABLE key. False means per-invocation assignment.
	Supplied bool
}

// GuardState is the in-process record of whether this rollout has already aborted itself.
//
// It lives in the customer's process. We never see it until their own telemetry export carries it, and
// that blindness is deliberate: a push channel back to us would be a runtime dependency wearing an
// observability costume (P13 PRD §14 Q14).
type GuardState struct {
	Tripped bool   `json:"tripped"`
	Kind    string `json:"kind,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// ResolveReason says WHY an invocation got the arm it got. It is emitted alongside the arm so a reader
// of the telemetry can tell a routine parent assignment from an expiry from an abort.
type ResolveReason string

const (
	ReasonAssigned    ResolveReason = "assigned"
	ReasonExpired     ResolveReason = "expired"
	ReasonGuardTitped ResolveReason = "guard-tripped"
)

// Assignment is one invocation's resolved arm and everything the run must record about it.
type Assignment struct {
	RolloutID string `json:"rollout_id"`
	Arm       Arm    `json:"arm"`
	// ConfigHash is 🔴 THE ARM'S OWN HASH (FR63) — not the rollout's identity, and not the parent's hash
	// for a candidate invocation. This is ADR-002's comparability objection answered: two runs recorded
	// under one config_hash stay exactly as comparable as they were, because the arm is the unit of
	// record rather than the document.
	ConfigHash string        `json:"config_hash"`
	Reason     ResolveReason `json:"reason"`
	// StableKey reports whether the assignment came from a stable key. False is honest, not a failure.
	StableKey bool `json:"stable_key"`
	// Unverified marks an arm resolving to a configuration with no verified-delta record (ADR-004 H3).
	Unverified bool `json:"unverified,omitempty"`
}

// Resolve picks the arm for one invocation. This is the function that runs in the customer's process.
//
// Order is safety-first and is not negotiable:
//  1. A tripped guard serves the parent. The abort already happened in-process; nothing re-litigates it,
//     and in particular it does NOT resume because the condition cleared (FR65).
//  2. An expired rollout serves the parent, with no network call and no human present (FR64).
//  3. Otherwise the arm is a pure hash of (rollout id, key).
func Resolve(r Rollout, key AssignmentKey, nowUnixMs int64, guard GuardState) Assignment {
	out := Assignment{RolloutID: r.ID, StableKey: key.Supplied}
	switch {
	case guard.Tripped:
		out.Arm, out.Reason, out.ConfigHash = ArmParent, ReasonGuardTitped, r.ParentConfigHash
		return out
	case nowUnixMs >= r.ExpiresAtUnixMs:
		out.Arm, out.Reason, out.ConfigHash = ArmParent, ReasonExpired, r.ParentConfigHash
		return out
	}
	out.Reason = ReasonAssigned
	if bucket(r.ID, key.Value) < uint64(r.ShareBasisPoints) {
		out.Arm, out.ConfigHash = ArmCandidate, r.CandidateConfigHash
		out.Unverified = !r.VerifiedDelta
		return out
	}
	out.Arm, out.ConfigHash = ArmParent, r.ParentConfigHash
	return out
}

// bucket maps (rollout id, key) into [0, ShareDenominator).
//
// SHA-256 rather than a fast non-cryptographic hash, for one reason that is not about security: its
// output is FIXED BY A STANDARD. A runtime hash (Go's maphash, FNV with a seed, anything the toolchain
// may re-tune) can change between versions or between architectures, and the day it does, every
// customer's arm assignment silently re-shuffles mid-rollout — the evidence would be about two
// different experiments stitched together, and nothing would report an error.
func bucket(rolloutID, key string) uint64 {
	sum := sha256.Sum256([]byte(rolloutID + "\x00" + key))
	return binary.BigEndian.Uint64(sum[:8]) % uint64(ShareDenominator)
}

// ErrAttributionMismatch is FR63's fail-the-run: a resolver emitted something in the arm-hash slot that
// is not one of the rollout's arms.
//
// 🔴 This is the same CLASS of failure as ADR-004 H1's "observed != requested" — a run recorded under an
// identity that is not a configuration corrupts every comparison built on top of it, and it does so
// invisibly. So the run FAILS rather than being scored.
type ErrAttributionMismatch struct {
	RolloutID string
	Emitted   string
	Reason    string
}

func (e *ErrAttributionMismatch) Error() string {
	return fmt.Sprintf("run failed: rollout %s emitted %q where an arm config_hash was required — %s", e.RolloutID, e.Emitted, e.Reason)
}

// ValidateAttribution asserts an emitted hash is one of the rollout's two arms (FR63).
//
// Called on the telemetry path, on the necessary route the data already travels, so it cannot be
// skipped by forgetting to call it — the same containment shape ADR-004 H1 used.
func ValidateAttribution(r Rollout, emittedConfigHash string) error {
	switch emittedConfigHash {
	case r.ParentConfigHash, r.CandidateConfigHash:
		return nil
	case r.ID:
		return &ErrAttributionMismatch{RolloutID: r.ID, Emitted: emittedConfigHash,
			Reason: "this is the rollout's IDENTITY, not a configuration; recording it would make the run uncomparable to every other run of either arm"}
	case "":
		return &ErrAttributionMismatch{RolloutID: r.ID, Emitted: emittedConfigHash,
			Reason: "no configuration was attributed; an unattributed invocation under an active rollout cannot be assigned to an arm after the fact"}
	}
	return &ErrAttributionMismatch{RolloutID: r.ID, Emitted: emittedConfigHash,
		Reason: "the emitted hash is neither arm of this rollout"}
}

// EvaluateGuards decides whether the candidate arm has aborted itself, IN PROCESS.
//
// observations are the candidate arm's own counters. The returned state is what Resolve consults on
// every subsequent invocation; once tripped it stays tripped, and clearing it requires an authored
// document change delivered as a merged pull request (FR65).
//
// 🚫 There is no call to the platform anywhere in this path, and there must not be one. Reverting is
// the safe direction, so automating it costs nothing safety values; RESUMING moves traffic back toward
// a configuration that just failed under load, which is the platform overriding a reviewed
// configuration on its own authority. Those are different acts and do not get the same permission.
type GuardObservation struct {
	ErrorRatePerMyriad int64  `json:"error_rate_per_myriad"`
	LatencyMs          int64  `json:"latency_ms"`
	ExceptionClass     string `json:"exception_class,omitempty"`
}

func EvaluateGuards(r Rollout, prior GuardState, obs GuardObservation) GuardState {
	if prior.Tripped {
		return prior
	}
	for _, g := range r.Guards {
		switch g.Kind {
		case GuardErrorRate:
			if obs.ErrorRatePerMyriad > g.Threshold {
				return GuardState{Tripped: true, Kind: string(g.Kind),
					Detail: fmt.Sprintf("candidate error rate %d per myriad exceeded the declared ceiling %d", obs.ErrorRatePerMyriad, g.Threshold)}
			}
		case GuardLatencyMs:
			if obs.LatencyMs > g.Threshold {
				return GuardState{Tripped: true, Kind: string(g.Kind),
					Detail: fmt.Sprintf("candidate latency %d ms exceeded the declared ceiling %d ms", obs.LatencyMs, g.Threshold)}
			}
		case GuardExceptionClass:
			if obs.ExceptionClass != "" && obs.ExceptionClass == g.Class {
				return GuardState{Tripped: true, Kind: string(g.Kind),
					Detail: fmt.Sprintf("candidate raised the declared exception class %q", g.Class)}
			}
		}
	}
	return prior
}

// Expired reports whether the rollout has passed its expiry at the supplied instant.
func (r Rollout) Expired(nowUnixMs int64) bool { return nowUnixMs >= r.ExpiresAtUnixMs }

// ArmHashes returns the two arm hashes, sorted, for a surface that renders both.
func (r Rollout) ArmHashes() []string {
	out := []string{r.ParentConfigHash, r.CandidateConfigHash}
	sort.Strings(out)
	return out
}
