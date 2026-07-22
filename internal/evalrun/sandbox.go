package evalrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/sandbox"
)

// sandbox.go is DevOps task 6.5: adversarial and injection cases execute ONLY in the P3 sandbox,
// with no ambient credentials.
//
// The reasoning is not abstract. An injection probe is, by construction, an attempt to make the
// workflow do something its author did not intend. Running one on a host that still holds provider
// credentials is not a robustness test — it is a live attack against production secrets with the
// safety catch off, executed by us, on purpose, at scale. So the routing decision is made from the
// case's taxonomy slot before anything runs, and the refusal is FAIL-CLOSED: a host that cannot
// isolate does not get to run the case "just this once".

// ErrSandboxRequired is returned when an adversarial case is asked to run outside the sandbox.
var ErrSandboxRequired = errors.New("evalrun: this case may only execute in the P3 sandbox with no ambient credentials")

// ErrSandboxUnavailable is returned when the host cannot provide an isolate that scrubs credentials.
var ErrSandboxUnavailable = errors.New("evalrun: no credential-scrubbing sandbox is available")

// SandboxRouter decides where a case executes and enforces that decision.
type SandboxRouter struct {
	// Sandbox is the P3 sandbox. Nil means no isolation is available, which makes every adversarial
	// case a refusal rather than a host-side execution.
	Sandbox *sandbox.Sandbox
	// Bounds and Egress are applied to every sandboxed eval case. Egress defaults to DENY-ALL: an
	// eval case has no legitimate reason to reach the network, and an injection probe that can
	// reach the network is an exfiltration channel.
	Bounds sandbox.ResourceBounds
	Egress sandbox.EgressPolicy
}

// Placement is where one case will execute, and why.
type Placement struct {
	CaseID string `json:"case_id"`
	// Sandboxed is the decision.
	Sandboxed bool `json:"sandboxed"`
	// Reason states which rule fired, so a reviewer can check the routing without re-deriving it.
	Reason string `json:"reason"`
}

// Route decides placement for a case. It is a pure function of the case's taxonomy slot and origin,
// so the same case always lands the same way.
func Route(c evalharness.Case) Placement {
	if RequiresSandbox(c) {
		reason := fmt.Sprintf("edge_case=%q requires isolation", c.EdgeCase)
		if c.EdgeCase == evalharness.EdgeCaseNone {
			reason = fmt.Sprintf("origin=%q requires isolation", c.Origin)
		}
		return Placement{CaseID: c.CaseID, Sandboxed: true, Reason: reason}
	}
	return Placement{CaseID: c.CaseID, Sandboxed: false, Reason: "ordinary case; no isolation required"}
}

// Spec builds the sandbox spec for one eval case. RequireNetworkIsolation and RequireFilesystemScope
// are both demanded: sandbox.Run refuses when the host cannot meet a required capability, which is
// what turns "we intended to isolate" into "it did not run unless it was isolated".
func (r *SandboxRouter) Spec(nodeID, runID string, workingSet []string) sandbox.Spec {
	bounds := r.Bounds
	if bounds == (sandbox.ResourceBounds{}) {
		bounds = sandbox.DefaultBounds()
	}
	return sandbox.Spec{
		NodeID:                  nodeID,
		RunID:                   runID,
		WorkingSet:              workingSet,
		Bounds:                  bounds,
		Egress:                  r.Egress, // zero value = allow nothing
		RequireNetworkIsolation: true,
		RequireFilesystemScope:  true,
	}
}

// Run executes an adversarial case's tool inside the sandbox, refusing every path that would let it
// run with ambient credentials.
//
// Three refusals, all fail-closed:
//   - a case that does not require isolation is not routed here at all (a caller that does anyway
//     gets an error rather than a silent host-side run);
//   - no sandbox configured is a refusal, not a downgrade;
//   - a host that cannot scrub credentials is a refusal — sandbox.Run enforces this itself, and the
//     error is surfaced rather than swallowed.
func (r *SandboxRouter) Run(ctx context.Context, c evalharness.Case, runID string, tool sandbox.Tool, workingSet []string) (*sandbox.Result, error) {
	if !RequiresSandbox(c) {
		return nil, fmt.Errorf("evalrun: case %q does not require the sandbox; route it normally", c.CaseID)
	}
	if r == nil || r.Sandbox == nil {
		return nil, fmt.Errorf("%w: case %q is adversarial and no sandbox is configured", ErrSandboxRequired, c.CaseID)
	}
	if !r.Sandbox.CanIsolate() {
		return nil, fmt.Errorf("%w: case %q refused rather than executed with ambient credentials", ErrSandboxUnavailable, c.CaseID)
	}
	res, err := r.Sandbox.Run(ctx, r.Spec(c.CaseID, runID, workingSet), tool)
	if err != nil {
		return nil, fmt.Errorf("evalrun: sandboxed case %q: %w", c.CaseID, err)
	}
	return res, nil
}

// AuditPlacements returns the placement decision for every case in a set, so a reviewer can check
// in one glance that no adversarial case is scheduled to run on the host.
func AuditPlacements(cases []evalharness.Case) []Placement {
	out := make([]Placement, 0, len(cases))
	for _, c := range cases {
		out = append(out, Route(c))
	}
	return out
}

// UnsandboxedAdversarial returns the ids of adversarial cases NOT routed to the sandbox. It must
// always be empty; it is exported so the assertion can live in a test and in a pre-flight check
// rather than only in a comment.
func UnsandboxedAdversarial(placements []Placement, cases []evalharness.Case) []string {
	byID := map[string]evalharness.Case{}
	for _, c := range cases {
		byID[c.CaseID] = c
	}
	var bad []string
	for _, p := range placements {
		c, ok := byID[p.CaseID]
		if !ok {
			continue
		}
		if RequiresSandbox(c) && !p.Sandboxed {
			bad = append(bad, p.CaseID)
		}
	}
	return bad
}
