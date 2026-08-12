package hostedboard

import (
	"github.com/heros-foreal/agentd/internal/evalboard"
	"github.com/heros-foreal/agentd/internal/linkingest"
)

// evalset.go assembles the eval-set surface from a tenant's LINKED runs (P30 task 1.12).
//
// # 🔴 What this deployment can list, and why it is not a gap
//
// `internal/runlink/allowlist.go` permits `eval.case_count` and says so in terms: "a count, never the
// cases. The eval set itself never crosses, and there is no field here it could occupy." That is a
// boundary somebody argued for, and this package does not widen it — so what comes back is the
// denominator and the quality counts, in the `counts_only` state, with the rule named on the page.
//
// The alternative shape — an empty table — was rejected for the reason every empty state in this
// product is: a workflow whose cases stay on the customer's machine and a workflow with no cases at all
// would render identically, and one of those is a healthy deployment while the other is a broken eval.
//
// The denominator itself is the NEWEST linked run's, matching the board. Averaging case counts across
// runs would produce a number no run has, and taking the largest would report a set somebody has since
// shrunk.

// BuildEvalSet assembles the eval-set surface for one workflow from its linked runs.
func BuildEvalSet(workflowID string, runs []linkingest.LinkedRun) evalboard.EvalSetView {
	in := evalboard.EvalSetInput{WorkflowID: workflowID}
	// CasesAvailable stays FALSE on this substrate, always, and it is a statement about the wire rather
	// than about this tenant. See the header.
	in.CasesAvailable = false

	var newest *linkingest.LinkedRun
	for i := range runs {
		if !runs[i].EvalEvidencePresent() {
			// A run linked before migration 0023 carries no case count, and that is NOT "zero cases".
			// Reading it as a denominator would report an eval set nobody measured.
			continue
		}
		if newest == nil || runs[i].LinkedAt.After(newest.LinkedAt) {
			newest = &runs[i]
		}
	}
	if newest != nil {
		in.Linked = true
		in.NCases = newest.Eval.CaseCount
	}
	return evalboard.BuildEvalSet(in)
}

// EvalSet returns the eval-set surface for one tenant's workflow.
//
// ok=false means this tenant has no runs for this workflow — the same limitation Board carries, and it
// is inherited from the api.EvalSetSource signature rather than introduced here: it has nowhere to put
// an error, so a database outage renders as "no such workflow". Stated rather than hidden.
func (s *Source) EvalSet(tenantID, workflowID string) (evalboard.EvalSetView, bool) {
	runs, err := s.runs.ForWorkflow(tenantID, workflowID)
	if err != nil || len(runs) == 0 {
		return evalboard.EvalSetView{}, false
	}
	return BuildEvalSet(workflowID, runs), true
}
