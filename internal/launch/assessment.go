package launch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/hostdiscovery"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

// assessment.go wires P33 into the hosted deployment: what an assessment reads, what its evidence
// references are checked against, and what identifies the configuration that produced it.
//
// # Why the adapters live here rather than in `internal/assessment`
//
// The assessment package must not know about the pattern graph, the eval board or the scorecard —
// design D5 says a finding points INTO an existing surface, and a package that imported all three would
// be a package that could compute over them. It declares two narrow interfaces and this file supplies
// them from the wiring that already holds those sources.

// assessmentSource resolves a workflow to the material the structural pass reads.
//
// Two steps, and the split matters. The REVISION comes from the stored graph, because that is what the
// console is looking at and an assessment of a different revision than the graph beside it would be two
// answers to "what is this repository doing". The IR and the discovery report are then RE-DERIVED from
// the retained snapshot, for `hostdiscovery.Runner.IR`'s reason, quoted because it is the reason this
// costs a re-parse: *"The IR carries prompt text, I/O-contract schemas and in-scope symbol sets.
// Storing it would undo the line this package draws — keep the CONCLUSION, not the evidence."*
type assessmentSource struct {
	graphs  hostdiscovery.GraphStore
	discove *hostdiscovery.Runner
}

// Analyse implements assessment.SourceReader.
func (s assessmentSource) Analyse(ctx context.Context, tenantID, workflowID string) (string, *discovery.IR, discovery.DiscoveryReport, error) {
	g, ok, err := s.graphs.Latest(ctx, tenantID, workflowID)
	if err != nil {
		return "", nil, discovery.DiscoveryReport{}, fmt.Errorf("assessment source: reading the stored graph: %w", err)
	}
	if !ok {
		// 🔴 ErrNoSource rather than an empty IR. "We have never seen your code" and "we saw your code
		// and could determine nothing" are different sentences, and the second costs a customer an
		// afternoon if we say it when the first is true.
		return "", nil, discovery.DiscoveryReport{}, assessment.ErrNoSource
	}
	ir, rep, err := s.discove.Analyse(ctx, sourceingest.Ref{
		TenantID: tenantID, WorkflowID: workflowID, SourceRevision: g.SourceRevision,
	})
	if err != nil {
		if errors.Is(err, sourceingest.ErrNoSource) {
			// The graph outlived its snapshot — a real state after P32's 72-hour retention sweep, and
			// it is `ErrNoSource` rather than an error page: the customer's next action is to push or
			// re-connect, not to file a bug.
			return "", nil, discovery.DiscoveryReport{}, assessment.ErrNoSource
		}
		return "", nil, discovery.DiscoveryReport{}, err
	}
	return g.SourceRevision, ir, rep, nil
}

// assessmentEvidence checks that a finding's evidence reference points at something that exists
// (P33 task 2.4).
//
// 🔴 It asks the SAME sources the routes serve, not a copy. A resolver that checked a different store
// would answer "yes" for a reference the reader's own request will 404 on, which is worse than not
// checking: it converts a dead link into a dead link somebody has certified.
type assessmentEvidence struct {
	graphs    hostdiscovery.GraphStore
	board     api.BoardSource
	scorecard api.ScorecardSource
}

// Resolves implements assessment.EvidenceResolver.
func (e assessmentEvidence) Resolves(ctx context.Context, tenantID string, ref assessment.EvidenceRef) (bool, error) {
	switch ref.Surface {
	case assessment.SurfaceGraph:
		_, ok, err := e.graphs.Latest(ctx, tenantID, ref.Locator)
		return ok, err
	case assessment.SurfaceBoard:
		if e.board == nil {
			// 🚫 NOT an error, and NOT true. A deployment that does not mount the board cannot carry a
			// finding whose evidence is a board, and saying so at the WRITE is exactly what task 2.4
			// asks for — the alternative is persisting a reference that will 503 for every reader.
			return false, nil
		}
		_, ok := e.board.Board(tenantID, ref.Locator, "")
		return ok, nil
	case assessment.SurfaceScorecard:
		if e.scorecard == nil {
			return false, nil
		}
		_, ok := e.scorecard.Scorecard(tenantID, ref.Locator)
		return ok, nil
	default:
		return false, fmt.Errorf("assessment evidence: %q is not a surface this deployment serves", ref.Surface)
	}
}

// StructuralOnlyConfigHash identifies an assessment produced with NO agent configuration at all.
//
// # Why a named constant and not an empty string
//
// FR16 requires an assessment to record the configuration that produced it, and `Assessment.Validate`
// refuses an empty hash. A deployment in rollout stage 1 (PRD §12 — "structural only ... no inference,
// no eval") genuinely has no agent configuration, and that is not a missing value: it is the honest
// answer, and it is a DIFFERENT answer from every hash a published definition produces.
//
// It also does the right thing to the pin. The day an operator activates an agent definition, the hash
// changes, `FindPin` misses, and the workflow is re-assessed — which is correct, because the report can
// now say more than it could yesterday.
const StructuralOnlyConfigHash = "structural-only"

// agentConfigHash reads the ACTIVE agent definition's hash at run time.
//
// 🔴 Read per run, never captured at start-up. A hash captured once would keep naming a configuration
// an operator has since replaced, and design D7's whole point is that a finding stays attributable to
// the configuration that actually made it.
func agentConfigHash(versions interface {
	Active(ctx context.Context) (herosagent.Version, bool, error)
}) assessment.ConfigHasher {
	return func(ctx context.Context) (string, error) {
		if versions == nil {
			return StructuralOnlyConfigHash, nil
		}
		v, ok, err := versions.Active(ctx)
		if err != nil {
			return "", err
		}
		if !ok {
			return StructuralOnlyConfigHash, nil
		}
		return v.ConfigHash, nil
	}
}

// newAssessmentID mints an assessment id. Sixteen random bytes, `crypto/rand`, prefixed so a value seen
// in a log is identifiable without a lookup.
func newAssessmentID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is a broken host, not a condition to route around. Every other id in
		// this package takes the same position.
		panic("launch: crypto/rand unavailable: " + err.Error())
	}
	return "as_" + hex.EncodeToString(b[:])
}
