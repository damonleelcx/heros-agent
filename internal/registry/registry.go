// Package registry implements the four P2 registries — model, prompt, skill, and context — that hold
// the reusable definitions a Variant Spec references by ID (PRD docs/prd/P2-config-runtime.md §6,
// FR6–FR10; spec openspec/changes/p2-config-runtime/specs/registries/spec.md; tasks 1.1–1.7).
//
// # Why immutability is the whole design
//
// Under ADR-001 a Variant Spec is realized by generating an AST codemod that rewrites the values in a
// registry entry into the user's call site. So a registry version is not a config row — it is the
// content of a source diff that must still be byte-identical when regenerated months later (FR5a,
// FR16). If a model_ref could silently change under a pinned spec, "reproducible" would be a
// coincidence. Hence: every published entry is addressed BY ITS CONTENT, and editing publishes a new
// version rather than mutating the old one.
//
//	version_id = SHA-256( canonical_json( {"kind":…, "name":…, "spec":{…}} ) )
//
// Canonicalization is internal/confighash — deliberately THE SAME canonicalizer that produces
// config_hash, so a spec and the refs inside it can never disagree about what "canonical" means.
//
// # Where each guarantee is enforced
//
// Defense in depth, mirroring the P0 lineage schema's stance — the application is the first line, the
// database is the last:
//
//   - This package offers NO mutation API. There is no UpdateModel; there is only Register*, which
//     computes the id from the content. Changing content changes the id. That is FR6 at the API shape
//     level: mutating a published version is not something a caller can express.
//   - db/migrations/postgres/0002_p2_registries.up.sql enforces the same invariant structurally, so a
//     row that violates it cannot be written even by SQL that bypasses this package: a CHECK proves
//     version_id = sha256(envelope), and a BEFORE UPDATE OR DELETE trigger rejects mutation outright.
//   - Every read re-verifies the stored bytes against the version_id (see verifyEnvelope), so a row
//     written by some future bypassing path is caught at resolution rather than trusted.
//
// # Expand-contract evolution (FR10, task 1.7)
//
// The rule that makes an old pinned version keep resolving after the schema grows is in resolve():
// decode the STORED envelope bytes; never re-marshal a decoded struct and re-hash it. A spec struct
// that gains an optional field must therefore leave old envelopes byte-identical. See the package
// tests for both directions (old row/new code, new row/old code).
package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/confighash"
)

// Kind names a registry. It is hashed into every version_id, so a model and a prompt with an
// identical name and spec cannot collide onto one id: a version_id is unique across all four
// registries, and a ref pasted into the wrong dimension fails closed instead of resolving.
type Kind string

const (
	KindModel   Kind = "model"
	KindPrompt  Kind = "prompt"
	KindSkill   Kind = "skill"
	KindContext Kind = "context"
)

// Table names. A closed set of package constants — never caller input — because they are formatted
// into SQL.
const (
	tableModel   = "model_entry"
	tablePrompt  = "prompt_entry"
	tableSkill   = "skill_entry"
	tableContext = "context_entry"
)

// Sentinel errors. The P2 Loader (task 3.1) must fail closed on an unresolved ref and distinguish
// "this ref names nothing" from "this ref names something broken", so these are typed rather than
// formatted strings.
var (
	// ErrNotFound means the version_id resolves to no entry. The Loader aborts on this before any
	// transform is generated (FR11).
	ErrNotFound = errors.New("registry: version_id not found")

	// ErrInvalidEntry means the caller's entry was rejected at registration — a malformed template,
	// an invalid JSON schema, an unregistered context policy.
	ErrInvalidEntry = errors.New("registry: invalid entry")

	// ErrCorruptEntry means a stored envelope does not hash to the version_id it is filed under, i.e.
	// content-addressing has been violated by something that bypassed both this package and the DB
	// guards. Never silently repaired: a corrupt entry would silently change a generated diff.
	ErrCorruptEntry = errors.New("registry: stored entry does not match its version_id")
)

// envelope is the hashed unit. Field order here is irrelevant — canonicalization sorts keys — but the
// JSON names are a frozen contract shared with the SQL guards in 0002, which read `kind`, `name`, and
// `spec.body_blob_hash` out of these exact bytes.
type envelope struct {
	Kind Kind            `json:"kind"`
	Name string          `json:"name"`
	Spec json.RawMessage `json:"spec"`
}

// seal computes the content address of an entry: it returns the version_id and the exact canonical
// bytes that id addresses. The bytes are what gets stored, so that a later read can re-derive the id
// from them and prove nothing drifted.
func seal(kind Kind, name string, spec any) (versionID string, canonical []byte, err error) {
	if name == "" {
		return "", nil, fmt.Errorf("%w: name must not be empty", ErrInvalidEntry)
	}
	specRaw, err := json.Marshal(spec)
	if err != nil {
		return "", nil, fmt.Errorf("%w: marshal %s spec: %v", ErrInvalidEntry, kind, err)
	}
	raw, err := json.Marshal(envelope{Kind: kind, Name: name, Spec: specRaw})
	if err != nil {
		return "", nil, fmt.Errorf("%w: marshal %s envelope: %v", ErrInvalidEntry, kind, err)
	}
	// CanonicalizeBytes fails loud on a number it cannot represent canonically (e.g. 1e+21 from a
	// float64 max_tokens) rather than hashing an ambiguous form — see confighash.ErrNonCanonicalNumber.
	canonical, err = confighash.CanonicalizeBytes(raw)
	if err != nil {
		return "", nil, fmt.Errorf("%w: canonicalize %s envelope: %v", ErrInvalidEntry, kind, err)
	}
	return hashBytes(canonical), canonical, nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// verifyEnvelope re-derives the content address of stored bytes and checks it against the id they
// were filed under. Called on every read: the cost is one SHA-256 over a small blob, and it is what
// makes "resolving a ref returns the content that ref addresses" a checked fact rather than a hope.
func verifyEnvelope(versionID string, stored []byte) error {
	if got := hashBytes(stored); got != versionID {
		return fmt.Errorf("%w: filed under %s but content addresses %s", ErrCorruptEntry, versionID, got)
	}
	return nil
}

// decodeEnvelope unpacks stored bytes into the envelope and the caller's spec type.
//
// The decode is deliberately lenient about unknown keys (no DisallowUnknownFields): a row written by
// a NEWER build whose spec type has a field this build has never heard of must still resolve, which
// is one half of expand-contract (FR10). The other half — a row written by an OLDER build, missing a
// field this build's spec type has — falls out of decoding into a zero value. Both halves rely on
// this function reading the STORED bytes and never re-hashing a re-marshal of `spec`.
func decodeEnvelope(kind Kind, versionID string, stored []byte, spec any) (name string, err error) {
	if err := verifyEnvelope(versionID, stored); err != nil {
		return "", err
	}
	var env envelope
	if err := json.Unmarshal(stored, &env); err != nil {
		return "", fmt.Errorf("%w: decode envelope %s: %v", ErrCorruptEntry, versionID, err)
	}
	if env.Kind != kind {
		return "", fmt.Errorf("%w: %s is a %s entry, not a %s entry", ErrCorruptEntry, versionID, env.Kind, kind)
	}
	if spec != nil {
		if err := json.Unmarshal(env.Spec, spec); err != nil {
			return "", fmt.Errorf("%w: decode %s spec %s: %v", ErrCorruptEntry, kind, versionID, err)
		}
	}
	return env.Name, nil
}
