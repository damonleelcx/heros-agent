import Link from "next/link";
import { BarChart3, FlaskConical } from "lucide-react";
import type { ScorecardView, NodeRow, DiagnosisCard, AblationCard } from "@/lib/types.generated";
import { load } from "@/lib/view";
import { requireSession } from "@/lib/session";
import { recordVisit, routes } from "@/lib/subjects";
import {
  PageFrame,
  Section,
  Status,
  Chip,
  Empty,
  Failure,
  DataTable,
  Banner,
  Value,
  Stat,
  Stats,
  Row,
  Card,
} from "@/components/primitives";
import { score, percent, usd2, ms, integer, plural } from "@/lib/format";

export const dynamic = "force-dynamic";

export default async function ScorecardPage({ params }: { params: Promise<{ variantId: string }> }) {
  const session = await requireSession();
  const { variantId } = await params;
  const id = decodeURIComponent(variantId);
  recordVisit(session, { kind: "variant", id, label: id, href: routes.scorecard(id) });
  const { outcome } = await load<ScorecardView>((paths) => paths.scorecard(id), [
    "config_hash",
    "eval_set_hash",
    "workflow_id",
  ]);
  return (
    <PageFrame
      eyebrow="Variant scorecard"
      title={id}
      lede="Why this variant scored what it scored — per node, per failure cluster, and what an ablation actually showed."
      wide
    >
      {!outcome.ok ? (
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="scorecard" />
      ) : (
        <Body view={outcome.data} />
      )}
    </PageFrame>
  );
}

