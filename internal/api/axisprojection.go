package api

import (
	"net/http"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/axisprojection"
	"github.com/heros-foreal/agentd/internal/changedelivery"
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
	if s.workflowIR == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"state": StateNotMounted,
			"error": "this deployment does not accept workflow structure, so there is nothing to project",
		})
		return axisprojection.Projection{}, false
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "reading a projection requires an authenticated tenant"})
		return axisprojection.Projection{}, false
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
		return axisprojection.Projection{}, false
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
		return axisprojection.Projection{}, false
	}
	return axisprojection.Build(ir, transform.CoverageTableVersion()), true
}

func (s *Server) handleAxisProjection(w http.ResponseWriter, r *http.Request) {
	p, ok := s.projectionForRequest(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": "ok", "projection": p})
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
