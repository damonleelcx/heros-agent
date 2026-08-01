package proposal

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalstats"
)

// P13 13c section 10 — authoring meets the guardrail.
//
// The single sentence these tests enforce: authoring changes who PICKS the candidate, and nothing about
// who JUDGES it. A user may author the change. A user may not author the evidence.

// TestAuthoredChangeUsesSameVerdictPath (task 10.1).
//
// Structural, because the property is about what EXISTS. The failure it guards is a future
// "AuthoredVerdict" / "AuthoredGuardrail" type added for convenience: a second verdict shape is a second
// definition of "better", and the two drift the moment one is updated.
func TestAuthoredChangeUsesSameVerdictPath(t *testing.T) {
	t.Run("no authoring-specific verdict or guardrail type exists", func(t *testing.T) {
		for _, pkg := range []string{".", filepath.Join("..", "verification")} {
			entries, err := os.ReadDir(pkg)
			if err != nil {
				t.Fatalf("read %s: %v", pkg, err)
			}
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
					continue
				}
				f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(pkg, name), nil, 0)
				if err != nil {
					t.Fatalf("parse %s: %v", name, err)
				}
				ast.Inspect(f, func(n ast.Node) bool {
					ts, ok := n.(*ast.TypeSpec)
					if !ok {
						return true
					}
					id := ts.Name.Name
					// AuthoredReport is the allowed one: it is about what may be CLAIMED, not a second
					// verdict. A type that pairs "Authored" with a judgment word is the smell.
					for _, judgment := range []string{"Verdict", "Guardrail", "Gate", "Score", "Rank"} {
						if strings.Contains(id, "Authored") && strings.Contains(id, judgment) {
							t.Errorf("%s declares %s — authoring must reuse the one verdict path, not fork it",
								name, id)
						}
					}
					return true
				})
			}
		}
	})

	t.Run("an authored candidate carries the same shape an operator's does", func(t *testing.T) {
		// Same struct, same fields — the only difference is who is recorded on it.
		authored := Candidate{Operator: "authored", Origin: OriginUser, NodeID: "n1"}
		operated := Candidate{Operator: OpModelDowngrade, NodeID: "n1"}
		if reflect.TypeOf(authored) != reflect.TypeOf(operated) {
			t.Fatal("authored and operator candidates are different types")
		}
		if operated.Origin.Normalized() != OriginOperator {
			t.Error("an operator candidate did not normalize to operator origin")
		}
		if !authored.Origin.IsUser() {
			t.Error("an authored candidate did not report user origin")
		}
	})
}

// TestAuthoredRunCannotSelectItsOwnCases (task 10.2, FR33).
//
// The guardrail's inputs are the configuration hash and the case ids the two runs actually share.
// Nothing an author supplies appears in that signature, and this test asserts the absence — because the
// way this rule gets broken is not a bad split, it is a helpful new parameter.
func TestAuthoredRunCannotSelectItsOwnCases(t *testing.T) {
	t.Run("the split takes no actor, no user, and no case selection", func(t *testing.T) {
		ft := reflect.TypeOf(HeldOutSplit)
		for i := 0; i < ft.NumIn(); i++ {
			name := strings.ToLower(ft.In(i).String())
			for _, banned := range []string{"actor", "user", "principal", "selection", "chosen", "authoring"} {
				if strings.Contains(name, banned) {
					t.Errorf("HeldOutSplit parameter %d is %s — the platform derives the evidence, not the author",
						i, ft.In(i))
				}
			}
		}
		if ft.NumIn() != 2 {
			t.Errorf("HeldOutSplit takes %d parameters; it should take exactly (configHash, caseIDs)", ft.NumIn())
		}
	})

	t.Run("the same configuration splits identically no matter who authored it", func(t *testing.T) {
		cases := []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8"}
		a := HeldOutSplit("hash-1", cases)
		// A different caller, a different order, the same configuration: the same split.
		shuffled := []string{"c8", "c3", "c1", "c7", "c5", "c2", "c6", "c4"}
		b := HeldOutSplit("hash-1", shuffled)
		if !reflect.DeepEqual(a.HeldOut, b.HeldOut) || !reflect.DeepEqual(a.Motivating, b.Motivating) {
			t.Errorf("the split depends on the caller's ordering:\n a=%+v\n b=%+v", a, b)
		}
		// And it is tied to the configuration, so it cannot be shopped for by re-submitting.
		if c := HeldOutSplit("hash-2", cases); reflect.DeepEqual(a.HeldOut, c.HeldOut) && len(a.HeldOut) > 0 {
			t.Log("note: two configurations happened to share a split; not an error, but check the seeding")
		}
	})
}

