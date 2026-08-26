package launch

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/approval"

	"github.com/heros-foreal/agentd/internal/hostdiscovery"
	"github.com/heros-foreal/agentd/internal/improvementrun"
	"github.com/heros-foreal/agentd/internal/linkingest"
)

// improvementwiring.go holds the two P35 decisions a deployment has to make, kept out of
// `capabilities.go` so the mounting block reads as wiring rather than as policy.

// improvementPlanBounds is the per-plan run allowance (`improvementrun.PlanBounds`).
//
// # Why these numbers, and why they are DATA rather than a computation
//
// A run's spend budget is the only thing between one typed sentence and an invoice. It is a commercial
// decision per plan, so it is written down per plan — deriving it from something else (seats, usage,
// a percentage of a tier) would make a customer's ceiling move for reasons unrelated to what they
// bought, which is a surprise that arrives on a bill.
//
// The values are deliberately small. A run's job at this stage is to find ONE change worth reviewing,
// not to sweep a space, and a cap of eight candidates at $4 is roughly four assessments' worth of
// provider spend — enough for a real answer, small enough that a mistake is cheap. Raising them is a
// commercial decision somebody makes, in this file, where the reason they are small is written next to
// them.
//
// 🚫 A plan absent from this map gets NO allowance, and `Translate` refuses it by name with the plan
// action. "This organization has not bought improvement runs" and "this organization has a tiny
// budget" are different facts, and giving the first a small default spends money for a customer who
// agreed to none.
func improvementPlanBounds() map[string]improvementrun.PlanBounds {
	return map[string]improvementrun.PlanBounds{
		"team":       {MaxCandidates: 8, MaxSpendUSD: 4.00},
		"enterprise": {MaxCandidates: 24, MaxSpendUSD: 12.00},
	}
}

// improvementSubject resolves the workflow and source revision a tenant's run is pinned to.
//
// 🔴 It reads the DISCOVERED GRAPH's revision rather than asking a forge for HEAD, and the difference
// is FR17. The graph is what the platform actually holds and what a candidate would be compiled
// against; HEAD is what the repository is at right now, which may be a revision this platform has never
// read. Pinning to a revision we do not hold would make every re-measurement compare two different
// trees and blame the change.
//
// A tenant with more than one workflow gets a refusal rather than a guess: `Translate`'s
// `RefusedNoSubject` names the next action, which is to pick one. 🚫 Defaulting to the first workflow
// we know about would spend money changing a repository nobody asked about — the same refusal
// `web/console`'s assess page makes for the same reason.
// 🔴 It reads the workflow list from `linkingest` — the SAME source the console's own subject picker
// reads — rather than adding a lister to the graph store. `careful-api-creation`'s alternative taken
// rather than dismissed: a second enumeration would eventually disagree with the picker, and the
// disagreement would show as a run against a workflow the console does not list.
func improvementSubject(
	ctx context.Context, index linkingest.WorkflowIRStore, graphs *hostdiscovery.PGGraphStore, tenantID string,
) (string, string, error) {
	if index == nil || graphs == nil {
		return "", "", nil
	}
	rows, err := index.ListWorkflows(tenantID)
	if err != nil {
		return "", "", fmt.Errorf("reading this organization's workflows: %w", err)
	}
	if len(rows) != 1 {
		// Zero workflows and several workflows are both "the surface has to say which one", and
		// `Translate` says it. Returning an empty subject is how that reaches the person as a refusal
		// with a next action rather than as a run against a workflow they did not name.
		return "", "", nil
	}
	graph, ok, err := graphs.Latest(ctx, tenantID, rows[0].WorkflowID)
	if err != nil {
		return "", "", fmt.Errorf("reading the discovered graph: %w", err)
	}
	if !ok {
		// 🔴 The workflow with NO revision, deliberately. `Translate` then refuses with
		// `no_source_revision` and names the next action — push source — rather than pinning the run to
		// the linked run's revision, which is a different tree from the one a candidate would compile
		// against (this is `proposalgen.StateRevisionMismatch`'s defect, prevented one layer earlier).
		return rows[0].WorkflowID, "", nil
	}
	return rows[0].WorkflowID, graph.SourceRevision, nil
}

// approvalStore adapts `internal/approval`'s package-level functions — which take a `*sql.DB` — to the
// narrow interface `improvementrun` declares.
//
// 🔴 The adapter lives HERE rather than in `improvementrun`, because the decision about which database
// this is already lives in this package, and putting it there would make the run package import
// `database/sql` for one handle. It adds NO logic: `approval.Approve`'s empty-actor refusal is the one
// that decides, and a second check here would be a second place it could be relaxed.
type approvalStore struct{ db *sql.DB }

func (a approvalStore) Submit(tenantID string, layer approval.Layer, title, rationale, diff string) (string, error) {
	p, err := approval.Submit(a.db, tenantID, layer, title, rationale, diff)
	if err != nil {
		return "", err
	}
	return p.ID, nil
}

func (a approvalStore) Approve(id, approvedBy string) error {
	return approval.Approve(a.db, id, approvedBy)
}

func (a approvalStore) Decline(id string) error { return approval.Reject(a.db, id) }

var _ improvementrun.ApprovalStore = approvalStore{}

