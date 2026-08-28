package api

import (
	"log/slog"
	"net/http"
	"sort"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/axisprojection"
	"github.com/heros-foreal/agentd/internal/changedelivery"
	"github.com/heros-foreal/agentd/internal/errorcode"
	"github.com/heros-foreal/agentd/internal/eventname"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/nodeaxisvalue"
	"github.com/heros-foreal/agentd/internal/telemetry"
	"github.com/heros-foreal/agentd/internal/transform"
)

// axisprojection.go serves `coverage × your nodes` (P29 §5.8).
//
// Two reads, both computed at request time from the coverage table this build carries and the structure
// this organization chose to send. Neither is materialised — see `internal/axisprojection`'s header for
// why a stored projection would become a second source of truth for a refusal.
//
// 🔴 A tenant with no reported structure gets `not-reported`, not an empty projection. The distinction
// is the whole point: an empty table reads as "nothing applies to you", and what is true is "you have
// not told us anything to apply it to" — which names a command the reader can run.

// MountAxisProjection registers the projection reads. Call after MountWorkflowIR.
func (s *Server) MountAxisProjection() {
	s.Mux.HandleFunc("GET /api/v1/workflows/{workflow_id}/axis-projection", s.handleAxisProjection)
	s.Mux.HandleFunc("GET /api/v1/workflows/{workflow_id}/delivery-projection", s.handleDeliveryProjection)
}

// projectionForRequest resolves the stored structure a projection is drawn from, or writes the
// three-state refusal and returns false.
func (s *Server) projectionForRequest(w http.ResponseWriter, r *http.Request) (axisprojection.Projection, bool) {
	p, _, ok := s.projectionAndIRForRequest(w, r)
	return p, ok
}

// projectionAndIRForRequest is the same resolution, returning the structure as well.
//
// 🔴 Two returns from one read rather than two reads. P37's per-node VALUES (`internal/nodeaxisvalue`)
// and P29's per-node VERDICTS (`internal/axisprojection`) are computed from the same stored structure,
// and reading it twice is two chances for them to disagree — the reader would see a node in the value
// list that the verdict list did not carry, with nothing on the screen to explain it.
func (s *Server) projectionAndIRForRequest(
	w http.ResponseWriter, r *http.Request,
) (axisprojection.Projection, linkingest.WorkflowIR, bool) {
	if s.workflowIR == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"state": StateNotMounted,
			"error": "this deployment does not accept workflow structure, so there is nothing to project",
		})
		return axisprojection.Projection{}, linkingest.WorkflowIR{}, false
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "reading a projection requires an authenticated tenant"})
		return axisprojection.Projection{}, linkingest.WorkflowIR{}, false
	}
	workflowID := r.PathValue("workflow_id")

	ir, found, err := s.workflowIR.Latest(principal.TenantID, workflowID)
	if err != nil {
		// A read failure is NOT "you reported nothing". Reporting it as the latter would tell a customer
		// their structure was never received, on a day the database was merely unreachable.
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"state": StateReadFailed,
			"error": "could not read this workflow's reported structure: " + err.Error(),
		})
		return axisprojection.Projection{}, linkingest.WorkflowIR{}, false
	}
	if !found {
		// 🔴 200, not 404, and it carries the NEXT ACTION. The workflow may well exist — the platform has
		// simply never been told its shape. A 404 here would read as "no such workflow", which sends the
		// reader to check an id that is correct.
		writeJSON(w, http.StatusOK, map[string]any{
			"state":            "not-reported",
			"workflow_id":      workflowID,
			"coverage_version": transform.CoverageTableVersion(),
			"node_count":       0,
			"detail": "This organization has not reported this workflow's structure, so there is nothing " +
				"to cross the coverage table with. The platform computes no verdict it was not sent.",
			"fill_with": "heros link --with-ir",
		})
		return axisprojection.Projection{}, linkingest.WorkflowIR{}, false
	}
	return axisprojection.Build(ir, transform.CoverageTableVersion()), ir, true
}

// handleAxisProjection serves the per-node VERDICTS (P29) and, beside them, the per-node current
// VALUES the editors bind to (P37 §5.1, §5.2).
//
// 🔴 An ADDITIVE field on an existing response rather than a new endpoint. P37 §2.4 (D-37.4) records
// that this phase adds no new endpoint shape: the two reads answer questions about the same
// (workflow, node) grid, are computed from the same stored structure, and splitting them would make a
// surface issue two requests whose answers can disagree about which nodes exist.
//
// `values` and `context_coverage` are both `omitempty`-free on purpose — a client that expects them and
// gets `null` must be able to tell that from a deployment that never sends them.
func (s *Server) handleAxisProjection(w http.ResponseWriter, r *http.Request) {
	p, ir, ok := s.projectionAndIRForRequest(w, r)
	if !ok {
		return
	}
	values := nodeaxisvalue.Build(ir)
	s.warnOnUnresolvedAxisValues(r, values)

	// 🔴 FR17 — the LIVE per-node context coverage that replaces `/app/context`'s hand-transcribed
	// table. Keyed by the languages this workflow actually reports, so the surface never has to pick a
	// row by guessing which language the reader meant.
	coverage := map[string][]nodeaxisvalue.PolicyCoverage{}
	for _, n := range ir.Nodes {
		if n.Language == "" {
			continue
		}
		if _, seen := coverage[n.Language]; seen {
			continue
		}
		// A language with no rewriter gets an EMPTY slice rather than being absent from the map. Absent
		// and empty read the same in JSON only if the client is careless; empty says "we looked and this
		// language has no rows", which is the answer.
		rows := nodeaxisvalue.ContextCoverageForLanguage(n.Language)
		if rows == nil {
			rows = []nodeaxisvalue.PolicyCoverage{}
		}
		coverage[n.Language] = rows
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"state":             "ok",
		"projection":        p,
		"values":            values,
		"context_coverage":  coverage,
		"covered_languages": nodeaxisvalue.CoveredLanguages(),
	})
}

