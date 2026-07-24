package optimizer

import (
	"context"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/verification"
)

// ── test fixtures ───────────────────────────────────────────────────────────────────────────────────

const testBaseline = `{"variant":"baseline"}`

// mkCand builds a candidate whose config hash is the content hash of its spec bytes, so the fake repo's
// merge (which hashes the spec) and the static verifier's lookup agree.
func mkCand(spec, diag, node string, expGain float64) SearchCandidate {
	b := []byte(spec)
	return SearchCandidate{
		DiagnosisID: diag, Node: node, Dimension: "model", Operator: "model_upgrade",
		ConfigHash: ContentHash(b), SpecBytes: b, ExpectedGain: expGain, Providers: []string{"anthropic"},
	}
}

// goodResult is a verified, gate-passing candidate with a strong composite (a real merge).
func goodResult(compositeLow, delta float64) VerifyResult {
	return VerifyResult{
		ContractOK: true, Builds: true,
		Verdict: verification.Verdict{GateResult: verification.GatePass, Significant: true, HeldOut: true,
			RegressionPass: true, Delta: evalstats.Interval{Mean: delta, Low: delta - 0.05, High: delta + 0.05}},
		Metrics: CandidateMetrics{Providers: []string{"anthropic"}, Quality: 0.9, LatencyMS: 500,
			Composite: evalstats.Interval{Mean: compositeLow + 0.05, Low: compositeLow, High: compositeLow + 0.1}},
	}
}

// gateFailResult is verified (real held-out gain) but calls a provider off the allowlist — the P4 gate
// disqualifies it however high its composite (design Decision 1).
func gateFailResult(compositeLow float64) VerifyResult {
	r := goodResult(compositeLow, 0.5)
	r.Metrics.Providers = []string{"openai"} // off the allowlist
	return r
}

func testEnum(cands ...SearchCandidate) Enumerator {
	return fakeEnum{byNode: map[string][]SearchCandidate{"node3": cands}}
}

func testAuthority(armed bool) Authority {
	return Authority{
		RunID: "run-1", WorkflowID: "wf-1", Actor: "damon", WeightProfile: "balanced",
		Constraints: Constraints{
			BudgetCeilingUSD: 100, ProviderAllowlist: []string{"anthropic"}, MinImprovement: 0.02,
			MaxIterations: 20, StallK: 3,
		},
		KillSwitchArmed: armed, AuditArmed: armed, RollbackArmed: armed,
	}
}

