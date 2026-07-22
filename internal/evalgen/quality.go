package evalgen

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/evalharness"
)

// quality.go is tasks 4.5 and 4.6: gold-vs-weak reference labeling, and eval-set difficulty /
// diversity with near-duplicate deduping.
//
// The failure this prevents: a generated set of near-identical, trivially-easy cases with
// LLM-written references nobody reviewed, scoring 98% and being read as "the workflow is good". Each
// of those three defects is separately invisible; together they produce a confident number about
// nothing. So each is measured and surfaced.

// Floors below which a set is surfaced as low-confidence. Values, not literals at a call site.
const (
	// DefaultDifficultyFloor: a set where the baseline passes more than 85% of cases is too easy to
	// distinguish variants — every variant will score near the ceiling and the board will rank noise.
	DefaultDifficultyFloor = 0.15
	// DefaultDiversityFloor: mean pairwise distance below this means the set is largely one case
	// repeated.
	DefaultDiversityFloor = 0.30
	// DefaultDedupeThreshold: Jaccard similarity at or above this is "near-identical".
	DefaultDedupeThreshold = 0.85
	// DefaultOracleFloor: the fraction of cases that must be able to DECIDE success.
	//
	// This floor was added after running the real pipeline: a generated set can pass both the
	// difficulty and diversity floors while 12 of its 17 cases carry no oracle at all — no
	// reference, no schema, no regex. task_success is then measured on five cases, the CI is wide,
	// and a variant that is genuinely broken can top the board with a quality of 1.000. Difficulty
	// and diversity say nothing about that failure, because they describe the INPUTS; this floor
	// describes whether the set can answer the question it exists to answer.
	DefaultOracleFloor = 0.60
)

// SetQuality is the eval set's own report card.
type SetQuality struct {
	NCases int `json:"n_cases"`
	// Difficulty is the mean baseline FAILURE rate in [0,1]: 0 = the baseline passes everything
	// (trivial set), 1 = it fails everything. Measured, not asserted; zero when unmeasured.
	Difficulty float64 `json:"difficulty"`
	// DifficultyMeasured distinguishes "difficulty is 0 because the set is trivial" from
	// "difficulty is 0 because nobody ran a baseline". Collapsing those two is how an unmeasured
	// set passes as a measured easy one.
	DifficultyMeasured bool `json:"difficulty_measured"`
	// Diversity is the mean pairwise Jaccard DISTANCE between case inputs, in [0,1].
	Diversity float64 `json:"diversity"`
	// Deduped is how many near-identical cases were removed.
	Deduped int `json:"deduped"`
	// OracleCoverage is the fraction of cases whose oracle can actually return NO. A set whose
	// quality number rests on a handful of cases is as dangerous as a trivially easy one, and far
	// less obvious.
	//
	// It counts DECISIVE oracles, not present ones. Counting presence reported 4% for a set whose
	// truthful figure was 0: both of its "oracles" were the unconstrained `{"type":"object"}`
	// contract a syntactic frontend emits, which accepts every possible output.
	OracleCoverage float64 `json:"oracle_coverage"`
	NOracle        int     `json:"n_oracle"`
	// NIndecisive counts cases carrying an oracle that can never fail. They are the most misleading
	// cases in a set — they look measured and decide nothing — so they are counted separately
	// rather than folded into "no oracle".
	NIndecisive int `json:"n_indecisive"`
	// IndecisiveReasons are the distinct explanations, so a reader learns WHY rather than just how
	// many. Distinct, not per-case: forty copies of one sentence is noise.
	IndecisiveReasons []string `json:"indecisive_reasons,omitempty"`
	// Counts by reference label — the gold/weak split a consumer must see.
	NGold int `json:"n_gold"`
	NWeak int `json:"n_weak"`
	NNone int `json:"n_none"`
	// LowConfidence is set when the set is below a floor. Any score computed over it must be
	// surfaced as low-confidence.
	LowConfidence bool     `json:"low_confidence"`
	Reasons       []string `json:"reasons,omitempty"`
}

