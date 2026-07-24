package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/optimizer"
	"github.com/heros-foreal/agentd/internal/verification"
)

// hermesrepo.go runs a REAL P6 autonomous merge against the REAL hermes-agent checkout, so the P8
// audit log holds a merge an auditor can `git show`.
//
// # Why P8's demo touches a real repository at all
//
// P8's load-bearing audit claim is that EVERY autonomous merge is on the tamper-evident record with
// its motivating diagnosis, verified delta and merge commit (FR16). Against a fixture, "the merge
// commit is recorded" is a string comparison. Against this repository it is a git object: the loop
// opens a branch, writes the candidate Variant Spec, merges it, and the SHA the audit chain carries is
// the SHA `git log` shows. The same reasoning is why cmd/p6hermes and cmd/p7hermes run here.
//
// # What is real, and what is stubbed — labelled, exactly as the sibling demos label theirs
//
// REAL: the repository and every git operation (branch, spec write, merge commit); the node ids,
// symbols and files (actual call sites in the hermes source, read from the checkout's own
// variant_spec.json); the whole P6 controller and its merge-prerequisite gates; the P8 AuditingLedger
// that mirrors the merge into the hash-chained audit log; and the kill-switch admission gate consulted
// before the merge. STUBBED: the DIAGNOSIS input (in production from the P4.5 attribution engine) and
// the VERIFICATION deltas (in production from real eval runs through a provider).

// specPath is the repo-relative path of the live Variant Spec the loop merges.
const specPath = "variant_spec.json"

// hermesMerge is the outcome of one real merge against the checkout.
type hermesMerge struct {
	Node        string
	DiagnosisID string
	MergeCommit string
	Delta       float64
	Summary     string
}

// singleCandidateEnum enumerates exactly one candidate for the target node — the diagnosis-guided
// candidate this demo proposes.
type singleCandidateEnum struct {
	byNode map[string][]optimizer.SearchCandidate
}

func (e singleCandidateEnum) Enumerate(t optimizer.Target) []optimizer.SearchCandidate {
	return e.byNode[t.Node]
}

