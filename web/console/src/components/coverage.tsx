import type { ReactNode } from "react";

import type { AxisCoverageView, CoverageCell } from "@/lib/types.generated";
import { Chip, Row } from "@/components/primitives";

/**
 * coverage.tsx renders WHAT THE PLATFORM CAN APPLY, and — where it cannot — WHOSE MOVE IT IS.
 *
 * # Why three states and not two
 *
 * The obvious design is a checkmark and a cross: supported, unsupported. It is also the design that
 * makes this surface useless, because "unsupported" is three different situations with three different
 * next steps:
 *
 *   your move       — this call site's own source cannot carry the change (unpacked arguments, a tool
 *                     list built at run time). Nothing we ship will change that. The reader edits code.
 *   nobody's move   — the value does not exist in source in ANY language (a summary a model writes at
 *                     run time). There is no "when". The reader stops waiting.
 *   our move        — the artifact has not landed. There IS a when, and the missing piece is named.
 *
 * Collapsing them sends the first reader to wait for us and the second to file a bug. So the CAUSE
 * CLASS — a stable identifier from the engine, never the sentence — selects the treatment, and the
 * treatments are visually distinct from each other at a glance.
 *
 * # Why the hazard palette is not used
 *
 * `--warn` and `--danger` are reserved for hazard (project.md): a destructive control, an armed halt.
 * A refusal is not a hazard — it is an answer — and spending the hazard colour here would make the
 * colour stop meaning anything where it matters. The three states are distinguished by BORDER STYLE and
 * weight instead: solid for applied, a left rule for a source fact, a dashed rule for a platform gap.
 * That also keeps them distinguishable without colour, which is the accessibility floor.
 *
 * # Why nothing here is derived
 *
 * Status, cause, and the missing artifact all arrive from `transform.AxisCoverage()` through the BFF and
 * are rendered as received. A console that decided for itself whether a cell "counts as supported" would
 * be the second coverage source this whole contract exists to prevent.
 */

/** The four visual states. `applies` plus the three refusal classes — no fifth, and no default. */
export type CoverageState =
  | "applies"
  | "not-expressible-at-a-call-site"
  | "call-site-cannot-carry-it"
  | "no-materializer-for-this-language";

export function stateOf(cell: CoverageCell): CoverageState {
  if (cell.status === "materializes") return "applies";
  return (cell.cause as CoverageState) ?? "call-site-cannot-carry-it";
}

const STATE_STYLE: Record<CoverageState, { box: string; dot: string; short: string }> = {
  applies: {
    box: "border-primary/45 bg-primary/[0.07] text-foreground",
    dot: "bg-primary",
    short: "applies",
  },
  "call-site-cannot-carry-it": {
    // A left rule: something to act on, at the reader's end.
    box: "border-border border-l-2 border-l-foreground/45 bg-surface text-foreground/80",
    dot: "bg-foreground/45",
    short: "your call site",
  },
  "not-expressible-at-a-call-site": {
    // Flat and quiet: there is nothing to do and no date to wait for.
    box: "border-border/50 bg-background text-muted-foreground",
    dot: "bg-muted-foreground/50",
    short: "not in source",
  },
  "no-materializer-for-this-language": {
    // Dashed: unfinished, and ours. The only state that names an artifact.
    box: "border-dashed border-primary/50 bg-background text-foreground/75",
    dot: "bg-primary/50",
    short: "not yet — ours",
  },
};

