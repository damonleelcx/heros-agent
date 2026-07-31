package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The memory registry (P17 task 5.2, decisions.md D1).
//
// Shaped deliberately like model.go and context.go rather than as a new storage pattern: seal the spec
// into the envelope, publish under its content address, resolve by reading the STORED bytes back. There
// is no fifth way to store a registry entry here, which is the point — a new Kind should cost a table and
// a file, not a new theory of persistence.
//
// What it borrows from context.go is the schema gate: a strategy declares a ParamsSchema, and params that
// violate it are rejected AT SEAL, before the entry is stored, rather than discovered when a run reaches
// the node. What it borrows from model.go is everything else.

// MemorySpec is a memory entry's content: which strategy, and the params it runs with. It is the hashed
// unit — changing either publishes a new version_id and leaves the old one resolvable, so a pinned spec's
// meaning cannot shift underneath it (FR6).
type MemorySpec struct {
	Strategy string          `json:"strategy"`
	Params   json.RawMessage `json:"params"`
}

// MemoryEntry is a resolved memory version.
type MemoryEntry struct {
	VersionID string
	Name      string
	Spec      MemorySpec
	// Strategy is the resolved implementation. Non-nil on success: an entry naming a strategy this build
	// does not implement fails to RESOLVE rather than resolving to a nil strategy. Falling back to `none`
	// would be the worst available behaviour — it would run no memory while the pinned spec named one, and
	// report the result under that spec's config_hash.
	Strategy MemoryStrategy
	Envelope []byte
}

// IsNone reports whether this entry is the identity strategy. The one place `none`-ness is decided, so
// resolve, the transform refusal, and the console cannot disagree about what counts as "no memory"
// (decisions.md D3).
func (e *MemoryEntry) IsNone() bool { return e != nil && e.Spec.Strategy == StrategyNone }

// RegisterMemory publishes a memory entry — a named strategy plus its params — and returns its
// content-addressed version_id (task 5.2).
//
// An unregistered strategy name is rejected here, which is the registry half of "a name outside the closed
// set does not resolve": a spec cannot reference an entry that was never allowed to exist. params may be
// nil, meaning `{}`.
//
// 🔴 The schema check runs BEFORE seal, so a malformed entry never acquires a version_id. That ordering is
// the requirement, not an implementation detail: an id for a rejected entry is an id someone can paste
// into a spec, and it would resolve to nothing forever.
func (s *Store) RegisterMemory(ctx context.Context, name, strategyName string, params json.RawMessage) (versionID string, err error) {
	_, params, err = s.ValidateMemoryParams(name, strategyName, params)
	if err != nil {
		return "", err
	}
	versionID, env, err := seal(KindMemory, name, MemorySpec{Strategy: strategyName, Params: params})
	if err != nil {
		return "", err
	}
	if err := s.publish(ctx, tableMemory, versionID, name, env, "", nil); err != nil {
		return "", err
	}
	return versionID, nil
}

// ValidateMemoryParams runs everything RegisterMemory checks BEFORE it seals or writes: the strategy is
// in the closed set, the params are JSON, and they satisfy the strategy's ParamsSchema. It returns the
// resolved strategy and the params normalized to `{}` when empty.
//
// It is exported and separate from RegisterMemory for one reason: the AUTHORING surface must be able to
// tell a user their params are wrong without registering anything (P17 §9), and the alternative — a
// second validator beside this one — would be two rules to keep true, with the copy in the authoring
// path being the one that drifts (禁止分裂 source-of-truth). One validator, two callers.
//
// 🔴 It performs no write and touches no database, which is what makes it safe for a surface to call on
// every keystroke and what makes "rejected before the entry is stored" structural rather than a matter of
// call ordering.
func (s *Store) ValidateMemoryParams(name, strategyName string, params json.RawMessage) (MemoryStrategy, json.RawMessage, error) {
	st, ok := s.strategies[strategyName]
	if !ok {
		return nil, nil, errInvalid("memory entry %q: strategy %q is not registered (available: %s)",
			name, strategyName, strings.Join(s.strategyNames(), ", "))
	}
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	sch, err := compileSchema("params_schema", st.ParamsSchema())
	if err != nil {
		// The strategy's own schema is broken — a programming error in this package, not the caller's
		// input, so it is not dressed up as ErrInvalidEntry.
		return nil, nil, fmt.Errorf("memory entry %q: strategy %q has an invalid params schema: %w", name, strategyName, err)
	}
	var decoded any
	if err := json.Unmarshal(params, &decoded); err != nil {
		return nil, nil, errInvalid("memory entry %q: params is not valid JSON: %v", name, err)
	}
	if err := sch.Validate(decoded); err != nil {
		return nil, nil, errInvalid("memory entry %q: params violate strategy %q's schema: %v", name, strategyName, err)
	}
	return st, params, nil
}

// ResolveMemory returns the memory version a memory_ref names, with its strategy implementation bound.
//
// It resolves ONLY against the memory registry. A memory version_id handed to ResolveModel — or a model
// version_id handed here — is ErrNotFound, and that is structural rather than checked: the Kind is part of
// the content address (seal), so the two id spaces cannot overlap except by SHA-256 collision.
func (s *Store) ResolveMemory(ctx context.Context, versionID string) (*MemoryEntry, error) {
	env, err := s.resolve(ctx, tableMemory, versionID)
	if err != nil {
		return nil, err
	}
	var spec MemorySpec
	name, err := decodeEnvelope(KindMemory, versionID, env, &spec)
	if err != nil {
		return nil, err
	}
	st, ok := s.strategies[spec.Strategy]
	if !ok {
		// A version published by a build that implemented a strategy this one does not. Fail closed:
		// silently falling back to `none` would run no memory while the pinned spec named one, and report
		// the result as that spec's.
		return nil, fmt.Errorf("%w: memory %s names strategy %q, which is not implemented in this build",
			ErrNotFound, versionID, spec.Strategy)
	}
	return &MemoryEntry{VersionID: versionID, Name: name, Spec: spec, Strategy: st, Envelope: env}, nil
}

// AddMemoryStrategy makes a strategy implementation available to RegisterMemory. It is the seam a future
// memory-runtime phase plugs a strategy into — the same shape as AddPolicy — so growing the vocabulary is
// an implementation plus a version bump, never a schema change.
func (s *Store) AddMemoryStrategy(st MemoryStrategy) { s.strategies[st.Name()] = st }

// MemoryStrategyNamed returns the builtin strategy with this name, for a caller that needs its schema or
// its human labels without going through the database (the authoring surface's strategy list). Nil when
// the name is outside the closed set — a caller must fail closed on that, never invent a strategy.
func MemoryStrategyNamed(name string) MemoryStrategy {
	for _, st := range BuiltinMemoryStrategies() {
		if st.Name() == name {
			return st
		}
	}
	return nil
}

func (s *Store) strategyNames() []string {
	out := make([]string, 0, len(s.strategies))
	for n := range s.strategies {
		out = append(out, n)
	}
	sort.Strings(out) // map order must not leak into an error message
	return out
}
