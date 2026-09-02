package bounds

import (
	"errors"
	"os"
	"regexp"
	"testing"
	"time"
)

func full() Ceilings {
	return Ceilings{MaxIterations: 10, MaxTasks: 50, MaxAttemptsPerTask: 3, MaxToolCalls: 100,
		MaxTokens: 1_000_000, MaxCostCents: 500, MaxWallClock: time.Hour, MaxSpawnDepth: 3}
}

// TestZeroMeansForbiddenNotUnlimited is the most important fence in this package.
//
// The opposite convention turns every forgotten field, every partially-populated struct and every
// zero-valued test fixture into an unbounded run. This asserts each field individually, so a ceiling
// added later without a Validate arm is caught rather than inherited as "unlimited".
func TestZeroMeansForbiddenNotUnlimited(t *testing.T) {
	if err := full().Validate(); err != nil {
		t.Fatalf("fully populated ceilings must validate: %v", err)
	}
	if err := (Ceilings{}).Validate(); !errors.Is(err, ErrCeilingUnset) {
		t.Fatalf("the zero Ceilings must be REFUSED, not treated as unlimited; got %v", err)
	}
	zeroOut := map[string]func(*Ceilings){
		"MaxIterations":      func(c *Ceilings) { c.MaxIterations = 0 },
		"MaxTasks":           func(c *Ceilings) { c.MaxTasks = 0 },
		"MaxAttemptsPerTask": func(c *Ceilings) { c.MaxAttemptsPerTask = 0 },
		"MaxToolCalls":       func(c *Ceilings) { c.MaxToolCalls = 0 },
		"MaxTokens":          func(c *Ceilings) { c.MaxTokens = 0 },
		"MaxCostCents":       func(c *Ceilings) { c.MaxCostCents = 0 },
		"MaxWallClock":       func(c *Ceilings) { c.MaxWallClock = 0 },
		"MaxSpawnDepth":      func(c *Ceilings) { c.MaxSpawnDepth = 0 },
	}
	for name, zero := range zeroOut {
		c := full()
		zero(&c)
		if err := c.Validate(); !errors.Is(err, ErrCeilingUnset) {
			t.Errorf("%s left at zero was accepted; a forgotten field must not authorise an unbounded run", name)
		}
	}
}

// TestEveryCeilingActuallyTrips. A ceiling nothing checks is decoration.
func TestEveryCeilingActuallyTrips(t *testing.T) {
	c := full()
	cases := map[string]Spend{
		"MaxIterations": {Iterations: 10},
		"MaxTasks":      {Tasks: 50},
		"MaxToolCalls":  {ToolCalls: 100},
		"MaxTokens":     {Tokens: 1_000_000},
		"MaxCostCents":  {CostMicroCents: 500 * MicroCentsPerCent},
		"MaxWallClock":  {Elapsed: time.Hour},
		"MaxSpawnDepth": {SpawnDepth: 3},
	}
	for want, s := range cases {
		got, exceeded := s.Exceeded(c)
		if !exceeded {
			t.Errorf("%s: spend at the limit did not trip it", want)
			continue
		}
		if got != want {
			t.Errorf("spend at %s reported %q instead", want, got)
		}
	}
	if _, exceeded := (Spend{Iterations: 1, Tasks: 1}).Exceeded(c); exceeded {
		t.Error("a spend below every ceiling reported exceeded")
	}
}

// TestTokensAndMoneyAreSeparateCeilings. Deriving one from the other means a vendor price change
// silently moves the real cost of a ceiling a customer agreed to in money.
func TestTokensAndMoneyAreSeparateCeilings(t *testing.T) {
	c := full()
	if _, exceeded := (Spend{CostMicroCents: 500 * MicroCentsPerCent, Tokens: 1}).Exceeded(c); !exceeded {
		t.Error("money ceiling must trip on its own, with tokens far below their limit")
	}
	if _, exceeded := (Spend{Tokens: 1_000_000, CostMicroCents: 1}).Exceeded(c); !exceeded {
		t.Error("token ceiling must trip on its own, with cost far below its limit")
	}
}

// TestEveryRefusalNamesANextAction. "I cannot do that" teaches a person nothing, and a cause added
// without an action ships exactly that.
func TestEveryRefusalNamesANextAction(t *testing.T) {
	for _, c := range Causes() {
		r := Refusal{Cause: c}
		if r.NextAction() == "" {
			t.Errorf("%s has no next action: the refusal ends the conversation instead of moving it", c)
		}
		if !contains(r.Error(), r.NextAction()) {
			t.Errorf("%s: the rendered error drops the next action", c)
		}
	}
}

// TestUnboundedIsRefusedRatherThanDefaulted. The case the whole rule exists for.
func TestUnboundedIsRefusedRatherThanDefaulted(t *testing.T) {
	r := Refusal{Cause: UnboundedRequested, Detail: `"keep going until it is perfect"`}
	if r.NextAction() == "" {
		t.Fatal("an unbounded request must be told how to become bounded")
	}
	if !contains(r.Error(), "keep going until it is perfect") {
		t.Error("the refusal drops what the person actually said, so they cannot see what was rejected")
	}
}

func contains(hay, needle string) bool {
	return len(needle) > 0 && len(hay) >= len(needle) && indexOf(hay, needle) >= 0
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// TestCausesListsEveryCauseThatExists.
//
// # 🔴 The bug this exists for
//
// Causes() is a hand-maintained register, and TestEveryRefusalNamesANextAction iterates IT rather
// than the type. So a cause added to the type and forgotten here is not merely unlisted — it is
// exempt from the one check that guarantees a refusal tells somebody what to do. The register rots
// in the safe direction: everything it names stays correct, and the check quietly covers less and
// less. That happened on the two causes added for plan_exhausted / plan_stalled.
//
// This reads the source of the declarations rather than the register, so the register cannot be both
// the subject and the authority.
func TestCausesListsEveryCauseThatExists(t *testing.T) {
	src, err := os.ReadFile("bounds.go")
	if err != nil {
		t.Fatalf("bounds.go: %v", err)
	}
	declared := regexp.MustCompile(`(?m)^\s*(\w+)\s+RefusalCause\s*=\s*"([^"]+)"`).
		FindAllStringSubmatch(string(src), -1)
	if len(declared) == 0 {
		t.Fatal("no RefusalCause declarations found — this fence is measuring nothing")
	}

	listed := map[RefusalCause]bool{}
	for _, c := range Causes() {
		listed[c] = true
	}
	for _, d := range declared {
		if !listed[RefusalCause(d[2])] {
			t.Errorf("%s (%q) is declared but missing from Causes(), so it is exempt from "+
				"TestEveryRefusalNamesANextAction and can ship with no next action", d[1], d[2])
		}
	}
	if len(declared) != len(Causes()) {
		t.Errorf("%d causes declared, %d in Causes() — the register names something that is not a cause",
			len(declared), len(Causes()))
	}
}
