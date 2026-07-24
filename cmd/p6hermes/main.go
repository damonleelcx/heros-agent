// Command p6hermes drives the whole P6 autonomous-optimizer loop against a REAL repository
// (github.com/nousresearch/hermes-agent): the controller opens+merges pull requests into the checkout
// with real git, writes a write-ahead change ledger, halts/stops per the hard constraints, and reverts
// any merged change with a real `git revert` — all on the actual repo the loop owns.
//
// What is REAL here: the target repository, the diagnosis-guided candidates' node ids / files / symbols
// (real discovered call sites in the hermes source), and every git operation — the branch, the merge
// commit, the audit trail, and the revert. What is STUBBED — and labelled as such, exactly as
// cmd/p55hermes stubs it — is the DIAGNOSIS input (in production from the P4.5 attribution engine) and
// the VERIFICATION deltas (in production from real eval runs through a provider); here the verification
// gate and the whole controller run for real over canned deltas, so the loop logic is the shipped path.
//
// The loop's live spec is a canonical Variant Spec file (variant_spec.json) the merge lands in the
// checkout; a `git revert` of a merge commit restores the byte-identical prior spec, matching its
// config_hash — the reversibility the DevOps directive requires.
//
//	go run ./cmd/p6hermes -repo /path/to/hermes-agent [-addr 127.0.0.1:8499] [-headless]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/optimizer"
	"github.com/heros-foreal/agentd/internal/verification"
)

const specPath = "variant_spec.json"

// hermesNode is a REAL discovered node in the hermes source, with the diagnosis-guided change the loop
// proposes for it and the canned verification outcome the (real) gate then judges.
type hermesNode struct {
	nodeID, symbol, file string
	diag, operator, dim  string
	from, to             string
	providers            []string
	compositeLow, delta  float64
	tryOrder, spend      float64
}

// hermesTargets covers real hermes call sites across the operator catalog. The composite/delta values
// are the stubbed verification outcome; the node ids, symbols, and files are real.
func hermesTargets() []hermesNode {
	return []hermesNode{
		{nodeID: "node:auxiliary_client:async_call_llm", symbol: "async_call_llm", file: "agent/auxiliary_client.py",
			diag: "diag-model-mismatch", operator: "model_upgrade", dim: "model", from: "gpt-4o-mini", to: "gpt-5",
			providers: []string{"openai"}, compositeLow: 0.71, delta: 0.38, tryOrder: 0.9, spend: 0.09},
		{nodeID: "node:trajectory_compressor:_generate_summary", symbol: "_generate_summary", file: "trajectory_compressor.py",
			diag: "diag-prompt-drift", operator: "prompt_rewrite", dim: "prompt", from: "summary@v1", to: "summary@v2+schema",
			providers: []string{"openai"}, compositeLow: 0.79, delta: 0.19, tryOrder: 0.8, spend: 0.08},
		// Highest composite (0.94) but proposes an off-allowlist provider → the P4 gate blocks the merge.
		{nodeID: "node:chat_completion_helpers:handle_max_iterations", symbol: "handle_max_iterations", file: "agent/chat_completion_helpers.py",
			diag: "diag-context-overflow", operator: "context_policy_switch", dim: "context", from: "full-window", to: "summarization",
			providers: []string{"cohere"}, compositeLow: 0.94, delta: 0.31, tryOrder: 0.7, spend: 0.05},
		// Marginal gain below min-improvement → converged.
		{nodeID: "node:auxiliary_client:call_llm", symbol: "call_llm", file: "agent/auxiliary_client.py",
			diag: "diag-tool-schema", operator: "add_skill", dim: "skills", from: "N tools", to: "N+1 (+web_search)",
			providers: []string{"openai"}, compositeLow: 0.805, delta: 0.05, tryOrder: 0.6, spend: 0.06},
	}
}

// variantSpec is the canonical live-spec document the loop merges. A candidate is the baseline with one
// node's dimension changed — content-hashed like any Variant Spec, so a git revert restores it exactly.
func variantSpec(base map[string]any, node hermesNode) []byte {
	spec := map[string]any{}
	for k, v := range base {
		spec[k] = v
	}
	spec[node.nodeID] = map[string]any{"dimension": node.dim, "value": node.to, "symbol": node.symbol, "file": node.file}
	b, _ := json.MarshalIndent(spec, "", "  ")
	return b
}

