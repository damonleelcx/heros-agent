package improvementrun

import (
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/assessment"
)

// translate.go is FR3 and design D1: a question that cannot be bounded is REFUSED, not run with
// defaults.
//
// # Why refusal rather than defaults, stated where the temptation lives
//
// Defaults are how a conversational surface spends someone's money on a search they did not ask for.
// The failure is silent — the run looks exactly like a run they wanted — and it is discovered on an
// invoice. An unbounded search is not a larger version of a bounded one; it is a different product with
// a different risk, and the person who typed a sentence did not choose it.
//
// The pressure against this rule is entirely predictable and worth naming in advance: somebody will
// observe that refusing is a worse first-run experience than picking sensible bounds, and they will be
// right about the experience and wrong about the product. So the refusals below each name a NEXT
// ACTION, which is the thing that actually fixes the experience.
//
// # 🔴 What is and is not read from the sentence
//
// The AXES are read from the sentence, because "make my memory strategy better" is a scope and
// discarding it would run a nine-axis search somebody asked to be a one-axis search. Everything else —
// the tenant, the workflow, the revision, the origin, the budget — comes from the request's context and
// the tenant's entitlement. 🚫 Nothing a person can type may widen a bound: a sentence that could raise
// its own budget is a sentence that spends a month's allowance.

// RefusalCause is why a question could not become a bounded plan. A CLOSED set: each value leads to a
// different next action, and a caller that cannot tell them apart can only say "I cannot do that",
// which is the shape of refusal that teaches a person nothing.
type RefusalCause string

const (
	// RefusedNoSubject: no workflow was named and none was supplied by the surface. 🔴 Distinct from
	// every other cause because it is the only one where the person has typed nothing wrong — they
	// simply have not said which repository — and the next action is to pick one, not to rephrase.
	RefusedNoSubject RefusalCause = "no_subject"
	// RefusedNoSourceRevision: the workflow has no resolved revision to pin. An unpinned run cannot be
	// re-measured against what it changed (FR17), so it is refused rather than run unpinned.
	RefusedNoSourceRevision RefusalCause = "no_source_revision"
	// RefusedMultipleSubjects: the question names more than one repository or workflow. One workflow,
	// one revision is a stated non-goal boundary, not a limitation to work around.
	RefusedMultipleSubjects RefusalCause = "multiple_subjects"
	// RefusedUnboundedRequested: the question explicitly asks for no bound — "keep going until",
	// "as many as it takes", "no limit". 🔴 This is the case the whole rule exists for, and it is the
	// one where refusing feels least helpful and is most necessary: the person has asked, in words, for
	// the product this platform does not sell.
	RefusedUnboundedRequested RefusalCause = "unbounded_requested"
	// RefusedUnknownAxis: the question names a surface outside the closed set of nine.
	RefusedUnknownAxis RefusalCause = "unknown_axis"
	// RefusedNoBudget: the tenant's entitlement yields no spend budget for a run. Not an error — a
	// deployment or a plan that does not include improvement runs is a real state — but emphatically
	// not a reason to run with a default budget.
	RefusedNoBudget RefusalCause = "no_budget"
)

// Refusal is a question that could not be bounded, with what to do about it.
type Refusal struct {
	Cause RefusalCause `json:"cause"`
	// Detail is the named condition, in the product's own nouns.
	Detail string `json:"detail"`
	// NextAction is what to do. 🔴 Every cause here HAS one — unlike `forgedelivery.Withheld`, where an
	// empty next action is meaningful — because a question that cannot be bounded is always either
	// missing an input or asking for something out of scope, and both have a step.
	NextAction string `json:"next_action"`
}

// Error makes a Refusal usable as an error at the call boundary without losing its structure.
func (r *Refusal) Error() string { return "improvementrun: " + r.Detail }

