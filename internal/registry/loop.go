package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The loop registry (P34 tasks 3.2–3.5, decisions.md D-34.1; ADR-014).
//
// Shaped deliberately like harness.go rather than as a new storage pattern: seal the spec into the
// envelope, publish under its content address, resolve by reading the STORED bytes back. There is no
// seventh way to store a registry entry here, which is the point — a new Kind should cost a table and a
// file, not a new theory of persistence.

// LoopSpec is a loop entry's content: which strategy, and the params it runs with. It is the hashed
// unit — changing either publishes a new version_id and leaves the old one resolvable, so a pinned
// spec's meaning cannot shift underneath it.
type LoopSpec struct {
	Strategy string          `json:"strategy"`
	Params   json.RawMessage `json:"params"`
}

// LoopEntry is a resolved loop version.
type LoopEntry struct {
	VersionID string
	Name      string
	Spec      LoopSpec
	// Strategy is the resolved implementation. Non-nil on success: an entry naming a strategy this build
	// does not implement fails to RESOLVE rather than resolving to a nil strategy (task 3.4).
	Strategy LoopStrategy
	Envelope []byte
}

// IsSingleShot reports whether this entry is the identity strategy. The one place identity-ness is
// decided for the loop axis, so resolve, the transform, the coverage read and the console cannot
// disagree about what counts as "no iteration change".
func (e *LoopEntry) IsSingleShot() bool { return e != nil && e.Spec.Strategy == StrategySingleShot }

// MaxTurns returns the turn count this entry chose, and whether it chose one at all. `single-shot`
// expresses none (its schema forbids the key), so it reports (1, false) — one turn, not chosen.
//
// 🔴 The bool is not decoration. "Chose 1" is inexpressible and "chose nothing" must not be read as
// "chose 1": the envelope's ceiling check has nothing to compare against when no value was chosen, and
// comparing a fabricated 1 would make the check silently pass on a shape it never examined.
func (e *LoopEntry) MaxTurns() (int, bool) {
	if e == nil || e.Spec.Strategy == StrategySingleShot {
		return 1, false
	}
	var p struct {
		MaxTurns *int `json:"max_turns"`
	}
	if err := json.Unmarshal(e.Spec.Params, &p); err != nil || p.MaxTurns == nil {
		return 1, false
	}
	return *p.MaxTurns, true
}

// RegisterLoop publishes a loop entry — a named strategy plus its params — and returns its
// content-addressed version_id.
//
// 🔴 Every check runs BEFORE seal, so a malformed entry never acquires a version_id. That ordering is
// the requirement, not an implementation detail: an id for a rejected entry is an id someone can paste
// into a spec, and it would resolve to nothing forever.
//
// 🔴 The critic ref is resolved against the MODEL registry here. It cannot live in ValidateLoopParams,
// which is a pure function with no database — and that separation is honest rather than awkward:
// "these params are well-formed" is answerable on every keystroke, while "this critic exists" is a
// question only a store can answer.
func (s *Store) RegisterLoop(ctx context.Context, name, strategyName string, params json.RawMessage) (versionID string, err error) {
	_, params, err = s.ValidateLoopParams(name, strategyName, params)
	if err != nil {
		return "", err
	}
	if err := validateLoopCriticRef(ctx, s, name, params); err != nil {
		return "", err
	}
	versionID, env, err := seal(KindLoop, name, LoopSpec{Strategy: strategyName, Params: params})
	if err != nil {
		return "", err
	}
	table, err := tableFor(KindLoop)
	if err != nil {
		return "", err
	}
	if err := s.publish(ctx, table, versionID, name, env, "", nil); err != nil {
		return "", err
	}
	return versionID, nil
}

