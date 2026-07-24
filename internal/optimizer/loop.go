package optimizer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/evalstats"
)

// loop.go is the P6 controller: it drives the analyze → propose → verify → apply loop under the hard
// constraints, with all the loop engineering that keeps an autonomous optimizer from wandering —
// explicit stopping conditions, stall detection, verification-in-the-loop, recovery — and all the
// DevOps guardrails that keep merging safe — the merge-prerequisite gate, write-ahead audit,
// regression/budget halts, disarm-until-re-arm, fail-closed degradation, and the per-workflow lock.
//
// The loop's cardinal rule: it NEVER ends with a half-merged or unverified spec live. Every exit path
// — converged, max_iter, stalled, stopped, halted_regression, halted_budget, a failed merge, a crashed
// verification, a downed ledger — leaves the last-good (currently-merged) Variant Spec live.

// AppliedChange is one merged change the loop made (the design's applied_change row). It carries what a
// rollback needs (the merge commit and the from→to config hashes) and what the UI shows (the motivating
// diagnosis, the verified delta, and the cost/latency impact). RevertedBySeq is set once it is rolled
// back.
type AppliedChange struct {
	RunID          string  `json:"run_id"`
	Idx            int     `json:"idx"`
	FromConfigHash string  `json:"from_config_hash"`
	ToConfigHash   string  `json:"to_config_hash"`
	PRRef          string  `json:"pr_ref"`
	MergeCommit    string  `json:"merge_commit"`
	DiagnosisID    string  `json:"diagnosis_id"`
	Operator       string  `json:"operator"`
	Node           string  `json:"node_id"`
	VerifiedDelta  float64 `json:"verified_delta"`
	CostDelta      float64 `json:"cost_delta"`
	LatencyDelta   float64 `json:"latency_delta"`
	LedgerSeq      int     `json:"ledger_seq"`
	RevertedBySeq  int     `json:"reverted_by_seq,omitempty"`
}

// RunResult is the terminal record of one Run: its state, every iteration it ran, every change it
// merged, the cumulative spend, and the final live config. MergeEnabled reflects whether the merge step
// is still armed (a halt disarms it); DisarmReason narrates why.
type RunResult struct {
	RunID           string          `json:"run_id"`
	WorkflowID      string          `json:"workflow_id"`
	State           RunState        `json:"state"`
	Iterations      []Iteration     `json:"iterations"`
	Merges          []AppliedChange `json:"merges"`
	CumulativeSpend float64         `json:"cumulative_spend"`
	LiveConfigHash  string          `json:"live_config_hash"`
	MergeEnabled    bool            `json:"merge_enabled"`
	DisarmReason    string          `json:"disarm_reason,omitempty"`
	StopReason      string          `json:"stop_reason,omitempty"`
}

// RegressionMonitor is the post-merge regression detector (spec Requirement "Regression detection ...
// SHALL halt the loop and disarm merge"). After a merge it checks whether any tracked metric degraded
// beyond threshold versus the current best; a true return halts the run with state halted_regression
// and disarms merge until a human re-arms.
type RegressionMonitor interface {
	Check(ctx context.Context, applied AppliedChange) (regressed bool, reason string)
}

// Reattributor re-runs P4.5 attribution after a merge (design risk mitigation / Q3: a diagnosis can be
// stale once a node changed, so re-attribute and apply serially). Nil keeps the initial targets.
type Reattributor interface {
	Reattribute(ctx context.Context, liveConfigHash string) []Target
}

