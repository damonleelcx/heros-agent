package api

import (
	"net/http"

	"github.com/heros-foreal/agentd/internal/changedelivery"
	"github.com/heros-foreal/agentd/internal/transform"
)

// GET /api/p13/delivery — the total delivery read model (P13 §23.18, FR57/FR58/FR66).
//
// # Why the console gets a table rather than a status word
//
// "Delivery" used to render as one word per change — delivered, pending — and the word had no room for
// the thing that is actually true most of the time: THE REWRITER REFUSED, so there is no diff, so there
// is no pull request, so nothing is going to happen. On screen that state was indistinguishable from
// "queued behind other work", which is how a dead end came to look like a promise.
//
// So the payload is a ROUTE TABLE. Every change kind answers for both routes, each refusal carries its
// cause AND its owner, and a change refused by both routes is `undeliverable` — a state with no
// hopeful synonym.
//
// 🚫 The BFF is a pass-through, not a brain: no merging, no re-ranking, no status translation. Every
// field is emitted by `changedelivery` and rendered as received. A console that recomputed eligibility
// would be the second delivery table the whole contract exists to prevent, and it would drift in the
// usual direction — the copy is always the optimistic one.
type ChangeDeliveryView struct {
	// Version is the coverage table version the source-route column was read against, so a
	// console/CLI disagreement is attributable rather than mysterious.
	Version string `json:"version"`
	// Routes are the two, in the order every surface renders them: source first, because it is the
	// default and the only road to permanence.
	Routes []deliveryRouteMeta `json:"routes"`
	// Causes are the three stable identifiers with their owner and permanence, sent so the console
	// branches on DATA rather than on prose.
	Causes []deliveryCauseMeta `json:"causes"`
	// States is the closed state set, so the page's legend cannot drift from the state machine.
	States []deliveryStateMeta `json:"states"`
	// Languages is the registered language set the source column is total over.
	Languages []string `json:"languages"`
	// Cells is the runtime/source table: every change kind × both routes.
	Cells []deliveryCellView `json:"cells"`
	// SourceCells is the source route's per-language reality for each change kind — the half that
	// genuinely varies by frontend.
	SourceCells []deliverySourceCellView `json:"source_cells"`
}

type deliveryRouteMeta struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Permanence says whether this route can make a change durable. It is the sentence that keeps a
	// rollout from being read as a deployment.
	Permanence string `json:"permanence"`
}

type deliveryCauseMeta struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
	// Permanent marks a boundary rather than unbuilt work. 🔴 The console renders these two
	// differently, and the identifier — never the sentence — selects the treatment.
	Permanent bool   `json:"permanent"`
	Label     string `json:"label"`
}

type deliveryStateMeta struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type deliveryCellView struct {
	Axis            string `json:"axis"`
	Change          string `json:"change"`
	Route           string `json:"route"`
	Status          string `json:"status"`
	Cause           string `json:"cause,omitempty"`
	Owner           string `json:"owner,omitempty"`
	Permanent       bool   `json:"permanent,omitempty"`
	MissingArtifact string `json:"missing_artifact,omitempty"`
	Note            string `json:"note,omitempty"`
	// Contingent marks a refusal whose reason could change, as distinct from a boundary that cannot.
	//
	// 🔴 Memory and wiring both refuse as "not data". Wiring is a property of compiled code; memory is
	// waiting on a runtime component that could exist. A reader who cannot tell them apart draws the
	// wrong conclusion from one of them, so the distinction is a field rather than a matter of prose.
	Contingent bool `json:"contingent,omitempty"`
	// MissingComponent names what a contingent refusal waits on. 🚫 Never a date.
	MissingComponent string `json:"missing_component,omitempty"`
	// BoundOnly marks a cell that is eligible on a bound node and reports `node-not-bound` on an
	// inline one. It is computed here rather than inferred, so the console does not have to know the
	// evaluation order to render the migration hint in the right place.
	BoundOnly bool `json:"bound_only,omitempty"`
}

type deliverySourceCellView struct {
	Change          string `json:"change"`
	Language        string `json:"language"`
	Status          string `json:"status"`
	Cause           string `json:"cause,omitempty"`
	Permanent       bool   `json:"permanent,omitempty"`
	MissingArtifact string `json:"missing_artifact,omitempty"`
	Note            string `json:"note,omitempty"`
}

