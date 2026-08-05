package evalboard

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/scoring"
)

// p27_board_recording_test.go is P27 task 9.2: scoring, confidence intervals, tie determination and
// Pareto dominance are unchanged by the account system.
//
// # Why a RECORDING and not a re-computation
//
// The obvious version of this test builds a board, builds it again, and asserts the two agree. That
// test passes on every P27 tree, including one where ownership changed every number on the board — both
// sides move together. What it actually asserts is that Build is a function, which nobody doubted.
//
// So the reference is BYTES, produced by the code that existed before P27 and checked in. The fixture in
// testdata/ was written by running this same file in a worktree at the pre-P27 commit (see RECORDING
// below); this tree only ever reads it. A measurement that moved has nowhere to hide: the pre-P27
// numbers are sitting in the repository, and nothing in this tree can regenerate them.
//
// # RECORDING
//
// The fixture is not regenerated as a matter of course. Re-recording it in THIS tree would destroy the
// only evidence the test carries. It is re-recorded only from a pre-P27 checkout:
//
//	git worktree add /tmp/pre-p27 <pre-P27-commit> --detach
//	cp internal/evalboard/p27_board_recording_test.go /tmp/pre-p27/internal/evalboard/
//	cd /tmp/pre-p27 && GOWORK=off P27_RECORD_PRE=1 go test ./internal/evalboard/ -run TestPreP27Board
//	cp /tmp/pre-p27/internal/evalboard/testdata/p27-pre-board.json internal/evalboard/testdata/
//
// The env var rather than a -update flag is deliberate: a flag is something a developer reaches for
// when a test goes red, and this is the one test where making it green that way is the bug.

const preBoardFixture = "testdata/p27-pre-board.json"

// recordingBoard is the fixture the reference was recorded from. It is deliberately not a happy board:
// it carries a statistical TIE (the thing 9.2 names that is easiest to break silently), a variant
// DISQUALIFIED by a gate, and a Pareto frontier with both a dominated point and a non-dominated cheap
// one. A recording of a board where nothing interesting happens would agree with almost any change.
//
// Everything about it is seeded. The observations come from a fixed-seed RNG and the bootstrap runs at
// evalstats.DefaultConfig's fixed RNGSeed, so the intervals are reproducible numbers rather than
// approximately-equal ones — which is what lets this be a byte comparison at all.
func recordingBoard(t *testing.T) View {
	t.Helper()
	return buildView(t, []scoring.Variant{
		// alpha and beta are the SAME configuration measured twice — identical means, different
		// observation noise. The tie the overlap test must keep finding.
		//
		// They were first written as near-but-not-equal (0.900/0.901, and a cent apart on cost), and the
		// recording came back with no tie at all: the composite blends quality, cost and latency, so a
		// difference too small to see on any one axis still separated the intervals once all three were
		// weighted. Which is the argument for recording the fixture and LOOKING at it — a board that was
		// supposed to exercise tie determination did not, and nothing but reading it would have said so.
		recVariant("v-alpha", 0.900, 0.0200, 900, 101),
		recVariant("v-beta", 0.900, 0.0200, 900, 202),
		// gamma is much cheaper and much worse — the frontier's third non-dominated point.
		recVariant("v-gamma", 0.700, 0.0020, 300, 303),
		// epsilon is worse than alpha on ALL THREE axes while still clearing the gate: the DOMINATED
		// point. It has to pass the gate to be here at all — the frontier is drawn over the ranked set,
		// so a disqualified variant is absent rather than dominated, and a fixture whose only bad variant
		// was disqualified produced a frontier on which nothing was dominated by anything.
		recVariant("v-epsilon", 0.800, 0.0300, 1200, 505),
		// 🔴 zeta and eta exist because the frontier assertion was DEMONSTRATED INSENSITIVE without them.
		// Deleting the latency term from scoring's dominance rule left this board byte-identical, and the
		// test reported nothing: every domination relation in the fixture happened to hold on quality and
		// cost alone, so two thirds of the rule were never consulted.
		//
		// Each of these is worse than alpha on two axes and BETTER on the third, so it sits on the
		// frontier only because all three are weighed. zeta is faster and dearer; eta is cheaper and
		// slower. Drop latency from the rule and zeta collapses; drop cost and eta does. (Quality was
		// already covered — gamma is worse on it and better on both others.)
		recVariant("v-zeta", 0.850, 0.0250, 600, 606),
		recVariant("v-eta", 0.850, 0.0150, 1100, 707),
		// delta is below the gate: the disqualified variant, out of the ranking entirely.
		recVariant("v-delta", 0.300, 0.0500, 1500, 404),
	}, scoring.GateSet{Name: "prod", MinQuality: f(0.50)}, Input{
		WorkflowID: "wf-p27-recording",
		Profile:    scoring.Balanced(),
		Progress:   Progress{UnitsPlanned: 7, UnitsCompleted: 7, SeedFloor: 5},
		Labels: map[string]string{
			"v-alpha": "alpha", "v-beta": "beta", "v-gamma": "gamma", "v-epsilon": "epsilon",
			"v-zeta": "zeta", "v-eta": "eta", "v-delta": "delta",
		},
	})
}