// Controller is the assembled loop. Every field is a seam so the whole machine is provable with fakes;
// the one live path (a real git Repo + a real verification fan-out) is the shipped code.
type Controller struct {
	Search     Search
	Verifier   Verifier
	Repo       Repo
	Ledger     ChangeLedger
	Kill       *KillSwitch
	Locks      *LockSet
	Regression RegressionMonitor
	Reattr     Reattributor
	// Clock supplies timestamps for ledger events. Injected (never time.Now inline) so a run is
	// replayable and the tests are deterministic.
	Clock func() time.Time
	// MaxCostPerRun is the optional per-candidate cost gate (P4 GateMaxCostPerRun). Zero disables it;
	// the run's cumulative ceiling is a separate halt.
	MaxCostPerRun float64
	// Enrich fills a VerifyRequest with the per-candidate eval context (generating cases, cluster
	// structure). Nil leaves the request with just the candidate, baseline, and eval set.
	Enrich func(req *VerifyRequest, cand SearchCandidate)
	// OnIteration, when set, is called with a snapshot of the run's progress at the start of every
	// iteration (and once when the loop ends). It is the seam the live monitor reads P2.5 signals through
	// so the UI shows current iteration / cumulative spend / PRs merged without deriving drifting state.
	OnIteration func(res RunResult)
	// Entitlement is the P7 commercial gate, consulted BEFORE every merge (P7 task 5.2 / FR8).
	//
	// It is deliberately separate from the three technical prerequisites: those answer "is merging SAFE
	// here", this answers "did this customer contract for it". Absent the Autonomous auto-merge
	// entitlement the loop FALLS BACK to opening a pull request for a human — the same verified change,
	// one automation level down — rather than merging or silently dropping the candidate.
	//
	// Nil disables the commercial gate entirely, which is the correct behaviour for a self-hosted or
	// pre-commercial deployment: there is no billing plan to consult, so there is nothing to enforce.
	// A deployment that HAS billing wires this in, and the P7 rollout keeps the Enterprise auto-merge
	// entitlement off until the gate is verified.
	Entitlement MergeEntitlement
	// Admission is the P8 OPERATOR brake, consulted immediately before every merge (P8 FR7/FR12).
	//
	// It is separate from Kill, and the difference matters. `Kill` is the per-run switch a customer or
	// this run's operator fires — in-process, scoped to this Controller. `Admission` is the PLATFORM's
	// brake: the operator console's global and per-tenant kill switch and a tenant suspension, set
	// from outside this process, effective immediately and with no deploy. A runaway fleet needs the
	// second one, and wiring it to the same pre-merge check point means there is one enforcement
	// point rather than two that can drift.
	//
	// Nil disables it, which is correct for a self-hosted deployment with no operator console. A
	// deployment that HAS one wires it, and then an indeterminate answer halts (see the call site).
	Admission MergeAdmission
}

// MergeAdmission is the P6 loop's view of the P8 operator brake.
//
// Declared here as the narrowest possible interface, for the same reason MergeEntitlement is: the
// optimizer must remain buildable and testable with no operator console at all.
type MergeAdmission interface {
	// AllowMerge reports whether the platform currently permits this tenant's autonomous merge.
	//
	// The error return is the load-bearing part. It means the state is INDETERMINATE — the kill-switch
	// store is unreachable — and its ONLY correct handling is to halt: "can't tell if we're stopped"
	// means stopped (P8 design Decision 4). An implementation must never return (true, nil) when it
	// could not read the state.
	AllowMerge(customerID string) (allowed bool, reason string, err error)
}

// MergeEntitlement is the P6 loop's view of the P7 entitlement gate.
//
// It is declared here, as the narrowest possible interface, rather than importing the entitlement
// package: the optimizer must remain buildable and testable with no billing stack at all, and a
// one-method interface is the seam that keeps the commercial concern from leaking into the loop's
// dependency graph. `entitlement.MergeGate` satisfies it.
type MergeEntitlement interface {
	// AllowAutoMerge reports whether this customer's active plan entitles Autonomous auto-merge. On a
	// denial it MUST return a named reason and, where one exists, the plan that lifts it — the loop
	// writes both into the audit trail, so a fallback is never a silent one.
	AllowAutoMerge(customerID string) (allowed bool, reason, upgradePlan string)
}

// RunInput is one Run's parameters: the recorded authority (constraints + arms), the attributed targets
// to search, the baseline spec bytes, and the eval set.
type RunInput struct {
	Authority         Authority
	Targets           []Target
	BaselineSpecBytes []byte
	EvalSetCaseIDs    []string
	Seeds             []int64
}

func (c *Controller) now() time.Time {
	if c.Clock != nil {
		return c.Clock()
	}
	return time.Unix(0, 0).UTC()
}

