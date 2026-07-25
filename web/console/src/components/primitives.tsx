import type { ReactNode } from "react";
import Link from "next/link";
import { AlertTriangle, FolderOpen, Hash, Lock, Package, Search, WifiOff } from "lucide-react";
import { statusOf, TONE_CLASS, type Tone } from "@/lib/status";
import { emphasis, qualifiersOf, QUALIFIER_COPY, QUALIFIER_LABEL } from "@/lib/emphasis";
import { cx } from "@/lib/cx";

/**
 * primitives.tsx is the closed set every view composes from (tasks 5.3, 5.4, 5.6, 5.10, 5.11).
 *
 * # Why the set is closed
 *
 * A view that needs a shape not here is a change to THIS FILE first, reviewed, and not a page-local
 * component that quietly becomes a second design language. The design language in this repository has
 * already forked three ways across four files; the mechanism was always a shape somebody needed once.
 *
 * # Where the shapes came from
 *
 * They are the components in `Component library design` — the same header, chip, value, stat, banner,
 * table and state blocks — re-expressed against this console's own vocabulary. Two things were
 * deliberately NOT carried across from that bundle:
 *
 *   1. **Its status list.** The bundle enumerates eleven presentational statuses (`running`, `tied`,
 *      `withheld`, …) mixing run state, comparison outcome and entitlement into one axis. This console
 *      already has that vocabulary, split correctly, in `lib/status.ts` and `lib/emphasis.ts`: a status
 *      is what the platform said the thing IS, a qualifier is a withdrawal of confidence in a number.
 *      Collapsing them is exactly the R3/R14 defect.
 *   2. **Its fixed hues.** `text-emerald-400` is a dark-theme colour. Every status here names a token
 *      that flips with the palette, so the light theme carries the same information rather than a
 *      washed-out approximation of it.
 *
 * # Why the treatments are classes rather than utility strings
 *
 * A chip, a state and a value each encode a RULE — a status carries a word, an unknown status cannot
 * impersonate a known one, a qualified figure loses weight rather than changing hue. Those rules live
 * in `globals.css` under one name each, so there is one place to change them and one place to read
 * them. The layout around them is utilities, which is what utilities are good at.
 *
 * # Why React and not string interpolation
 *
 * `p25monitor.html` builds table rows by concatenation — `'<td>' + n.node_id + '</td>'` — and the page
 * has **no escaping helper defined at all**, while `node_id` is derived from customer source code.
 * React's default escaping removes the class of defect rather than fixing one instance — provided the
 * port never reaches for `dangerouslySetInnerHTML`, which `scan-markup.mjs` bans (task 5.7).
 */

// ── PageFrame · R13: one subject, present in the first paint ─────────────────

/**
 * PageFrame carries a view's single subject.
 *
 * `title` is the subject and it is a REQUIRED prop, rendered as the one display-level heading. That is
 * the mechanism behind R13: a view cannot be written without naming its subject, and it cannot name
 * two, because there is one slot.
 *
 * Crucially the frame renders **around** its data. A page renders the frame synchronously and streams
 * the data into it, so the subject is on screen before the data resolves — the reader can confirm they
 * opened the right thing during the second they most want to know.
 *
 * The measure is the frame's job and not a page's. `wide` is the only escape, for the views whose
 * content is genuinely a wide table, and it widens the measure rather than removing it: a leaderboard
 * running the full width of a 32-inch display is unreadable in a different way from a cramped one.
 */