// recVariant is this file's own variant builder rather than view_test.go's. The fixture's numbers are
// only meaningful if the inputs that produced them are pinned HERE, in the file that gets copied into
// the pre-P27 checkout — a helper edited elsewhere would silently re-point the recording at a different
// board and the byte comparison would report it as a measurement change.
func recVariant(id string, quality, cost, latency float64, seed int64) scoring.Variant {
	return scoring.Variant{
		VariantID:  id,
		ConfigHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Label:      id,
		Providers:  []string{"anthropic"},
		Metrics: map[string]evalstats.Series{
			evalharness.MetricTaskSuccess:   recSeries(id, evalharness.MetricTaskSuccess, quality, 0.02, seed),
			evalharness.MetricRunCostUSD:    recSeries(id, evalharness.MetricRunCostUSD, cost, 0.0005, seed+1),
			evalharness.MetricRunLatencyMS:  recSeries(id, evalharness.MetricRunLatencyMS, latency, 10, seed+2),
			evalharness.MetricReliability:   recSeries(id, evalharness.MetricReliability, 0.98, 0.005, seed+3),
			evalharness.MetricToolErrorRate: recSeries(id, evalharness.MetricToolErrorRate, 0.01, 0.002, seed+4),
			evalharness.MetricRunTokens:     recSeries(id, evalharness.MetricRunTokens, 800, 20, seed+5),
		},
	}
}

func recSeries(variantID, metric string, mean, noise float64, seed int64) evalstats.Series {
	rng := rand.New(rand.NewSource(seed))
	s := evalstats.Series{VariantID: variantID, Metric: metric}
	for c := 0; c < 12; c++ {
		for sd := 0; sd < 5; sd++ {
			s.Obs = append(s.Obs, evalstats.Observation{
				CaseID: "case-" + string(rune('a'+c)), Seed: int64(sd),
				Value: mean + (rng.Float64()-0.5)*2*noise,
			})
		}
	}
	return s
}