func newController(v Verifier, repo Repo, ledger ChangeLedger, kill *KillSwitch, cands ...SearchCandidate) *Controller {
	return &Controller{
		Search:   Search{Enum: testEnum(cands...)},
		Verifier: v,
		Repo:     repo,
		Ledger:   ledger,
		Kill:     kill,
		Clock:    func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
}

func baseInput(auth Authority) RunInput {
	return RunInput{Authority: auth, Targets: []Target{{DiagnosisID: "d3", Node: "node3", Dimension: "model", Priority: 1}},
		BaselineSpecBytes: []byte(testBaseline), EvalSetCaseIDs: []string{"c1", "c2", "c3"}}
}

// ── Section 2 / 8.3: verification decides; a gate-failing high-scorer is never merged ──────────────

func TestLoop_GateFailingHighScorerNotMerged(t *testing.T) {
	high := mkCand(`{"v":"illegal-high"}`, "d3", "node3", 0.9) // highest expected gain, but gate-failing
	legal := mkCand(`{"v":"legal"}`, "d3", "node3", 0.3)
	verifier := StaticVerifier{ByConfig: map[string]VerifyResult{
		high.ConfigHash:  gateFailResult(0.95), // composite 0.95 but provider off the allowlist
		legal.ConfigHash: goodResult(0.70, 0.4),
	}}
	repo := NewFakeRepo([]byte(testBaseline))
	ledger := NewMemLedger()
	c := newController(verifier, repo, ledger, NewKillSwitch(), high, legal)

	res, err := c.Run(context.Background(), baseInput(testAuthority(true)))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Merges {
		if m.ToConfigHash == high.ConfigHash {
			t.Fatal("a gate-failing candidate was merged despite the highest composite score")
		}
	}
	if len(res.Merges) != 1 || res.Merges[0].ToConfigHash != legal.ConfigHash {
		t.Fatalf("expected exactly the lower-scoring gate-passing candidate to merge, got %d merges", len(res.Merges))
	}
}

func TestLoop_UnverifiedNotMerged(t *testing.T) {
	tie := mkCand(`{"v":"tie"}`, "d3", "node3", 0.5)
	nonbuild := mkCand(`{"v":"nonbuild"}`, "d3", "node3", 0.4)
	verifier := StaticVerifier{ByConfig: map[string]VerifyResult{
		tie.ConfigHash: {ContractOK: true, Builds: true, Verdict: verification.Verdict{GateResult: verification.GateFailSig}},
		nonbuild.ConfigHash: {ContractOK: true, Builds: false, // build failed → never verified, never merged
			BuildLog: "syntax error"},
	}}
	c := newController(verifier, NewFakeRepo([]byte(testBaseline)), NewMemLedger(), NewKillSwitch(), tie, nonbuild)
	res, _ := c.Run(context.Background(), baseInput(testAuthority(true)))
	if len(res.Merges) != 0 {
		t.Fatalf("no unverified or non-building candidate may merge, got %d merges", len(res.Merges))
	}
	if res.State != StateStalled {
		t.Fatalf("expected stalled (no progress), got %s", res.State)
	}
}

// Section 2.3: a candidate that violates the P5 typed I/O contract is rejected before verification and
// never merged.
func TestLoop_ContractViolationNotMerged(t *testing.T) {
	bad := mkCand(`{"v":"incoherent"}`, "d3", "node3", 0.5)
	verifier := StaticVerifier{ByConfig: map[string]VerifyResult{
		bad.ConfigHash: {ContractOK: false, ContractReason: "incoherent re-arrangement: node ordering breaks typed I/O"},
	}}
	c := newController(verifier, NewFakeRepo([]byte(testBaseline)), NewMemLedger(), NewKillSwitch(), bad)
	res, _ := c.Run(context.Background(), baseInput(testAuthority(true)))
	if len(res.Merges) != 0 {
		t.Fatalf("a contract-violating candidate must not merge, got %d", len(res.Merges))
	}
}

// Section 3.1: reaching max iterations stops the loop with state max_iter.
func TestLoop_MaxIterStop(t *testing.T) {
	// Many gate-failing candidates so the loop keeps iterating; cap iterations at 2.
	var cands []SearchCandidate
	byConfig := map[string]VerifyResult{}
	for _, v := range []string{"a", "b", "c", "d", "e", "f"} {
		cd := mkCand(`{"v":"`+v+`"}`, "d3", "node3", 0.5)
		cands = append(cands, cd)
		byConfig[cd.ConfigHash] = gateFailResult(0.9)
	}
	auth := testAuthority(true)
	auth.Constraints.MaxIterations = 2
	auth.Constraints.StallK = 100 // don't stall first
	c := newController(StaticVerifier{ByConfig: byConfig}, NewFakeRepo([]byte(testBaseline)), NewMemLedger(), NewKillSwitch(), cands...)
	res, _ := c.Run(context.Background(), baseInput(auth))
	if res.State != StateMaxIter {
		t.Fatalf("expected max_iter, got %s", res.State)
	}
	if len(res.Iterations) != 2 {
		t.Fatalf("expected exactly 2 iterations, got %d", len(res.Iterations))
	}
}

// Section 6.4: Run itself enforces one active run per workflow via the shared lock.
func TestLoop_RunHoldsWorkflowLock(t *testing.T) {
	locks := NewLockSet()
	if err := locks.Acquire("wf-1", "other-run"); err != nil { // someone else holds it
		t.Fatal(err)
	}
	cand := mkCand(`{"v":"good"}`, "d3", "node3", 0.5)
	verifier := StaticVerifier{ByConfig: map[string]VerifyResult{cand.ConfigHash: goodResult(0.8, 0.5)}}
	c := newController(verifier, NewFakeRepo([]byte(testBaseline)), NewMemLedger(), NewKillSwitch(), cand)
	c.Locks = locks
	_, err := c.Run(context.Background(), baseInput(testAuthority(true)))
	if _, ok := err.(ErrWorkflowLocked); !ok {
		t.Fatalf("expected a workflow-locked error when another run is active, got %v", err)
	}
}

// ── Section 4.1 / 8.2: a missing prerequisite disables merge entirely (dry-run, zero merges) ───────

func TestLoop_PrerequisiteGate_DryRunZeroMerges(t *testing.T) {
	for _, missing := range []string{"kill_switch", "audit_trail", "rollback"} {
		auth := testAuthority(true)
		switch missing {
		case "kill_switch":
			auth.KillSwitchArmed = false
		case "audit_trail":
			auth.AuditArmed = false
		case "rollback":
			auth.RollbackArmed = false
		}
		cand := mkCand(`{"v":"good"}`, "d3", "node3", 0.5)
		verifier := StaticVerifier{ByConfig: map[string]VerifyResult{cand.ConfigHash: goodResult(0.8, 0.5)}}
		repo := NewFakeRepo([]byte(testBaseline))
		c := newController(verifier, repo, NewMemLedger(), NewKillSwitch(), cand)

		res, _ := c.Run(context.Background(), baseInput(auth))
		if len(res.Merges) != 0 {
			t.Errorf("missing %s: expected zero merges (dry-run), got %d", missing, len(res.Merges))
		}
		if res.MergeEnabled {
			t.Errorf("missing %s: merge must be disabled", missing)
		}
		head, _ := repo.Head(context.Background())
		if head != ContentHash([]byte(testBaseline)) {
			t.Errorf("missing %s: last-good spec must stay live, head changed", missing)
		}
	}
}

// ── Section 4.4 / 8.2: write-ahead — every merge is preceded by its ledger apply event ─────────────

func TestLoop_WriteAheadAudit(t *testing.T) {
	cand := mkCand(`{"v":"good"}`, "d3", "node3", 0.5)
	verifier := StaticVerifier{ByConfig: map[string]VerifyResult{cand.ConfigHash: goodResult(0.8, 0.5)}}
	ledger := NewMemLedger()
	c := newController(verifier, NewFakeRepo([]byte(testBaseline)), ledger, NewKillSwitch(), cand)

	res, _ := c.Run(context.Background(), baseInput(testAuthority(true)))
	if len(res.Merges) != 1 {
		t.Fatalf("expected 1 merge, got %d", len(res.Merges))
	}
	// Every merge must have an apply event, and that event's merge-commit is the one that landed
	// (backfilled after the merge succeeded — the write-ahead event existed before the merge).
	events := ledger.Events("run-1")
	for _, m := range res.Merges {
		found := false
		for _, ev := range events {
			if ev.Type == EventApply && ev.ToConfigHash == m.ToConfigHash {
				found = true
				if ev.Seq != m.LedgerSeq {
					t.Errorf("merge %s apply-event seq %d != recorded %d", m.ToConfigHash, ev.Seq, m.LedgerSeq)
				}
				if ev.MergeCommit != m.MergeCommit {
					t.Errorf("merge %s apply-event commit %q != merge commit %q", m.ToConfigHash, ev.MergeCommit, m.MergeCommit)
				}
			}
		}
		if !found {
			t.Errorf("merge %s has no preceding apply event — an unaudited merge", m.ToConfigHash)
		}
	}
}

// ── Section 4.2 / 8.6: kill switch mid-iteration — no merge after stop, last-good live ─────────────

// killingVerifier fires the kill switch during verification, so the loop's post-verify kill check
// discards the in-flight result rather than merging it.
type killingVerifier struct {
	kill  *KillSwitch
	inner Verifier
}

func (k killingVerifier) Verify(ctx context.Context, req VerifyRequest) (VerifyResult, error) {
	k.kill.Fire("user")
	return k.inner.Verify(ctx, req)
}

func TestLoop_KillSwitchMidIteration(t *testing.T) {
	cand := mkCand(`{"v":"good"}`, "d3", "node3", 0.5)
	inner := StaticVerifier{ByConfig: map[string]VerifyResult{cand.ConfigHash: goodResult(0.8, 0.5)}}
	kill := NewKillSwitch()
	repo := NewFakeRepo([]byte(testBaseline))
	ledger := NewMemLedger()
	c := newController(killingVerifier{kill: kill, inner: inner}, repo, ledger, kill, cand)

	res, _ := c.Run(context.Background(), baseInput(testAuthority(true)))
	if len(res.Merges) != 0 {
		t.Fatalf("no candidate may merge after the kill switch fires, got %d", len(res.Merges))
	}
	if res.State != StateStopped {
		t.Fatalf("expected stopped, got %s", res.State)
	}
	head, _ := repo.Head(context.Background())
	if head != ContentHash([]byte(testBaseline)) {
		t.Fatal("last-good spec must stay live after a kill-switch stop")
	}
	// The stop is recorded in the audit trail.
	if !hasEvent(ledger.Events("run-1"), EventStop) {
		t.Fatal("the kill-switch stop was not recorded in the change ledger")
	}
}

// ── Section 3.2 / 8.4: min-improvement stop → converged ────────────────────────────────────────────

func TestLoop_MinImprovementStop(t *testing.T) {
	strong := mkCand(`{"v":"strong"}`, "d3", "node3", 0.9)
	marginal := mkCand(`{"v":"marginal"}`, "d3", "node3", 0.5)
	verifier := StaticVerifier{ByConfig: map[string]VerifyResult{
		strong.ConfigHash:   goodResult(0.75, 0.4),  // first merge sets best composite low = 0.75
		marginal.ConfigHash: goodResult(0.77, 0.05), // marginal gain 0.02 == threshold boundary; below → stop
	}}
	auth := testAuthority(true)
	auth.Constraints.MinImprovement = 0.05 // marginal gain 0.77-0.75=0.02 < 0.05 → converged
	c := newController(verifier, NewFakeRepo([]byte(testBaseline)), NewMemLedger(), NewKillSwitch(), strong, marginal)

	res, _ := c.Run(context.Background(), baseInput(auth))
	if res.State != StateConverged {
		t.Fatalf("expected converged on sub-threshold gain, got %s", res.State)
	}
	if len(res.Merges) != 1 {
		t.Fatalf("expected exactly the strong candidate to merge before convergence, got %d", len(res.Merges))
	}
}

// ── Section 3.3 / 8.4: stall detection → stalled, no infinite loop ─────────────────────────────────

func TestLoop_StallDetection(t *testing.T) {
	// Three candidates that all verify but fail the P4 provider gate → no progress → stalled at K=3.
	var cands []SearchCandidate
	byConfig := map[string]VerifyResult{}
	for _, v := range []string{"a", "b", "c", "d", "e"} {
		cd := mkCand(`{"v":"`+v+`"}`, "d3", "node3", 0.5)
		cands = append(cands, cd)
		byConfig[cd.ConfigHash] = gateFailResult(0.9)
	}
	c := newController(StaticVerifier{ByConfig: byConfig}, NewFakeRepo([]byte(testBaseline)), NewMemLedger(), NewKillSwitch(), cands...)
	res, _ := c.Run(context.Background(), baseInput(testAuthority(true)))
	if res.State != StateStalled {
		t.Fatalf("expected stalled, got %s", res.State)
	}
	if len(res.Merges) != 0 {
		t.Fatalf("a stalled run merges nothing, got %d", len(res.Merges))
	}
	if len(res.Iterations) != 3 { // stopped at K=3, did not run all 5 → no infinite loop
		t.Fatalf("expected 3 iterations before stall, got %d", len(res.Iterations))
	}
}

// ── Section 3.4: recovery — a failed merge leaves the last-good spec live ──────────────────────────

func TestLoop_MergeFailureRecovery(t *testing.T) {
	cand := mkCand(`{"v":"good"}`, "d3", "node3", 0.5)
	verifier := StaticVerifier{ByConfig: map[string]VerifyResult{cand.ConfigHash: goodResult(0.8, 0.5)}}
	repo := NewFakeRepo([]byte(testBaseline))
	repo.FailMerge = true // every merge fails
	c := newController(verifier, repo, NewMemLedger(), NewKillSwitch(), cand)

	res, _ := c.Run(context.Background(), baseInput(testAuthority(true)))
	if len(res.Merges) != 0 {
		t.Fatalf("a failed merge must not count as merged, got %d", len(res.Merges))
	}
	head, _ := repo.Head(context.Background())
	if head != ContentHash([]byte(testBaseline)) {
		t.Fatal("last-good spec must stay live after a failed merge")
	}
	if res.State.IsHalt() {
		t.Fatalf("a merge failure is a recovery, not a halt; got %s", res.State)
	}
}

// ── Section 5.2 / 8.4: budget halt mid-run → halted_budget, disarm, no further merge ───────────────

func TestLoop_BudgetHalt(t *testing.T) {
	first := mkCand(`{"v":"first"}`, "d3", "node3", 0.9)
	second := mkCand(`{"v":"second"}`, "d3", "node3", 0.5)
	r1 := goodResult(0.75, 0.4)
	r1.SpendUSD = 0.06
	r2 := goodResult(0.80, 0.4)
	r2.SpendUSD = 0.06
	verifier := StaticVerifier{ByConfig: map[string]VerifyResult{first.ConfigHash: r1, second.ConfigHash: r2}}
	auth := testAuthority(true)
	auth.Constraints.BudgetCeilingUSD = 0.10 // first (0.06) merges; second pushes to 0.12 → halt before merge
	c := newController(verifier, NewFakeRepo([]byte(testBaseline)), NewMemLedger(), NewKillSwitch(), first, second)

	res, _ := c.Run(context.Background(), baseInput(auth))
	if res.State != StateHaltedBudget {
		t.Fatalf("expected halted_budget, got %s", res.State)
	}
	if res.MergeEnabled {
		t.Fatal("a budget halt must disarm the merge step")
	}
	if len(res.Merges) != 1 {
		t.Fatalf("only the pre-ceiling candidate should merge, got %d", len(res.Merges))
	}
}

// ── Section 5.3 / 8.6: regression halt → halted_regression, disarm ─────────────────────────────────

type regressAfterFirst struct{ merges int }

func (r *regressAfterFirst) Check(context.Context, AppliedChange) (bool, string) {
	r.merges++
	return r.merges >= 1, "cluster X success dropped 0.12 vs current best"
}

func TestLoop_RegressionHalt(t *testing.T) {
	first := mkCand(`{"v":"first"}`, "d3", "node3", 0.9)
	second := mkCand(`{"v":"second"}`, "d3", "node3", 0.5)
	verifier := StaticVerifier{ByConfig: map[string]VerifyResult{
		first.ConfigHash: goodResult(0.75, 0.4), second.ConfigHash: goodResult(0.85, 0.4)}}
	c := newController(verifier, NewFakeRepo([]byte(testBaseline)), NewMemLedger(), NewKillSwitch(), first, second)
	c.Regression = &regressAfterFirst{}

	res, _ := c.Run(context.Background(), baseInput(testAuthority(true)))
	if res.State != StateHaltedRegression {
		t.Fatalf("expected halted_regression, got %s", res.State)
	}
	if res.MergeEnabled {
		t.Fatal("a regression halt must disarm merge")
	}
	if len(res.Merges) != 1 {
		t.Fatalf("the loop must stop merging after the regression, got %d merges", len(res.Merges))
	}
}

// ── Section 5.4: disarm-until-re-arm ────────────────────────────────────────────────────────────────

func TestLoop_DisarmUntilRearm(t *testing.T) {
	ledger := NewMemLedger()
	c := &Controller{Ledger: ledger, Clock: func() time.Time { return time.Unix(1, 0) }}
	auth := testAuthority(true)
	rearmed, err := c.Rearm("run-1", auth, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if !rearmed.MergeArmed() {
		t.Fatal("re-arm should re-arm the merge prerequisites")
	}
	if !hasEvent(ledger.Events("run-1"), EventRearm) {
		t.Fatal("re-arm must be an audited action")
	}
}

// ── Section 6.3 / 8.7: fail-closed — ledger down or verification down → stop merging ───────────────

// applyDownLedger is up for grant/consider/verify but fails the apply write — the change-ledger store
// going down at the moment a merge would occur (spec Requirement).
type applyDownLedger struct{ *MemLedger }

func (l applyDownLedger) Append(ev LedgerEvent) (int, error) {
	if ev.Type == EventApply {
		return 0, ErrLedgerUnavailable
	}
	return l.MemLedger.Append(ev)
}

func TestLoop_FailClosed_LedgerDown(t *testing.T) {
	cand := mkCand(`{"v":"good"}`, "d3", "node3", 0.5)
	verifier := StaticVerifier{ByConfig: map[string]VerifyResult{cand.ConfigHash: goodResult(0.8, 0.5)}}
	repo := NewFakeRepo([]byte(testBaseline))
	c := newController(verifier, repo, applyDownLedger{NewMemLedger()}, NewKillSwitch(), cand)

	res, _ := c.Run(context.Background(), baseInput(testAuthority(true)))
	if len(res.Merges) != 0 {
		t.Fatalf("a downed write-ahead ledger must prevent the merge, got %d", len(res.Merges))
	}
	head, _ := repo.Head(context.Background())
	if head != ContentHash([]byte(testBaseline)) {
		t.Fatal("last-good spec must stay live when the ledger is down")
	}
}

func TestLoop_FailClosed_VerificationDown(t *testing.T) {
	cand := mkCand(`{"v":"good"}`, "d3", "node3", 0.5)
	verifier := StaticVerifier{
		ByConfig: map[string]VerifyResult{},
		Err:      map[string]error{cand.ConfigHash: context.DeadlineExceeded},
	}
	repo := NewFakeRepo([]byte(testBaseline))
	c := newController(verifier, repo, NewMemLedger(), NewKillSwitch(), cand)
	res, _ := c.Run(context.Background(), baseInput(testAuthority(true)))
	if len(res.Merges) != 0 || res.State != StateStopped {
		t.Fatalf("verification-down must stop merging (state=%s, merges=%d)", res.State, len(res.Merges))
	}
}

// ── Section 6.4: one active run per workflow ──────────────────────────────────────────────────────

func TestLoop_ConcurrencyLock(t *testing.T) {
	locks := NewLockSet()
	if err := locks.Acquire("wf-1", "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := locks.Acquire("wf-1", "run-2"); err == nil {
		t.Fatal("a second run against the same workflow must be rejected")
	}
	locks.Release("wf-1", "run-1")
	if err := locks.Acquire("wf-1", "run-2"); err != nil {
		t.Fatalf("after release, a new run should acquire the lock: %v", err)
	}
}

// ── Section 4.5: rollback via revert (fake repo) restores the byte-identical prior spec ────────────

func TestLoop_RollbackFakeRepo(t *testing.T) {
	cand := mkCand(`{"v":"good"}`, "d3", "node3", 0.5)
	verifier := StaticVerifier{ByConfig: map[string]VerifyResult{cand.ConfigHash: goodResult(0.8, 0.5)}}
	repo := NewFakeRepo([]byte(testBaseline))
	ledger := NewMemLedger()
	c := newController(verifier, repo, ledger, NewKillSwitch(), cand)

	res, _ := c.Run(context.Background(), baseInput(testAuthority(true)))
	if len(res.Merges) != 1 {
		t.Fatalf("expected 1 merge, got %d", len(res.Merges))
	}
	applied := res.Merges[0]
	_, live, err := c.Rollback(context.Background(), "run-1", applied, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if live != applied.FromConfigHash {
		t.Fatalf("rollback did not restore the prior spec: live %s, want %s", live, applied.FromConfigHash)
	}
	if live != ContentHash([]byte(testBaseline)) {
		t.Fatal("rollback should restore the byte-identical baseline")
	}
	if !hasEvent(ledger.Events("run-1"), EventRevert) {
		t.Fatal("the revert must be recorded in the change ledger")
	}
}

func hasEvent(events []LedgerEvent, t EventType) bool {
	for _, ev := range events {
		if ev.Type == t {
			return true
		}
	}
	return false
}
