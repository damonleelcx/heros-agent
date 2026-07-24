package optimizer

import (
	"context"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/verification"
)

// integration_test.go is the P6 fixture (task 8.1) and the M9 exit-checklist confirmation (task 8.9):
// a multi-node workflow with two independently diagnosable defects — a reasoning-heavy node on a weak
// model, and a low-relevance RAG node — a P4 eval set with a held-out slice, P4.5 attribution per
// defect, and the P5.5 verification gate, driven through the whole controller loop.

// twoDefectFixture builds the two-defect enumerator + verifier + repo/ledger for the M9 run.
func twoDefectFixture() (Search, StaticVerifier, []Target, []SearchCandidate) {
	// Defect 1: reasoning node on a weak model → model upgrade (a real, gate-passing, big gain).
	upgrade := SearchCandidate{DiagnosisID: "diag-reason", Node: "reason", Dimension: "model",
		Operator: "model_upgrade", ConfigHash: ContentHash([]byte(`{"reason":"sonnet-5"}`)),
		SpecBytes: []byte(`{"reason":"sonnet-5"}`), Providers: []string{"anthropic"}, ExpectedGain: 0.9}
	// Defect 2: low-relevance RAG node → add rerank (a real, gate-passing, smaller-but-above-threshold gain).
	rerank := SearchCandidate{DiagnosisID: "diag-rag", Node: "rag", Dimension: "skills",
		Operator: "add_rerank", ConfigHash: ContentHash([]byte(`{"rag":"rerank"}`)),
		SpecBytes: []byte(`{"rag":"rerank"}`), Providers: []string{"anthropic"}, ExpectedGain: 0.8}
	// A higher-composite candidate that calls an off-allowlist provider → must never merge.
	illegal := SearchCandidate{DiagnosisID: "diag-reason", Node: "reason", Dimension: "model",
		Operator: "model_swap", ConfigHash: ContentHash([]byte(`{"reason":"gpt-4o"}`)),
		SpecBytes: []byte(`{"reason":"gpt-4o"}`), Providers: []string{"openai"}, ExpectedGain: 0.7}

	enum := fakeEnum{byNode: map[string][]SearchCandidate{
		"reason": {upgrade, illegal},
		"rag":    {rerank},
	}}
	verifier := StaticVerifier{
		Spend: 0.05,
		ByConfig: map[string]VerifyResult{
			upgrade.ConfigHash: pass(0.72, 0.42, []string{"anthropic"}),
			rerank.ConfigHash:  pass(0.80, 0.20, []string{"anthropic"}),
			illegal.ConfigHash: pass(0.95, 0.50, []string{"openai"}), // highest composite, gate-fails
		},
	}
	targets := []Target{
		{DiagnosisID: "diag-reason", Node: "reason", Dimension: "model", Priority: 1.0},
		{DiagnosisID: "diag-rag", Node: "rag", Dimension: "skills", Priority: 0.8},
	}
	return Search{Enum: enum}, verifier, targets, []SearchCandidate{upgrade, rerank, illegal}
}

func pass(compositeLow, delta float64, providers []string) VerifyResult {
	return VerifyResult{ContractOK: true, Builds: true,
		Verdict: verification.Verdict{GateResult: verification.GatePass, Significant: true, HeldOut: true,
			RegressionPass: true, Delta: evalstats.Interval{Mean: delta, Low: delta - 0.05, High: delta + 0.05},
			CostDelta: -0.001, LatencyDelta: 25},
		Metrics: CandidateMetrics{Providers: providers, Quality: 0.9, LatencyMS: 500,
			Composite: evalstats.Interval{Mean: compositeLow + 0.05, Low: compositeLow, High: compositeLow + 0.1}}}
}

func m9Controller(search Search, v Verifier, repo Repo, ledger ChangeLedger, kill *KillSwitch) *Controller {
	return &Controller{Search: search, Verifier: v, Repo: repo, Ledger: ledger, Kill: kill,
		Clock: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }}
}

func m9Auth(armed bool) Authority {
	return Authority{RunID: "m9", WorkflowID: "wf", Actor: "damon", WeightProfile: "balanced",
		Constraints: Constraints{BudgetCeilingUSD: 1.0, ProviderAllowlist: []string{"anthropic"},
			MinImprovement: 0.03, MaxIterations: 10, StallK: 3},
		KillSwitchArmed: armed, AuditArmed: armed, RollbackArmed: armed}
}