// Run drives the whole loop for one authority grant. It returns the terminal RunResult and only errors
// on a precondition it cannot even start under (a lock conflict) — every in-loop failure is handled by
// failing closed, not by returning an error, so the caller always gets a well-defined terminal state
// with the last-good spec live.
func (c *Controller) Run(ctx context.Context, in RunInput) (RunResult, error) {
	auth := in.Authority
	res := RunResult{RunID: auth.RunID, WorkflowID: auth.WorkflowID, State: StateRunning}

	// Concurrency safety (task 6.4): one active run per workflow.
	if c.Locks != nil {
		if err := c.Locks.Acquire(auth.WorkflowID, auth.RunID); err != nil {
			return res, err
		}
		defer c.Locks.Release(auth.WorkflowID, auth.RunID)
	}

	// The three-prerequisite merge gate (design Decision 3): merge is enabled only when kill switch +
	// audit + rollback are ALL armed. Absent any one, the run is a propose/verify dry-run that may open
	// draft PRs but merges nothing.
	mergeEnabled := auth.MergeArmed()
	res.MergeEnabled = mergeEnabled

	// Record the grant (spec: the grant is recorded in the audit trail). A downed ledger at grant time
	// means we cannot audit anything: fail closed before doing any work.
	if _, err := c.append(auth.RunID, LedgerEvent{Type: EventGrant, Actor: auth.Actor,
		Summary: c.grantSummary(auth)}); err != nil {
		res.State = StateStopped
		res.StopReason = "change-ledger unavailable at grant: " + err.Error()
		res.MergeEnabled = false
		return res, nil
	}

	head, err := c.Repo.Head(ctx)
	if err != nil {
		res.State = StateStopped
		res.StopReason = "cannot read live spec: " + err.Error()
		res.MergeEnabled = false
		return res, nil
	}
	res.LiveConfigHash = head

	cons := auth.Constraints
	var (
		cumulative    float64
		blindSpent    float64
		bestComposite evalstats.Interval // the current best's composite; gains are measured against it
		stall         int
		consumed      = map[string]bool{}
	)
	targets := in.Targets

	for idx := 0; ; idx++ {
		if c.OnIteration != nil {
			c.OnIteration(res) // live-monitor snapshot of progress so far (P2.5 signal, never derived)
		}
		// ── Stopping conditions checked at the top of every iteration ──
		if c.Kill != nil && c.Kill.Fired() {
			c.stop(&res, StateStopped, "kill switch fired", auth.Actor)
			break
		}
		if cons.MaxIterations > 0 && idx >= cons.MaxIterations {
			c.stop(&res, StateMaxIter, fmt.Sprintf("reached max iterations (%d)", cons.MaxIterations), auth.Actor)
			break
		}
		if cons.BudgetCeilingUSD > 0 && cumulative >= cons.BudgetCeilingUSD {
			c.halt(&res, StateHaltedBudget, fmt.Sprintf("cumulative spend $%.4f reached ceiling $%.4f", cumulative, cons.BudgetCeilingUSD), auth.Actor)
			break
		}

		// ── Enumerate: diagnosis-guided first; blind only once guided exhausted ──
		budgetRemaining := remaining(cons.BudgetCeilingUSD, cumulative)
		blindRemaining := remaining(cons.BlindSubBudgetUSD, blindSpent)
		cands := filterConsumed(c.Search.NextCandidates(targets, head, Policy{
			TargetedExhausted: false, BudgetRemaining: budgetRemaining, BlindBudgetRemaining: blindRemaining}), consumed)
		if len(cands) == 0 {
			cands = filterConsumed(c.Search.NextCandidates(targets, head, Policy{
				TargetedExhausted: true, BudgetRemaining: budgetRemaining, BlindBudgetRemaining: blindRemaining}), consumed)
		}
		if len(cands) == 0 {
			// The search space is exhausted. If we merged at least one gain, we converged on the best
			// reachable config; if nothing ever merged, the search made no progress — that is a stall, not
			// a convergence (an honest state never claims a win it did not earn).
			if len(res.Merges) > 0 {
				c.stop(&res, StateConverged, "search space exhausted after gains — converged on the best reachable config", auth.Actor)
			} else {
				c.stop(&res, StateStalled, "search space exhausted with no gate-passing, verified gain", auth.Actor)
			}
			break
		}
		cand := cands[0]
		consumed[cand.ConfigHash] = true

		iter := Iteration{RunID: auth.RunID, Idx: idx, DiagnosisID: cand.DiagnosisID,
			CandidateConfigHash: cand.ConfigHash, Node: cand.Node, Dimension: cand.Dimension, Source: cand.Source}
		c.appendConsider(auth.RunID, cand)

		// Kill check immediately before spending on verification (task 4.2: firing mid-iteration
		// merges/spends nothing further).
		if c.Kill != nil && c.Kill.Fired() {
			c.stop(&res, StateStopped, "kill switch fired before verification", auth.Actor)
			break
		}

		// ── Verify (build + typed-contract + held-out gate) ──
		req := VerifyRequest{Candidate: cand, BaselineConfig: head, EvalSetCaseIDs: in.EvalSetCaseIDs,
			Config: verifyConfigFor(cons, in.Seeds)}
		if c.Enrich != nil {
			c.Enrich(&req, cand)
		}
		vr, verr := c.Verifier.Verify(ctx, req)
		if verr != nil {
			// Fail-closed degradation (task 6.3): the verification service is unavailable → stop merging,
			// leave last-good live.
			c.stop(&res, StateStopped, "verification unavailable: "+verr.Error(), auth.Actor)
			res.MergeEnabled = false
			break
		}
		cumulative += vr.SpendUSD
		if cand.Source == SourceBlind {
			blindSpent += vr.SpendUSD
		}
		res.CumulativeSpend = cumulative

		iter.SpendUSD = vr.SpendUSD
		iter.Builds = vr.Builds
		iter.VerifyDelta = vr.Verdict.Delta
		iter.VerifySignificant = vr.Verdict.Significant
		iter.Regression = vr.Regressed()
		c.appendVerify(auth.RunID, cand, vr)

		// Budget halt AFTER charging this iteration's spend (task 5.2: drive past the ceiling → halt).
		if cons.BudgetCeilingUSD > 0 && cumulative >= cons.BudgetCeilingUSD {
			iter.Reason = "budget ceiling reached"
			res.Iterations = append(res.Iterations, iter)
			c.halt(&res, StateHaltedBudget, fmt.Sprintf("cumulative spend $%.4f reached ceiling $%.4f", cumulative, cons.BudgetCeilingUSD), auth.Actor)
			break
		}

		// ── Objective + hard-constraint gates (design Decision 1) ──
		gates := EvaluateGates(cons, vr.Metrics, c.MaxCostPerRun)
		iter.GatePassed = gates.Passed
		iter.GateFailed = gates.Failed

		// Kill check right before the merge decision (discard the in-flight result rather than merge it).
		if c.Kill != nil && c.Kill.Fired() {
			res.Iterations = append(res.Iterations, iter)
			c.stop(&res, StateStopped, "kill switch fired before merge — in-flight result discarded", auth.Actor)
			break
		}

		// ── P8 operator brake (P8 FR7/FR12), read FAIL-CLOSED to halt ─────────────
		//
		// Checked here, in the same pre-merge window as the per-run kill switch, so an operator arming
		// the global switch stops the next merge everywhere without a deploy and without this process
		// restarting. An error is INDETERMINATE state and halts exactly as an armed switch does — the
		// last-good Variant Spec stays live either way, which is the only outcome that is safe when we
		// cannot tell whether we are supposed to be stopped.
		if c.Admission != nil {
			allowed, why, aerr := c.Admission.AllowMerge(auth.CustomerID)
			switch {
			case aerr != nil:
				res.Iterations = append(res.Iterations, iter)
				c.stop(&res, StateStopped, "operator kill-switch state indeterminate — fail closed, no merge: "+aerr.Error(), auth.Actor)
				res.MergeEnabled = false
				return res, nil
			case !allowed:
				res.Iterations = append(res.Iterations, iter)
				c.stop(&res, StateStopped, "halted by the operator console: "+why, auth.Actor)
				res.MergeEnabled = false
				return res, nil
			}
		}

		// A candidate that fails verification (contract/build/held-out) OR fails any P4 gate is never
		// merged, however high its composite (design Decision 1). It is a no-progress iteration.
		if !vr.MergeReady() || !gates.Passed {
			iter.Reason = noProgressReason(vr, gates)
			res.Iterations = append(res.Iterations, iter)
			stall++
			if stall >= cons.stallK() {
				c.stop(&res, StateStalled, fmt.Sprintf("%d consecutive iterations with no gate-passing, verified gain", stall), auth.Actor)
				break
			}
			continue
		}

		// ── Min-improvement stop (task 3.2): the verified marginal gain (composite CI-lower-bound) is
		//    below the threshold → converge rather than chase a smaller gain. ──
		gain := CompositeGain(vr.Metrics.Composite, bestComposite)
		iter.CompositeGain = gain
		if cons.MinImprovement > 0 && gain < cons.MinImprovement {
			iter.Reason = fmt.Sprintf("verified gain %.4f below min-improvement %.4f", gain, cons.MinImprovement)
			res.Iterations = append(res.Iterations, iter)
			c.stop(&res, StateConverged, iter.Reason, auth.Actor)
			break
		}

		// ── Apply ──
		if !mergeEnabled {
			// Dry-run (design Decision 3): a missing prerequisite disables merge. Open a DRAFT PR so the
			// verified proposal is reviewable, merge nothing, keep the last-good spec live.
			pr, derr := c.Repo.OpenPR(ctx, OpenPRRequest{ProposalID: cand.ConfigHash[:min(12, len(cand.ConfigHash))],
				Branch: "optimizer/dryrun-" + shortHash(cand.ConfigHash), SpecBytes: cand.SpecBytes, Draft: true,
				Message: "optimizer dry-run candidate " + cand.Operator})
			if derr == nil {
				iter.PRRef = pr.Branch
			}
			iter.Reason = "dry-run: draft PR opened, merge disabled (missing prerequisite)"
			res.Iterations = append(res.Iterations, iter)
			stall = 0 // a verified, gate-passing candidate is progress even when we may not merge it
			continue
		}

		// ── P7 entitlement gate (task 5.2 / FR8) ─────────────────────────────────
		//
		// Consulted HERE — after the candidate has passed every technical gate, immediately before the
		// merge — because that is the only point at which the question "may this customer merge" has a
		// concrete answer to gate. Denied, the loop opens a NON-DRAFT pull request: the customer gets the
		// verified change reviewable by a human (the Assisted contract they DO have), and the denial is
		// audited with its named reason and upgrade path. It is never a silent drop and never a silent
		// merge.
		if c.Entitlement != nil {
			allowed, reason, upgrade := c.Entitlement.AllowAutoMerge(auth.CustomerID)
			if !allowed {
				pr, derr := c.Repo.OpenPR(ctx, OpenPRRequest{ProposalID: cand.ConfigHash[:min(12, len(cand.ConfigHash))],
					Branch: "optimizer/assisted-" + shortHash(cand.ConfigHash), SpecBytes: cand.SpecBytes, Draft: false,
					Message: "verified optimization candidate " + cand.Operator + " (auto-merge not entitled)"})
				if derr == nil {
					iter.PRRef = pr.Branch
				}
				summary := "auto-merge not entitled: " + reason
				if upgrade != "" {
					summary += " (upgrade plan: " + upgrade + ")"
				}
				summary += " — opened a pull request for a human instead"
				// Audited, not merely logged: a commercial decision that changed what the loop did to a
				// customer's repository has to survive in the trail alongside the technical ones.
				if _, lerr := c.append(auth.RunID, LedgerEvent{Type: EventEntitlementDenied, Actor: auth.Actor,
					FromConfigHash: head, ToConfigHash: cand.ConfigHash, PRRef: iter.PRRef,
					DiagnosisID: cand.DiagnosisID, Summary: summary}); lerr != nil {
					// The ledger being down means the fallback itself is unauditable. Fail closed exactly as a
					// merge would: stop, last-good spec live.
					iter.Reason = "change-ledger unavailable — entitlement fallback unaudited"
					res.Iterations = append(res.Iterations, iter)
					c.stop(&res, StateStopped, "change-ledger unavailable — fail closed", auth.Actor)
					res.MergeEnabled = false
					break
				}
				iter.Reason = summary
				res.Iterations = append(res.Iterations, iter)
				stall = 0 // a verified, gate-passing candidate is progress even when we may not merge it
				continue
			}
		}

		applied, aerr := c.apply(ctx, auth, idx, cand, vr, head)
		if aerr != nil {
			if errors.Is(aerr, ErrLedgerUnavailable) {
				// Write-ahead audit down (design Decision 4 / task 6.3): the merge cannot be audited, so it
				// must not happen. Fail closed, last-good live.
				iter.Reason = "change-ledger unavailable — merge withheld"
				res.Iterations = append(res.Iterations, iter)
				c.stop(&res, StateStopped, "change-ledger unavailable — fail closed", auth.Actor)
				res.MergeEnabled = false
				break
			}
			// Merge failed after the write-ahead event (task 3.4 recovery): the audit shows the attempt,
			// no merge commit exists, and the last-good spec is still live. Treat as no-progress.
			iter.Reason = "merge failed, last-good spec left live: " + aerr.Error()
			res.Iterations = append(res.Iterations, iter)
			stall++
			if stall >= cons.stallK() {
				c.stop(&res, StateStalled, "repeated merge failures", auth.Actor)
				break
			}
			continue
		}

		// Merge succeeded: the candidate is now live.
		iter.Merged = true
		iter.PRRef = applied.PRRef
		iter.MergeCommit = applied.MergeCommit
		head = applied.ToConfigHash
		res.LiveConfigHash = head
		bestComposite = vr.Metrics.Composite
		res.Merges = append(res.Merges, applied)
		res.Iterations = append(res.Iterations, iter)
		stall = 0

		// ── Post-merge regression halt (task 5.3) ──
		if c.Regression != nil {
			if regressed, why := c.Regression.Check(ctx, applied); regressed {
				c.halt(&res, StateHaltedRegression, "regression detected after merge: "+why, auth.Actor)
				break
			}
		}

		// ── Re-attribute after an apply (design Q3): the earlier diagnosis may be stale ──
		if c.Reattr != nil {
			targets = c.Reattr.Reattribute(ctx, head)
		}
	}

	if c.OnIteration != nil {
		c.OnIteration(res) // final terminal-state snapshot for the monitor
	}
	return res, nil
}

