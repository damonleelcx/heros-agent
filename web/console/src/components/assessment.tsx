import { ArrowUpRight, Bot, FileSearch } from "lucide-react";

import { Chip, Section, Banner, Card } from "@/components/primitives";
import { TONE_CLASS } from "@/lib/status";
import { cx } from "@/lib/cx";
import {
  AXIS_LABEL,
  AXIS_QUESTION,
  MISSING_INPUT_LABEL,
  ORIGIN_LABEL,
  ORIGIN_TONE,
  REFUSAL_CAUSE_LABEL,
  STATE_LABEL,
  STATE_MEANING,
  STATE_TONE,
  groupByRank,
} from "@/lib/assessment";
import type { AssessmentView, AxisDiff, EvalSetReport, FindingView, Tally } from "@/lib/types.generated";

/**
 * assessment.tsx renders P33's report: nine axes, four states, no composite.
 *
 * # 🚫 What this component must never grow
 *
 * A number spanning axes. There is no score here, no grade, no percentage-complete, no ring, no
 * "7 of 9 healthy" — program ruling R4, and `assessment.test.mjs` scans this file for the shapes a
 * composite arrives in. The honest summary a reader is owed is the TALLY: five numbers, four of which
 * sum to nine, from which no ordering of one repository against another can be computed.
 *
 * # The three rules that are easy to break here
 *
 *  1. `not_measured` is a DIFFERENT MESSAGE, not a dimmer `observed`. It gets its own band, its own
 *     heading, and a line saying what was missing. A greyed-out row would read as "nothing to see".
 *  2. `origin: inferred` is visible WITHOUT HOVERING. It is a chip with a word in it, in the row, not
 *     a `title` attribute — which is invisible to touch, to keyboard, and to most screen readers.
 *  3. The order arrives from the platform. This file GROUPS by the rank it was given; it never sorts.
 */

// ── The tally ────────────────────────────────────────────────────────────────

/**
 * TallyStrip is what the manager in PRD §4 gets INSTEAD of a score.
 *
 * 🔴 Five counts and a sentence, deliberately not reducible. "4 read from your code · 3 not measured ·
 * 2 refused" is a shape; no arithmetic over it produces an ordering of one repository against another,
 * which is precisely the property a single number would lose.
 */
export function TallyStrip({ tally, axes }: { tally: Tally; axes: number }) {
  const cells: Array<{ label: string; count: number; tone?: string }> = [
    { label: "measured", count: tally.measured, tone: TONE_CLASS[STATE_TONE.measured] },
    { label: "read from your code", count: tally.observed, tone: TONE_CLASS[STATE_TONE.observed] },
    { label: "not measured", count: tally.not_measured, tone: TONE_CLASS[STATE_TONE.not_measured] },
    { label: "refused", count: tally.refused, tone: TONE_CLASS[STATE_TONE.refused] },
  ];
  return (
    <div className="flex flex-col gap-3">
      <ul className="flex flex-wrap items-center gap-2" aria-label={`How the ${axes} axes came out`}>
        {cells.map((c) => (
          <li key={c.label}>
            <span className={cx("chip", c.tone)}>
              <span className="chip__dot" aria-hidden="true" />
              <span className="tabular-nums font-semibold">{c.count}</span>
              <span>{c.label}</span>
            </span>
          </li>
        ))}
        {tally.inferred > 0 ? (
          <li>
            <span className={cx("chip", TONE_CLASS.info)}>
              <Bot className="size-3" aria-hidden="true" />
              <span className="tabular-nums font-semibold">{tally.inferred}</span>
              <span>of those a model wrote</span>
            </span>
          </li>
        ) : null}
      </ul>
      <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">
        There is no overall score, and that is deliberate. Every score this platform produces is
        comparative and verified — variant against variant, multi-seed, ties declared when intervals
        overlap. An absolute number for a repository would be a judgement in a metric&rsquo;s typeface,
        and no held-out set exists that would make one true. What you get instead is what we measured
        and what we could not.
      </p>
    </div>
  );
}

// ── One finding ──────────────────────────────────────────────────────────────

/** StateChip carries the state as a WORD as well as a tone — colour alone fails in four ways. */
function StateChip({ finding }: { finding: FindingView }) {
  return (
    <span className={cx("chip", TONE_CLASS[STATE_TONE[finding.state]])}>
      <span className="chip__dot" aria-hidden="true" />
      {STATE_LABEL[finding.state]}
    </span>
  );
}

