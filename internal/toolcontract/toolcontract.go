// Package toolcontract is the typed boundary between the agent loop and anything that acts.
//
// # Why a tool is a CONTRACT rather than a function
//
// A function has a signature. A contract additionally states what the tool may touch, how long it may
// take, whether retrying it is safe, and — the part that is usually missing — how to CHECK that the
// thing it claims to have done actually happened.
//
// That last part is the reason this package exists in this shape. A tool returning nil error means the
// call completed, not that the world changed. "Opened a pull request" returning cleanly while the pull
// request does not exist is not a hypothetical: it is what a timeout on the response leg of a
// successful write looks like from the caller's side, every time.
package toolcontract

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Call is one invocation.
type Call struct {
	TaskID string
	GoalID string
	// Kind selects the tool.
	Kind string
	// IdempotencyKey makes a retry safe. Required for effect-bearing tools and checked at Invoke, so a
	// tool cannot be the place the requirement is forgotten.
	IdempotencyKey string
	Input          []byte
	// Attempt is which try this is, passed through so a tool can log it and a verifier can distinguish
	// "already done by my own earlier attempt" from "done by somebody else".
	Attempt int
}

// Result is what a tool produced, including what it spent.
//
// 🔴 Spend is reported by the TOOL rather than estimated by the caller. A caller estimating tokens is a
// caller whose ceiling drifts from reality in whichever direction is least convenient to notice.
type Result struct {
	Output []byte
	Tokens int64
	// CostMicroCents is millionths of a cent; see provider.Price.CostMicroCents for why.
	CostMicroCents int64
	// ToolCalls counts underlying calls, which may exceed one for a tool that loops internally.
	ToolCalls int
}

// Permission is what a tool is allowed to touch. Declared, not inferred.
type Permission string

const (
	ReadSource      Permission = "read_source"
	WriteSource     Permission = "write_source"
	CallModel       Permission = "call_model"
	NetworkEgress   Permission = "network_egress"
	WriteRemoteRepo Permission = "write_remote_repo"
)

// Spec is everything about a tool that is not its behaviour.
type Spec struct {
	Kind string
	// Permissions are what this tool may touch. A tool that requests none and acts anyway is a bug the
	// registry cannot catch; a tool that declares them lets a reviewer read the blast radius off a table.
	Permissions []Permission
	// Timeout bounds one attempt. Required — a tool with no timeout is an unbounded run wearing a
	// smaller name, and it is the shape that hangs a worker until its lease expires.
	Timeout time.Duration
	// RetrySafe says whether a failed attempt may simply be retried. False means the tool must be given
	// an idempotency key AND have its effect verified before a retry, because the failure may have
	// occurred after the effect landed.
	RetrySafe bool
	// EffectBearing says this tool can change something outside the platform.
	EffectBearing bool
}

// Tool does the work.
type Tool interface {
	Spec() Spec
	Execute(ctx context.Context, c Call) (Result, error)
}

// Verifier independently confirms that a call's intended result actually occurred.
//
// # 🔴 Why this is a SEPARATE interface and not a method on Tool
//
// A tool verifying itself asks the component that may have failed to report on whether it failed. The
// useful check is the one that goes and looks: not "did my write return 200" but "is the row there".
// Keeping it separate makes it possible to say, structurally, that an effect-bearing tool without a
// verifier is not deployable — see Registry.Register.
//
// Verify returns (ok, why-not, error). The middle value matters: "I could not confirm it" and "I
// confirmed it did not happen" lead to different next actions, and collapsing them into a boolean makes
// a transient lookup failure indistinguishable from a real one.
type Verifier interface {
	Verify(ctx context.Context, c Call, r Result) (ok bool, detail string, err error)
}

