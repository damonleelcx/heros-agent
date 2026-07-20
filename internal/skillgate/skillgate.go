// Package skillgate is the runtime contract gate for Skill Registry entries (P3 skill-registry spec,
// tasks 2.3–2.6). The Skill Registry (internal/registry) already validates a skill's JSON-schema
// contract as well-formed at REGISTRATION; this package enforces it at EXECUTION:
//
//   - CheckInput runs BEFORE the implementation is invoked. It validates (a) tool availability — the
//     skill version resolves AND its implementation handle is bindable in the sandbox — and (b) that
//     the argument object conforms to the input schema. On any failure the impl is never invoked.
//   - CheckOutput runs BEFORE the result propagates downstream. It validates the result against the
//     output schema. On failure the result is discarded and the node fails closed.
//
// Every failure is a typed *ContractError naming the skill and the violated field, so the unhappy path
// tells the operator exactly which dimension broke (Product Designer discipline) rather than surfacing
// a partial or default result (backend rule: fail closed, no silent proceed).
//
// # Determinism (spec: "deterministic and independent of ambient host state")
//
// Validation is a function of (version_id, args) alone. Resolution reads the content-addressed
// registry row; schema validation is pure; and the default Binder inspects only the impl handle's
// shape — never env vars, the host filesystem, or the wall-clock. Two validations of the same
// version_id against the same args return the same verdict.
package skillgate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Resolver resolves a skill version to its compiled contract. registry.Store satisfies it.
type Resolver interface {
	ResolveSkill(ctx context.Context, versionID string) (*registry.SkillEntry, error)
}

// Binder reports whether a skill's implementation handle can be bound in the sandbox isolate. This is
// the "tool availability" half of pre-execution validation (spec 2.3a). The sandbox (P3 §3) supplies
// the real implementation; a nil binder means "resolution alone proves availability" but the default
// HandleFormatBinder is used when none is given so an unbindable handle still fails closed.
//
// CanBind MUST be deterministic and free of ambient host state (spec 2.6): it inspects the handle, not
// the machine.
type Binder interface {
	CanBind(implHandle string) error
}

// HandleFormatBinder accepts impl handles whose scheme this platform knows how to bind: `builtin:<name>`
// for trusted host built-ins and `repo:<path>` for sandboxed repo tool code. An unknown scheme is
// unbindable and fails closed — the exact "impl handle cannot be bound in the sandbox" case (spec 2.3).
type HandleFormatBinder struct{}

func (HandleFormatBinder) CanBind(implHandle string) error {
	if implHandle == "" {
		return errors.New("empty impl handle")
	}
	for _, scheme := range []string{"builtin:", "repo:"} {
		if strings.HasPrefix(implHandle, scheme) && len(implHandle) > len(scheme) {
			return nil
		}
	}
	return fmt.Errorf("impl handle %q names no bindable scheme (want builtin:<name> or repo:<path>)", implHandle)
}

// FailureKind classifies which contract check failed, so a caller can branch (retry vs. abort) and a
// metric can slice by reason without parsing the message.
type FailureKind string

const (
	FailureUnavailable  FailureKind = "unavailable"   // version does not resolve, or impl handle unbindable
	FailureInputSchema  FailureKind = "input_schema"  // argument object violates the input contract
	FailureOutputSchema FailureKind = "output_schema" // result violates the output contract
)

// ErrContract is the sentinel every gate failure wraps, so callers fail closed with errors.Is.
var ErrContract = errors.New("skillgate: skill contract violation")

// ContractError names the skill, the failing check, and — for schema violations — the violated field.
type ContractError struct {
	Skill     string
	VersionID string
	Kind      FailureKind
	Field     string // JSON pointer-ish path of the offending field; "" when not field-specific
	Reason    string
}

