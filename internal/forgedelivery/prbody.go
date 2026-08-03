package forgedelivery

import (
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/verification"
)

// PRBodyContractVersion is the single source of truth for the pull-request body layout (task 1.1 /
// contract §1). It is the first line of every delivered body, so a customer's PR-parsing automation has
// a stable anchor and a layout change it could notice is a visible version bump rather than a silent
// break. Bump it only when the rendered layout changes in a way a parser could observe.
const PRBodyContractVersion = "pr-body/v1"

// prBodyMarker is the machine-readable marker line.
func prBodyMarker() string { return "<!-- heros-delivery: " + PRBodyContractVersion + " -->" }

// Evidence is everything the pull request body renders about a verified change. It is narration OVER
// the structured verdict (verification.Narrate's discipline): every number is read off Verdict, never
// re-derived here, so the body can never contradict the source of truth.
type Evidence struct {
	Title          string
	Level          string // "advisory" | "assisted" | "autonomous", verbatim
	Verdict        verification.Verdict
	ConfigHash     string
	SourceRevision string
	// ConsoleRef is the canonical console reference that opens the full evidence (P9's rules). It
	// resolves from anywhere it is pasted (task 3.3).
	ConsoleRef string
	// DiffStat is a short human summary of the diff (files/lines). The diff itself rides the pull
	// request; the body summarizes it so a reviewer sees the shape before opening the Files tab.
	DiffStat string
}

// DeltaReadsAsTie reports whether a verified delta's confidence interval overlaps the baseline. When it
// does, the change is a TIE and must be rendered as one, never as an improvement (task 3.2 / FR): the
// baseline sits at a zero delta, so an interval whose low bound is at or below zero includes "no
// change". A pull request that oversells a tie spends a reviewer's trust once and does not get it back.
func DeltaReadsAsTie(v verification.Verdict) bool {
	return v.Delta.Low <= 0
}

// RenderPRBody renders the pull-request body from evidence. It is DETERMINISTIC and mode-independent:
// the same evidence produces byte-identical output in CI-mediated and hosted-App mode (design
// Decision 3), which the parity gate byte-compares. It contains no credential on any path.
//
// The sections are fixed and ordered (contract §1) so a parser can anchor on the headings:
// Summary · Verified delta · Held-out status · Eval evidence · Lineage · Evidence in the console.
func RenderPRBody(e Evidence) string {
	var b strings.Builder
	v := e.Verdict

	b.WriteString(prBodyMarker())
	b.WriteString("\n\n")

	// ── Summary ──────────────────────────────────────────────────────────────
	b.WriteString("## Summary\n\n")
	fmt.Fprintf(&b, "%s\n\n", e.Title)
	fmt.Fprintf(&b, "This pull request was opened by heros-agent at the **%s** automation level. ", e.Level)
	b.WriteString("The change was verified on held-out data before delivery; the evidence is below and the full record is linked at the end.\n\n")

	// ── Verified delta — rendered AS COMPUTED (task 3.2) ─────────────────────
	b.WriteString("## Verified delta\n\n")
	if DeltaReadsAsTie(v) {
		// The interval overlaps the baseline: this is a tie, and it is stated as one.
		fmt.Fprintf(&b,
			"**No measurable improvement (a tie).** The %s delta is %+.3f with a 95%% confidence interval of [%+.3f, %+.3f], which overlaps the baseline — the change is statistically indistinguishable from no change on this evidence.\n\n",
			metricLabel(v), v.Delta.Mean, v.Delta.Low, v.Delta.High)
	} else {
		fmt.Fprintf(&b,
			"**Verified %s improvement of %+.3f** (95%% confidence interval [%+.3f, %+.3f]). The interval lies entirely above the baseline, so the improvement is not an artifact of noise.\n\n",
			metricLabel(v), v.Delta.Mean, v.Delta.Low, v.Delta.High)
	}

	// ── Held-out status ──────────────────────────────────────────────────────
	b.WriteString("## Held-out status\n\n")
	fmt.Fprintf(&b, "Generalization: **%s**. ", verification.HeldOutLabel(v))
	if v.HeldOut {
		b.WriteString("The delta was measured on cases the proposal was not generated from, so it is expected to generalize.\n\n")
	} else {
		b.WriteString("No held-out split could be formed, so the delta is measured in-sample and generalizes unproven.\n\n")
	}

	// ── Eval evidence ────────────────────────────────────────────────────────
	b.WriteString("## Eval evidence\n\n")
	// The COUNTS, not len() of the id lists: a verdict reported by the customer's CI carries no ids
	// (they do not cross the P11 boundary), and len(nil) would print "Cases fixed: 0" in a pull request
	// body under a change that fixed four.
	fmt.Fprintf(&b, "- Cases fixed: **%d**\n", v.CasesFixedCount)
	fmt.Fprintf(&b, "- Cases broken: **%d**\n", v.CasesBrokenCount)
	fmt.Fprintf(&b, "- Cost impact: **%+.4f $/run**\n", v.CostDelta)
	fmt.Fprintf(&b, "- Latency impact: **%+.0f ms/run**\n", v.LatencyDelta)
	if e.DiffStat != "" {
		fmt.Fprintf(&b, "- Diff: %s\n", e.DiffStat)
	}
	b.WriteString("\n")

	// ── Lineage ──────────────────────────────────────────────────────────────
	b.WriteString("## Lineage\n\n")
	fmt.Fprintf(&b, "- `config_hash`: `%s`\n", e.ConfigHash)
	fmt.Fprintf(&b, "- `source_revision`: `%s`\n", e.SourceRevision)
	b.WriteString("\nThese anchor the change to a reproducible configuration: the same pair regenerates this exact diff.\n\n")

	// ── Evidence in the console ──────────────────────────────────────────────
	b.WriteString("## Full evidence\n\n")
	if e.ConsoleRef != "" {
		fmt.Fprintf(&b, "Open the full verification record, held-out methodology, and eval breakdown in the console:\n\n%s\n", e.ConsoleRef)
	} else {
		b.WriteString("The full verification record is available in the heros-agent console for this configuration.\n")
	}

	return b.String()
}

// metricLabel is the metric name for the body, defaulting to "quality" when unnamed so the sentence
// reads rather than showing an empty metric.
func metricLabel(v verification.Verdict) string {
	if strings.TrimSpace(v.Metric) == "" {
		return "quality"
	}
	return v.Metric
}
