// Package adminops is P8's privileged COMMAND surface: the one shape every state-changing operator
// action takes, and the tenant/billing/registry/job/fleet commands built on it (design Decision 3,
// FR6–FR13).
//
// # One command path, not one per feature
//
//	authenticate → authorize (deny by default) → confirm + reason → write-ahead audit → effect → observe
//
// Every command in this package runs through Executor.Execute. That is deliberate and load-bearing:
// the alternative — each feature remembering to check a session, call the gate, demand a reason and
// write an audit entry — has a predictable failure mode, which is the fourteenth command forgetting
// one of the four. Here a command supplies its capability, its target, its reason and its EFFECT, and
// the discipline is applied to it rather than by it.
//
// # Why the audit entry is written before the effect
//
// Write-ahead. If the process dies between the audit append and the effect, the record says an action
// was attempted and the effect did not happen — recoverable, and visible. The other order can lose a
// completed action entirely, which is the one outcome an audit trail may not have. It also gives
// FR16 its teeth: if the audit store cannot take the write-ahead entry, the effect never runs.
//
// # Why friction is scaled rather than uniform
//
// A reversible per-tenant action takes one reason-required confirmation; an irreversible one
// additionally requires the operator to TYPE THE TARGET. Uniform friction trains operators to click
// through, which removes the protection exactly where blast radius is largest.
package adminops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// Result values recorded on an audit entry. Central enum: a result spelled two ways is a trail that
// cannot be filtered.
const (
	// ResultAttempted is the WRITE-AHEAD entry, committed before the effect runs.
	ResultAttempted = "attempted"
	// ResultApplied is the outcome entry for an effect that succeeded.
	ResultApplied = "applied"
	// ResultFailed is the outcome entry for an effect that returned an error. The action is on the
	// record either way — a failed privileged action is exactly as interesting as a successful one.
	ResultFailed = "failed"
)

var (
	// ErrNoReason means no reason was supplied for an action that requires one (FR6). The action does
	// not proceed and no state changes.
	ErrNoReason = errors.New("adminops: a recorded reason is required — the action did not proceed")
	// ErrNotConfirmed means the operator did not confirm.
	ErrNotConfirmed = errors.New("adminops: this action requires an explicit confirmation")
	// ErrSecondConfirmation means an irreversible action was invoked without typing the target
	// identifier (FR6's second confirmation).
	ErrSecondConfirmation = errors.New("adminops: this action is irreversible and requires the target identifier to be typed as a second confirmation")
	// ErrDenied means the RBAC gate refused. Distinct from a transport failure so the console can
	// render "denied" rather than "degraded" (FR26).
	ErrDenied = adminrbac.ErrDenied
	// ErrImpersonatedWrite means a write was attempted under a read-scoped impersonation session. It
	// is a distinct error from a permission denial: the operator HOLDS the capability, but the session
	// they are acting through does not permit writes until it is elevated (FR13).
	ErrImpersonatedWrite = errors.New("adminops: a write while impersonating requires an elevated session")
)

// ImpersonationGuard decides whether a write may proceed under an impersonation session.
//
// It is an interface on the executor rather than a check inside each command for the reason the whole
// command path exists: a rule applied by fourteen call sites is a rule thirteen of them get right.
type ImpersonationGuard interface {
	// AllowWrite reports whether the session permits a write now. An error means the session could not
	// be resolved (expired, unknown, store down), which denies.
	AllowWrite(impersonationID string) (allowed bool, reason string, err error)
}

// Confirmation is what the operator supplied at the confirmation step.
//
// A struct rather than a bool because the second confirmation is a different KIND of evidence: not
// "did you mean it" but "name the thing you are about to destroy". Collapsing them into one boolean
// is how a type-the-target requirement becomes a checkbox.
type Confirmation struct {
	// Confirmed is the first confirmation.
	Confirmed bool
	// TypedTarget is the target identifier the operator typed. Required — and required to MATCH — for
	// an irreversible action.
	TypedTarget string
}

// Confirm builds a first-confirmation-only Confirmation.
func Confirm() Confirmation { return Confirmation{Confirmed: true} }

// ConfirmTyping builds a confirmation that also carries a typed target.
func ConfirmTyping(target string) Confirmation {
	return Confirmation{Confirmed: true, TypedTarget: target}
}