func baselineSpec() ([]byte, map[string]any) {
	base := map[string]any{
		"workflow":                                           "nousresearch/hermes-agent",
		"node:auxiliary_client:async_call_llm":               map[string]any{"dimension": "model", "value": "gpt-4o-mini"},
		"node:trajectory_compressor:_generate_summary":       map[string]any{"dimension": "prompt", "value": "summary@v1"},
		"node:chat_completion_helpers:handle_max_iterations": map[string]any{"dimension": "context", "value": "full-window"},
		"node:auxiliary_client:call_llm":                     map[string]any{"dimension": "skills", "value": "N tools"},
	}
	b, _ := json.MarshalIndent(base, "", "  ")
	return b, base
}

// slowVerifier delays each verification so a human watching the monitor sees the loop advance.
type slowVerifier struct {
	inner optimizer.Verifier
	delay time.Duration
}

func (s slowVerifier) Verify(ctx context.Context, req optimizer.VerifyRequest) (optimizer.VerifyResult, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
	}
	return s.inner.Verify(ctx, req)
}

type source struct {
	mu         sync.Mutex
	dir        string
	ledger     *optimizer.MemLedger
	kill       *optimizer.KillSwitch
	ctrl       *optimizer.Controller
	started    bool
	snapshot   optimizer.RunResult
	rolledBack map[string]string
	granted    optimizer.Authority
	delay      time.Duration
}

func (s *source) Monitor(string) (api.Monitor, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return api.Monitor{}, false
	}
	return s.buildMonitor(), true
}

func (s *source) Grant(req api.GrantRequest) (api.Monitor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return s.buildMonitor(), nil
	}
	auth := optimizer.Authority{RunID: req.RunID, WorkflowID: req.WorkflowID, Actor: req.Actor, WeightProfile: "balanced",
		Constraints: optimizer.Constraints{BudgetCeilingUSD: req.BudgetCeilingUSD, ProviderAllowlist: req.ProviderAllowlist,
			MinImprovement: req.MinImprovement, MaxIterations: req.MaxIterations, StallK: 3},
		KillSwitchArmed: true, AuditArmed: true, RollbackArmed: true, GrantedAt: time.Unix(1_700_000_000, 0)}
	s.granted, s.started = auth, true

	base, baseMap := baselineSpec()
	nodes := hermesTargets()
	byConfig := map[string]optimizer.VerifyResult{}
	enum := candEnum{byNode: map[string][]optimizer.SearchCandidate{}}
	for _, n := range nodes {
		spec := variantSpec(baseMap, n)
		hash := optimizer.ContentHash(spec)
		byConfig[hash] = optimizer.VerifyResult{ContractOK: true, Builds: true, SpendUSD: n.spend,
			Verdict: verification.Verdict{GateResult: verification.GatePass, Significant: true, HeldOut: true,
				RegressionPass: true, Delta: evalstats.Interval{Mean: n.delta, Low: n.delta - 0.05, High: n.delta + 0.05},
				CostDelta: -0.003, LatencyDelta: 55},
			Metrics: optimizer.CandidateMetrics{Providers: n.providers, Quality: 0.9, LatencyMS: 540,
				Composite: evalstats.Interval{Mean: n.compositeLow + 0.05, Low: n.compositeLow, High: n.compositeLow + 0.1}}}
		enum.byNode[n.nodeID] = append(enum.byNode[n.nodeID], optimizer.SearchCandidate{
			DiagnosisID: n.diag, Node: n.nodeID, Dimension: n.dim, Operator: n.operator,
			ConfigHash: hash, SpecBytes: spec, Providers: n.providers, ExpectedGain: n.tryOrder,
			Rationale: fmt.Sprintf("%s at %s (%s): %s → %s", n.operator, n.symbol, n.file, n.from, n.to)})
	}
	var targets []optimizer.Target
	for i, n := range nodes {
		targets = append(targets, optimizer.Target{DiagnosisID: n.diag, Node: n.nodeID, Dimension: n.dim,
			Priority: 1.0 - 0.1*float64(i)})
	}

	s.ctrl = &optimizer.Controller{
		Search:   optimizer.Search{Enum: enum},
		Verifier: slowVerifier{inner: optimizer.StaticVerifier{ByConfig: byConfig}, delay: s.delay},
		Repo:     optimizer.GitRepo{Dir: s.dir, SpecPath: specPath, Branch: "main"},
		Ledger:   s.ledger, Kill: s.kill, Clock: func() time.Time { return time.Now().UTC() },
		OnIteration: func(res optimizer.RunResult) { s.mu.Lock(); s.snapshot = res; s.mu.Unlock() },
	}
	in := optimizer.RunInput{Authority: auth, Targets: targets, BaselineSpecBytes: base,
		EvalSetCaseIDs: []string{"h1", "h2", "h3", "h4", "h5", "h6"}}
	go func() {
		res, err := s.ctrl.Run(context.Background(), in)
		if err != nil {
			log.Printf("run error: %v", err)
		}
		s.mu.Lock()
		s.snapshot = res
		s.mu.Unlock()
	}()
	return s.buildMonitor(), nil
}