// runHermesMerge drives one real P6 merge against dir, recording it through ledger (the P8
// AuditingLedger) so the merge lands in the audit chain with its real commit SHA.
//
// customerID attributes the run to the tenant, so the audit entry is reconstructable to a tenant the
// console can open.
func runHermesMerge(dir, customerID string, ledger optimizer.ChangeLedger, admission optimizer.MergeAdmission) (hermesMerge, error) {
	// ── The baseline is the checkout's OWN live Variant Spec, not an invented one ──
	baseBytes, err := os.ReadFile(filepath.Join(dir, specPath))
	if err != nil {
		return hermesMerge{}, fmt.Errorf("read the checkout's live variant spec: %w", err)
	}
	var baseline map[string]any
	if err := json.Unmarshal(baseBytes, &baseline); err != nil {
		return hermesMerge{}, fmt.Errorf("parse %s: %w", specPath, err)
	}

	// Pick a REAL node out of the checkout's spec — a genuine hermes call site, in a stable order so
	// re-running the demo picks the same one.
	node, entry, err := firstNode(baseline)
	if err != nil {
		return hermesMerge{}, err
	}

	// ── The candidate: a real, minimal change to that node's model dimension ──
	candidate := deepCopy(baseline)
	candEntry, _ := candidate[node].(map[string]any)
	if candEntry == nil {
		return hermesMerge{}, fmt.Errorf("node %q has no object body in the spec", node)
	}
	// The checkout's spec shape is {dimension, file, symbol, value} per node — the model lives in
	// `value`. Editing that one field is the smallest real change that produces a genuine diff.
	from, _ := entry["value"].(string)
	const to = "claude-sonnet-5"
	candEntry["value"] = to
	candidate[node] = candEntry

	specBytes, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return hermesMerge{}, err
	}
	specBytes = append(specBytes, '\n')
	hash := optimizer.ContentHash(specBytes)

	const diagnosisID = "diag-hermes-latency-p95"
	const delta = 0.42

	cand := optimizer.SearchCandidate{
		DiagnosisID: diagnosisID, Node: node, Dimension: "model", Operator: "model_upgrade",
		ConfigHash: hash, SpecBytes: specBytes, Providers: []string{"anthropic"}, ExpectedGain: 0.5,
		Rationale: fmt.Sprintf("model_upgrade at %s: %s → %s", node, from, to),
	}

	verifier := optimizer.StaticVerifier{ByConfig: map[string]optimizer.VerifyResult{
		hash: {
			ContractOK: true, Builds: true, SpendUSD: 0.42,
			Verdict: verification.Verdict{
				GateResult: verification.GatePass, Significant: true, HeldOut: true, RegressionPass: true,
				Delta:     evalstats.Interval{Mean: delta, Low: delta - 0.05, High: delta + 0.05},
				CostDelta: -0.0031, LatencyDelta: -85,
			},
			Metrics: optimizer.CandidateMetrics{
				Providers: []string{"anthropic"}, Quality: 0.91, LatencyMS: 505,
				Composite: evalstats.Interval{Mean: 0.75, Low: 0.70, High: 0.80},
			},
		},
	}}

	ctrl := &optimizer.Controller{
		Search:   optimizer.Search{Enum: singleCandidateEnum{byNode: map[string][]optimizer.SearchCandidate{node: {cand}}}},
		Verifier: verifier,
		Repo:     optimizer.GitRepo{Dir: dir, SpecPath: specPath, Branch: "main"},
		Ledger:   ledger,
		Kill:     optimizer.NewKillSwitch(),
		// The P8 operator brake, consulted before the merge — the same gate the console arms.
		Admission: admission,
		Clock:     func() time.Time { return time.Now().UTC() },
	}

	auth := optimizer.Authority{
		RunID: "run-" + customerID, WorkflowID: "nousresearch/hermes-agent", Actor: "optimizer",
		CustomerID:    customerID,
		WeightProfile: "balanced",
		Constraints: optimizer.Constraints{
			BudgetCeilingUSD: 25, ProviderAllowlist: []string{"anthropic"},
			MinImprovement: 0.02, MaxIterations: 2, StallK: 2,
		},
		KillSwitchArmed: true, AuditArmed: true, RollbackArmed: true, GrantedAt: time.Now().UTC(),
	}
	in := optimizer.RunInput{
		Authority:         auth,
		Targets:           []optimizer.Target{{DiagnosisID: diagnosisID, Node: node, Dimension: "model", Priority: 1}},
		BaselineSpecBytes: baseBytes,
		EvalSetCaseIDs:    []string{"h1", "h2", "h3", "h4", "h5", "h6"},
	}

	res, err := ctrl.Run(context.Background(), in)
	if err != nil {
		return hermesMerge{}, fmt.Errorf("optimizer run: %w", err)
	}
	if len(res.Merges) == 0 {
		return hermesMerge{}, fmt.Errorf("the loop merged nothing (state %s: %s)", res.State, res.StopReason)
	}
	m := res.Merges[0]
	return hermesMerge{
		Node: m.Node, DiagnosisID: m.DiagnosisID, MergeCommit: m.MergeCommit, Delta: m.VerifiedDelta,
		Summary: fmt.Sprintf("merge %s at %s: held-out +%.3f, cost %+.4f, latency %+.0f",
			m.Operator, m.Node, m.VerifiedDelta, m.CostDelta, m.LatencyDelta),
	}, nil
}

// firstNode returns the lexicographically first node in the spec whose dimension is "model", so the
// demo acts on a REAL hermes call site and picks the same one on every run.
//
// It skips the spec's non-node entries (the top-level "workflow" string) by requiring an object body
// with a model dimension — the shape the checkout actually uses.
func firstNode(spec map[string]any) (string, map[string]any, error) {
	best := ""
	var bestEntry map[string]any
	for node, raw := range spec {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue // the top-level "workflow" string, not a node
		}
		if dim, _ := entry["dimension"].(string); dim != "model" {
			continue
		}
		if _, has := entry["value"]; !has {
			continue
		}
		if best == "" || node < best {
			best, bestEntry = node, entry
		}
	}
	if best == "" {
		return "", nil, fmt.Errorf("the checkout's %s has no node with a model dimension", specPath)
	}
	return best, bestEntry, nil
}

// deepCopy clones the parsed spec so the candidate's edit cannot mutate the baseline the loop compares
// against.
func deepCopy(in map[string]any) map[string]any {
	b, err := json.Marshal(in)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return map[string]any{}
	}
	return out
}