// Command describes one privileged action, independent of what it does.
type Command struct {
	// Capability is the gate this command is checked against. Its classification also decides how much
	// friction the command takes, so a command cannot opt out of confirmation by omission.
	Capability adminrbac.Capability
	// Action is the audit action name recorded for this command.
	Action adminaudit.Action
	// Target is what the command acts on, in the platform's target syntax ("tenant:acme", "global").
	Target string
	// Reason is the operator's recorded justification.
	Reason string
	// Confirm is what the operator supplied at the confirmation step.
	Confirm Confirmation
	// Params are the command's parameters, hashed into the audit entry's ParamsDigest. Values, not
	// digests: hashing happens here so no call site can forget and put a raw parameter in the chain.
	Params []string
	// Evidence is non-PII reference material recorded with the entry (a plan ref, a job id).
	Evidence map[string]string
}

// Receipt is what a completed command returns: the audit coordinates that prove it happened.
type Receipt struct {
	// WriteAheadSeq is the sequence number of the entry committed BEFORE the effect.
	WriteAheadSeq int `json:"write_ahead_seq"`
	// OutcomeSeq is the sequence number of the entry recording what the effect did.
	OutcomeSeq int `json:"outcome_seq"`
	// EntryHash is the outcome entry's hash — the value an auditor re-derives to prove the record.
	EntryHash string `json:"entry_hash"`
	Result    string `json:"result"`
	// Evidence is what the effect reported (a merge commit, a billing event id).
	Evidence map[string]string `json:"evidence,omitempty"`
	// ActorAdminID and At restate who and when, so a receipt is self-describing in a response body.
	ActorAdminID string    `json:"actor_admin_id"`
	At           time.Time `json:"at"`
}

// Effect is a command's actual work. It returns non-PII evidence to record and an error if it failed.
type Effect func(ctx context.Context) (evidence map[string]string, err error)

// Observer receives one event per executed command. It is how privileged-action volume becomes a live
// operational signal on the P2.5 substrate (task 13.1) without this package importing telemetry.
type Observer interface {
	AdminCommand(ev CommandEvent)
}

// ObserverFunc adapts a function to Observer.
type ObserverFunc func(ev CommandEvent)

// AdminCommand implements Observer.
func (f ObserverFunc) AdminCommand(ev CommandEvent) { f(ev) }

// CommandEvent is one executed command, as an observation. Identifiers and outcomes only — no reason
// body, no parameters, nothing that could carry tenant content into a metric label.
type CommandEvent struct {
	ActorAdminID string               `json:"actor_admin_id"`
	Capability   adminrbac.Capability `json:"capability"`
	Action       adminaudit.Action    `json:"action"`
	Target       string               `json:"target"`
	Result       string               `json:"result"`
	Denied       bool                 `json:"denied"`
	Impersonated bool                 `json:"impersonated"`
	At           time.Time            `json:"at"`
	Roles        []adminrbac.Role     `json:"roles,omitempty"`
	// Decision is the authorization outcome behind a denial, so an observer can report WHICH role
	// would have held the capability without re-querying the gate. Never serialized: it is for the
	// in-process observer, not for a metric label.
	Decision *adminrbac.Decision `json:"-"`
}

// Executor runs the shared command path.
type Executor struct {
	gate     *adminrbac.Gate
	audit    adminaudit.Store
	observer Observer
	now      func() time.Time
	// impersonation guards writes taken under an impersonation session. Nil in a deployment without
	// impersonation; installed by NewImpersonationService.
	impersonation ImpersonationGuard
}

// SetImpersonationGuard installs the write guard. Called by NewImpersonationService, so wiring
// impersonation at all wires its restriction too — there is no order of construction that produces an
// executor with sessions but no guard.
func (e *Executor) SetImpersonationGuard(g ImpersonationGuard) { e.impersonation = g }