export function PageFrame({
  eyebrow,
  title,
  lede,
  actions,
  children,
  wide,
}: {
  eyebrow: string;
  title: string;
  lede?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  wide?: boolean;
}) {
  return (
    // Viewport-first (NFR17): the frame FILLS the main region (h-full) and clips (overflow-hidden), so
    // the page never grows the document. A compact, shrink-0 header keeps the subject and actions fixed;
    // the body is a bounded flex region (min-h-0 flex-1) that OWNS its own scroll — the page does not.
    // A page whose sections would stack tall should split them into <Tabs> instead of relying on this
    // body scroll (the studio does).
    <div
      className={cx(
        "flex h-full min-h-0 w-full flex-col px-4 pt-5 md:px-8",
        wide ? "max-w-[120rem]" : "max-w-6xl",
      )}
    >
      <div className="mb-3 shrink-0 border-b border-border/60 pb-3">
        <p className="mb-1 font-mono text-[10px] uppercase tracking-[0.18em] text-primary">{eyebrow}</p>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h1 className="page__title min-w-0 break-words font-display font-normal leading-tight text-foreground">
            {title}
          </h1>
          {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
        </div>
        {lede ? <p className="mt-1.5 max-w-3xl text-sm leading-snug text-muted-foreground">{lede}</p> : null}
      </div>
      {/* The body owns the scroll, not the page. pb-5 gives the last row breathing room inside the box. */}
      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto pb-5">{children}</div>
    </div>
  );
}

/** Section is a titled panel. `aside` carries provenance — as-of, source, hash — never a control. */
export function Section({
  title,
  aside,
  children,
  id,
}: {
  title: string;
  aside?: ReactNode;
  children: ReactNode;
  id?: string;
}) {
  return (
    <section className="flex flex-col gap-4" id={id} aria-labelledby={id ? `${id}-title` : undefined}>
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <h2
          className="section__title font-display font-normal text-foreground"
          id={id ? `${id}-title` : undefined}
        >
          {title}
        </h2>
        {aside ? (
          <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">{aside}</div>
        ) : null}
      </div>
      <div className="flex flex-col gap-4">{children}</div>
    </section>
  );
}

/** Row lays a set of chips, statuses or small controls out on one line. */
export function Row({ children }: { children: ReactNode }) {
  return <div className="flex flex-wrap items-center gap-2">{children}</div>;
}

/** Card is the panel a grouped readout sits on. It carries no semantics — only a surface. */
export function Card({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cx("rounded-xl border border-border bg-card p-5", className)}>{children}</div>;
}

// ── Status · R3 ──────────────────────────────────────────────────────────────

/**
 * Status renders a status value with a distinct colour AND a distinct word, and degrades visibly.
 *
 * An unmodelled value gets the `unknown` treatment — dashed, no status hue — plus the raw value and a
 * screen-reader-visible note. It cannot impersonate a modelled state, which is the specific failure
 * `p25monitor.html` has today.
 *
 * The dot is decoration ON TOP of the word, never instead of it, and it takes `currentColor` so it
 * cannot drift from the chip it sits in. A chip whose only signal is a coloured dot is unreadable in
 * greyscale, to a colour-blind reader, and to a screen reader.
 */
export function Status({ value, title }: { value: string | null | undefined; title?: string }) {
  const status = statusOf(value);
  const tone = TONE_CLASS[status.tone];
  return (
    <span className={tone ? `chip ${tone}` : "chip"} title={title ?? status.raw}>
      <span className="chip__dot" aria-hidden="true" />
      {status.word}
      {status.known ? null : (
        <span className="visually-hidden"> (a status this console does not model, shown as received)</span>
      )}
    </span>
  );
}

/** Chip is the generic labelled token: a key, a hash, a count. Never a status — use `Status`. */
export function Chip({
  children,
  tone,
  title,
  variant = "default",
}: {
  children: ReactNode;
  /** A tone, for the few chips that legitimately carry a pass/fail sense of their own. */
  tone?: Tone;
  title?: string;
  variant?: "default" | "hash" | "count" | "plan";
}) {
  const variants = {
    default: "",
    hash: "font-mono",
    count: "tabular-nums text-foreground",
    plan: "border-primary/25 bg-primary/10 text-primary",
  } as const;
  return (
    <span className={cx("chip", tone ? TONE_CLASS[tone] : variants[variant])} title={title}>
      {variant === "hash" ? <Hash className="size-2.5 opacity-40" aria-hidden="true" /> : null}
      {children}
    </span>
  );
}

// ── Emphasis · 🔴 R14 ────────────────────────────────────────────────────────

/**
 * Value renders a figure with the emphasis its qualifiers permit, and the qualifiers beside it.
 *
 * This is the ONLY place the reserved treatment is applied. A test asserts that no component writes
 * the class by hand — because `className={row.tie ? … : …}` at a dozen call sites is a rule with a
 * dozen chances to be got wrong, and the twelfth is written under deadline.
 *
 * A qualifier renders as a short badge AND its full sentence. The badge is what a reader scanning
 * forty rows sees; the sentence is what tells them what it MEANS. Dropping the sentence would leave a
 * coloured word that reads as a category rather than as a withdrawal of confidence — and putting it in
 * a `title` would hide it from touch, from keyboard, and from every screen reader that does not
 * announce tooltips.
 */
export function Value({
  children,
  flags,
  showQualifiers = true,
}: {
  children: ReactNode;
  flags?: readonly string[] | null;
  showQualifiers?: boolean;
}) {
  const level = emphasis(flags);
  const qualifiers = qualifiersOf(flags);
  return (
    <span className="inline-flex flex-col items-start gap-1">
      <span className={level}>{children}</span>
      {showQualifiers && qualifiers.length > 0 ? (
        <span className="flex flex-col gap-0.5">
          {qualifiers.map((q) => (
            <span className="qualifier" key={q}>
              <span className="qualifier__badge">{QUALIFIER_LABEL[q]}</span>
              <span className="qualifier__copy">{QUALIFIER_COPY[q]}</span>
            </span>
          ))}
        </span>
      ) : null}
    </span>
  );
}

// ── Stat · 🔴 R16 / FR36 ─────────────────────────────────────────────────────

/**
 * Stat is how this console renders a quantity, and it exists because the console rendered them
 * wrongly.
 *
 * # The defect it replaces
 *
 * Measured in a browser against the real hermes checkout: the graph view presented `40 nodes`,
 * `0 edges` and `0 LLM calls` as extra-small grey chips, inside a section whose heading — the words
 * "This graph", which carry no information at all — was set larger. The reader came for three integers
 * and those integers were the smallest text on the screen.
 *
 * That is an inverted hierarchy, not a taste disagreement, and it is measurable: `--type-stat`,
 * `--type-section` and `--type-label` are declared together and a test compares them.
 *
 * # The reservation still governs (🔴 R14 / FR29)
 *
 * `flags` goes through the same `emphasis()` every other value uses. This matters MORE at display
 * scale, not less: size reads as certainty, so a provisional number set large asserts more than the
 * same number in a table cell. A qualified stat is drawn qualified and wears its qualifier beside it
 * at readable size — never shrunk into a footnote, never deferred to a tooltip.
 *
 * # `unit` is stated once (FR31)
 *
 * The unit belongs to the stat, not to the figure, so it is a prop rather than something a caller
 * concatenates into `value`. Concatenation is how `1.2s`, `1.2 s` and `1.2 sec` end up on one screen.
 */
export function Stat({
  label,
  value,
  unit,
  flags,
  note,
  dense,
}: {
  label: string;
  value: ReactNode;
  /** The unit or scale, stated once beside the figure — never repeated per value. */
  unit?: string;
  /** Server-supplied qualifiers. Read, never derived: deciding here would be a client-side statistic. */
  flags?: readonly string[] | null;
  /** Provenance or a caveat that is not a qualifier — an as-of time, a source. */
  note?: ReactNode;
  dense?: boolean;
}) {
  const level = emphasis(flags);
  const qualifiers = qualifiersOf(flags);
  return (
    <div className={cx("flex min-w-0 flex-col gap-1.5", dense && "stat--dense")}>
      <span className="stat__label">{label}</span>
      <span className="flex flex-wrap items-baseline gap-2">
        <span className={cx("stat__value", level)}>{value}</span>
        {unit ? <span className="stat__unit">{unit}</span> : null}
      </span>
      {qualifiers.length > 0 ? (
        <span className="flex flex-col gap-0.5">
          {qualifiers.map((q) => (
            <span className="qualifier" key={q}>
              <span className="qualifier__badge">{QUALIFIER_LABEL[q]}</span>
              <span className="qualifier__copy">{QUALIFIER_COPY[q]}</span>
            </span>
          ))}
        </span>
      ) : null}
      {note ? <span className="stat__note">{note}</span> : null}
    </div>
  );
}

/** Stats lays a set of Stat out on the composition grid. `fill` spreads them; the default hugs. */
export function Stats({ children, fill }: { children: ReactNode; fill?: boolean }) {
  return (
    <div className={cx("gap-x-10 gap-y-6", fill ? "grid grid-cols-2 lg:grid-cols-4" : "flex flex-wrap")}>
      {children}
    </div>
  );
}

// ── The three data states · R5 ───────────────────────────────────────────────

/**
 * Loading is a SKELETON, shaped like the content it precedes (R13/FR27).
 *
 * `rows` is the shape, not a decoration: the point is that the populated view has the same structure,
 * so the arrival of data changes values and never layout. A generic spinner cannot do that, which is
 * why one is not offered here. The sweep is the console's one animation and it stops entirely under
 * `prefers-reduced-motion`, where a static bar still says "something of this size is coming".
 */
export function Loading({ rows = 3, label }: { rows?: number; label: string }) {
  return (
    <div className="skeleton" role="status" aria-live="polite">
      <span className="visually-hidden">{label}</span>
      {Array.from({ length: rows }, (_, index) => (
        <span
          className="skeleton__bar h-4 rounded bg-muted motion-safe:animate-pulse"
          key={index}
          style={{ width: `${92 - index * 11}%` }}
          aria-hidden="true"
        />
      ))}
    </div>
  );
}

/**
 * Empty is a real state with a NEXT ACTION, not an absence.
 *
 * `p4board.html` already gets this right — *No variant passed every gate.* reads differently from
 * *No variants on this board yet. Add a variant to compare.* — and the port must not flatten them into
 * one grey sentence. Where the copy depends on a status, the caller passes the status-dependent
 * string; this component does not invent one.
 */
export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="state state--empty flex flex-col items-center">
      <FolderOpen className="mb-3 size-6 text-muted-foreground/40" aria-hidden="true" />
      <p className="state__title text-foreground">{title}</p>
      {children ? <div className="state__body text-center">{children}</div> : null}
    </div>
  );
}

