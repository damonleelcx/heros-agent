import type { GraphView, ViewNode, ViewEdge, ViewRegion, ViewLabel } from "@/lib/types.generated";
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
          <Stat label="Edges" value={integer(edges.length)} note="dependencies mapped" />
          <Stat
            label={`LLM fallback ${plural(view.llm_calls, "call", "calls")}`}
            value={integer(view.llm_calls)}
            note={
              view.llm_calls === 0
                ? "Fully rule-covered — no model was consulted"
                : "a model was consulted to classify this graph"
            }
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
        <GraphLegend />
      </Section>

      <Section title="Patterns" aside={`${regions.length} labelled · ${unclassified.length} not yet`}>
        {regions.length === 0 && unclassified.length === 0 ? (
          <Empty title="No region in this workflow carries a label yet.">
            <p>
              That is a state, not an error. It does not mean the workflow implements no patterns — it
              means no structural signature matched and, where a model was consulted, nothing it
              returned was in the taxonomy.
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
      <DataTable
        caption="Every edge and its kind"
        columns={[
          { key: "from", label: "From" },
          { key: "to", label: "To" },
          { key: "kind", label: "Kind" },
        ]}
      >
        <tbody>
          {edges.map((edge, index) => (
            <tr key={index}>
              <td className="mono">{edge.from}</td>
              <td className="mono">{edge.to}</td>
              <td>{edge.kind}</td>
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
        return (
          <path
            key={index}
            d={d}
            className={edge.kind === "control" ? "edge edge--control" : "edge edge--data"}
            markerEnd={edge.kind === "control" ? "url(#arrow-control)" : "url(#arrow-data)"}
          />
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

function UnclassifiedCard({ region }: { region: ViewRegion }) {
  return (
    <Card className="flex flex-col gap-3 border-dashed">
      <p className="flex flex-wrap items-center gap-2">
        <span className="mono text-xs text-muted-foreground">{region.subgraph_id}</span>
        <Chip tone="unknown">not yet classified</Chip>
      </p>
      <p className="hint">
        No structural signature matched this region, and either no model was consulted or the fallback
        returned nothing in the taxonomy. This region is <strong>unlabelled</strong> — which is not the
        same as &ldquo;no pattern&rdquo;.
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
