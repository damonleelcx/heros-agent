// Package nodeexec is the node-execution path for a repo-tool skill (P3, closing the "wire the gate +
// sandbox into node execution" follow-up). It composes the three P3 controls into the single operation
// the runtime performs when a node runs untrusted repo tool code:
//
//	CheckInput (availability + arg schema, pre-invoke)  →  run impl in the sandbox isolate  →  CheckOutput (pre-propagation)
//
// Every step fails closed. The implementation is never invoked on unavailable/ malformed input, its
// result never propagates unless it conforms to the output contract, and the tool only ever runs inside
// an isolate that holds no ambient credentials. This is the seam the transformed program calls back
// into (via the host) whenever its graph reaches a repo-tool node — the executor runs the built copy,
// but a node that must run *untrusted repo code* routes that code here so it lands in the isolate, not
// the host process.
package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/sandbox"
	"github.com/heros-foreal/agentd/internal/skillgate"
)

// Runner executes repo-tool skill nodes. It owns the contract gate and the sandbox, so a caller cannot
// accidentally skip either — the only way to run a skill is through RunSkill, which does all three
// steps in order.
type Runner struct {
	gate *skillgate.Gate
	sb   *sandbox.Sandbox
}

// New builds a Runner. The gate is constructed with a sandbox-backed Binder (NewSandboxBinder) so
// "tool availability" means the impl handle can actually be bound in THIS sandbox — not merely that its
// name is well-formed. That is the upgrade over the plain handle-format default: on a host that cannot
// isolate, a repo tool is unavailable and the node fails closed rather than running unsandboxed.
func New(resolver skillgate.Resolver, sb *sandbox.Sandbox) *Runner {
	return &Runner{
		gate: skillgate.New(resolver, NewSandboxBinder(sb)),
		sb:   sb,
	}
}

// SkillRequest is one repo-tool node invocation.
type SkillRequest struct {
	NodeID    string
	RunID     string
	VersionID string // the skill version to run
	Args      any    // the argument object (validated against the input contract before the tool runs)
	// WorkingSet is the set of host paths the tool may read (staged read-only into the isolate). Nothing
	// outside it is visible.
	WorkingSet []string
	// Argv is how the tool is invoked inside the isolate. The node knows this; interpreter choice is a
	// deployment fact, not P3's concern. The validated Args are delivered to the tool on stdin as JSON.
	Argv []string
	// Egress is the tool's allowlist (default-deny; empty means no egress). Bounds staged for §4 broker.
	Egress sandbox.EgressPolicy
	// Bounds overrides the resource bounds; zero fields fall back to sandbox.DefaultBounds.
	Bounds sandbox.ResourceBounds
}

// SkillResult is the validated, ready-to-propagate output plus the isolate outcome for the record.
type SkillResult struct {
	Output   any
	Isolate  *sandbox.Result
	ExitCode int
}

// ErrToolOutputNotJSON: the tool's stdout was not JSON, so it cannot be validated against the output
// contract. Fails closed — an unparseable result must not propagate.
var ErrToolOutputNotJSON = errors.New("nodeexec: tool output is not valid JSON")

// RunSkill runs one repo-tool node end to end, failing closed at each gate.
func (r *Runner) RunSkill(ctx context.Context, req SkillRequest) (*SkillResult, error) {
	// 1. Pre-execution: availability (resolve + bindable-in-sandbox) + argument-schema conformance.
	//    On any failure the implementation is never invoked (spec 2.3 / 2.5).
	entry, err := r.gate.CheckInput(ctx, req.VersionID, req.Args)
	if err != nil {
		return nil, err // *skillgate.ContractError, already fail-closed and typed
	}

	// 2. Run the impl inside a per-node isolate. The validated args go in on stdin as JSON; the tool
	//    holds no credential and has no host access (§3). If the isolate cannot be created with the
	//    required restrictions, sandbox.Run fails closed — no host fallback.
	argsJSON, err := json.Marshal(req.Args)
	if err != nil {
		return nil, fmt.Errorf("nodeexec: marshal args: %w", err)
	}
	spec := sandbox.Spec{
		NodeID: req.NodeID, RunID: req.RunID,
		WorkingSet:              req.WorkingSet,
		Bounds:                  req.Bounds,
		Egress:                  req.Egress,
		RequireNetworkIsolation: true, // untrusted repo tool code always requires both
		RequireFilesystemScope:  true,
	}
	iso, err := r.sb.Run(ctx, spec, sandbox.Tool{Argv: req.Argv, Stdin: argsJSON})
	if err != nil {
		// Includes ErrIsolateUnavailable (fail-closed isolation) and ErrResourceBreach (bound breach).
		return &SkillResult{Isolate: iso}, fmt.Errorf("nodeexec: skill %q isolate run: %w", entry.Name, err)
	}

	// 3. Pre-propagation: the result must be JSON and conform to the output contract, or it is discarded
	//    and the node fails closed (spec 2.4 / 2.5). The untrusted code never gets to inject an
	//    unvalidated value into the graph.
	var out any
	if err := json.Unmarshal(iso.Stdout, &out); err != nil {
		return &SkillResult{Isolate: iso}, fmt.Errorf("%w: skill %q: %v", ErrToolOutputNotJSON, entry.Name, err)
	}
	if err := r.gate.CheckOutput(entry, out); err != nil {
		return &SkillResult{Isolate: iso}, err // *skillgate.ContractError
	}
	return &SkillResult{Output: out, Isolate: iso, ExitCode: iso.ExitCode}, nil
}

// SandboxBinder ties skill "availability" to real isolation capability: a `repo:` impl handle is
// bindable only if the sandbox can actually create an isolate with the required restrictions on this
// host. `builtin:` handles run host-side (trusted) and are always bindable. An unknown scheme is never
// bindable. This is the sandbox-backed replacement for the plain handle-format default — on a host that
// cannot isolate, repo tools become unavailable and their nodes fail closed instead of running
// unsandboxed (which is exactly the fail-closed posture §3 requires).
type SandboxBinder struct {
	sb *sandbox.Sandbox
}

func NewSandboxBinder(sb *sandbox.Sandbox) *SandboxBinder { return &SandboxBinder{sb: sb} }

func (b *SandboxBinder) CanBind(implHandle string) error {
	switch {
	case hasPrefix(implHandle, "builtin:"):
		return nil // trusted host-side built-in; not sandboxed
	case hasPrefix(implHandle, "repo:"):
		if b.sb == nil || !b.sb.CanIsolate() {
			return fmt.Errorf("repo tool %q is not bindable: this host cannot create an isolate with the "+
				"required restrictions (no ambient creds / deny egress / scoped FS / resource bounds)", implHandle)
		}
		return nil
	default:
		return fmt.Errorf("impl handle %q names no bindable scheme (want builtin:<name> or repo:<path>)", implHandle)
	}
}

func hasPrefix(s, p string) bool { return len(s) > len(p) && s[:len(p)] == p }
