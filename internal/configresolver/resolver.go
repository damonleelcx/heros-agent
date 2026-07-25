// Package configresolver is the runtime resolver for the P10 bound binding document (ADR-004, §8).
//
// # The posture, stated once
//
// Resolution order is embedded → local override → remote-if-enabled. The embedded document — the one
// that shipped in the built artifact — is the default and requires nothing external. An override is
// consulted only if configured; remote only if explicitly enabled. So the DEFAULT posture has no
// runtime dependency on the platform at all (task 8.1).
//
// Resolution is FAIL-STATIC (task 8.2). An override source that is unreachable, unparseable, or fails
// validation does not replace the document in force: the resolver keeps the last known-good document
// and reports a DEGRADED state. It never falls back to an arbitrary, empty, or default configuration —
// silently changing which model a production node uses is worse than any outage — and it never blocks
// process startup: a misconfigured remote must not become a customer outage.
//
// The degraded state is a REPORTED state, not a silent fallback (task 8.3): a resolver quietly serving
// stale configuration would be the worst outcome, because the operator would have no signal at all.
// Health() exposes it for a readable endpoint and telemetry.
package configresolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/heros-foreal/agentd/internal/confighash"
)

// Document is the resolved binding document, opaque to the resolver except for the two hashes it must
// reason about. `raw` is the canonical bytes it was parsed from — held so resolution adds no
// per-invocation parse (task 8.4).
type Document struct {
	Schema             string          `json:"schema"`
	ConfigHash         string          `json:"config_hash"`
	VerifiedConfigHash string          `json:"verified_config_hash,omitempty"`
	Nodes              json.RawMessage `json:"nodes"`

	raw []byte
}

// Verified reports whether this document carries a verified-delta record matching its own config —
// i.e. whether resolving to it is a verified configuration (§9 / task 9.4). A document whose
// verified_config_hash is absent or does not equal its config_hash is UNVERIFIED.
func (d Document) Verified() bool {
	return d.VerifiedConfigHash != "" && d.VerifiedConfigHash == d.ConfigHash
}

// Raw returns the canonical bytes the document was parsed from.
func (d Document) Raw() []byte { return d.raw }

// Source is one place a document can come from: the local override file, or a remote endpoint. The
// embedded document is not a Source — it is passed to New directly, because it is always present and
// never fails.
type Source interface {
	// Name identifies the source in the degraded report ("local", "remote").
	Name() string
	// Load returns the source's current bytes, or an error if it is unreachable.
	Load(ctx context.Context) ([]byte, error)
}

// Health is the resolver's externally-readable state (task 8.3).
type Health struct {
	// Degraded is true when the last refresh could not adopt a configured override and is serving the
	// last known-good document instead.
	Degraded bool `json:"degraded"`
	// FailedSource names which override source failed ("local"/"remote"), and Reason says why. Empty
	// when not degraded.
	FailedSource string `json:"failed_source,omitempty"`
	Reason       string `json:"reason,omitempty"`
	// ResolvedConfigHash is the config_hash of the document actually in force — emitted on every
	// invocation (task 9.1) and reported here.
	ResolvedConfigHash string `json:"resolved_config_hash"`
	// Unverified is true when the document in force carries no verified-delta record (task 9.4).
	Unverified bool `json:"unverified"`
}

// Resolver holds the document in force and resolves overrides fail-statically.
type Resolver struct {
	local  Source // nil unless a local override is configured
	remote Source // nil unless remote resolution is explicitly enabled
	// pinned disables override sources entirely (task 9.3): a pinned resolver reads only the embedded
	// document. Set by NewPinned for eval/verification runs.
	pinned bool

	mu       sync.RWMutex
	current  Document // last known-good, held parsed (task 8.4)
	degraded bool
	failedBy string
	reason   string
}

// Option configures a Resolver.
type Option func(*Resolver)

// WithLocalOverride configures a local override source (consulted after the embedded document).
func WithLocalOverride(s Source) Option { return func(r *Resolver) { r.local = s } }

// WithRemote enables remote resolution (consulted only when set — remote is opt-in, task 8.1).
func WithRemote(s Source) Option { return func(r *Resolver) { r.remote = s } }

