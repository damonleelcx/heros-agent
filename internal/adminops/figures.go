package adminops

import (
	"strconv"

	"github.com/heros-foreal/agentd/internal/linkingest"
)

// figures.go pairs a SUM-derived figure with its link coverage — in the TYPE, so a figure cannot reach
// a surface without one (P26 task 2.1).
//
// # Why the pairing is structural rather than a convention
//
// `openspec/project.md` states the rule once: metering counts only what it observed, SUM derives from
// LINKED runs, the platform never infers or extrapolates unlinked spend, and link coverage is
// displayed wherever a derived figure is shown. The customer console honours it. The operator billing
// surface, which predates run linking, did not — there was no coverage field in this package or on the
// page, and no assertion that would have noticed.
//
// A convention would have been fixed by adding a field and remembering to render it. This is fixed by
// making the figure and its coverage ONE VALUE: `Coverage == nil` means unknown, and a figure with
// unknown coverage is not rendered at all. A later change that adds a derived figure gets the coverage
// requirement whether or not its author read this comment.
//
// # Why unknown coverage withholds the figure instead of qualifying it
//
// The bundle scanner in this repository already made the argument about a different measurement: it
// refuses to report a number it cannot measure honestly, because a wrong number is worse than no
// number — the wrong number gets acted on. A billing operator issuing a credit against a SUM figure
// whose link coverage is 31% is that sentence with money attached, and a footnote beside the figure
// reads as reassurance rather than as a caveat.

// DerivedFigure is one SUM-derived figure and the link coverage that qualifies it.
//
// Value is a STRING because the read model formats it: the console renders what the platform sent and
// derives nothing, so a figure cannot acquire a second value by being re-rounded in a browser.
type DerivedFigure struct {
	Value string `json:"value"`
	// Coverage is the percentage of the tenant's observed runs that were linked, 0–100.
	//
	// 🔴 nil means UNKNOWN, and unknown means the figure is NOT RENDERED. It is a pointer rather than a
	// float with a sentinel because a sentinel (-1, or 0) is a value some caller will eventually
	// compare against, and "0% coverage" and "we do not know the coverage" are opposite claims that
	// look identical as a number.
	Coverage *float64 `json:"coverage"`
	// Source names where the figure came from, so an operator can tell a verified figure from an
	// estimate without leaving the page — e.g. the P5.5 verified-delta ledger for a gainshare figure.
	Source string `json:"source"`
	// Basis states what the figure counts and what it deliberately does not, in the operator's words.
	Basis string `json:"basis,omitempty"`
	// RunsLinked / RunsReported are the coverage fraction's own numerator and denominator, carried so
	// the percentage can be checked rather than believed.
	RunsLinked   int `json:"runs_linked"`
	RunsReported int `json:"runs_reported"`
}

// Renderable reports whether the figure may be displayed. A figure whose coverage is unknown is not.
func (f DerivedFigure) Renderable() bool { return f.Coverage != nil }

// formatQuantity renders a measured quantity for a read model.
//
// The read model formats; the console does not. `-1` precision emits the shortest representation that
// round-trips, so a whole number does not acquire trailing zeros and a fractional one does not lose
// digits to a fixed width chosen for a different figure.
func formatQuantity(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// NewDerivedFigure pairs a formatted value with a link-coverage reading.
//
// It is the only constructor, and it takes the coverage as a REQUIRED argument rather than as an
// optional setter. A setter would make "forgot to call it" a reachable state, which is the exact
// failure this type exists to make unreachable.
func NewDerivedFigure(value string, cov linkingest.LinkCoverage, source, basis string) DerivedFigure {
	f := DerivedFigure{Value: value, Source: source, Basis: basis,
		RunsLinked: cov.RunsLinked, RunsReported: cov.RunsReported}
	if !cov.Known {
		// Coverage stays nil. The figure is withheld rather than shown bare — the surface says coverage
		// is unknown instead, which is a different sentence and prompts a different action.
		return f
	}
	pct := 100.0
	if cov.RunsReported > 0 {
		pct = float64(cov.RunsLinked) / float64(cov.RunsReported) * 100
	}
	f.Coverage = &pct
	return f
}

// CoverageView is the link-coverage reading itself, as the surface states it beside the figures.
//
// It is reported even when no figure is rendered: "coverage is unknown" is the answer in that case,
// and an operator who sees nothing at all cannot tell it apart from a page that failed to load.
type CoverageView struct {
	Known        bool    `json:"known"`
	Complete     bool    `json:"complete"`
	Percent      float64 `json:"percent"`
	RunsLinked   int     `json:"runs_linked"`
	RunsReported int     `json:"runs_reported"`
	// Statement is the sentence the surface shows. Written here rather than in the browser because the
	// console renders a read model and does not compose claims about measurement.
	Statement string `json:"statement"`
}

// LinkCoverageSource reports how much of a tenant's activity the platform actually observed.
//
// Narrow on purpose: the operator billing surface needs the coverage reading and nothing else from run
// linking, and a wider dependency would let a later change reach for the linked runs themselves.
type LinkCoverageSource interface {
	Coverage(tenantID string) linkingest.LinkCoverage
}

// coverageView renders a coverage reading for the surface.
func coverageView(cov linkingest.LinkCoverage, wired bool) CoverageView {
	if !wired {
		return CoverageView{Statement: "Link coverage is unknown: this deployment does not carry run linking, " +
			"so the platform cannot say what share of activity the figures below reflect. " +
			"No SUM-derived figure is shown."}
	}
	if !cov.Known {
		return CoverageView{RunsLinked: cov.RunsLinked, RunsReported: cov.RunsReported,
			Statement: "Link coverage is unknown: no run count has been reported for this tenant, so the " +
				"platform cannot say what share of activity a SUM-derived figure would reflect. " +
				"No SUM-derived figure is shown, and none is inferred or extrapolated."}
	}
	v := CoverageView{Known: true, Complete: cov.Complete, RunsLinked: cov.RunsLinked, RunsReported: cov.RunsReported}
	if cov.RunsReported > 0 {
		v.Percent = float64(cov.RunsLinked) / float64(cov.RunsReported) * 100
	} else {
		v.Percent = 100
	}
	v.Statement = "SUM-derived figures below count LINKED runs only — " +
		strconv.Itoa(cov.RunsLinked) + " of " + strconv.Itoa(cov.RunsReported) + " runs observed. " +
		"Unlinked spend is never inferred, extrapolated or scaled."
	if cov.Complete {
		v.Statement = "Link coverage is complete: every run the platform was told about is linked (" +
			strconv.Itoa(cov.RunsLinked) + " of " + strconv.Itoa(cov.RunsReported) + ")."
	}
	return v
}

// The provenance strings a figure names on the surface. Constants because a provenance spelled two
// ways is two claims, and the whole point of naming it is that an operator can tell them apart.
const (
	// SourceVerifiedDeltaLedger is the ONLY source a gainshare or verified-savings figure may draw on.
	SourceVerifiedDeltaLedger = "P5.5 verified-delta ledger"
	// SourceMeteredUsage is the recorded usage records — what the platform metered from linked runs.
	SourceMeteredUsage = "recorded usage records (linked runs only)"
)

// ExclusionUnverifiedAuthoredChange is the sentence every aggregate improvement, savings and quality
// figure carries. It is a constant so the statement cannot drift between the surfaces that make it.
const ExclusionUnverifiedAuthoredChange = "Unverified authored changes are excluded: a change the " +
	"harness never measured contributes zero to every improvement, savings and quality figure."
