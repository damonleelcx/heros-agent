package patternclassifier

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// CALIBRATION (tasks 3.9 / 8.9). Rule-detector confidences are checked against the hand-labeled
// fixture set and agreement is REPORTED, not merely asserted — the same treatment an LLM judge would
// get. Run with -v to read the table.
//
// What makes this calibration rather than a restatement of the code: every detector is a pure
// predicate, so its behaviour on a fixture is exact. There is no sampling noise to model, and
// therefore the interesting number is not a distribution but the CONFUSION COUNTS — did the detector
// fire where a human said it should, and did it stay silent on the near-misses that look like it?
// A detector with perfect recall and zero discrimination (fires everywhere) scores identically to a
// correct one unless near-misses are in the set, which is why over half the fixtures are near-misses.
func TestCalibrationAgainstHandLabeledFixtures(t *testing.T) {
	type counts struct{ tp, fp, fn int }
	agree := map[Pattern]*counts{}
	confidences := map[Pattern]map[float64]int{}
	get := func(p Pattern) *counts {
		if agree[p] == nil {
			agree[p] = &counts{}
		}
		return agree[p]
	}

	for _, f := range allFixtures() {
		res, err := Classify(context.Background(), f.ir, f.opts())
		if err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		// What the hand label says this fixture contains, keyed by pattern+region.
		wanted := map[string]bool{}
		for _, w := range f.want {
			wanted[string(w.pattern)+"@"+w.ref()] = true
		}
		emitted := map[string]bool{}
		for _, l := range res.Labels {
			key := string(l.Pattern) + "@" + l.SubgraphRef
			emitted[key] = true
			if confidences[l.Pattern] == nil {
				confidences[l.Pattern] = map[float64]int{}
			}
			confidences[l.Pattern][l.Confidence]++
			if wanted[key] {
				get(l.Pattern).tp++
			} else {
				get(l.Pattern).fp++
				t.Errorf("%s: FALSE POSITIVE %q on %s — not in the hand label", f.name, l.Pattern, l.SubgraphRef)
			}
		}
		for _, w := range f.want {
			if !emitted[string(w.pattern)+"@"+w.ref()] {
				get(w.pattern).fn++
				t.Errorf("%s: FALSE NEGATIVE %q on %v — hand-labeled but not emitted", f.name, w.pattern, w.nodeIDs)
			}
		}
	}

	// Report. Every structural detector must have been EXERCISED: a detector with no fixture has no
	// calibration evidence at all, and shipping it would be an untested claim.
	var report strings.Builder
	fmt.Fprintf(&report, "\n%-28s %4s %4s %4s  %-8s %-9s %s\n", "PATTERN", "TP", "FP", "FN", "RECALL", "PRECISION", "CONFIDENCE BANDS")
	for _, p := range StructuralPatterns() {
		c := get(p)
		recall, precision := 1.0, 1.0
		if c.tp+c.fn > 0 {
			recall = float64(c.tp) / float64(c.tp+c.fn)
		}
		if c.tp+c.fp > 0 {
			precision = float64(c.tp) / float64(c.tp+c.fp)
		}
		var bands []string
		for conf, n := range confidences[p] {
			bands = append(bands, fmt.Sprintf("%.2f×%d", conf, n))
		}
		sort.Strings(bands)
		fmt.Fprintf(&report, "%-28s %4d %4d %4d  %-8.2f %-9.2f %s\n", p, c.tp, c.fp, c.fn, recall, precision, strings.Join(bands, " "))
		if c.tp == 0 {
			t.Errorf("detector for %q never fired across the whole fixture set — it has no calibration evidence", p)
		}
	}
	// Reflection is structural-signature-detectable but behavioral to confirm; it is calibrated too.
	c := get(Reflection)
	fmt.Fprintf(&report, "%-28s %4d %4d %4d  (candidate, capped at %.2f)\n", Reflection, c.tp, c.fp, c.fn, BehavioralCandidateCap)
	if c.tp == 0 {
		t.Error("the Reflection candidate detector never fired across the fixture set")
	}
	t.Log(report.String())

	// The calibrated bands are the ones the package documents. A detector silently drifting to some
	// other number would make the confidence meaningless, so the permitted values are pinned.
	allowed := map[float64]bool{ConfidenceTopologyDetermined: true, ConfidenceTopologyStrong: true, BehavioralCandidateCap: true}
	for p, byConf := range confidences {
		for conf := range byConf {
			if !allowed[conf] {
				t.Errorf("%q emitted an uncalibrated confidence %.3f — use a documented band", p, conf)
			}
		}
	}
}

// Every detector that ships must appear in the fixture set with at least one near-miss aimed at it.
// Without that, "it detects X" is unfalsifiable: a predicate that returns true always would pass a
// positives-only suite.
func TestEveryStructuralDetectorHasANearMissFixture(t *testing.T) {
	guarded := map[Pattern]bool{}
	for _, f := range allFixtures() {
		for _, p := range f.mustNot {
			guarded[p] = true
		}
	}
	for _, p := range append(StructuralPatterns(), Reflection) {
		if !guarded[p] {
			t.Errorf("no fixture asserts that %q must NOT fire — the detector has no discriminative-power evidence", p)
		}
	}
}

// The dump path is part of the package, not a throwaway: it is how a per-sample bug becomes visible
// when the aggregate suite is green. Assert it actually walks every stage.
func TestDumpShowsEveryStage(t *testing.T) {
	f := fxComposite()
	out, err := Dump(context.Background(), f.ir, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"1. TOPOLOGY", "2. RULE DETECTORS", "3. ARBITRATION", "4. LABELS",
		"5. AMBIGUOUS RESIDUE", "6. DIAGNOSTICS",
		"routing.conditional_control_fanout.v1", "retrieval_rag", "n_tech",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dump is missing %q\n%s", want, out)
		}
	}
	t.Log("\n" + out)
}
