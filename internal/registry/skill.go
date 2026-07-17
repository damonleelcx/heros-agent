package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// SkillSpec is a skill entry's content (FR8): a JSON-schema contract for inputs and outputs, plus a
// handle naming the implementation.
//
// Hashing the SCHEMAS into the version_id — not just the skill's name — is what makes a skill_ref
// pin a contract. If the version pinned only a name, tightening a skill's input schema would
// retroactively change what an already-pinned Variant Spec means, and P2's promise that an old spec
// still resolves and produces the same diff would be false for exactly the dimension most likely to
// be edited.
type SkillSpec struct {
	// ImplHandle names the implementation to bind at the call site, e.g. "builtin:search". P2 runs
	// only trusted/built-in skills; sandboxed execution of arbitrary repo tool code is P3 (PRD §3).
	ImplHandle   string          `json:"impl_handle"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
}

// SkillEntry is a resolved skill version, with both schemas compiled ready for the runtime to
// validate argument shape against before the transform binds the skill at a call site (task 3.2).
type SkillEntry struct {
	VersionID string
	Name      string
	Spec      SkillSpec
	Input     *jsonschema.Schema
	Output    *jsonschema.Schema
	Envelope  []byte
}

// compileSchema validates a JSON Schema document and compiles it.
//
// Compiling is how the schema ITSELF is validated (task 1.4): the compiler checks the document
// against its dialect's metaschema, so `{"type":"nonsense"}` is rejected at registration rather than
// at the first node execution that happens to exercise it.
//
// The compiler is HERMETIC — UseLoader refuses every remote reference. By default the library would
// fetch an `$ref` to an absolute URL over the network, which would make registration depend on a
// third party's uptime, make a version_id's meaning depend on content we never hashed, and let a
// registered schema drive an outbound request. A remote $ref is rejected, loudly, instead.
func compileSchema(field string, raw json.RawMessage) (*jsonschema.Schema, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errInvalid("skill entry: %s must not be empty", field)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, errInvalid("skill entry: %s is not valid JSON: %v", field, err)
	}
	c := jsonschema.NewCompiler()
	c.UseLoader(offlineLoader{})
	const loc = "heros:skill-contract"
	if err := c.AddResource(loc, doc); err != nil {
		return nil, errInvalid("skill entry: %s: %v", field, err)
	}
	sch, err := c.Compile(loc)
	if err != nil {
		return nil, errInvalid("skill entry: %s is not a valid JSON Schema: %v", field, err)
	}
	return sch, nil
}

// offlineLoader refuses to resolve any reference the schema document does not carry itself.
type offlineLoader struct{}

func (offlineLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("remote $ref %q is not allowed: a skill contract must be self-contained "+
		"so its version_id addresses the whole contract", url)
}

// RegisterSkill publishes a skill entry and returns its content-addressed version_id (task 1.4).
// Both schemas are validated before anything is published, so an invalid contract never gets an id.
func (s *Store) RegisterSkill(ctx context.Context, name string, spec SkillSpec) (versionID string, err error) {
	if spec.ImplHandle == "" {
		return "", errInvalid("skill entry %q: impl_handle must not be empty", name)
	}
	if _, err := compileSchema("input_schema", spec.InputSchema); err != nil {
		return "", fmt.Errorf("skill entry %q: %w", name, err)
	}
	if _, err := compileSchema("output_schema", spec.OutputSchema); err != nil {
		return "", fmt.Errorf("skill entry %q: %w", name, err)
	}
	versionID, env, err := seal(KindSkill, name, spec)
	if err != nil {
		return "", err
	}
	if err := s.publish(ctx, tableSkill, versionID, name, env, "", nil); err != nil {
		return "", err
	}
	return versionID, nil
}

// ResolveSkill returns the skill version a skill_ref names, with both schemas compiled.
//
// A skill_ref naming a version that is not present resolves to ErrNotFound and the node does not
// execute — the registries spec's "unavailable skill rejected" scenario, enforced by the Loader
// failing closed on this error (FR11).
func (s *Store) ResolveSkill(ctx context.Context, versionID string) (*SkillEntry, error) {
	env, err := s.resolve(ctx, tableSkill, versionID)
	if err != nil {
		return nil, err
	}
	var spec SkillSpec
	name, err := decodeEnvelope(KindSkill, versionID, env, &spec)
	if err != nil {
		return nil, err
	}
	in, err := compileSchema("input_schema", spec.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("%w: skill %s: %v", ErrCorruptEntry, versionID, err)
	}
	out, err := compileSchema("output_schema", spec.OutputSchema)
	if err != nil {
		return nil, fmt.Errorf("%w: skill %s: %v", ErrCorruptEntry, versionID, err)
	}
	return &SkillEntry{VersionID: versionID, Name: name, Spec: spec, Input: in, Output: out, Envelope: env}, nil
}

// ValidateInput checks an argument object against the skill's input contract. The runtime calls this
// before the transform binds the skill at a call site, so an argument-shape violation is caught
// before the node executes and before the implementation is invoked (FR8).
func (e *SkillEntry) ValidateInput(args any) error {
	if err := e.Input.Validate(args); err != nil {
		return fmt.Errorf("%w: skill %q input violates its contract: %v", ErrInvalidEntry, e.Name, err)
	}
	return nil
}

// ValidateOutput checks a skill's result against its output contract.
func (e *SkillEntry) ValidateOutput(result any) error {
	if err := e.Output.Validate(result); err != nil {
		return fmt.Errorf("%w: skill %q output violates its contract: %v", ErrInvalidEntry, e.Name, err)
	}
	return nil
}