func (e *ContractError) Error() string {
	loc := ""
	if e.Field != "" {
		loc = fmt.Sprintf(" field %q", e.Field)
	}
	skill := e.Skill
	if skill == "" {
		skill = e.VersionID
	}
	return fmt.Sprintf("%s: skill %q (%s)%s: %s", ErrContract.Error(), skill, e.Kind, loc, e.Reason)
}

func (e *ContractError) Unwrap() error { return ErrContract }

// Gate ties resolution, availability, and schema validation into the two runtime checks.
type Gate struct {
	resolver Resolver
	binder   Binder
}

// New builds a Gate. A nil binder defaults to HandleFormatBinder so availability is always checked —
// never silently skipped (a skipped availability check is a fail-open, which the spec forbids).
func New(resolver Resolver, binder Binder) *Gate {
	if binder == nil {
		binder = HandleFormatBinder{}
	}
	return &Gate{resolver: resolver, binder: binder}
}

// CheckInput validates availability + argument shape BEFORE the implementation is invoked (spec 2.3).
// On success it returns the resolved entry, which the caller passes to CheckOutput after invoking. On
// any failure it returns a *ContractError and the caller MUST NOT invoke the implementation.
func (g *Gate) CheckInput(ctx context.Context, versionID string, args any) (*registry.SkillEntry, error) {
	entry, err := g.resolver.ResolveSkill(ctx, versionID)
	if err != nil {
		// Unresolvable version = unavailable tool. Fail closed before any impl thought (spec 2.3 scenario
		// "Unavailable tool fails closed"). errors.Is preserves ErrNotFound for callers that branch on it.
		return nil, &ContractError{
			VersionID: versionID, Kind: FailureUnavailable,
			Reason: fmt.Sprintf("skill version does not resolve: %v", err),
		}
	}
	if err := g.binder.CanBind(entry.Spec.ImplHandle); err != nil {
		return nil, &ContractError{
			Skill: entry.Name, VersionID: versionID, Kind: FailureUnavailable,
			Reason: fmt.Sprintf("implementation handle not bindable in the sandbox: %v", err),
		}
	}
	if err := entry.Input.Validate(args); err != nil {
		return nil, &ContractError{
			Skill: entry.Name, VersionID: versionID, Kind: FailureInputSchema,
			Field: violatedField(err), Reason: "argument object violates the input contract",
		}
	}
	return entry, nil
}

// CheckOutput validates a result against the output schema BEFORE it propagates downstream (spec 2.4).
// A violation returns a *ContractError; the caller MUST discard the result and fail the node closed.
func (g *Gate) CheckOutput(entry *registry.SkillEntry, result any) error {
	if entry == nil {
		return &ContractError{Kind: FailureOutputSchema, Reason: "no resolved skill entry to validate against"}
	}
	if err := entry.Output.Validate(result); err != nil {
		return &ContractError{
			Skill: entry.Name, VersionID: entry.VersionID, Kind: FailureOutputSchema,
			Field: violatedField(err), Reason: "result violates the output contract",
		}
	}
	return nil
}

// violatedField extracts the offending field path from a jsonschema validation error. It walks to the
// most specific cause and reports its instance location as a slash path (e.g. "top_k", "items/0/id"),
// so the ContractError names the field, not just "something was wrong".
func violatedField(err error) string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return ""
	}
	leaf := deepest(ve)
	if len(leaf.InstanceLocation) == 0 {
		return "" // a root-level violation (e.g. wrong top-level type) has no sub-field
	}
	return strings.Join(leaf.InstanceLocation, "/")
}

// deepest returns the most specific validation error — the leaf cause with the longest instance
// location — so "the object's `top_k` field is out of range" beats the generic root failure above it.
func deepest(ve *jsonschema.ValidationError) *jsonschema.ValidationError {
	best := ve
	var walk func(v *jsonschema.ValidationError)
	walk = func(v *jsonschema.ValidationError) {
		if len(v.InstanceLocation) > len(best.InstanceLocation) {
			best = v
		}
		for _, c := range v.Causes {
			walk(c)
		}
	}
	walk(ve)
	return best
}
