package improvementrun

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/optimizer"
)

// plan.go is FR1/FR2 and design D1: a question becomes an artifact somebody can decline.
//
// # Why the plan is a value rather than a set of arguments
//
// Before the run, a plan is a DECISION the person can decline. After the run, the same information is a
// RECEIPT. Only one of those is useful, and it is only available if the plan exists as a thing before
// anything executes — a set of arguments scattered across a call is a plan nobody can be shown.
//
// # 🔴 Every bound is REQUIRED and a zero is not a bound
//
// `BudgetEnvelope.Complete` in `internal/conversation` makes the same statement for the same reason, and
// the reason is worth repeating here because this is where money is: a plan with `spend_budget_usd: 0`
// either stops instantly or — far more likely, because a `<= 0` somewhere reads as "no budget
// configured" — runs unbounded. `Bounded()` refuses the ambiguity, so it has no runtime.

// RunOrigin is the SURFACE a run came from. It is a closed set because two things key off it and both
// are load-bearing:
//
//   - the delivery mode's default (R3 / design D3): console runs use the hosted Git App, CLI- and
//     CI-originated runs keep CI-mediated delivery and the platform receives no forge credential;
//   - whether the run may deliver AT ALL (decisions.md D-35.3): a scheduled run stops at proposals.
//
// 🔴 It is recorded on the PLAN, at translation time, from the transport that accepted the question —
// never supplied in a request body. An origin a caller could set is an origin a caller could set to
// `console` to obtain a write credential path, which is the whole reason it is not a field a caller
// controls.
type RunOrigin string

const (
	// OriginConsole — a person typed the question in the console. Delivery defaults to the hosted App.
	OriginConsole RunOrigin = "console"
	// OriginCLI — `heros` on the customer's machine. Delivery stays CI-mediated (ADR-005 unamended).
	OriginCLI RunOrigin = "cli"
	// OriginCI — a CI job. Delivery stays CI-mediated.
	OriginCI RunOrigin = "ci"
	// OriginScheduled — unattended. 🚫 STOPS AT PROPOSALS at every automation level, including
	// Autonomous (decisions.md D-35.3). Per-proposal approval is the consent primitive and an
	// entitlement is not consent.
	OriginScheduled RunOrigin = "scheduled"
)

var origins = []RunOrigin{OriginConsole, OriginCLI, OriginCI, OriginScheduled}

// Valid reports membership in the closed set.
func (o RunOrigin) Valid() bool {
	for _, v := range origins {
		if v == o {
			return true
		}
	}
	return false
}

// String makes RunOrigin printable in an error.
func (o RunOrigin) String() string { return string(o) }

// MayDeliver reports whether a run from this surface may reach a forge at all.
//
// 🔴 It is a property of the ORIGIN, checked server-side before entitlement is consulted, so a
// scheduled run cannot reach delivery through any plan, role, entitlement, flag or parameter. Checking
// it after entitlement would make "may this deliver" answerable by buying a plan, which is precisely
// what D-35.3 refuses.
func (o RunOrigin) MayDeliver() bool { return o != OriginScheduled }

// Origins returns the closed set, for a fence and for a refusal that has to name the alternatives.
func Origins() []RunOrigin { return append([]RunOrigin(nil), origins...) }

// StoppingCondition is when a run stops because it has stopped GAINING, as distinct from stopping
// because it ran out of something.
//
// The distinction is the whole reason it is a separate bound from the candidate cap and the budget: a
// run that converged found the best change reachable and is a SUCCESS; a run that hit its cap was
// truncated and may have had more to give. Reporting both as "finished" would make those two
// indistinguishable to the person deciding whether to run it again with a bigger budget.
type StoppingCondition struct {
	// MinImprovement is the floor on the verified marginal gain (the composite CI lower bound). A gain
	// below it converges rather than chasing a smaller one.
	MinImprovement float64 `json:"min_improvement"`
	// StallAfter is how many consecutive candidates may produce no gate-passing, verified gain before
	// the run declares itself stalled. Zero selects `optimizer.DefaultStallK` — the loop's own default,
	// not a second one declared here, because two defaults for one behaviour is two numbers that drift.
	StallAfter int `json:"stall_after"`
}