// Bounds is what the SURFACE supplies to a translation: the subject, the origin and the tenant's
// entitlement-derived ceilings.
//
// 🚫 It is not part of the request body and no field of it is settable by a person. That is the
// structural half of "nothing a person can type may widen a bound" — the widenable fields are not in
// the type the question arrives in.
type Bounds struct {
	TenantID       string
	WorkflowID     string
	SourceRevision string
	Origin         RunOrigin

	// MaxCandidates and MaxSpendUSD come from the tenant's entitlement. Zero in either is
	// RefusedNoBudget, never a default.
	MaxCandidates int
	MaxSpendUSD   float64

	// MinImprovement is the gain floor. Zero selects DefaultMinImprovement, which is a PLATFORM
	// constant rather than a per-tenant one — see that constant for why it is not configurable.
	MinImprovement float64
	// StallAfter is passed through to the loop; zero selects the loop's own default.
	StallAfter int

	// EstimatedCostPerCandidateUSD is what one candidate is expected to cost to verify, used only to
	// project the plan's spend for the disclosure threshold. 🔴 An estimate, and everything it feeds is
	// labelled as one. Zero selects DefaultCostPerCandidateUSD.
	EstimatedCostPerCandidateUSD float64

	// NowMS is the injected clock. Nothing here reads the wall clock directly, so a translation is
	// deterministic under test and a plan id is reproducible.
	NowMS int64
}

// DefaultMinImprovement is the verified-gain floor below which a run converges rather than chasing a
// smaller gain.
//
// 🔴 A CONSTANT, not a per-tenant setting, for `conversation.AbstainThreshold`'s reason: a floor an
// operator can lower is a floor that gets lowered the first time somebody wants the run to "find
// something", and lowering it does not make the run better — it makes it deliver changes whose gain is
// inside the noise. The honest way to get more out of a run is a bigger eval set, not a smaller floor.
const DefaultMinImprovement = 0.01

// DefaultCostPerCandidateUSD is the per-candidate verification estimate used ONLY to project a plan's
// spend against the disclosure threshold. It never bounds anything; `SpendBudgetUSD` does.
const DefaultCostPerCandidateUSD = 0.25

// unboundedMarkers are the phrasings that ASK for an unbounded search.
//
// 🔴 A closed list, matched literally, and deliberately SMALL. A classifier here would make the refusal
// as accurate as the classifier, and a false positive on this list refuses a question somebody asked
// perfectly reasonably. Every entry is a phrase whose only reading is "do not stop" — "if needed" and
// "as much as you can" are absent on purpose, because both are ordinary emphasis rather than a request
// for an unbounded run.
var unboundedMarkers = []string{
	"no limit", "no budget", "unlimited", "without a limit", "without limit",
	"as many as it takes", "whatever it takes", "keep going until",
	"don't stop", "dont stop", "do not stop", "no cap", "uncapped",
}

// multiSubjectMarkers are the phrasings that name more than one subject.
var multiSubjectMarkers = []string{
	"all my repos", "all my repositories", "every repository", "every repo",
	"across my repos", "across my repositories", "all workflows", "every workflow",
	"both repos", "both repositories",
}

// axisMarkers maps a word a person types onto the axis it names.
//
// 🔴 Keyed on the AXIS's own noun and the words the console already uses for it, so the scope a person
// asks for and the scope the report showed them are the same vocabulary. A person who read a finding
// about "memory" and typed "fix the memory thing" gets the memory axis, because both came from the same
// dictionary — `internal/assessment/axis.go` is that dictionary and this is a view over it, not a
// second copy: `TestAxisMarkersCoverEveryAxis` fails when an axis has no marker.
var axisMarkers = map[string][]assessment.Axis{
	"model":       {assessment.AxisModel},
	"prompt":      {assessment.AxisPrompt},
	"instruction": {assessment.AxisPrompt},
	"skill":       {assessment.AxisSkills},
	"skills":      {assessment.AxisSkills},
	"context":     {assessment.AxisContext},
	"tool":        {assessment.AxisTools},
	"tools":       {assessment.AxisTools},
	"memory":      {assessment.AxisMemory},
	"remember":    {assessment.AxisMemory},
	"harness":     {assessment.AxisHarness},
	"sandbox":     {assessment.AxisHarness},
	"loop":        {assessment.AxisLoop},
	"iteration":   {assessment.AxisLoop},
	"graph":       {assessment.AxisGraph},
	"topology":    {assessment.AxisGraph},
	"wiring":      {assessment.AxisGraph},
}