// MeasureQuality computes diversity, the label split, and the low-confidence verdict. Difficulty
// needs a baseline run and is filled in by MeasureDifficulty.
func MeasureQuality(cases []evalharness.Case, cfg LoopConfig) SetQuality {
	q := SetQuality{NCases: len(cases)}
	for _, c := range cases {
		switch c.Label {
		case evalharness.LabelGold:
			q.NGold++
		case evalharness.LabelWeak:
			q.NWeak++
		default:
			q.NNone++
		}
	}
	q.Diversity = Diversity(cases)

	seenReason := map[string]bool{}
	for _, c := range cases {
		v := c.DecisiveOracle()
		switch {
		case v.Decisive:
			q.NOracle++
		case c.HasOracle():
			q.NIndecisive++
			if v.Reason != "" && !seenReason[v.Reason] {
				seenReason[v.Reason] = true
				q.IndecisiveReasons = append(q.IndecisiveReasons, v.Reason)
			}
		}
	}
	sort.Strings(q.IndecisiveReasons)
	if len(cases) > 0 {
		q.OracleCoverage = float64(q.NOracle) / float64(len(cases))
	}

	// Difficulty carried on the cases, if a baseline measured it.
	var sum float64
	measured := 0
	for _, c := range cases {
		if c.Difficulty > 0 {
			sum += c.Difficulty
			measured++
		}
	}
	if measured > 0 {
		q.Difficulty = sum / float64(len(cases))
		q.DifficultyMeasured = true
	}

	diversityFloor := cfg.DiversityFloor
	if diversityFloor <= 0 {
		diversityFloor = DefaultDiversityFloor
	}
	difficultyFloor := cfg.DifficultyFloor
	if difficultyFloor <= 0 {
		difficultyFloor = DefaultDifficultyFloor
	}
	if len(cases) > 1 && q.Diversity < diversityFloor {
		q.LowConfidence = true
		q.Reasons = append(q.Reasons, fmt.Sprintf(
			"diversity %.3f is below the floor %.3f: the set is largely one case repeated", q.Diversity, diversityFloor))
	}
	if q.DifficultyMeasured && q.Difficulty < difficultyFloor {
		q.LowConfidence = true
		q.Reasons = append(q.Reasons, fmt.Sprintf(
			"difficulty %.3f is below the floor %.3f: the baseline already passes almost every case, so no variant can be distinguished",
			q.Difficulty, difficultyFloor))
	}
	if !q.DifficultyMeasured && len(cases) > 0 {
		q.LowConfidence = true
		q.Reasons = append(q.Reasons, "difficulty has not been measured (no baseline run), so the set's discriminating power is unknown")
	}
	oracleFloor := cfg.OracleFloor
	if oracleFloor <= 0 {
		oracleFloor = DefaultOracleFloor
	}
	if len(cases) > 0 && q.OracleCoverage < oracleFloor {
		q.LowConfidence = true
		msg := fmt.Sprintf(
			"only %d of %d cases (%.0f%%) carry an oracle that can FAIL, below the floor %.0f%%: task "+
				"success is measured on a fraction of the set, so a variant can score well without being good",
			q.NOracle, len(cases), q.OracleCoverage*100, oracleFloor*100)
		if q.NIndecisive > 0 {
			msg += fmt.Sprintf(" (%d further case(s) carry an oracle that can never fail, which is worse "+
				"than none: they look measured and decide nothing)", q.NIndecisive)
		}
		q.Reasons = append(q.Reasons, msg)
		q.Reasons = append(q.Reasons, q.IndecisiveReasons...)
	}
	if q.NWeak > 0 {
		q.Reasons = append(q.Reasons, fmt.Sprintf(
			"%d of %d references are weak (LLM-generated, unreviewed) and must not silently drive a gate", q.NWeak, len(cases)))
	}
	sort.Strings(q.Reasons)
	return q
}

// ─────────────────────────────────────────────────────────────────────────────
// Difficulty
// ─────────────────────────────────────────────────────────────────────────────

// BaselineRunner runs one case against the baseline configuration and reports whether it passed.
// Difficulty is operationalized as the baseline's failure rate (design Q6), which means it is a
// MEASUREMENT over real runs — there is no path where a difficulty is asserted.
type BaselineRunner interface {
	Pass(ctx context.Context, c evalharness.Case) (bool, error)
}

