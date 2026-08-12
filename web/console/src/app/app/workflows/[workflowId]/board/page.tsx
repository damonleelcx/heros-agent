import Link from "next/link";
import type { BoardView, CoverageView } from "@/lib/types.generated";
import { load } from "@/lib/view";
import {
  PageFrame,
  Section,
  Status,
  Chip,
  Empty,
  Failure,
  DataTable,
  Banner,
  Row,
  Card,
} from "@/components/primitives";
import { Disclosure } from "@/components/figure";
import { score, usd2, percent, integer, plural, NULL_VALUE } from "@/lib/format";
import { routes } from "@/lib/routes";
import { cx } from "@/lib/cx";
import { Leaderboard } from "./leaderboard";
import { ParetoChart } from "./pareto";

export const dynamic = "force-dynamic";

export default async function BoardPage({
  params,
  searchParams,
}: {
  params: Promise<{ workflowId: string }>;
  searchParams: Promise<{ profile?: string }>;
}) {
  const { workflowId } = await params;
  const id = decodeURIComponent(workflowId);
  const profile = (await searchParams).profile;
  const { outcome } = await load<BoardView>((paths) => paths.board(id, profile), [
    "state",
    "profile",
    "progress",
    "coverage",
    "spend",
  ]);
  return (
    <PageFrame
      eyebrow="Eval board"
      title={id}
      lede="How the variants compare, with everything that qualifies the comparison kept beside it."
      wide
    >
      {!outcome.ok ? (
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="workflow" />
      ) : (
        <BoardBody workflowId={id} board={outcome.data} />
      )}
    </PageFrame>
  );
}