// unknownAxisMarkers are surfaces a person will plausibly ask this to improve that are NOT axes.
//
// 🔴 They are enumerated rather than left to fall through to "every axis", and that is the point: a
// question about the retriever falling through to a nine-axis run is the platform silently deciding it
// knows better, spending money on a search the person did not ask for. Naming the boundary is what
// FR3's refusal is FOR.
var unknownAxisMarkers = []string{
	"retriever", "retrieval", "embedding", "embeddings", "vector store", "vector database",
	"database", "infrastructure", "latency of my api", "my frontend", "my ui",
}

// Translate turns a question into a bounded plan, or refuses.
//
// The order of the checks is the order of the answers a person needs distinguished, and it is not
// arbitrary: the two "you asked for something we do not do" causes are tested BEFORE the two "we are
// missing an input" causes, because a person who asked for an unbounded run across every repository
// should be told that, not told their workflow has no revision.
func Translate(question string, b Bounds) (Plan, error) {
	q := normalise(question)

	// 1 · The person asked, in words, for the product this platform does not sell.
	if m, ok := firstMatch(q, unboundedMarkers); ok {
		return Plan{}, &Refusal{
			Cause: RefusedUnboundedRequested,
			Detail: fmt.Sprintf("This asks for a run with no bound (%q). Every run here declares a "+
				"candidate cap, a spend budget and a stopping condition before it starts, and an "+
				"unbounded search is not a larger version of a bounded one — it is a run whose cost "+
				"nobody can predict.", m),
			NextAction: "Ask the same question without the limit clause; the plan will show you the " +
				"budget it will run under before anything spends.",
		}
	}

	// 2 · More than one subject. One workflow, one revision.
	if m, ok := firstMatch(q, multiSubjectMarkers); ok {
		return Plan{}, &Refusal{
			Cause: RefusedMultipleSubjects,
			Detail: fmt.Sprintf("This names more than one subject (%q). An improvement run is scoped to "+
				"one workflow at one revision, because a verified delta and an approval are both bound "+
				"to a single configuration and a single source revision.", m),
			NextAction: "Ask about one workflow. Run it again for the next one.",
		}
	}

	// 3 · A surface that is not one of the nine.
	if m, ok := firstMatch(q, unknownAxisMarkers); ok {
		return Plan{}, &Refusal{
			Cause: RefusedUnknownAxis,
			Detail: fmt.Sprintf("This platform configures nine surfaces and %q is not one of them. The "+
				"nine are: %s.", m, joinAxes(assessment.Axes())),
			NextAction: "Ask about one of the nine, or ask what is weak in this repository and pick " +
				"from what the assessment reports.",
		}
	}

	// 4 · The subject. Supplied by the surface, never parsed out of the sentence.
	if strings.TrimSpace(b.WorkflowID) == "" {
		return Plan{}, &Refusal{
			Cause: RefusedNoSubject,
			Detail: "No workflow was named, so there is nothing to run against. A default subject is " +
				"not available here: a run against the first workflow we know about would spend money " +
				"changing a repository nobody asked about.",
			NextAction: "Pick a workflow, then ask again.",
		}
	}
	if strings.TrimSpace(b.SourceRevision) == "" {
		return Plan{}, &Refusal{
			Cause: RefusedNoSourceRevision,
			Detail: "This workflow has no resolved source revision to pin the run to. Every measurement " +
				"here is pinned, because a change that cannot be re-measured against the revision it " +
				"was proposed for cannot be shown to have reproduced.",
			NextAction: "Connect the repository or push a source snapshot, then ask again.",
		}
	}

	// 5 · The budget. Zero is a refusal, never a default.
	if b.MaxCandidates <= 0 || b.MaxSpendUSD <= 0 {
		return Plan{}, &Refusal{
			Cause: RefusedNoBudget,
			Detail: "This organization has no improvement-run budget configured, so there is no bound " +
				"to run under. This is not an error and it is not a reason to run with a default: a " +
				"budget nobody agreed to is the one that shows up on an invoice.",
			NextAction: "Ask your account owner to enable improvement runs for this organization.",
		}
	}

	if !b.Origin.Valid() {
		return Plan{}, &Refusal{
			Cause:      RefusedNoSubject,
			Detail:     fmt.Sprintf("%q is not a run origin, so the surface this question came from is unknown.", b.Origin),
			NextAction: "This is a defect in the surface, not in the question; report it with the trace id.",
		}
	}

	axes := axesIn(q)

	minImprovement := b.MinImprovement
	if minImprovement <= 0 {
		minImprovement = DefaultMinImprovement
	}
	perCandidate := b.EstimatedCostPerCandidateUSD
	if perCandidate <= 0 {
		perCandidate = DefaultCostPerCandidateUSD
	}

	p := Plan{
		TenantID:       b.TenantID,
		Origin:         b.Origin,
		Question:       strings.TrimSpace(question),
		WorkflowID:     b.WorkflowID,
		SourceRevision: b.SourceRevision,
		Axes:           axes,
		CandidateCap:   b.MaxCandidates,
		SpendBudgetUSD: b.MaxSpendUSD,
		Stopping: StoppingCondition{
			MinImprovement: minImprovement,
			StallAfter:     b.StallAfter,
		},
		// 🔴 The projection is capped by the BUDGET. A projection above the budget would tell somebody
		// the run might cost more than it structurally can, which is a disclosure that is wrong in the
		// alarming direction and trains people to ignore the next one.
		ProjectedSpendUSD: minFloat(float64(b.MaxCandidates)*perCandidate, b.MaxSpendUSD),
		CreatedAtMS:       b.NowMS,
	}
	p.PlanID = NewPlanID(p)
	if err := p.Validate(); err != nil {
		// Reaching here is a defect in this function rather than in the question — every refusal above
		// covers a way a question can fail. It is returned rather than panicked because a defect that
		// refuses one turn is better than one that takes down the process, and the message says which
		// kind of failure it is so nobody debugs the sentence.
		return Plan{}, fmt.Errorf("improvementrun: the translation produced an invalid plan, which is a "+
			"defect in the translator rather than in the question: %w", err)
	}
	return p, nil
}