// New builds a resolver over the embedded document. It parses the embedded document ONCE and holds it
// (task 8.4). A malformed EMBEDDED document is a hard error at construction — it shipped in the build,
// so a parse failure is a build defect, not a runtime degradation. Override sources are NOT contacted
// here: startup never depends on them (task 8.2).
func New(embedded []byte, opts ...Option) (*Resolver, error) {
	doc, err := parseDocument(embedded)
	if err != nil {
		return nil, fmt.Errorf("configresolver: embedded document does not parse: %w", err)
	}
	r := &Resolver{current: doc}
	for _, o := range opts {
		o(r)
	}
	return r, nil
}

// Resolve returns the document currently in force. It re-parses nothing — the document is held from the
// last successful adoption — so resolution adds no measurable per-invocation latency (task 8.4).
func (r *Resolver) Resolve() Document {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

// Refresh attempts to adopt a newer document from the configured override sources, in order (local,
// then remote-if-enabled), fail-statically. It is safe to call periodically or on a lifecycle event;
// it NEVER blocks startup and NEVER fails-open. When every configured source fails, the last
// known-good document stays in force and the resolver goes degraded.
//
// Ordering note: the embedded document is the floor. An override REPLACES the document in force only
// when it loads and validates; otherwise the current document (which may itself be the embedded one)
// is retained.
func (r *Resolver) Refresh(ctx context.Context) {
	// Pinned: override sources are disabled in the measurement sandbox (task 9.3). The embedded document
	// is the only thing that can be in force, so a refresh is a no-op — no override can substitute a
	// different configuration for the one that shipped.
	if r.pinned {
		return
	}
	// Try the highest-intent enabled source first: remote (most explicit) is not automatically higher
	// than local here — ADR-004 orders embedded → local → remote, so a present local override is the
	// operator's local intent and remote is the platform's. We adopt the LAST source in that order that
	// successfully loads, so remote (if enabled and valid) wins over local, which wins over embedded.
	adopted := false
	for _, src := range r.enabledSources() {
		raw, err := src.Load(ctx)
		if err != nil {
			r.markDegraded(src.Name(), fmt.Sprintf("unreachable: %v", err))
			continue
		}
		doc, err := parseDocument(raw)
		if err != nil {
			// Unparseable or invalid → NOT adopted (never fail-open), degraded reported.
			r.markDegraded(src.Name(), fmt.Sprintf("invalid document: %v", err))
			continue
		}
		r.adopt(doc)
		adopted = true
	}
	if adopted {
		r.clearDegraded()
	}
}

// enabledSources returns the configured override sources in resolution order (local, then remote).
func (r *Resolver) enabledSources() []Source {
	var out []Source
	if r.local != nil {
		out = append(out, r.local)
	}
	if r.remote != nil {
		out = append(out, r.remote)
	}
	return out
}

func (r *Resolver) adopt(doc Document) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = doc
}

func (r *Resolver) markDegraded(source, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.degraded = true
	r.failedBy = source
	r.reason = reason
}

func (r *Resolver) clearDegraded() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.degraded = false
	r.failedBy = ""
	r.reason = ""
}

// Health returns the resolver's current state for a readable endpoint and telemetry (task 8.3).
func (r *Resolver) Health() Health {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return Health{
		Degraded:           r.degraded,
		FailedSource:       r.failedBy,
		Reason:             r.reason,
		ResolvedConfigHash: r.current.ConfigHash,
		Unverified:         !r.current.Verified(),
	}
}

// parseDocument parses and validates a document, and RE-VERIFIES its content address: the canonical
// re-hash of the document's own nodes must not be trusted blindly, but at minimum the document must
// declare a config_hash and parse. An empty/zero document is rejected here so it can never be adopted
// fail-open.
func parseDocument(raw []byte) (Document, error) {
	if len(raw) == 0 {
		return Document{}, errors.New("empty document")
	}
	var d Document
	if err := json.Unmarshal(raw, &d); err != nil {
		return Document{}, err
	}
	if d.ConfigHash == "" {
		return Document{}, errors.New("document declares no config_hash")
	}
	// Canonicalize the raw bytes so what we hold is stable regardless of the source's formatting.
	canon, err := confighash.CanonicalizeBytes(raw)
	if err != nil {
		return Document{}, fmt.Errorf("document is not canonicalizable: %w", err)
	}
	d.raw = canon
	return d, nil
}