// TestM9ExitChecklist confirms the headline M9 acceptance criteria (PRD §13) in one end-to-end run over
// the two-defect fixture, plus the dark/dry-run default.
func TestM9ExitChecklist(t *testing.T) {
	ctx := context.Background()

	// ── Analyze → propose → verify → apply, diagnosis-guided, composite objective, gates hard. ──
	search, verifier, targets, cands := twoDefectFixture()
	repo := NewFakeRepo([]byte(`{"baseline":true}`))
	ledger := NewMemLedger()
	c := m9Controller(search, verifier, repo, ledger, NewKillSwitch())
	in := RunInput{Authority: m9Auth(true), Targets: targets, BaselineSpecBytes: []byte(`{"baseline":true}`),
		EvalSetCaseIDs: []string{"c1", "c2", "c3", "c4"}}
	res, err := c.Run(ctx, in)
	if err != nil {
		t.Fatal(err)
	}

	// Both real defects merged; the off-allowlist high-scorer never did (composite objective + P4 gate).
	if len(res.Merges) != 2 {
		t.Fatalf("expected the two diagnosable defects to merge, got %d", len(res.Merges))
	}
	illegal := cands[2]
	for _, m := range res.Merges {
		if m.ToConfigHash == illegal.ConfigHash {
			t.Fatal("the higher-composite gate-failing candidate was merged")
		}
		if m.DiagnosisID == "" {
			t.Fatal("a merged change lacks its motivating diagnosis")
		}
		if m.MergeCommit == "" {
			t.Fatal("a merged change lacks its git merge commit (auditability)")
		}
	}
	// Diagnosis-guided: the first considered candidate is at an attributed node.
	events := ledger.Events("m9")
	firstConsider := ""
	for _, ev := range events {
		if ev.Type == EventConsider {
			firstConsider = ev.DiagnosisID
			break
		}
	}
	if firstConsider != "diag-reason" && firstConsider != "diag-rag" {
		t.Fatalf("first considered candidate is not at an attributed node: %q", firstConsider)
	}
	// Write-ahead audit: every merge has a preceding apply event with the merge commit backfilled.
	for _, m := range res.Merges {
		ok := false
		for _, ev := range events {
			if ev.Type == EventApply && ev.ToConfigHash == m.ToConfigHash && ev.MergeCommit == m.MergeCommit {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("merge %s has no write-ahead apply event", m.ToConfigHash)
		}
	}

	// ── Rollback via git revert to the byte-identical prior spec, audited. ──
	first := res.Merges[0]
	_, live, err := c.Rollback(ctx, "m9", first, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if live != first.FromConfigHash {
		t.Fatalf("rollback did not restore the prior spec: %s != %s", live, first.FromConfigHash)
	}
	if !hasEvent(ledger.Events("m9"), EventRevert) {
		t.Fatal("the revert was not audited")
	}

	// ── Ships DARK / dry-run-only: with a prerequisite missing, the loop merges nothing. ──
	search2, verifier2, targets2, _ := twoDefectFixture()
	repo2 := NewFakeRepo([]byte(`{"baseline":true}`))
	c2 := m9Controller(search2, verifier2, repo2, NewMemLedger(), NewKillSwitch())
	auth2 := m9Auth(true)
	auth2.RollbackArmed = false // one prerequisite absent → dry-run
	res2, _ := c2.Run(ctx, RunInput{Authority: auth2, Targets: targets2,
		BaselineSpecBytes: []byte(`{"baseline":true}`), EvalSetCaseIDs: []string{"c1", "c2", "c3", "c4"}})
	if len(res2.Merges) != 0 {
		t.Fatalf("dry-run must merge zero changes, got %d", len(res2.Merges))
	}
	if res2.MergeEnabled {
		t.Fatal("dry-run must report merge disabled")
	}
	head2, _ := repo2.Head(ctx)
	if head2 != ContentHash([]byte(`{"baseline":true}`)) {
		t.Fatal("dry-run must leave the baseline spec live")
	}
}
