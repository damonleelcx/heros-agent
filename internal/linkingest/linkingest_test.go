package linkingest

import (
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/runlink"
)

func samplePayload(runID string) runlink.Payload {
	return runlink.BuildPayload(runlink.RunRecord{
		RunID: runID, WorkflowID: "wf", ConfigHash: strings.Repeat("a", 64),
		SourceRevision: "rev1", Timestamp: "2026-07-25T00:00:00Z", Seeds: []int64{1000},
		ToolVersion:  "0.11.0",
		Metrics:      runlink.Metrics{CostUSD: 0.50, LatencyMS: 300, TokensIn: 100, TokensOut: 40},
		IR:           runlink.IRStructure{NodeIDs: []string{"n_1"}},
		Scores:       []runlink.Score{{Metric: "quality", Value: 0.8, CILow: 0.7, CIHigh: 0.9}},
		RunsReported: 4,
	})
}

func newHarness() (*Ingester, *metering.MemCostEvents, *MemStore) {
	sub := metering.NewMemCostEvents()
	links := NewMemStore()
	ing := New(sub, links, func(t, r string) string { return "https://heros-agent.space/app/runs/" + r })
	ing.SetClock(func() time.Time { return time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC) })
	return ing, sub, links
}

func periodOf(ts string) metering.Period {
	t, _ := time.Parse(time.RFC3339, ts)
	return metering.MonthPeriod(t)
}

// TestConsoleRoute_IsThePlatformCanonicalRunPath pins the run reference the CLI prints (FR18) to the
// console's canonical run route (P9 §11c.4). A URL pasted from a terminal into a pull request must open
// exactly that run in the console, so the emitted PATH must be `/app/runs/{run_id}` — the same segment
// the console's `routes.run` defines and its routes.test.mjs pins from the other side. If this default
// ever drifts, every already-pasted link silently stops resolving.
func TestConsoleRoute_IsThePlatformCanonicalRunPath(t *testing.T) {
	// No consoleURL override — this is the DEFAULT the platform uses when it has not been told otherwise.
	ing := New(metering.NewMemCostEvents(), NewMemStore(), nil)

	got := ing.route("tenant-A", "run-77")
	want := "https://heros-agent.space/app/runs/run-77"
	if got != want {
		t.Fatalf("default console run route = %q, want %q (must match P9 routes.run)", got, want)
	}
	if !strings.HasPrefix(got, runlink.PlatformBaseURL+"/app/runs/") {
		t.Fatalf("run route %q is not under the pinned platform /app/runs/ path", got)
	}
}

