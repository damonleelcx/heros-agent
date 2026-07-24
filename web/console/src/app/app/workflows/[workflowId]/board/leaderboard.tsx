"use client";

import { useMemo, useRef, useState } from "react";
import Link from "next/link";
import { ChevronDown, ChevronRight } from "lucide-react";
import type { Row, ComponentView } from "@/lib/types.generated";
import { Value, Chip, Status, CIBar } from "@/components/primitives";
import { score, integer, percent, plural, NULL_VALUE } from "@/lib/format";
import { routes } from "@/lib/routes";
import { cx } from "@/lib/cx";

/**
 * The leaderboard.
 *
 * # Why it is still a `<table>` and not the design system's grid of `<div>`s
 *
 * The design bundle draws this as `grid-cols-[40px_1fr_140px_…]` divs. That renders identically and
 * announces as nothing: a screen reader reading a grid of divs gets a flat list of forty-five values
 * with no column association, so "0.831" is never heard as "score of claude-3-5-sonnet". The table
 * element carries that association for free, and every visual property of the design — the column
 * widths, the hover, the expander — is reachable from it.
 *
 * # Virtualization above sixty rows
 *
 * With spacer rows rather than absolute positioning, so the scrollbar keeps meaning what it normally
 * means. Below the threshold nothing is virtualized, because the complexity is only worth it where the
 * document would otherwise hold thousands of cells.
 */
const VIRTUALIZE_ABOVE = 60;
const ROW_HEIGHT = 64;
const WINDOW = 40;

export function Leaderboard({ rows, workflowId }: { rows: Row[]; workflowId: string }) {
  const [expanded, setExpanded] = useState<string | null>(null);
  const [start, setStart] = useState(0);
  const bodyRef = useRef<HTMLTableSectionElement>(null);
  const virtualized = rows.length > VIRTUALIZE_ABOVE;

  const visible = useMemo(
    () => (virtualized ? rows.slice(start, start + WINDOW) : rows),
    [rows, start, virtualized],
  );

  const [lo, hi] = useMemo(() => {
    const lows = rows.map((r) => r.ci_low);
    const highs = rows.map((r) => r.ci_high);
    return [Math.min(...lows), Math.max(...highs)];
  }, [rows]);

  function onKeyDown(event: React.KeyboardEvent<HTMLTableSectionElement>) {
    const target = event.target as HTMLElement;
    const row = target.closest("tr[data-row]") as HTMLTableRowElement | null;
    if (!row) return;
    const all = Array.from(bodyRef.current?.querySelectorAll<HTMLTableRowElement>("tr[data-row]") ?? []);
    const index = all.indexOf(row);
    if (index < 0) return;
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      const id = row.dataset.row ?? null;
      setExpanded((was) => (was === id ? null : id));
      return;
    }
    if (event.key === "ArrowDown") {
      event.preventDefault();
      all[(index + 1) % all.length]?.focus();
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      all[(index - 1 + all.length) % all.length]?.focus();
    }
  }

  return (
    <>
      <div
        className="overflow-hidden rounded-xl border border-border"
        onScroll={(event) => {
          if (!virtualized) return;
          const top = (event.target as HTMLElement).scrollTop;
          const next = Math.max(0, Math.floor(top / ROW_HEIGHT) - 5);
          setStart(Math.min(next, Math.max(0, rows.length - WINDOW)));
        }}
      >
        <div className={cx("overflow-x-auto", virtualized && "max-h-[70vh] overflow-y-auto")}>
          {/* A minimum width, so a narrow viewport scrolls the table instead of squeezing the variant
              column until every label wraps to four lines. The same rule as the graph: it scrolls, it
              does not shrink. */}
          <table className="data-table min-w-[54rem]">
            <caption>
              Variants ranked under this profile, each with the interval its score was measured to
            </caption>
            <thead>
              <tr>
                <th scope="col" className="num">
                  #
                </th>
                <th scope="col">Variant</th>
                <th scope="col">Score with interval</th>
                <th scope="col">Gate</th>
                <th scope="col">State</th>
                <th scope="col">
                  <span className="visually-hidden">Breakdown</span>
                </th>
              </tr>
            </thead>
            <tbody ref={bodyRef} onKeyDown={onKeyDown}>
              {/* Spacer rows keep the scrollbar honest about how much there is, so the thumb's size and
                  position mean what they normally mean. */}
              {virtualized && start > 0 ? <tr style={{ height: start * ROW_HEIGHT }} aria-hidden="true" /> : null}
              {visible.map((row) => (
                <RowPair
                  key={row.variant_id}
                  row={row}
                  lo={lo}
                  hi={hi}
                  workflowId={workflowId}
                  expanded={expanded === row.variant_id}
                  onToggle={() => setExpanded((was) => (was === row.variant_id ? null : row.variant_id))}
                />
              ))}
              {virtualized && start + WINDOW < rows.length ? (
                <tr style={{ height: (rows.length - start - WINDOW) * ROW_HEIGHT }} aria-hidden="true" />
              ) : null}
            </tbody>
          </table>
        </div>
      </div>
      {virtualized ? (
        <p className="caption">
          {integer(rows.length)} variants · virtualized — only the rows near your scroll position are in
          the document
        </p>
      ) : null}
    </>
  );
}