function BoardBody({ workflowId, board }: { workflowId: string; board: BoardView }) {
  if (board.state === "error") {
    return (
      <Section title="This board could not be computed">
        <Failure
          kind="upstream"
          subject="board"
          error={board.error ?? "no reason was given"}
        >
          <p>
            No rows are shown, deliberately: a board that renders half its rows beside an error reads as
            mostly fine.
          </p>
        </Failure>
      </Section>
    );
  }

  const ranked = board.ranked ?? [];
  const disqualified = board.disqualified ?? [];
  const notes = board.notes ?? [];
  const unmeasured = board.unmeasured ?? [];
  const pareto = board.pareto ?? [];
  const profiles = board.profiles ?? [];

  return (
    <>
      <Section
        title="This board"
        aside={
          <>
            <Status value={board.state} />
            <span className="mono" title="the gate set that produced these pass/fail outcomes">
              gates {board.gate_set || NULL_VALUE}
            </span>
          </>
        }
      >
        <Row>
          <Chip variant="hash" title={board.eval_set_hash}>
            eval set {board.eval_set_hash ? board.eval_set_hash.slice(0, 12) : NULL_VALUE}
          </Chip>
          {/* `runs_enqueued` is rendered rather than claimed. The legacy page's help text asserts
              "0 new runs"; this shows the number the server actually reported, which turns a claim
              into evidence. */}
          <Chip tone={board.runs_enqueued === 0 ? "ok" : "warn"}>
            {integer(board.runs_enqueued)} new {plural(board.runs_enqueued, "run", "runs")} to render this
          </Chip>
        </Row>

        {/*
          P4-2: switching profile re-reads the board. It is a set of LINKS rather than a select and a
          submit button, for two reasons that both matter more than the keystroke saved: each profile
          becomes a shareable URL (R9), and the control works with no JavaScript at all.

          The sentence beside it is the product. A reader who believes re-ranking spends money will
          never try a second profile, and the whole point of having four is that you try them.
        */}
        <div className="flex flex-col gap-2">
          <p className="font-mono text-[10px] uppercase tracking-widest text-muted-foreground">
            Weight profile
          </p>
          <div className="flex flex-wrap gap-1.5">
            {profiles.map((name) => (
              <Link
                key={name}
                href={routes.board(workflowId, name)}
                aria-current={name === board.profile ? "true" : undefined}
                className={cx(
                  "rounded-lg border px-3 py-1 font-mono text-xs transition-colors",
                  name === board.profile
                    ? "border-primary/25 bg-primary/12 text-primary"
                    : "border-border text-muted-foreground hover:bg-muted/40 hover:text-foreground",
                )}
              >
                {name}
              </Link>
            ))}
          </div>
          <p className="hint">Re-ranks from the score cache. It enqueues no runs and costs nothing.</p>
        </div>
      </Section>

      <BoardBanners board={board} />

      {/* Each section below keeps its own empty state rather than the board hiding itself: a board
          with no variants and a board that failed to compute are different facts. */}
      <Section title="Leaderboard" id="leaderboard" aside={`${ranked.length} gate-passing under ${board.profile}`}>
        {ranked.length === 0 ? (
          disqualified.length > 0 ? (
            <Empty title="No variant passed every gate.">
              <p>
                The variants below were measured and then excluded. A gate is not a weighted preference —
                it cannot be traded against a good score.
              </p>
            </Empty>
          ) : (
            <Empty title="No variants on this board yet. Add a variant to compare." />
          )
        ) : (
          <Leaderboard rows={ranked} workflowId={workflowId} />
        )}
      </Section>

      {/* P4-22: disqualified variants in their OWN section. Ranking them last would say they came
          bottom; they were excluded, which is a different fact. */}
      {disqualified.length > 0 ? (
        <Section title="Disqualified" aside="excluded from the ranked order, not ranked last">
          <Leaderboard rows={disqualified} workflowId={workflowId} />
        </Section>
      ) : null}

      {/* P4-7: unmeasured variants are EXPLAINED, not silently absent. */}
      {unmeasured.length > 0 ? (
        <Section title="Not measured" aside={`${unmeasured.length} excluded`}>
          <Disclosure
            summary={`Why ${unmeasured.length} ${plural(unmeasured.length, "variant was", "variants were")} not measured`}
          >
            <DataTable
              caption="Variants whose runs did not complete, and the reason"
              columns={[
                { key: "variant", label: "Variant" },
                { key: "id", label: "Variant id" },
                { key: "reason", label: "Reason" },
              ]}
            >
              <tbody>
                {unmeasured.map((row) => (
                  <tr key={row.variant_id}>
                    <td>{row.label}</td>
                    {/* Surfaced per the surface-or-drop decision: an excluded variant that cannot be
                        looked up cannot be fixed, and this table is where a reader goes BECAUSE
                        something is missing. */}
                    <td className="mono">{row.variant_id}</td>
                    <td>{row.reason}</td>
                  </tr>
                ))}
              </tbody>
            </DataTable>
          </Disclosure>
        </Section>
      ) : null}

      {/*
        🔴 The cost axis is drawn only when cost and latency were MEASURED.

        This section used to render `ParetoChart` unconditionally. On a board assembled from linked runs
        the assembler left cost and latency at zero — deliberately, because it had declined to compute a
        cost frontier — and this chart rendered those zeros: a zero cost column beside a real quality, a
        marker sized by a latency nobody reported, an x-axis whose ticks went NEGATIVE because a
        zero-width domain pads outward, and a legend reading "nothing beats it on both quality and
        cost". Every one of those is a claim about a dimension the board did not have.

        `cost_latency` is the state that decision now travels as, so the refusal upstream stays a refusal
        on screen instead of becoming a confident drawing.

        (Written without currency literals on purpose: `TestNoPricedLiteralInPaymentUI` scans this file
        and cannot tell a prose example from a committed price. It caught the first draft of this
        comment, which is the fence working.)
      */}
      <Section
        title="Cost and quality"
        aside={
          board.cost_latency === "measured"
            ? "raw units — this frontier is not a ranking"
            : "cost and latency were not reported"
        }
      >
        {pareto.length === 0 ? (
          <Empty title="No gate-passing variants to plot." />
        ) : board.cost_latency === "measured" ? (
          <ParetoChart points={pareto} />
        ) : (
          <Empty title="No cost/quality frontier — cost and latency were not reported.">
            The highlighted rows above are the highest reported quality, not a frontier: nothing here was
            compared on cost. Link a run from a CLI that reports <code>cost_usd</code> and{" "}
            <code>latency_ms</code> and this becomes a real comparison.
          </Empty>
        )}
      </Section>

      <CoverageSection coverage={board.coverage} workflowId={workflowId} />
      <SpendSection board={board} />

      {notes.length > 0 ? (
        <Section title="Notes from the platform">
          {notes.map((note, index) => (
            <Banner key={index} tone="info" title={note} />
          ))}
        </Section>
      ) : null}
    </>
  );
}

