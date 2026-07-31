import type { ChangeDeliveryView, DeliveryCellView, DeliverySourceCellView } from "@/lib/types.generated";
import { Chip, Row } from "@/components/primitives";

/**
 * deliveryRoutes.tsx renders HOW AN ACCEPTED CHANGE REACHES A RUNNING AGENT — as a route ledger.
 *
 * # Why this is not a status word
 *
 * Delivery used to render as one word per change: delivered, or pending. That word had no room for the
 * thing that is true most of the time — the rewriter refused, so there is no diff, so there is no pull
 * request, so nothing is going to happen. On screen that state was indistinguishable from "queued
 * behind other work", which is how a dead end came to look like a promise.
 *
 * So every change shows BOTH ROUTES SIDE BY SIDE, always, and a change both routes refuse reads as
 * `undeliverable` — a word with no hopeful synonym.
 *
 * # Why the two refusals look different
 *
 * A refusal's next action differs completely depending on whose move it is, and only one of the three
 * is a wait:
 *
 *   nobody's move    — program structure, not data. There is no "when". The reader stops asking.
 *   your move        — the node applies inline; a bound migration would unlock it.
 *   our move         — the document has no field yet. There IS a when, and the missing piece is NAMED.
 *
 * 🔴 The permanent one may never acquire an artifact or a date. A boundary that starts rendering like a
 * backlog item is how "we will never do this" becomes "we have not done this yet", and the product ends
 * up promising something that cannot be built. `permanent` is a boolean on the wire, and it — never a
 * sentence — selects the treatment.
 *
 * # Why the hazard palette is not used
 *
 * `--warn`/`--danger` are reserved for hazard (project.md). A refusal is an ANSWER, not a hazard, and
 * spending the hazard colour here would make it stop meaning anything where it matters. The states are
 * separated by border weight and style, so they stay distinguishable without colour.
 */

/** The six route-cell states. No seventh, and no default. */
export type RouteState = "delivers" | "varies" | "boundary" | "contingent" | "migration" | "gap";

export function routeStateOf(cell: DeliveryCellView): RouteState {
  if (cell.status === "delivers") return "delivers";
  // 🔴 The source route's honest answer is usually neither yes nor no: P12 can carry any diff, but
  // whether a diff EXISTS is decided per language and per call-site form. Rendering that as "carries it"
  // would tell a reader with a Rust repository that a pull request is coming.
  if (cell.status === "varies-by-language") return "varies";
  // 🔴 Contingent before boundary. Memory and wiring both refuse as "not data" and carry the same cause,
  // but wiring is a property of compiled code while memory waits on a runtime component that could
  // exist. A reader who cannot tell them apart draws the wrong conclusion from one of them: either they
  // stop asking about something merely unbuilt, or they keep waiting on something that cannot be built.
  if (cell.contingent) return "contingent";
  if (cell.permanent) return "boundary";
  if (cell.cause === "node-not-bound") return "migration";
  return "gap";
}

const ROUTE_STYLE: Record<RouteState, { box: string; dot: string; short: string }> = {
  delivers: {
    box: "border-primary/45 bg-primary/[0.07] text-foreground",
    dot: "bg-primary",
    short: "carries it",
  },
  varies: {
    // Half-committed on purpose: the route is real, the outcome is a cell in another table.
    box: "border-primary/30 bg-primary/[0.03] text-foreground/85",
    dot: "bg-primary/45",
    short: "per language",
  },
  migration: {
    // A left rule: something to act on, at the reader's end.
    box: "border-border border-l-2 border-l-foreground/45 bg-surface text-foreground/80",
    dot: "bg-foreground/45",
    short: "your node",
  },
  boundary: {
    // Flat and quiet: there is nothing to do and no date to wait for.
    box: "border-border/50 bg-background text-muted-foreground",
    dot: "bg-muted-foreground/50",
    short: "not data",
  },
  contingent: {
    // Quiet like a boundary — it is not data today either — but a solid left rule marks that something
    // is genuinely missing rather than impossible. 🚫 It is deliberately NOT the dashed "ours" treatment:
    // naming a missing component is not a commitment to build it, and a dashed row reads as a promise.
    box: "border-border/60 border-l-2 border-l-muted-foreground/60 bg-background text-muted-foreground",
    dot: "bg-muted-foreground/70",
    short: "needs a component",
  },
  gap: {
    // Dashed: unfinished, and ours. The only state that names an artifact.
    box: "border-dashed border-primary/50 bg-background text-foreground/75",
    dot: "bg-primary/50",
    short: "not yet — ours",
  },
};

