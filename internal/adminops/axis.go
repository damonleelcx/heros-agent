package adminops

import (
	"context"
	"errors"
	"sort"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/transform"
)

// axis.go is the surface that lets the platform's own backlog be ordered by evidence (P26 wave 26d).
//
// Six axes resolve into `config_hash` and the console showed none of them, so the question *which
// materializer would unblock the most refused nodes across the fleet* had no data path — and the last
// several such decisions were made without one.
//
// # 🔴 It READS the one coverage source and computes nothing
//
// `transform.AxisCoverage()` is the same read the transform's refusal, preflight, the CLI and the
// customer console perform. The cells are passed through UNCHANGED: not merged, not re-ranked, not
// reformatted, not cached. A console that rolled up coverage would be a second opinion about coverage,
// and coverage is a claim about a customer's code.
//
// The counting this file does is counting REFUSALS, which is a different thing from restating one: a
// count is over the cells as received, and `TestTheAxisSurfaceOffersNoCellTheEngineRefuses` asserts
// parity in both directions against the real engine.
//
// # 🔴 An absent row renders as UNKNOWN, never as *not applicable*
//
// *Not applicable* says "your call site cannot carry this". The truth may be "we have not built the
// materializer". That substitution converts our backlog into the customer's problem, invisibly, and
// the customer has no way to discover it. So the read model has no "not applicable" value at all —
// only `applies`, `refused(cause)` and `unknown(missing)` — and the one thing that produces *not
// applicable* on a screen is a PRESENT cell whose named cause is `call-site-cannot-carry-it`.

// CellState is how one coverage cell renders. Three states, and there is no fourth.
type CellState string

const (
	// CellApplies — the engine materializes here.
	CellApplies CellState = "applies"
	// CellRefused — the engine refuses, and the cell carries the stable cause that says which kind.
	CellRefused CellState = "refused"
	// CellUnknown — no cell exists for this (axis, language, form). 🔴 It names the missing input. It is
	// NOT *not applicable*: that would be a claim about the customer's code, made by accident.
	CellUnknown CellState = "unknown"
)

// CellStates lists the three.
func CellStates() []CellState { return []CellState{CellApplies, CellRefused, CellUnknown} }

// CoverageCellRow is one cell of the matrix, rendered as received from the one coverage source.
type CoverageCellRow struct {
	Axis     string    `json:"axis"`
	Language string    `json:"language"`
	Form     string    `json:"form"`
	State    CellState `json:"state"`
	// Cause is the engine's STABLE cause identifier, verbatim. Never prose, and never translated into
	// a second vocabulary — the same cause renders identically on every surface that shows it.
	Cause string `json:"cause,omitempty"`
	// MissingInput is what would close this cell: the engine's missing artifact for a refusal, or —
	// for an unknown cell — the named input whose absence is why there is no answer.
	MissingInput string `json:"missing_input,omitempty"`
	// Note is the cell's own sentence from the engine. The human half; Cause is the machine half.
	Note string `json:"note,omitempty"`
}

// AxisRow is one axis's fleet picture.
//
// Counts, never scores. Only the eval harness ranks and only a P5.5 verified delta is a claim, so a
// refusal count must not be rendered with the visual grammar of a ranked evaluation result — see
// `IsRanking`, which is false here and says so on the wire.
type AxisRow struct {
	Axis string `json:"axis"`
	// Status is the axis's OWN declared status, from `transform.StatusFor`. The console does not
	// compute, adjust or reinterpret it.
	Status string `json:"status"`
	// Tenants and Nodes are fleet adoption — how many carry an override on this axis. nil means
	// UNKNOWN (no adoption source is wired), which is not zero and does not render as zero.
	Tenants *int `json:"tenants"`
	Nodes   *int `json:"nodes"`
	// Refusals is keyed by STABLE typed cause identifier, never prose. The three causes stay separate
	// because they are answered by three different parties, and one combined total tells all three of
	// them the same useless thing.
	Refusals map[string]int `json:"refusals"`
	// RefusalsByLanguage is the same counts, per language, so "which language is worst served" is
	// answerable without a second query.
	RefusalsByLanguage map[string]map[string]int `json:"refusals_by_language"`
	// DrillDown reaches the individual refused nodes behind the counts.
	DrillDown string `json:"drill_down"`
}

// ArtefactRank is one candidate artefact and how many refusals it would close.
type ArtefactRank struct {
	// Artefact is the thing that would be built — a form row, a list splitter, a statement resolver, a
	// registry row, a frontend field.
	Artefact string `json:"artefact"`
	// Closes is a COUNT of refused cells, not a score. The distinction is on the wire so a renderer
	// cannot mistake it for an evaluation result.
	Closes    int      `json:"closes"`
	Axes      []string `json:"axes"`
	Languages []string `json:"languages"`
	DrillDown string   `json:"drill_down"`
}