/**
 * OriginChip is FR3's persistent, non-decorative marker.
 *
 * It renders only for `inferred`. A chip on every row saying "read from your code" would make the
 * marker ambient, and an ambient marker is one nobody notices — which is the same failure as no marker
 * at all, arrived at by being thorough.
 */
function OriginChip({ finding }: { finding: FindingView }) {
  if (finding.origin !== "inferred") return null;
  return (
    <span className={cx("chip", TONE_CLASS[ORIGIN_TONE.inferred ?? "info"])}>
      <Bot className="size-3" aria-hidden="true" />
      {ORIGIN_LABEL.inferred}
    </span>
  );
}

/**
 * FindingRow is one axis. Every state renders the same SHAPE — axis, claim, evidence — and a different
 * MESSAGE, which is the distinction task 5.1 is about.
 */
export function FindingRow({ finding }: { finding: FindingView }) {
  const missing = finding.missing_input ? MISSING_INPUT_LABEL[finding.missing_input] : null;
  const refusal = finding.refusal_cause ? REFUSAL_CAUSE_LABEL[finding.refusal_cause] : null;
  return (
    <Card>
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h3 className="text-sm font-semibold text-foreground">{AXIS_LABEL[finding.axis]}</h3>
          <span className="text-xs text-muted-foreground">{AXIS_QUESTION[finding.axis]}</span>
        </div>

        <p className="max-w-prose text-sm leading-relaxed text-foreground">{finding.claim}</p>

        <div className="flex flex-wrap items-center gap-2">
          <StateChip finding={finding} />
          <OriginChip finding={finding} />
          {missing ? (
            // 🔴 The missing input, rendered as a phrase and not as its identifier. `budget_exhausted`
            // reads as a leaked internal on the one axis a reader most needs to understand.
            <Chip title="What this finding needed and did not have">missing: {missing}</Chip>
          ) : null}
          {refusal ? <Chip title="Which part of ours is missing">we lack: {refusal}</Chip> : null}
          <EvidenceLink finding={finding} />
        </div>

        {finding.eval_set ? <Decisiveness report={finding.eval_set} cannotFail={finding.eval_set_cannot_fail} /> : null}
        {finding.origin === "inferred" ? <InferenceAttribution finding={finding} /> : null}
      </div>
    </Card>
  );
}

/**
 * EvidenceLink navigates INTO an existing surface (task 5.5, design D5).
 *
 * 🔴 It links; it recomputes nothing. The assessment is an index over evidence the platform already
 * holds, and a view that derived its own number here would be a second source of truth for a
 * statistical claim — this console's founding prohibition with an extra hop.
 */
function EvidenceLink({ finding }: { finding: FindingView }) {
  const label =
    finding.evidence_surface === "graph" ? "see it in the graph"
    : finding.evidence_surface === "board" ? "see the board"
    : "see the scorecard";
  return (
    // 🔴 A plain <a>, not next/link, and this is the one place on the surface that deviates from the
    // console's convention. `Link` NORMALISES the href it is given: an already-encoded segment is
    // encoded again, so `github.com%2Fnousresearch%2Fhermes-agent` ships as
    // `github.com%252Fnousresearch%252Fhermes-agent` and the link resolves to nothing. That was
    // observed, in a browser, against the first real repository this was pointed at.
    //
    // The alternative — pass the raw locator and let Link encode — does not work either: Link treats a
    // slash as a segment separator, so the same id becomes four route segments. An anchor is the only
    // shape that ships the URL this component computed. The cost is a full navigation rather than a
    // client one, which for a link ACROSS surfaces is barely a cost at all.
    //
    // ⚠️ Every other cross-subject link in this console has the first shape — `routes.graph(id)`
    // encodes and hands the result to `Link`/`SubjectLink` — so a workflow whose id contains a slash
    // is likely mis-linked elsewhere too. Not fixed here: it is a different surface's defect and
    // changing it is a change nobody asked for. Reported instead.
    <a
      href={consoleHrefFor(finding)}
      className="chip transition-colors hover:border-primary/40 hover:text-primary"
      title={`Platform route: ${finding.evidence_path}`}
    >
      <FileSearch className="size-3" aria-hidden="true" />
      {label}
      <ArrowUpRight className="size-3 opacity-60" aria-hidden="true" />
    </a>
  );
}

