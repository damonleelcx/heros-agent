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
//
// # v2 — P35 task 5.7
//
// Three sections were added and the version was bumped rather than the change being slipped in:
//
//	What changed        the AXIS and the NODE. A reviewer's first question is "which part of my agent
//	                    is this?", and a body that answered it only through a config hash was asking
//	                    them to resolve it themselves.
//	Eval-set decisiveness  how many of the cases behind the number could have said NO. 🔴 A score from
//	                    a set that cannot fail is not evidence, and P30 found that fact computed and
//	                    then not shown.
//	How to revert       ADR-001's founding property, written where the reviewer is. It is what makes
//	                    the whole phase acceptable — the blast radius of a delivery is a pull request
//	                    and its reversal is `git revert` — and it was the one place that said so.
//
// 🔴 Bumping is the DESIGNED response to an observable layout change, not an escape from one. A parser
// anchored on `pr-body/v1` now sees `pr-body/v2` and can say so, which is the entire reason the marker
// exists. Adding the sections silently under v1 would break exactly the automation the version was put
// there to protect.
const PRBodyContractVersion = "pr-body/v2"

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

	// ── P35 task 5.7 ─────────────────────────────────────────────────────────────────────────────

	// Axis and Node say WHICH PART of the agent this changes, in the nine-axis vocabulary the console
	// and the assessment already use. Optional only because P12's existing callers do not have them;
	// when they are empty the section is omitted rather than rendered with blanks.
	Axis string
	Node string
	// Operator names the change generator from `internal/proposal`'s catalog.
	Operator string

	// EvalSetCases is how many cases the number came from, and EvalSetIndecisive is how many of them
	// carry an oracle that can never return NO.
	EvalSetCases      int
	EvalSetIndecisive int
	// EvalSetCannotFail is computed SERVER-SIDE from the case list, exactly as the console computes it,
	// so the claim and the enumeration behind it cannot disagree. When true the body states that the
	// set could not have failed and does not present the score as evidence of quality.
	//
	// 🔴 This is the sharpest sentence in the body and the one most likely to be dropped as unhelpful.
	// It is the difference between "this scored 0.94" and "this scored 0.94 on a set where every case
	// passes whatever the agent does".
	EvalSetCannotFail bool

	// RevertRef is the branch or commit a reviewer reverts. Empty renders the general instruction,
	// which is still true: the change is one commit on one branch and `git revert` restores it exactly.
	RevertRef string
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
// Summary · Verified delta · Held-out status · Eval evidence · What changed · How decisive the eval set
// is · Lineage · How to revert this · Full evidence.
//
// ⚠️ "What changed" and "How decisive the eval set is" are CONDITIONAL — a caller with no axis and no
// eval-set counts renders neither. A parser anchoring on headings must therefore treat those two as
// optional, which is why the version was bumped: under v1 they did not exist at all.
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

	// ── What changed (P35 5.7) ───────────────────────────────────────────────
	//
	// 🔴 Placed BEFORE Lineage because a reviewer's first question is "which part of my agent is this?",
	// and the answer to that is a noun, not a hash. Omitted entirely rather than rendered with blanks
	// when a caller has no axis — an empty field under a heading reads as a fact we could not determine.
	if e.Axis != "" || e.Node != "" {
		b.WriteString("## What changed\n\n")
		if e.Axis != "" {
			fmt.Fprintf(&b, "- Axis: **%s**\n", e.Axis)
		}
		if e.Node != "" {
			fmt.Fprintf(&b, "- Node: `%s`\n", e.Node)
		}
		if e.Operator != "" {
			fmt.Fprintf(&b, "- Change: `%s`\n", e.Operator)
		}
		b.WriteString("\n")
	}

	// ── Eval-set decisiveness (P35 5.7) ──────────────────────────────────────
	//
	// 🔴 The number's DENOMINATOR, and the one statement that can withdraw the number's authority. A
	// score from a set where every case passes whatever the agent does is not evidence of quality, and
	// P30 found that fact computed and then not shown.
	if e.EvalSetCases > 0 {
		b.WriteString("## How decisive the eval set is\n\n")
		fmt.Fprintf(&b, "- Cases behind this number: **%d**\n", e.EvalSetCases)
		fmt.Fprintf(&b, "- Of those, cases whose check can never fail: **%d**\n", e.EvalSetIndecisive)
		if e.EvalSetCannotFail {
			b.WriteString("\n⚠️ **Every case in this set passes whatever the agent does.** The score above " +
				"is therefore not evidence that this change improves quality — it is evidence that the " +
				"change did not break a set that cannot break. Treat the delta as unproven.\n")
		}
		b.WriteString("\n")
	}

	// ── Lineage ──────────────────────────────────────────────────────────────
	b.WriteString("## Lineage\n\n")
	fmt.Fprintf(&b, "- `config_hash`: `%s`\n", e.ConfigHash)
	fmt.Fprintf(&b, "- `source_revision`: `%s`\n", e.SourceRevision)
	b.WriteString("\nThese anchor the change to a reproducible configuration: the same pair regenerates this exact diff.\n\n")

	// ── How to revert (P35 5.7) ──────────────────────────────────────────────
	//
	// 🔴 ADR-001's founding property, written where the reviewer is. The reason the whole phase is
	// acceptable is that the blast radius of a delivery is a pull request and its reversal is one
	// command — and until now the body was the one place that never said so. A reviewer who does not
	// know how to undo a change reviews it differently.
	b.WriteString("## How to revert this\n\n")
	b.WriteString("This change is one commit on one branch, and it is reversible with one command.\n\n")
	if e.RevertRef != "" {
		fmt.Fprintf(&b, "```\ngit revert %s\n```\n\n", e.RevertRef)
	} else {
		b.WriteString("```\ngit revert <the merge commit>\n```\n\n")
	}
	b.WriteString("Closing this pull request without merging reverts nothing, because nothing was " +
		"applied to your default branch. Nothing here is merged by the platform below the Autonomous " +
		"automation level.\n\n")

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
