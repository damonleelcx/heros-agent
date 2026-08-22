package assessment

import (
	"context"
	"log/slog"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// health_test.go covers §6.1, §6.2 and §6.4.
//
// # 🔴 The test that matters is the one that fires
//
// Task 6.2's whole premise is that the condition it alerts on is INVISIBLE in every other signal. So a
// test asserting "the counters exist" would be worth nothing: the counters can exist, be correct, and
// never be looked at. What has to be asserted is that the ALERT goes true — and that it does not go
// true on the cases that look similar and are healthy.

func healthRunner(t *testing.T) (*Runner, *Metrics) {
	t.Helper()
	m := NewMetrics()
	tick := int64(0)
	r, err := NewRunner(&memStore{}, allResolve{}, nil, func() int64 { tick++; return tick },
		slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r.WithMetrics(m), m
}

// TestHealthIsBrokenOutPerAxisAndPerState is §6.1. An aggregate hides the one axis that is broken,
// which is the standing warning of the whole phase applied to its own observability.
func TestHealthIsBrokenOutPerAxisAndPerState(t *testing.T) {
	r, m := healthRunner(t)
	if _, err := r.Run(context.Background(), cfg(), subjectFor(t, "python")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	h := m.Health()
	if h.Started != 1 || h.Completed != 1 || h.Refused != 0 {
		t.Fatalf("lifecycle counters are %d/%d/%d, want 1/1/0", h.Started, h.Completed, h.Refused)
	}
	axes := map[Axis]bool{}
	total := int64(0)
	for _, cell := range h.PerAxis {
		axes[cell.Axis] = true
		total += cell.Count
	}
	if len(axes) != len(Axes()) {
		t.Fatalf("the breakdown covers %d axes, want %d — an axis missing from the document is an axis "+
			"nobody notices has stopped producing findings", len(axes), len(Axes()))
	}
	if total != int64(len(Axes())) {
		t.Fatalf("the cells sum to %d findings, want %d", total, len(Axes()))
	}
}

// TestTheAlertFiresWhenTheProductSilentlyStopsSayingAnything is §6.2, and it is the fence that has to
// be able to go red.
//
// 🔴 P34 changed what the FIRST half of this test asserts, and the change is the alarm arriving rather
// than the alarm breaking. Before the axis split, an unreadable repository produced seven absences and
// two refusals — `loop` and `graph` were refused pending their configuration — so it could never reach
// nine and the counter stayed at zero. That was a property of the pre-P34 build, and this test said so
// in its own second half: the nine-absence case had to be built by hand "because producing it needs a
// build in which the two P34 axes are no longer refused, WHICH IS THE WORLD THIS ALARM HAS TO KEEP
// WORKING IN."
//
// This is that world. A repository with no discoverable call sites now produces nine absences and DOES
// count — which is the counter doing its job, because `no_call_sites_discovered` on all nine axes is
// exactly what a broken frontend or a broken sandbox looks like from outside. Suppressing it as
// "expected for an unreadable repository" would suppress the signal the counter exists for.
//
// What still must NOT alert is one such assessment on its own: the threshold is a RATE, and the second
// half below is what proves the rate is what fires rather than the count.
func TestTheAlertFiresWhenTheProductSilentlyStopsSayingAnything(t *testing.T) {
	r, m := healthRunner(t)

	empty := Subject{WorkflowID: "wf-empty", IR: &discovery.IR{}}
	c := cfg()
	c.AssessmentID = "as-empty"
	if _, err := r.Run(context.Background(), c, empty); err != nil {
		t.Fatalf("Run: %v", err)
	}
	h := m.Health()
	if h.AllNotMeasured != 1 {
		t.Fatalf("all-not-measured is %d, want 1. Post-P34 every axis answers through `precondition` on "+
			"an unreadable repository, so nine absences is the produced shape — and it is the shape a "+
			"broken frontend produces too, which is what this counter is for", h.AllNotMeasured)
	}
	// 🔴 Every finding must carry a REASON. Nine absences with no named missing input would be the
	// counter firing on a report that says nothing about why, which is not a signal anyone can act on.
	got, err := r.Run(context.Background(), func() Config { c := cfg(); c.AssessmentID = "as-empty-2"; return c }(), empty)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range got.Findings {
		if f.MissingInput() == "" {
			t.Errorf("axis %s is not_measured with no named missing input", f.Axis())
		}
	}

	// Now the rate. Three more nine-absence assessments on top of the two produced above, so the rate
	// reaches 1.0 and crosses the threshold. Built directly as well as produced, because the counter
	// has to work on a hand-built report too — that is the shape the store replays.
	nine := nine(t, nil)
	for i := 0; i < 3; i++ {
		nine.AssessmentID = "as-nm"
		m.Completed(nine)
	}
	h = m.Health()
	const producedAbove = 2
	if h.AllNotMeasured != 3+producedAbove {
		t.Fatalf("counted %d nine-absence assessments, want %d (%d produced by Run above, 3 recorded "+
			"directly)", h.AllNotMeasured, 3+producedAbove, producedAbove)
	}
	if !h.Alerting {
		t.Fatalf("the alert did NOT fire at a rate of %.2f (threshold %.2f). This is the single best "+
			"early signal that a frontend or the sandbox broke, and it is invisible in a success rate: "+
			"such a run completes, answers 201 and writes nine rows",
			h.AllNotMeasuredRate, h.AlertAbove)
	}
}

// TestARateOverNothingIsNotZero is `evalboard.CostLatencyAnalysis`'s lesson again: `0.0` is a number
// somebody measured, and a deployment that has never assessed anything has no rate at all. Reporting
// 0.0 would tell a monitor "we checked and it is fine".
func TestARateOverNothingIsNotZero(t *testing.T) {
	h := NewMetrics().Health()
	if h.AllNotMeasuredRate != -1 {
		t.Fatalf("a rate over zero completed assessments is %v, want -1", h.AllNotMeasuredRate)
	}
	if h.Alerting {
		t.Fatal("a deployment that has never assessed anything is alerting")
	}
}

// TestARefusedAssessmentIsCountedAndIsNotACompletion separates the two failure shapes an operator
// reads differently: an assessment that could not be produced at all, and one that was produced and
// found nothing.
func TestARefusedAssessmentIsCountedAndIsNotACompletion(t *testing.T) {
	m := NewMetrics()
	tick := int64(0)
	r, err := NewRunner(&memStore{}, allResolve{refuse: map[Surface]bool{SurfaceGraph: true}}, nil,
		func() int64 { tick++; return tick }, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.WithMetrics(m).Run(context.Background(), cfg(), subjectFor(t, "python")); err == nil {
		t.Fatal("an unresolvable evidence reference did not fail the run")
	}
	h := m.Health()
	if h.Started != 1 || h.Refused != 1 || h.Completed != 0 {
		t.Fatalf("lifecycle counters are %d started / %d refused / %d completed, want 1/1/0",
			h.Started, h.Refused, h.Completed)
	}
	if len(h.PerAxis) != 0 {
		t.Fatal("a refused assessment contributed findings to the breakdown — it produced none")
	}
}

// TestSpendIsAttributedToTheTenant is §6.4. It is an attribution for capacity and ceilings, NOT a
// charge: an inference is the platform's spend and P7's rule is that the platform never resells
// provider tokens.
func TestSpendIsAttributedToTheTenant(t *testing.T) {
	m := NewMetrics()
	a := nine(t, nil)
	a.TenantID = "tn-a"
	a.SpendUSD = 0.42
	m.Completed(a)
	b := nine(t, nil)
	b.TenantID = "tn-b"
	b.SpendUSD = 0.11
	m.Completed(b)

	h := m.Health()
	if got := h.PerTenantSpendUSD["tn-a"]; got != 0.42 {
		t.Fatalf("tn-a is attributed $%.2f, want $0.42", got)
	}
	if got := h.PerTenantSpendUSD["tn-b"]; got != 0.11 {
		t.Fatalf("tn-b is attributed $%.2f, want $0.11", got)
	}
	if h.SpendUSD < 0.52 || h.SpendUSD > 0.54 {
		t.Fatalf("total spend is $%.4f, want $0.53", h.SpendUSD)
	}
}

// TestCompletionIsCountedAfterTheWrite is `e2e-acceptance-live-events`' rule applied to a counter: a
// 201 is not evidence of a write, and a metric counted on the way in counts INTENT.
func TestCompletionIsCountedAfterTheWrite(t *testing.T) {
	m := NewMetrics()
	tick := int64(0)
	store := &memStore{fail: context.DeadlineExceeded}
	r, err := NewRunner(store, allResolve{}, nil, func() int64 { tick++; return tick },
		slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.WithMetrics(m).Run(context.Background(), cfg(), subjectFor(t, "python")); err == nil {
		t.Fatal("a failing store did not fail the run")
	}
	h := m.Health()
	if h.Completed != 0 {
		t.Fatalf("an assessment whose write FAILED was counted as completed (%d). The counter would "+
			"then read healthy on a deployment persisting nothing", h.Completed)
	}
	if h.Refused != 1 {
		t.Fatalf("the failed write was not counted as refused (%d)", h.Refused)
	}
}