// TestIdempotentLinking — re-linking the same run does not double-count SUM (FR14, task 3.4/7.7).
func TestIdempotentLinking(t *testing.T) {
	ing, sub, _ := newHarness()
	p := samplePayload("run-1")

	r1, err := ing.Ingest("tenantA", p)
	if err != nil || !r1.Accepted || r1.AlreadyLinked {
		t.Fatalf("first ingest: %+v err=%v", r1, err)
	}
	r2, err := ing.Ingest("tenantA", p)
	if err != nil {
		t.Fatal(err)
	}
	if !r2.AlreadyLinked {
		t.Errorf("second ingest should report already_linked")
	}

	sum, err := metering.DeriveSUM(sub, "tenantA", periodOf("2026-07-25T00:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if sum.Quantity != 0.50 {
		t.Errorf("SUM = %v, want 0.50 (re-link double-counted)", sum.Quantity)
	}
	if sum.EventCount != 1 {
		t.Errorf("distinct cost events = %d, want 1", sum.EventCount)
	}
}

// TestServerSideTenantAttribution — a linked run counts for the authenticated tenant only; another
// tenant sees nothing, and the payload carries no tenant field to widen scope (FR/NFR11, task 3.6).
func TestServerSideTenantAttribution(t *testing.T) {
	ing, sub, _ := newHarness()
	if _, err := ing.Ingest("tenantA", samplePayload("run-1")); err != nil {
		t.Fatal(err)
	}
	a, _ := metering.DeriveSUM(sub, "tenantA", periodOf("2026-07-25T00:00:00Z"))
	b, _ := metering.DeriveSUM(sub, "tenantB", periodOf("2026-07-25T00:00:00Z"))
	if a.Quantity != 0.50 {
		t.Errorf("tenantA SUM = %v, want 0.50", a.Quantity)
	}
	if b.Quantity != 0 {
		t.Errorf("tenantB SUM = %v, want 0 — scope leaked across tenants", b.Quantity)
	}
}

// TestPartialCoverageSpendIsExact — a partially-linked tenant's spend equals the sum of LINKED runs
// exactly, with no estimate added for unlinked activity (task 4.4). Coverage reflects the fraction.
func TestPartialCoverageSpendIsExact(t *testing.T) {
	ing, sub, links := newHarness()
	// The CLI observed 4 runs this session (runs_reported=4) but links only 2 of them.
	if _, err := ing.Ingest("tenantA", samplePayload("run-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := ing.Ingest("tenantA", samplePayload("run-2")); err != nil {
		t.Fatal(err)
	}
	sum, _ := metering.DeriveSUM(sub, "tenantA", periodOf("2026-07-25T00:00:00Z"))
	if sum.Quantity != 1.00 {
		t.Errorf("SUM = %v, want exactly 1.00 (2 linked runs × 0.50, no estimate)", sum.Quantity)
	}
	cov := links.Coverage("tenantA")
	if cov.RunsLinked != 2 || cov.RunsReported != 4 || cov.Complete {
		t.Errorf("coverage = %+v, want 2/4 incomplete", cov)
	}
	if !cov.Known {
		t.Errorf("coverage should be known (a denominator was reported)")
	}
}

// TestUnknownVsCompleteCoverage — coverage distinguishes complete from unknown (task 5.3).
func TestUnknownVsCompleteCoverage(t *testing.T) {
	// Complete: report 1, link 1.
	_, _, links := newHarness()
	links.ObserveRunsReported("t", 1)
	_, _ = links.Record(LinkedRun{RunID: "r1", TenantID: "t", LinkedAt: time.Now()})
	if c := links.Coverage("t"); !c.Complete || !c.Known {
		t.Errorf("complete coverage misreported: %+v", c)
	}
	// Unknown: no runs, no denominator.
	empty := NewMemStore()
	if c := empty.Coverage("t"); c.Known || c.Complete {
		t.Errorf("empty coverage should be unknown, got %+v", c)
	}
}

// TestScoresRecordedAsComputed — a linked run's scores + intervals are recorded exactly as the CLI
// computed them, so a hosted scorecard matches the local one (task 4.3, parity 7.6).
func TestScoresRecordedAsComputed(t *testing.T) {
	ing, _, links := newHarness()
	p := samplePayload("run-1") // carries quality 0.8 [0.7, 0.9]
	if _, err := ing.Ingest("tenantA", p); err != nil {
		t.Fatal(err)
	}
	lr, ok := links.Get("tenantA", "run-1")
	if !ok || len(lr.Scores) != 1 {
		t.Fatalf("scores not recorded: %+v", lr)
	}
	s := lr.Scores[0]
	if s.Metric != "quality" || s.Value != 0.8 || s.CILow != 0.7 || s.CIHigh != 0.9 {
		t.Errorf("recorded score drifted from computed: %+v", s)
	}
}

// TestNoExtrapolationPath — SUM is a sum of observed events only; there is no code path that estimates
// unlinked spend (task 4.1). We link one run and assert SUM equals exactly its cost — never more.
func TestNoExtrapolationPath(t *testing.T) {
	ing, sub, links := newHarness()
	// The CLI observed 100 runs but linked 1. An extrapolating meter would inflate SUM ~100×.
	p := samplePayload("run-1")
	p.RunsReported = 100
	if _, err := ing.Ingest("tenantA", p); err != nil {
		t.Fatal(err)
	}
	sum, _ := metering.DeriveSUM(sub, "tenantA", periodOf("2026-07-25T00:00:00Z"))
	if sum.Quantity != 0.50 {
		t.Errorf("SUM = %v, want exactly 0.50 — an estimate was added for unlinked runs", sum.Quantity)
	}
	if c := links.Coverage("tenantA"); c.RunsLinked != 1 || c.RunsReported != 100 || c.Complete {
		t.Errorf("coverage should show 1/100 incomplete, got %+v", c)
	}
}

// TestClosedPeriodRejected — a run in a closed period cannot reopen a closed meter (Q6).
func TestClosedPeriodRejected(t *testing.T) {
	ing, sub, _ := newHarness()
	ing.SetPeriodClosed(func(time.Time) bool { return true })
	_, err := ing.Ingest("tenantA", samplePayload("run-1"))
	var closed *ClosedPeriod
	if err == nil || !asClosed(err, &closed) {
		t.Fatalf("expected ClosedPeriod, got %v", err)
	}
	sum, _ := metering.DeriveSUM(sub, "tenantA", periodOf("2026-07-25T00:00:00Z"))
	if sum.Quantity != 0 {
		t.Errorf("a rejected run must contribute nothing, got SUM %v", sum.Quantity)
	}
}

// TestContractMismatchRejected — a payload from a CLI on a different contract is refused loudly.
func TestContractMismatchRejected(t *testing.T) {
	ing, _, _ := newHarness()
	p := samplePayload("run-1")
	p.ContractVersion = "p11.link.v0" // stale
	_, err := ing.Ingest("tenantA", p)
	var mm *ContractMismatch
	if err == nil || !asMismatch(err, &mm) {
		t.Fatalf("expected ContractMismatch, got %v", err)
	}
}

func asClosed(err error, target **ClosedPeriod) bool {
	for err != nil {
		if c, ok := err.(*ClosedPeriod); ok {
			*target = c
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func asMismatch(err error, target **ContractMismatch) bool {
	for err != nil {
		if c, ok := err.(*ContractMismatch); ok {
			*target = c
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