function Body({ view }: { view: ScorecardView }) {
  const nodes = view.nodes ?? [];
  const clusters = view.clusters ?? [];
  const diagnoses = view.diagnoses ?? [];
  const ablations = view.ablations ?? [];

  /*
   * 🔴 `overall` and `analyst` are guarded for the same reason the lists above are.
   *
   * Found by the §9.8 acceptance test, not by a build: given a 200 whose body did not carry `overall`,
   * `view.overall.n_cases` threw during render, Next served its error page, and the whole view
   * disappeared — no frame, no heading, no subject. A reader sees a blank page and cannot even confirm
   * which variant they opened, which is precisely what FR27 exists to prevent.
   *
   * The other views survive the same input because they already reach for `?? []`. This one dereferenced
   * a nested object directly, and an optional-looking field in a generated type is not a runtime
   * guarantee — the type describes what the platform INTENDS to send.
   *
   * Absent metrics render as absent, which is honest: it is a different fact from "measured zero", and
   * the formatter's null placeholder already says so.
   */
  const overall = view.overall ?? null;
  const analyst = view.analyst ?? null;

  return (
    <>
      <Section
        title="This variant"
        aside={
          <Link className="text-primary underline underline-offset-2" href={routes.board(view.workflow_id)}>
            the board it was ranked on
          </Link>
        }
      >
        <Row>
          <Status value={view.state} />
          <Chip variant="hash" title={view.config_hash}>
            config {view.config_hash.slice(0, 12)}
          </Chip>
          <Chip variant="hash" title={view.eval_set_hash}>
            eval set {view.eval_set_hash.slice(0, 12)}
          </Chip>
        </Row>
        {view.message ? <p className="hint">{view.message}</p> : null}
        {view.partial ? (
          <Banner tone="warn" title="This scorecard is partial">
            <p>Not every case has been attributed. What is below covers the cases that were.</p>
          </Banner>
        ) : null}
        {view.analyst_uncalibrated && analyst?.present ? (
          <Banner tone="warn" title="The analyst behind these diagnoses is uncalibrated">
            <p>
              Its agreement with human labels is {score(analyst.agreement)} over {integer(analyst.n_human)}{" "}
              {plural(analyst.n_human, "label", "labels")}, against a floor of {score(analyst.floor)}. Treat
              every diagnosis below as an opinion, not as evidence.
            </p>
          </Banner>
        ) : null}
        {view.unclassified_node_count > 0 ? (
          <p className="hint">
            {integer(view.classified_node_count)} of{" "}
            {integer(view.classified_node_count + view.unclassified_node_count)} nodes carry a pattern.
            Attribution for an unclassified node is still measured; it simply cannot be explained in
            terms of a pattern.
          </p>
        ) : null}
      </Section>

      <Section title="Overall" aside={overall ? `${integer(overall.n_cases)} cases` : undefined}>
        {overall === null ? (
          <Empty title="The platform returned no overall metrics for this variant.">
            <p>
              The per-node attribution below, where present, is unaffected — it is measured separately.
            </p>
          </Empty>
        ) : (
          <Stats fill>
            <Stat label="Task success" value={percent(overall.task_success)} note="across the eval set" />
            <Stat label="Failing cases" value={integer(overall.n_failing)} note={`of ${integer(overall.n_cases)}`} />
            <Stat label="Cost" value={usd2(overall.cost_usd)} unit="USD" note="per run" />
            <Stat label="Latency" value={ms(overall.latency_ms)} note="end to end" />
          </Stats>
        )}
      </Section>

      <Section title="Where the failures are" aside={`${nodes.length} ${plural(nodes.length, "node", "nodes")}`}>
        {nodes.length === 0 ? (
          <Empty title="No per-node attribution was produced for this variant." />
        ) : (
          <DataTable
            caption="Each node's share of failure, and of cost and latency"
            columns={[
              { key: "node", label: "Node" },
              { key: "share", label: "Share of failure", numeric: true },
              { key: "first", label: "First divergence", numeric: true },
              { key: "cost", label: "Cost share", numeric: true },
              { key: "latency", label: "Latency share", numeric: true },
            ]}
          >
            <tbody>
              {nodes.map((node) => (
                <NodeRowView key={node.node_id} node={node} />
              ))}
            </tbody>
          </DataTable>
        )}
      </Section>

      {clusters.length > 0 ? (
        <Section title="Failure clusters" aside={`${clusters.length} identified`}>
          <div className="grid gap-4 lg:grid-cols-3">
            {clusters.map((cluster) => (
              <Card key={cluster.signature} className="flex flex-col gap-2 p-4">
                <div className="flex items-start justify-between gap-2">
                  <span className="text-sm font-medium text-foreground">{cluster.label}</span>
                  <span className="shrink-0 rounded border border-bad/25 bg-bad/10 px-2 py-0.5 font-mono text-xs tabular-nums text-bad">
                    {integer(cluster.size)}
                  </span>
                </div>
                <p className="caption mono">{cluster.signature}</p>
                <p className="caption">
                  representative case <span className="mono">{cluster.representative_case_id}</span>
                </p>
              </Card>
            ))}
          </div>
        </Section>
      ) : null}

      {/*
        The two panels below are the scorecard's whole argument, and they are drawn as two visibly
        different objects on purpose: one is what a model THINKS, the other is what was RE-RUN. A
        surface that renders them alike invites a reader to act on a hypothesis as if it were a
        measurement, which is the most expensive mistake this product can cause.
      */}
      {diagnoses.length > 0 ? (
        <section className="flex flex-col gap-4 rounded-xl border border-warn/20 bg-warn/4 p-5">
          <div className="flex flex-wrap items-center gap-2">
            <FlaskConical className="size-4 shrink-0 text-warn" aria-hidden="true" />
            <h2 className="font-display text-lg font-normal text-foreground/80">What the analyst believes</h2>
            <span className="rounded border border-warn/25 bg-warn/10 px-2 py-0.5 font-mono text-[10px] uppercase tracking-widest text-warn">
              hypothesis — not a measurement
            </span>
          </div>
          <p className="hint">
            A diagnosis proposes; verification decides. Nothing here has been re-run to test it — the
            ablations below are the part that was.
          </p>
          <div className="flex flex-col gap-3">
            {diagnoses.map((diagnosis, index) => (
              <DiagnosisCardView key={`${diagnosis.code}-${index}`} diagnosis={diagnosis} />
            ))}
          </div>
        </section>
      ) : null}

      {ablations.length > 0 ? (
        <section className="flex flex-col gap-4 rounded-xl border border-primary/20 bg-primary/4 p-5">
          <div className="flex flex-wrap items-center gap-2">
            <BarChart3 className="size-4 shrink-0 text-primary" aria-hidden="true" />
            <h2 className="font-display text-lg font-normal text-foreground">What was actually re-run</h2>
            <span className="rounded border border-primary/25 bg-primary/10 px-2 py-0.5 font-mono text-[10px] uppercase tracking-widest text-primary">
              measurement · with intervals
            </span>
          </div>
          <DataTable
            caption="Each ablation's measured delta and the interval it was measured to"
            columns={[
              { key: "node", label: "Node" },
              { key: "metric", label: "Metric" },
              { key: "delta", label: "Delta with interval", numeric: true },
              { key: "seeds", label: "Seeds", numeric: true },
              { key: "verdict", label: "Verdict" },
            ]}
          >
            <tbody>
              {ablations.map((ablation, index) => (
                <AblationRow key={`${ablation.node_id}-${ablation.metric}-${index}`} ablation={ablation} />
              ))}
            </tbody>
          </DataTable>
        </section>
      ) : null}

      {view.read_only ? (
        <Banner tone="info" title="This scorecard explains; it does not change anything">
          <p>
            Nothing here applies a change. Where the platform proposes one, it appears on{" "}
            <Link className="text-primary underline underline-offset-2" href={routes.proposals(view.workflow_id)}>
              this workflow&apos;s proposals
            </Link>{" "}
            with a verified delta behind it.
          </p>
        </Banner>
      ) : null}
    </>
  );
}

