import Link from "next/link";
import { Section } from "./primitives";

/**
 * AxisProjectionPanel renders `coverage × your nodes` for ONE axis (P29 §5.10).
 *
 * # Where it sits, and what it must not replace
 *
 * BESIDE the worked examples, never instead of them. Those examples carry the transform engine's own
 * verbatim sentences, and they are what makes a refusal legible: a reader meeting one for the first time
 * needs to see the APPLIED case next to it to read "declined" as a boundary rather than as a failure.
 * That is the stated reason those pages exist and it does not stop being true when the page also has
 * real rows. Per `ui-redesign-feature-and-visual-consistency`, a redesign ADDS; it does not remove —
 * this phase adds panels and removes none.
 *
 * So the panel carries its OWN heading, its OWN denominator, and says outright that it is live data —
 * because a page that mixes an example with a fact and labels neither has made both unreadable.
 *
 * # 🔴 The fourth state
 *
 * `not-reported` is not a polite way of saying "no". The platform was never told about that node, and
 * the panel says so and names the command that would tell it. It gets its own design token
 * (`--not-reported`), separate from `--unknown`: "you did not tell us" is a boundary the customer chose,
 * and `--unknown` means "we could not determine this" — showing an egress decision in an outage's colour
 * on the screen where somebody is deciding whether to opt in would be the wrong sentence in the right
 * place.
 *
 * # Three transport treatments, and a business state is never one of them
 *
 * `not-mounted`, `read-failed` and `not-reported` are three different screens here. §5.11: no 404 is
 * mapped to a business state — a workflow the platform has no structure for answers 200 and says
 * `not-reported`, so a genuine transport failure keeps its own meaning.
 */

export type ProjectionTotals = {
  axis: string;
  applies: number;
  refused: number;
  not_applicable: number;
  not_reported: number;
  nodes: number;
  stale_excluded: number;
};

export type ProjectionCell = {
  node_id: string;
  axis: string;
  state: "applies" | "refused" | "not-applicable" | "not-reported";
  cause?: string;
  owner?: string;
  stale?: boolean;
};

export type ProjectionNode = {
  node_id: string;
  symbol?: string;
  file?: string;
  language?: string;
  cells: ProjectionCell[];
};

export type Projection = {
  workflow_id: string;
  source_revision?: string;
  reported_at?: string;
  coverage_version: string;
  reported_coverage_version?: string;
  stale: boolean;
  axes: string[];
  totals: ProjectionTotals[];
  nodes: ProjectionNode[];
  node_count: number;
  verdicts_reported: number;
};

export type ProjectionOutcome =
  | { state: "ok"; projection: Projection }
  | { state: "not-reported"; workflow_id?: string; detail?: string; fill_with?: string }
  | { state: "read-failed"; detail?: string }
  | { state: "not-mounted"; detail?: string };

/** STATE_COPY names each cell state in the reader's terms, with the token that carries it. */
const STATE_COPY: Record<ProjectionCell["state"], { word: string; token: string }> = {
  applies: { word: "applies", token: "var(--color-ok)" },
  refused: { word: "refused", token: "var(--color-warn)" },
  "not-applicable": { word: "not applicable", token: "var(--color-neutral)" },
  "not-reported": { word: "not reported", token: "var(--color-not-reported)" },
};

/** CAUSE_COPY renders a refusal class from the CONSOLE's own catalogue, never from the wire. */
const CAUSE_COPY: Record<string, string> = {
  "not-expressible-at-a-call-site":
    "The value does not exist in source until run time, in any language. Nobody can close this.",
  "call-site-cannot-carry-it":
    "This call site's own source has nothing to change — unpacked arguments, a run-time-assembled list, or an SDK that binds before the call.",
  "no-materializer-for-this-language":
    "The platform has not landed the rewriter for this language yet. This one is ours.",
};

function Count({ label, value, of, token }: { label: string; value: number; of: number; token: string }) {
  return (
    <div className="flex flex-col gap-1 rounded-xl border border-border bg-card p-4">
      <span className="flex items-center gap-2 text-xs text-muted-foreground">
        <span aria-hidden="true" className="size-2 rounded-full" style={{ background: token }} />
        {label}
      </span>
      {/*
        🔴 §5.7 — the DENOMINATOR is rendered with every count, never a bare proportion. "68% covered"
        over three nodes and over four hundred are the same string and different facts, and the reader
        is the one who has to tell them apart.
      */}
      <span className="mono text-lg text-foreground">
        {value}
        <span className="text-sm text-muted-foreground"> / {of}</span>
      </span>
    </div>
  );
}

export function AxisProjectionPanel({
  axis,
  outcome,
  workflowHref,
}: {
  axis: string;
  outcome: ProjectionOutcome;
  /** workflowHref, when present, links the reader to the workflow whose nodes these are. */
  workflowHref?: string;
}) {
  return (
    <Section
      title="Your nodes, crossed with this table"
      aside="live data for this organization"
      id="projection"
    >
      {outcome.state === "not-mounted" ? (
        <p className="hint">
          This deployment does not accept workflow structure, so there is nothing to cross this table
          with. Nothing failed — the capability is not served here.
        </p>
      ) : outcome.state === "read-failed" ? (
        <p className="hint">
          Your reported structure could not be read. This is <strong>not</strong> the same as having sent
          none: nothing has been lost, and retrying is safe.
          {outcome.detail ? <span className="mono block">{outcome.detail}</span> : null}
        </p>
      ) : outcome.state === "not-reported" ? (
        /*
         * 🔴 The FOURTH state at the page level, and the one this whole phase exists for. The table
         * above is a fact about the BUILD and it is correct; it says nothing about the reader's nodes
         * because the reader has not sent any. That is a boundary they chose, and the honest screen
         * names the command that would change it rather than showing an empty table that reads as
         * "nothing here applies to you".
         */
        <div className="flex flex-col gap-3 rounded-xl border border-dashed border-border bg-card/50 p-5">
          <p className="text-sm text-foreground">
            The platform has not been told this organization&rsquo;s workflow structure, so it will not
            say anything about your nodes.
          </p>
          <p className="hint">
            {outcome.detail ??
              "The platform computes no verdict it was not sent: it knows your nodes' language and could guess the rest, and a guess that is right most of the time is the worst thing to put under this heading."}
          </p>
          <p className="hint">
            Send it with <code className="mono">{outcome.fill_with ?? "heros link --with-ir"}</code>. The
            structure carries symbols, file and line spans, models, context policies and tool counts — no
            prompt text, no source, no keys.
          </p>
        </div>
      ) : (
        <ProjectionBody axis={axis} projection={outcome.projection} workflowHref={workflowHref} />
      )}
    </Section>
  );
}