// axesIn reads the scope out of the sentence, defaulting to ALL NINE when the sentence names none.
//
// 🔴 "All nine" is a legitimate default and "some bound we picked" is not, and the difference is worth
// being precise about because they look similar. The scope is not a BOUND — the candidate cap and the
// budget bound the run, and they are the same whether the scope is one axis or nine. Widening the scope
// cannot make the run cost more than the person was shown. Widening a budget can.
func axesIn(q string) []assessment.Axis {
	seen := map[assessment.Axis]bool{}
	for marker, axes := range axisMarkers {
		if strings.Contains(q, " "+marker+" ") {
			for _, a := range axes {
				seen[a] = true
			}
		}
	}
	if len(seen) == 0 {
		return assessment.Axes()
	}
	// Ordered as `assessment.Axes()` orders them, so two plans over the same scope are byte-identical
	// and their ids agree.
	var out []assessment.Axis
	for _, a := range assessment.Axes() {
		if seen[a] {
			out = append(out, a)
		}
	}
	return out
}

// AxisMarkers returns the marker→axis table, sorted by marker, for the fence that asserts every axis
// is reachable by at least one word a person would type.
func AxisMarkers() map[string][]assessment.Axis {
	out := make(map[string][]assessment.Axis, len(axisMarkers))
	for k, v := range axisMarkers {
		out[k] = append([]assessment.Axis(nil), v...)
	}
	return out
}

// RefusalCauses returns the closed set, sorted, for the fence that asserts every cause renders its own
// sentence.
func RefusalCauses() []RefusalCause {
	out := []RefusalCause{
		RefusedNoSubject, RefusedNoSourceRevision, RefusedMultipleSubjects,
		RefusedUnboundedRequested, RefusedUnknownAxis, RefusedNoBudget,
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func firstMatch(q string, markers []string) (string, bool) {
	// Sorted so the reported marker is deterministic when a sentence contains two.
	sorted := append([]string(nil), markers...)
	sort.Strings(sorted)
	for _, m := range sorted {
		if strings.Contains(q, " "+m+" ") {
			return m, true
		}
	}
	return "", false
}

// normalise lowercases and collapses punctuation to single spaces, with a leading and trailing space so
// a marker table written in plain words can be matched on word boundaries. It is deliberately the same
// shape as `conversation.normalise` — a stemmer would make matching depend on a library version.
func normalise(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte(' ')
	lastSpace := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '\'':
			b.WriteRune(r)
			lastSpace = false
		default:
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	out := b.String()
	if !strings.HasSuffix(out, " ") {
		out += " "
	}
	return out
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
