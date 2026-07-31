package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The harness registry (P18 tasks 2.1–2.5, decisions.md D-1).
//
// Shaped deliberately like memory.go rather than as a new storage pattern: seal the spec into the
// envelope, publish under its content address, resolve by reading the STORED bytes back. There is no
// sixth way to store a registry entry here, which is the point — a new Kind should cost a table and a
// file, not a new theory of persistence.
//
// What it adds over memory.go is one check memory did not need: `critic-loop` names a SEPARATE critic
// model by ref, and that ref is resolved against the MODEL registry at registration. A cross-registry
// check has to live where a database is reachable, which is why the pure validator below and the
// registering function below it are two functions rather than one.

// HarnessSpec is a harness entry's content: which strategy, and the params it runs with. It is the hashed
// unit — changing either publishes a new version_id and leaves the old one resolvable, so a pinned spec's
// meaning cannot shift underneath it (FR4, FR6).
type HarnessSpec struct {
	Strategy string          `json:"strategy"`
	Params   json.RawMessage `json:"params"`
}

// HarnessEntry is a resolved harness version.
type HarnessEntry struct {
	VersionID string
	Name      string
	Spec      HarnessSpec
	// Strategy is the resolved implementation. Non-nil on success: an entry naming a strategy this build
	// does not implement fails to RESOLVE rather than resolving to a nil strategy. Falling back to
	// `single-shot` would be the worst available behaviour — it would run one turn while the pinned spec
	// named a loop, and report the result under that spec's config_hash.
	Strategy HarnessStrategy
	Envelope []byte
}

// IsSingleShot reports whether this entry is the identity strategy. The one place identity-ness is
// decided, so resolve, the transform, the coverage read and the console cannot disagree about what counts
// as "no scaffold change" (decisions.md D-8, D-11).
func (e *HarnessEntry) IsSingleShot() bool { return e != nil && e.Spec.Strategy == StrategySingleShot }

// RegisterHarness publishes a harness entry — a named strategy plus its params — and returns its
// content-addressed version_id (task 2.1).
//
// An unregistered strategy name is rejected here, which is the registry half of "a name outside the closed
// set does not resolve": a spec cannot reference an entry that was never allowed to exist. params may be
// nil, meaning `{}`.
//
// 🔴 Every check runs BEFORE seal, so a malformed entry never acquires a version_id. That ordering is the
// requirement, not an implementation detail: an id for a rejected entry is an id someone can paste into a
// spec, and it would resolve to nothing forever.
//
// 🔴 The critic ref is resolved against the MODEL registry here (task 2.3). It cannot live in
// ValidateHarnessParams, which is a pure function with no database — and that separation is honest rather
// than awkward: "these params are well-formed" is answerable on every keystroke, while "this critic
// exists" is a question only a store can answer, and pretending otherwise would make the authoring
// surface either wrong or slow.
func (s *Store) RegisterHarness(ctx context.Context, name, strategyName string, params json.RawMessage) (versionID string, err error) {
	_, params, err = s.ValidateHarnessParams(name, strategyName, params)
	if err != nil {
		return "", err
	}
	if err := validateCriticRef(ctx, s, name, params); err != nil {
		return "", err
	}
	versionID, env, err := seal(KindHarness, name, HarnessSpec{Strategy: strategyName, Params: params})
	if err != nil {
		return "", err
	}
	if err := s.publish(ctx, tableHarness, versionID, name, env, "", nil); err != nil {
		return "", err
	}
	return versionID, nil
}