/** How a change kind reads in a sentence, derived from the wire identifier only. */
function changeLabel(change: string): string {
  return change.replace(/-/g, " ");
}

/**
 * undeliverableChanges is the set of changes NO route can carry, anywhere.
 *
 * 🔴 It needs both tables, and that is the point. The ledger alone cannot answer it: the source route's
 * cell says "varies by language", which is neither a yes nor a no. A change is undeliverable only when
 * the runtime route refuses AND every registered language refuses the source route — otherwise some
 * call site somewhere does get a diff, and calling it undeliverable would be as wrong as calling it
 * delivered.
 *
 * This is derivation, but it is derivation of a JOIN rather than of a verdict: every input is a status
 * the platform sent, and nothing here decides whether a cell "counts".
 */
export function undeliverableChanges(view: ChangeDeliveryView): Set<string> {
  const out = new Set<string>();
  const cells = view.cells ?? [];
  const source = view.source_cells ?? [];
  const changes = new Set(cells.map((c) => c.change));
  for (const change of changes) {
    const runtime = cells.find((c) => c.change === change && c.route === "runtime");
    if (!runtime || runtime.status !== "refuses") continue;
    const langs = source.filter((c) => c.change === change);
    // No per-language rows at all is not evidence of anything, so it is not counted. An absent row must
    // never become a verdict.
    if (langs.length === 0) continue;
    if (langs.every((c) => c.status === "refuses")) out.add(change);
  }
  return out;
}