// DisclosureThresholdUSD is the projected spend above which a plan must be ACKNOWLEDGED before the run
// begins (FR2).
//
// # Why it is one assessment's cap and not a number somebody liked
//
// The threshold should sit where a person would want to be asked, and there is a non-arbitrary answer:
// a P33 assessment is bounded at `assessment.DefaultSpendCapUSD`, and it is everything the person has
// already been shown for free. A run projected to cost more than that costs more than the entire report
// it is responding to — which is exactly the moment the spend stops being incidental to the
// conversation and becomes a decision.
//
// 🔴 Derived from `assessment.DefaultSpendCapUSD` rather than written as a literal, so raising the
// assessment cap cannot silently leave this threshold behind. Two numbers that mean "one unit of
// analysis" must not be able to disagree.
const DisclosureThresholdUSD = assessment.DefaultSpendCapUSD

// Plan is what a question became. It is shown BEFORE any candidate is generated (FR1).
type Plan struct {
	// PlanID is deterministic in (tenant, workflow, revision, origin, axes, bounds) — see NewPlanID.
	// Deterministic so that re-translating the same question against the same subject produces the same
	// id, which is what makes the "was this plan acknowledged" question answerable without a second
	// notion of identity.
	PlanID   string    `json:"plan_id"`
	TenantID string    `json:"tenant_id"`
	Origin   RunOrigin `json:"origin"`

	// Question is the sentence, verbatim. Carried so the receipt can show what was asked beside what
	// was planned — a plan that lost its question cannot be reviewed for whether it answered it.
	Question string `json:"question"`

	// WorkflowID and SourceRevision are the SUBJECT. One workflow, one revision — multi-repository and
	// cross-workflow runs are a stated non-goal, and a plan that could name two is a plan whose
	// `config_hash` binding means nothing.
	WorkflowID     string `json:"workflow_id"`
	SourceRevision string `json:"source_revision"`

	// Axes is the scope, ordered as `assessment.Axes()` orders them so two plans over the same scope are
	// byte-identical. Never empty: an empty scope is not "everything", it is a plan that proposes
	// nothing, and the two must not be spelled the same way.
	Axes []assessment.Axis `json:"axes"`

	// CandidateCap is the maximum number of candidates the run may enumerate, across all axes.
	CandidateCap int `json:"candidate_cap"`
	// SpendBudgetUSD is the cumulative provider spend ceiling for the whole run.
	SpendBudgetUSD float64 `json:"spend_budget_usd"`
	// Stopping is the gain-based stop.
	Stopping StoppingCondition `json:"stopping_condition"`

	// ProjectedSpendUSD is what this plan is expected to cost, and it is what the disclosure threshold
	// is compared against. 🔴 It is an ESTIMATE and is labelled as one everywhere it renders; the
	// BUDGET is the promise. Presenting the estimate as the promise is how a person agrees to a number
	// the run is not actually bound by.
	ProjectedSpendUSD float64 `json:"projected_spend_usd"`

	CreatedAtMS int64 `json:"created_at_ms"`
}

// RequiresAcknowledgement reports whether execution must wait for the person (FR2).
func (p Plan) RequiresAcknowledgement() bool { return p.ProjectedSpendUSD > DisclosureThresholdUSD }

// MissingBounds names every bound the plan failed to declare, in a stable order.
//
// 🔴 A LIST rather than a boolean, for `BudgetEnvelope.MissingLimits`' reason: a refusal that says "this
// plan is not bounded" sends somebody to read code, and one that says "candidate_cap, spend_budget_usd"
// does not.
func (p Plan) MissingBounds() []string {
	var out []string
	if p.CandidateCap <= 0 {
		out = append(out, "candidate_cap")
	}
	if p.SpendBudgetUSD <= 0 {
		out = append(out, "spend_budget_usd")
	}
	if p.Stopping.MinImprovement <= 0 {
		out = append(out, "stopping_condition.min_improvement")
	}
	return out
}

// Bounded reports whether every bound is present and positive, and returns a refusal naming the
// missing ones when it is not.
func (p Plan) Bounded() error {
	if missing := p.MissingBounds(); len(missing) > 0 {
		return fmt.Errorf("improvementrun: this plan declares no %s, and an absent bound is not a bound "+
			"of zero — a run under it would either stop instantly or not stop at all",
			strings.Join(missing, ", "))
	}
	return nil
}

