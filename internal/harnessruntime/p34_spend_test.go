package harnessruntime

import (
	"errors"
	"strings"
	"testing"
)

// P34 — the envelope's spend ceiling, checked BEFORE the call (harness-envelope spec).

type fixedMeter struct{ spent float64 }

func (m *fixedMeter) SpentUSD() float64 { return m.spent }

// risingMeter charges a fixed amount per read, so a loop that keeps invoking eventually exhausts it.
// It reads on the gate's check, which is exactly once per turn.
type risingMeter struct {
	perTurn float64
	reads   int
}

func (m *risingMeter) SpentUSD() float64 {
	spent := float64(m.reads) * m.perTurn
	m.reads++
	return spent
}

func usd(v float64) *float64 { return &v }

// TestTheSpendCeilingStopsBeforeTheCall is the spec's own wording: the check happens BEFORE the call is
// made. Checking afterwards would enforce the ceiling by having already exceeded it, which on the turn
// that matters is the difference between a bound and a report.
func TestTheSpendCeilingStopsBeforeTheCall(t *testing.T) {
	calls := 0
	invoke := func([]Message) (string, error) { calls++; return "answer", nil }

	got, err := Run(Config{
		Strategy:        "reflexion",
		Params:          Params{MaxTurns: 4, StopCondition: "max-turns", ReflectionPrompt: "improve"},
		SpendCeilingUSD: usd(1.00),
	}, Hosts{SpendMeter: &fixedMeter{spent: 1.00}}, nil, invoke)

	if err != nil {
		t.Fatalf("exhausting the budget returned an error (%v). The spec requires a named STOPPING "+
			"CONDITION: a run that stopped on budget produced a real answer under a known configuration, "+
			"and filing it beside \"the provider was down\" gets it the wrong response", err)
	}
	if calls != 0 {
		t.Fatalf("the turn function was called %d time(s) after the budget was already exhausted; the "+
			"ceiling is checked BEFORE the call, not after", calls)
	}
	if got.Stop != StopSpendCeiling {
		t.Fatalf("stop reason is %q, want %q", got.Stop, StopSpendCeiling)
	}
	if !got.Stop.Limit() {
		t.Error("StopSpendCeiling does not report itself as a limit; a surface would then be free to " +
			"render a run that ran out of money as COMPLETE")
	}
	// The trace names WHICH turn was not taken, so "we stopped at one of four because the money ran out"
	// is legible without joining to a billing table.
	if len(got.Trace) != 1 || got.Trace[0].Turn != 1 || got.Trace[0].Reason != StopSpendCeiling {
		t.Errorf("the trace does not record the turn that was refused: %+v", got.Trace)
	}
}

// TestTheSpendCeilingStopsMidLoop — the interesting case, where turns HAVE run. A run that stops here
// has a partial answer, and it must keep it.
func TestTheSpendCeilingStopsMidLoop(t *testing.T) {
	calls := 0
	invoke := func([]Message) (string, error) { calls++; return "partial answer", nil }

	// $0.40 per turn against a $1.00 ceiling: turns 1 and 2 are affordable ($0.00, $0.40), turn 3 is
	// checked at $0.80 and affordable, turn 4 at $1.20 is not.
	got, err := Run(Config{
		Strategy:        "reflexion",
		Params:          Params{MaxTurns: 6, StopCondition: "max-turns", ReflectionPrompt: "improve"},
		SpendCeilingUSD: usd(1.00),
	}, Hosts{SpendMeter: &risingMeter{perTurn: 0.40}}, nil, invoke)

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Stop != StopSpendCeiling {
		t.Fatalf("stop reason is %q, want %q (calls: %d)", got.Stop, StopSpendCeiling, calls)
	}
	if got.Answer != "partial answer" {
		t.Errorf("the partial answer was discarded (%q); a run that stopped on budget produced real work "+
			"and throwing it away spends the money for nothing", got.Answer)
	}
	if got.Turns == 0 {
		t.Error("Turns is 0 after turns actually ran")
	}
	if got.Turns >= 6 {
		t.Errorf("the loop ran %d turns despite a ceiling it should have hit at 3; the meter is not being "+
			"consulted between turns", got.Turns)
	}
}

