package proposal

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalstats"
)

// caseIDs makes n synthetic case ids.
func caseIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("case-%03d", i)
	}
	return out
}

// task 1.4: the held-out split is a pure function of (config_hash, case ids) — reproducible and
// independent of the input order.
func TestHeldOutSplitIsDeterministic(t *testing.T) {
	const cfg = "cfghash-aaaa"
	cases := caseIDs(40)

	a := HeldOutSplit(cfg, cases)

	// Same inputs → identical split.
	b := HeldOutSplit(cfg, cases)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("split is not reproducible:\n a=%+v\n b=%+v", a, b)
	}

	// Shuffled input order → identical split (each case's bucket depends only on its own id).
	shuffled := append([]string(nil), cases...)
	sort.Sort(sort.Reverse(sort.StringSlice(shuffled)))
	c := HeldOutSplit(cfg, shuffled)
	if !reflect.DeepEqual(a, c) {
		t.Fatalf("split depends on input order:\n forward=%+v\n reverse=%+v", a, c)
	}

	// A different config hash produces (at least sometimes) a different split — the split is tied to the
	// configuration being judged, not global.
	d := HeldOutSplit("cfghash-bbbb", cases)
	if reflect.DeepEqual(a.HeldOut, d.HeldOut) {
		t.Error("two different config hashes produced an identical held-out set; the split is not seeded by config_hash")
	}

	// Disjoint and total by construction.
	assertDisjointAndTotal(t, a, cases)
}

// task 3.2 (proved here on the split primitive): motivating and held-out never share a case.
func TestGuardrailCasesDisjointFromMotivating(t *testing.T) {
	cases := caseIDs(60)
	s := HeldOutSplit("cfg-disjoint", cases)
	assertDisjointAndTotal(t, s, cases)
	if len(s.HeldOut) == 0 || len(s.Motivating) == 0 {
		t.Fatalf("degenerate split: motivating=%d held-out=%d", len(s.Motivating), len(s.HeldOut))
	}
}

func assertDisjointAndTotal(t *testing.T, s Split, all []string) {
	t.Helper()
	seen := map[string]int{}
	for _, id := range s.Motivating {
		seen[id]++
	}
	for _, id := range s.HeldOut {
		seen[id]++
	}
	for _, id := range all {
		if seen[id] != 1 {
			t.Errorf("case %q appears %d times across the two buckets, want exactly 1 (not disjoint/total)", id, seen[id])
		}
	}
	if len(seen) != len(all) {
		t.Errorf("split covers %d distinct cases, want %d", len(seen), len(all))
	}
}

// task 1.4: below the held-out floor the guardrail returns the THIRD verdict, not a tie-by-default.
func TestGuardrailInsufficientDataIsThirdVerdict(t *testing.T) {
	// Only 3 shared cases → after the ~50/50 split the held-out set is below MinHeldOutCases.
	incumbent := successSeries("incumbent", caseIDs(3), []int64{1, 2, 3, 4, 5}, 1.0)
	candidate := successSeries("candidate", caseIDs(3), []int64{1, 2, 3, 4, 5}, 1.0)

	res := EvaluateDowngradeGuardrail("cfg-insufficient", incumbent, candidate, evalstats.DefaultConfig())
	if res.Verdict != GuardrailInsufficientData {
		t.Fatalf("want %q, got %q (reason: %s)", GuardrailInsufficientData, res.Verdict, res.Reason)
	}
	if res.Verdict.Admissible() {
		t.Error("insufficient-data must not be admissible")
	}
	if res.Reason == "" {
		t.Error("the third verdict must carry a stated reason, not a bare label")
	}
}

// successSeries builds a task_success Series: every (case, seed) observation has value v.
func successSeries(variant string, cases []string, seeds []int64, v float64) evalstats.Series {
	s := evalstats.Series{VariantID: variant, Metric: "task_success"}
	for _, c := range cases {
		for _, seed := range seeds {
			s.Obs = append(s.Obs, evalstats.Observation{CaseID: c, Seed: seed, Value: v})
		}
	}
	return s
}
