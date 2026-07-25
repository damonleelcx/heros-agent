"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { Section, Card, Row, Chip, Banner, Loading, Empty } from "@/components/primitives";
import { cx } from "@/lib/cx";

/**
 * The node × model matrix (P10 M-series, FR34–FR40). Agent nodes are COLUMNS, models are ROWS. Each
 * cell edits/previews/tests/binds a node's prompt against a model.
 *
 * The one rule that shapes everything: the matrix RANKS NOTHING (D9). No cell is scored, no cell is
 * highlighted as best, and no cell is ranked. The only distinctions a cell can carry are "in force"
 * (bound into runtime) and "unverified/verified" — a studio bind is never a proof, only a selection.
 * Cost/latency/token figures on a tested cell are that execution's raw figures, never a comparison.
 */

type Model = { version_id: string; name: string; provider: string; model_id: string };
type Node = { node_id: string; symbol: string; file: string; prompt_name: string; discovered_model: string };
type Binding = { node_id: string; model_version_id: string; model_id: string; prompt_version_id: string; verified: boolean };
type TimelineEntry = { version_id: string; slots: string[]; created_at: string };
type RunResult = { output: string; cost_usd: number; latency_ms: number; input_tokens: number; output_tokens: number; capped?: boolean; cap_scope?: string };

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url, { headers: { accept: "application/json" } });
  if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error ?? `request failed (${res.status})`);
  return res.json();
}
async function postJSON<T>(url: string, body: unknown): Promise<T> {
  const res = await fetch(url, { method: "POST", headers: { "content-type": "application/json", accept: "application/json" }, body: JSON.stringify(body) });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error ?? `request failed (${res.status})`);
  return data as T;
}

function slotsOf(body: string): string[] {
  const out = new Set<string>();
  for (const m of body.matchAll(/\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}/g)) out.add(m[1]);
  return [...out].sort();
}

export function StudioMatrix() {
  const [workflows, setWorkflows] = useState<string[] | null>(null);
  const [workflow, setWorkflow] = useState<string>("");
  const [models, setModels] = useState<Model[]>([]);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [bindings, setBindings] = useState<Record<string, Binding>>({});
  const [error, setError] = useState<string | null>(null);
  const [cell, setCell] = useState<{ node: Node; model: Model } | null>(null);

  useEffect(() => {
    (async () => {
      try {
        const [wf, mc] = await Promise.all([
          getJSON<{ workflows: string[] }>("/api/console/studio/workflows"),
          getJSON<{ models: Model[] }>("/api/console/studio/models"),
        ]);
        setWorkflows(wf.workflows);
        setModels(mc.models);
        if (wf.workflows.length > 0) setWorkflow(wf.workflows[0]);
      } catch (e) {
        setError(e instanceof Error ? e.message : "could not load the matrix");
      }
    })();
  }, []);

  const loadWorkflow = useCallback(async (wf: string) => {
    if (!wf) return;
    try {
      const [n, b] = await Promise.all([
        getJSON<{ nodes: Node[] }>(`/api/console/studio/nodes?workflow=${encodeURIComponent(wf)}`),
        getJSON<{ bindings: Record<string, Binding> }>(`/api/console/studio/bindings?workflow=${encodeURIComponent(wf)}`),
      ]);
      setNodes(n.nodes);
      setBindings(b.bindings ?? {});
    } catch (e) {
      setError(e instanceof Error ? e.message : "could not load workflow");
    }
  }, []);

  useEffect(() => {
    void loadWorkflow(workflow);
  }, [workflow, loadWorkflow]);

  const refreshBindings = useCallback(async () => {
    if (!workflow) return;
    const b = await getJSON<{ bindings: Record<string, Binding> }>(`/api/console/studio/bindings?workflow=${encodeURIComponent(workflow)}`);
    setBindings(b.bindings ?? {});
  }, [workflow]);

  if (error) return <Banner tone="warn" title="Could not load the matrix">{error}</Banner>;
  if (workflows === null) return <Loading rows={4} label="Loading the studio matrix" />;

  return (
    <div className="flex flex-col gap-8">
      <Section title="Workflow">
        {workflows.length === 0 ? (
          <Empty title="No workflows loaded">Discover a workflow to populate the matrix columns.</Empty>
        ) : (
          <Row>
            {workflows.map((w) => (
              <button key={w} type="button" onClick={() => setWorkflow(w)}
                className={cx("chip cursor-pointer", workflow === w ? "border-primary/40 bg-primary/10 text-primary" : "")}>
                {w}
              </button>
            ))}
          </Row>
        )}
      </Section>

      <Section title="Matrix" aside={<span>nodes across · models down · nothing ranked</span>}>
        {models.length === 0 || nodes.length === 0 ? (
          <Empty title="Empty axis">
            {models.length === 0 ? "No models are registered (no rows)." : "This workflow has no nodes (no columns)."}
          </Empty>
        ) : (
          <MatrixGrid models={models} nodes={nodes} bindings={bindings} selected={cell} onSelect={(node, model) => setCell({ node, model })} />
        )}
      </Section>

      {cell ? (
        <CellPanel
          key={`${cell.node.node_id}:${cell.model.version_id}`}
          workflow={workflow}
          node={cell.node}
          model={cell.model}
          binding={bindings[cell.node.node_id]}
          onBound={refreshBindings}
        />
      ) : nodes.length > 0 && models.length > 0 ? (
        <p className="text-sm text-muted-foreground">Select a cell to edit its prompt, preview, test-run, and bind it into runtime.</p>
      ) : null}
    </div>
  );
}