// ValidateHarnessParams runs everything RegisterHarness checks that needs NO database: the strategy is in
// the closed set, the params are JSON, they satisfy the strategy's ParamsSchema, and the cross-field
// dependencies hold. It returns the resolved strategy and the params normalized to `{}` when empty.
//
// It is exported and separate from RegisterHarness for one reason: the AUTHORING surface must be able to
// tell a user their params are wrong without registering anything (P18 task 12.1), and the alternative —
// a second validator beside this one — would be two rules to keep true, with the copy in the authoring
// path being the one that drifts. One validator, two callers.
//
// 🔴 It performs no write and touches no database, which is what makes it safe for a surface to call on
// every keystroke and what makes "rejected before the entry is stored" structural rather than a matter of
// call ordering.
func (s *Store) ValidateHarnessParams(name, strategyName string, params json.RawMessage) (HarnessStrategy, json.RawMessage, error) {
	st, ok := s.harnesses[strategyName]
	if !ok {
		return nil, nil, errInvalid("harness entry %q: strategy %q is not registered (available: %s)",
			name, strategyName, strings.Join(s.harnessNames(), ", "))
	}
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	sch, err := compileSchema("params_schema", st.ParamsSchema())
	if err != nil {
		// The strategy's own schema is broken — a programming error in this package, not the caller's
		// input, so it is not dressed up as ErrInvalidEntry.
		return nil, nil, fmt.Errorf("harness entry %q: strategy %q has an invalid params schema: %w", name, strategyName, err)
	}
	var decoded any
	if err := json.Unmarshal(params, &decoded); err != nil {
		return nil, nil, errInvalid("harness entry %q: params is not valid JSON: %v", name, err)
	}
	if err := sch.Validate(decoded); err != nil {
		return nil, nil, errInvalid("harness entry %q: params violate strategy %q's schema: %v", name, strategyName, err)
	}
	if err := validateHarnessDependencies(name, strategyName, params); err != nil {
		return nil, nil, err
	}
	return st, params, nil
}

// validateHarnessDependencies enforces the cross-field rules a params schema states awkwardly and a
// reader understands immediately.
//
// Today there is exactly one: a `reflexion` stopping on `answer-marker` must declare the marker. Writing
// it as an if/then in the JSON Schema is possible, and it produces an error message
// ("doesn't validate with .../then") that tells a user nothing about what to type.
func validateHarnessDependencies(name, strategyName string, params json.RawMessage) error {
	var p struct {
		StopCondition string `json:"stop_condition"`
		AnswerMarker  string `json:"answer_marker"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errInvalid("harness entry %q: params is not a JSON object: %v", name, err)
	}
	if p.StopCondition == StopAnswerMarker && strings.TrimSpace(p.AnswerMarker) == "" {
		return errInvalid("harness entry %q: strategy %q stops on %q but declares no answer_marker; a "+
			"marker-terminated loop with no marker can only ever stop at the turn ceiling, which is a "+
			"different (and more expensive) configuration than the one being asked for",
			name, strategyName, StopAnswerMarker)
	}
	return nil
}

// StopAnswerMarker is the stop condition whose marker is required. Declared here rather than inline so the
// runtime, the schema and this check name one string.
const StopAnswerMarker = "answer-marker"

// modelLookup is the one thing the critic check needs from a store: the ability to resolve a model
// version_id. Declared as an interface here, rather than taking *Store, so the check is exercisable
// against a fake — which matters because the alternative is a check that only runs when a Postgres is
// up, i.e. a check that is green by absence in every unit run.
type modelLookup interface {
	ResolveModel(ctx context.Context, versionID string) (*ModelEntry, error)
}

// validateCriticRef enforces task 2.3's cross-registry half: a declared critic ref must resolve to a
// MODEL entry, or the harness entry is rejected before it is sealed.
//
// 🔴 The rejection is phrased as a REGISTRATION failure, not a resolution one, because the reader's next
// question is "was anything stored?" and the answer is no. An entry that acquired a version_id and then
// failed to resolve its critic would be an id someone can paste into a spec forever.
func validateCriticRef(ctx context.Context, r modelLookup, name string, params json.RawMessage) error {
	ref := criticModelRef(params)
	if ref == "" {
		return nil
	}
	if _, err := r.ResolveModel(ctx, ref); err != nil {
		return errInvalid("harness entry %q: critic_model_ref %q does not resolve to a model entry (%v); "+
			"a critic that cannot be resolved cannot be pinned, and a loop judged by an unknown critic is "+
			"not reproducible", name, ref, err)
	}
	return nil
}

// criticModelRef reads the critic ref out of validated params, or "" when there is none. Deliberately
// tolerant of absence — only `critic-loop` declares the field, and its schema has already made it
// required by the time this runs.
func criticModelRef(params json.RawMessage) string {
	var p struct {
		CriticModelRef string `json:"critic_model_ref"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return ""
	}
	return strings.TrimSpace(p.CriticModelRef)
}

