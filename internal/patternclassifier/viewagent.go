package patternclassifier

// viewagent.go carries what the analysis agent contributed to a graph, and what a reader may do about
// it (tasks 8.2, 8.6, 8.7, 8.8).
//
// # Why this type lives here and is FILLED IN BY A CALLER
//
// `internal/herosagent` imports this package — its labels enter through this partitioner, which is D3's
// one arbitration path — so this package cannot import it back. That constraint turns out to be the
// right shape anyway: everything here is a CURRENT fact (what is this tenant's placement, is the agent
// answering right now) and the rest of GraphView is a fact about a stored analysis. A `ViewAgent`
// computed when the graph was discovered and stored beside it would be stale the moment an operator
// changed a placement — reporting `customer` on a page for a tenant switched to `disabled` yesterday.
//
// So the API layer attaches it at READ time, from the live placement, and `nil` is a legitimate value
// meaning this deployment runs no agent at all.

// AgentAction is what a reader may do from the graph page.
type AgentAction string

const (
	// ActionAnalyse: analysis can run for this tenant, and the page offers it.
	ActionAnalyse AgentAction = "analyse"
	// ActionRunLocally: the tenant is placed `customer`, so the platform cannot run anything — the page
	// names the command that can. 🔴 An ACTION rather than a refusal: "your organization runs this
	// itself" with no next step reads as a dead end, and the next step is one line of shell.
	ActionRunLocally AgentAction = "run_locally"
	// ActionNone: nothing can be offered, and Reason says why. 🚫 The page must not render a control
	// that cannot work — an action that fails on press is worse than an absent one with a sentence.
	ActionNone AgentAction = "none"
)

// ViewAgent is the analysis agent's contribution to this graph, and its current availability.
type ViewAgent struct {
	// State is how the inferred facts on this graph are known, from the four-value vocabulary. It is
	// the state of the AGENT's contribution, not of the graph: a fully rule-covered graph with the agent
	// switched off is `not_analysed` here and entirely `measured` in its composition, and both are true.
	State FactState `json:"state"`
	// StateSentence is State rendered for a reader, resolved here so a page cannot invent a fifth.
	StateSentence string `json:"state_sentence"`

	// Placement is which machine produced the inferred facts — `platform`, `customer` or `disabled`
	// (task 8.6). A STRING rather than a typed placement, because typing it would need the import this
	// file's header explains is impossible; the API layer sets it from the typed value it holds.
	Placement string `json:"placement"`
	// PlacementSentence attributes the facts on the page to a machine. Empty when nothing was inferred,
	// because attributing an absence is a sentence about nothing.
	PlacementSentence string `json:"placement_sentence,omitempty"`

	// Narrative is the agent's prose about this workflow. ALWAYS rendered as `assessed` and visually
	// distinct from measured facts.
	//
	// 🔴 EMPTY IS EMPTY. When the agent is off, unavailable, or produced no narrative, this is absent
	// and the page renders nothing in its place — 🚫 never a sentence assembled from the composition to
	// fill the space, which would be prose that looks assessed and was written by a template.
	Narrative string `json:"narrative,omitempty"`

	// Action is what the page may offer. Reason is why, and it is required for `none`.
	Action AgentAction `json:"action"`
	// ActionReason explains an unavailable action in words a reader can act on (task 8.7).
	ActionReason string `json:"action_reason,omitempty"`

	// Failure is set when the agent's own read FAILED — as distinct from being off. The panel renders
	// this and the rest of the page renders normally (task 8.8).
	//
	// 🚫 A HEROS failure must never become a full-screen error. Every other surface on the page is
	// rule-derived and did not depend on the agent at all; replacing them with an error would make an
	// optional subsystem's outage look like a total loss of the customer's data.
	Failure string `json:"failure,omitempty"`
}

// AgentUnavailable builds the panel for a deployment or tenant whose agent could not answer.
//
// A constructor rather than a struct literal at the call site, so the three fields that must agree —
// state, action and reason — cannot be set inconsistently. A panel reading `unavailable` while offering
// the analyse action is the specific inconsistency this prevents.
func AgentUnavailable(failure string) *ViewAgent {
	return &ViewAgent{
		State:         StateUnavailable,
		StateSentence: SentenceForState(StateUnavailable),
		Action:        ActionNone,
		ActionReason: "The analysis agent could not be reached, so nothing can be started from here. " +
			"Everything else on this page is rule-derived and is unaffected.",
		Failure: failure,
	}
}

// SentenceNotAnalysedYet is the `not_analysed` sentence for a tenant whose analysis is ENABLED and who
// simply has nothing from it on this workflow yet.
//
// 🔴 A SECOND SENTENCE FOR ONE STATE, and the reason is the defect that produced it. `not_analysed` is
// reached two ways — the agent is switched off, or the agent is on and has not produced anything for
// THIS workflow — and `SentenceForState` returned the switched-off wording for both. A page for a
// `platform`-placed organization therefore read "Analysis is off for this organization, which is the
// default", which is false and sends a reader to ask an operator to enable something already enabled.
//
// It is the same failure `llm_calls_note` had (task 1.3): one sentence over three cases, wrong in the
// case it ran in most often. The STATE is still `not_analysed` — that part was right — and what a
// reader needs is why, which is a property of the situation rather than of the state.
const SentenceNotAnalysedYet = "Analysis is enabled for this organization and has not contributed " +
	"anything to this workflow. Either it has not run against this revision yet, or it ran and " +
	"declined every proposal it considered — both are ordinary outcomes, and neither is a fault."

// AgentNotAnalysed builds the panel for a tenant whose agent is off — the DEFAULT state, and therefore
// the one most readers will see.
//
// 🔴 It is not a failure and must not read as one. Q2 makes `disabled` the default placement, so on a
// fresh deployment this is every workflow, and a page that rendered it in an alarming tone would report
// a deliberate configuration as a problem on every customer's first visit.
func AgentNotAnalysed(placement, reason string) *ViewAgent {
	return &ViewAgent{
		State:         StateNotAnalysed,
		StateSentence: SentenceForState(StateNotAnalysed),
		Placement:     placement,
		Action:        ActionNone,
		ActionReason:  reason,
	}
}