// improvementBinding answers "what is this proposal's subject NOW?" for the FR13 void check.
//
// # 🔴 One half is re-resolved and the other is echoed, and which is which matters
//
//	source_revision   RE-RESOLVED from the discovered graph. It is the customer's tree, it moves on its
//	                  own, and it is the half FR13 exists for: an approval that survived a revision
//	                  change would be an approval for a diff nobody saw.
//	config_hash       ECHOED from the proposal. It identifies the CANDIDATE, and a proposal id is
//	                  content-derived from the candidate (`proposalgen.proposalID` hashes the candidate's
//	                  canonical spec), so the configuration under a given proposal id structurally
//	                  cannot change. Re-reading it would compare a value to itself and would read as a
//	                  check that is doing something.
//
// ⚠️ That echo is sound only while proposal ids stay content-derived. If a future phase mints them any
// other way, this becomes a check that always passes — so it is stated here rather than left as an
// implementation detail, and `improvementrun.Binding` keeps both halves so the other one is available
// the day it starts moving.
func improvementBinding(
	index linkingest.WorkflowIRStore, graphs *hostdiscovery.PGGraphStore,
) func(context.Context, improvementrun.Plan, improvementrun.VerifiedProposal) (improvementrun.Binding, error) {
	return func(ctx context.Context, p improvementrun.Plan, proposal improvementrun.VerifiedProposal) (improvementrun.Binding, error) {
		_, rev, err := improvementSubject(ctx, index, graphs, p.TenantID)
		if err != nil {
			return improvementrun.Binding{}, err
		}
		return improvementrun.Binding{ConfigHash: proposal.ConfigHash, SourceRevision: rev}, nil
	}
}

// 🔴 The COMPILE-TIME proof that the shipped operator switch fits the run's brake with no adapter.
//
// It lives here, in the wiring, rather than in `internal/improvementrun`, and that placement is the
// same decision `optimizer.MergeAdmission` makes: *the optimizer must remain buildable and testable
// with no operator console at all*, and a run has the same requirement. Declaring the interface there
// and asserting the fit here is what lets both be true — the run never imports `adminops`, and nobody
// can change `KillSwitchService.HaltsMerge`'s signature without this line going red.
var _ improvementrun.OperatorBrake = (*adminops.KillSwitchService)(nil)

// reconcileInterval is how often the improvement run's reconciliation pass runs.
//
// # Why it runs on a timer at all, when nothing may be broken
//
// design D6 in one line: *a repair path that only runs after failures is a path that is never exercised
// until it is needed.* A pass that fired on an error flag is code that runs for the first time during an
// incident, on data nobody has seen. Running it unconditionally means the ordinary result — nothing to
// do — is the result it produces thousands of times before it produces the other one.
//
// It also makes the health signal mean something: `improvement_run.reconcile_last_success_ms` going
// STALE is the alert, and staleness is only a signal for a job that runs whether or not there is work.
//
// # Why five minutes
//
// It bounds how long a delivery interrupted between apply and pull-request creation stays incomplete,
// and the customer-visible cost of that window is a pull request arriving late rather than anything
// being wrong. Shorter would add database reads for a case that is rare; longer would leave a person
// looking at "the pull request has not been opened yet" past the point where they conclude it never
// will be. 🔴 A constant rather than configuration, for `AlertWithdrawalRateAbove`'s reason: an
// interval an operator can lengthen is one that gets lengthened during the incident it was meant to
// shorten.
const reconcileInterval = 5 * time.Minute

// startImprovementReconciler runs the reconciliation pass on a timer (task 9.4).
//
// Three properties, matching `startPricingPreflight`'s and design D6's:
//
//  1. BACKGROUND, never blocking the boot. A platform must come up with a forge unreachable.
//  2. IT GATES NOTHING. A pass that cannot run leaves deliveries incomplete for one more cycle, which
//     is the state they were already in; it must never take a deployment down.
//  3. RUN EVERY CYCLE, not on a flag. See `reconcileInterval`.
//
// 🚫 It is deliberately NOT started when the service cannot deliver. A pass that ran every five minutes
// to discover it has no deliverer would write a fresh last-success timestamp over a pass that examined
// nothing — which makes the staleness signal lie in the one direction that matters.
func startImprovementReconciler(svc *improvementrun.Service, tenants func() []string) {
	if svc == nil || tenants == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(reconcileInterval)
		defer ticker.Stop()
		for range ticker.C {
			for _, tenant := range tenants() {
				ctx, cancel := context.WithTimeout(context.Background(), reconcileInterval/2)
				res, err := svc.Reconcile(ctx, tenant)
				cancel()
				switch {
				case err != nil:
					// Loud. A pass that cannot read the ledger is the one case where silence would be
					// indistinguishable from "nothing to do", which is its ordinary result.
					log.Printf("improvement-run reconciliation (tenant %s): FAILED — %v", tenant, err)
				case res.Resolved > 0 || res.Failed > 0:
					log.Printf("improvement-run reconciliation (tenant %s): examined %d, resolved %d, "+
						"could not complete %d", tenant, res.Examined, res.Resolved, res.Failed)
					for _, d := range res.Details {
						log.Printf("  reconcile: %s", d)
					}
				}
				// 🚫 A pass that found nothing logs NOTHING. It is the normal outcome and it happens
				// every five minutes forever; logging it would bury the two lines above under it. The
				// health endpoint carries the timestamp, which is what a monitor reads.
			}
		}
	}()
}