// ResolveHarness returns the harness version a harness_ref names, with its strategy implementation bound.
//
// It resolves ONLY against the harness registry. A harness version_id handed to ResolveModel — or a model
// version_id handed here — is ErrNotFound, and that is structural rather than checked: the Kind is part of
// the content address (seal), so the two id spaces cannot overlap except by SHA-256 collision.
func (s *Store) ResolveHarness(ctx context.Context, versionID string) (*HarnessEntry, error) {
	env, err := s.resolve(ctx, tableHarness, versionID)
	if err != nil {
		return nil, err
	}
	var spec HarnessSpec
	name, err := decodeEnvelope(KindHarness, versionID, env, &spec)
	if err != nil {
		return nil, err
	}
	st, err := s.bindHarnessStrategy(versionID, spec.Strategy)
	if err != nil {
		return nil, err
	}
	return &HarnessEntry{VersionID: versionID, Name: name, Spec: spec, Strategy: st, Envelope: env}, nil
}

// bindHarnessStrategy binds a stored strategy NAME to this build's implementation, or fails closed.
//
// 🔴 It is its own function so the decision is testable without a database. The decision is the part that
// can silently do the wrong thing: a version published by a build that implemented a strategy this one
// does not must FAIL, never fall back to `single-shot` — which would run one turn while the pinned spec
// named a loop, and report the result under that spec's config_hash.
func (s *Store) bindHarnessStrategy(versionID, strategyName string) (HarnessStrategy, error) {
	st, ok := s.harnesses[strategyName]
	if !ok {
		return nil, fmt.Errorf("%w: harness %s names strategy %q, which is not implemented in this build "+
			"(implemented here: %s)", ErrNotFound, versionID, strategyName, strings.Join(s.harnessNames(), ", "))
	}
	return st, nil
}

// HarnessVersions lists every published version of a harness entry name, oldest first.
func (s *Store) HarnessVersions(ctx context.Context, name string) ([]string, error) {
	return s.versions(ctx, tableHarness, name)
}

// AddHarnessStrategy makes a strategy implementation available to RegisterHarness. It is the same seam
// AddPolicy and AddMemoryStrategy are, so growing the vocabulary is an implementation plus a version bump,
// never a schema change.
func (s *Store) AddHarnessStrategy(st HarnessStrategy) { s.harnesses[st.Name()] = st }

// HarnessStrategyNamed returns the builtin strategy with this name, for a caller that needs its schema or
// its human labels without going through the database (the authoring surface's strategy list). Nil when
// the name is outside the closed set — a caller must fail closed on that, never invent a strategy.
func HarnessStrategyNamed(name string) HarnessStrategy {
	for _, st := range BuiltinHarnessStrategies() {
		if st.Name() == name {
			return st
		}
	}
	return nil
}

// HarnessStrategyNames lists the closed builtin vocabulary, sorted. Read by the runtime's conformance
// test and by the console's strategy list, so both name the same set.
func HarnessStrategyNames() []string {
	out := make([]string, 0, HarnessStrategySetSize)
	for _, st := range BuiltinHarnessStrategies() {
		out = append(out, st.Name())
	}
	sort.Strings(out)
	return out
}

func (s *Store) harnessNames() []string {
	out := make([]string, 0, len(s.harnesses))
	for n := range s.harnesses {
		out = append(out, n)
	}
	sort.Strings(out) // map order must not leak into an error message
	return out
}
