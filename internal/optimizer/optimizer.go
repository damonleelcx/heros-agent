// Package optimizer is the P6 autonomous optimizer: the CONTROLLER that closes the
// analyze → propose → verify → apply loop. It adds no new evaluation machinery — the objective (the P4
// composite score), the constraints (the P4 gates), the verifier (the P5.5 held-out gate), and the
// change-operators + their codemod/build gate (the P5.5 catalog) all already exist. P6 drives them and
// turns Assisted's "open a PR" into "open AND merge a PR", plus the operational substrate that makes
// merging safe: kill switch + audit trail (git history + an append-only change ledger) + rollback
// (`git revert`), hard-constraint gates, and regression/budget halts.
//
// Per ADR-001 and design Decision 3, the merge step is DISABLED unless all three prerequisites — kill
// switch, audit trail, rollback — are armed for the run; absent any one, the loop runs in propose/
// verify DRY-RUN (may open draft PRs, merges nothing). Every merge is additionally gated by build +
// held-out eval + regression AND the P4 gates, and the change-ledger event is written AHEAD of the
// merge so no applied change can escape the trail.
//
// The controller is deliberately a set of injectable seams (Search, Verifier, Repo, ChangeLedger,
// KillSwitch, Clock) so the whole loop — diagnosis-guided search, verification-in-the-loop, stopping/
// stall/recovery, halts, disarm-until-re-arm, fail-closed degradation, git-revert rollback — is
// provable with fakes, and the ONE live path (an actual git repo + a real verification fan-out) is the
// shipped code, not a parallel mock.
package optimizer

import (
	"time"

	"github.com/heros-foreal/agentd/internal/evalstats"
)

// RunState is the first-class terminal (or in-progress) state of an optimization run. It is the single
// value the UI and the audit trail key off, so it is a stable string, never an ordinal. Two families:
// STOPS are terminal (the run ends); HALTS disarm the merge step until a human re-arms (design
// Decision 5).
type RunState string

const (
	StateRunning          RunState = "running"           // the loop is iterating
	StateConverged        RunState = "converged"         // min-improvement stop: the next gain is below threshold
	StateMaxIter          RunState = "max_iter"          // reached the max-iterations bound
	StateStalled          RunState = "stalled"           // K consecutive no-progress iterations
	StateStopped          RunState = "stopped"           // kill switch fired (terminal)
	StateHaltedRegression RunState = "halted_regression" // a tracked metric regressed → merge disarmed
	StateHaltedBudget     RunState = "halted_budget"     // cumulative spend breached the ceiling → merge disarmed
	StateRolledBack       RunState = "rolled_back"       // a merged change was reverted
)

// IsHalt reports whether the state is a HALT (disarm-until-re-arm) rather than a terminal STOP. A halt
// leaves the run inspectable and re-armable; a stop is done.
func (s RunState) IsHalt() bool {
	return s == StateHaltedRegression || s == StateHaltedBudget
}

// IsTerminal reports whether the loop has ended and will not iterate again without a re-grant.
func (s RunState) IsTerminal() bool {
	switch s {
	case StateConverged, StateMaxIter, StateStalled, StateStopped:
		return true
	default:
		return false
	}
}

// Constraints is the immutable, grant-time hard-constraint set for a whole run (spec Requirement
// "enforce budget ceiling, provider allowlist, min-improvement threshold, and max iterations"). Every
// field is fixed when authority is granted and NEVER changes for the run's duration; changing a
// constraint requires stopping and re-granting. This is a value type copied into the run's
// constraints_snapshot so a mid-run mutation is structurally impossible.
type Constraints struct {
	// BudgetCeilingUSD is the cumulative-spend cap for the whole run. When cumulative spend breaches it,
	// the loop HALTS (design Decision 5) — it does not merely stop.
	BudgetCeilingUSD float64 `json:"budget_ceiling_usd"`
	// ProviderAllowlist is the closed set of providers a candidate may call. A candidate that calls a
	// provider outside it fails the provider-allowlist gate and is never merged. Empty means "no
	// allowlist configured" — every provider is admitted (the gate is not evaluated).
	ProviderAllowlist []string `json:"provider_allowlist,omitempty"`
	// MinImprovement is the marginal-gain floor on the composite CI-lower-bound (design Q2). A verified
	// gain below it stops the loop with state converged rather than chasing a smaller gain.
	MinImprovement float64 `json:"min_improvement"`
	// MaxIterations bounds the run. Reaching it stops the loop with state max_iter.
	MaxIterations int `json:"max_iterations"`
	// MinQuality is the P4 min-quality gate: a candidate whose held-out quality falls below it fails the
	// gate. Zero means unset.
	MinQuality float64 `json:"min_quality"`
	// LatencySLAMs is the P4 latency-SLA gate in milliseconds. Zero means unset.
	LatencySLAMs float64 `json:"latency_sla_ms"`
	// BlindSubBudgetUSD caps the spend the blind (grid/Bayesian) expansion may consume, carved OUT of
	// the total ceiling (design Decision 2 / Q1) so blind search can never eat the whole budget. Zero
	// disables blind expansion entirely.
	BlindSubBudgetUSD float64 `json:"blind_sub_budget_usd"`
	// StallK is the number of consecutive no-progress iterations that trips the stall detector. Zero
	// defaults to DefaultStallK.
	StallK int `json:"stall_k"`
}

