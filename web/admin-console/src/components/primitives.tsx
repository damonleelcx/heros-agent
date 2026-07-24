import type { ReactNode } from "react";
import { count, quantity } from "@/lib/format";

/**
 * primitives.tsx is the console's CLOSED primitive set (FR29).
 *
 * Every view composes from the nine shapes here — page frame, section, data table, stat, timeline,
 * drawer, confirm sheet (actionForm.tsx), receipt (actionForm.tsx), state block (states.tsx) — and
 * from nothing else. A view that needs a shape not in this file does not invent one inline: the
 * change is to `web/design-system/README.md` §7 first, then here.
 *
 * # Why "closed" is the load-bearing word
 *
 * An open set is what produces the surface this phase set out to replace. Each page invents the panel
 * it needs, twelve variants of "a box with a heading" accumulate, the spacing between them drifts, and
 * nobody can restyle the console without visiting every file. Worse on this surface: a bespoke panel
 * is a panel whose hazard treatment, focus behaviour and empty state were decided by whoever was in a
 * hurry. Closing the set makes those decisions once.
 *
 * # Numbers (FR30)
 *
 * `Num` and `DataTable`'s numeric columns are the only way a figure reaches the screen. They render
 * tabular figures, right-align the column, and put the unit in the HEADER rather than on every cell —
 * so a column can be scanned for magnitude without reading a single value. The console never computes
 * a number; it formats one the platform sent.
 */

/* ══════════════════════════════════════════════════════════════════════════
   PageFrame
   ══════════════════════════════════════════════════════════════════════════ */

/**
 * PageFrame carries the page's editorial hierarchy: an eyebrow naming the surface, the display line
 * naming the subject, and a lede capped at the prose measure.
 */
export function PageFrame({
  eyebrow,
  title,
  lede,
  children,
}: {
  eyebrow: string;
  title: string;
  lede?: ReactNode;
  children: ReactNode;
}) {
  return (
    <>
      <p className="page__eyebrow">{eyebrow}</p>
      <h1>{title}</h1>
      {lede ? <p className="lede">{lede}</p> : null}
      {children}
    </>
  );
}

/* ══════════════════════════════════════════════════════════════════════════
   Section
   ══════════════════════════════════════════════════════════════════════════ */

/**
 * Section is a titled panel of related content. `aside` is for PROVENANCE — a count, a source, an
 * as-of time — never for an action, because an action in a header is an action without a confirmation
 * next to it.
 */
export function Section({
  title,
  aside,
  flush,
  id,
  children,
}: {
  title: string;
  aside?: ReactNode;
  flush?: boolean;
  id?: string;
  children: ReactNode;
}) {
  return (
    <section className="section" id={id}>
      <div className="section__header">
        <h2 className="section__title">{title}</h2>
        {aside ? <div className="section__aside">{aside}</div> : null}
      </div>
      <div className={flush ? "section__body section__body--flush" : "section__body"}>{children}</div>
    </section>
  );
}

/* ══════════════════════════════════════════════════════════════════════════
   Num — the only way a figure reaches the screen (FR30)
   ══════════════════════════════════════════════════════════════════════════ */

/**
 * Num formats a platform-supplied figure in tabular numerals.
 *
 * `kind` selects the formatter, both pinned to en-US in the single locale swap point: `count` for
 * whole numbers, `quantity` for measured values. There is no third option, because a third precision
 * in the same view is how one quantity comes to look like two.
 */
export function Num({ value, kind = "count" }: { value: number; kind?: "count" | "quantity" }) {
  return <span className="num">{kind === "count" ? count(value) : quantity(value)}</span>;
}

/* ══════════════════════════════════════════════════════════════════════════
   DataTable
   ══════════════════════════════════════════════════════════════════════════ */

export type Column = {
  /** The column heading. Sentence case; the stylesheet does the typographic treatment. */
  label: string;
  /** numeric right-aligns the column and renders its cells in tabular figures. */
  numeric?: boolean;
  /**
   * unit is stated ONCE, here in the header — never repeated on every cell (FR30). A column of
   * "1,204 tokens / 33 tokens / 7 tokens" makes the reader parse the unit 3n times to compare n
   * numbers.
   */
  unit?: string;
};

