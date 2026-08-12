import type {
  GraphView,
  ViewNode,
  ViewEdge,
  ViewRegion,
  ViewLabel,
  ViewTopology,
  ViewAgent,
  Composition,
  CompositionPattern,
} from "@/lib/types.generated";
import { load } from "@/lib/view";
import {
  PageFrame,
  Section,
  Chip,
  Empty,
  Failure,
  DataTable,
  Value,
  Stat,
  Stats,
  Card,
  Banner,
} from "@/components/primitives";
import { Figure } from "@/components/figure";
import { percent, integer, plural } from "@/lib/format";
import { cx } from "@/lib/cx";

export const dynamic = "force-dynamic";

/**
 * The classified graph.
 *
 * # Why the layout is computed here and is deterministic
 *
 * `layer` and `order` come from the platform and are turned into pixels by arithmetic, not by a force
 * simulation. A graph that settles somewhere slightly different on every load cannot be compared to
 * the screenshot in yesterday's review, and "the node moved" becomes a question nobody can answer.
 * Fixed positions also mean the SVG is identical between server and client renders.
 *
 * # Why every colour is a token and none is a literal
 *
 * The design bundle's graph writes `rgba(46,207,168,0.45)` into stroke attributes. That is a dark-theme
 * colour on a permanently dark canvas. Here the canvas is a token that flips with the palette and every
 * stroke resolves through one too — so the graph is legible in the light theme instead of being a
 * black rectangle in the middle of a white page.
 */
const COL = 240;
const ROW = 132;
const BOX_W = 188;
const BOX_H = 76;
const PAD = 28;

export default async function GraphPage({ params }: { params: Promise<{ workflowId: string }> }) {
  const { workflowId } = await params;
  const id = decodeURIComponent(workflowId);
  const { outcome } = await load<GraphView>((paths) => paths.graph(id), ["ir_version", "taxonomy_version"]);
  return (
    <PageFrame
      eyebrow="Graph"
      title={id}
      lede="What the classifier found in this workflow, and — where it found nothing — why. Node positions are computed and fixed, so this drawing is the same one you saw yesterday."
      wide
    >
      {!outcome.ok ? (
        <Failure kind={outcome.kind} error={outcome.error} denial={outcome.denial} subject="workflow" />
      ) : (
        <GraphBody view={outcome.data} />
      )}
    </PageFrame>
  );
}