function BoardBanners({ board }: { board: BoardView }) {
  const progress = board.progress;
  const unmeasured = board.unmeasured ?? [];
  return (
    <>
      {/* 🔴 P4-4. The all-tie banner. This is the board where a well-meaning UI does the most damage,
          and the sentence "the ranks are ordering, not evidence" is the product. */}
      {board.all_tie ? (
        <Banner tone="warn" title="No winner.">
          <p>
            Every variant&apos;s confidence interval overlaps every other&apos;s on this profile. The
            ranks below are an <strong>ordering, not evidence</strong> — reading row 1 as the winner
            would be reading something the measurement does not say.
          </p>
          <p className="hint">
            To resolve it: run more seeds, or strengthen the eval set so the differences it can detect
            are smaller than the differences you care about.
          </p>
        </Banner>
      ) : null}

      {/* P4-5: the partial banner, with the seed floor NAMED. The legacy board marks rows
          "provisional — below the seed floor" without ever saying what the floor is, so the reader
          cannot tell how far below. Surfaced per the surface-or-drop decision. */}
      {/* 🔴 A board assembled from LINKED runs has no plan and no seed floor, because this platform
          planned nothing and ran nothing — the eval happened on the customer's machine and only its
          result crossed. Every field of `progress` is 0 there, and this banner rendered them anyway:
          "0 of 0 units complete" and "intervals computed from fewer than 0 seeds — the seed floor".
          The second is not merely empty, it is unreadable: no interval is computed from fewer than
          zero seeds, so the sentence describes a set that cannot exist. Such a board is `partial` for
          an entirely different reason — some configurations could not be measured — and that reason
          has its own section below, which this now points at instead of inventing a progress bar. */}
      {board.state === "partial" ? (
        progress.units_planned > 0 || progress.seed_floor > 0 ? (
          <Banner tone="warn" title="This board is still filling in">
            <p>
              {integer(progress.units_completed)} of {integer(progress.units_planned)} units complete. The
              rows are readable, and intervals computed from fewer than{" "}
              <strong>{integer(progress.seed_floor)} seeds</strong> — the seed floor — are marked
              provisional.
            </p>
          </Banner>
        ) : (
          <Banner tone="warn" title="This board is incomplete">
            <p>
              {unmeasured.length > 0 ? (
                <>
                  {integer(unmeasured.length)}{" "}
                  {plural(unmeasured.length, "configuration is", "configurations are")} not on it. The rows
                  shown are readable; what is missing is listed under <strong>Not measured</strong> below,
                  with the reason for each.
                </>
              ) : (
                <>
                  Some of what this board would compare is missing. The rows shown are readable; nothing
                  here is estimated to fill a gap.
                </>
              )}
            </p>
            <p className="hint">
              This board was assembled from runs you linked, so it has no progress of its own to report —
              the evaluation ran on your machines and only its result crossed.
            </p>
          </Banner>
        )
      ) : null}

      {board.state === "complete" ? (
        <Banner tone="info" title="Every planned unit landed">
          <p>
            {integer(progress.units_completed)} of {integer(progress.units_planned)} units complete. This
            board is final for this profile and eval set.
          </p>
        </Banner>
      ) : null}
    </>
  );
}