function ProjectionBody({
  axis,
  projection,
  workflowHref,
}: {
  axis: string;
  projection: Projection;
  workflowHref?: string;
}) {
  const totals = projection.totals.find((t) => t.axis === axis);
  const rows = projection.nodes
    .map((n) => ({ node: n, cell: n.cells.find((c) => c.axis === axis) }))
    .filter((r): r is { node: ProjectionNode; cell: ProjectionCell } => Boolean(r.cell));

  if (!totals) {
    return (
      <p className="hint">
        This build&rsquo;s coverage table carries no row for the <span className="mono">{axis}</span>{" "}
        axis, so there is nothing to cross. That is a fact about the build, not about your code.
      </p>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <p className="hint">
        {projection.node_count} node{projection.node_count === 1 ? "" : "s"} reported for{" "}
        {workflowHref ? (
          <Link className="underline" href={workflowHref}>
            {projection.workflow_id}
          </Link>
        ) : (
          <span className="mono">{projection.workflow_id}</span>
        )}
        {projection.reported_at ? <> · reported {projection.reported_at}</> : null}
        {" · "}
        <span className="mono">{projection.verdicts_reported}</span> carry verdicts computed on your
        machine by the transform engine.
      </p>

      {projection.stale ? (
        /*
         * 🔴 §5.6 — STALE. The verdicts were computed against a coverage table this build does not
         * carry, so the counts EXCLUDE them and both versions are shown. Two builds' answers mixed into
         * one number is a number nobody can act on, and the reader deserves to see why.
         */
        <div className="rounded-xl border border-[color:var(--color-warn)] bg-card p-4">
          <p className="text-sm text-foreground">These verdicts are from a different coverage table.</p>
          <p className="hint">
            Reported against <span className="mono">{projection.reported_coverage_version}</span>; this
            build carries <span className="mono">{projection.coverage_version}</span>. The rows are shown
            and are <strong>excluded from every count</strong> — re-run{" "}
            <code className="mono">heros link --with-ir</code> with a current CLI to refresh them.
          </p>
        </div>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Count label="applies" value={totals.applies} of={totals.nodes} token={STATE_COPY.applies.token} />
        <Count label="refused" value={totals.refused} of={totals.nodes} token={STATE_COPY.refused.token} />
        <Count
          label="not applicable"
          value={totals.not_applicable}
          of={totals.nodes}
          token={STATE_COPY["not-applicable"].token}
        />
        <Count
          label="not reported"
          value={totals.not_reported}
          of={totals.nodes}
          token={STATE_COPY["not-reported"].token}
        />
      </div>
      {totals.stale_excluded > 0 ? (
        <p className="hint">
          {totals.stale_excluded} cell{totals.stale_excluded === 1 ? "" : "s"} excluded as stale. The four
          counts plus the exclusions always equal the denominator.
        </p>
      ) : null}

      <ul className="divide-y divide-border/50 overflow-hidden rounded-xl border border-border">
        {rows.map(({ node, cell }) => {
          const copy = STATE_COPY[cell.state];
          return (
            <li className="flex items-start gap-3 px-4 py-3" key={node.node_id}>
              <span
                aria-hidden="true"
                className="mt-1.5 size-2 shrink-0 rounded-full"
                style={{ background: copy.token }}
              />
              <span className="min-w-0 flex-1">
                <span className="mono block truncate text-sm text-foreground">
                  {node.symbol || node.node_id}
                </span>
                <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                  {[node.file, node.language].filter(Boolean).join(" · ") || node.node_id}
                </span>
                {cell.state === "refused" && cell.cause ? (
                  <span className="mt-1 block text-xs text-muted-foreground">
                    {/*
                      The SENTENCE is rendered from this console's own catalogue, keyed by the stable
                      identifier that crossed the boundary. A CLI three versions old cannot put stale
                      copy on a paid surface, and the engine's own `Detail` — which names the customer's
                      arguments and symbols — has no field on the wire at all.
                    */}
                    {CAUSE_COPY[cell.cause] ?? cell.cause}
                    {cell.owner ? (
                      <span className="text-muted-foreground/70"> · closed by {cell.owner}</span>
                    ) : null}
                  </span>
                ) : null}
                {cell.state === "not-reported" ? (
                  <span className="mt-1 block text-xs text-muted-foreground">
                    The platform was not told what this axis does at this call site. Re-run with{" "}
                    <code className="mono">--with-ir</code> from a current CLI.
                  </span>
                ) : null}
              </span>
              <span className="shrink-0 text-xs" style={{ color: copy.token }}>
                {copy.word}
              </span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