/**
 * DataTable renders scoped column headers, a caption, and the numeric alignment its columns declare.
 *
 * The caption is required rather than optional: a table whose meaning lives only in the page around it
 * is unreadable to a screen reader that lands on it directly, and unreadable to an operator who
 * scrolled past the heading.
 */
export function DataTable({
  caption,
  columns,
  children,
}: {
  caption: string;
  columns: Column[];
  children: ReactNode;
}) {
  return (
    <div className="table-scroll">
      <table>
        <caption>{caption}</caption>
        <thead>
          <tr>
            {columns.map((c) => (
              <th key={c.label} scope="col" className={c.numeric ? "num" : undefined}>
                {c.label}
                {c.unit ? <span className="stat__unit"> ({c.unit})</span> : null}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

/* ══════════════════════════════════════════════════════════════════════════
   Stat
   ══════════════════════════════════════════════════════════════════════════ */

/**
 * Stat is one labelled figure with its unit and, when it is live, the time it is current as of.
 *
 * `asOf` is not decoration. A live figure with no as-of time cannot be told apart from a stale one,
 * and a stale figure read as current is how an operator concludes the fleet is halted when it is not
 * (FR34). `stale` says so explicitly rather than letting an old number stand.
 */
export function Stat({
  label,
  value,
  unit,
  tone,
  meta,
  small,
}: {
  label: string;
  value: ReactNode;
  unit?: string;
  /** alarm and warn are HAZARD tones — permitted here only for an alarming or attention-needing figure. */
  tone?: "alarm" | "warn";
  meta?: ReactNode;
  small?: boolean;
}) {
  const cls = ["stat", tone === "alarm" ? "stat--alarm" : "", tone === "warn" ? "stat--warn" : ""]
    .filter(Boolean)
    .join(" ");
  return (
    <div className={cls}>
      <span className="stat__label">{label}</span>
      <span className={small ? "stat__value stat__value--sm" : "stat__value"}>
        {value}
        {unit ? <span className="stat__unit"> {unit}</span> : null}
      </span>
      {meta ? <span className="stat__meta">{meta}</span> : null}
    </div>
  );
}

/** StatRow lays stats out on the shared grid so two pages of stats never disagree about width. */
export function StatRow({ children }: { children: ReactNode }) {
  return <div className="stat-row">{children}</div>;
}

/* ══════════════════════════════════════════════════════════════════════════
   Timeline
   ══════════════════════════════════════════════════════════════════════════ */

/** Timeline renders ordered events: when on the left, what on the right, newest first. */
export function Timeline({ children }: { children: ReactNode }) {
  return <ul className="timeline">{children}</ul>;
}

export function TimelineItem({ when, children }: { when: string; children: ReactNode }) {
  return (
    <li className="timeline__item">
      <span className="timeline__when">{when}</span>
      <span className="timeline__what">{children}</span>
    </li>
  );
}

/* ══════════════════════════════════════════════════════════════════════════
   Drawer
   ══════════════════════════════════════════════════════════════════════════ */

/**
 * Drawer discloses a subject's detail in place.
 *
 * It is a native `<details>` on purpose: keyboard operation, the open/closed state and the
 * accessibility semantics come from the platform rather than from a hand-rolled toggle that has to
 * re-earn them. `id` makes a drawer addressable, which is what lets the command palette NAVIGATE to a
 * dangerous action's confirmation without performing it (FR37).
 */
export function Drawer({
  summary,
  id,
  open,
  children,
}: {
  summary: string;
  id?: string;
  open?: boolean;
  children: ReactNode;
}) {
  return (
    <details className="drawer" id={id} open={open}>
      <summary>{summary}</summary>
      <div className="drawer__body">{children}</div>
    </details>
  );
}

/* ══════════════════════════════════════════════════════════════════════════
   Freshness — the as-of marker every live figure carries (FR34)
   ══════════════════════════════════════════════════════════════════════════ */

/**
 * Freshness states when a figure was last confirmed, and says so LOUDLY when it could not be.
 *
 * `stale` is its own rendering rather than an absence: a figure the console failed to refresh looks
 * exactly like a fresh one, and on this surface that similarity is the hazard.
 */
export function Freshness({ asOf, stale }: { asOf: string; stale?: boolean }) {
  return (
    <span className={stale ? "freshness freshness--stale" : "freshness"}>
      {stale ? `Stale — could not refresh. Last confirmed ${asOf}` : `As of ${asOf}`}
    </span>
  );
}
