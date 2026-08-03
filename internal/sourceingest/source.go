// Package sourceingest gives the platform something it has never had: source to run discovery on.
//
// # Why this package exists
//
// `discovery.Run` takes `Options.Repo` — a path to a checked-out tree — and every caller until now has
// been a developer's own machine or a demo binary pointed at a local clone. The platform had no path to
// pass. That single missing input is why six console surfaces were mounted nil: the eval board, the
// scorecard, proposals, the optimizer, the run monitor and the graph editor all render evidence that is
// produced by running something over source, and the platform ran nothing over nothing.
//
// The consequence people actually saw was subtler and worse than an empty page. `heros link --with-ir`
// let a customer OPT IN to sending their workflow's shape, so the pattern graph could finally be drawn —
// but drawn without labels, because (as internal/launch/workflowgraph.go says at length) the classifier
// reads prompt text and tool names and neither crosses the boundary. The graph was real and unlabelled,
// and the honest comment explaining why was itself the ceiling.
//
// # What changes, and what deliberately does not
//
// When the platform holds source, it runs discovery ITSELF. Prompts and tool names are then inputs to a
// computation happening on the platform's own side of the boundary — they are not transmitted, not
// projected onto a wire allowlist, and not stored as text by anything in this package's caller. The
// classifier gets its inputs; the graph gets its labels; the wire contract in internal/runlink is
// untouched, byte for byte.
//
// 🔴 That is a real widening of what the platform HOLDS, and it is not disguised as a wiring change. A
// customer who pushes source has handed over their source. This package therefore makes the handover
// explicit, customer-initiated, and revision-scoped — never a standing credential the platform uses to
// help itself. See BundleSource for the mechanism and its retention rule.
//
// # Why an interface rather than one implementation
//
// Bundle-push and git-remote-clone are the same operation to every caller — "give me a tree at this
// revision" — and differ only in who initiates and what is stored. Source is that operation. BundleSource
// implements it today. A GitSource that clones from a registered remote can implement it later without
// touching the discovery orchestrator, and the choice between them stays one constructor call in
// internal/launch rather than a branch threaded through the pipeline.
package sourceingest

import (
	"context"
	"errors"
	"fmt"
)

// ErrNoSource reports that this tenant has pushed no source for this workflow at this revision. It is a
// FIRST-CLASS state, not a failure: the overwhelmingly common case is a customer who has not opted in,
// and a caller must be able to tell that apart from a store it could not read. Every other error from a
// Source means something went wrong.
var ErrNoSource = errors.New("sourceingest: no source has been pushed for this workflow revision")

// Ref identifies one source snapshot. All three fields are required — a snapshot with no revision is a
// tree the platform cannot say anything reproducible about, and "the latest source" is a moving target
// that would make a stored graph a picture of nothing in particular.
type Ref struct {
	TenantID       string
	WorkflowID     string
	SourceRevision string
}

// Validate rejects a partial Ref. Called at every entry point rather than trusted from the caller: a Ref
// with an empty TenantID would otherwise read as a legitimate lookup against a store keyed by tenant, and
// return another tenant's row.
func (r Ref) Validate() error {
	switch {
	case r.TenantID == "":
		return fmt.Errorf("sourceingest: ref has no tenant_id")
	case r.WorkflowID == "":
		return fmt.Errorf("sourceingest: ref has no workflow_id")
	case r.SourceRevision == "":
		return fmt.Errorf("sourceingest: ref has no source_revision")
	}
	return nil
}

// String renders a Ref for logs and errors. Deliberately includes the revision: an operator reading
// "discovery failed for workflow X" needs to know which snapshot, because the next question is always
// whether re-pushing fixes it.
func (r Ref) String() string {
	return fmt.Sprintf("%s/%s@%s", r.TenantID, r.WorkflowID, r.SourceRevision)
}

// Materialized is a source tree on local disk, ready for discovery.Run, plus the release that removes it.
//
// Release is not optional and not a finalizer. An unreleased tree is a customer's source left on the
// platform's disk after the job that needed it finished, which is the exact failure this package's
// retention rule exists to prevent — so callers `defer m.Release()` and the orchestrator's test asserts
// the directory is gone.
type Materialized struct {
	// Dir is an absolute path to the root of the extracted tree.
	Dir string
	// Release removes the tree. Safe to call more than once; never nil on a successful Materialize.
	Release func()
}

// Source materializes source at a revision into a directory on local disk.
//
// Implementations MUST return ErrNoSource (not a generic error, and not an empty Materialized with a nil
// error) when the snapshot simply is not there. The orchestrator branches on it to distinguish "this
// customer has not opted in" from "this platform is broken", and those two produce different console
// states and different pages for an operator.
type Source interface {
	Materialize(ctx context.Context, ref Ref) (Materialized, error)
}
