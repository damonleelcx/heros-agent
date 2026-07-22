// Package scoring is P4 Decision 6: scoring is a strict pipeline — normalize, gate, weight — with a
// per-variant score cache so switching the weight profile re-ranks WITHOUT re-executing anything.
//
// Three failures this shape prevents:
//
//   - Raw units are not comparable. Dollars and percent cannot be summed, so every metric is
//     normalized to [0,1] before it enters the weighted sum.
//   - Gates-as-penalties let a cheap-but-broken arrangement top a cost-weighted board. So a hard
//     constraint DISQUALIFIES — it removes the variant from the ranked order — rather than
//     subtracting points, and gates are evaluated BEFORE the weighted sum. Soft preferences are only
//     meaningful among variants that already clear the hard floor.
//   - Re-scoring to change priorities is slow and expensive. Caching the normalized values and the
//     bootstrap replicates means a profile switch is a weighted sum over cached numbers: zero new
//     runs, and fast enough that a user explores quality-first vs. cost-optimized freely.
package scoring

import (
	"errors"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// Role is the slot a metric occupies in the composite formula. It is declared per metric rather than
// inferred from the metric's name, because inferring "anything containing cost is the cost term" is
// the kind of name-guessing that eventually puts a token count in the dollars slot.
type Role string

const (
	RoleQuality     Role = "quality"
	RoleCost        Role = "cost"
	RoleLatency     Role = "latency"
	RoleReliability Role = "reliability"
	// RoleInformational metrics are normalized, cached and shown, but do not enter the weighted sum.
	RoleInformational Role = "informational"
)

// MetricSpec declares one metric's role and direction.
type MetricSpec struct {
	Name      string              `json:"name"`
	Role      Role                `json:"role"`
	Direction evalstats.Direction `json:"direction"`
	// Unit is the RAW unit ($, ms, ratio). Carried so a board row can print "$0.021" beside "0.72"
	// — a user acts on the dollars and understands the ranking through the normalized value.
	Unit string `json:"unit"`
	// JudgeStanding is non-nil when this metric comes from an LLM judge. It is what the gate layer
	// consults before allowing the metric to disqualify anything.
	JudgeStanding *evalharness.JudgeStanding `json:"judge_standing,omitempty"`
}

// DefaultSpecs is the standard family's role assignment.
func DefaultSpecs() []MetricSpec {
	return []MetricSpec{
		{Name: evalharness.MetricTaskSuccess, Role: RoleQuality, Direction: evalstats.HigherIsBetter, Unit: telemetry.UnitRatio},
		{Name: evalharness.MetricRunCostUSD, Role: RoleCost, Direction: evalstats.LowerIsBetter, Unit: telemetry.UnitUSD},
		{Name: evalharness.MetricRunLatencyMS, Role: RoleLatency, Direction: evalstats.LowerIsBetter, Unit: telemetry.UnitMS},
		{Name: evalharness.MetricReliability, Role: RoleReliability, Direction: evalstats.HigherIsBetter, Unit: telemetry.UnitRatio},
		{Name: evalharness.MetricToolErrorRate, Role: RoleInformational, Direction: evalstats.LowerIsBetter, Unit: telemetry.UnitRatio},
		{Name: evalharness.MetricRunTokens, Role: RoleInformational, Direction: evalstats.LowerIsBetter, Unit: telemetry.UnitTokens},
	}
}

// Variant is one candidate on the board, with its measured series per metric.
type Variant struct {
	VariantID  string `json:"variant_id"`
	ConfigHash string `json:"config_hash"`
	Label      string `json:"label"`
	// Providers is every provider this configuration calls, for the provider-allowlist gate.
	Providers []string `json:"providers"`
	// Metrics is metric name -> the multi-seed observations behind it.
	Metrics map[string]evalstats.Series `json:"-"`
	// WeakCaseIDs are the weak-labeled cases that contributed to these numbers. Carried so the
	// board can show a score as weak-labeled and so a gate can refuse to be driven by one.
	WeakCaseIDs []string `json:"weak_case_ids,omitempty"`
	// LowConfidence is the eval set's own verdict on itself (difficulty/diversity below floor).
	LowConfidence bool `json:"low_confidence"`
}

// Board is the input to scoring: an eval set, a metric vocabulary, and the variants measured on it.
type Board struct {
	// EvalSetHash pins WHICH eval set produced these numbers. Together with each variant's
	// config_hash it is what makes a leaderboard attributable to an exact measurement.
	EvalSetHash string       `json:"eval_set_hash"`
	Specs       []MetricSpec `json:"specs"`
	Variants    []Variant    `json:"variants"`
}

var (
	// ErrNoVariants means there is nothing to rank.
	ErrNoVariants = errors.New("scoring: board carries no variants")
	// ErrMissingMetric means a variant lacks a metric the board declares. It is an error rather than
	// a zero, because a missing quality number scored as zero quality would disqualify a variant
	// nobody measured.
	ErrMissingMetric = errors.New("scoring: variant is missing a declared metric")
)

// ─────────────────────────────────────────────────────────────────────────────
// Weight profiles
// ─────────────────────────────────────────────────────────────────────────────

// Profile is a named set of weights. Named rather than free-floating numbers so two people
// comparing boards are demonstrably comparing under the same priorities.
type Profile struct {
	Name         string  `json:"name"`
	WQuality     float64 `json:"w_quality"`
	WCost        float64 `json:"w_cost"`
	WLatency     float64 `json:"w_latency"`
	WReliability float64 `json:"w_reliability"`
	// Penalties are named subtractive terms, weight by term name (see PenaltyTerms).
	Penalties map[string]float64 `json:"penalties,omitempty"`
}

// Penalty term names. Constants because the profile, the row breakdown and the UI all key off them.
const (
	// PenaltyWeakReference subtracts for the fraction of the score resting on unreviewed
	// LLM-written references. It is a SOFT penalty, never a gate: weak evidence should lower
	// confidence in a ranking, not silently disqualify a variant.
	PenaltyWeakReference = "weak_reference"
	// PenaltyToolError subtracts for tool-call failures the reliability term does not fully capture.
	PenaltyToolError = "tool_error"
)

// PenaltyTerms is the closed set of penalty names.
var PenaltyTerms = []string{PenaltyWeakReference, PenaltyToolError}

// QualityFirst, CostOptimized and Balanced are the named profiles the spec requires.
func QualityFirst() Profile {
	return Profile{Name: "quality-first", WQuality: 0.70, WCost: 0.05, WLatency: 0.05, WReliability: 0.20,
		Penalties: map[string]float64{PenaltyWeakReference: 0.10, PenaltyToolError: 0.05}}
}

func CostOptimized() Profile {
	return Profile{Name: "cost-optimized", WQuality: 0.20, WCost: 0.55, WLatency: 0.15, WReliability: 0.10,
		Penalties: map[string]float64{PenaltyWeakReference: 0.05, PenaltyToolError: 0.05}}
}

func Balanced() Profile {
	return Profile{Name: "balanced", WQuality: 0.40, WCost: 0.25, WLatency: 0.15, WReliability: 0.20,
		Penalties: map[string]float64{PenaltyWeakReference: 0.05, PenaltyToolError: 0.05}}
}

// NamedProfiles returns the built-in profiles keyed by name.
func NamedProfiles() map[string]Profile {
	out := map[string]Profile{}
	for _, p := range []Profile{QualityFirst(), CostOptimized(), Balanced()} {
		out[p.Name] = p
	}
	return out
}

// ProfileNames returns the built-in profile names in a stable display order.
func ProfileNames() []string { return []string{"balanced", "quality-first", "cost-optimized"} }

// ─────────────────────────────────────────────────────────────────────────────
// Gates
// ─────────────────────────────────────────────────────────────────────────────

// Gate names, so a failed gate is reported by a stable identifier the UI can key off rather than a
// sentence someone might reword.
const (
	GateMaxCostPerRun     = "max_cost_per_run"
	GateLatencySLA        = "latency_sla"
	GateMinQuality        = "min_quality"
	GateProviderAllowlist = "provider_allowlist"
)

// GateSet is the hard-constraint set. Every field is a POINTER: a nil gate is not configured, which
// is a different thing from a gate configured to zero. A max-cost gate of 0.0 disqualifies
// everything; treating "unset" as "zero" would empty the board.
type GateSet struct {
	Name              string   `json:"name"`
	MaxCostPerRun     *float64 `json:"max_cost_per_run,omitempty"`
	LatencySLAMs      *float64 `json:"latency_sla_ms,omitempty"`
	MinQuality        *float64 `json:"min_quality,omitempty"`
	ProviderAllowlist []string `json:"provider_allowlist,omitempty"`
	// QualityMetric names which metric the min-quality gate reads. Explicit, because a board may
	// carry several quality metrics and gating on the wrong one is invisible.
	QualityMetric string `json:"quality_metric,omitempty"`
}

// GateOutcome is one variant's gate verdict.
type GateOutcome struct {
	Passed bool `json:"passed"`
	// Failed names the gates violated, in stable order.
	Failed []string `json:"failed,omitempty"`
	// Reasons explains each failure in words a UI renders verbatim.
	Reasons []string `json:"reasons,omitempty"`
	// Refused names gates that were CONFIGURED but not evaluated, with why. A gate driven by an
	// uncalibrated judge lands here: it disqualifies nobody, and the board says so out loud instead
	// of silently behaving as if the gate were not configured.
	Refused []string `json:"refused,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Component breakdown
// ─────────────────────────────────────────────────────────────────────────────

// Component is one metric's contribution to a row, raw and normalized. Both are carried because a
// user needs the raw dollars to act and the normalized value to understand the ranking.
type Component struct {
	Metric     string              `json:"metric"`
	Role       Role                `json:"role"`
	Raw        evalstats.Interval  `json:"raw"`
	Normalized float64             `json:"normalized"`
	Direction  evalstats.Direction `json:"direction"`
	// Unit is the RAW unit, carried so a row can print "$0.021" beside "0.72".
	Unit string `json:"unit"`
	// Contribution is this component's signed term in the weighted sum under the active profile.
	Contribution float64 `json:"contribution"`
	// JudgeStanding rides along when the metric came from a judge, so a row never shows a judge
	// number without its agreement.
	JudgeStanding *evalharness.JudgeStanding `json:"judge_standing,omitempty"`
}

// Row is one leaderboard row.
type Row struct {
	Rank       int    `json:"rank"`
	VariantID  string `json:"variant_id"`
	ConfigHash string `json:"config_hash"`
	Label      string `json:"label"`
	// Composite is the score WITH its confidence interval — never a bare point value.
	Composite  evalstats.Interval   `json:"composite"`
	Components map[string]Component `json:"components"`
	Penalties  map[string]float64   `json:"penalties,omitempty"`
	Gate       GateOutcome          `json:"gate"`
	// TiedWith names the variants whose composite CIs overlap this one's. A pair listed here is NOT
	// a strict ordering, however the ranks happen to fall.
	TiedWith []string `json:"tied_with,omitempty"`
	// WeakLabeled is true when any of this row's numbers rests on an unreviewed reference.
	WeakLabeled   bool `json:"weak_labeled"`
	LowConfidence bool `json:"low_confidence"`
}

// Leaderboard is a COMPUTED VIEW over the score cache, the active profile and the gate set — not a
// materialized table. That is what lets a profile switch re-rank instantly and stay consistent with
// the cache by construction.
type Leaderboard struct {
	Profile     string `json:"profile"`
	GateSet     string `json:"gate_set"`
	EvalSetHash string `json:"eval_set_hash"`
	// Ranked holds the gate-PASSERS, best first.
	Ranked []Row `json:"ranked"`
	// Disqualified holds gate violators, listed separately with the failed gate named. They are
	// excluded from the ranked order entirely — not ranked last.
	Disqualified []Row `json:"disqualified"`
	// RunsEnqueued is how many runs producing this board cost. Ranking is a pure function of the
	// cache, so it is always 0 — the field exists so a test can ASSERT that rather than assume it.
	RunsEnqueued int `json:"runs_enqueued"`
	// Notes carries board-level caveats (refused gates, low-confidence eval set).
	Notes []string `json:"notes,omitempty"`
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func fmtGateReason(gate, detail string) string { return fmt.Sprintf("%s: %s", gate, detail) }