function CoverageSection({ coverage, workflowId }: { coverage: CoverageView; workflowId: string }) {
  const dimensions = coverage.dimensions ?? [];
  const residual = coverage.residual ?? [];
  const reasons = coverage.reasons ?? [];

  if (!coverage.measured) {
    return (
      <Section title="Coverage">
        <Empty title="Coverage was not measured for this eval set.">
          <p>
            Without it this board cannot say whether the failing path was ever exercised — so a high
            score here is not evidence that the workflow is correct where it matters.
          </p>
        </Empty>
      </Section>
    );
  }

  return (
    <Section
      title="Coverage"
      aside={
        coverage.low_confidence ? (
          <span className="qualifier">
            <span className="qualifier__badge">low confidence</span>
            <span className="qualifier__copy">the eval set cannot support a strong claim</span>
          </span>
        ) : null
      }
    >
      {!coverage.met && coverage.stopped_because ? (
        <Banner tone="warn" title="Coverage stopped below its threshold">
          <p className="mono">{coverage.stopped_because}</p>
        </Banner>
      ) : null}

      <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
        {dimensions.map((dimension) => (
          <Card key={dimension.name} className="flex flex-col gap-2 p-4">
            <div className="flex items-center justify-between gap-3">
              <p className="text-xs text-muted-foreground">{dimension.name}</p>
              {dimension.vacuous ? null : (
                <span className="font-mono text-sm font-medium tabular-nums text-foreground">
                  {percent(dimension.achieved)}
                </span>
              )}
            </div>
            {dimension.vacuous ? (
              <p className="hint">not measurable — no obligations on this axis</p>
            ) : (
              <>
                {/*
                  The bar shows the ACHIEVED fraction and marks the TARGET on the same track, so
                  "94% of a 90% target" and "94% of a 99% target" do not draw identically. A bar
                  without its target is a number that looks like a verdict.
                */}
                <span
                  className={cx("meter__bar", !dimension.met && "meter__bar--short")}
                  role="img"
                  aria-label={`${percent(dimension.achieved)} of a ${percent(dimension.target)} target`}
                  style={
                    {
                      "--achieved": `${clampPercent(dimension.achieved)}%`,
                      "--target": `${clampPercent(dimension.target)}%`,
                    } as React.CSSProperties
                  }
                />
                <p className="caption">
                  target {percent(dimension.target)} · {integer(dimension.covered)} of{" "}
                  {integer(dimension.total)}
                </p>
                {(dimension.uncovered ?? []).length > 0 ? (
                  <Disclosure summary={`What is uncovered (${(dimension.uncovered ?? []).length})`}>
                    <ul className="flex list-none flex-col gap-1 p-0">
                      {(dimension.uncovered ?? []).map((item) => (
                        <li key={item} className="mono caption">
                          {item}
                        </li>
                      ))}
                    </ul>
                  </Disclosure>
                ) : null}
              </>
            )}
          </Card>
        ))}
      </div>

      <DataTable
        caption="Eval-set quality, as the platform measured it"
        columns={[
          { key: "k", label: "Measure" },
          { key: "v", label: "Value", numeric: true },
        ]}
      >
        <tbody>
          <tr>
            {/* 🔴 The denominator is a LINK (P30 task 1.14). Every score on this board is computed over
                it, and until this route existed the number was unopenable — "8 cases" doing load-bearing
                work with no way to ask which eight. The eval-set surface asserts that its list and this
                number agree, and reports an error rather than a table when they do not. */}
            <td>
              <Link
                className="text-primary underline-offset-2 hover:underline"
                href={routes.evalSet(workflowId)}
              >
                Cases
              </Link>
            </td>
            <td className="num">{integer(coverage.n_cases)}</td>
          </tr>
          <tr>
            <td>Rounds</td>
            <td className="num">{integer(coverage.iterations)}</td>
          </tr>
          <tr>
            <td>Difficulty</td>
            <td className="num">{coverage.difficulty_measured ? score(coverage.difficulty) : "not measured"}</td>
          </tr>
          <tr>
            <td>Diversity</td>
            <td className="num">{score(coverage.diversity)}</td>
          </tr>
          <tr>
            <td>
              Oracle coverage
              {coverage.n_indecisive > 0 ? (
                <span className="qualifier mt-1">
                  <span className="qualifier__badge">indecisive</span>
                  <span className="qualifier__copy">
                    {integer(coverage.n_indecisive)} {plural(coverage.n_indecisive, "oracle", "oracles")} look
                    measured and decide nothing
                  </span>
                </span>
              ) : null}
            </td>
            <td className="num">{percent(coverage.oracle_coverage)}</td>
          </tr>
          <tr>
            <td>References: gold / weak / none</td>
            <td className="num">
              {integer(coverage.n_gold)} / {integer(coverage.n_weak)} / {integer(coverage.n_none)}
            </td>
          </tr>
        </tbody>
      </DataTable>

      {reasons.length > 0 ? (
        <ul className="diagnostics">
          {reasons.map((reason, index) => (
            <li key={index}>{reason}</li>
          ))}
        </ul>
      ) : null}

      {residual.length > 0 ? (
        <Disclosure summary={`Residual obligations (${residual.length})`}>
          {/* 🔴 P4-38. This sentence's MEANING is load-bearing and is preserved deliberately. */}
          <p className="hint mb-3">
            These stay in the denominator. Dropping them would raise the percentage by deleting the
            evidence of failure.
          </p>
          <DataTable
            caption="Obligations the generator could not discharge, and why"
            columns={[
              { key: "id", label: "Obligation" },
              { key: "dim", label: "Dimension" },
              { key: "why", label: "Why" },
            ]}
          >
            <tbody>
              {residual.map((item) => (
                <tr key={item.id}>
                  <td className="mono">
                    {item.id} {item.unreachable ? <Chip tone="warn">unreachable</Chip> : null}
                  </td>
                  <td>{item.dimension}</td>
                  <td>{item.reason}</td>
                </tr>
              ))}
            </tbody>
          </DataTable>
        </Disclosure>
      ) : null}
    </Section>
  );
}

