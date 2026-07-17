package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Policy is a context-assembly strategy. P2 ships only `full` (PRD §3 non-goals: sliding-window,
// summarization, RAG, and compaction are P3), but the INTERFACE lands here, because a `full`-shaped
// interface would force a breaking change in P3 — the risk the PRD calls "context interface too
// full-specific, blocks P3".
//
// What P2 needs from a policy, and therefore all this interface asserts:
//
//   - Name, the identifier a context entry's spec selects.
//   - ParamsSchema, the JSON Schema its params must satisfy — so `{"window": "big"}` is rejected when
//     the entry is REGISTERED, not when a run reaches the node. Carrying a schema rather than a Go
//     struct is what lets P3 add a policy with a rich param surface (retrieval handles, a summarizer
//     model_ref) without this package learning its shape (PRD OQ5).
//
// P3 adds the assembly method — the operation whose output the codemod rewrites into the call site's
// context construction. It is deliberately absent here rather than stubbed: P2 has nothing to
// implement behind it, and an unused method would freeze a signature designed with no caller.
type Policy interface {
	Name() string
	ParamsSchema() json.RawMessage
}

// FullPolicy passes the complete context through with no truncation, summarization, or retrieval.
// It is P2's only policy and the baseline every P3 policy is measured against.
type FullPolicy struct{}

func (FullPolicy) Name() string { return "full" }

// ParamsSchema takes no params, and says so precisely: additionalProperties:false means
// `{"window":50}` on a `full` policy is a loud error at registration rather than a param silently
// ignored at runtime — the exact mistake someone makes when they think they selected a P3 policy.
func (FullPolicy) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false}`)
}

// BuiltinPolicies returns the policies P2 ships. Task 1.5: register the `full` policy.
func BuiltinPolicies() []Policy { return []Policy{FullPolicy{}} }

// ContextSpec is a context entry's content: which policy, and the params it runs with.
type ContextSpec struct {
	Policy string          `json:"policy"`
	Params json.RawMessage `json:"params"`
}

// ContextEntry is a resolved context version.
type ContextEntry struct {
	VersionID string
	Name      string
	Spec      ContextSpec
	// Policy is the resolved implementation. Non-nil on success: a context entry naming a policy this
	// build does not implement fails to resolve rather than resolving to a nil strategy.
	Policy   Policy
	Envelope []byte
}

// RegisterContextPolicy publishes a context entry — a named policy plus its params — and returns its
// content-addressed version_id (task 1.5).
//
// An unregistered policy name is rejected here, which is the registry half of FR5's "reject a Variant
// Spec that references an unregistered context_policy": a spec cannot reference an entry that was
// never allowed to exist. params may be nil, meaning `{}`.
func (s *Store) RegisterContextPolicy(ctx context.Context, name, policyName string, params json.RawMessage) (versionID string, err error) {
	p, ok := s.policies[policyName]
	if !ok {
		return "", errInvalid("context entry %q: policy %q is not registered (available: %s)",
			name, policyName, strings.Join(s.policyNames(), ", "))
	}
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	sch, err := compileSchema("params_schema", p.ParamsSchema())
	if err != nil {
		// The policy's own schema is broken — a programming error in the implementation, not the
		// caller's input.
		return "", fmt.Errorf("context entry %q: policy %q has an invalid params schema: %w", name, policyName, err)
	}
	var decoded any
	if err := json.Unmarshal(params, &decoded); err != nil {
		return "", errInvalid("context entry %q: params is not valid JSON: %v", name, err)
	}
	if err := sch.Validate(decoded); err != nil {
		return "", errInvalid("context entry %q: params violate policy %q's schema: %v", name, policyName, err)
	}
	versionID, env, err := seal(KindContext, name, ContextSpec{Policy: policyName, Params: params})
	if err != nil {
		return "", err
	}
	if err := s.publish(ctx, tableContext, versionID, name, env, "", nil); err != nil {
		return "", err
	}
	return versionID, nil
}

// ResolveContextPolicy returns the context version a context_policy ref names, with its policy
// implementation bound.
func (s *Store) ResolveContextPolicy(ctx context.Context, versionID string) (*ContextEntry, error) {
	env, err := s.resolve(ctx, tableContext, versionID)
	if err != nil {
		return nil, err
	}
	var spec ContextSpec
	name, err := decodeEnvelope(KindContext, versionID, env, &spec)
	if err != nil {
		return nil, err
	}
	p, ok := s.policies[spec.Policy]
	if !ok {
		// A version published by a build that implemented a policy this one does not. Fail closed:
		// silently falling back to `full` would run a different strategy than the pinned spec names
		// and report the result as that spec's.
		return nil, fmt.Errorf("%w: context %s names policy %q, which is not implemented in this build",
			ErrNotFound, versionID, spec.Policy)
	}
	return &ContextEntry{VersionID: versionID, Name: name, Spec: spec, Policy: p, Envelope: env}, nil
}

func (s *Store) policyNames() []string {
	out := make([]string, 0, len(s.policies))
	for n := range s.policies {
		out = append(out, n)
	}
	sort.Strings(out) // map order must not leak into an error message
	return out
}

func errInvalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidEntry, fmt.Sprintf(format, args...))
}