function RowPair({
  row,
  lo,
  hi,
  workflowId,
  expanded,
  onToggle,
}: {
  row: Row;
  lo: number;
  hi: number;
  workflowId: string;
  expanded: boolean;
  onToggle: () => void;
}) {
  const flags = row.flags ?? [];
  const span = hi - lo || 1;
  return (
    <>
      <tr
        data-row={row.variant_id}
        tabIndex={0}
        aria-expanded={expanded}
        className={cx("lb-row cursor-pointer", expanded && "lb-row--expanded")}
        onClick={onToggle}
      >
        {/* 🔴 P4-9. A tied rank renders DE-EMPHASISED. A tie does not look like a win, because the
            server explicitly declined to say it was one. */}
        <td className="num">
          <Value flags={flags} showQualifiers={false}>
            <span className="mono">{integer(row.rank)}</span>
          </Value>
        </td>
        <td>
          <Link
            className="text-sm text-foreground underline-offset-2 hover:text-primary hover:underline"
            href={routes.scorecard(row.variant_id)}
            onClick={(e) => e.stopPropagation()}
          >
            {row.label}
          </Link>
          <p className="mono caption" title={row.config_hash}>
            {row.config_hash_short}
          </p>
        </td>
        <td className="min-w-56">
          <Value flags={flags}>
            <span className="mono tabular-nums">
              {score(row.score)}{" "}
              <span className="caption">
                ± [{score(row.ci_low)}, {score(row.ci_high)}]
              </span>
            </span>
          </Value>
          <span className="mt-2 block">
            <CIBar
              left={((row.ci_low - lo) / span) * 100}
              width={Math.max(((row.ci_high - row.ci_low) / span) * 100, 1.5)}
              mean={((row.score - lo) / span) * 100}
              label={`score ${score(row.score)}, interval ${score(row.ci_low)} to ${score(row.ci_high)}`}
            />
          </span>
          <p className="caption mt-1">
            n={integer(row.n_seeds)} {plural(row.n_seeds, "seed", "seeds")} · {integer(row.n_cases)}{" "}
            {plural(row.n_cases, "case", "cases")}
          </p>
        </td>
        <td>
          {row.gate_pass ? (
            <Chip tone="ok">pass</Chip>
          ) : (
            <Chip tone="bad">fails {(row.failed_gates ?? []).join(", ") || "a gate"}</Chip>
          )}
        </td>
        <td>
          {flags.length === 0 ? (
            <span className="caption">{NULL_VALUE}</span>
          ) : (
            <span className="flex flex-wrap gap-1">
              {flags.map((flag) => (
                <Status key={flag} value={flag} />
              ))}
            </span>
          )}
        </td>
        <td className="text-center">
          {expanded ? (
            <ChevronDown className="inline size-3.5 text-muted-foreground" aria-hidden="true" />
          ) : (
            <ChevronRight className="inline size-3.5 text-muted-foreground/50" aria-hidden="true" />
          )}
        </td>
      </tr>
      {expanded ? (
        <tr className="bg-muted/15">
          <td colSpan={6}>
            <Breakdown row={row} workflowId={workflowId} />
          </td>
        </tr>
      ) : null}
    </>
  );
}

