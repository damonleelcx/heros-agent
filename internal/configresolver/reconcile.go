package configresolver

import (
	"errors"
	"fmt"
	"sync"

	"github.com/heros-foreal/agentd/internal/telemetry"
)

// reconcile.go is the reproducibility interlock (P10 §9, ADR-004 H1). A bound run can execute a
// different configuration than the one requested and still be recorded under the requested hash — which
// would corrupt every comparison built on it. The containment is an event-write-reconcile-read
// invariant with a NAMED, idempotent reconcile point: a run's own telemetry is a path every invocation
// already travels, so the check cannot be skipped by forgetting to call it.

// ErrConfigHashMismatch means a run's invocations resolved a configuration different from the one
// requested. The run FAILS — it is not scored under the requested hash (task 9.2).
var ErrConfigHashMismatch = errors.New("configresolver: resolved config_hash differs from the requested one")

// NewPinned builds a resolver PINNED to the embedded document (task 9.3): override sources are disabled
// entirely, so a measurement run reads the embedded document or it does not run. Passing override
// options to a pinned resolver is refused at construction rather than silently ignored — a pinned
// resolver that quietly held an override would defeat the whole guarantee.
func NewPinned(embedded []byte) (*Resolver, error) {
	r, err := New(embedded)
	if err != nil {
		return nil, err
	}
	r.pinned = true
	return r, nil
}

// Tags returns the per-invocation reconciliation tag set (task 9.1): the resolved config_hash and the
// unverified flag. Emitted on EVERY node invocation as part of the standard tag set, so the harness can
// reconcile against the requested hash.
func (r *Resolver) Tags() map[string]string {
	d := r.Resolve()
	unverified := "false"
	if !d.Verified() {
		unverified = "true"
	}
	return map[string]string{
		telemetry.AttrResolvedConfigHash: d.ConfigHash,
		telemetry.AttrUnverified:         unverified,
	}
}

// RefuseUnverified reports whether resolving the current document must be refused because it carries no
// verified-delta record AND the caller's automation level requires verified configurations (task 9.4,
// ADR-004 Decision 7 — "under Autonomous, an unverified resolution does not run").
//
// requiresVerified is supplied by the caller (Autonomous → true), keeping this package decoupled from
// the verification package's AutomationLevel type. An unverified resolution under a permissive level is
// PERMITTED — it is marked, not blocked — which is why this returns a decision rather than always
// refusing.
func (r *Resolver) RefuseUnverified(requiresVerified bool) (refuse bool, reason string) {
	if !requiresVerified {
		return false, ""
	}
	if r.Resolve().Verified() {
		return false, ""
	}
	return true, "the resolved configuration carries no verified delta and the automation level requires a verified configuration"
}

// Reconciliation accumulates the resolved config_hash observed on each invocation of a run and decides
// whether the run may be scored (task 9.2). It fails on ANY mismatch — the run is not partially scored
// from the invocations that matched (task 9.2 scenario "Reconciliation covers every invocation").
type Reconciliation struct {
	requested string

	mu            sync.Mutex
	invocations   int
	firstMismatch string
}

// NewReconciliation starts reconciling a run against its requested config_hash.
func NewReconciliation(requestedConfigHash string) *Reconciliation {
	return &Reconciliation{requested: requestedConfigHash}
}

// Observe records one invocation's resolved config_hash. Called on the standard telemetry path (the
// reconcile point on the data's necessary route), so it cannot be forgotten.
func (r *Reconciliation) Observe(resolvedConfigHash string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.invocations++
	if resolvedConfigHash != r.requested && r.firstMismatch == "" {
		r.firstMismatch = resolvedConfigHash
	}
}

// Verdict returns nil if EVERY observed invocation matched the requested hash, or ErrConfigHashMismatch
// (wrapped with detail) if any differed. A run with zero invocations has nothing to reconcile and
// passes — the mismatch check is about what DID run.
func (r *Reconciliation) Verdict() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.firstMismatch != "" {
		return fmt.Errorf("%w: requested %s, an invocation resolved %s", ErrConfigHashMismatch, r.requested, r.firstMismatch)
	}
	return nil
}

// OK is Verdict() == nil, for a call site that only needs the boolean.
func (r *Reconciliation) OK() bool { return r.Verdict() == nil }