// TestPreP27BoardIsReproducedExactly is the whole of 9.2 in one comparison, with four named
// sub-assertions in front of it so a failure says WHICH measurement moved rather than pointing at a
// 300-line diff.
func TestPreP27BoardIsReproducedExactly(t *testing.T) {
	got := recordingBoard(t)

	if os.Getenv("P27_RECORD_PRE") == "1" {
		writeRecording(t, got)
		t.Skip("recorded the pre-P27 board; this mode must only ever run in a pre-P27 checkout")
	}

	want := readRecording(t)

	// ── scoring ──────────────────────────────────────────────────────────────────────────────────
	// Rank order first: it is what a user reads, and a change here is a change in the platform's
	// answer even when every number behind it is intact.
	if len(got.Ranked) != len(want.Ranked) {
		t.Fatalf("the ranked set changed size: %d rows now, %d before P27", len(got.Ranked), len(want.Ranked))
	}
	for i := range want.Ranked {
		w, g := want.Ranked[i], got.Ranked[i]
		if g.VariantID != w.VariantID || g.Rank != w.Rank {
			t.Errorf("rank %d: %s before P27, %s (rank %d) now — the ordering moved",
				w.Rank, w.VariantID, g.VariantID, g.Rank)
			continue
		}
		if g.Score != w.Score {
			t.Errorf("%s composite score: %.17g before P27, %.17g now", w.VariantID, w.Score, g.Score)
		}
		// ── confidence intervals ─────────────────────────────────────────────────────────────
		// Both bounds and the counts behind them. An interval that kept its bounds while n changed
		// is a different claim made with the same numbers.
		if g.CILow != w.CILow || g.CIHigh != w.CIHigh {
			t.Errorf("%s interval: [%.17g, %.17g] before P27, [%.17g, %.17g] now",
				w.VariantID, w.CILow, w.CIHigh, g.CILow, g.CIHigh)
		}
		if g.NSeeds != w.NSeeds || g.NCases != w.NCases || g.Method != w.Method {
			t.Errorf("%s interval provenance: %d seeds/%d cases/%s before P27, %d/%d/%s now",
				w.VariantID, w.NSeeds, w.NCases, w.Method, g.NSeeds, g.NCases, g.Method)
		}
		// ── tie determination ────────────────────────────────────────────────────────────────
		if !sameStrings(g.TiedWith, w.TiedWith) {
			t.Errorf("%s tied_with: %v before P27, %v now", w.VariantID, w.TiedWith, g.TiedWith)
		}
		if g.GatePass != w.GatePass {
			t.Errorf("%s gate verdict: pass=%v before P27, pass=%v now", w.VariantID, w.GatePass, g.GatePass)
		}
	}
	if got.AllTie != want.AllTie || got.TieAnalysis != want.TieAnalysis {
		t.Errorf("board tie state: all_tie=%v/%s before P27, all_tie=%v/%s now",
			want.AllTie, want.TieAnalysis, got.AllTie, got.TieAnalysis)
	}
	// The disqualified set is part of the answer too — a variant quietly moving between the two lists
	// changes what the board says about it without changing either list's contents.
	if len(got.Disqualified) != len(want.Disqualified) {
		t.Errorf("the disqualified set changed size: %d before P27, %d now",
			len(want.Disqualified), len(got.Disqualified))
	}

	// ── Pareto dominance ─────────────────────────────────────────────────────────────────────────
	if len(got.Pareto) != len(want.Pareto) {
		t.Fatalf("the frontier changed size: %d points before P27, %d now", len(want.Pareto), len(got.Pareto))
	}
	if got.CostLatency != want.CostLatency {
		t.Errorf("cost/latency analysis: %s before P27, %s now", want.CostLatency, got.CostLatency)
	}
	for i := range want.Pareto {
		w, g := want.Pareto[i], got.Pareto[i]
		if g.VariantID != w.VariantID {
			t.Errorf("frontier point %d: %s before P27, %s now", i, w.VariantID, g.VariantID)
			continue
		}
		if g.NonDominated != w.NonDominated {
			t.Errorf("%s dominance: non_dominated=%v before P27, %v now — the frontier changed shape",
				w.VariantID, w.NonDominated, g.NonDominated)
		}
		if g.Quality != w.Quality || g.CostUSD != w.CostUSD || g.LatencyMS != w.LatencyMS || g.Composite != w.Composite {
			t.Errorf("%s frontier coordinates moved: (q=%.17g c=%.17g l=%.17g comp=%.17g) before P27, (q=%.17g c=%.17g l=%.17g comp=%.17g) now",
				w.VariantID, w.Quality, w.CostUSD, w.LatencyMS, w.Composite,
				g.Quality, g.CostUSD, g.LatencyMS, g.Composite)
		}
	}

	// ── the backstop ─────────────────────────────────────────────────────────────────────────────
	// Everything above names a field. This names none, which is the point: a board field that did not
	// exist when the assertions above were written is exactly the kind of thing P27 could have added,
	// and a per-field test cannot notice a field nobody thought to check.
	gotJSON := mustMarshal(t, got)
	wantJSON := mustMarshal(t, want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("the board differs from the pre-P27 recording in a field the assertions above do not cover.\n"+
			"first difference at byte %d:\n  before P27: %s\n  now:        %s",
			firstDiff(wantJSON, gotJSON), excerpt(wantJSON, firstDiff(wantJSON, gotJSON)), excerpt(gotJSON, firstDiff(wantJSON, gotJSON)))
	}
}

