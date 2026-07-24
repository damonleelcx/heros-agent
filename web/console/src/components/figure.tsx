import type { ReactNode } from "react";

/**
 * figure.tsx is the accessible-graphic pattern (R6, task 5.6).
 *
 * # What it enforces, and why each half is required
 *
 * Every graphical data representation in this console is wrapped by `Figure`, which takes **two
 * required props** a caller cannot omit:
 *
 *   `alt`       a text alternative describing what the graphic SHOWS — not what it is. "A scatter of
 *               11 variants; 3 are on the frontier" is an alternative; "Pareto chart" is a label.
 *   `table`     a `<details>` tabular fallback carrying the same numbers.
 *
 * Both, because they answer different questions. The alternative tells a screen-reader user what they
 * are looking at in one sentence; the table lets them read the actual values, which is the only way a
 * chart's information is genuinely available without seeing it. `p4board.html` already ships the
 * pattern for its scatter plot — this makes it the floor rather than the exception.
 *
 * The graph in `p35graph.html` is the strongest case for this file existing: it is an SVG with **no
 * text alternative at all**, and it is the primary comprehension surface for a workflow. Today it is
 * unreadable to a screen reader.
 *
 * # Why the table is a `<details>` and not visually hidden
 *
 * Because sighted readers want it too. The number behind a bar is the thing people squint at charts
 * to estimate, and a disclosure that gives it to everyone is better than a hidden one that serves
 * only assistive technology. Accessibility work that also helps the majority is the kind that
 * survives a redesign.
 */
export function Figure({
  title,
  alt,
  table,
  children,
}: {
  title: string;
  /** A sentence describing what the graphic shows. Required — a graphic without one does not ship. */
  alt: string;
  /** The same data as a table. Required, for the same reason. */
  table: ReactNode;
  children: ReactNode;
}) {
  return (
    <figure className="m-0 flex flex-col gap-3">
      <figcaption className="caption">{title}</figcaption>
      {/*
        `role="img"` plus the alternative makes the whole graphic one accessible object with one
        description, rather than a tree of unlabelled shapes a screen reader reads out individually —
        which is worse than silence.
      */}
      <div
        role="img"
        aria-label={alt}
        className="overflow-hidden rounded-xl border border-border bg-graph-canvas"
      >
        {children}
      </div>
      <Disclosure className="figure__table" summary="Show the underlying values">
        {table}
      </Disclosure>
    </figure>
  );
}

/**
 * Disclosure is the one `<details>` treatment, so a fallback table, a build log and a breakdown all
 * open the same way.
 *
 * It uses the native element rather than a scripted accordion: the native one is keyboard-operable,
 * announced correctly, findable by the browser's own in-page search, and works before hydration.
 */
export function Disclosure({
  summary,
  children,
  open,
  className,
}: {
  summary: ReactNode;
  children: ReactNode;
  open?: boolean;
  className?: string;
}) {
  return (
    <details className={`group rounded-xl border border-border ${className ?? ""}`} open={open}>
      <summary className="cursor-pointer list-none px-4 py-2.5 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground [&::-webkit-details-marker]:hidden">
        <span className="inline-flex items-center gap-2">
          <span
            className="inline-block transition-transform group-open:rotate-90"
            aria-hidden="true"
          >
            ›
          </span>
          {summary}
        </span>
      </summary>
      <div className="border-t border-border p-4">{children}</div>
    </details>
  );
}

/**
 * Tooltip is the focus-reachable readout pattern (P4-27).
 *
 * The load-bearing detail is in the caller: a tooltip is bound to **`focus` as well as hover**, and
 * carries `role="status"` so its content is announced when it changes. A hover-only tooltip is
 * unreachable by keyboard, by touch, and by anyone navigating with assistive technology — which is
 * most of the ways a chart's detail is actually asked for.
 *
 * It renders in the flow rather than as an overlay: an absolutely-positioned tooltip near a viewport
 * edge is clipped, and a clipped tooltip is a value nobody can read. `min-h` reserves its line so the
 * page does not jump when it appears.
 */
export function Tooltip({ children, visible }: { children: ReactNode; visible: boolean }) {
  return (
    <p className="mono min-h-5 text-xs text-muted-foreground" role="status" aria-live="polite">
      {visible ? children : null}
    </p>
  );
}