/**
 * FailureKind is the taxonomy as the browser sees it, mirroring `PlatformFailureKind`.
 *
 * They are five renderings, not five strings in one box: different icon, different border, different
 * surface, so they are distinguishable before the copy is read. Collapsing them into one *Something
 * went wrong* turns four remedies — mount the subsystem, check the id, check the network, upgrade the
 * plan — into none.
 */
export type FailureKind = "not-mounted" | "not-found" | "gated" | "upstream" | "transport";

/** FailureCopy is what a non-gated failure class needs to be distinguishable before it is read. */
type FailureCopy = { title: string; body: string; icon: ReactNode; tint: string };

/**
 * 🔴 `gated` is a state, not a failure — and rendering it as one is what FR15 forbids.
 *
 * Before this existed, an entitlement refusal fell through to *"The platform refused this request"*.
 * Nothing is broken and nothing has gone wrong: the capability is outside the tenant's plan, which is a
 * commercial boundary the product should state plainly. A reader shown an error opens a support ticket;
 * a reader shown the plan that unlocks it has a different conversation entirely, and — more to the
 * point — has been told the truth about what the product does.
 *
 * The plan name comes from the platform's own `DenialView`, never from the console's capability table.
 * That table exists to say which plan to MENTION in the absence of an answer; this is the answer, and
 * it wins, because the screen and the gate must not be able to disagree.
 */