// CauseLegend is one typed cause and who answers it — rendered so the three never blur together.
type CauseLegend struct {
	Cause string `json:"cause"`
	// Owner is who can close it. Three causes, three parties: nobody, the customer's engineer, us.
	Owner string `json:"owner"`
	// Permanent marks a cause no artefact would close. A permanent cause names no missing artefact,
	// and attaching one would turn a boundary into a promise.
	Permanent bool   `json:"permanent"`
	Meaning   string `json:"meaning"`
}

// RefusedNode is one node behind a refusal count — the drill-down that keeps a fleet number from
// hiding a single tenant's pathological repository.
type RefusedNode struct {
	TenantID string `json:"tenant_id"`
	NodeID   string `json:"node_id"`
	Language string `json:"language"`
	Axis     string `json:"axis"`
	Cause    string `json:"cause"`
}

// AxisAdoptionSource reports fleet adoption and the refused nodes behind it.
//
// It is a seam rather than a table (D7). A deployment with no source reports adoption as UNKNOWN —
// `nil` counts, rendered as *unknown*, never as `0`. "No tenant uses this axis" and "we did not
// measure adoption" are opposite claims that look identical as a zero.
type AxisAdoptionSource interface {
	// Adoption returns the tenant and node counts carrying an override on an axis.
	Adoption(axis string) (tenants, nodes int)
	// RefusedNodes returns the nodes the engine refused, for the drill-down.
	RefusedNodes(axis string) []RefusedNode
	Describe() string
}

// AxisView is the operator's axis read model.
type AxisView struct {
	Axes []AxisRow `json:"axes"`
	// Matrix is the coverage table, rendered AS RECEIVED from the one coverage source.
	Matrix []CoverageCellRow `json:"matrix"`
	// Ranking orders the artefacts that would close the most refusals, so the backlog is ordered by
	// evidence rather than by taste.
	Ranking []ArtefactRank `json:"ranking"`
	// Legend keeps the three causes distinguishable wherever they appear.
	Legend []CauseLegend `json:"legend"`
	// IsRanking is FALSE and stated on the wire. Only the eval harness ranks and only a P5.5 verified
	// delta is a claim; the numbers here are counts, and a renderer must not dress them as results.
	IsRanking bool `json:"is_ranking"`
	// PlanIndependent is TRUE and stated on the wire: a coverage gap is not a plan boundary. It is
	// identical on every plan, and no tier unlocks a cell the engine refuses.
	PlanIndependent bool `json:"plan_independent"`
	// AdoptionKnown is false when no adoption source is wired. The counts are then nil and render as
	// unknown rather than as zero.
	AdoptionKnown bool `json:"adoption_known"`
	// CoverageSource names the ONE source, so an operator can check that this surface is not a second
	// opinion — and `CoverageVersion` is the engine's own table version, carried rather than derived.
	CoverageSource  string `json:"coverage_source"`
	CoverageVersion string `json:"coverage_version"`
	ReadOnly        bool   `json:"read_only"`
}

// AxisService serves the axis read model. READ-ONLY.
type AxisService struct {
	exec     *Executor
	adoption AxisAdoptionSource
}

// NewAxisService wires the read model. A nil adoption source is legal: adoption then reads as unknown.
func NewAxisService(exec *Executor, adoption AxisAdoptionSource) (*AxisService, error) {
	if exec == nil {
		return nil, errors.New("adminops: the axis read model needs the command path")
	}
	return &AxisService{exec: exec, adoption: adoption}, nil
}

