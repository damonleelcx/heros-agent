package hostedboard

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalboard"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// frontier_test.go is the regression fence for a defect found by linking a real run and then reading
// the console it produced.
//
// A run reporting cost $0.2957 and latency 15,460 ms rendered on the eval board as `$0.00` and `0 ms`,
// under an axis labelled "Cost (USD) — lower is better" spanning $-1.00 to $1.00, with the point
// labelled "on the frontier: nothing beats it on both quality and cost". Quality on the same row was
// correct. The assembler had deliberately declined to compute a cost frontier — and expressed that
// refusal by leaving two float64s at zero, which the view then rendered as measurements.
//
// The scores were never missing. `cost_usd` and `latency_ms` ride in the same Scores slice as
// `quality`, and spendOf in this very file has always read `cost_usd` off them.

// withScores replaces the run's scores wholesale, for the cases where a metric must be ABSENT.
func withScores(scores ...runlink.Score) func(*linkingest.LinkedRun) {
	return func(lr *linkingest.LinkedRun) { lr.Scores = scores }
}

func pointFor(t *testing.T, v evalboard.View, variantID string) evalboard.ParetoPoint {
	t.Helper()
	for _, p := range v.Pareto {
		if p.VariantID == variantID {
			return p
		}
	}
	t.Fatalf("no pareto point for %q (have %d)", variantID, len(v.Pareto))
	return evalboard.ParetoPoint{}
}

// ── the defect itself ───────────────────────────────────────────────────────────────────────────

func TestReportedCostAndLatencyReachTheFrontier(t *testing.T) {
	// The exact numbers from the run that exposed this.
	v := Build("wf", []linkingest.LinkedRun{
		run("5eb397521d07aaaa", 0.8139, runlink.GatePass, withScores(
			runlink.Score{Metric: "quality", Value: 0.8139, CILow: 0.7639, CIHigh: 0.8611},
			runlink.Score{Metric: "cost_usd", Value: 0.2957294117647059},
			runlink.Score{Metric: "latency_ms", Value: 15459.985337243397},
		)),
	})

	if v.CostLatency != evalboard.CostLatencyMeasured {
		t.Fatalf("cost_latency = %q, want %q — the run reported both", v.CostLatency, evalboard.CostLatencyMeasured)
	}
	p := pointFor(t, v, "5eb397521d07aaaa")
	if p.CostUSD == 0 {
		t.Error("cost reached the frontier as 0 — the run reported $0.2957")
	}
	if p.LatencyMS == 0 {
		t.Error("latency reached the frontier as 0 — the run reported 15459.99 ms")
	}
	if got, want := p.CostUSD, 0.2957294117647059; got != want {
		t.Errorf("cost = %v, want %v", got, want)
	}
	if got, want := p.LatencyMS, 15459.985337243397; got != want {
		t.Errorf("latency = %v, want %v", got, want)
	}
}

// ── the frontier is a real one when it claims to be ─────────────────────────────────────────────

func TestCheaperAtEqualQualityDominates(t *testing.T) {
	// Same quality and latency; one costs a tenth as much. Under quality-only ranking BOTH were on the
	// frontier — which is how "nothing beats it on both quality and cost" became false on a real board.
	v := Build("wf", []linkingest.LinkedRun{
		run("aaaa000000001111", 0.90, runlink.GatePass, withScores(
			runlink.Score{Metric: "quality", Value: 0.90},
			runlink.Score{Metric: "cost_usd", Value: 1.00},
			runlink.Score{Metric: "latency_ms", Value: 500},
		)),
		run("bbbb000000002222", 0.90, runlink.GatePass, withScores(
			runlink.Score{Metric: "quality", Value: 0.90},
			runlink.Score{Metric: "cost_usd", Value: 0.10},
			runlink.Score{Metric: "latency_ms", Value: 500},
		)),
	})

	if v.CostLatency != evalboard.CostLatencyMeasured {
		t.Fatalf("cost_latency = %q, want measured", v.CostLatency)
	}
	if expensive := pointFor(t, v, "aaaa000000001111"); expensive.NonDominated {
		t.Error("the 10x more expensive variant at identical quality and latency is on the frontier")
	}
	if cheap := pointFor(t, v, "bbbb000000002222"); !cheap.NonDominated {
		t.Error("the cheaper variant at identical quality and latency is NOT on the frontier")
	}
}

