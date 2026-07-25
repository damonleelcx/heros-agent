import type { LinkCoverageView } from "@/lib/types.generated";

/**
 * linkCoverage.tsx renders how much of a customer's activity a SUM figure reflects (P11 FR17, tasks
 * 5.2/5.3).
 *
 * # Why this is not a footnote
 *
 * SUM is derived from LINKED runs only — a run executed locally and never linked contributes nothing,
 * and the platform never estimates the rest. So a spend figure can reflect a fraction of real activity,
 * and a fraction shown *as if it were the whole* is exactly what a billing dispute is made of. This
 * component sits beside the figure and states the fraction, every time.
 *
 * # The distinction that matters (5.3): complete vs unknown
 *
 * A figure at 100% and a figure whose denominator is unknown look identical as a number and mean
 * opposite things. This renders THREE states, never collapsing the last two:
 *
 *   complete   every reported run is linked — the figure reflects all known activity.
 *   partial    N of M runs linked — the figure reflects a measured fraction.
 *   unknown    no run count reported — completeness cannot be assessed. NOT zero and NOT full.
 *
 * Colour comes from the token layer (--ok for complete, --info for partial, --muted-foreground for
 * unknown) via `var(...)` references, never literals — the hazard palette is deliberately NOT used, as
 * partial coverage is informational, not a hazard.
 */
export function LinkCoverage({ coverage }: { coverage: LinkCoverageView | null | undefined }) {
  // Absent coverage is itself the "unknown" state — the server did not (or could not) report it.
  const cov: LinkCoverageView = coverage ?? { runs_linked: 0, runs_reported: 0, known: false, complete: false };
  const state = coverageState(cov);
  const pct = cov.runs_reported > 0 ? Math.min(100, Math.round((cov.runs_linked / cov.runs_reported) * 100)) : 0;
  const copy = describe(cov, state, pct);

  return (
    <div
      className="flex flex-col gap-2 rounded-xl border border-border bg-graph-canvas px-4 py-3"
      role="group"
      aria-label={`Link coverage: ${copy}`}
    >
      <div className="flex items-center justify-between gap-3">
        <span className="stat__label">Coverage of this figure</span>
        <span
          className="rounded-full px-2 py-0.5 text-xs font-medium"
          style={{ color: BADGE_COLOR[state], backgroundColor: BADGE_BG[state] }}
        >
          {state}
        </span>
      </div>

      {state === "unknown" ? (
        <div
          className="h-2 w-full rounded-full border border-dashed border-border"
          aria-hidden="true"
          title="coverage unknown"
        />
      ) : (
        <div
          className="h-2 w-full overflow-hidden rounded-full"
          style={{ backgroundColor: "var(--muted)" }}
          role="progressbar"
          aria-valuemin={0}
          aria-valuemax={cov.runs_reported}
          aria-valuenow={cov.runs_linked}
          aria-valuetext={`${cov.runs_linked} of ${cov.runs_reported} runs linked`}
        >
          <span
            className="block h-full rounded-full"
            style={{ width: `${state === "complete" ? 100 : pct}%`, backgroundColor: FILL[state] }}
          />
        </div>
      )}

      <p className="stat__note max-w-prose">{copy}</p>
    </div>
  );
}

type CovState = "complete" | "partial" | "unknown";

function coverageState(c: LinkCoverageView): CovState {
  if (!c.known) return "unknown";
  if (c.complete) return "complete";
  return "partial";
}

// Colours are token references, not literals: --ok (complete), --info (partial), --muted-foreground
// (unknown). No hazard palette — partial coverage is informational.
const FILL: Record<CovState, string> = {
  complete: "var(--ok)",
  partial: "var(--info)",
  unknown: "var(--muted-foreground)",
};
const BADGE_COLOR: Record<CovState, string> = {
  complete: "var(--ok)",
  partial: "var(--info)",
  unknown: "var(--muted-foreground)",
};
const BADGE_BG: Record<CovState, string> = {
  complete: "color-mix(in srgb, var(--ok) 14%, transparent)",
  partial: "color-mix(in srgb, var(--info) 14%, transparent)",
  unknown: "color-mix(in srgb, var(--muted-foreground) 12%, transparent)",
};

function describe(c: LinkCoverageView, state: CovState, pct: number): string {
  switch (state) {
    case "complete":
      return `Every run this account reported (${c.runs_reported}) has been linked. This figure reflects all known activity.`;
    case "partial":
      return `This figure reflects ${c.runs_linked} of ${c.runs_reported} reported runs (${pct}%). Unlinked runs contribute nothing and are never estimated.`;
    case "unknown":
      return `Coverage is unknown: this figure reflects only linked runs, and no run count has been reported to establish how much activity that is. Link runs from the CLI to measure it.`;
  }
}