/** A single cause's swatch + sentence, for the legend. */
export function CoverageLegend({ view }: { view: AxisCoverageView }) {
  const owners = new Map((view.causes ?? []).map((c) => [c.id, c]));
  const order: CoverageState[] = [
    "applies",
    "call-site-cannot-carry-it",
    "not-expressible-at-a-call-site",
    "no-materializer-for-this-language",
  ];
  return (
    <ul className="grid gap-2 sm:grid-cols-2">
      {order.map((state) => {
        const style = STATE_STYLE[state];
        const cause = owners.get(state);
        return (
          <li key={state} className={`flex gap-2.5 rounded-md border px-3 py-2 text-xs ${style.box}`}>
            <span className={`mt-1 h-2 w-2 shrink-0 rounded-full ${style.dot}`} aria-hidden />
            <span className="min-w-0">
              <span className="mono block text-[11px] uppercase tracking-wide">{style.short}</span>
              <span className="mt-0.5 block leading-snug">
                {state === "applies"
                  ? "The platform writes this change into your source."
                  : cause?.label}
              </span>
              {cause ? (
                <span className="mt-1 block text-[11px] text-muted-foreground">
                  Whose move: <strong className="font-medium">{cause.owner}</strong>
                </span>
              ) : null}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

/**
 * CoverageMatrix is the surface's centre: one row per language, one column per axis, every cell filled.
 *
 * 🔴 Every registered language gets a row whether or not anything applies to it. An absent row is the
 * one thing this design may not do — it renders as "not applicable", which is a claim about the reader's
 * code rather than about the platform.
 *
 * A cell aggregates the forms within it (a language has many registry rows, many providers, many
 * policies), and the aggregate is honest rather than optimistic: it shows the count that applies out of
 * the total, so "3/11" is legible as partial instead of rounding to a tick.
 */
export function CoverageMatrix({
  view,
  onSelect,
}: {
  view: AxisCoverageView;
  onSelect?: (axis: string, language: string) => void;
}) {
  const axes = view.axes ?? [];
  const languages = view.languages ?? [];
  const cells = view.cells ?? [];

  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[46rem] border-separate border-spacing-1 text-left text-xs">
        <caption className="sr-only">
          What this build can apply, by axis and language. Every language appears on every axis.
        </caption>
        <thead>
          <tr>
            <th scope="col" className="w-28 px-2 pb-1 font-medium text-muted-foreground">
              Language
            </th>
            {axes.map((axis) => (
              <th key={axis} scope="col" className="px-2 pb-1 font-medium text-muted-foreground">
                {axis}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {languages.map((language) => (
            <tr key={language}>
              <th scope="row" className="mono px-2 py-1 text-left font-normal text-foreground">
                {language}
              </th>
              {axes.map((axis) => {
                const group = cells.filter((c) => c.axis === axis && c.language === language);
                const applied = group.filter((c) => c.status === "materializes").length;
                const state = dominantState(group);
                const style = STATE_STYLE[state];
                const label = `${axis} in ${language}: ${applied} of ${group.length} form(s) apply`;
                return (
                  <td key={axis} className="p-0 align-top">
                    <button
                      type="button"
                      onClick={onSelect ? () => onSelect(axis, language) : undefined}
                      aria-label={label}
                      title={label}
                      className={`flex w-full flex-col gap-0.5 rounded-md border px-2.5 py-2 text-left transition-colors ${style.box} ${
                        onSelect ? "hover:border-primary/70" : "cursor-default"
                      }`}
                    >
                      <span className="mono text-[10px] uppercase tracking-wide">{style.short}</span>
                      <span className="text-[11px] text-muted-foreground">
                        {applied}/{group.length} form{group.length === 1 ? "" : "s"}
                      </span>
                    </button>
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/**
 * dominantState picks the cell's headline.
 *
 * 🔴 Partial coverage reads as the REFUSAL, not as the success. A cell where two providers apply and
 * five do not is a cell most readers will hit a refusal in, and rounding it up to "applies" is how a
 * coverage table becomes a promise. The refusal chosen is the one that owns the most forms, so the
 * headline names the move most readers will have to make.
 */
function dominantState(group: CoverageCell[]): CoverageState {
  if (group.length === 0) return "no-materializer-for-this-language";
  const refused = group.filter((c) => c.status !== "materializes");
  if (refused.length === 0) return "applies";
  const counts = new Map<CoverageState, number>();
  for (const c of refused) {
    const s = stateOf(c);
    counts.set(s, (counts.get(s) ?? 0) + 1);
  }
  let best: CoverageState = "call-site-cannot-carry-it";
  let bestN = -1;
  for (const [s, n] of counts) {
    if (n > bestN) {
      best = s;
      bestN = n;
    }
  }
  return best;
}

/** CoverageDetail lists the individual forms behind one (axis, language) cell. */
export function CoverageDetail({ cells }: { cells: CoverageCell[] }) {
  if (cells.length === 0) {
    return <p className="text-sm text-muted-foreground">No forms recorded.</p>;
  }
  return (
    <ul className="flex flex-col gap-1.5">
      {cells.map((c) => {
        const style = STATE_STYLE[stateOf(c)];
        return (
          <li key={`${c.axis}/${c.language}/${c.form}`} className={`rounded-md border px-3 py-2 ${style.box}`}>
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <span className="mono text-xs">{c.form}</span>
              <span className="mono text-[10px] uppercase tracking-wide">{style.short}</span>
            </div>
            {c.note ? <p className="mt-1 text-xs leading-snug">{c.note}</p> : null}
            {c.missing_artifact ? (
              <p className="mt-1 text-xs leading-snug">
                <span className="text-muted-foreground">Missing: </span>
                {c.missing_artifact}
              </p>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

/**
 * CoverageBoundary states the boundary for ONE node BEFORE a picker is shown (P13 FR55, P14 FR30,
 * P15 FR36, P16 FR29).
 *
 * # Why this component exists at all
 *
 * The failure it prevents is an empty selector. A list with nothing in it reads as "this node has no
 * options" — a fact about the catalog — when the truth is "this language cannot carry one yet" — a fact
 * about the platform. The user then goes looking for the skills they were promised.
 *
 * So the boundary is STATED, with the language and the form named, and the picker is not rendered at
 * all. Two sentences, never one: a platform gap has a "when" and a source fact does not, and giving
 * them the same wording is how a fixable call-site problem becomes a support ticket about language
 * support.
 */
export function CoverageBoundary({
  axis,
  language,
  cells,
  children,
}: {
  axis: string;
  language: string;
  cells: CoverageCell[];
  children?: ReactNode;
}) {
  const group = cells.filter((c) => c.axis === axis && c.language === language);
  const applies = group.filter((c) => c.status === "materializes");

  if (applies.length > 0) {
    return (
      <div className="flex flex-col gap-3">
        <Row>
          <Chip tone="ok">{applies.length} of {group.length} form(s) apply here</Chip>
          <Chip>{language}</Chip>
        </Row>
        {children}
      </div>
    );
  }

  const state = dominantState(group);
  const style = STATE_STYLE[state];
  const platformGap = state === "no-materializer-for-this-language";
  const missing = group.find((c) => c.missing_artifact)?.missing_artifact;

  return (
    <div className={`rounded-md border px-4 py-3 ${style.box}`} role="note">
      <p className="mono mb-1 text-[10px] uppercase tracking-wide">{style.short}</p>
      <p className="text-sm leading-snug">
        {platformGap ? (
          <>
            This node is <strong className="font-medium">{language}</strong>, and the platform does not
            apply <strong className="font-medium">{axis}</strong> here <em>yet</em>. Nothing you change
            in your code will unlock it — the missing piece is ours.
          </>
        ) : state === "not-expressible-at-a-call-site" ? (
          <>
            <strong className="font-medium">{axis}</strong> is not something that can be written into
            source in any language, so there is no version of this the platform will apply.{" "}
            <strong className="font-medium">This is not a wait</strong> — nothing we ship changes it.
          </>
        ) : (
          <>
            This node&rsquo;s own source cannot carry a <strong className="font-medium">{axis}</strong>{" "}
            change. A materializer would refuse it for the same reason, so this is a change to make in
            your code rather than something to wait for.
          </>
        )}
      </p>
      {missing ? (
        <p className="mt-1.5 text-xs leading-snug">
          <span className="text-muted-foreground">Missing: </span>
          {missing}
        </p>
      ) : null}
      {group[0]?.note ? (
        <p className="mt-1.5 text-xs leading-snug text-muted-foreground">{group[0].note}</p>
      ) : null}
      <p className="mt-2 text-[11px] text-muted-foreground">
        This answer is identical on every plan. No tier, role or setting changes it.
      </p>
    </div>
  );
}