// TestAuthoredHeldOutStillDisjoint (task 10.2): the disjointness that protects an operator protects an
// author identically. A human has the same incentive a cost-driven operator has, and better tools for
// acting on it.
func TestAuthoredHeldOutStillDisjoint(t *testing.T) {
	cases := []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8", "c9", "c10"}
	split := HeldOutSplit("authored-config-hash", cases)

	if len(split.HeldOut) == 0 || len(split.Motivating) == 0 {
		t.Fatalf("degenerate split: %+v", split)
	}
	held := map[string]bool{}
	for _, id := range split.HeldOut {
		held[id] = true
	}
	for _, id := range split.Motivating {
		if held[id] {
			t.Errorf("case %q is in BOTH buckets — a guardrail judged on its own motivating cases is not a guardrail", id)
		}
	}
	if len(split.HeldOut)+len(split.Motivating) != len(cases) {
		t.Errorf("split lost or duplicated cases: %d + %d != %d",
			len(split.HeldOut), len(split.Motivating), len(cases))
	}
}

// TestForkedProposalDoesNotCreditOperator (task 10.4, FR29).
func TestForkedProposalDoesNotCreditOperator(t *testing.T) {
	cands := []Candidate{
		{Operator: OpModelUpgrade}, // proposed and won
		{Operator: OpModelUpgrade}, // proposed and lost
		{Operator: OpModelUpgrade, Origin: OriginUser, ForkedFromProposal: "cand-7"}, // human fixed it, then won
		{Operator: "authored", Origin: OriginUser},                                   // authored from scratch, won
		{Operator: OpPromptRewrite, ForkedFromProposal: "cand-9"},                    // fork pointer without Origin
	}
	won := func(c Candidate) bool { return c.Origin.IsUser() || c.Operator == OpModelUpgrade }

	credits := OperatorCredits(cands, won)

	up := credits[OpModelUpgrade]
	if up.Proposed != 2 {
		t.Errorf("model_upgrade proposed = %d, want 2 — the human-corrected fork must not count", up.Proposed)
	}
	if up.Won != 2 {
		t.Errorf("model_upgrade won = %d, want 2 (its own two)", up.Won)
	}
	if _, present := credits[OperatorKind("authored")]; present {
		t.Error("an 'authored' row appeared in operator performance — that is not a catalog operator")
	}
	if _, present := credits[OpPromptRewrite]; present {
		t.Error("a candidate carrying a fork pointer was credited — the pointer is the evidence a person touched it")
	}

	// The direct predicate, both directions.
	if _, ok := CreditedOperator(Candidate{Operator: OpModelUpgrade}); !ok {
		t.Error("an ordinary operator candidate was not credited")
	}
	if _, ok := CreditedOperator(Candidate{Operator: OpModelUpgrade, Origin: OriginUser}); ok {
		t.Error("a user-originated candidate was credited to an operator")
	}
	if got := (OperatorCredit{Proposed: 4, Won: 1}).WinRate(); got != 0.25 {
		t.Errorf("WinRate = %v, want 0.25", got)
	}
	if got := (OperatorCredit{}).WinRate(); got != 0 {
		t.Errorf("empty WinRate = %v, want 0 (no divide by zero)", got)
	}
}

// TestAuthoredDowngradeStillMeetsTheGuardrail: the admissibility computation is unchanged and does not
// consult authorship. An authored downgrade meets exactly the predicate an operator's does.
func TestAuthoredDowngradeStillMeetsTheGuardrail(t *testing.T) {
	cfg := evalstats.Config{MinSeeds: 3, Bootstrap: 400, Confidence: 0.95, RNGSeed: 7}
	// A cheaper model that is measurably worse on every case.
	incumbent := seriesFor("incumbent", 1.0)
	candidate := seriesFor("cheaper", 0.0)

	res := EvaluateDowngradeGuardrail("cfg-hash-authored", incumbent, candidate, cfg)
	if res.Verdict.Admissible() {
		t.Fatalf("a measurably worse cheaper model was admitted: %+v", res)
	}
	out := ClassifyDowngrade(res, 1.0, 0.1)
	if out.Admissible {
		t.Error("an inadmissible guardrail produced a shippable outcome")
	}
	if out.QualityWin {
		t.Error("a downgrade reported a quality win — that is never a valid outcome")
	}

	// And the disjointness the guardrail relies on holds for this run too.
	held := map[string]bool{}
	for _, id := range res.HeldOut {
		held[id] = true
	}
	for _, id := range res.Motivating {
		if held[id] {
			t.Errorf("case %q judged its own motivation", id)
		}
	}
}

// seriesFor builds a constant-valued series across cases and seeds, enough for the guardrail's floor.
func seriesFor(id string, value float64) evalstats.Series {
	s := evalstats.Series{VariantID: id, ConfigHash: id, Metric: "task_success"}
	for c := 1; c <= 16; c++ {
		for seed := int64(1); seed <= 5; seed++ {
			s.Obs = append(s.Obs, evalstats.Observation{
				CaseID: fmt.Sprintf("c%02d", c), Seed: seed, Value: value,
			})
		}
	}
	return s
}