// DefaultStallK is the default number of consecutive no-progress iterations before the loop declares
// itself stalled. Small enough that a wandering search stops before burning much budget; large enough
// that one unlucky iteration does not.
const DefaultStallK = 3

// stallK returns the effective stall threshold (the configured value or the default).
func (c Constraints) stallK() int {
	if c.StallK > 0 {
		return c.StallK
	}
	return DefaultStallK
}

// Authority is the recorded grant that lets a run merge (spec Requirement "Autonomous SHALL be a
// distinct automation level with an explicit, recorded authority grant"). It pins the constraints, the
// actor, and the three prerequisite arms; the run's merge step is enabled iff all three are armed AND
// the run is not halted. A grant is itself a change-ledger event.
type Authority struct {
	RunID       string      `json:"run_id"`
	WorkflowID  string      `json:"workflow_id"`
	Actor       string      `json:"actor"`
	Constraints Constraints `json:"constraints"`
	// WeightProfile is the active P4 weight profile whose composite score is the objective.
	WeightProfile string `json:"weight_profile"`
	// The three prerequisites (design Decision 3). Merge is disabled unless ALL are true.
	KillSwitchArmed bool      `json:"kill_switch_armed"`
	AuditArmed      bool      `json:"audit_armed"`
	RollbackArmed   bool      `json:"rollback_armed"`
	GrantedAt       time.Time `json:"granted_at"`
}

// MergeArmed reports whether the three prerequisites are all present (design Decision 3). It is a
// NECESSARY condition for a merge, not a sufficient one — the run must also be un-halted and every
// per-merge gate (build + eval + regression + P4 gates) must pass.
func (a Authority) MergeArmed() bool {
	return a.KillSwitchArmed && a.AuditArmed && a.RollbackArmed
}

// MissingPrereqs names the prerequisites that are NOT armed, in a stable order, for the dry-run banner
// ("apply disabled — rollback not armed"). Empty when all three are armed.
func (a Authority) MissingPrereqs() []string {
	var out []string
	if !a.KillSwitchArmed {
		out = append(out, "kill_switch")
	}
	if !a.AuditArmed {
		out = append(out, "audit_trail")
	}
	if !a.RollbackArmed {
		out = append(out, "rollback")
	}
	return out
}

// CandidateSource records whether a candidate came from the diagnosis-guided phase or the blind
// fallback (design Decision 2). It is stored per iteration so the sample-efficiency claim — "guidance
// beat a blind sweep" — is auditable, not asserted.
type CandidateSource string

const (
	SourceDiagnosisGuided CandidateSource = "diagnosis_guided"
	SourceBlind           CandidateSource = "blind"
)

// Iteration is the record of one loop step (the design's optimization_iteration row). Every field is
// measured or decided by the loop; nothing here is narrative. It is what the audit trail replays.
type Iteration struct {
	RunID               string             `json:"run_id"`
	Idx                 int                `json:"idx"`
	DiagnosisID         string             `json:"diagnosis_id"`
	CandidateConfigHash string             `json:"candidate_config_hash"`
	Node                string             `json:"node_id"`
	Dimension           string             `json:"dimension"`
	Source              CandidateSource    `json:"source"`
	Builds              bool               `json:"builds"`
	VerifyDelta         evalstats.Interval `json:"verify_delta"`
	VerifySignificant   bool               `json:"verify_significant"`
	Regression          bool               `json:"regression"`
	GatePassed          bool               `json:"gate_passed"`
	GateFailed          []string           `json:"gate_failed,omitempty"`
	// CompositeGain is the marginal composite improvement (CI-lower-bound) over the current best — the
	// quantity the min-improvement stop reads (design Q2).
	CompositeGain float64 `json:"composite_gain"`
	Merged        bool    `json:"merged"`
	PRRef         string  `json:"pr_ref,omitempty"`
	MergeCommit   string  `json:"merge_commit,omitempty"`
	// SpendUSD is the provider spend this iteration incurred (added to the run's cumulative spend).
	SpendUSD float64 `json:"spend_usd"`
	Reason   string  `json:"reason,omitempty"`
}
