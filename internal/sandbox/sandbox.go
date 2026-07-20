// Package sandbox is the P3 isolate: repo tool code from a target repo is untrusted, and every node
// that runs it does so inside a per-node isolate that holds no ambient credentials, denies network
// egress by default, exposes a least-privilege read-only filesystem, and enforces hard resource
// bounds (sandbox spec, tasks 3.1–3.7). This is the platform's sharpest security boundary.
//
// # Why a capability-gated enforcer, and why it fails closed
//
// The isolate's restrictions are enforced by an OS-level `Enforcer`. Some controls are portable and
// always available — a scrubbed environment (no ambient creds), a wall-clock deadline, a captured-
// output cap, an ephemeral scratch dir. Others — a network-egress namespace, a filesystem-scope
// namespace — depend on the platform (Linux namespaces, macOS `sandbox-exec`) and on privilege. The
// Sandbox asks the enforcer what it can guarantee and compares that to what the node's Spec REQUIRES.
// If a required restriction cannot be enforced, the isolate is not created and the node fails closed
// (task 3.6): there is no downgrade to running untrusted code on the host. That is the single most
// important property here — a fallback-to-host on sandbox failure would convert an operational error
// into a full security bypass exactly when the environment is already degraded.
//
// # What crosses the boundary
//
// Nothing persists between nodes except the typed I/O contract carried back through the host. A tool
// that legitimately needs a credentialed call (an LLM or retrieval) does not hold the credential; it
// asks the host **broker** (internal/sandbox/broker), which performs the call with real secrets and
// returns only the result. Every denied action and every lifecycle transition is emitted as a tagged
// audit event with no secret values in it.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DenialKind classifies a denied action for audit (task 5.2). A closed vocabulary so a consumer can
// slice sandbox-denial rate by reason without parsing a message.
type DenialKind string

const (
	DenyEgress     DenialKind = "egress"          // un-allowlisted outbound connection attempt
	DenyCredential DenialKind = "credential_read" // attempt to read env/cred files/metadata endpoint
	DenyFilesystem DenialKind = "filesystem"      // read outside the declared working set
	DenyResource   DenialKind = "resource"        // CPU/mem/wall-clock/PID/output bound breach
	DenyBroker     DenialKind = "broker"          // brokered call to a non-allowlisted host
)

// Phase is an isolate lifecycle transition (task 5.2: lifecycle transitions are audited).
type Phase string

const (
	PhaseCreated      Phase = "created"
	PhaseStarted      Phase = "started"
	PhaseFinished     Phase = "finished"
	PhaseDestroyed    Phase = "destroyed"
	PhaseCreateFailed Phase = "create_failed" // isolation could not be established → node fails closed
)

// EventKind distinguishes a denial from a lifecycle transition on the audit stream.
type EventKind string

const (
	EventDenial    EventKind = "denial"
	EventLifecycle EventKind = "lifecycle"
)

// Event is one audit record. It NEVER carries a secret value (task 5.2): only the node/run identity,
// what happened, and why. The telemetry adapter (internal/sandbox → P2.5) turns it into a tagged
// metric event carrying the P0 tag set.
type Event struct {
	Kind   EventKind
	NodeID string
	RunID  string
	Denial DenialKind // set when Kind == EventDenial
	Phase  Phase      // set when Kind == EventLifecycle
	Reason string     // human-readable, secret-free
}

// AuditSink receives audit events. Nil is allowed (a Sandbox with no sink still enforces; it just does
// not record) but production always wires one — a denial nobody can read is not a control (DevOps rule:
// health signals must be externally readable).
type AuditSink interface {
	Record(Event)
}

// ResourceBounds are the per-isolate limits. None may be zero-as-unbounded: DefaultBounds fills any
// unset field, and Validate rejects a non-positive bound rather than treating it as "no limit".
type ResourceBounds struct {
	CPU       time.Duration // CPU time (RLIMIT_CPU where supported)
	Memory    int64         // address-space bytes (RLIMIT_AS where supported)
	Wallclock time.Duration // hard wall-clock deadline (always enforced by the host)
	MaxPIDs   int           // process/thread count (RLIMIT_NPROC where supported)
	MaxOutput int64         // captured-output bytes (always enforced by the host reader)
}

// DefaultBounds are the design defaults (1 vCPU-second-ish budget expressed as wall CPU / 512 MB /
// 60 s / 128 PIDs / 8 MB). Configurable per node, never unbounded.
func DefaultBounds() ResourceBounds {
	return ResourceBounds{
		CPU:       30 * time.Second,
		Memory:    512 << 20,
		Wallclock: 60 * time.Second,
		MaxPIDs:   128,
		MaxOutput: 8 << 20,
	}
}