/** MatrixGrid renders nodes as columns and models as rows. An in-force cell (the node's bound model) is
 *  marked — distinct from any notion of "best," which the grid never renders. */
function MatrixGrid({
  models, nodes, bindings, selected, onSelect,
}: {
  models: Model[]; nodes: Node[]; bindings: Record<string, Binding>;
  selected: { node: Node; model: Model } | null;
  onSelect: (node: Node, model: Model) => void;
}) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-sm">
        <thead>
          <tr>
            <th className="sticky left-0 bg-canvas p-2 text-left text-xs font-normal text-muted-foreground">model \\ node</th>
            {nodes.map((n) => (
              <th key={n.node_id} className="min-w-40 p-2 text-left align-bottom">
                <div className="font-mono text-xs text-foreground">{n.symbol || n.node_id.slice(0, 10)}</div>
                <div className="truncate text-[color:var(--text-muted)] text-xs" title={n.file}>{n.file}</div>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {models.map((m) => (
            <tr key={m.version_id}>
              <th className="sticky left-0 bg-canvas p-2 text-left align-top">
                <div className="font-mono text-xs text-foreground">{m.name}</div>
                <div className="text-[color:var(--text-muted)] text-xs">{m.provider}/{m.model_id}</div>
              </th>
              {nodes.map((n) => {
                const inForce = bindings[n.node_id]?.model_version_id === m.version_id;
                const isSel = selected?.node.node_id === n.node_id && selected?.model.version_id === m.version_id;
                return (
                  <td key={n.node_id} className="p-1 align-top">
                    <button type="button" onClick={() => onSelect(n, m)}
                      className={cx(
                        "flex h-full w-full min-w-36 flex-col gap-1 rounded-lg border p-2 text-left transition-colors",
                        isSel ? "border-primary/60 bg-primary/10" : "border-border hover:border-border-strong",
                      )}>
                      {inForce ? (
                        <Chip tone="info" title="This node is bound to this model at runtime — unverified">in force</Chip>
                      ) : (
                        <span className="text-xs text-muted-foreground">try here</span>
                      )}
                    </button>
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** CellPanel is the intersection's editor: pick the node's prompt version, inject variables, edit,
 *  preview (byte-identical), test-run (output+cost+latency+tokens), and save-and-inject (bind). */
function CellPanel({
  workflow, node, model, binding, onBound,
}: {
  workflow: string; node: Node; model: Model; binding?: Binding; onBound: () => void;
}) {
  const [timeline, setTimeline] = useState<TimelineEntry[] | null>(null);
  const [versionId, setVersionId] = useState<string>("");
  const [editing, setEditing] = useState(false);
  const [body, setBody] = useState(`Prompt for ${node.symbol || node.node_id}:\n{{input}}`);
  const [bindingsVals, setBindingsVals] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<string | null>(null);
  const [run, setRun] = useState<RunResult | null>(null);
  const [msg, setMsg] = useState<{ tone: "info" | "warn"; text: string } | null>(null);

  const promptName = node.prompt_name;

  const loadTimeline = useCallback(async () => {
    try {
      const d = await getJSON<{ versions: TimelineEntry[] }>(`/api/console/studio/timeline?name=${encodeURIComponent(promptName)}`);
      setTimeline(d.versions);
      if (d.versions.length > 0) setVersionId(d.versions[d.versions.length - 1].version_id);
      else setEditing(true); // nothing published yet → start in the editor
    } catch (e) {
      setMsg({ tone: "warn", text: e instanceof Error ? e.message : "could not load versions" });
    }
  }, [promptName]);

  useEffect(() => { void loadTimeline(); }, [loadTimeline]);

  const version = timeline?.find((v) => v.version_id === versionId);
  const slots = version ? version.slots : slotsOf(body);

  async function doPreview() {
    setPreview(null); setMsg(null);
    try {
      const d = await postJSON<{ rendered: string }>("/api/console/studio/preview", { version_id: versionId, bindings: bindingsVals });
      setPreview(d.rendered);
    } catch (e) { setMsg({ tone: "warn", text: e instanceof Error ? e.message : "preview failed" }); }
  }
  async function doRun() {
    setRun(null); setMsg(null);
    try {
      const d = await postJSON<RunResult>("/api/console/studio/run", { model_version_id: model.version_id, prompt_version_id: versionId, bindings: bindingsVals });
      setRun(d);
      if (d.capped) setMsg({ tone: "info", text: `Studio spend cap reached (${d.cap_scope}); test-run stopped — this is configured behavior, not a failure.` });
    } catch (e) { setMsg({ tone: "warn", text: e instanceof Error ? e.message : "test-run failed" }); }
  }
  async function doPublish() {
    setMsg(null);
    try {
      const d = await postJSON<{ version_id: string }>("/api/console/studio/publish", { name: promptName, body });
      setEditing(false);
      await loadTimeline();
      setVersionId(d.version_id);
      setMsg({ tone: "info", text: "Saved as a new version — the prior version stays resolvable." });
    } catch (e) { setMsg({ tone: "warn", text: e instanceof Error ? e.message : "save failed" }); }
  }
  async function doBind() {
    setMsg(null);
    if (!versionId) { setMsg({ tone: "warn", text: "publish a prompt version first" }); return; }
    try {
      await postJSON("/api/console/studio/bind", {
        workflow_id: workflow, node_id: node.node_id, model_version_id: model.version_id,
        model_id: `${model.provider}/${model.model_id}`, prompt_name: promptName, prompt_version_id: versionId,
      });
      setMsg({ tone: "info", text: "Bound into runtime — in force, unverified. A selection is not a proof; run a multi-seed evaluation to make a claim." });
      onBound();
    } catch (e) { setMsg({ tone: "warn", text: e instanceof Error ? e.message : "bind failed" }); }
  }

  return (
    <Card className="border-primary/30">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <Row>
          <Chip variant="hash">{node.symbol || node.node_id.slice(0, 10)}</Chip>
          <span className="text-xs text-muted-foreground">×</span>
          <Chip>{model.provider}/{model.model_id}</Chip>
          {binding?.model_version_id === model.version_id ? <Chip tone="info">in force — unverified</Chip> : null}
        </Row>
        <span className="text-xs text-muted-foreground">exploratory — no score, no winner</span>
      </div>

      <div className="flex flex-col gap-3">
        {timeline === null ? (
          <Loading rows={1} label="Loading prompt versions" />
        ) : (
          <Row>
            <span className="text-xs text-muted-foreground">version</span>
            {timeline.length === 0 ? <span className="text-xs text-muted-foreground">none yet</span> : (
              <select value={versionId} onChange={(e) => setVersionId(e.target.value)}
                className="rounded-lg border border-border bg-canvas px-2 py-1 font-mono text-xs text-foreground">
                {timeline.map((v, i) => (
                  <option key={v.version_id} value={v.version_id}>
                    {v.version_id.slice(0, 12)}{i === timeline.length - 1 ? " (latest)" : ""}
                  </option>
                ))}
              </select>
            )}
            <button type="button" onClick={() => setEditing((v) => !v)} className="chip cursor-pointer">
              {editing ? "Close editor" : "Edit"}
            </button>
          </Row>
        )}

        {editing ? (
          <div className="flex flex-col gap-2">
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              Body — variables are {"{{name}}"}; rendered as text, never markup
              <textarea value={body} onChange={(e) => setBody(e.target.value)} rows={5}
                className="rounded-lg border border-border bg-canvas px-2 py-2 font-mono text-xs text-foreground" />
            </label>
            <Row>
              <button type="button" onClick={doPublish} className="chip cursor-pointer border-primary/40 bg-primary/10 text-primary">
                Save as new version
              </button>
            </Row>
          </div>
        ) : null}

        {slots.length > 0 ? (
          <div className="flex flex-col gap-2">
            <span className="text-xs text-muted-foreground">Variables</span>
            {slots.map((s) => (
              <label key={s} className="flex items-center gap-2 text-sm">
                <span className="w-32 shrink-0 font-mono text-xs text-foreground">{s}</span>
                <input value={bindingsVals[s] ?? ""} onChange={(e) => setBindingsVals((b) => ({ ...b, [s]: e.target.value }))}
                  className="min-w-0 flex-1 rounded-lg border border-border bg-canvas px-2 py-1 text-sm" placeholder={`value for {{${s}}}`} />
              </label>
            ))}
          </div>
        ) : null}

        <Row>
          <button type="button" onClick={doPreview} disabled={!versionId} className="chip cursor-pointer disabled:opacity-50">Preview</button>
          <button type="button" onClick={doRun} disabled={!versionId} className="chip cursor-pointer disabled:opacity-50">Test-run</button>
          <button type="button" onClick={doBind} disabled={!versionId} className="chip cursor-pointer border-primary/40 bg-primary/10 text-primary disabled:opacity-50">
            Save & inject into runtime
          </button>
        </Row>

        {msg ? <Banner tone={msg.tone} title={msg.tone === "warn" ? "Problem" : "Done"}>{msg.text}</Banner> : null}

        {preview !== null ? (
          <div>
            <span className="text-xs text-muted-foreground">Preview — byte-identical to what a run sends</span>
            <pre className="mt-1 overflow-x-auto whitespace-pre-wrap rounded-lg border border-border bg-canvas p-3 font-mono text-xs text-foreground">{preview}</pre>
          </div>
        ) : null}

        {run !== null && !run.capped ? (
          <div>
            <span className="text-xs text-muted-foreground">Test-run — this execution's figures only, not a comparison</span>
            <div className="mt-1 flex flex-wrap gap-2">
              <Chip variant="count" title="cost of this execution">${run.cost_usd.toFixed(6)}</Chip>
              <Chip variant="count">{run.latency_ms} ms</Chip>
              <Chip variant="count">{run.input_tokens}+{run.output_tokens} tok</Chip>
            </div>
            <pre className="mt-2 overflow-x-auto whitespace-pre-wrap rounded-lg border border-border bg-canvas p-3 font-mono text-xs text-foreground">{run.output}</pre>
          </div>
        ) : null}
      </div>
    </Card>
  );
}