function NodeRowView({ node }: { node: NodeRow }) {
  const flags = node.classified ? [] : ["low-confidence"];
  return (
    <tr>
      <td>
        <span className="mono text-sm">{node.node_id}</span>
        {node.pattern ? (
          <span className="ml-2">
            <Chip tone="info">{node.pattern}</Chip>
          </span>
        ) : null}
        {(node.bottleneck_dimensions ?? []).length > 0 ? (
          <p className="caption mt-1">bottleneck: {(node.bottleneck_dimensions ?? []).join(", ")}</p>
        ) : null}
      </td>
      <td className="num">
        <Value flags={flags}>
          <span className="mono">{percent(node.failure_share)}</span>
        </Value>
      </td>
      <td className="num mono">{integer(node.first_divergence_count)}</td>
      <td className="num mono">{percent(node.mean_cost_share)}</td>
      <td className="num mono">{percent(node.mean_latency_share)}</td>
    </tr>
  );
}

function DiagnosisCardView({ diagnosis }: { diagnosis: DiagnosisCard }) {
  const flags = [
    "unverified",
    ...(diagnosis.calibrated ? [] : ["uncalibrated"]),
    ...(diagnosis.low_confidence ? ["low-confidence"] : []),
  ];
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-border bg-background p-4">
      <div className="flex flex-wrap items-center gap-2">
        <Chip>{diagnosis.code}</Chip>
        <span className="mono text-xs">{diagnosis.node_id}</span>
        <Chip tone="info">{diagnosis.source}</Chip>
        {diagnosis.analyst_flagged ? <Chip tone="warn">analyst flagged</Chip> : null}
      </div>
      <p className="text-sm italic leading-relaxed text-muted-foreground">{diagnosis.description}</p>
      <Value flags={flags}>
        <span className="caption">
          confidence {score(diagnosis.confidence)} · agreement {score(diagnosis.agreement)} over{" "}
          {integer(diagnosis.n_human)} human {plural(diagnosis.n_human, "label", "labels")}
        </span>
      </Value>
      {(diagnosis.evidence_case_ids ?? []).length > 0 ? (
        <p className="caption mono">evidence: {(diagnosis.evidence_case_ids ?? []).join(", ")}</p>
      ) : null}
    </div>
  );
}

function AblationRow({ ablation }: { ablation: AblationCard }) {
  const inconclusive = ablation.verdict !== "significant";
  return (
    <tr>
      <td className="mono">{ablation.node_id}</td>
      <td>{ablation.metric}</td>
      <td className="num">
        <Value flags={inconclusive ? ["low-confidence"] : []}>
          <span className="mono">
            {score(ablation.delta_mean)}{" "}
            <span className="caption">
              ± [{score(ablation.ci_low)}, {score(ablation.ci_high)}]
            </span>
          </span>
        </Value>
      </td>
      <td className="num mono">{integer(ablation.n_seeds)}</td>
      <td>
        <Status value={ablation.verdict} />
      </td>
    </tr>
  );
}