func TestBetterOnOneAxisAndWorseOnAnotherAreBothOnTheFrontier(t *testing.T) {
	// The case a frontier exists for: neither dominates. A ranking would have to pick one; a frontier
	// must not.
	v := Build("wf", []linkingest.LinkedRun{
		run("aaaa000000001111", 0.95, runlink.GatePass, withScores(
			runlink.Score{Metric: "quality", Value: 0.95},
			runlink.Score{Metric: "cost_usd", Value: 1.00},
			runlink.Score{Metric: "latency_ms", Value: 500},
		)),
		run("bbbb000000002222", 0.80, runlink.GatePass, withScores(
			runlink.Score{Metric: "quality", Value: 0.80},
			runlink.Score{Metric: "cost_usd", Value: 0.10},
			runlink.Score{Metric: "latency_ms", Value: 500},
		)),
	})
	for _, id := range []string{"aaaa000000001111", "bbbb000000002222"} {
		if p := pointFor(t, v, id); !p.NonDominated {
			t.Errorf("%s was dominated, but it is better than the other on one axis", id)
		}
	}
}

// ── the refusal, when it is genuinely warranted ─────────────────────────────────────────────────

func TestOneVariantMissingLatencyMakesTheWholeAnalysisUnavailable(t *testing.T) {
	v := Build("wf", []linkingest.LinkedRun{
		run("aaaa000000001111", 0.95, runlink.GatePass),
		run("bbbb000000002222", 0.80, runlink.GatePass, withScores(
			runlink.Score{Metric: "quality", Value: 0.80},
			runlink.Score{Metric: "cost_usd", Value: 0.10},
			// latency_ms absent.
		)),
	})

	if v.CostLatency != evalboard.CostLatencyUnavailable {
		t.Fatalf("cost_latency = %q, want unavailable — one variant reported no latency", v.CostLatency)
	}
	// 🔴 And the zeros must not be dressed as measurements: when the state is unavailable, NOTHING
	// carries a cost. A frontier half-populated with real costs and half with zeros would rank the
	// unreported ones as free, which is worse than the defect this replaces.
	for _, p := range v.Pareto {
		if p.CostUSD != 0 || p.LatencyMS != 0 {
			t.Errorf("%s carries cost=%v latency=%v while the analysis is unavailable — a partly-filled "+
				"frontier ranks unreported variants as free", p.VariantID, p.CostUSD, p.LatencyMS)
		}
	}
	// Quality-only fallback still marks the best.
	if p := pointFor(t, v, "aaaa000000001111"); !p.NonDominated {
		t.Error("the highest-quality variant is not marked under the quality-only fallback")
	}
}

func TestUnavailableFrontierSaysSoInANote(t *testing.T) {
	// The old code's comment ended "...and the note says the board is quality-ordered". No such note was
	// ever appended. This is that note.
	v := Build("wf", []linkingest.LinkedRun{
		run("aaaa000000001111", 0.95, runlink.GatePass, withScores(
			runlink.Score{Metric: "quality", Value: 0.95},
		)),
	})
	if v.CostLatency != evalboard.CostLatencyUnavailable {
		t.Fatalf("cost_latency = %q, want unavailable", v.CostLatency)
	}
	joined := strings.Join(v.Notes, "\n")
	if !strings.Contains(joined, "no cost/quality frontier was computed") {
		t.Errorf("the board does not say the frontier was not computed.\nnotes:\n%s", joined)
	}
}

// ── the state is never the empty string ─────────────────────────────────────────────────────────

func TestCostLatencyIsAlwaysOneOfTheTwoStates(t *testing.T) {
	cases := map[string][]linkingest.LinkedRun{
		"no runs at all":     {},
		"one measured run":   {run("aaaa000000001111", 0.9, runlink.GatePass)},
		"only a failed gate": {run("aaaa000000001111", 0.9, runlink.GateFail)},
		"only unmeasured": {func() linkingest.LinkedRun {
			lr := run("aaaa000000001111", 0.9, runlink.GatePass)
			lr.Eval = runlink.EvalSummary{}
			return lr
		}()},
	}
	for name, runs := range cases {
		t.Run(name, func(t *testing.T) {
			v := Build("wf", runs)
			switch v.CostLatency {
			case evalboard.CostLatencyMeasured, evalboard.CostLatencyUnavailable:
			default:
				t.Errorf("cost_latency = %q — a UI switching on this falls through to whichever "+
					"branch was written first", v.CostLatency)
			}
		})
	}
}