function GraphBody({ view }: { view: GraphView }) {
  const nodes = view.nodes ?? [];
  const edges = view.edges ?? [];
  const regions = view.regions ?? [];
  const unclassified = view.unclassified ?? [];
  const diagnostics = view.diagnostics ?? [];
  // 🔴 NULLABLE, and it is not defensive habit. A 200 whose body does not carry a composition is a
  // FOURTH state on this console — not a crash and not a zero — and this page dereferenced it
  // unguarded, which turned a pre-P30 response into a server-rendered error page for the whole route.
  // Found by the acceptance suite, whose graph fixture is deliberately a body from before this field
  // existed.
  //
  // 🚫 It is NOT defaulted to an empty composition. `{nodes_covered: 0}` would render "0 of 0 nodes
  // covered" — a measurement nobody made — which is the same fabrication `uncovered_nodes` avoids on
  // the eval-set page for the same reason.
  const composition = view.composition ?? null;
  const agent = view.agent ?? null;

  return (
    <>
      <Section
        title="This graph"
        aside={
          <span className="mono">
            ir {view.ir_version} · taxonomy {view.taxonomy_version}
          </span>
        }
      >
        <Stats>
          <Stat label="Nodes" value={integer(nodes.length)} note="call sites found" />
          {/* 🔴 TASK 8.4 — a mixed count reports the TOTAL and the INFERRED PORTION, never one
              undifferentiated number. `19 edges` over a graph where four were proposed by a model is
              a number a reader takes as measured, and the arithmetic is how a hypothesis gets promoted
              to a fact without anybody claiming it. When nothing is inferred the note is the plain one:
              the split appears where there is a split, and does not nag where there is not. */}
          <Stat
            label="Edges"
            value={integer(edges.length)}
            note={
              composition && composition.edges_inferred > 0
                ? `dependencies mapped — ${integer(composition.edges_inferred)} of them inferred, not measured`
                : "dependencies mapped"
            }
          />
          {/* The sentence is resolved on the platform (patternclassifier.llmCallsNote). It used to be
              computed here from the count alone, which got the most common case exactly backwards:
              zero calls over a graph with zero labels read as "fully rule-covered". */}
          <Stat
            label={`LLM fallback ${plural(view.llm_calls, "call", "calls")}`}
            value={integer(view.llm_calls)}
            note={view.llm_calls_note}
          />
        </Stats>
      </Section>

      <Section title="Structure">
        {nodes.length === 0 ? (
          <Empty title="This workflow has no nodes in its IR.">
            <p>
              Discovery has not produced a graph for it yet, or the revision that was classified had no
              call sites.
            </p>
          </Empty>
        ) : view.topology ? (
          /* 🔴 Nodes and no edges. The positional drawing is WITHHELD rather than drawn, because a
             column of disconnected boxes looks like a finding — "these calls are independent" — and
             for a syntactic frontend that is a claim nobody made. The statement and its cause take
             its place; the node list stays, as a table, so nothing that was readable stops being
             readable. */
          <NoTopology topology={view.topology} nodes={nodes} edges={edges} />
        ) : (
          <Figure
            title="Nodes positioned by layer and order, with region rectangles beneath them"
            alt={textAlternative(view)}
            table={<GraphTable nodes={nodes} edges={edges} />}
          >
            <div className="graph-scroll">
              <GraphSVG nodes={nodes} edges={edges} regions={regions} unclassified={unclassified} />
            </div>
          </Figure>
        )}
        {view.topology ? null : <GraphLegend />}
      </Section>

      <CompositionSection composition={composition} agent={agent} />

      <Section title="Patterns" aside={`${regions.length} labelled · ${unclassified.length} unlabelled`}>
        {regions.length === 0 && unclassified.length === 0 ? (
          <Empty title="This graph was not partitioned into regions at all.">
            {/* Not the same as "regions exist and none is labelled" — that case renders cards below,
                each naming its own cause. This is the case where there was nothing to partition. */}
            <p>
              That is a state, not an error, and it does not mean the workflow implements no patterns.
            </p>
            <p>
              {view.topology
                ? "A region is a connected subgraph, and this graph has no edges — so there is nothing " +
                  "for the classifier to partition. The statement above says why there are no edges."
                : "The classifier produced neither a labelled region nor an unlabelled one, which means " +
                  "the graph carries no nodes the partitioner could group."}
            </p>
          </Empty>
        ) : (
          <div className="grid gap-4 lg:grid-cols-2">
            {regions.map((region) => (
              <RegionCard key={region.subgraph_id} region={region} />
            ))}
            {unclassified.map((region) => (
              <UnclassifiedCard key={region.subgraph_id} region={region} />
            ))}
          </div>
        )}
      </Section>

      {diagnostics.length > 0 ? (
        <Section title="Why a label was rejected" aside={`${diagnostics.length} recorded`}>
          <ul className="diagnostics">
            {diagnostics.map((d, index) => (
              <li key={index}>
                <span className="mono text-foreground/70">[{d.stage}]</span>{" "}
                <span className="mono">
                  {d.subgraph_ref ?? ""} {d.raw_pattern ?? ""}
                  {d.source ? ` (${d.source})` : ""}
                </span>{" "}
                — {d.reason}
              </li>
            ))}
          </ul>
        </Section>
      ) : null}
    </>
  );
}

/**
 * The zero-edge state: the statement, its cause, and the node list.
 *
 * Nothing here is written in this file. `sentence` is assembled on the platform from the discovery
 * report's frontend records, and the table below it names each contributing frontend with the analysis
 * kind that frontend declares. The day a frontend learns to emit edges, the sentence changes with no
 * edit here — which is the difference between an explanation and a caption.
 */