// View returns the axis picture.
func (s *AxisService) View(ctx context.Context) (AxisView, error) {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapAxisRead, TargetGlobal)
	if err != nil {
		return AxisView{}, err
	}
	if _, err := s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID: sess.AdminID, Target: TargetGlobal, Action: adminaudit.ActionCrossTenantView,
		Reason: "axis oversight read", Result: "viewed",
		Evidence: map[string]string{"read_model": "axis"}, CreatedAt: s.exec.Now(),
	}); err != nil {
		return AxisView{}, errors.New("adminops: axis read refused — it could not be logged: " + err.Error())
	}

	view := AxisView{
		// Empty rather than nil, for the same reason as the sibling read models.
		Axes: []AxisRow{}, Matrix: []CoverageCellRow{}, Ranking: []ArtefactRank{},
		ReadOnly: true, IsRanking: false, PlanIndependent: true,
		AdoptionKnown:   s.adoption != nil,
		CoverageSource:  "transform.AxisCoverage() — the one coverage source",
		CoverageVersion: transform.CoverageTableVersion(),
		Legend:          causeLegend(),
	}
	if s.adoption != nil {
		view.CoverageSource += " · adoption from " + s.adoption.Describe()
	}

	// 🔴 ONE read. Not cached: a cached refusal the engine has since stopped refusing is precisely the
	// offered-cell-the-engine-refuses failure the parity assertion exists to catch.
	cells := transform.AxisCoverage()

	refusalsByAxis := map[string]map[string]int{}
	refusalsByAxisLang := map[string]map[string]map[string]int{}
	byArtefact := map[string]*ArtefactRank{}

	for _, c := range cells {
		row := CoverageCellRow{
			Axis: c.Axis, Language: c.Language, Form: c.Form,
			Cause: string(c.Cause), MissingInput: c.MissingArtifact, Note: c.Note,
		}
		if c.Refused() {
			row.State = CellRefused
		} else {
			row.State = CellApplies
		}
		view.Matrix = append(view.Matrix, row)

		if !c.Refused() {
			continue
		}
		cause := string(c.Cause)
		if refusalsByAxis[c.Axis] == nil {
			refusalsByAxis[c.Axis] = map[string]int{}
			refusalsByAxisLang[c.Axis] = map[string]map[string]int{}
		}
		refusalsByAxis[c.Axis][cause]++
		if refusalsByAxisLang[c.Axis][c.Language] == nil {
			refusalsByAxisLang[c.Axis][c.Language] = map[string]int{}
		}
		refusalsByAxisLang[c.Axis][c.Language][cause]++

		// Only a `no-materializer-for-this-language` refusal names an artefact. The other two classes
		// have nothing to build, and ranking them would put work on a backlog that cannot close them.
		if c.MissingArtifact == "" {
			continue
		}
		r, ok := byArtefact[c.MissingArtifact]
		if !ok {
			r = &ArtefactRank{Artefact: c.MissingArtifact, DrillDown: "?artefact=" + c.MissingArtifact}
			byArtefact[c.MissingArtifact] = r
		}
		r.Closes++
		r.Axes = appendUnique(r.Axes, c.Axis)
		r.Languages = appendUnique(r.Languages, c.Language)
	}

	for _, axis := range transform.CoverageAxes() {
		row := AxisRow{
			Axis:               axis,
			Status:             string(transform.StatusFor(axis)),
			Refusals:           refusalsByAxis[axis],
			RefusalsByLanguage: refusalsByAxisLang[axis],
			DrillDown:          "?axis=" + axis,
		}
		if row.Refusals == nil {
			row.Refusals = map[string]int{}
		}
		if row.RefusalsByLanguage == nil {
			row.RefusalsByLanguage = map[string]map[string]int{}
		}
		if s.adoption != nil {
			tenants, nodes := s.adoption.Adoption(axis)
			row.Tenants, row.Nodes = &tenants, &nodes
		}
		view.Axes = append(view.Axes, row)
	}

	for _, r := range byArtefact {
		sort.Strings(r.Axes)
		sort.Strings(r.Languages)
		view.Ranking = append(view.Ranking, *r)
	}
	// Ordered by the count, then by name so the order is stable between builds. This is an ORDERING of
	// counts, not a ranking of results — `IsRanking` is false and the surface must render it as counts.
	sort.Slice(view.Ranking, func(i, j int) bool {
		if view.Ranking[i].Closes == view.Ranking[j].Closes {
			return view.Ranking[i].Artefact < view.Ranking[j].Artefact
		}
		return view.Ranking[i].Closes > view.Ranking[j].Closes
	})
	return view, nil
}

// RefusedNodes returns the individual nodes behind an axis's refusal count.
func (s *AxisService) RefusedNodes(ctx context.Context, axis string) ([]RefusedNode, error) {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapAxisRead, TargetGlobal)
	if err != nil {
		return nil, err
	}
	if _, err := s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID: sess.AdminID, Target: TargetGlobal, Action: adminaudit.ActionCrossTenantView,
		Reason: "axis refusal drill-down", Result: "viewed",
		Evidence: map[string]string{"read_model": "axis", "axis": axis}, CreatedAt: s.exec.Now(),
	}); err != nil {
		return nil, errors.New("adminops: axis drill-down refused — it could not be logged: " + err.Error())
	}
	if s.adoption == nil {
		return nil, nil
	}
	return s.adoption.RefusedNodes(axis), nil
}

// causeLegend renders the three typed causes and who answers each, from the engine's own set.
func causeLegend() []CauseLegend {
	out := make([]CauseLegend, 0, 3)
	for _, c := range transform.CauseClasses() {
		l := CauseLegend{Cause: string(c)}
		switch c {
		case transform.CauseNotAtCallSite:
			l.Owner, l.Permanent = "nobody", true
			l.Meaning = "The value does not exist until run time. It cannot be written into source in ANY " +
				"language, so no materializer would change it and no plan includes it."
		case transform.CauseCallSiteShape:
			l.Owner = "the customer's engineer"
			l.Meaning = "THIS call site's own source cannot express it — arguments unpacked from a mapping, " +
				"a tool list assembled at run time. A materializer would not change it."
		case transform.CauseNoMaterializer:
			l.Owner = "the platform"
			l.Meaning = "We have not landed the rewriter, splitter, resolver or form row for this cell. " +
				"The ONLY class that names work we owe."
		}
		out = append(out, l)
	}
	return out
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}