// MeasureDifficulty runs the baseline over each case for n repeats and writes the measured failure
// rate onto the case. Repeats matter: a stochastic model that passes a case four times in five is
// not the same evidence as one that passes it once.
func MeasureDifficulty(ctx context.Context, r BaselineRunner, cases []evalharness.Case, repeats int) ([]evalharness.Case, error) {
	if r == nil {
		return cases, fmt.Errorf("evalgen: MeasureDifficulty requires a baseline runner")
	}
	if repeats <= 0 {
		repeats = 3
	}
	out := append([]evalharness.Case(nil), cases...)
	for i := range out {
		fails := 0
		for k := 0; k < repeats; k++ {
			ok, err := r.Pass(ctx, out[i])
			if err != nil {
				return nil, fmt.Errorf("baseline on case %q: %w", out[i].CaseID, err)
			}
			if !ok {
				fails++
			}
		}
		out[i].Difficulty = float64(fails) / float64(repeats)
	}
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Diversity and deduping
// ─────────────────────────────────────────────────────────────────────────────

// Diversity is the mean pairwise Jaccard DISTANCE over case-input token sets, in [0,1]. Token-set
// Jaccard rather than an embedding: it is deterministic, needs no model, and is exactly right for
// catching the failure it exists to catch (the same case with one word changed). Embedding-space
// spread is the P5 refinement once real traces calibrate what "different enough" means.
func Diversity(cases []evalharness.Case) float64 {
	if len(cases) < 2 {
		return 1
	}
	sets := make([]map[string]bool, len(cases))
	for i, c := range cases {
		sets[i] = tokenize(c.Input)
	}
	var sum float64
	n := 0
	for i := 0; i < len(sets); i++ {
		for j := i + 1; j < len(sets); j++ {
			sum += 1 - jaccard(sets[i], sets[j])
			n++
		}
	}
	if n == 0 {
		return 1
	}
	return sum / float64(n)
}

// Dedupe removes near-identical cases, keeping the FIRST occurrence in stable case-id order.
// Deterministic keep-order matters: a dedupe that kept an arbitrary member would make the surviving
// set — and therefore every downstream score — depend on map iteration order.
func Dedupe(cases []evalharness.Case, threshold float64) (kept []evalharness.Case, removed int) {
	if len(cases) < 2 {
		return cases, 0
	}
	if threshold <= 0 {
		threshold = DefaultDedupeThreshold
	}
	ordered := append([]evalharness.Case(nil), cases...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].CaseID < ordered[j].CaseID })

	var keptSets []map[string]bool
	for _, c := range ordered {
		set := tokenize(c.Input)
		dup := false
		for _, k := range keptSets {
			if jaccard(set, k) >= threshold {
				dup = true
				break
			}
		}
		if dup {
			removed++
			continue
		}
		keptSets = append(keptSets, set)
		kept = append(kept, c)
	}
	return kept, removed
}

// tokenize reduces an input to a token set. Keys and values both contribute: two cases with the same
// keys and different values are different cases, and two with different keys certainly are.
func tokenize(raw json.RawMessage) map[string]bool {
	out := map[string]bool{}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		for _, t := range strings.Fields(string(raw)) {
			out[strings.ToLower(t)] = true
		}
		return out
	}
	walkTokens(v, "", out)
	return out
}

func walkTokens(v any, prefix string, out map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out["k:"+prefix+k] = true
			walkTokens(t[k], prefix+k+".", out)
		}
	case []any:
		for _, e := range t {
			walkTokens(e, prefix, out)
		}
	case string:
		for _, w := range strings.Fields(strings.ToLower(t)) {
			out["v:"+w] = true
		}
	case nil:
		out["v:null"] = true
	default:
		out[fmt.Sprintf("v:%v", t)] = true
	}
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}

// ─────────────────────────────────────────────────────────────────────────────
// Reference labeling (task 4.5)
// ─────────────────────────────────────────────────────────────────────────────

// LabelReference assigns gold or weak to a case's reference.
//
// GOLD requires an ORACLE: a decidable output contract (JSON schema), a deterministic regex, or an
// explicit human review. WEAK is everything an LLM produced that no human has looked at. There is no
// third path and no heuristic that promotes weak to gold — a promotion rule is exactly how an
// unreviewed synthetic reference ends up gating a variant.
func LabelReference(c evalharness.Case, humanReviewed bool) evalharness.Case {
	switch {
	case len(c.Reference) == 0 && len(c.OutputSchema) == 0 && c.Pattern == "":
		c.Label = evalharness.LabelNone
	case humanReviewed:
		c.Label = evalharness.LabelGold
	case len(c.OutputSchema) > 0 || c.Pattern != "":
		// A decidable oracle. Nobody has to review a schema-validity verdict.
		c.Label = evalharness.LabelGold
	case c.Origin == evalharness.OriginLLM:
		c.Label = evalharness.LabelWeak
	case c.Origin == evalharness.OriginHandAuthored || c.Origin == evalharness.OriginSeedTrace:
		// A human wrote it, or it came from a real recorded run.
		c.Label = evalharness.LabelGold
	default:
		c.Label = evalharness.LabelWeak
	}
	return c
}

// WeakCaseIDs returns the ids of every weak-labeled case, sorted.
func WeakCaseIDs(cases []evalharness.Case) []string {
	var out []string
	for _, c := range cases {
		if c.Label == evalharness.LabelWeak {
			out = append(out, c.CaseID)
		}
	}
	sort.Strings(out)
	return out
}

// GatingSet splits an eval set into the cases that MAY drive a hard gate and those that may not.
//
// Weak-labeled cases are excluded from gating and returned separately so a caller cannot use them by
// accident — the split is the enforcement, not a flag someone must remember to check. A gate
// computed over gold cases only, with the weak ones surfaced alongside, is the shape task 4.5's
// "weak references never silently drive scoring" requires.
func GatingSet(cases []evalharness.Case) (gating, weak []evalharness.Case) {
	for _, c := range cases {
		if c.Label == evalharness.LabelWeak {
			weak = append(weak, c)
			continue
		}
		gating = append(gating, c)
	}
	return gating, weak
}