// TestNoCeilingMeansNoCheck — an absent ceiling is not a ceiling of zero. If this went red, every loop
// that declared no budget would stop before its first turn.
func TestNoCeilingMeansNoCheck(t *testing.T) {
	calls := 0
	invoke := func([]Message) (string, error) { calls++; return "answer", nil }
	got, err := Run(Config{
		Strategy: "reflexion",
		Params:   Params{MaxTurns: 3, StopCondition: "max-turns", ReflectionPrompt: "improve"},
	}, Hosts{SpendMeter: &fixedMeter{spent: 99}}, nil, invoke)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls == 0 || got.Stop == StopSpendCeiling {
		t.Fatalf("a loop that declared no spend ceiling stopped on one anyway (calls=%d, stop=%q); an "+
			"absent ceiling is not a ceiling of zero", calls, got.Stop)
	}
}

// TestADeclaredCeilingWithNoMeterIsRefused — fail closed, with the other preflight checks, before the
// first turn.
//
// 🔴 The alternative is the failure this exists to prevent: silently ignoring the ceiling leaves an
// operator believing a bound is in force that is not, which is worse than having none because it stops
// them looking.
func TestADeclaredCeilingWithNoMeterIsRefused(t *testing.T) {
	calls := 0
	invoke := func([]Message) (string, error) { calls++; return "answer", nil }
	_, err := Run(Config{
		Strategy:        "reflexion",
		Params:          Params{MaxTurns: 3, StopCondition: "max-turns", ReflectionPrompt: "improve"},
		SpendCeilingUSD: usd(5),
	}, Hosts{}, nil, invoke)

	if err == nil {
		t.Fatal("a declared spend ceiling with no meter ran anyway; the bound was silently not enforced")
	}
	if !errors.Is(err, ErrMissingHostService) {
		t.Errorf("error is %v, want ErrMissingHostService — it is a host service that was not supplied, "+
			"exactly like a missing critic", err)
	}
	if calls != 0 {
		t.Errorf("the turn function ran %d time(s) before the preflight refused; a run that cannot "+
			"honour its own budget must never spend a call", calls)
	}
	if !strings.Contains(err.Error(), "not ignored") {
		t.Errorf("the refusal does not say the ceiling is not being ignored: %v", err)
	}
}

// TestTheCeilingIsInclusive — `>=`, not `>`. A run that has spent exactly its ceiling has used its
// budget; admitting one more turn on the strength of an equality makes every declared ceiling one turn
// looser than it reads.
func TestTheCeilingIsInclusive(t *testing.T) {
	invoke := func([]Message) (string, error) { return "answer", nil }
	got, err := Run(Config{
		Strategy:        "reflexion",
		Params:          Params{MaxTurns: 3, StopCondition: "max-turns", ReflectionPrompt: "improve"},
		SpendCeilingUSD: usd(2.50),
	}, Hosts{SpendMeter: &fixedMeter{spent: 2.50}}, nil, invoke)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Stop != StopSpendCeiling {
		t.Fatalf("a run that had spent exactly its ceiling took another turn (stop=%q)", got.Stop)
	}
}

// TestStopSpendCeilingIsInTheClosedVocabulary — the member has to be in StopReasons() or a consumer
// asserting its own terminal messages against that set will reject a run this package produces.
func TestStopSpendCeilingIsInTheClosedVocabulary(t *testing.T) {
	if !StopSpendCeiling.Valid() {
		t.Fatal("StopSpendCeiling is not a member of the closed set")
	}
	found := false
	for _, r := range StopReasons() {
		if r == StopSpendCeiling {
			found = true
		}
	}
	if !found {
		t.Fatal("StopReasons() omits StopSpendCeiling; a consumer checking its terminal messages against " +
			"that set would reject a run this package produces")
	}
	// 🔴 And it is DISTINCT from the per-turn token budget. An operator reading "token-budget" would go
	// and raise a per-turn allowance, which is not what ran out.
	if StopSpendCeiling == StopTokenBudget {
		t.Fatal("the node's money ceiling and a turn's token allowance are the same member")
	}
}
