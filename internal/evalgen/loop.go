package evalgen

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
)

// loop.go is task 4.3: the gap-filling loop — measure, target the gap, regenerate — iterating until
// the coverage thresholds are met or a maximum-iteration bound is hit.
//
// The bound is what makes the loop honest. An IR edge can be genuinely unreachable (a branch no
// input satisfies, dead code the discovery pass still sees). Without a bound the loop spins; with a
// bound but without a residual report it terminates and claims success. So it terminates AND reports
// exactly which obligations it could not discharge. A false "100%" here would be the most damaging
// output this package could produce, because every downstream number would then be computed over an
// eval set that never exercises the failing path.

// DefaultMaxIterations bounds the loop. Five rounds is enough for the cheap layers plus two targeted
// LLM rounds; beyond that the residual is almost always genuinely unreachable rather than merely
// unlucky.
const DefaultMaxIterations = 5

// LoopConfig configures the gap-filling loop.
type LoopConfig struct {
	Thresholds    Thresholds
	MaxIterations int
	// Generators run in order each round. Order IS the cost policy: cheap deterministic layers
	// first, the paid LLM layer pointed at whatever they could not reach.
	Generators []Generator
	// DedupeThreshold is the Jaccard similarity at or above which two cases are near-identical.
	DedupeThreshold float64
	// Floors below which the produced set is surfaced as low-confidence.
	DifficultyFloor float64
	DiversityFloor  float64
	// OracleFloor is the fraction of cases that must be able to decide success.
	OracleFloor float64
}

// DefaultLoopConfig builds the standard layered configuration.
func DefaultLoopConfig(model CaseModel) LoopConfig {
	return LoopConfig{
		Thresholds:      DefaultThresholds(),
		MaxIterations:   DefaultMaxIterations,
		DedupeThreshold: DefaultDedupeThreshold,
		DifficultyFloor: DefaultDifficultyFloor,
		DiversityFloor:  DefaultDiversityFloor,
		OracleFloor:     DefaultOracleFloor,
		Generators: []Generator{
			&SeedTraceGenerator{},
			&SchemaGenerator{},
			&LLMGenerator{Model: model},
			&AdversarialGenerator{},
		},
	}
}

// RoundLog is one iteration's record: what the gap was, which layer produced how many cases, and
// what coverage looked like afterwards. It is retained so a reader can DERIVE the final report from
// the process rather than take it on faith.
type RoundLog struct {
	Iteration int               `json:"iteration"`
	GapBefore Gap               `json:"gap_before"`
	Produced  map[string]int    `json:"produced"`
	Skipped   map[string]string `json:"skipped,omitempty"`
	Deduped   int               `json:"deduped"`
	After     CoverageReport    `json:"after"`
}

// LoopResult is the loop's full output.
type LoopResult struct {
	Cases    []evalharness.Case  `json:"cases"`
	Report   CoverageReport      `json:"report"`
	Quality  SetQuality          `json:"quality"`
	Rounds   []RoundLog          `json:"rounds"`
	Evidence map[string]Evidence `json:"-"`
	// StoppedBecause states, in words, why the loop ended.
	StoppedBecause string `json:"stopped_because"`
}

// Fill runs the gap-filling loop.
//
// Each round: probe the current set for evidence, measure coverage, extract the gap, run every
// generator against that gap in layer order, dedupe, and repeat. The loop stops when the thresholds
// are met, when a full round adds nothing new (further rounds cannot help), or at the iteration
// bound — and in the last two cases the residual is reported.
func Fill(ctx context.Context, ir *discovery.IR, seed []evalharness.Case, p Prober, cfg LoopConfig) (LoopResult, error) {
	if ir == nil {
		return LoopResult{}, errors.New("evalgen: Fill requires an IR")
	}
	if p == nil {
		return LoopResult{}, errors.New("evalgen: Fill requires a Prober — coverage is measured from execution, never from a case's declared intent")
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = DefaultMaxIterations
	}
	if cfg.Thresholds == (Thresholds{}) {
		cfg.Thresholds = DefaultThresholds()
	}

	cases := append([]evalharness.Case(nil), seed...)
	res := LoopResult{}

	for iter := 1; iter <= cfg.MaxIterations; iter++ {
		evidence, _ := ProbeAll(ctx, p, ir, cases)
		rep := Measure(ir, cases, evidence, cfg.Thresholds)
		rep.Iterations = iter - 1
		res.Evidence = evidence

		if rep.Met() {
			res.Cases, res.Report = cases, rep
			res.StoppedBecause = "coverage thresholds met"
			res.Quality = MeasureQuality(cases, cfg)
			return res, nil
		}

		gap := GapOf(rep)
		round := RoundLog{Iteration: iter, GapBefore: gap, Produced: map[string]int{}, Skipped: map[string]string{}}

		before := len(cases)
		for _, g := range cfg.Generators {
			produced, err := g.Generate(ctx, ir, gap, cases)
			if err != nil {
				if errors.Is(err, ErrGeneratorInactive) {
					// An inactive layer is a recorded fact, not a failure: P5 activates the seed
					// layer by supplying a source, and the log shows it was asked every round.
					round.Skipped[g.Name()] = err.Error()
					continue
				}
				return res, fmt.Errorf("generator %s: %w", g.Name(), err)
			}
			for i := range produced {
				produced[i].Origin = g.Origin()
			}
			cases = append(cases, produced...)
			round.Produced[g.Name()] = len(produced)
		}

		deduped, removed := Dedupe(cases, cfg.dedupeThreshold())
		round.Deduped = removed
		cases = deduped

		evidence, _ = ProbeAll(ctx, p, ir, cases)
		after := Measure(ir, cases, evidence, cfg.Thresholds)
		after.Iterations = iter
		round.After = after
		res.Rounds = append(res.Rounds, round)
		res.Evidence = evidence
		res.Cases, res.Report = cases, after

		if after.Met() {
			res.StoppedBecause = "coverage thresholds met"
			res.Quality = MeasureQuality(cases, cfg)
			return res, nil
		}
		if len(cases) == before {
			// A full round that added nothing cannot be improved by repeating it. Terminate here and
			// report the residual rather than burning the remaining iterations (and the LLM budget)
			// on an identical no-op.
			res.StoppedBecause = fmt.Sprintf(
				"no generator could produce a new case for the remaining gap after %d iteration(s); residual reported as unreachable", iter)
			markUnreachable(&res.Report)
			res.Quality = MeasureQuality(cases, cfg)
			return res, nil
		}
	}

	res.StoppedBecause = fmt.Sprintf("max-iteration bound (%d) reached with coverage below threshold; residual reported", cfg.MaxIterations)
	markUnreachable(&res.Report)
	res.Quality = MeasureQuality(res.Cases, cfg)
	return res, nil
}

func (c LoopConfig) dedupeThreshold() float64 {
	if c.DedupeThreshold <= 0 {
		return DefaultDedupeThreshold
	}
	return c.DedupeThreshold
}

// markUnreachable flags every still-uncovered obligation as residual/unreachable. The obligations
// stay IN the denominator: removing them would raise the achieved percentage by deleting the
// evidence of failure, which is exactly the false 100% the spec forbids.
func markUnreachable(rep *CoverageReport) {
	for _, d := range []*Dimension{&rep.Path, &rep.Node, &rep.EdgeCase} {
		for i := range d.Items {
			if !d.Items[i].Covered {
				d.Items[i].Unreachable = true
			}
		}
	}
	rep.Residual = append(append(rep.Path.Uncovered(), rep.Node.Uncovered()...), rep.EdgeCase.Uncovered()...)
	sort.Strings(rep.Residual)
}