// ValidateLoopParams runs everything RegisterLoop checks that needs NO database. Exported and separate
// from RegisterLoop for the reason ValidateHarnessParams is: the AUTHORING surface must be able to tell
// a user their params are wrong without registering anything, and the alternative — a second validator
// beside this one — would be two rules to keep true, with the copy in the authoring path being the one
// that drifts. One validator, two callers.
func (s *Store) ValidateLoopParams(name, strategyName string, params json.RawMessage) (LoopStrategy, json.RawMessage, error) {
	st, ok := s.loops[strategyName]
	if !ok {
		return nil, nil, errInvalid("loop entry %q: strategy %q is not registered (available: %s)",
			name, strategyName, strings.Join(s.loopNames(), ", "))
	}
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	sch, err := compileSchema("params_schema", st.ParamsSchema())
	if err != nil {
		return nil, nil, fmt.Errorf("loop entry %q: strategy %q has an invalid params schema: %w", name, strategyName, err)
	}
	var decoded any
	if err := json.Unmarshal(params, &decoded); err != nil {
		return nil, nil, errInvalid("loop entry %q: params is not valid JSON: %v", name, err)
	}
	if err := sch.Validate(decoded); err != nil {
		return nil, nil, errInvalid("loop entry %q: params violate strategy %q's schema: %v", name, strategyName, err)
	}
	if err := validateLoopDependencies(name, strategyName, params); err != nil {
		return nil, nil, err
	}
	return st, params, nil
}