// NewExecutor wires the command path. Both the gate and the audit store are required: a command path
// that can be built without one of them is a command path that will one day run without it.
func NewExecutor(gate *adminrbac.Gate, audit adminaudit.Store, observer Observer, now func() time.Time) (*Executor, error) {
	if gate == nil {
		return nil, errors.New("adminops: the command path requires the authorization gate")
	}
	if audit == nil {
		return nil, errors.New("adminops: the command path requires the audit store — an unauditable action must not run")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Executor{gate: gate, audit: audit, observer: observer, now: now}, nil
}

// Gate exposes the authorization gate so read paths can check a capability without executing a
// command. Read-only: the gate itself writes nothing but denial records.
func (e *Executor) Gate() *adminrbac.Gate { return e.gate }

// Audit exposes the audit store for the console's audit viewer and for Verify.
func (e *Executor) Audit() adminaudit.Store { return e.audit }

// Now exposes the executor's clock so commands stamp the same time the audit entries carry.
func (e *Executor) Now() time.Time { return e.now() }

// Authorize runs the identity and permission steps alone — the read-path equivalent of Execute.
//
// It exists because a READ is still permission-gated (FR14's cross-tenant reads, the audit viewer) but
// takes no reason and no confirmation, and pretending a read is a command would either demand a reason
// for looking at a page or quietly weaken the command path's requirements.
func (e *Executor) Authorize(ctx context.Context, capability adminrbac.Capability, target string) (adminidentity.Session, adminrbac.Decision, error) {
	sess, err := adminidentity.RequireSession(ctx)
	if err != nil {
		return adminidentity.Session{}, adminrbac.Decision{}, err
	}
	d := e.gate.Authorize(sess.AdminID, capability, target)
	if !d.Allowed {
		e.observe(CommandEvent{
			ActorAdminID: sess.AdminID, Capability: capability, Target: target,
			Result: "denied", Denied: true, At: e.now(), Roles: d.Roles, Decision: &d,
		})
		return sess, d, fmt.Errorf("%w: %v", ErrDenied, d.Error())
	}
	return sess, d, nil
}

// Execute runs one command through the full path.
func (e *Executor) Execute(ctx context.Context, cmd Command, effect Effect) (Receipt, error) {
	// ── 1. authenticate ──
	sess, err := adminidentity.RequireSession(ctx)
	if err != nil {
		return Receipt{}, err
	}

	// ── 2. authorize (deny by default; the gate audits its own denials) ──
	d := e.gate.Authorize(sess.AdminID, cmd.Capability, cmd.Target)
	if !d.Allowed {
		e.observe(CommandEvent{
			ActorAdminID: sess.AdminID, Capability: cmd.Capability, Action: cmd.Action, Target: cmd.Target,
			Result: "denied", Denied: true, At: e.now(), Roles: d.Roles, Decision: &d,
		})
		return Receipt{}, fmt.Errorf("%w: %v", ErrDenied, d.Error())
	}

	// ── 3. confirm + reason ──
	if err := checkFriction(cmd); err != nil {
		return Receipt{}, err
	}

	// ── 3b. impersonation scope ──
	//
	// A write taken through a read-scoped session is refused HERE, after authorization and before any
	// audit or effect, so "the operator may do this" and "this session may do this" stay two separate
	// questions with two separate answers.
	impID := ImpersonationFrom(ctx)
	if impID != "" && !cmd.Capability.ReadOnly() {
		if e.impersonation == nil {
			return Receipt{}, fmt.Errorf("%w: no impersonation guard is wired, so the scope cannot be checked", ErrImpersonatedWrite)
		}
		allowed, why, gerr := e.impersonation.AllowWrite(impID)
		if gerr != nil {
			return Receipt{}, fmt.Errorf("%w: %v", ErrImpersonatedWrite, gerr)
		}
		if !allowed {
			return Receipt{}, fmt.Errorf("%w: %s", ErrImpersonatedWrite, why)
		}
	}

	// ── 4. write-ahead audit ──
	base := adminaudit.Entry{
		ActorAdminID:    sess.AdminID,
		Target:          cmd.Target,
		Action:          cmd.Action,
		Reason:          cmd.Reason,
		ParamsDigest:    adminaudit.Digest(cmd.Params...),
		ImpersonationID: impID,
		Evidence:        cmd.Evidence,
		CreatedAt:       e.now(),
	}
	ahead := base
	ahead.Result = ResultAttempted
	committed, err := e.audit.Append(ahead)
	if err != nil {
		// FAIL CLOSED (FR16): the effect never runs, so no unaudited state change is possible.
		return Receipt{}, fmt.Errorf("adminops: %s not performed: %w", cmd.Action, err)
	}

	// ── 5. effect ──
	evidence, effErr := effect(ctx)

	// ── 6. observe: the outcome is a NEW appended entry, never an edit of the write-ahead one ──
	outcome := base
	outcome.Result = ResultApplied
	if effErr != nil {
		outcome.Result = ResultFailed
	}
	outcome.CreatedAt = e.now()
	outcome.Evidence = mergeEvidence(cmd.Evidence, evidence, committed.Seq)
	settled, aerr := e.audit.Append(outcome)
	if aerr != nil {
		// The effect already ran and its outcome could not be recorded. The write-ahead entry survives,
		// so the action is not invisible — but this is a real integrity degradation and it is reported
		// as an error rather than swallowed.
		return Receipt{WriteAheadSeq: committed.Seq, ActorAdminID: sess.AdminID, At: base.CreatedAt},
			fmt.Errorf("adminops: %s ran but its outcome could not be audited: %w", cmd.Action, aerr)
	}

	e.observe(CommandEvent{
		ActorAdminID: sess.AdminID, Capability: cmd.Capability, Action: cmd.Action, Target: cmd.Target,
		Result: outcome.Result, Impersonated: impID != "", At: outcome.CreatedAt, Roles: d.Roles,
	})

	receipt := Receipt{
		WriteAheadSeq: committed.Seq, OutcomeSeq: settled.Seq, EntryHash: settled.EntryHash,
		Result: outcome.Result, Evidence: evidence, ActorAdminID: sess.AdminID, At: settled.CreatedAt,
	}
	if effErr != nil {
		return receipt, effErr
	}
	return receipt, nil
}

// checkFriction applies the confirmation and reason rules for a command's capability.
func checkFriction(cmd Command) error {
	if cmd.Capability.ReadOnly() {
		return nil
	}
	if strings.TrimSpace(cmd.Reason) == "" {
		return ErrNoReason
	}
	if !cmd.Confirm.Confirmed {
		return ErrNotConfirmed
	}
	if cmd.Capability.RequiresTypedTarget() {
		typed := strings.TrimSpace(cmd.Confirm.TypedTarget)
		if typed == "" || typed != strings.TrimSpace(cmd.Target) {
			return fmt.Errorf("%w (type %q to proceed)", ErrSecondConfirmation, cmd.Target)
		}
	}
	return nil
}

// mergeEvidence combines the command's declared evidence with what the effect reported, and links the
// outcome entry back to its write-ahead entry so the pair is reconstructable without inference.
func mergeEvidence(declared, produced map[string]string, writeAheadSeq int) map[string]string {
	out := make(map[string]string, len(declared)+len(produced)+1)
	for k, v := range declared {
		out[k] = v
	}
	for k, v := range produced {
		out[k] = v
	}
	out["write_ahead_seq"] = fmt.Sprintf("%d", writeAheadSeq)
	return out
}

func (e *Executor) observe(ev CommandEvent) {
	if e.observer != nil {
		e.observer.AdminCommand(ev)
	}
}

// ── Target syntax ───────────────────────────────────────────────────────────────────────────────

// Target prefixes. Central constants so "tenant:acme" is spelled one way in the gate, the audit chain
// and the console.
const (
	// TargetGlobal is the target of a fleet-wide action.
	TargetGlobal  = "global"
	targetTenant  = "tenant:"
	targetJob     = "job:"
	targetModel   = "model:"
	targetAdmin   = "admin:"
	targetSubject = "subject:"
)

// TenantTarget renders a tenant's target identifier.
func TenantTarget(tenantID string) string { return targetTenant + tenantID }

// JobTarget renders a job's target identifier.
func JobTarget(jobID string) string { return targetJob + jobID }

// ModelTarget renders a model's target identifier.
func ModelTarget(modelID string) string { return targetModel + modelID }

// AdminTarget renders an admin principal's target identifier.
func AdminTarget(adminID string) string { return targetAdmin + adminID }

// SubjectTarget renders a data-subject's target identifier (compliance).
func SubjectTarget(subjectRef string) string { return targetSubject + subjectRef }

// TenantOf extracts the tenant id from a tenant target, or "" if it is not one.
func TenantOf(target string) string {
	if rest, ok := strings.CutPrefix(target, targetTenant); ok {
		return rest
	}
	return ""
}

// ── Impersonation context ───────────────────────────────────────────────────────────────────────

type ctxKey int

const impersonationCtxKey ctxKey = 1

// WithImpersonation marks a request as taken under an impersonation session, so every command it
// performs is audited AS impersonation (acting admin + tenant) rather than as the tenant (FR13).
func WithImpersonation(ctx context.Context, impID string) context.Context {
	return context.WithValue(ctx, impersonationCtxKey, impID)
}

// adminSessionFrom returns the acting admin's id from a request context. Private, because every
// public path in this package obtains the session through RequireSession and its error handling; this
// is the read for code that already knows a session is present.
func adminSessionFrom(ctx context.Context) (string, bool) {
	s, ok := adminidentity.SessionFrom(ctx)
	return s.AdminID, ok
}

// ImpersonationFrom returns the impersonation session id on this context, or "".
func ImpersonationFrom(ctx context.Context) string {
	v, _ := ctx.Value(impersonationCtxKey).(string)
	return v
}