func (b ResourceBounds) withDefaults() ResourceBounds {
	d := DefaultBounds()
	if b.CPU <= 0 {
		b.CPU = d.CPU
	}
	if b.Memory <= 0 {
		b.Memory = d.Memory
	}
	if b.Wallclock <= 0 {
		b.Wallclock = d.Wallclock
	}
	if b.MaxPIDs <= 0 {
		b.MaxPIDs = d.MaxPIDs
	}
	if b.MaxOutput <= 0 {
		b.MaxOutput = d.MaxOutput
	}
	return b
}

// EgressPolicy is default-deny: only hosts on the allowlist may be reached, and the allowlist is empty
// by default (task 3.3). It governs both direct egress (enforced by the OS network namespace) and the
// broker (task 4.2), so a tool cannot use the broker to bypass the deny.
type EgressPolicy struct {
	Allow []string
}

// Permits reports whether host is on the allowlist. An empty allowlist permits nothing.
func (p EgressPolicy) Permits(host string) bool {
	for _, h := range p.Allow {
		if h == host {
			return true
		}
	}
	return false
}

// Spec configures one node's isolate.
type Spec struct {
	NodeID string
	RunID  string
	// WorkingSet is the set of host paths the tool may read (read-only). Nothing else is visible: no
	// host FS, no other run's data, no `.git` credentials, no secrets store (task 3.4).
	WorkingSet []string
	Bounds     ResourceBounds
	Egress     EgressPolicy
	// RequireNetworkIsolation / RequireFilesystemScope express the restrictions this node's tool code
	// demands. When true and the enforcer cannot guarantee them, the isolate fails closed (task 3.6).
	// Untrusted repo tool code always sets both; they are fields (not always-on) so a trusted host-side
	// step that legitimately needs neither is not forced through an enforcer that cannot run on this box.
	RequireNetworkIsolation bool
	RequireFilesystemScope  bool
}

// Tool is the untrusted unit executed inside the isolate: an external command (the repo's tool code).
type Tool struct {
	Argv  []string
	Stdin []byte
}

// Result is the isolate outcome.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	// Denials collected during the run (also emitted to the AuditSink as they occur).
	Denials []Denial
	// ResourceBreach names the bound that terminated the isolate, if any.
	ResourceBreach string
}

// Denial is a recorded denied action.
type Denial struct {
	Kind   DenialKind
	Reason string
}

// Typed errors so callers fail closed with errors.Is.
var (
	// ErrIsolateUnavailable: the required restrictions could not be established. The node MUST NOT run
	// the tool on the host (task 3.6).
	ErrIsolateUnavailable = errors.New("sandbox: isolate could not be created with the required restrictions")
	// ErrResourceBreach: a resource bound was breached; the isolate was terminated (task 3.5).
	ErrResourceBreach = errors.New("sandbox: resource bound breached; isolate terminated")
	// ErrInvalidSpec: the spec is malformed (e.g. no argv).
	ErrInvalidSpec = errors.New("sandbox: invalid isolate spec")
)

// Sandbox creates and runs isolates. It is safe for concurrent use; each Run gets its own isolate, and
// a breach in one terminates only that isolate (task 3.5: other runs unaffected).
type Sandbox struct {
	enforcer Enforcer
	audit    AuditSink
	pool     *warmPool
}

// Option configures a Sandbox.
type Option func(*Sandbox)

// WithAudit wires the audit sink.
func WithAudit(a AuditSink) Option { return func(s *Sandbox) { s.audit = a } }

// WithWarmPool amortizes isolate start with a warm pool of size n (task 3.7).
func WithWarmPool(n int) Option {
	return func(s *Sandbox) {
		if n > 0 {
			s.pool = newWarmPool(n)
		}
	}
}