func changeDeliveryReadModel() ChangeDeliveryView {
	out := ChangeDeliveryView{
		Version:   transform.CoverageTableVersion(),
		Languages: transform.RegisteredLanguages(),
		Routes: []deliveryRouteMeta{
			{ID: string(changedelivery.RouteSource), Label: "Pull request",
				Permanence: "Permanent. A codemod produces a diff, the pull request carries it and its evidence, and a human merges it."},
			{ID: string(changedelivery.RouteRuntime), Label: "Gradual rollout",
				Permanence: "Temporary. Runs in your process, expires to the parent arm, and produces evidence — never delivery. Permanence still costs a merged pull request."},
		},
	}
	for _, c := range changedelivery.Causes() {
		out.Causes = append(out.Causes, deliveryCauseMeta{
			ID: string(c), Owner: c.Owner(), Permanent: c.Permanent(), Label: c.Label(),
		})
	}
	for _, s := range changedelivery.States() {
		out.States = append(out.States, deliveryStateMeta{ID: string(s), Label: deliveryStateLabel(s)})
	}

	for _, cell := range changedelivery.Table() {
		v := deliveryCellView{
			Axis: cell.Axis, Change: string(cell.Change), Route: string(cell.Route),
			Status: string(cell.Status), Note: cell.Note,
			MissingArtifact: cell.MissingArtifact,
		}
		if cell.Refused() {
			v.Cause = string(cell.Cause)
			v.Owner = cell.Cause.Owner()
			// A contingent refusal is NOT rendered as a permanent boundary, even though it carries the
			// same cause: the cause is about what the change is today, and permanence is about whether
			// that can change.
			v.Permanent = cell.Cause.Permanent() && !cell.Contingent
			v.Contingent = cell.Contingent
			v.MissingComponent = cell.MissingComponent
		}
		if cell.Route == changedelivery.RouteRuntime {
			// A cell is bound-only when the bound answer differs from the inline one. Computed from the
			// same function the authoring gate calls, so the hint cannot promise a migration that would
			// not actually unlock anything.
			bound, err1 := changedelivery.RuntimeEligibility(cell.Change, true)
			inline, err2 := changedelivery.RuntimeEligibility(cell.Change, false)
			if err1 == nil && err2 == nil {
				v.BoundOnly = bound.Eligible && inline.Cause == changedelivery.CauseNodeNotBound
			}
		}
		out.Cells = append(out.Cells, v)
	}

	for _, kind := range changedelivery.ChangeKinds() {
		for _, lang := range out.Languages {
			src := changedelivery.SourceOutcomeFor(kind, lang, "")
			cell := deliverySourceCellView{
				Change: string(kind), Language: lang,
				Status: string(changedelivery.StatusRefuses),
				Note:   src.Note,
			}
			switch {
			case src.Materializes:
				cell.Status = string(changedelivery.StatusDelivers)
			case src.Varies:
				// 🔴 Some forms in this language materialize and some do not. Sending `refuses` would let
				// a consumer conclude the change is a dead end here, when a call site next door gets a
				// diff. The cause travels too, so a reader can see which shape is the one that refuses.
				cell.Status = string(changedelivery.StatusVaries)
				cell.Cause = src.Cause
				cell.MissingArtifact = src.MissingArtifact
			default:
				cell.Cause = src.Cause
				cell.Permanent = src.Permanent
				cell.MissingArtifact = src.MissingArtifact
			}
			out.SourceCells = append(out.SourceCells, cell)
		}
	}
	return out
}

// deliveryStateLabel is the one place a state's sentence lives.
//
// 🔴 Note what none of these say. There is no "in review", no "queued", no "processing" — the states a
// dead end used to borrow. `undeliverable` is terminal and honest, and `rollout-active` says out loud
// that it is not a delivery.
func deliveryStateLabel(s changedelivery.State) string {
	switch s {
	case changedelivery.StateNothingToDeliver:
		return "Nothing to deliver — this change resolves to the configuration already running."
	case changedelivery.StateUndeliverable:
		return "Undeliverable — every route refused, and each names why."
	case changedelivery.StateSourcePending:
		return "A pull request is the next step; a human merges it."
	case changedelivery.StateRolloutActive:
		return "A rollout is running. That is evidence under real load, not a delivery."
	case changedelivery.StateDelivered:
		return "Delivered — a pull request carrying this change was merged."
	}
	return ""
}

// handleDelivery serves the read model.
//
// It takes no tenant, no plan and no role, and that is an assertion rather than an omission: the
// delivery table is IDENTICAL ON EVERY PLAN. What a route can carry is a property of the change, not of
// what someone paid — and a handler that accepted a plan would be a handler someone could later make
// vary by it.
func (s *Server) handleDelivery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, changeDeliveryReadModel())
}