// apply opens a PR and merges it, with the change-ledger event committed AHEAD of the merge (design
// Decision 4). The ordering is the guarantee: the apply event is durable before Repo.Merge is called,
// so a merge that is not audited cannot occur, and a downed ledger stops the merge rather than
// producing a silent one.
func (c *Controller) apply(ctx context.Context, auth Authority, idx int, cand SearchCandidate, vr VerifyResult, fromConfig string) (AppliedChange, error) {
	pr, err := c.Repo.OpenPR(ctx, OpenPRRequest{
		ProposalID: cand.ConfigHash[:min(12, len(cand.ConfigHash))],
		Branch:     "optimizer/" + shortHash(cand.ConfigHash),
		SpecBytes:  cand.SpecBytes,
		Draft:      false,
		Message:    fmt.Sprintf("optimizer: %s at %s (verified +%.3f)", cand.Operator, cand.Node, vr.Verdict.Delta.Mean),
	})
	if err != nil {
		return AppliedChange{}, fmt.Errorf("open PR: %w", err)
	}

	// WRITE-AHEAD: the apply event is committed BEFORE the merge (task 4.4).
	seq, lerr := c.append(auth.RunID, LedgerEvent{
		Type: EventApply, Actor: auth.Actor, DiagnosisID: cand.DiagnosisID,
		FromConfigHash: fromConfig, ToConfigHash: pr.ToConfigHash, PRRef: pr.Branch,
		Summary: fmt.Sprintf("merge %s at %s: held-out +%.3f [%.3f,%.3f], cost %+.4f, latency %+.0f",
			cand.Operator, cand.Node, vr.Verdict.Delta.Mean, vr.Verdict.Delta.Low, vr.Verdict.Delta.High,
			vr.Verdict.CostDelta, vr.Verdict.LatencyDelta),
	})
	if lerr != nil {
		return AppliedChange{}, lerr // ErrLedgerUnavailable → the loop fails closed, no merge
	}

	commit, merr := c.Repo.Merge(ctx, pr)
	if merr != nil {
		return AppliedChange{}, fmt.Errorf("merge: %w", merr)
	}
	// The merge commit ref is written back onto the apply event (design Decision 4).
	_ = c.Ledger.Backfill(auth.RunID, seq, commit)

	return AppliedChange{
		RunID: auth.RunID, Idx: idx, FromConfigHash: fromConfig, ToConfigHash: pr.ToConfigHash,
		PRRef: pr.Branch, MergeCommit: commit, DiagnosisID: cand.DiagnosisID, Operator: cand.Operator,
		Node: cand.Node, VerifiedDelta: vr.Verdict.Delta.Mean, CostDelta: vr.Verdict.CostDelta,
		LatencyDelta: vr.Verdict.LatencyDelta, LedgerSeq: seq,
	}, nil
}