export function Failure({
  kind,
  error,
  subject,
  denial,
  children,
}: {
  kind: FailureKind;
  /** error is the UPSTREAM message. It is rendered verbatim: the platform's words are the product. */
  error: string;
  /** subject names what was being loaded, so the message is about something rather than about nothing. */
  subject?: string;
  /** denial is the entitlement gate's decision, present on a `gated` outcome. */
  denial?: {
    feature_label?: string;
    reason?: string;
    upgrade_plan_name?: string;
  };
  children?: ReactNode;
}) {
  if (kind === "gated") {
    const capability = denial?.feature_label ?? subject ?? "This capability";
    const plan = denial?.upgrade_plan_name;
    return (
      <div className="state state--gated" role="note">
        <p className="state__title text-gated">
          <Lock className="size-4 shrink-0" aria-hidden="true" />
          {capability} {plan ? `is on the ${plan} plan` : "is not included in this plan"}
        </p>
        <div className="state__body">
          {/* The platform's own reason, where it gave one. Nothing is invented and nothing is softened. */}
          {denial?.reason ? <p>{denial.reason}</p> : null}
          <p>
            Nothing is wrong and nothing has failed — this is a plan boundary rather than an error.
            Everything else on this console is unaffected.
          </p>
          <p>
            <Link className="text-primary underline underline-offset-2" href="/app/account">
              See what this plan includes
            </Link>
          </p>
          {children}
        </div>
      </div>
    );
  }

  const copy: Record<Exclude<FailureKind, "gated">, FailureCopy> = {
    "not-mounted": {
      title: "This subsystem is not mounted on this deployment",
      body: "It is not an error and nothing is wrong with your data — the capability is simply not installed here. Everything else on this console is unaffected.",
      icon: <Package className="size-4 shrink-0" aria-hidden="true" />,
      tint: "text-neutral",
    },
    "not-found": {
      title: subject ? `No such ${subject}` : "No such subject",
      // The parenthetical is the product. `p35graph.html` says it explicitly, and it is the difference
      // between a user checking their identifier and a user filing a bug about missing data.
      body: "This is a routing fact, not a measurement: the identifier does not resolve. It is distinct from a subject that exists and has nothing to show yet.",
      icon: <Search className="size-4 shrink-0" aria-hidden="true" />,
      tint: "text-unknown",
    },
    transport: {
      title: "Could not reach the platform",
      body: "This is a transport failure, not an empty result. Nothing has been measured, and nothing about your data has changed — the console could not ask.",
      icon: <WifiOff className="size-4 shrink-0" aria-hidden="true" />,
      tint: "text-bad",
    },
    upstream: {
      title: "The platform refused this request",
      body: "The platform answered, and it answered with a refusal. Its own message is below.",
      icon: <AlertTriangle className="size-4 shrink-0" aria-hidden="true" />,
      tint: "text-warn",
    },
  };
  const { title, body, icon, tint } = copy[kind as Exclude<FailureKind, "gated">];
  return (
    <div className={`state state--${kind}`} role="alert">
      <p className={cx("state__title", tint)}>
        {icon}
        {title}
      </p>
      <div className="state__body">
        <p>{body}</p>
        <p className="mono overflow-x-auto rounded-lg border border-border bg-muted/20 px-3 py-2 text-xs">
          {error}
        </p>
        {children}
      </div>
    </div>
  );
}