/**
 * consoleHrefFor turns a finding's evidence reference into the CONSOLE page that renders it.
 *
 * The finding carries the PLATFORM path because that is where the evidence lives; a reader needs the
 * page. The mapping is here rather than stored on the finding for the reason `EvidenceRef.Path` gives
 * on the other side: a stored URL is a copy of a router, and the copy is what serves a 404 to somebody
 * following a two-month-old finding.
 *
 * 🔴 It reads `evidence_locator`, NOT the path. The first version split `evidence_path` on `/` and took
 * the fourth segment — which works for every workflow id without a slash in it, and the first real
 * repository this was run against had three: `github.com/nousresearch/hermes-agent`. The link went to
 * a workflow called `github.com`. Nothing errored and nothing looked wrong; parsing a subject back out
 * of a route is a decoding of an encoding nobody agreed on, and the field removes the need for it.
 */
function consoleHrefFor(finding: FindingView): string {
  const subject = encodeURIComponent(finding.evidence_locator);
  if (finding.evidence_surface === "graph") return `/app/workflows/${subject}/graph`;
  if (finding.evidence_surface === "board") return `/app/workflows/${subject}/board`;
  return `/app/variants/${subject}/scorecard`;
}

/**
 * InferenceAttribution is design D7 on screen: WHICH model produced this claim, and the address of the
 * pin behind it.
 *
 * Without the model version, an assessment's numbers move for three reasons and a reader can attribute
 * them to two — so a provider's routine upgrade renders as their repository getting worse.
 */
function InferenceAttribution({ finding }: { finding: FindingView }) {
  return (
    <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      <span>Produced by</span>
      <Chip variant="hash" title="The exact provider model that answered">
        {finding.provider_model_version ?? "not recorded"}
      </Chip>
      <span>· pinned at</span>
      <Chip variant="hash" title="The content address of this inference — the same input returns the same answer">
        {shortAddress(finding.inference_address)}
      </Chip>
    </p>
  );
}

function shortAddress(address: string | undefined): string {
  if (!address) return "not recorded";
  const body = address.startsWith("sha256:") ? address.slice("sha256:".length) : address;
  return body.length > 12 ? body.slice(0, 12) : body;
}

// ── Decisiveness ─────────────────────────────────────────────────────────────

/**
 * Decisiveness renders BESIDE the score, never behind a link (task 5.4, design D4).
 *
 * §D4: *"A property that changes how a number should be read must be visible at the same time as the
 * number. Behind a link, it is read by the people who already suspected the number, which is the wrong
 * half."*
 */