// Rollback reverts a merged change via `git revert` of its merge commit, reconstructing the exact prior
// Variant Spec, and records the revert in the change ledger (spec Requirement "Any applied change SHALL
// be reversible to the exact prior Variant Spec via git revert"). It returns the revert commit and the
// resulting live config hash, which equals the change's FromConfigHash.
func (c *Controller) Rollback(ctx context.Context, runID string, applied AppliedChange, actor string) (revertCommit, liveConfigHash string, err error) {
	revertCommit, liveConfigHash, err = c.Repo.Revert(ctx, applied.MergeCommit)
	if err != nil {
		return "", "", fmt.Errorf("optimizer: git revert: %w", err)
	}
	if _, aerr := c.append(runID, LedgerEvent{
		Type: EventRevert, Actor: actor, MergeCommit: revertCommit,
		FromConfigHash: applied.ToConfigHash, ToConfigHash: liveConfigHash,
		Summary: fmt.Sprintf("git revert of merge %s → live %s", shortHash(applied.MergeCommit), shortHash(liveConfigHash)),
	}); aerr != nil {
		return revertCommit, liveConfigHash, fmt.Errorf("optimizer: audit revert: %w", aerr)
	}
	return revertCommit, liveConfigHash, nil
}

// Rearm records a human re-arming the merge step after a halt (design Decision 5 / task 5.4). It is an
// explicit, audited action; re-arming does NOT change the run's immutable constraints. The returned
// Authority has the merge prerequisites armed so a resumed Run may merge again.
func (c *Controller) Rearm(runID string, auth Authority, actor string) (Authority, error) {
	if _, err := c.append(runID, LedgerEvent{Type: EventRearm, Actor: actor,
		Summary: "human re-armed merge after halt"}); err != nil {
		return auth, err
	}
	auth.KillSwitchArmed, auth.AuditArmed, auth.RollbackArmed = true, true, true
	return auth, nil
}