function NoTopology({
  topology,
  nodes,
  edges,
}: {
  topology: ViewTopology;
  nodes: ViewNode[];
  edges: ViewEdge[];
}) {
  const frontends = topology.frontends ?? [];
  return (
    <div className="flex flex-col gap-4">
      <Banner tone="warn" title="This graph has no edges, so no dependency drawing is shown.">
        <p>{topology.sentence}</p>
        <p>
          Everything on this console that reads topology — the pattern detectors, the metric sets they
          dispatch, cost attribution and proposal admissibility — has nothing to read for this workflow.
          That is why those surfaces are sparse, and it is one cause rather than several.
        </p>
      </Banner>
      {frontends.length > 0 ? (
        <DataTable
          caption="The frontends that produced this graph, and how deeply each analyses"
          columns={[
            { key: "language", label: "Language" },
            { key: "kind", label: "Analysis" },
            { key: "nodes", label: "Nodes", numeric: true },
            { key: "edges", label: "Edges", numeric: true },
          ]}
        >
          <tbody>
            {frontends.map((f) => (
              <tr key={f.language}>
                <td className="mono">{f.language}</td>
                <td>
                  <Chip tone={f.analysis_kind === "typed" ? "ok" : "unknown"}>{f.analysis_kind}</Chip>
                </td>
                <td className="num">{integer(f.nodes)}</td>
                <td className="num">{integer(f.edges)}</td>
              </tr>
            ))}
          </tbody>
        </DataTable>
      ) : null}
      {/* The node list survives the withheld drawing. A reader who could see the call sites yesterday
          can still see them, and the reason the picture is gone is stated above rather than implied by
          its absence. */}
      <GraphTable nodes={nodes} edges={edges} />
    </div>
  );
}

/**
 * What this workflow is made of, and what the agent contributed (P30 tasks 8.1, 8.2, 8.6–8.8).
 *
 * # Why a composition and not a workflow-level label
 *
 * The classifier deliberately refuses to name the whole workflow, because a label is the metric-set
 * DISPATCHER and a graph containing both a router and a RAG pipeline needs two metric sets. That
 * refusal left a real question unanswered — a reader opening this page wants to know what they are
 * looking at — and a composition answers it by ENUMERATING rather than collapsing. Two patterns read as
 * two patterns; one pattern reads as a composition of one, not as a workflow-level label restated.
 *
 * Everything here is computed on the platform. This component branches on data and renders sentences it
 * was handed, so a second copy of "how do we know this" cannot grow in TypeScript.
 */
function CompositionSection({
  composition,
  agent,
}: {
  composition: Composition | null;
  agent: ViewAgent | null;
}) {
  const patterns = composition?.patterns ?? [];
  return (
    <Section
      title="What this workflow is made of"
      aside={
        /* 🔴 TASK 8.4 again, on the figure a reader is most likely to quote. `12 of 19 nodes covered`
           reads as measured; the inferred portion is stated in the same breath or not at all. */
        !composition
          ? "not reported"
          : composition.nodes_covered_inferred > 0
            ? `${integer(composition.nodes_covered)} of ${integer(composition.nodes_total)} nodes covered · ${integer(composition.nodes_covered_inferred)} by inference alone`
            : `${integer(composition.nodes_covered)} of ${integer(composition.nodes_total)} nodes covered`
      }
    >
      {agent ? <AgentPanel agent={agent} /> : null}

      {!composition ? (
        /* The body carried no composition. 🚫 NOT rendered as "nothing is covered": this deployment
           did not tell us, and an invented zero here is a measurement nobody made. */
        <Empty title="This deployment did not report a composition for this workflow.">
          <p>
            The graph above is unaffected — it is what the classifier found. What is missing is the
            summary of which patterns cover which nodes, which a platform older than this console does
            not compute.
          </p>
        </Empty>
      ) : patterns.length === 0 ? (
        <Empty title="No pattern covers any part of this workflow yet.">
          <p>
            That is a statement about the twenty patterns in the taxonomy and what has been established
            so far — not about whether this workflow does something worth naming. The regions below name
            the cause for each part that carries no label.
          </p>
        </Empty>
      ) : (
        <div className="flex flex-col gap-4">
          <DataTable
            caption="Every pattern present in this workflow, what it covers, and how it is known"
            columns={[
              { key: "pattern", label: "Pattern" },
              { key: "state", label: "How we know" },
              { key: "regions", label: "Regions", numeric: true },
              { key: "nodes", label: "Nodes", numeric: true },
              { key: "provenance", label: "Established by" },
            ]}
          >
            <tbody>
              {patterns.map((p) => (
                <CompositionRow key={p.pattern} pattern={p} />
              ))}
            </tbody>
          </DataTable>
          <p className="caption">
            {integer(composition.unlabelled_remainder)}{" "}
            {plural(composition.unlabelled_remainder, "node is", "nodes are")} covered by no pattern at
            all. A composition names what was found; the remainder is what was not, and it is stated
            rather than left to be worked out from the difference.
          </p>
        </div>
      )}
    </Section>
  );
}

