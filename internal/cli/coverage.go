package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/transform"
)

// `heros coverage` — the offline, VERSIONED answer to "what can this platform apply, where?"
// ──────────────────────────────────────────────────────────────────────────────────────────
//
// This command exists because of one failure mode that costs a whole support cycle: the CLI refuses
// something the console accepts, and nobody can tell which side is stale. The fix is not to make the two
// agree by inspection — they will drift — but to make the disagreement DIAGNOSABLE:
//
//	🔴 the table is read from the engine, not written here (there is no second list to fall behind);
//	🔴 every refusal names the table's VERSION, so "yours says cov-1a2b, mine says cov-9f0e" is a
//	   one-line diagnosis instead of a mystery.
//
// The version is a hash of the table's CONTENT (transform.CoverageTableVersion), not a hand-bumped
// integer — a version that only moved when a row was added would miss the change that actually confuses
// a reader, which is a cell whose cause or note was narrowed.
//
// 🚫 No account, no network. Coverage is a property of the engine in this binary; asking a server what
// this binary can do would be both slower and wrong.

// Coverage prints the total per-axis, per-language coverage table.
//
// Human output is grouped by axis and sorted, so two builds diff cleanly. `--json` emits the same cells
// for a script, and both are the SAME read — a formatting fork would be the second source of truth this
// command exists to prevent.
func Coverage(cfg Config, s Streams) error {
	data := coverageDoc{
		Version:   transform.CoverageTableVersion(),
		Languages: transform.RegisteredLanguages(),
		Causes:    causeStrings(),
		Cells:     transform.AxisCoverage(),
	}

	s.Narratef("coverage %s — what this build can apply, per axis and language", data.Version)
	s.Narratef("🔴 a gap is a CELL, not an absence: every registered language appears on every axis, and a")
	s.Narratef("   refusal names which of three things is missing —")
	s.Narratef("     %-34s the value does not exist in source in ANY language", string(transform.CauseNotAtCallSite))
	s.Narratef("     %-34s this call site's own source cannot express it", string(transform.CauseCallSiteShape))
	s.Narratef("     %-34s the platform has not landed the artifact (named below)", string(transform.CauseNoMaterializer))
	for _, axis := range transform.CoverageAxes() {
		rows := transform.CoverageFor(axis)
		if len(rows) == 0 {
			continue
		}
		s.Narratef("── %s ──", axis)
		for _, c := range rows {
			status := "applies"
			if c.Refused() {
				status = "refuses: " + string(c.Cause)
			}
			s.Narratef("  %-11s %-34s %s", c.Language, truncate(c.Form, 34), status)
			if c.MissingArtifact != "" {
				s.Narratef("  %-11s %-34s   ↳ missing: %s", "", "", c.MissingArtifact)
			}
		}
	}
	s.Narratef("a coverage gap is identical on every plan: no tier, role, or flag materializes a refused cell")
	return s.EmitJSON("coverage", ExitOK, data, nil, nil)
}

type coverageDoc struct {
	Version   string                   `json:"coverage_version"`
	Languages []string                 `json:"registered_languages"`
	Causes    []string                 `json:"cause_classes"`
	Cells     []transform.CoverageCell `json:"cells"`
}

func causeStrings() []string {
	out := make([]string, 0, 3)
	for _, c := range transform.CauseClasses() {
		out = append(out, string(c))
	}
	sort.Strings(out)
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// CoverageRefusalSuffix is the sentence every offline refusal appends when the reason is a coverage
// cell. It names the local table's version so a verdict that differs from the hosted surface's is
// attributable rather than mysterious (P13 FR49).
//
// Exported so the author path and any future offline verdict use the SAME sentence: a second spelling
// would be a second thing to keep in step.
func CoverageRefusalSuffix() string {
	return fmt.Sprintf(" [offline coverage table %s]", transform.CoverageTableVersion())
}

// coverageSummaryLine is the one-line summary `status` prints, so an operator sees the version without
// running a second command.
func coverageSummaryLine() string {
	applies, refuses := 0, 0
	for _, c := range transform.AxisCoverage() {
		if c.Refused() {
			refuses++
			continue
		}
		applies++
	}
	return fmt.Sprintf("coverage %s — %d cell(s) apply, %d refuse by name, over %s",
		transform.CoverageTableVersion(), applies, refuses,
		strings.Join(transform.RegisteredLanguages(), "/"))
}
