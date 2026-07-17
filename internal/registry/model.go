package registry

import (
	"context"
)

// ModelParams are the inference params pinned by a model version (FR9).
//
// Pointers, not values, and every field `omitempty`. This is not style — it is what makes FR10's
// expand-contract promise hold for the field the registry is most likely to grow:
//
//   - A pointer distinguishes "temperature 0" (pin it) from "temperature unset" (leave the call
//     site's own value alone). With a float64, those are the same thing, and the codemod could not
//     tell "override to 0" from "no override" — the per-dimension independence FR2 requires would
//     collapse for exactly the values callers care most about.
//   - `omitempty` keeps an unset field OUT of the hashed envelope, so adding a param later leaves the
//     version_id of every entry that does not set it unchanged. Registering the same model after the
//     struct grows still dedups to the same id.
//
// A non-omitempty field added here would not break resolution of an old pinned version (resolve()
// reads stored bytes), but it WOULD change the id that identical content hashes to going forward.
// Keep new fields optional and omitempty.
type ModelParams struct {
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	// ThinkingBudget is the extended-thinking token budget. Modeled generically rather than as one
	// provider's parameter name: the gateway (task 4.3) must be able to map it per provider so P6 can
	// tier models without re-plumbing the ModelEntry shape.
	ThinkingBudget *int `json:"thinking_budget,omitempty"`
	// Seed is the provider-level sampling seed. It is pinned in the VERSION rather than passed at call
	// time so that a config_hash fully determines it (FR16). Providers do not honour a seed bitwise
	// (PRD OQ2) — reproducibility is asserted on identical seed PROPAGATION, not identical output.
	Seed *int64 `json:"seed,omitempty"`
	// TimeoutSeconds overrides the gateway's 60 s default per model entry (PRD §7 Reliability).
	TimeoutSeconds *int `json:"timeout_seconds,omitempty"`
}

// ModelSpec is a model entry's content: provider + model ID + inference params as ONE versioned unit
// (FR9). Bundling them is the point — a model_ref resolves the provider, the id, and every param
// exactly as stored, and changing any one of them requires publishing a new version_id, so it cannot
// silently alter a pinned Variant Spec's generated diff.
type ModelSpec struct {
	Provider string      `json:"provider"`
	ModelID  string      `json:"model_id"`
	Params   ModelParams `json:"params"`
}

// ModelEntry is a resolved model version.
type ModelEntry struct {
	VersionID string
	Name      string
	Spec      ModelSpec
	// Envelope is the exact canonical-JSON byte string version_id addresses. Carried so callers that
	// re-hash (the config_hash computation in task 2.5) use the stored bytes rather than re-marshal.
	Envelope []byte
}

// RegisterModel publishes a model entry and returns its content-addressed version_id (task 1.2).
//
// Registering identical content again returns the same version_id and publishes nothing — the id IS
// the content, so there is no second version to make. Registering CHANGED content returns a
// different version_id and leaves the previous version untouched and resolvable: that is the whole
// of "editing produces a new version, the old one stays intact" (FR6), and it needs no versioning
// logic because content-addressing gives it for free.
func (s *Store) RegisterModel(ctx context.Context, name string, spec ModelSpec) (versionID string, err error) {
	if spec.Provider == "" {
		return "", errInvalid("model entry %q: provider must not be empty", name)
	}
	if spec.ModelID == "" {
		return "", errInvalid("model entry %q: model_id must not be empty", name)
	}
	versionID, env, err := seal(KindModel, name, spec)
	if err != nil {
		return "", err
	}
	if err := s.publish(ctx, tableModel, versionID, name, env, "", nil); err != nil {
		return "", err
	}
	return versionID, nil
}

// ResolveModel returns the model version a model_ref names, or ErrNotFound. The Loader (task 3.1)
// fails closed on ErrNotFound before any transform is generated.
func (s *Store) ResolveModel(ctx context.Context, versionID string) (*ModelEntry, error) {
	env, err := s.resolve(ctx, tableModel, versionID)
	if err != nil {
		return nil, err
	}
	var spec ModelSpec
	name, err := decodeEnvelope(KindModel, versionID, env, &spec)
	if err != nil {
		return nil, err
	}
	return &ModelEntry{VersionID: versionID, Name: name, Spec: spec, Envelope: env}, nil
}