// Validate is the whole precondition on a plan: a subject, a scope, an origin, and every bound.
func (p Plan) Validate() error {
	if strings.TrimSpace(p.TenantID) == "" {
		return fmt.Errorf("improvementrun: a plan has no tenant; the tenant comes from the credential, " +
			"never from the question")
	}
	if strings.TrimSpace(p.WorkflowID) == "" {
		return fmt.Errorf("improvementrun: a plan has no workflow to run against")
	}
	if strings.TrimSpace(p.SourceRevision) == "" {
		return fmt.Errorf("improvementrun: a plan has no source revision to pin; an unpinned run cannot " +
			"be re-measured against what it changed")
	}
	if !p.Origin.Valid() {
		return fmt.Errorf("improvementrun: %q is not a run origin (%s)", p.Origin, joinOrigins())
	}
	if len(p.Axes) == 0 {
		return fmt.Errorf("improvementrun: a plan has no axes in scope; an empty scope proposes nothing " +
			"and is not the same as every axis")
	}
	for _, a := range p.Axes {
		if !a.Valid() {
			return fmt.Errorf("improvementrun: %q is not one of the axes this platform configures (%s)",
				a, joinAxes(assessment.Axes()))
		}
	}
	return p.Bounded()
}

// Constraints projects the plan onto the optimizer's own grant-time constraint set.
//
// 🔴 THIS IS THE WHOLE OF "no fork of the loop" (task 2.4). P35 adds no scheduler, no iteration policy
// and no second stopping rule; it expresses its bounds in the vocabulary `optimizer.Constraints`
// already has, and `optimizer.Controller` enforces them exactly as it does for every other caller.
// A bound this method could not express would be a bound the loop does not enforce — which is why the
// plan's fields are chosen to map one-for-one rather than to read well.
//
// The mapping, and the one place it is not one-for-one:
//
//	SpendBudgetUSD              → BudgetCeilingUSD
//	CandidateCap                → MaxIterations       (see below)
//	Stopping.MinImprovement     → MinImprovement
//	Stopping.StallAfter         → StallK
//
// 🔴 `CandidateCap → MaxIterations` is exact rather than approximate because the loop consumes exactly
// one candidate per iteration (`cand := cands[0]`, and the hash is marked consumed). If that ever stops
// being true the cap stops being a cap, so `TestOneIterationConsumesOneCandidate` asserts it against
// the loop rather than trusting this comment.
//
// 🚫 `BlindSubBudgetUSD` is left ZERO, which disables blind expansion entirely, and that is a decision
// rather than an omission: blind expansion widens the search beyond the diagnosis, and a person who
// asked a bounded question about named axes did not ask for a grid sweep over everything else. The
// budget they agreed to is for the scope they were shown.
func (p Plan) Constraints() optimizer.Constraints {
	return optimizer.Constraints{
		BudgetCeilingUSD: p.SpendBudgetUSD,
		MaxIterations:    p.CandidateCap,
		MinImprovement:   p.Stopping.MinImprovement,
		StallK:           p.Stopping.StallAfter,
		// 🚫 Deliberately zero. See the doc comment.
		BlindSubBudgetUSD: 0,
	}
}

// InScope reports whether an axis label is one this plan admits.
//
// It takes a plain string because that is what a candidate carries (`optimizer.SearchCandidate.Dimension`
// and `proposal.Candidate.Dimensions`), and converting at the boundary is what keeps a candidate whose
// dimension is empty or misspelled from silently matching.
func (p Plan) InScope(axis string) bool {
	for _, a := range p.Axes {
		if string(a) == axis {
			return true
		}
	}
	return false
}

// NewPlanID derives the deterministic plan id.
//
// Every field that changes what the run will DO is in the hash. A field left out would let two
// materially different plans share an id, and the id is what an acknowledgement is recorded against —
// so a collision would let an acknowledgement of a $0.50 plan authorize a $50 one.
func NewPlanID(p Plan) string {
	axes := make([]string, 0, len(p.Axes))
	for _, a := range p.Axes {
		axes = append(axes, string(a))
	}
	sort.Strings(axes)
	h := sha256.Sum256([]byte(strings.Join([]string{
		"improvementrun.plan\x00",
		p.TenantID, p.WorkflowID, p.SourceRevision, string(p.Origin),
		strings.Join(axes, ","),
		fmt.Sprintf("%d", p.CandidateCap),
		fmt.Sprintf("%.6f", p.SpendBudgetUSD),
		fmt.Sprintf("%.6f", p.Stopping.MinImprovement),
		fmt.Sprintf("%d", p.Stopping.StallAfter),
	}, "\x00")))
	return "plan_" + hex.EncodeToString(h[:16])
}

func joinOrigins() string {
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		out = append(out, string(o))
	}
	return strings.Join(out, ", ")
}

func joinAxes(axes []assessment.Axis) string {
	out := make([]string, 0, len(axes))
	for _, a := range axes {
		out = append(out, string(a))
	}
	return strings.Join(out, ", ")
}