/**
 * One pattern in the composition.
 *
 * The `state` chip is the load-bearing cell. A pattern every contributing detector read out of the
 * source and one the agent alone proposed occupy the same row shape and are different claims, and the
 * table would otherwise present them identically.
 */
function CompositionRow({ pattern }: { pattern: CompositionPattern }) {
  const provenance = pattern.provenance ?? [];
  return (
    <tr>
      <td>
        <span className="flex flex-wrap items-center gap-2">
          <Chip variant="count">#{integer(pattern.ordinal)}</Chip>
          <span className="text-sm font-medium">
            <Value flags={pattern.candidate ? ["candidate"] : []}>{pattern.title || pattern.pattern}</Value>
          </span>
          {pattern.group ? <Chip title="taxonomy group">{pattern.group}</Chip> : null}
        </span>
      </td>
      <td>
        <Chip tone={STATE_TONE[pattern.state] ?? "unknown"} title="how this pattern is known">
          {pattern.state.replace(/_/g, " ")}
        </Chip>
      </td>
      <td className="num">{integer(pattern.regions)}</td>
      <td className="num">{integer(pattern.nodes)}</td>
      <td className="mono text-xs">{provenance.length > 0 ? provenance.join(", ") : "—"}</td>
    </tr>
  );
}

/**
 * The four states, and the tone each carries.
 *
 * 🔴 `not_analysed` is NEUTRAL and `unavailable` is WARN, and that is the whole distinction rendered.
 * One is a setting — the default one, which every organization sees before an operator turns analysis
 * on — and the other is a fault on our side. Giving the default an alarming tone would report a
 * deliberate configuration as a problem on every customer's first visit.
 */
const STATE_TONE: Record<string, "ok" | "info" | "neutral" | "warn"> = {
  measured: "ok",
  inferred: "info",
  not_analysed: "neutral",
  unavailable: "warn",
};

/**
 * The agent's contribution to this graph: what it knows, which machine produced it, and what a reader
 * may do next.
 *
 * 🔴 TASK 8.8 — this is a PANEL. A HEROS failure renders here and the rest of the page renders
 * normally: every other surface on it is rule-derived and did not depend on the agent at all, so
 * replacing them with a full-screen error would make an optional subsystem's outage look like a total
 * loss of the customer's data.
 */