export function Decisiveness({ report, cannotFail }: { report: EvalSetReport; cannotFail?: boolean }) {
  const cases = report.cases ?? [];
  const vacuous = report.vacuous_dimensions ?? [];
  return (
    <div className="assess__decisive">
      {cannotFail ? (
        // 🔴 The most important sentence on this surface. A generated set whose oracles cannot fail
        // scores 1.0, and a reader shown 1.0 without this reads it as a strong result.
        <Banner tone="warn" title="This eval set cannot fail">
          {/* One <p>: `Banner`'s body is a flex column and a bare text node beside any element would
              break across lines. See the partial-report banner on the page for the same note. */}
          <p>
            Every case in it carries an oracle that can never return &ldquo;no&rdquo;. The number above
            is not evidence of quality — it is what any output would have scored.
          </p>
        </Banner>
      ) : null}

      <ul className="flex flex-wrap items-center gap-2" aria-label="How decisive this eval set is">
        <li>
          <Chip variant="count" title="How many cases the number is over">
            {report.n_cases} cases
          </Chip>
        </li>
        <li>
          <Chip variant="count" title="How many seeds the interval is over">
            {report.score.n_seeds} seeds
          </Chip>
        </li>
        <li>
          {report.coverage_measured ? (
            <Chip variant="count" title="The fraction of cases whose oracle can actually return no">
              {Math.round(report.oracle_coverage * 100)}% can decide
            </Chip>
          ) : (
            // `0%` is a number somebody measured; "we do not have this" is a different fact, and
            // rendering them alike reports a catastrophe where there is silence.
            <Chip title="Oracle coverage was not measured for this set">coverage not measured</Chip>
          )}
        </li>
        {report.n_indecisive > 0 ? (
          <li>
            <Chip tone="warn" title="Cases that look measured and decide nothing">
              {report.n_indecisive} cannot fail
            </Chip>
          </li>
        ) : null}
        {report.score.n_seeds === 1 ? (
          <li>
            <Chip tone="warn" title="An interval from one seed is a range around a single observation">
              single seed
            </Chip>
          </li>
        ) : null}
      </ul>

      {vacuous.length > 0 ? (
        <p className="text-xs text-muted-foreground">
          No case exercises {vacuous.join(", ")} coverage, so this set is silent about{" "}
          {vacuous.length === 1 ? "that axis" : "those axes"}. Nothing to measure is not everything
          covered.
        </p>
      ) : null}

      {cases.length > 0 ? (
        // 🔴 The cases themselves, not a count (FR13). P30's named gap: a reader sees `n=5 seeds ·
        // 8 cases` and cannot answer the only question that matters — 8 cases of what?
        <details className="text-xs">
          <summary className="cursor-pointer text-muted-foreground">
            The {cases.length} cases, and what decides each
          </summary>
          <ul className="mt-2 flex flex-col gap-1">
            {cases.map((c) => (
              <li key={c.case_id} className="flex flex-wrap items-baseline gap-2">
                <span className="mono text-foreground/80">{c.case_id}</span>
                <span className="text-muted-foreground">{c.oracle.kind}</span>
                {c.oracle.decisive ? (
                  <Chip title="This oracle can return no">can fail</Chip>
                ) : (
                  <Chip tone="warn" title={c.oracle.reason ?? undefined}>
                    cannot fail
                  </Chip>
                )}
                {!c.oracle.decisive && c.oracle.reason ? (
                  <span className="text-muted-foreground">— {c.oracle.reason}</span>
                ) : null}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </div>
  );
}

// ── The report ───────────────────────────────────────────────────────────────

/**
 * Findings renders the nine, in the bands the platform's rank already put them in.
 *
 * 🔴 Bands rather than one flat list, because the bands ARE the message: a reader who scrolls to the
 * bottom has to arrive somewhere that says "and here is what we could not tell you" rather than at a
 * row that looks like the others with less in it.
 */
export function Findings({ view }: { view: AssessmentView }) {
  const bands = groupByRank(view.findings ?? []);
  return (
    <div className="flex flex-col gap-6">
      {bands.map((band) => (
        <Section key={band.key} title={band.heading} aside={<span className="tabular-nums text-xs">{band.findings.length}</span>}>
          <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">{band.note}</p>
          <div className="mt-3 flex flex-col gap-3">
            {band.findings.map((f) => (
              <FindingRow key={f.axis} finding={f} />
            ))}
          </div>
        </Section>
      ))}
    </div>
  );
}

/** StateLegend explains the four states, so the words on the chips are not a vocabulary to guess at. */
export function StateLegend() {
  return (
    <dl className="grid gap-3 sm:grid-cols-2">
      {(["measured", "observed", "not_measured", "refused"] as const).map((state) => (
        <div key={state} className="flex flex-col gap-1">
          <dt>
            <span className={cx("chip", TONE_CLASS[STATE_TONE[state]])}>
              <span className="chip__dot" aria-hidden="true" />
              {STATE_LABEL[state]}
            </span>
          </dt>
          <dd className="max-w-prose text-xs leading-relaxed text-muted-foreground">{STATE_MEANING[state]}</dd>
        </div>
      ))}
    </dl>
  );
}

// ── The re-inference diff ────────────────────────────────────────────────────

const CAUSE_HEADING: Record<string, string> = {
  source: "Your repository changed",
  agent_config: "We changed how we analyse",
  provider_model: "The provider changed the model",
  unattributable: "Changed with nothing moved — our defect",
};

/**
 * DiffList renders a re-inference as a diff, and every row names WHICH INPUT MOVED.
 *
 * 🔴 The attribution is the point, not the change. A changed report raises exactly one question —
 * whose fault is it — and three of the four answers are not about the reader at all. Without this,
 * a provider's routine upgrade renders as their repository getting worse.
 */
export function DiffList({ diff }: { diff: readonly AxisDiff[] }) {
  if (diff.length === 0) {
    return (
      <Banner tone="info" title="Nothing changed">
        <p>
          We assessed again and every one of the nine findings is identical. Same revision, same
          configuration, same answers — which is what reproducibility looks like.
        </p>
      </Banner>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      {diff.map((d) => (
        <Card key={d.axis}>
          <div className="flex flex-col gap-2">
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
              <h3 className="text-sm font-semibold text-foreground">{AXIS_LABEL[d.axis]}</h3>
              <Chip tone={d.cause === "unattributable" ? "bad" : undefined}>{CAUSE_HEADING[d.cause] ?? d.cause}</Chip>
            </div>
            <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">{d.why}</p>
            <div className="flex flex-col gap-1 text-sm">
              <p className="text-muted-foreground line-through decoration-muted-foreground/40">{d.before_claim}</p>
              <p className="text-foreground">{d.after_claim}</p>
            </div>
          </div>
        </Card>
      ))}
    </div>
  );
}
