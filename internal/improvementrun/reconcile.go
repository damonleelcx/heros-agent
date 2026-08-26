package improvementrun

import (
	"context"
	"fmt"
)

// reconcile.go is FR20/§7.4 and design D6: an interrupted run resolves itself from the append-only
// record, **with no human step**, on a pass that runs **every cycle rather than only after a failure**.
//
// # 🔴 Why "every cycle" is the requirement and not a scheduling detail
//
// design D6 says it in one line: *a repair path that only runs after failures is a path that is never
// exercised until it is needed.* A reconciliation that fires on an error flag is code that runs for the
// first time during an incident, on data nobody has seen, written by somebody who has moved on. Running
// it unconditionally means the ordinary result — nothing to do — is the result it produces thousands of
// times before it produces the other one.
//
// It also changes what the health signal can say. A pass that only runs after failures cannot answer
// "is reconciliation working?", because silence is its normal state. A pass that runs every cycle
// publishes a last-success timestamp, and `Health.ReconcileLastSuccessMS` going stale is a signal in
// itself — 🔴 which is the whole reason zero deletions is not the metric.
//
// # The two interrupted states, and where they come from
//
// Both are consequences of the write-ahead ordering in `Service.Approve` and `Service.Deliver`:
//
//	change_applied with no delivery       the run died between applying and delivering. The change is
//	                                      approved and re-measured; the pull request was never opened.
//	delivery_pending_forge                the branch was pushed and the forge did not answer
//	                                      (decisions.md D-35.5). The retry bound was reached inside the
//	                                      run, and completion was handed here on purpose.
//
// 🚫 A CANCELLED run is not on this list, and that is D-35.6: the push is the last step and the
// cancellation check sits immediately before it, so a cancelled run never pushed. There is nothing to
// clean up, which is exactly why no branch-deletion capability was added.

// ReconcileResult is what one pass did. 🔴 Zero resolutions is the NORMAL outcome and is reported as a
// success, not as nothing having happened.
type ReconcileResult struct {
	// Examined is how many unresolved entries the pass looked at.
	Examined int `json:"examined"`
	// Resolved is how many deliveries it completed.
	Resolved int `json:"resolved"`
	// Failed is how many it could not complete this cycle. 🔴 NOT an error: a forge that is still down
	// is still down, and the next pass tries again. A pass that errored on this would stop reconciling
	// every OTHER run because one repository's forge was unavailable.
	Failed int `json:"failed"`
	// Details name what happened, one line per entry it acted on.
	Details []string `json:"details,omitempty"`
	AtMS    int64    `json:"at_ms"`
}

// Reconcile runs one pass over a tenant's unresolved deliveries.
//
// It is a NECESSARY PATH: a caller schedules it every cycle regardless of whether anything failed. See
// the file header for why that is the requirement rather than a preference.
//
// 🔴 It performs no human-visible act other than completing a delivery that was already approved,
// re-measured and authorized. It cannot approve, cannot withdraw, and cannot deliver anything a person
// did not already say yes to — every one of those decisions is read back from the ledger, and an entry
// the ledger does not carry is an act this pass will not perform.
func (s *Service) Reconcile(ctx context.Context, tenantID string) (ReconcileResult, error) {
	if err := s.checkDeliver(); err != nil {
		return ReconcileResult{}, err
	}
	out := ReconcileResult{AtMS: s.nowMS()}

	unresolved, err := s.Ledger.Unresolved(ctx, tenantID)
	if err != nil {
		// 🚫 The pass FAILS rather than reporting success over an unreadable ledger. Recording a
		// last-success timestamp here would make the staleness signal lie in the one direction that
		// matters: an operator would see a fresh timestamp over a pass that examined nothing.
		return out, fmt.Errorf("improvementrun: the reconciliation pass could not read the ledger, so "+
			"it did not run: %w", err)
	}
	out.Examined = len(unresolved)

	for _, e := range unresolved {
		run, found, rerr := s.Run(ctx, tenantID, e.RunID)
		if rerr != nil || !found {
			out.Failed++
			out.Details = append(out.Details, fmt.Sprintf("%s: the run could not be read back", e.RunID))
			continue
		}
		// 🔴 The proposal has to be readable, because delivery needs the diff. A deployment with no
		// proposal reader cannot complete a delivery from the record — and saying so is better than
		// completing it with an empty diff, which `forgedelivery.ErrNoDiff` would refuse anyway but
		// only after opening nothing and reporting a confusing cause.
		if _, ok := run.proposal(e.ProposalID); !ok {
			out.Failed++
			out.Details = append(out.Details, fmt.Sprintf("%s: proposal %s is not readable, so its "+
				"delivery cannot be completed from the record", e.RunID, e.ProposalID))
			continue
		}
		res, derr := s.Deliver(ctx, &run, e.ProposalID)
		switch {
		case derr != nil:
			// A forge that is still down is still down. Counted, named, and retried next cycle.
			out.Failed++
			out.Details = append(out.Details, fmt.Sprintf("%s/%s: %v", e.RunID, e.ProposalID, derr))
		case res.Withheld != nil:
			// A withheld delivery is RESOLVED for this pass's purposes — the condition is reported and
			// the customer has a next action. Retrying it every cycle forever would be a loop with no
			// exit that nobody asked for.
			out.Resolved++
			out.Details = append(out.Details, fmt.Sprintf("%s/%s: withheld (%s)",
				e.RunID, e.ProposalID, res.Withheld.Kind))
		default:
			out.Resolved++
			out.Details = append(out.Details, fmt.Sprintf("%s/%s: delivered at %s",
				e.RunID, e.ProposalID, res.PullRequestURL))
		}
	}

	if s.Metrics != nil {
		// 🔴 Recorded on EVERY successful pass, including the ones that resolved nothing. That is what
		// makes `ReconcileLastSuccessMS` a staleness signal rather than a record of the last incident.
		s.Metrics.ReconcilePassed(out.AtMS, out.Resolved)
	}
	return out, nil
}