// New builds a Sandbox over an Enforcer. The enforcer is the platform's isolation backend; New does
// not pick one for you because "which isolation this host can provide" is a deployment fact the caller
// owns (production wires the OS enforcer; tests wire a controllable one).
func New(e Enforcer, opts ...Option) *Sandbox {
	s := &Sandbox{enforcer: e}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Run creates a per-node isolate, executes the tool inside it, and destroys it. It fails closed if the
// isolate cannot be created with every required restriction (task 3.6) — returning ErrIsolateUnavailable
// and never touching the host execution path.
func (s *Sandbox) Run(ctx context.Context, spec Spec, tool Tool) (*Result, error) {
	if len(tool.Argv) == 0 {
		return nil, fmt.Errorf("%w: tool has no argv", ErrInvalidSpec)
	}
	spec.Bounds = spec.Bounds.withDefaults()

	// Fail-closed capability check BEFORE anything runs. This is the whole security posture in one gate:
	// if the platform cannot deny egress or scope the filesystem that this node requires, the untrusted
	// code does not run at all.
	caps := s.enforcer.Capabilities()
	if missing := requiredButMissing(spec, caps); missing != "" {
		s.record(Event{Kind: EventLifecycle, NodeID: spec.NodeID, RunID: spec.RunID, Phase: PhaseCreateFailed,
			Reason: "cannot enforce required restriction: " + missing})
		return nil, fmt.Errorf("%w: %s", ErrIsolateUnavailable, missing)
	}
	// A scrubbed environment (no ambient credentials) is non-negotiable for every isolate.
	if !caps.ScrubEnv {
		s.record(Event{Kind: EventLifecycle, NodeID: spec.NodeID, RunID: spec.RunID, Phase: PhaseCreateFailed,
			Reason: "cannot scrub ambient credentials from the isolate environment"})
		return nil, fmt.Errorf("%w: no credential scrub", ErrIsolateUnavailable)
	}

	s.record(Event{Kind: EventLifecycle, NodeID: spec.NodeID, RunID: spec.RunID, Phase: PhaseCreated})

	iso, err := s.enforcer.Create(ctx, spec, s.pool)
	if err != nil {
		s.record(Event{Kind: EventLifecycle, NodeID: spec.NodeID, RunID: spec.RunID, Phase: PhaseCreateFailed,
			Reason: "isolate creation failed: " + err.Error()})
		return nil, fmt.Errorf("%w: %v", ErrIsolateUnavailable, err)
	}
	// Ephemeral lifecycle: the isolate is always destroyed, even on panic — no state persists between
	// nodes (task 3.7).
	defer func() {
		iso.Destroy()
		s.record(Event{Kind: EventLifecycle, NodeID: spec.NodeID, RunID: spec.RunID, Phase: PhaseDestroyed})
	}()

	s.record(Event{Kind: EventLifecycle, NodeID: spec.NodeID, RunID: spec.RunID, Phase: PhaseStarted})
	res, err := iso.Exec(ctx, tool, func(d Denial) {
		s.record(Event{Kind: EventDenial, NodeID: spec.NodeID, RunID: spec.RunID, Denial: d.Kind, Reason: d.Reason})
	})
	s.record(Event{Kind: EventLifecycle, NodeID: spec.NodeID, RunID: spec.RunID, Phase: PhaseFinished})
	return res, err
}

func (s *Sandbox) record(e Event) {
	if s.audit != nil {
		s.audit.Record(e)
	}
}

// CanIsolate reports whether this host can create an isolate with EVERY restriction untrusted repo tool
// code requires: no ambient credentials, default-deny egress, a scoped read-only filesystem, and
// resource bounds. It is the availability predicate the skill binder uses (nodeexec.SandboxBinder): if
// this is false, a repo tool is unbindable and its node fails closed rather than running unsandboxed.
func (s *Sandbox) CanIsolate() bool {
	c := s.enforcer.Capabilities()
	return c.ScrubEnv && c.ResourceLimits && c.NetworkDeny && c.FilesystemScope
}

// requiredButMissing returns the name of the first required-but-unenforceable restriction, or "".
func requiredButMissing(spec Spec, caps Capabilities) string {
	if spec.RequireNetworkIsolation && !caps.NetworkDeny {
		return "network egress isolation"
	}
	if spec.RequireFilesystemScope && !caps.FilesystemScope {
		return "filesystem scoping"
	}
	if !caps.ResourceLimits {
		return "resource bounds"
	}
	return ""
}

// Enforcer is the platform isolation backend. Capabilities reports what this host can guarantee (read
// before every Run, so a degraded host fails closed); Create builds one isolate honoring the spec.
type Enforcer interface {
	Capabilities() Capabilities
	Create(ctx context.Context, spec Spec, pool *warmPool) (Isolate, error)
}

// Capabilities is the set of restrictions an enforcer can guarantee on this host.
type Capabilities struct {
	ScrubEnv        bool // start with no ambient credentials
	ResourceLimits  bool // CPU/mem/PID/wall-clock/output bounds
	NetworkDeny     bool // default-deny egress
	FilesystemScope bool // read-only working-set-scoped FS
}

// Isolate is one live isolate.
type Isolate interface {
	// Exec runs the tool, calling onDenial for each denied action as it happens. It returns a typed
	// resource error (wrapping ErrResourceBreach) if a bound is breached.
	Exec(ctx context.Context, tool Tool, onDenial func(Denial)) (*Result, error)
	// Destroy tears the isolate down and removes its ephemeral scratch. Idempotent.
	Destroy()
}
