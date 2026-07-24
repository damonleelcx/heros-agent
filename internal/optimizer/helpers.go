package optimizer

import (
	"fmt"

	"github.com/heros-foreal/agentd/internal/verification"
)

// helpers.go holds the small pure functions the loop leans on, kept out of loop.go so the control flow
// reads top-to-bottom.

// remaining returns how much of a ceiling is left given spend, or a large positive number when the
// ceiling is unset (zero) — an unset ceiling means "no cap", not "no budget".
func remaining(ceiling, spent float64) float64 {
	if ceiling <= 0 {
		return 1e18 // effectively unbounded
	}
	r := ceiling - spent
	if r < 0 {
		return 0
	}
	return r
}

// filterConsumed drops candidates whose config hash the loop has already tried this run, so the search
// never re-verifies the same candidate (and a bounded candidate set guarantees termination).
func filterConsumed(cands []SearchCandidate, consumed map[string]bool) []SearchCandidate {
	out := cands[:0:0]
	for _, c := range cands {
		if !consumed[c.ConfigHash] {
			out = append(out, c)
		}
	}
	return out
}

// noProgressReason narrates why an iteration did not merge — a build/contract failure, an unverified
// (insignificant/regressing) delta, or a failed P4 gate — for the iteration record and the audit trail.
func noProgressReason(vr VerifyResult, gates GateVerdict) string {
	switch {
	case !vr.ContractOK:
		return "typed-contract violation: " + vr.ContractReason
	case !vr.Builds:
		return "candidate diff failed to build"
	case !vr.Verdict.Passed():
		return "held-out verification did not pass: " + string(vr.Verdict.GateResult)
	case !gates.Passed:
		return "failed hard constraint gate(s): " + fmt.Sprintf("%v", gates.Failed)
	default:
		return "no merge"
	}
}

// shortHash renders a hash for a branch name / summary without leaking length assumptions.
func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

// verifyConfigFor builds the P5.5 verification config from the run's immutable constraints: the seed
// set, the default statistics config, and the hard cost/latency budget the regression check enforces.
// The provider allowlist is enforced by EvaluateGates in the loop, not smuggled in here.
func verifyConfigFor(cons Constraints, seeds []int64) verification.Config {
	cfg := verification.DefaultConfig()
	if len(seeds) > 0 {
		cfg.Seeds = seeds
	}
	if cons.LatencySLAMs > 0 {
		cfg.Budget.MaxLatencyMS = cons.LatencySLAMs
	}
	return cfg
}

// Regressed reports whether the verification verdict was withheld specifically for a regression (a
// tracked metric degraded beyond threshold), as opposed to a non-significant gain or a constraint
// breach. The loop records this per iteration so the audit distinguishes "no gain" from "caused harm".
func (r VerifyResult) Regressed() bool {
	return r.Verdict.GateResult == verification.GateFailRegress
}