// TestTheRecordedBoardActuallyExercisesWhat92Names keeps the recording honest. A fixture that drifted
// into a single-variant board with no tie and no gate failure would still compare equal to itself
// forever, and 9.2 would be discharged by a test that measures nothing.
func TestTheRecordedBoardActuallyExercisesWhat92Names(t *testing.T) {
	want := readRecording(t)

	tied := 0
	for _, r := range want.Ranked {
		if len(r.TiedWith) > 0 {
			tied++
		}
	}
	if tied < 2 {
		t.Errorf("the recording contains %d tied rows; tie determination is one of the four things 9.2 "+
			"asserts, and a board with no tie does not test it", tied)
	}
	if len(want.Disqualified) == 0 {
		t.Error("the recording disqualifies nothing, so it does not exercise the gate that moves a variant " +
			"out of the ranking")
	}
	if want.TieAnalysis != TieComputed {
		t.Errorf("tie_analysis = %s: the recording must be a board that COULD test for ties, or the tie "+
			"assertions above compare two absences", want.TieAnalysis)
	}
	// Dominance is only tested where the coordinates it ranks are real. This asserts the COORDINATES
	// rather than the CostLatency field on purpose: Build never sets that field (see
	// TestBuildLeavesCostLatencyUnset below), so reading it here would report "unavailable" about a
	// frontier whose points all carry a measured cost and latency.
	dominated, free := 0, 0
	for _, p := range want.Pareto {
		if p.CostUSD == 0 || p.LatencyMS == 0 {
			t.Errorf("frontier point %s has a zero coordinate (cost=%v latency=%v); dominance over a "+
				"dimension a point lacks is not defined, so this recording would compare zeros",
				p.VariantID, p.CostUSD, p.LatencyMS)
		}
		if p.NonDominated {
			free++
		} else {
			dominated++
		}
	}
	if free < 2 || dominated < 1 {
		t.Errorf("the recorded frontier has %d non-dominated and %d dominated points; dominance is only "+
			"tested by a frontier that has both", free, dominated)
	}
}

// TestBuildLeavesCostLatencyUnset records a defect this task FOUND and deliberately did not fix.
//
// 🔴 `CostLatency` is documented as a two-valued state — "measured" or "unavailable" — and Build never
// assigns it, so a locally computed board ships the empty string. That is precisely the failure
// hostedboard's own Build guards against in a comment three files away: "an empty string is not one of
// the two states, and a UI switching on it would fall through to whichever branch it wrote first." One
// assembler holds the discipline and the other does not.
//
// It is pinned rather than repaired because this section's job is to prove the measurement did not move
// across P27, and it did not: the empty string is what pre-P27 emitted and it is what this tree emits.
// Fixing it here would change the board while the change under review is an account system, and the
// recording above would report the repair as a P27 regression. It belongs to whoever owns the board.
//
// The test exists so the finding cannot be lost: if someone does fix it, this goes red and points at
// the recording that also needs re-taking.
func TestBuildLeavesCostLatencyUnset(t *testing.T) {
	v := recordingBoard(t)
	if v.CostLatency != "" {
		t.Fatalf("cost_latency = %q. If this was FIXED, that is good news and this test is now the "+
			"obsolete half of a known defect — delete it, and re-record testdata/p27-pre-board.json from "+
			"a checkout that also carries the fix. Do not re-record it from this tree.", v.CostLatency)
	}
	// The frontier itself is fine — every point carries a real cost and latency. Only the field that
	// tells the UI whether to trust them was never filled in.
	for _, p := range v.Pareto {
		if p.CostUSD == 0 || p.LatencyMS == 0 {
			t.Fatalf("%s: the defect is the STATE field, not the coordinates; a zero here means something "+
				"else broke", p.VariantID)
		}
	}
}

func writeRecording(t *testing.T, v View) {
	t.Helper()
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	if err := os.WriteFile(preBoardFixture, append(mustMarshal(t, v), '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", preBoardFixture, err)
	}
	t.Logf("recorded %s", filepath.Clean(preBoardFixture))
}

func readRecording(t *testing.T) View {
	t.Helper()
	raw, err := os.ReadFile(preBoardFixture)
	if err != nil {
		t.Fatalf("read the pre-P27 recording: %v\n"+
			"This fixture is EVIDENCE, not a cache. It is re-recorded from a pre-P27 checkout only — see "+
			"this file's header. Do not regenerate it here.", err)
	}
	var v View
	dec := json.NewDecoder(bytes.NewReader(raw))
	// A field P27 REMOVED from the view would otherwise decode as a zero value and compare equal to a
	// board that no longer emits it.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("the pre-P27 recording no longer fits evalboard.View: %v\n"+
			"That is itself a finding: the board's shape changed across P27.", err)
	}
	return v
}

func mustMarshal(t *testing.T, v View) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal board: %v", err)
	}
	return b
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func firstDiff(a, b []byte) int {
	n := int(math.Min(float64(len(a)), float64(len(b))))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func excerpt(b []byte, at int) string {
	lo := at - 60
	if lo < 0 {
		lo = 0
	}
	hi := at + 60
	if hi > len(b) {
		hi = len(b)
	}
	return string(b[lo:hi])
}