// ── ledger helpers (all events are secret-free: numbers, hashes, stable ids only) ──

func (c *Controller) append(runID string, ev LedgerEvent) (int, error) {
	ev.RunID = runID
	ev.TS = c.now()
	return c.Ledger.Append(ev)
}

func (c *Controller) appendConsider(runID string, cand SearchCandidate) {
	_, _ = c.append(runID, LedgerEvent{Type: EventConsider, DiagnosisID: cand.DiagnosisID,
		ToConfigHash: cand.ConfigHash,
		Summary:      fmt.Sprintf("consider %s at %s (%s), expected gain %.3f", cand.Operator, cand.Node, cand.Source, cand.ExpectedGain)})
}

func (c *Controller) appendVerify(runID string, cand SearchCandidate, vr VerifyResult) {
	_, _ = c.append(runID, LedgerEvent{Type: EventVerify, DiagnosisID: cand.DiagnosisID,
		ToConfigHash: cand.ConfigHash,
		Summary: fmt.Sprintf("verify %s: builds=%t gate=%s held-out +%.3f sig=%t regression-clean=%t",
			cand.Operator, vr.Builds, vr.Verdict.GateResult, vr.Verdict.Delta.Mean, vr.Verdict.Significant, vr.Verdict.RegressionPass)})
}

func (c *Controller) stop(res *RunResult, state RunState, reason, actor string) {
	res.State = state
	res.StopReason = reason
	_, _ = c.append(res.RunID, LedgerEvent{Type: EventStop, Actor: actor, Summary: string(state) + ": " + reason})
}

func (c *Controller) halt(res *RunResult, state RunState, reason, actor string) {
	res.State = state
	res.MergeEnabled = false // disarm-until-re-arm (design Decision 5)
	res.DisarmReason = reason
	_, _ = c.append(res.RunID, LedgerEvent{Type: EventHalt, Actor: actor, Summary: string(state) + ": " + reason + " — merge disarmed until re-armed"})
}

func (c *Controller) grantSummary(a Authority) string {
	return fmt.Sprintf("grant: budget $%.2f, max-iter %d, min-improvement %.3f, allowlist %v, merge-armed=%t",
		a.Constraints.BudgetCeilingUSD, a.Constraints.MaxIterations, a.Constraints.MinImprovement,
		a.Constraints.ProviderAllowlist, a.MergeArmed())
}
