package api

import (
	"net/http"

	"github.com/heros-foreal/agentd/internal/transform"
)

// GET /api/p13/coverage — the total per-axis, per-language coverage read model.
//
// # Why the console gets a read model rather than a list of what works
//
// The console's job on this surface is to say what happens to a reader's node, and the honest answer has
// three shapes, not two: it applies, their call site cannot carry it, or we have not built it. A payload
// carrying only the cells that work would force the console to INFER the third from an absence — and an
// absence renders as "not applicable", which is a claim about the reader's code.
//
// So the payload is TOTAL: every axis × every registered language × every form, each with its status,
// its cause class, and — for a platform gap only — the artifact whose absence explains it.
//
// 🚫 The BFF is a pass-through, not a brain: no merging, no re-ranking, no status translation. Every
// field below is emitted by transform.AxisCoverage() and rendered as received. A console that recomputed
// any of it would be the second coverage source the whole contract exists to prevent.
type coverageView struct {
	// Version is the content hash of the table this build refuses from. It is displayed, because a
	// console/CLI disagreement is only diagnosable when both name their table.
	Version string `json:"version"`
	// Languages is the registered language set, so a reader can see the table is total rather than
	// inferring it from the rows present.
	Languages []string `json:"languages"`
	// Axes is the axis order the console renders in — server-side, so two surfaces cannot disagree
	// about which axis comes first.
	Axes []string `json:"axes"`
	// Causes are the three stable identifiers, sent so the console branches on data rather than prose.
	Causes []coverageCause `json:"causes"`
	Cells  []coverageCell  `json:"cells"`
}

// coverageCause pairs a stable identifier with the sentence that says WHOSE move it is. The console
// renders three visually distinct states from these, and the identifier — never the sentence — is what
// selects the treatment.
type coverageCause struct {
	ID string `json:"id"`
	// Owner is who can close it: "nobody", "you", or "the platform". It is the one word that decides
	// what a reader does next, so it is computed once here rather than inferred from the id at each
	// surface.
	Owner string `json:"owner"`
	Label string `json:"label"`
}

type coverageCell struct {
	Axis            string `json:"axis"`
	Language        string `json:"language"`
	Form            string `json:"form"`
	Status          string `json:"status"`
	Cause           string `json:"cause,omitempty"`
	MissingArtifact string `json:"missing_artifact,omitempty"`
	Note            string `json:"note,omitempty"`
}

func coverageReadModel() coverageView {
	cells := transform.AxisCoverage()
	out := coverageView{
		Version:   transform.CoverageTableVersion(),
		Languages: transform.RegisteredLanguages(),
		Axes:      transform.CoverageAxes(),
		Causes: []coverageCause{
			{ID: string(transform.CauseNotAtCallSite), Owner: "nobody",
				Label: "Not expressible in source — the value does not exist until run time, in any language."},
			{ID: string(transform.CauseCallSiteShape), Owner: "you",
				Label: "This call site cannot carry it — its own source has nothing to change."},
			{ID: string(transform.CauseNoMaterializer), Owner: "the platform",
				Label: "Not yet applied by the platform — the missing artifact is named."},
		},
		Cells: make([]coverageCell, 0, len(cells)),
	}
	for _, c := range cells {
		out.Cells = append(out.Cells, coverageCell{
			Axis: c.Axis, Language: c.Language, Form: c.Form,
			Status: string(c.Status), Cause: string(c.Cause),
			MissingArtifact: c.MissingArtifact, Note: c.Note,
		})
	}
	return out
}

// CoverageReadModelForPreview exposes the read model to the preview seeder, so the console's
// browser-checkable preview is seeded from the ENGINE rather than from a hand-written fixture. A
// hand-written one drifts, and a preview that drifts is a preview that stops catching anything.
func CoverageReadModelForPreview() any { return coverageReadModel() }

// handleCoverage serves the read model. It takes no tenant, no plan and no role — and that absence is
// the assertion: coverage is a property of the engine in this build, so no entitlement can move a cell
// (P13 FR51). A future contributor adding a plan input has to change this signature.
func (s *Server) handleCoverage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, coverageReadModel())
}
