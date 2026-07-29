package verification

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalstats"
)

// TestAuthoredDowngradeFailingGuardrailIsRegression (P13 13c task 10.3, FR39).
//
// The tempting bug this guards is not a crash — it is a sentence. A cheaper model whose held-out
// quality interval does NOT overlap the incumbent's has measurably regressed, and the natural way to
// report it ("quality dipped slightly, but we saved 40% on cost") is how a regression gets merged. So
// the claim is `regression`, the equal-quality phrase is withheld, and the cost saving is not offered as
// a win beside it.
func TestAuthoredDowngradeFailingGuardrailIsRegression(t *testing.T) {
	// A real, measured verdict: the cheaper model lost quality and saved cost.
	v := Verdict{
		Metric:      "task_success",
		Delta:       evalstats.Interval{Mean: -0.31, Low: -0.44, High: -0.18},
		Significant: true,
		HeldOut:     true,
		CostDelta:   -0.42, // cheaper
	}

	got := ReportAuthored(v, true, GuardrailFailed)

	if got.Quality != ClaimRegression {
		t.Fatalf("quality = %q, want %q", got.Quality, ClaimRegression)
	}
	if got.EqualQuality {
		t.Error("a failed guardrail was reported as equal-quality — the one phrase it may never carry")
	}
	if got.CostWin {
		t.Error("a measured quality regression was paired with a cost win; that pairing is how a regression ships")
	}
	if !strings.Contains(strings.ToLower(got.Reason), "regression") {
		t.Errorf("the reason does not say regression: %q", got.Reason)
	}
	// The narration must not soften it with the cost saving.
	for _, softener := range []string{"but", "however", "saving", "cheaper model is fine"} {
		if strings.Contains(strings.ToLower(got.Reason), softener) && softener != "cheaper" {
			t.Errorf("the reason softens the regression with %q: %q", softener, got.Reason)
		}
	}
}

// TestAuthoredClearedGuardrailIsTieAndCostWin: the other side of the same rule. Cleared means the
// platform cannot tell the models apart — a quality TIE and a cost win, never a quality win.
func TestAuthoredClearedGuardrailIsTieAndCostWin(t *testing.T) {
	v := Verdict{Metric: "task_success", Delta: evalstats.Interval{Mean: -0.01, Low: -0.06, High: 0.04},
		HeldOut: true, CostDelta: -0.35}

	got := ReportAuthored(v, true, GuardrailCleared)

	if got.Quality != ClaimTie {
		t.Errorf("quality = %q, want %q", got.Quality, ClaimTie)
	}
	if got.Quality == ClaimWin {
		t.Error("an overlapping interval was reported as a quality win — the overlap denies that measurement")
	}
	if !got.CostWin {
		t.Error("a cleared downgrade with strictly lower cost is a cost win")
	}
	if !got.EqualQuality {
		t.Error("a cleared guardrail is exactly what equal-quality means")
	}
}

// TestUnverifiedAuthoredChangeClaimsNothing is the honesty default (FR25).
//
// It is asserted field by field rather than as one struct comparison, because each field is a separate
// promise and a future contributor is far more likely to relax one of them than all four.
func TestUnverifiedAuthoredChangeClaimsNothing(t *testing.T) {
	// Even handed a verdict that LOOKS like a win, an un-run change claims nothing.
	tempting := Verdict{Metric: "task_success", Delta: evalstats.Interval{Mean: 0.4, Low: 0.2, High: 0.6},
		Significant: true, CostDelta: -0.5}

	got := ReportAuthored(tempting, false, GuardrailNotRun)

	if got.Quality != ClaimUnmeasured {
		t.Errorf("quality = %q, want %q — the harness never ran", got.Quality, ClaimUnmeasured)
	}
	if got.CostWin {
		t.Error("an unverified change claimed a cost win — tokens not spent are not a saving until something measured what they bought")
	}
	if got.EqualQuality {
		t.Error("an unverified change claimed equal quality")
	}
	if got.Countable {
		t.Error("an unverified change is countable — it would enter every aggregate improvement figure")
	}
	if got.Reason == "" {
		t.Error("an unverified change with no reason reads as 'the tool did not work'")
	}
}

// TestAuthoredReportUsesTheOneVerdictType: there is no authored verdict. ReportAuthored consumes the
// same Verdict every other candidate produces, which is what keeps one definition of "better".
func TestAuthoredReportUsesTheOneVerdictType(t *testing.T) {
	// A win with no guardrail reads exactly as any other candidate's win.
	win := Verdict{Metric: "task_success", Delta: evalstats.Interval{Mean: 0.2, Low: 0.05, High: 0.35},
		Significant: true}
	if got := ReportAuthored(win, true, GuardrailNotRun); got.Quality != ClaimWin {
		t.Errorf("quality = %q, want %q", got.Quality, ClaimWin)
	}
	loss := Verdict{Metric: "task_success", Delta: evalstats.Interval{Mean: -0.2, Low: -0.35, High: -0.05},
		Significant: true}
	if got := ReportAuthored(loss, true, GuardrailNotRun); got.Quality != ClaimRegression {
		t.Errorf("quality = %q, want %q", got.Quality, ClaimRegression)
	}
	tie := Verdict{Metric: "task_success", Delta: evalstats.Interval{Mean: 0.01, Low: -0.1, High: 0.12}}
	if got := ReportAuthored(tie, true, GuardrailNotRun); got.Quality != ClaimTie {
		t.Errorf("quality = %q, want %q", got.Quality, ClaimTie)
	}
}