// ── Data table · R6 + FR31 ───────────────────────────────────────────────────

export type Column = {
  key: string;
  label: string;
  /** numeric columns are right-aligned and tabular so magnitude is visible without reading. */
  numeric?: boolean;
};

/**
 * DataTable enforces the two table properties that are always forgotten: SCOPED column headers and a
 * caption. Both are free to write once and invisible to omit — which is why they live here rather
 * than in each view.
 *
 * The caption is rendered to assistive technology only. It is not decoration a designer can delete: a
 * table announced without one is announced as "table, six columns" and nothing else.
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
    <div className="overflow-hidden rounded-xl border border-border">
      <div className="overflow-x-auto">
        <table className="data-table">
          <caption>{caption}</caption>
          <thead>
            <tr>
              {columns.map((column) => (
                <th key={column.key} scope="col" className={column.numeric ? "num" : undefined}>
                  {column.label}
                </th>
              ))}
            </tr>
          </thead>
          {children}
        </table>
      </div>
    </div>
  );
}

// ── Banner ───────────────────────────────────────────────────────────────────

/** Banner is a board- or view-level caveat: it changes what the WHOLE view means. */
export function Banner({
  tone = "info",
  title,
  children,
}: {
  tone?: "info" | "warn" | "bad";
  title: string;
  children?: ReactNode;
}) {
  return (
    <div className={`banner banner--${tone}`} role="note">
      <p className="banner__title">
        <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
        <span>{title}</span>
      </p>
      {children ? (
        <div className="mt-2 flex max-w-3xl flex-col gap-2 pl-6 text-sm leading-relaxed text-muted-foreground">
          {children}
        </div>
      ) : null}
    </div>
  );
}

// ── Confidence interval bar ──────────────────────────────────────────────────

/**
 * CIBar draws an interval as an interval: a span with a mark in it, not a bar to a point.
 *
 * `left`, `width` and `mean` arrive as percentages already scaled by the caller against the WHOLE
 * board's range, so two rows' bars are comparable. Scaling per row would make every interval look the
 * same width, which is the one thing this graphic exists to show.
 */
export function CIBar({
  left,
  width,
  mean,
  label,
}: {
  left: number;
  width: number;
  mean: number;
  label: string;
}) {
  return (
    <span
      className="ci"
      role="img"
      aria-label={label}
      style={
        {
          "--left": `${left}%`,
          "--width": `${width}%`,
          "--mean": `${mean}%`,
        } as React.CSSProperties
      }
    />
  );
}
