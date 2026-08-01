// Command governance serves the P6 autonomous-optimizer governance surface against a REAL controller loop
// running over a REAL git repository (a throwaway temp clone the loop owns), with the ONLY stub being
// the provider/verification fan-out (canned per-candidate verdicts, exactly as p55demo stubs it).
//
// Everything the UI drives is the shipped code path: the authority grant records the constraints in the
// change ledger before any merge; the loop opens+merges PRs only on build-passing, held-out-verified,
// gate-passing candidates with the three prerequisites armed; the stop control fires the real kill
// switch; every merged change is reverted with a real `git revert`; and the monitor reads the loop's
// own progress signal. Not a shipped service — a demo harness (task 7.7 / 8.8).
//
//	go run ./cmd/demo/governance   # then open the printed URL
package main

import (
	"context"
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

var baseline = []byte(`{"workflow":"demo","router":{"model":"haiku"},"rag":{"skills":["retriever"]}}`)

// candidate is one diagnosis-guided candidate + its canned verdict. tryOrder sets the verification
// order (highest first), independent of the absolute composite, so the demo tells a clear story.
type candidate struct {
	spec         []byte
	diag, node   string
	operator     string
	providers    []string
	compositeLow float64
	delta        float64
	tryOrder     float64
	spend        float64
}

func candidates() []candidate {
	return []candidate{
		// 1. strong model upgrade → merges, best composite 0.72.
		{spec: []byte(`{"workflow":"demo","router":{"model":"sonnet-5"},"rag":{"skills":["retriever"]}}`),
			diag: "diag-router", node: "router", operator: "model_upgrade", providers: []string{"anthropic"},
			compositeLow: 0.72, delta: 0.41, tryOrder: 0.9, spend: 0.08},
		// 2. add rerank → marginal composite gain 0.08 > min-improvement → merges, best 0.80.
		{spec: []byte(`{"workflow":"demo","router":{"model":"sonnet-5"},"rag":{"skills":["retriever","rerank"]}}`),
			diag: "diag-rag", node: "rag", operator: "add_rerank", providers: []string{"anthropic"},
			compositeLow: 0.80, delta: 0.22, tryOrder: 0.8, spend: 0.09},
		// 3. HIGHEST composite (0.95) but calls an off-allowlist provider → gate-fails → NEVER merged.
		{spec: []byte(`{"workflow":"demo","router":{"model":"gpt-4o"},"rag":{"skills":["retriever","rerank"]}}`),
			diag: "diag-cost", node: "router", operator: "model_downgrade", providers: []string{"openai"},
			compositeLow: 0.95, delta: 0.30, tryOrder: 0.7, spend: 0.05},
		// 4. context-policy switch: gate-passes but marginal gain 0.015 < min-improvement → converged.
		{spec: []byte(`{"workflow":"demo","router":{"model":"sonnet-5"},"rag":{"skills":["retriever","rerank","summarize"]}}`),
			diag: "diag-rag", node: "rag", operator: "context_policy_switch", providers: []string{"anthropic"},
			compositeLow: 0.815, delta: 0.05, tryOrder: 0.6, spend: 0.06},
	}
}

// slowVerifier wraps a StaticVerifier with a per-call delay so the live monitor visibly advances.
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

// source implements api.OptimizerSource over a real controller loop.
type source struct {
	mu         sync.Mutex
	dir        string
	ledger     *optimizer.MemLedger
	kill       *optimizer.KillSwitch
	ctrl       *optimizer.Controller
	started    bool
	snapshot   optimizer.RunResult
	rolledBack map[string]string // merge commit → revert commit
	level      string
	granted    optimizer.Authority
}

func (s *source) Monitor(runID string) (api.Monitor, bool) {
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
	auth := optimizer.Authority{
		RunID: req.RunID, WorkflowID: req.WorkflowID, Actor: req.Actor, WeightProfile: "balanced",
		Constraints: optimizer.Constraints{
			BudgetCeilingUSD: req.BudgetCeilingUSD, ProviderAllowlist: req.ProviderAllowlist,
			MinImprovement: req.MinImprovement, MaxIterations: req.MaxIterations, StallK: 3,
		},
		KillSwitchArmed: true, AuditArmed: true, RollbackArmed: true, GrantedAt: time.Unix(1_700_000_000, 0),
	}
	s.granted = auth
	s.started = true
	s.level = "autonomous"

	cands := candidates()
	byConfig := map[string]optimizer.VerifyResult{}
	var searchCands []optimizer.SearchCandidate
	for _, c := range cands {
		hash := optimizer.ContentHash(c.spec)
		providers := c.providers
		byConfig[hash] = optimizer.VerifyResult{
			ContractOK: true, Builds: true, SpendUSD: c.spend,
			Verdict: verification.Verdict{GateResult: verification.GatePass, Significant: true, HeldOut: true,
				RegressionPass: true, Delta: evalstats.Interval{Mean: c.delta, Low: c.delta - 0.05, High: c.delta + 0.05},
				CostDelta: -0.002, LatencyDelta: 40},
			Metrics: optimizer.CandidateMetrics{Providers: providers, Quality: 0.9, LatencyMS: 520,
				Composite: evalstats.Interval{Mean: c.compositeLow + 0.05, Low: c.compositeLow, High: c.compositeLow + 0.1}},
		}
		searchCands = append(searchCands, optimizer.SearchCandidate{
			DiagnosisID: c.diag, Node: c.node, Dimension: "model", Operator: c.operator,
			ConfigHash: hash, SpecBytes: c.spec, Providers: providers, ExpectedGain: c.tryOrder,
		})
	}

	enum := candEnum{byNode: map[string][]optimizer.SearchCandidate{}}
	for _, sc := range searchCands {
		enum.byNode[sc.Node] = append(enum.byNode[sc.Node], sc)
	}
	s.ctrl = &optimizer.Controller{
		Search:   optimizer.Search{Enum: enum},
		Verifier: slowVerifier{inner: optimizer.StaticVerifier{ByConfig: byConfig}, delay: 900 * time.Millisecond},
		Repo:     optimizer.GitRepo{Dir: s.dir, SpecPath: specPath, Branch: "main"},
		Ledger:   s.ledger,
		Kill:     s.kill,
		Clock:    func() time.Time { return time.Now().UTC() },
		OnIteration: func(res optimizer.RunResult) {
			s.mu.Lock()
			s.snapshot = res
			s.mu.Unlock()
		},
	}
	targets := []optimizer.Target{
		{DiagnosisID: "diag-router", Node: "router", Dimension: "model", Priority: 1.0},
		{DiagnosisID: "diag-rag", Node: "rag", Dimension: "skills", Priority: 0.8},
	}
	in := optimizer.RunInput{Authority: auth, Targets: targets, BaselineSpecBytes: baseline,
		EvalSetCaseIDs: []string{"c1", "c2", "c3", "c4", "c5", "c6"}}

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
	// Give the loop a moment to observe the stop.
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

// buildMonitor renders the shared snapshot + ledger into the api.Monitor read model. Caller holds mu.
func (s *source) buildMonitor() api.Monitor {
	res := s.snapshot
	cons := s.granted.Constraints
	m := api.Monitor{
		RunID: res.RunID, WorkflowID: res.WorkflowID, AutomationLevel: s.level,
		State: string(res.State), MergeEnabled: res.MergeEnabled, DisarmReason: res.DisarmReason,
		StopReason: res.StopReason, MissingPrereqs: s.granted.MissingPrereqs(), DryRun: !s.granted.MergeArmed(),
		BudgetCeilingUSD: cons.BudgetCeilingUSD, CumulativeSpendUSD: res.CumulativeSpend,
		MaxIterations: cons.MaxIterations, CurrentIteration: len(res.Iterations),
		MinImprovement: cons.MinImprovement, ProviderAllowlist: cons.ProviderAllowlist,
		PRsMerged: len(res.Merges),
	}
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

// candEnum enumerates diagnosis-guided candidates per node.
type candEnum struct {
	byNode map[string][]optimizer.SearchCandidate
}

func (c candEnum) Enumerate(t optimizer.Target) []optimizer.SearchCandidate { return c.byNode[t.Node] }

// initGitRepo makes a throwaway git repo the optimizer owns, with the baseline spec committed on main.
func initGitRepo() (string, error) {
	dir, err := os.MkdirTemp("", "p6demo-repo-*")
	if err != nil {
		return "", err
	}
	run := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v: %s", args, err, out)
		}
		return nil
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"config", "user.email", "seed@demo.local"}, {"config", "user.name", "seed"},
	} {
		if err := run(args...); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, specPath), baseline, 0o644); err != nil {
		return "", err
	}
	if err := run("add", specPath); err != nil {
		return "", err
	}
	if err := run("commit", "-q", "-m", "baseline"); err != nil {
		return "", err
	}
	return dir, nil
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8496", "listen address")
	flag.Parse()

	dir, err := initGitRepo()
	if err != nil {
		log.Fatalf("init demo repo: %v", err)
	}
	src := &source{dir: dir, ledger: optimizer.NewMemLedger(), kill: optimizer.NewKillSwitch(),
		rolledBack: map[string]string{}, level: "autonomous"}

	s := api.New(nil, config.Config{})
	s.MountOptimizer(src)

	fmt.Printf("P6 monitor:  http://%s/optimizer?run=demo-run\n", *addr)
	fmt.Printf("demo repo:   %s\n", dir)
	log.Fatal(http.ListenAndServe(*addr, s.Handler))
}