// warnOnUnresolvedAxisValues records ONE warning per request naming the (node, axis) pairs whose value
// could not be resolved (P37 §5.3, §5.5).
//
// # 🔴 Why only `unresolved_in_ir`, and not every `not_measured`
//
// Four of the eight axes have NO field on the wire — memory, harness, loop, prompt — so every node is
// `not_measured` on all four, in every repository, forever. Warning on those would emit `4 × nodes`
// lines per request and would say nothing: it is the designed answer, not a fallback.
//
// `unresolved_in_ir` is different, and it is exactly the shape the logging convention requires a WARN
// for: a field that SHOULD carry a value carries discovery's sentinel or nothing, and the read falls
// back to reporting absence. That is a parse/shape event about a specific node, and an operator seeing
// its rate climb is seeing discovery degrade.
//
// # Why one line rather than one per pair
//
// A per-pair line on a four-hundred-node workflow is four hundred lines describing one condition, which
// is how a real signal becomes something people filter out. The pairs travel as a bounded attribute.
func (s *Server) warnOnUnresolvedAxisValues(r *http.Request, report nodeaxisvalue.Report) {
	var pairs []string
	for _, node := range report.Nodes {
		for _, v := range node.Values {
			if v.State == assessment.StateNotMeasured && v.MissingInput == assessment.MissingUnresolvedField {
				pairs = append(pairs, node.NodeID+"/"+string(v.Axis))
			}
		}
	}
	if len(pairs) == 0 {
		return
	}
	sort.Strings(pairs)
	// Bounded, for the reason a name may never carry an identifier: an unbounded attribute built from
	// customer node ids is an unbounded log line.
	const maxPairs = 20
	shown := pairs
	if len(shown) > maxPairs {
		shown = shown[:maxPairs]
	}
	traceID := traceIDFor(r)
	slog.Default().Warn("a reported node carries no resolvable value on an axis that has a wire field",
		"event", eventname.ConsoleSubjectResolved.String(),
		"error_code", errorcode.AxisValueUnresolved.String(),
		"request_id", telemetry.TraceIDFromContext(r.Context()),
		"trace_id", traceID,
		"span_id", telemetry.RequestSpanID(traceID),
		"workflow_id", report.WorkflowID,
		"unresolved_pairs", len(pairs),
		"sample", shown,
	)
}

// DeliveryProjectionRow is one reported node crossed with the delivery table's two routes.
type DeliveryProjectionRow struct {
	NodeID   string `json:"node_id"`
	Symbol   string `json:"symbol,omitempty"`
	Language string `json:"language,omitempty"`
	Axis     string `json:"axis"`
	// Deliverable is `source`, `runtime`, `both`, `neither` or `not-reported`.
	Deliverable string `json:"deliverable"`
	Cause       string `json:"cause,omitempty"`
	Owner       string `json:"owner,omitempty"`
}

// handleDeliveryProjection crosses the delivery table with this tenant's reported nodes.
//
// 🔴 The number this exists to produce is `undeliverable` — how many of THIS reader's nodes cannot
// receive a given change by EITHER route. `/app/delivery` renders a correct total delivery table today
// and says nothing about the reader; this is the multiplication nobody had performed.
func (s *Server) handleDeliveryProjection(w http.ResponseWriter, r *http.Request) {
	p, ok := s.projectionForRequest(w, r)
	if !ok {
		return
	}

	rows := make([]DeliveryProjectionRow, 0, len(p.Nodes)*len(p.Axes))
	undeliverable, reported := 0, 0
	for _, n := range p.Nodes {
		for _, c := range n.Cells {
			row := DeliveryProjectionRow{
				NodeID: n.NodeID, Symbol: n.Symbol, Language: n.Language, Axis: c.Axis,
			}
			switch c.State {
			case axisprojection.StateApplies:
				row.Deliverable = "source"
				reported++
			case axisprojection.StateRefused:
				reported++
				row.Cause, row.Owner = c.Cause, c.Owner
				// The RUNTIME route is asked only when the SOURCE route refused, and its answer comes
				// from `changedelivery`'s own table rather than from a rule restated here — a second copy
				// of that decision is a second thing that can disagree with the delivery page.
				if changedelivery.RuntimeDeliversDespiteSourceRefusal(c.Cause) {
					row.Deliverable = "runtime"
				} else {
					row.Deliverable = "neither"
					undeliverable++
				}
			default:
				// 🚫 A node the platform was not told about is NOT counted as undeliverable. That would be
				// a claim about the customer's code drawn from our own ignorance, and it is exactly the
				// number a reader would act on.
				row.Deliverable = "not-reported"
			}
			rows = append(rows, row)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"state":       "ok",
		"workflow_id": p.WorkflowID,
		// 🔴 THE DENOMINATORS, beside every count. `undeliverable` over `reported` is the honest ratio;
		// `undeliverable` over `cells` would silently treat every unreported cell as deliverable.
		"undeliverable":    undeliverable,
		"reported_cells":   reported,
		"cells":            len(rows),
		"node_count":       p.NodeCount,
		"coverage_version": p.CoverageVersion,
		"stale":            p.Stale,
		"rows":             rows,
	})
}