function AgentPanel({ agent }: { agent: ViewAgent }) {
  return (
    <div className="flex flex-col gap-3">
      <p className="flex flex-wrap items-center gap-2">
        <Chip tone={STATE_TONE[agent.state] ?? "unknown"} title="what the analysis agent contributed">
          {agent.state.replace(/_/g, " ")}
        </Chip>
        {/* 🔴 TASK 8.6 — WHICH MACHINE. `platform` means we read your source; `customer` means you ran
            it and we never saw it. Those are different promises, and a page that showed inferred facts
            without saying which one applied would be silent about the only part a security review
            cares about. */}
        {agent.placement ? (
          <Chip tone="unknown" title="where this organization's analysis runs">
            {agent.placement}
          </Chip>
        ) : null}
      </p>
      <p className="hint">{agent.state_sentence}</p>
      {agent.placement_sentence ? <p className="hint">{agent.placement_sentence}</p> : null}

      {/* 🚫 A failure is a BANNER inside this panel, never the page. */}
      {agent.failure ? (
        <Banner tone="warn" title="The analysis agent could not be reached.">
          <p>{agent.failure}</p>
          <p>
            Everything else on this page was established by reading your source and is unaffected. What
            is missing is anything the agent would have added.
          </p>
        </Banner>
      ) : null}

      {/* 🔴 TASK 8.2 — the narrative is ASSESSED, and it is ABSENT rather than fabricated. There is no
          `else` branch here on purpose: when the agent wrote nothing, nothing is rendered. Prose
          assembled from the counts above would appear in this same treatment, which tells a reader a
          model wrote it. */}
      {agent.narrative ? (
        <div className="assessed">
          <span className="assessed__mark">assessed</span>
          <p className="text-sm">{agent.narrative}</p>
          <p className="caption">
            Written by the analysis agent about this workflow. It is an assessment, not a measurement —
            nothing on this console dispatches off it.
          </p>
        </div>
      ) : null}

      {/* 🔴 TASK 8.7 — an action where one is available, a stated reason where none is. Never a control
          that cannot work: an action that fails on press is worse than an absent one with a sentence. */}
      {agent.action === "analyse" ? (
        <p className="hint">
          Analysis runs on this platform for your organization. A new revision is analysed when it is
          discovered; there is nothing to start by hand.
        </p>
      ) : null}
      {agent.action_reason ? <p className="hint">{agent.action_reason}</p> : null}
    </div>
  );
}

function textAlternative(view: GraphView): string {
  const nodes = view.nodes ?? [];
  const edges = view.edges ?? [];
  const regions = view.regions ?? [];
  const unclassified = view.unclassified ?? [];
  const layers = new Set(nodes.map((n) => n.layer)).size;
  const control = edges.filter((e) => e.kind === "control").length;
  return (
    `${nodes.length} ${plural(nodes.length, "node", "nodes")} across ${layers} ${plural(layers, "layer", "layers")}, ` +
    `joined by ${edges.length} ${plural(edges.length, "edge", "edges")} of which ${control} ${control === 1 ? "is a control edge" : "are control edges"}. ` +
    `${regions.length} ${plural(regions.length, "region carries a pattern label", "regions carry pattern labels")}; ` +
    `${unclassified.length} ${plural(unclassified.length, "region is", "regions are")} not yet classified. ` +
    `The values are in the table below this graphic.`
  );
}