func (s *source) Stop(runID, actor string) (api.Monitor, error) {
	s.kill.Fire(actor)
	time.Sleep(60 * time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildMonitor(), nil
}

func (s *source) Rearm(runID, actor string) (api.Monitor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	auth, err := s.ctrl.Rearm(runID, s.granted, actor)
	if err != nil {
		return api.Monitor{}, err
	}
	s.granted = auth
	s.snapshot.MergeEnabled = true
	return s.buildMonitor(), nil
}

func (s *source) Rollback(runID, mergeCommit, actor string) (api.Monitor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var applied optimizer.AppliedChange
	found := false
	for _, m := range s.snapshot.Merges {
		if m.MergeCommit == mergeCommit {
			applied, found = m, true
			break
		}
	}
	if !found {
		return api.Monitor{}, fmt.Errorf("no merged change with commit %s", mergeCommit)
	}
	revert, _, err := s.ctrl.Rollback(context.Background(), runID, applied, actor)
	if err != nil {
		return api.Monitor{}, err
	}
	s.rolledBack[mergeCommit] = revert
	return s.buildMonitor(), nil
}

func (s *source) buildMonitor() api.Monitor {
	res := s.snapshot
	cons := s.granted.Constraints
	m := api.Monitor{RunID: res.RunID, WorkflowID: res.WorkflowID, AutomationLevel: "autonomous",
		State: string(res.State), MergeEnabled: res.MergeEnabled, DisarmReason: res.DisarmReason,
		StopReason: res.StopReason, MissingPrereqs: s.granted.MissingPrereqs(), DryRun: !s.granted.MergeArmed(),
		BudgetCeilingUSD: cons.BudgetCeilingUSD, CumulativeSpendUSD: res.CumulativeSpend,
		MaxIterations: cons.MaxIterations, CurrentIteration: len(res.Iterations),
		MinImprovement: cons.MinImprovement, ProviderAllowlist: cons.ProviderAllowlist, PRsMerged: len(res.Merges)}
	if m.State == "" {
		m.State = "running"
	}
	for _, mg := range res.Merges {
		mv := api.MergeView{Idx: mg.Idx, FromConfigHash: mg.FromConfigHash, ToConfigHash: mg.ToConfigHash,
			MergeCommit: mg.MergeCommit, PRRef: mg.PRRef, DiagnosisID: mg.DiagnosisID, Operator: mg.Operator,
			Node: mg.Node, VerifiedDelta: mg.VerifiedDelta, CostDelta: mg.CostDelta, LatencyDelta: mg.LatencyDelta}
		if rev, ok := s.rolledBack[mg.MergeCommit]; ok {
			mv.RolledBack, mv.RevertCommit = true, rev
		}
		m.Merges = append(m.Merges, mv)
	}
	for _, ev := range s.ledger.Events(res.RunID) {
		m.Audit = append(m.Audit, api.AuditView{Seq: ev.Seq, Type: string(ev.Type), Summary: ev.Summary,
			MergeCommit: ev.MergeCommit, TS: ev.TS.Format(time.RFC3339)})
	}
	return m
}

type candEnum struct {
	byNode map[string][]optimizer.SearchCandidate
}

func (c candEnum) Enumerate(t optimizer.Target) []optimizer.SearchCandidate { return c.byNode[t.Node] }

// prepareRepo ensures the hermes checkout is a git repo with a committed baseline variant_spec.json on
// main, so the loop's merges have something to land on. It works on the checkout the loop OWNS — never
// an upstream tree — and only adds the spec file.
func prepareRepo(dir string, baseline []byte) error {
	git := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v: %s", args, err, out)
		}
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := git("init", "-q", "-b", "main"); err != nil {
			return err
		}
	}
	_ = git("config", "user.email", "seed@demo.local")
	_ = git("config", "user.name", "seed")
	if err := os.WriteFile(filepath.Join(dir, specPath), baseline, 0o644); err != nil {
		return err
	}
	if err := git("add", "--", specPath); err != nil {
		return err
	}
	// Commit only if the spec changed (idempotent on re-run).
	_ = git("commit", "-q", "-m", "optimizer: baseline variant spec")
	return nil
}