// validateLoopDependencies enforces the cross-field rules a params schema states awkwardly and a reader
// understands immediately.
//
// Two of them, and the second is P34 task 3.5's half that a JSON Schema cannot state usefully:
//
//  1. a `reflexion` stopping on `answer-marker` must declare the marker;
//  2. `max_turns` below one is REFUSED, never defaulted.
//
// (2) is expressed in every multi-turn schema's `minimum` as well, and is re-stated here on purpose:
// the schema's message ("must be >= 2") tells a user what to type, and this one tells them why a loop
// with no turns is not a loop. Both fire; the Go check runs second and is the one a reader remembers.
func validateLoopDependencies(name, strategyName string, params json.RawMessage) error {
	var p struct {
		StopCondition string `json:"stop_condition"`
		AnswerMarker  string `json:"answer_marker"`
		MaxTurns      *int   `json:"max_turns"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errInvalid("loop entry %q: params is not a JSON object: %v", name, err)
	}
	if p.StopCondition == StopAnswerMarker && strings.TrimSpace(p.AnswerMarker) == "" {
		return errInvalid("loop entry %q: strategy %q stops on %q but declares no answer_marker; a "+
			"marker-terminated loop with no marker can only ever stop at the turn ceiling, which is a "+
			"different (and more expensive) configuration than the one being asked for",
			name, strategyName, StopAnswerMarker)
	}
	if p.MaxTurns != nil && *p.MaxTurns < 1 {
		return errInvalid("loop entry %q: max_turns is %d. A loop with no turns is not a loop, and "+
			"reading it as 1 would run a single shot under a multi-turn config_hash — so it is REFUSED "+
			"rather than defaulted. If one turn is what you want, the strategy for that is %q.",
			name, *p.MaxTurns, StrategySingleShot)
	}
	return nil
}

// validateLoopCriticRef enforces the cross-registry half: a declared critic ref must resolve to a MODEL
// entry, or the loop entry is rejected before it is sealed.
//
// 🔴 The rejection is phrased as a REGISTRATION failure, not a resolution one, because the reader's next
// question is "was anything stored?" and the answer is no.
func validateLoopCriticRef(ctx context.Context, r modelLookup, name string, params json.RawMessage) error {
	ref := criticModelRef(params)
	if ref == "" {
		return nil
	}
	if _, err := r.ResolveModel(ctx, ref); err != nil {
		return errInvalid("loop entry %q: critic_model_ref %q does not resolve to a model entry (%v); "+
			"a critic that cannot be resolved cannot be pinned, and a loop judged by an unknown critic is "+
			"not reproducible", name, ref, err)
	}
	return nil
}

// ResolveLoop returns the loop version a loop_ref names, with its strategy implementation bound.
//
// It resolves ONLY against the loop registry. A loop version_id handed to ResolveHarness — or a harness
// version_id handed here — is ErrNotFound, and that is structural rather than checked: the Kind is part
// of the content address (seal), so the two id spaces cannot overlap except by SHA-256 collision.
func (s *Store) ResolveLoop(ctx context.Context, versionID string) (*LoopEntry, error) {
	table, err := tableFor(KindLoop)
	if err != nil {
		return nil, err
	}
	env, err := s.resolve(ctx, table, versionID)
	if err != nil {
		return nil, err
	}
	var spec LoopSpec
	name, err := decodeEnvelope(KindLoop, versionID, env, &spec)
	if err != nil {
		return nil, err
	}
	st, err := s.bindLoopStrategy(versionID, spec.Strategy)
	if err != nil {
		return nil, err
	}
	return &LoopEntry{VersionID: versionID, Name: name, Spec: spec, Strategy: st, Envelope: env}, nil
}

// bindLoopStrategy binds a stored strategy NAME to this build's implementation, or fails closed
// (task 3.4).
//
// 🔴 Its own function so the decision is testable without a database, because it is the part that can
// silently do the wrong thing: a version published by a build that implemented a strategy this one does
// not must FAIL, never fall back to `single-shot` — which would run one turn while the pinned spec named
// a loop, and report the result under that spec's config_hash.
func (s *Store) bindLoopStrategy(versionID, strategyName string) (LoopStrategy, error) {
	st, ok := s.loops[strategyName]
	if !ok {
		return nil, fmt.Errorf("%w: loop %s names strategy %q, which is not implemented in this build "+
			"(implemented here: %s). 🚫 It does NOT fall back to %q: that would run one turn under a "+
			"multi-turn config_hash and report the result as if the loop had run",
			ErrNotFound, versionID, strategyName, strings.Join(s.loopNames(), ", "), StrategySingleShot)
	}
	return st, nil
}

// LoopVersions lists every published version of a loop entry name, oldest first.
func (s *Store) LoopVersions(ctx context.Context, name string) ([]string, error) {
	table, err := tableFor(KindLoop)
	if err != nil {
		return nil, err
	}
	return s.versions(ctx, table, name)
}

// AddLoopStrategy makes a strategy implementation available to RegisterLoop. The same seam AddPolicy,
// AddMemoryStrategy and AddHarnessStrategy are.
func (s *Store) AddLoopStrategy(st LoopStrategy) { s.loops[st.Name()] = st }

// LoopStrategyNamed returns the builtin loop strategy with this name, for a caller that needs its
// schema or its human labels without going through the database. Nil when the name is outside the
// closed set — a caller must fail closed on that, never invent a strategy.
func LoopStrategyNamed(name string) LoopStrategy {
	for _, st := range BuiltinLoopStrategies() {
		if st.Name() == name {
			return st
		}
	}
	return nil
}

// LoopStrategyNames lists the closed builtin vocabulary, sorted. Read by the runtime's conformance
// test, by the coverage read and by the console's strategy list, so all three name the same set.
func LoopStrategyNames() []string {
	out := make([]string, 0, LoopStrategySetSize)
	for _, st := range BuiltinLoopStrategies() {
		out = append(out, st.Name())
	}
	sort.Strings(out)
	return out
}

// IsLoopStrategy reports whether a strategy name is a member of the loop vocabulary.
//
// 🔴 This is the predicate that decides whether a legacy HARNESS entry is loop-bearing, and it is one
// function rather than a list repeated at each call site. See HarnessEntry.IsLoopBearing.
func IsLoopStrategy(name string) bool {
	for _, st := range BuiltinLoopStrategies() {
		if st.Name() == name {
			return true
		}
	}
	return false
}

func (s *Store) loopNames() []string {
	out := make([]string, 0, len(s.loops))
	for n := range s.loops {
		out = append(out, n)
	}
	sort.Strings(out) // map order must not leak into an error message
	return out
}