var (
	ErrUnknownTool        = errors.New("toolcontract: no tool registered for this kind")
	ErrNoTimeout          = errors.New("toolcontract: tool declares no timeout")
	ErrNoVerifier         = errors.New("toolcontract: effect-bearing tool has no verifier")
	ErrNoIdempotency      = errors.New("toolcontract: effect-bearing call has no idempotency key")
	ErrNoPermissions      = errors.New("toolcontract: effect-bearing tool declares no permissions")
	ErrVerifyFailed       = errors.New("toolcontract: the tool reported success but its effect could not be confirmed")
	ErrVerifyInconclusive = errors.New("toolcontract: the effect could not be checked")
)

// Registry holds the tools a worker may invoke.
type Registry struct {
	tools     map[string]Tool
	verifiers map[string]Verifier
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}, verifiers: map[string]Verifier{}}
}

// Register admits a tool, refusing one that cannot be operated safely.
//
// 🔴 The refusals here are the whole point of registration. An effect-bearing tool with no verifier
// would be trusted on its own word about whether it changed the world; one with no declared permissions
// would have an unreadable blast radius; one with no timeout hangs a worker until its lease expires and
// looks, from outside, exactly like a crash. Each is caught at wiring time rather than at 3am.
func (r *Registry) Register(t Tool, v Verifier) error {
	s := t.Spec()
	if s.Kind == "" {
		return fmt.Errorf("toolcontract: tool has no kind")
	}
	if s.Timeout <= 0 {
		return fmt.Errorf("%w: %q", ErrNoTimeout, s.Kind)
	}
	if s.EffectBearing {
		if v == nil {
			return fmt.Errorf("%w: %q", ErrNoVerifier, s.Kind)
		}
		if len(s.Permissions) == 0 {
			return fmt.Errorf("%w: %q", ErrNoPermissions, s.Kind)
		}
	}
	if _, dup := r.tools[s.Kind]; dup {
		return fmt.Errorf("toolcontract: %q registered twice", s.Kind)
	}
	r.tools[s.Kind] = t
	if v != nil {
		r.verifiers[s.Kind] = v
	}
	return nil
}

// Kinds returns the registered kinds.
func (r *Registry) Kinds() []string {
	out := make([]string, 0, len(r.tools))
	for k := range r.tools {
		out = append(out, k)
	}
	return out
}

// Invoke runs one call: enforce the contract, execute under a timeout, then VERIFY.
//
// # Why verification is not optional and not the caller's business
//
// "Verify every step — do not trust tool execution alone" is a rule that decays the moment it is left
// to each call site, because the call site that skips it is the one written under time pressure. So
// Invoke does it, and a successful return from Invoke means the effect was confirmed rather than merely
// attempted.
//
// 🔴 A verifier that cannot reach its check returns ErrVerifyInconclusive, which is DISTINCT from
// confirmed-absent. The retry ladder treats them differently: an unconfirmable effect must not be
// blindly retried, because the effect may have landed and the retry would duplicate it.
func (r *Registry) Invoke(ctx context.Context, c Call) (Result, error) {
	t, ok := r.tools[c.Kind]
	if !ok {
		return Result{}, fmt.Errorf("%w: %q", ErrUnknownTool, c.Kind)
	}
	s := t.Spec()
	if s.EffectBearing && c.IdempotencyKey == "" {
		return Result{}, fmt.Errorf("%w: %q", ErrNoIdempotency, c.Kind)
	}

	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	res, err := t.Execute(ctx, c)
	if err != nil {
		return res, err
	}

	v, has := r.verifiers[c.Kind]
	if !has {
		// Only non-effect-bearing tools reach here; Register refuses the other case.
		return res, nil
	}
	ok, detail, verr := v.Verify(ctx, c, res)
	switch {
	case verr != nil:
		return res, fmt.Errorf("%w: %q: %v", ErrVerifyInconclusive, c.Kind, verr)
	case !ok:
		return res, fmt.Errorf("%w: %q: %s", ErrVerifyFailed, c.Kind, detail)
	}
	return res, nil
}