func main() {
	repo := flag.String("repo", ".", "path to the hermes-agent checkout the loop owns")
	addr := flag.String("addr", "127.0.0.1:8499", "listen address")
	headless := flag.Bool("headless", false, "run the loop once, print the audit trail, and exit (no server)")
	delayFlag := flag.Duration("delay", 900*time.Millisecond, "per-iteration verification delay (slow the loop to watch it merge live)")
	flag.Parse()

	baseline, _ := baselineSpec()
	if err := prepareRepo(*repo, baseline); err != nil {
		log.Fatalf("prepare hermes repo: %v", err)
	}
	delay := *delayFlag
	if *headless {
		delay = 0
	}
	src := &source{dir: *repo, ledger: optimizer.NewMemLedger(), kill: optimizer.NewKillSwitch(),
		rolledBack: map[string]string{}, delay: delay}

	if *headless {
		runHeadless(src)
		return
	}

	s := api.New(nil, config.Config{})
	s.MountP6(src)
	fmt.Printf("P6 on hermes-agent:  http://%s/p6/monitor?run=hermes&workflow=nousresearch/hermes-agent\n", *addr)
	fmt.Printf("target checkout:     %s\n", *repo)
	log.Fatal(http.ListenAndServe(*addr, s.Handler))
}

// runHeadless grants authority, runs the loop to completion, and prints the audit trail + git log — a
// non-interactive proof the loop merged real PRs into the real repo with a real audit trail.
func runHeadless(src *source) {
	_, _ = src.Grant(api.GrantRequest{RunID: "hermes", WorkflowID: "nousresearch/hermes-agent", Actor: "damon",
		BudgetCeilingUSD: 0.60, ProviderAllowlist: []string{"openai", "anthropic"}, MinImprovement: 0.03, MaxIterations: 10})
	// Wait for the background run to finish.
	for i := 0; i < 200; i++ {
		time.Sleep(50 * time.Millisecond)
		src.mu.Lock()
		done := src.snapshot.State != "" && src.snapshot.State != optimizer.StateRunning
		src.mu.Unlock()
		if done {
			break
		}
	}
	m, _ := src.Monitor("hermes")
	fmt.Printf("\n=== P6 run on nousresearch/hermes-agent ===\nstate=%s  merges=%d  spend=$%.4f/%.2f  iterations=%d\n\n",
		m.State, m.PRsMerged, m.CumulativeSpendUSD, m.BudgetCeilingUSD, m.CurrentIteration)
	fmt.Println("MERGED CHANGES (real git merge commits):")
	for _, mg := range m.Merges {
		fmt.Printf("  %-22s at %-52s  +%.3f  merge %s\n", mg.Operator, mg.Node, mg.VerifiedDelta, short(mg.MergeCommit))
	}
	fmt.Println("\nAUDIT TRAIL (change ledger + git history):")
	for _, ev := range m.Audit {
		fmt.Printf("  #%-2d %-9s %s\n", ev.Seq, ev.Type, ev.Summary)
	}
	// Demonstrate git-revert rollback on the LATEST merge (the always-clean case; reverting an older
	// merge under a newer one can conflict — design Q7).
	if len(m.Merges) > 0 {
		last := m.Merges[len(m.Merges)-1]
		fmt.Println("\nROLLBACK (git revert of the latest merge):")
		after, err := src.Rollback("hermes", last.MergeCommit, "damon")
		if err != nil {
			fmt.Printf("  rollback error: %v\n", err)
		} else {
			for _, ev := range after.Audit {
				if ev.Type == "revert" {
					fmt.Printf("  #%-2d revert    %s\n", ev.Seq, ev.Summary)
				}
			}
		}
	}
	fmt.Println("\ngit log --oneline (the loop's merges + revert on the real repo):")
	cmd := exec.Command("git", "log", "--oneline", "-12")
	cmd.Dir = src.dir
	out, _ := cmd.CombinedOutput()
	fmt.Print(string(out))
}

func short(h string) string {
	if len(h) > 10 {
		return h[:10]
	}
	return h
}