function GraphTable({ nodes, edges }: { nodes: ViewNode[]; edges: ViewEdge[] }) {
  return (
    <div className="flex flex-col gap-4">
      <DataTable
        caption="Every node, its position in the deterministic layout, and its per-node detail"
        columns={[
          { key: "node", label: "Node" },
          { key: "symbol", label: "Symbol" },
          { key: "model", label: "Model" },
          { key: "policy", label: "Context policy" },
          { key: "tools", label: "Tools", numeric: true },
          { key: "layer", label: "Layer", numeric: true },
          { key: "order", label: "Order", numeric: true },
        ]}
      >
        <tbody>
          {nodes.map((node) => (
            <tr key={node.node_id}>
              <td className="mono">{node.node_id}</td>
              <td className="mono">{node.symbol || "—"}</td>
              <td className="mono">{node.model || "—"}</td>
              <td className="mono">{node.policy || "—"}</td>
              <td className="num">{integer(node.tools)}</td>
              <td className="num">{integer(node.layer)}</td>
              <td className="num">{integer(node.order)}</td>
            </tr>
          ))}
        </tbody>
      </DataTable>
      {/* The table is the drawing's text alternative, so the inferred/measured distinction has to be
          IN it — a reader using the table instead of the SVG must not lose the one channel that says
          which edges are hypotheses. */}
      <DataTable
        caption="Every edge, its kind, and whether it was measured or inferred"
        columns={[
          { key: "from", label: "From" },
          { key: "to", label: "To" },
          { key: "kind", label: "Kind" },
          { key: "how", label: "How we know" },
        ]}
      >
        <tbody>
          {edges.map((edge, index) => (
            <tr key={index}>
              <td className="mono">{edge.from}</td>
              <td className="mono">{edge.to}</td>
              <td>{edge.kind}</td>
              <td>
                {edge.author === "heros" ? (
                  <span className="flex flex-wrap items-center gap-2">
                    <Chip tone="info">inferred</Chip>
                    <span className="caption">{percent(edge.confidence ?? 0)} confidence</span>
                  </span>
                ) : (
                  <Chip tone="ok">measured</Chip>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </DataTable>
    </div>
  );
}

function GraphSVG({
  nodes,
  edges,
  regions,
  unclassified,
}: {
  nodes: ViewNode[];
  edges: ViewEdge[];
  regions: ViewRegion[];
  unclassified: ViewRegion[];
}) {
  const at = new Map<string, { x: number; y: number }>();
  for (const node of nodes) {
    at.set(node.node_id, { x: PAD + node.layer * COL, y: PAD + node.order * ROW });
  }
  // The extent is the last node's far edge plus the padding — not a whole extra ROW/COL, which is
  // what a naive `(max + 1) * pitch` gives and which left a band of empty canvas under every graph.
  const width = PAD * 2 + Math.max(0, ...nodes.map((n) => n.layer)) * COL + BOX_W;
  const height = PAD * 2 + Math.max(0, ...nodes.map((n) => n.order)) * ROW + BOX_H;

  function boundsOf(region: ViewRegion) {
    const members = (region.node_ids ?? []).map((id) => at.get(id)).filter(Boolean) as { x: number; y: number }[];
    if (members.length === 0) return null;
    const x = Math.min(...members.map((m) => m.x)) - 14;
    const y = Math.min(...members.map((m) => m.y)) - 14;
    const w = Math.max(...members.map((m) => m.x)) + BOX_W + 14 - x;
    const h = Math.max(...members.map((m) => m.y)) + BOX_H + 14 - y;
    return { x, y, w, h };
  }

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      className="graph max-w-none"
      aria-hidden="true"
    >
      <defs>
        <marker id="arrow-data" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto">
          <path d="M 0 0 L 10 5 L 0 10 z" className="marker-data" />
        </marker>
        {/* 🔴 An inferred edge gets its OWN marker as well as its own stroke (task 8.3). Two channels,
            not one: a reader who cannot separate the hues — greyscale, a projector, colour vision
            deficiency — still sees a smaller, hollow arrowhead. Colour is never the only channel on
            this drawing, which is the same rule that makes a control edge differ from a data edge by
            arrowhead shape rather than by hue alone. */}
        <marker
          id="arrow-inferred"
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerWidth="6"
          markerHeight="6"
          orient="auto"
        >
          <path d="M 0 0 L 10 5 L 0 10 z" className="marker-inferred" />
        </marker>
        <marker
          id="arrow-control"
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerWidth="8"
          markerHeight="8"
          orient="auto"
        >
          <path d="M 0 0 L 10 5 L 0 10 L 3 5 z" className="marker-control" />
        </marker>
      </defs>

      {[...regions.map((r) => ({ r, source: sourceOf(r) })), ...unclassified.map((r) => ({ r, source: "none" }))].map(
        ({ r, source }) => {
          const b = boundsOf(r);
          if (!b) return null;
          return (
            <rect
              key={r.subgraph_id}
              x={b.x}
              y={b.y}
              width={b.w}
              height={b.h}
              rx="10"
              className={`region region--${source}`}
            />
          );
        },
      )}

      {edges.map((edge, index) => {
        const from = at.get(edge.from);
        const to = at.get(edge.to);
        if (!from || !to) return null;
        const sx = from.x + BOX_W;
        const sy = from.y + BOX_H / 2;
        const tx = to.x;
        const ty = to.y + BOX_H / 2;
        const back = tx <= sx;
        const dip = Math.max(sy, ty) + ROW * 0.42;
        const d = back
          ? `M ${sx} ${sy} C ${sx + 40} ${dip}, ${tx - 40} ${dip}, ${tx} ${ty}`
          : `M ${sx} ${sy} C ${sx + COL * 0.32} ${sy}, ${tx - COL * 0.32} ${ty}, ${tx} ${ty}`;
        // Control and data edges differ by line style AND arrowhead shape, never by colour alone.
        //
        // 🔴 AUTHORSHIP WINS OVER KIND (task 8.3). An inferred data edge and an inferred control edge
        // are both drawn as inferred, because "a model proposed this dependency" is the fact a reader
        // has to see first — the kind is a detail of a relationship whose existence is the hypothesis.
        // Rendering an inferred control edge in the control treatment would put a model's guess in the
        // same channel as a parsed fact.
        const inferred = edge.author === "heros";
        const className = inferred
          ? "edge edge--inferred"
          : edge.kind === "control"
            ? "edge edge--control"
            : "edge edge--data";
        const marker = inferred
          ? "url(#arrow-inferred)"
          : edge.kind === "control"
            ? "url(#arrow-control)"
            : "url(#arrow-data)";
        return (
          <path
            key={index}
            d={d}
            className={className}
            markerEnd={marker}
          >
            {/* The title is the accessible name for a reader on a pointer device, and it carries the
                confidence — which exists only for an inferred edge and must not read as zero on one
                that has none. */}
            <title>
              {inferred
                ? `${edge.from} → ${edge.to} (${edge.kind}) — inferred, ${percent(edge.confidence ?? 0)} confidence`
                : `${edge.from} → ${edge.to} (${edge.kind}) — measured`}
            </title>
          </path>
        );
      })}

      {nodes.map((node) => {
        const p = at.get(node.node_id)!;
        const labels = (node.labels ?? []).map((l) => l.title).filter(Boolean);
        const isLlm = Boolean(node.model);
        return (
          <g key={node.node_id} transform={`translate(${p.x} ${p.y})`}>
            <rect
              width={BOX_W}
              height={BOX_H}
              rx="10"
              className={cx("nodebox", isLlm && "nodebox--llm")}
            />
            <text x="12" y="26" className="nodebox__id">
              {truncate(node.node_id, 22)}
            </text>
            <text x="12" y="45" className="nodebox__model">
              {truncate(node.model || "—", 24)}
            </text>
            {labels.length > 0 ? (
              <text x="12" y="63" className="nodebox__labels">
                {truncate(labels.join(", "), 26)}
              </text>
            ) : null}
          </g>
        );
      })}
    </svg>
  );
}

function sourceOf(region: ViewRegion): string {
  const labels = region.labels ?? [];
  if (labels.length === 0) return "none";
  return labels[0].source === "llm" ? "llm" : "rule";
}

function RegionCard({ region }: { region: ViewRegion }) {
  const labels = region.labels ?? [];
  return (
    <Card className="flex flex-col gap-4">
      <p className="mono text-xs text-muted-foreground">{region.subgraph_id}</p>
      {labels.map((label, index) => (
        <LabelRow key={index} label={label} />
      ))}
    </Card>
  );
}

function LabelRow({ label }: { label: ViewLabel }) {
  const flags = label.candidate ? ["candidate"] : [];
  return (
    <div className={`label-row label-row--${label.source}`}>
      <div className="flex flex-wrap items-center gap-2">
        <Chip variant="count">#{integer(label.ordinal)}</Chip>
        <span className="text-sm font-medium">
          <Value flags={flags}>{label.title || label.pattern}</Value>
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {/* Which layer produced the label is the single most load-bearing fact on this card: a rule
            match is a fact about the code, a model match is an opinion about it. */}
        <Chip tone={label.source === "llm" ? "info" : "ok"} title="which layer produced this label">
          {label.source === "llm" ? "model-guessed" : "rule-matched"}
        </Chip>
        {label.group ? <Chip title="taxonomy group">{label.group}</Chip> : null}
      </div>
      <div className="flex items-center gap-3">
        <span
          className="confidence"
          role="img"
          aria-label={`confidence ${percent(label.confidence)}`}
          style={{ "--confidence": `${Math.max(0, Math.min(1, label.confidence)) * 100}%` } as React.CSSProperties}
        />
        <span className="caption">{percent(label.confidence)} confidence</span>
        {label.provenance ? (
          <span className="mono caption truncate" title="the exact detector or model run that produced this label">
            {label.provenance}
          </span>
        ) : null}
      </div>
      <p className="caption">
        {label.primary_metric
          ? `dispatches ${label.primary_metric}${(label.metrics ?? []).length > 1 ? ` + ${(label.metrics ?? []).filter((m) => m !== label.primary_metric).join(", ")}` : ""}`
          : "dispatches: no metric-set mapped"}
      </p>
      {label.candidate ? (
        <p className="hint">
          Structure shows the shape of this pattern, but confirming it needs runtime traces. Its
          confidence is capped accordingly.
        </p>
      ) : null}
    </div>
  );
}

/**
 * An unlabelled region, with the ONE cause that explains it.
 *
 * 🔴 The old copy covered all four causes with one sentence beginning "No structural signature matched
 * this region, and either…". The four have four different next actions — push a revision a typed
 * frontend can read, nothing at all, configure a model, look at what the model was asked — and an
 * "either/or" sentence gives a reader none of them. The cause and its sentence are both resolved on the
 * platform, so this component cannot invent a fifth.
 */
function UnclassifiedCard({ region }: { region: ViewRegion }) {
  return (
    <Card className="flex flex-col gap-3 border-dashed">
      <p className="flex flex-wrap items-center gap-2">
        <span className="mono text-xs text-muted-foreground">{region.subgraph_id}</span>
        <Chip tone="unknown">unlabelled</Chip>
        {region.reason ? (
          <Chip tone="info" title="why this region carries no label">
            {region.reason.replace(/_/g, " ")}
          </Chip>
        ) : null}
      </p>
      {region.reason_sentence ? <p className="hint">{region.reason_sentence}</p> : null}
      <p className="hint">
        Unlabelled is not the same as &ldquo;no pattern&rdquo;: it is a statement about what has been
        established, not about what this code does.
      </p>
    </Card>
  );
}

/**
 * The legend names every channel the drawing uses.
 *
 * It is not optional decoration: the graph distinguishes five things, and a reader who cannot tell a
 * model-guessed region from a rule-matched one is reading the drawing as if every label were equally
 * trustworthy.
 */
function GraphLegend() {
  const items = [
    { swatch: "legend__swatch--data", label: "data edge — dashed line, plain arrow" },
    { swatch: "legend__swatch--control", label: "control edge — solid line, notched arrow" },
    {
      swatch: "legend__swatch--inferred",
      label: "inferred edge — a model proposed this dependency; it was not parsed out of your source",
    },
    { swatch: "legend__swatch--rule", label: "rule-labelled region" },
    { swatch: "legend__swatch--llm", label: "model-labelled region — a candidate, not a fact" },
    { swatch: "legend__swatch--none", label: "not yet classified" },
  ];
  return (
    <ul className="legend" aria-label="How to read this graph">
      {items.map((item) => (
        <li key={item.label} className="flex items-center gap-2">
          <span className={cx("legend__swatch", item.swatch)} aria-hidden="true" />
          <span className="caption">{item.label}</span>
        </li>
      ))}
    </ul>
  );
}

function truncate(value: string, max: number): string {
  return value.length <= max ? value : `${value.slice(0, max - 1)}…`;
}