function SpendSection({ board }: { board: BoardView }) {
  const spend = board.spend;
  const byKind = spend.by_kind ?? {};
  const callsByKind = spend.calls_by_kind ?? {};
  const kinds = Object.keys(byKind).sort();
  const exhausted = spend.exhausted ?? [];
  const budget = spend.budget;

  return (
    <Section
      title="Spend"
      aside={spend.eval_run_id ? <span className="mono">eval run {spend.eval_run_id}</span> : null}
    >
      {exhausted.length > 0 ? (
        <Banner tone="info" title="Measurement stopped rather than overspending">
          <p>
            The {exhausted.join(", ")} {plural(exhausted.length, "cap was", "caps were")} reached. The
            board covers what was measured before the stop.
          </p>
        </Banner>
      ) : null}

      {kinds.length === 0 ? (
        <Empty title="No spend recorded." />
      ) : (
        <DataTable
          caption="What producing this board cost, by kind, against the caps that were set"
          columns={[
            { key: "kind", label: "Kind" },
            { key: "usd", label: "USD", numeric: true },
            { key: "calls", label: "Calls", numeric: true },
            { key: "cap", label: "Cap", numeric: true },
          ]}
        >
          <tbody>
            {kinds.map((kind) => (
              <tr key={kind}>
                <td>{kind}</td>
                <td className="num">{usd2(byKind[kind])}</td>
                <td className="num">{integer(callsByKind[kind])}</td>
                {/* Surfaced per the surface-or-drop decision: the legacy board says "budget cap
                    reached" and never says what the cap was, so a reader cannot tell how close they
                    are until they have hit it. */}
                <td className="num">{capFor(kind, budget)}</td>
              </tr>
            ))}
            <tr>
              <td>
                <strong>Total</strong>
              </td>
              <td className="num">
                <strong>{usd2(spend.total_usd)}</strong>
              </td>
              <td className="num" />
              <td className="num">
                {budget?.total_usd !== undefined && budget?.total_usd !== null ? usd2(budget.total_usd) : NULL_VALUE}
              </td>
            </tr>
          </tbody>
        </DataTable>
      )}
    </Section>
  );
}

function capFor(kind: string, budget: BoardView["spend"]["budget"]): string {
  if (!budget) return NULL_VALUE;
  if (kind === "judge") {
    return budget.judge_usd !== undefined && budget.judge_usd !== null ? usd2(budget.judge_usd) : NULL_VALUE;
  }
  if (kind === "generation") {
    return budget.generation_usd !== undefined && budget.generation_usd !== null
      ? usd2(budget.generation_usd)
      : NULL_VALUE;
  }
  return NULL_VALUE;
}

/** clampPercent keeps a fraction inside the track. A server value above 1 would draw outside the bar. */
function clampPercent(fraction: number): number {
  return Math.max(0, Math.min(1, fraction)) * 100;
}