function Breakdown({ row, workflowId }: { row: Row; workflowId: string }) {
  const components = row.components ?? [];
  const penalties = row.penalties ?? [];
  const gateReasons = row.gate_reasons ?? [];
  const tiedWith = row.tied_with ?? [];
  return (
    <div className="flex flex-col gap-4 py-2">
      <p className="font-mono text-[10px] uppercase tracking-widest text-muted-foreground">
        How this score was composed
      </p>
      <div className="overflow-hidden rounded-lg border border-border bg-background">
        <div className="overflow-x-auto">
          {/* A minimum width, so a narrow viewport scrolls the table instead of squeezing the variant
              column until every label wraps to four lines. The same rule as the graph: it scrolls, it
              does not shrink. */}
          <table className="data-table min-w-[54rem]">
            <caption>How this score was composed</caption>
            <thead>
              <tr>
                <th scope="col">Metric</th>
                <th scope="col">Role</th>
                <th scope="col" className="num">
                  Raw
                </th>
                <th scope="col" className="num">
                  Normalized
                </th>
                <th scope="col" className="num">
                  Contribution
                </th>
              </tr>
            </thead>
            <tbody>
              {components.map((component) => (
                <ComponentRow key={component.metric} component={component} />
              ))}
              {penalties.map((penalty) => (
                <tr key={penalty.term}>
                  <td>{penalty.term}</td>
                  <td>penalty</td>
                  <td className="num">{NULL_VALUE}</td>
                  <td className="num">{NULL_VALUE}</td>
                  <td className="num">{score(-penalty.amount)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      <p className="caption mono" title={row.config_hash}>
        {row.config_hash} · {row.method}
      </p>

      {tiedWith.length > 0 ? (
        <p className="hint">
          Statistically indistinguishable from {tiedWith.join(", ")}. Overlapping confidence intervals
          are not an ordering.
        </p>
      ) : null}

      {gateReasons.length > 0 ? (
        <ul className="diagnostics">
          {gateReasons.map((reason, index) => (
            <li key={index}>{reason}</li>
          ))}
        </ul>
      ) : null}

      <p className="caption flex flex-wrap gap-3">
        <Link className="text-primary underline underline-offset-2" href={routes.scorecard(row.variant_id)}>
          Why did this variant score what it scored?
        </Link>
        <Link className="text-primary underline underline-offset-2" href={routes.board(workflowId)}>
          Back to the board
        </Link>
      </p>
    </div>
  );
}

function ComponentRow({ component }: { component: ComponentView }) {
  const judge = component.judge;
  const flags = judge && !judge.calibrated ? ["uncalibrated"] : [];
  return (
    <>
      <tr>
        <td>{component.metric}</td>
        <td>{component.role}</td>
        <td className="num">
          {/* raw_ci_low / raw_ci_high / unit are surfaced per the surface-or-drop decision. The
              composite shows its interval while components showed a bare point estimate, which implies
              more precision than exists. */}
          <Value flags={flags} showQualifiers={false}>
            <span className="mono">
              {score(component.raw)}
              {component.unit ? ` ${component.unit}` : ""}
              <span className="caption">
                {" "}
                [{score(component.raw_ci_low)}, {score(component.raw_ci_high)}]
              </span>
            </span>
          </Value>
        </td>
        <td className="num mono">{score(component.normalized)}</td>
        <td className="num mono">{score(component.contribution)}</td>
      </tr>
      {judge ? (
        <tr>
          <td colSpan={5}>
            <Value flags={flags}>
              <span className="caption">
                judge κ {score(judge.agreement)} · {percent(judge.percent_agreement)} raw agreement · n=
                {integer(judge.n_human)} human {plural(judge.n_human, "label", "labels")} · floor{" "}
                {score(judge.floor)}
              </span>
            </Value>
          </td>
        </tr>
      ) : null}
    </>
  );
}