export function DeliveryRouteLegend({ view }: { view: ChangeDeliveryView }) {
  const causes = new Map((view.causes ?? []).map((c) => [c.id, c]));
  const order: Array<{ state: RouteState; causeId?: string; fallback: string }> = [
    { state: "delivers", fallback: "This route can carry the change." },
    { state: "migration", causeId: "node-not-bound", fallback: "" },
    { state: "boundary", causeId: "not-runtime-resolvable", fallback: "" },
    {
      state: "contingent",
      fallback:
        "Not data today for a reason that could change — a runtime component is missing, and it is named. Naming it is not a commitment to build it.",
    },
    { state: "gap", causeId: "no-rollout-binding", fallback: "" },
  ];
  return (
    <ul className="grid gap-2 sm:grid-cols-2">
      {order.map(({ state, causeId, fallback }) => {
        const style = ROUTE_STYLE[state];
        const cause = causeId ? causes.get(causeId) : undefined;
        return (
          <li key={state} className={`flex gap-2.5 rounded-md border px-3 py-2 text-xs ${style.box}`}>
            <span className={`mt-1 h-2 w-2 shrink-0 rounded-full ${style.dot}`} aria-hidden />
            <span className="min-w-0">
              <span className="mono block text-[11px] uppercase tracking-wide">{style.short}</span>
              <span className="mt-0.5 block leading-snug">{cause?.label ?? fallback}</span>
              {cause ? (
                <span className="mt-1 block text-[11px] text-muted-foreground">
                  Whose move: <strong className="font-medium">{cause.owner}</strong>
                  {cause.permanent ? " · a boundary, not a backlog item" : null}
                </span>
              ) : null}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

/** One route's answer, rendered as a cell in the ledger. */
function RouteCell({ cell }: { cell: DeliveryCellView }) {
  const state = routeStateOf(cell);
  const style = ROUTE_STYLE[state];
  return (
    <div className={`h-full rounded-md border px-3 py-2 text-xs ${style.box}`}>
      <span className="flex items-center gap-1.5">
        <span className={`h-2 w-2 shrink-0 rounded-full ${style.dot}`} aria-hidden />
        <span className="mono text-[11px] uppercase tracking-wide">{style.short}</span>
      </span>
      {cell.note ? <p className="mt-1.5 leading-snug">{cell.note}</p> : null}
      {/*
        🔴 The artifact renders ONLY when the cause is not permanent. The wire already guarantees a
        permanent cell carries none, and this condition is the second lock: if the backend ever regressed
        and sent one, the console still would not render a date onto a boundary.
      */}
      {!cell.permanent && cell.missing_artifact ? (
        <p className="mt-1.5 text-[11px] text-muted-foreground">
          Missing: <span className="mono">{cell.missing_artifact}</span>
        </p>
      ) : null}
      {/*
        A contingent refusal names the component it waits on, and nothing else. There is no date here
        and no place to put one — the payload carries none, and this markup offers no slot for it.
      */}
      {cell.contingent && cell.missing_component ? (
        <p className="mt-1.5 text-[11px] text-muted-foreground">
          Waiting on: <span className="mono">{cell.missing_component}</span>
        </p>
      ) : null}
      {cell.bound_only ? (
        <p className="mt-1.5 text-[11px] text-muted-foreground">
          Eligible once the node applies in bound mode.
        </p>
      ) : null}
    </div>
  );
}

/**
 * DeliveryRouteLedger is the surface's centre: one row per change kind, one cell per route, every cell
 * filled.
 *
 * 🔴 A change with no row, or a row with one route, is the one thing this design may not do. An absent
 * cell renders as "not applicable", which is a claim nobody made.
 */
export function DeliveryRouteLedger({ view }: { view: ChangeDeliveryView }) {
  const cells = view.cells ?? [];
  const routes = view.routes ?? [];
  const changes: string[] = [];
  for (const c of cells) if (!changes.includes(c.change)) changes.push(c.change);
  const undeliverableSet = undeliverableChanges(view);

  return (
    <div className="overflow-hidden rounded-xl border border-border">
      <div className="overflow-x-auto">
        <table className="w-full min-w-[46rem] border-collapse text-sm">
          <caption className="sr-only">
            Every change the platform can propose, and what each delivery route does with it.
          </caption>
          <thead>
            <tr className="border-b border-border bg-surface">
              <th scope="col" className="px-3 py-2 text-left text-xs font-medium text-muted-foreground">
                Change
              </th>
              {routes.map((r) => (
                <th
                  key={r.id}
                  scope="col"
                  className="px-3 py-2 text-left text-xs font-medium text-muted-foreground"
                >
                  {r.label}
                  <span className="mt-0.5 block font-normal leading-snug">{r.permanence}</span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {changes.map((change) => {
              const row = cells.filter((c) => c.change === change);
              const axis = row[0]?.axis ?? "";
              const undeliverable = undeliverableSet.has(change);
              return (
                <tr key={change} className="border-b border-border/60 align-top last:border-0">
                  <th scope="row" className="px-3 py-3 text-left align-top">
                    <span className="block text-sm font-medium">{changeLabel(change)}</span>
                    <span className="mt-1 flex flex-wrap gap-1">
                      <Chip>{axis}</Chip>
                      {/*
                        The verdict is stated on the row rather than left to be inferred from two cells.
                        "Undeliverable" is terminal and honest; there is deliberately no "pending".

                        🔴 It requires the runtime route to refuse AND every language to refuse the
                        source route — a row whose source cell merely "varies" is NOT undeliverable,
                        because some call site somewhere does get a diff. Counting it as undeliverable
                        would be as wrong as counting it as delivered.
                      */}
                      {undeliverable ? <Chip title="Every route refuses this change, in every language.">undeliverable</Chip> : null}
                    </span>
                  </th>
                  {routes.map((r) => {
                    const cell = row.find((c) => c.route === r.id);
                    return (
                      <td key={r.id} className="px-3 py-3">
                        {cell ? (
                          <RouteCell cell={cell} />
                        ) : (
                          // Rendered rather than omitted: a missing cell is a defect in the payload, and
                          // saying so beats a blank that reads as "not applicable".
                          <div className="rounded-md border border-dashed border-border px-3 py-2 text-xs text-muted-foreground">
                            No answer was sent for this route. The delivery table is meant to be total,
                            so this is a defect rather than an absence.
                          </div>
                        )}
                      </td>
                    );
                  })}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/**
 * DeliverySourceReality is the per-language half — the one that genuinely varies by frontend.
 *
 * The ledger above says whether a route EXISTS for a change. This says whether the source route will
 * actually produce a diff for a given language, which is the question a reader with a Rust repository is
 * really asking.
 */
export function DeliverySourceReality({ view }: { view: ChangeDeliveryView }) {
  const languages = view.languages ?? [];
  const cells = view.source_cells ?? [];
  const changes: string[] = [];
  for (const c of cells) if (!changes.includes(c.change)) changes.push(c.change);

  return (
    <div className="overflow-hidden rounded-xl border border-border">
      <div className="overflow-x-auto">
        <table className="w-full min-w-[42rem] border-collapse text-sm">
          <caption className="sr-only">
            Whether the pull-request route produces a diff, per change and per language.
          </caption>
          <thead>
            <tr className="border-b border-border bg-surface">
              <th scope="col" className="px-3 py-2 text-left text-xs font-medium text-muted-foreground">
                Change
              </th>
              {languages.map((lang) => (
                <th key={lang} scope="col" className="px-2 py-2 text-left text-xs font-medium text-muted-foreground">
                  {lang}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {changes.map((change) => (
              <tr key={change} className="border-b border-border/60 last:border-0">
                <th scope="row" className="px-3 py-2 text-left text-sm font-medium">
                  {changeLabel(change)}
                </th>
                {languages.map((lang) => {
                  const cell = cells.find((c) => c.change === change && c.language === lang);
                  return (
                    <td key={lang} className="px-2 py-2">
                      <SourceMark cell={cell} />
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function SourceMark({ cell }: { cell?: DeliverySourceCellView }) {
  if (!cell) {
    return <span className="mono text-[11px] text-muted-foreground">no cell</span>;
  }
  if (cell.status === "delivers") {
    return (
      <span
        className="mono inline-flex rounded border border-primary/45 bg-primary/[0.07] px-1.5 py-0.5 text-[11px]"
        title={cell.note ?? undefined}
      >
        diff
      </span>
    );
  }
  if (cell.status === "varies-by-language") {
    // Some call-site forms in this language get a diff and some do not. Rendering it as a flat refusal
    // would tell a reader with a covered SDK that nothing can ship.
    return (
      <span
        className="mono inline-flex rounded border border-primary/30 px-1.5 py-0.5 text-[11px] text-foreground/85"
        title={cell.note ?? undefined}
      >
        some
      </span>
    );
  }
  const permanent = Boolean(cell.permanent);
  return (
    <span
      className={
        permanent
          ? "mono inline-flex rounded border border-border/50 px-1.5 py-0.5 text-[11px] text-muted-foreground"
          : "mono inline-flex rounded border border-dashed border-primary/50 px-1.5 py-0.5 text-[11px] text-foreground/75"
      }
      title={[cell.note, cell.missing_artifact ? `Missing: ${cell.missing_artifact}` : null]
        .filter(Boolean)
        .join(" — ")}
    >
      {permanent ? "never" : "not yet"}
    </span>
  );
}

/** The state legend, rendered from the wire so it cannot drift from the state machine. */
export function DeliveryStateLegend({ view }: { view: ChangeDeliveryView }) {
  const states = view.states ?? [];
  return (
    <Row>
      {states.map((s) => (
        <span key={s.id} className="flex items-baseline gap-1.5 text-xs">
          <Chip>{s.id}</Chip>
          <span className="text-muted-foreground">{s.label}</span>
        </span>
      ))}
    </Row>
  );
}
