// Package evalharness is P4's measurement substrate: it runs a Variant Spec over an eval set for N
// seeds, scores every run with pluggable evaluators over the P2.5 traces, and reports every metric
// as a mean with a confidence interval.
//
// The package exists to make one law enforceable — evals before optimization. Everything downstream
// (P4.5 attribution, P5.5 verification, P6 optimizer) is a CONSUMER of what this package measures,
// so a dishonest number here is a dishonest number everywhere. Three properties are therefore
// structural rather than advisory:
//
//  1. An evaluator declares its output RANGE and the set of P3.5 patterns it is ADMISSIBLE for.
//     The harness refuses to compute an inadmissible metric on a node (a router is not scored with
//     relevance@k) and flags an out-of-range value as invalid instead of recording it. Admissibility
//     is derived from patternclassifier's metricSets table, the single source of truth for
//     pattern -> metric-set; this package never re-derives that mapping.
//
//  2. Evaluators are functions over the TRACE the substrate already emits, not over a bespoke eval
//     hook. A custom metric therefore needs no harness change, and every evaluator — built-in or
//     user-registered — sees the same tagged substrate.
//
//  3. The seven P0 tags come from the RunContext, never from the evaluator's arguments, so an
//     evaluator cannot emit an under-tagged result. This is the property telemetry's evaluator seam
//     (EvaluatorInterfaceVersion 1.0.0) froze in P2.5; AsTelemetryEvaluator adapts a P4 evaluator
//     onto it rather than opening a second emission path.
package evalharness
