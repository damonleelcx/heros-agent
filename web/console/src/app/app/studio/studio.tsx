"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { Section, Card, Row, Chip, Banner, Loading, Empty } from "@/components/primitives";
import { cx } from "@/lib/cx";
import { ReadOn } from "@/components/editorKit";
import { AXIS_DOC } from "@/lib/axisSubject";
import { BoundNodePanel, DegradedBanner, type BoundNode, type ResolverHealth } from "./boundmode";

/**
 * The studio surface (P10 §4). One client component, because every panel shares the selected prompt and
 * the draft body, and threading that through five server components would be more machinery than the
 * feature needs. Every data call goes through the BFF (`/api/console/studio/*`), never the platform
 * directly — the browser never holds the platform credential.
 *
 * Two rules govern everything rendered here:
 *   1. Prompt bodies are CUSTOMER CONTENT. They are rendered as text (in <pre>/{string}), never as
 *      markup — no dangerouslySetInnerHTML anywhere — and never logged.
 *   2. Nothing is ranked. There is no score, no winner, no confidence interval, and no button that
 *      promotes a configuration. A comparison shows two outputs side by side and says nothing about
 *      which is better (P10 Decision 8). The exploratory label is on the results themselves.
 */

type TimelineEntry = { version_id: string; name: string; slots: string[]; created_at: string };
type DiffResult = {
  version_a: string;
  version_b: string;
  body_changed: boolean;
  body_diff: { op: "context" | "add" | "remove"; text: string }[];
  slots_added: string[];
  slots_removed: string[];
};
type ImpactResult = {
  proposed_slots: string[];
  blocked: { node_id: string; reason: string }[];
  unanalyzed: { node_id: string; why: string }[];
};

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url, { headers: { accept: "application/json" } });
  if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error ?? `request failed (${res.status})`);
  return res.json();
}
async function postJSON<T>(url: string, body: unknown): Promise<T> {
  const res = await fetch(url, {
    method: "POST",
    headers: { "content-type": "application/json", accept: "application/json" },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error ?? `request failed (${res.status})`);
  return data as T;
}

/** slotsOf extracts {{name}} slot names from a body, mirroring the platform's ParseTemplate. The
 *  authoritative parse is server-side; this only drives the binding editor's row list. */
function slotsOf(body: string): string[] {
  const out = new Set<string>();
  for (const m of body.matchAll(/\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}/g)) out.add(m[1]);
  return [...out].sort();
}

export function Studio() {
  const [names, setNames] = useState<string[] | null>(null);
  const [namesError, setNamesError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);

  const refreshNames = useCallback(async () => {
    try {
      const data = await getJSON<{ names: string[] }>("/api/console/studio/names");
      setNames(data.names);
      setNamesError(null);
    } catch (e) {
      setNamesError(e instanceof Error ? e.message : "could not load prompts");
    }
  }, []);

  useEffect(() => {
    void refreshNames();
  }, [refreshNames]);

  return (
    <div className="flex flex-col gap-6">
      <Section title="Prompts">
        {namesError ? (
          <Banner tone="warn" title="Could not load prompts">
            {namesError}
          </Banner>
        ) : names === null ? (
          <Loading rows={2} label="Loading prompts" />
        ) : names.length === 0 ? (
          <Empty title="No prompts yet">
            Publish one below with “Save as new version” and it will appear here.
          </Empty>
        ) : (
          <Row>
            {names.map((n) => (
              <button
                key={n}
                type="button"
                onClick={() => setSelected(n)}
                className={cx(
                  "chip cursor-pointer",
                  selected === n ? "border-primary/40 bg-primary/10 text-primary" : "",
                )}
              >
                {n}
              </button>
            ))}
          </Row>
        )}
      </Section>

      {selected ? <PromptWorkbench name={selected} onPublished={refreshNames} /> : null}

      <Editor names={names ?? []} onPublished={refreshNames} />
    </div>
  );
}

/**
 * BoundModeShowcase illustrates the bound-mode surfaces (P10 §10): apply mode per node, the effective
 * resolved values rather than the indirection, the verified-vs-unverified distinction, and the degraded
 * resolver state. It is clearly labelled as an illustration — bound-node data is populated from a
 * candidate's binding document and the resolver's health endpoint when one is in force.
 */
export function BoundModeShowcase() {
  const health: ResolverHealth = {
    degraded: true,
    failedSource: "remote",
    reason: "connection refused",
    resolvedConfigHash: "b3f1a9c2d4e5f6a7",
    unverified: false,
  };
  const nodes: BoundNode[] = [
    {
      nodeId: "n_triage",
      applyMode: "bound",
      modelId: "anthropic/claude-sonnet-5",
      params: { max_tokens: 1024, temperature: 0.2 },
      promptTemplate: "Triage this ticket:\n{{ticket}}\nTier: {{tier}}",
      literalBindings: { tier: "gold" },
      envBindings: { region: "AWS_REGION" },
      exprBindings: { ticket: "ticket" },
      verified: true,
    },
    {
      nodeId: "n_summary",
      applyMode: "bound",
      modelId: "openai/gpt-5",
      params: { max_tokens: 512 },
      promptTemplate: "Summarize:\n{{doc}}",
      exprBindings: { doc: "document" },
      verified: false,
    },
    { nodeId: "n_classify", applyMode: "inline", verified: false },
  ];
  return (
    <Section title="Bound-mode nodes" aside={<span>illustration — effective values, not the pointer</span>}>
      <DegradedBanner health={health} />
      {nodes.map((n) => (
        <BoundNodePanel key={n.nodeId} node={n} />
      ))}
    </Section>
  );
}

/** PromptWorkbench shows the timeline for a selected prompt, its diff view, and a preview panel. */
function PromptWorkbench({ name, onPublished }: { name: string; onPublished: () => void }) {
  const [timeline, setTimeline] = useState<TimelineEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setTimeline(null);
    try {
      const data = await getJSON<{ versions: TimelineEntry[] }>(
        `/api/console/studio/timeline?name=${encodeURIComponent(name)}`,
      );
      setTimeline(data.versions);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "could not load timeline");
    }
  }, [name]);

  useEffect(() => {
    void load();
  }, [load, onPublished]);

  return (
    <>
      <Section title={`Version timeline — ${name}`} aside={<span>newest is the live version</span>}>
        {error ? (
          <Banner tone="warn" title="Could not load the timeline">
            {error}
          </Banner>
        ) : timeline === null ? (
          <Loading rows={3} label="Loading versions" />
        ) : timeline.length === 0 ? (
          <Empty title="No versions">This name has no published versions.</Empty>
        ) : (
          <Timeline entries={timeline} />
        )}
      </Section>

      {timeline && timeline.length >= 2 ? <DiffView versions={timeline} /> : null}
      {timeline && timeline.length >= 1 ? <PreviewPanel versions={timeline} /> : null}
      {timeline && timeline.length >= 2 ? <SideBySide versions={timeline} /> : null}
    </>
  );
}

/** Timeline lists versions with the LIVE (newest) one unmistakable — a list of look-alike hashes where
 *  the running one is not obvious invites pointing a node at the wrong one. */
function Timeline({ entries }: { entries: TimelineEntry[] }) {
  // Newest first for display; the platform returns oldest-first.
  const ordered = [...entries].reverse();
  return (
    <div className="flex flex-col gap-2">
      {ordered.map((e, i) => {
        const live = i === 0;
        return (
          <Card key={e.version_id} className={cx(live ? "border-primary/40" : "")}>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="flex flex-wrap items-center gap-2">
                {live ? <Chip tone="ok">Live</Chip> : <Chip>Older</Chip>}
                <Chip variant="hash" title={e.version_id}>
                  {e.version_id.slice(0, 12)}
                </Chip>
                <span className="text-xs text-muted-foreground">
                  {e.created_at ? new Date(e.created_at).toISOString().replace("T", " ").slice(0, 19) : ""}
                </span>
              </div>
              <div className="flex flex-wrap items-center gap-1">
                <span className="text-xs text-muted-foreground">slots:</span>
                {e.slots.length === 0 ? (
                  <span className="text-xs text-muted-foreground">none</span>
                ) : (
                  e.slots.map((s) => (
                    <Chip key={s} variant="count">
                      {s}
                    </Chip>
                  ))
                )}
              </div>
            </div>
          </Card>
        );
      })}
    </div>
  );
}

/** DiffView shows the SLOT-SET change separately from the body change (task 4.2) — a slot change is
 *  what alters where a prompt can be applied and is nearly invisible in a body diff. */
function DiffView({ versions }: { versions: TimelineEntry[] }) {
  const [a, setA] = useState(versions[0].version_id);
  const [b, setB] = useState(versions[versions.length - 1].version_id);
  const [diff, setDiff] = useState<DiffResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const run = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setDiff(await getJSON<DiffResult>(`/api/console/studio/diff?a=${encodeURIComponent(a)}&b=${encodeURIComponent(b)}`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "diff failed");
      setDiff(null);
    } finally {
      setLoading(false);
    }
  }, [a, b]);

  return (
    <Section title="Diff two versions">
      <Card>
        <div className="flex flex-wrap items-end gap-3">
          <VersionSelect label="From" value={a} onChange={setA} versions={versions} />
          <VersionSelect label="To" value={b} onChange={setB} versions={versions} />
          <button type="button" onClick={run} className="chip cursor-pointer border-primary/40 bg-primary/10 text-primary">
            Compare
          </button>
        </div>
      </Card>
      {error ? (
        <Banner tone="warn" title="Diff failed">
          {error}
        </Banner>
      ) : loading ? (
        <Loading rows={2} label="Diffing" />
      ) : diff ? (
        <div className="flex flex-col gap-4">
          <Card>
            <h3 className="mb-2 text-sm font-medium text-foreground">Slot-set change</h3>
            <div className="flex flex-wrap items-center gap-2">
              {diff.slots_added.length === 0 && diff.slots_removed.length === 0 ? (
                <span className="text-sm text-muted-foreground">unchanged — this edit does not move where the prompt can be applied</span>
              ) : (
                <>
                  {diff.slots_added.map((s) => (
                    <Chip key={`+${s}`} tone="ok">
                      + {s}
                    </Chip>
                  ))}
                  {diff.slots_removed.map((s) => (
                    <Chip key={`-${s}`} tone="bad">
                      − {s}
                    </Chip>
                  ))}
                </>
              )}
            </div>
          </Card>
          <Card>
            <h3 className="mb-2 text-sm font-medium text-foreground">Body</h3>
            {!diff.body_changed ? (
              <span className="text-sm text-muted-foreground">no body change</span>
            ) : (
              <pre className="overflow-x-auto rounded-lg border border-border bg-canvas p-3 font-mono text-xs leading-relaxed">
                {diff.body_diff.map((line, i) => (
                  <div
                    key={i}
                    className={cx(
                      line.op === "add" && "text-[color:var(--ok)]",
                      line.op === "remove" && "text-[color:var(--danger)]",
                      line.op === "context" && "text-muted-foreground",
                    )}
                  >
                    {line.op === "add" ? "+ " : line.op === "remove" ? "− " : "  "}
                    {line.text}
                  </div>
                ))}
              </pre>
            )}
          </Card>
        </div>
      ) : null}
    </Section>
  );
}

function VersionSelect({
  label,
  value,
  onChange,
  versions,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  versions: TimelineEntry[];
}) {
  return (
    <label className="flex flex-col gap-1 text-xs text-muted-foreground">
      {label}
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="rounded-lg border border-border bg-canvas px-2 py-1 font-mono text-xs text-foreground"
      >
        {versions.map((v) => (
          <option key={v.version_id} value={v.version_id}>
            {v.version_id.slice(0, 12)}
          </option>
        ))}
      </select>
    </label>
  );
}

/** PreviewPanel renders the byte-identical string a run would send (task 4.6). A missing/unknown
 *  binding fails and names the slot — no partial render is shown. */
function PreviewPanel({ versions }: { versions: TimelineEntry[] }) {
  const [versionId, setVersionId] = useState(versions[versions.length - 1].version_id);
  const version = versions.find((v) => v.version_id === versionId) ?? versions[versions.length - 1];
  const [bindings, setBindings] = useState<Record<string, string>>({});
  const [rendered, setRendered] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const run = useCallback(async () => {
    setError(null);
    setRendered(null);
    try {
      const data = await postJSON<{ rendered: string }>("/api/console/studio/preview", {
        version_id: versionId,
        bindings,
      });
      setRendered(data.rendered);
    } catch (e) {
      setError(e instanceof Error ? e.message : "preview failed");
    }
  }, [versionId, bindings]);

  return (
    <Section title="Preview" aside={<span>byte-identical to what a run sends</span>}>
      <Card>
        <div className="mb-3 flex flex-wrap items-end gap-3">
          <VersionSelect label="Version" value={versionId} onChange={setVersionId} versions={versions} />
          <button type="button" onClick={run} className="chip cursor-pointer border-primary/40 bg-primary/10 text-primary">
            Render
          </button>
        </div>
        {version.slots.length > 0 ? (
          <div className="mb-3 flex flex-col gap-2">
            <span className="text-xs text-muted-foreground">Sample bindings</span>
            {version.slots.map((s) => (
              <label key={s} className="flex items-center gap-2 text-sm">
                <span className="w-40 shrink-0 font-mono text-xs text-foreground">{s}</span>
                <input
                  value={bindings[s] ?? ""}
                  onChange={(e) => setBindings((b) => ({ ...b, [s]: e.target.value }))}
                  className="min-w-0 flex-1 rounded-lg border border-border bg-canvas px-2 py-1 text-sm"
                  placeholder={`value for {{${s}}}`}
                />
              </label>
            ))}
          </div>
        ) : null}
        {error ? (
          <Banner tone="warn" title="Preview failed">
            {error}
          </Banner>
        ) : rendered !== null ? (
          <pre className="overflow-x-auto whitespace-pre-wrap rounded-lg border border-border bg-canvas p-3 font-mono text-xs leading-relaxed text-foreground">
            {rendered}
          </pre>
        ) : (
          <span className="text-sm text-muted-foreground">Choose bindings and Render to see the exact string.</span>
        )}
      </Card>
    </Section>
  );
}

/** SideBySide compares two versions over the SAME bindings, presenting both outputs together — and
 *  saying nothing about which is better (task 4.7, Decision 8). No score, no winner. */
function SideBySide({ versions }: { versions: TimelineEntry[] }) {
  const [left, setLeft] = useState(versions[0].version_id);
  const [right, setRight] = useState(versions[versions.length - 1].version_id);
  const slots = useMemo(() => {
    const set = new Set<string>();
    for (const v of versions) if (v.version_id === left || v.version_id === right) v.slots.forEach((s) => set.add(s));
    return [...set].sort();
  }, [versions, left, right]);
  const [bindings, setBindings] = useState<Record<string, string>>({});
  const [out, setOut] = useState<{ left?: string; right?: string; leftErr?: string; rightErr?: string } | null>(null);

  const run = useCallback(async () => {
    const render = async (id: string) => {
      try {
        const d = await postJSON<{ rendered: string }>("/api/console/studio/preview", { version_id: id, bindings });
        return { rendered: d.rendered as string | undefined, err: undefined as string | undefined };
      } catch (e) {
        return { rendered: undefined, err: e instanceof Error ? e.message : "failed" };
      }
    };
    const [l, r] = await Promise.all([render(left), render(right)]);
    setOut({ left: l.rendered, leftErr: l.err, right: r.rendered, rightErr: r.err });
  }, [left, right, bindings]);

  return (
    <Section title="Side-by-side" aside={<span>two versions, same bindings — no winner declared</span>}>
      <Card>
        <div className="mb-3 flex flex-wrap items-end gap-3">
          <VersionSelect label="Left" value={left} onChange={setLeft} versions={versions} />
          <VersionSelect label="Right" value={right} onChange={setRight} versions={versions} />
          <button type="button" onClick={run} className="chip cursor-pointer border-primary/40 bg-primary/10 text-primary">
            Render both
          </button>
        </div>
        {slots.map((s) => (
          <label key={s} className="mb-2 flex items-center gap-2 text-sm">
            <span className="w-40 shrink-0 font-mono text-xs text-foreground">{s}</span>
            <input
              value={bindings[s] ?? ""}
              onChange={(e) => setBindings((b) => ({ ...b, [s]: e.target.value }))}
              className="min-w-0 flex-1 rounded-lg border border-border bg-canvas px-2 py-1 text-sm"
            />
          </label>
        ))}
      </Card>
      {out ? (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          <ComparePane label={`Left — ${left.slice(0, 12)}`} rendered={out.left} error={out.leftErr} />
          <ComparePane label={`Right — ${right.slice(0, 12)}`} rendered={out.right} error={out.rightErr} />
        </div>
      ) : null}
    </Section>
  );
}

function ComparePane({ label, rendered, error }: { label: string; rendered?: string; error?: string }) {
  return (
    <Card>
      <h3 className="mb-2 font-mono text-xs text-muted-foreground">{label}</h3>
      {error ? (
        <Banner tone="warn" title="Render failed">
          {error}
        </Banner>
      ) : (
        <pre className="overflow-x-auto whitespace-pre-wrap rounded-lg border border-border bg-canvas p-3 font-mono text-xs leading-relaxed text-foreground">
          {rendered ?? ""}
        </pre>
      )}
    </Card>
  );
}

/** Editor is where a prompt is authored. Its action is "Save as new version" — never "Save":
 *  publishing is immutable and content-addressed, so a verb that implies mutation would misdescribe
 *  system behaviour (task 4.3). Impact analysis runs BEFORE publish (task 4.4). */
function Editor({ names, onPublished }: { names: string[]; onPublished: () => void }) {
  const [name, setName] = useState("");
  const [body, setBody] = useState("Triage this ticket:\n{{ticket}}");
  const [nodesJSON, setNodesJSON] = useState(
    JSON.stringify([{ NodeID: "n_triage", CallSiteExprs: ["ticket"], Analyzable: true }], null, 2),
  );
  const [impact, setImpact] = useState<ImpactResult | null>(null);
  const [impactError, setImpactError] = useState<string | null>(null);
  const [publishState, setPublishState] = useState<
    { kind: "idle" } | { kind: "publishing" } | { kind: "done"; versionId: string } | { kind: "error"; message: string }
  >({ kind: "idle" });

  const slots = slotsOf(body);

  const analyze = useCallback(async () => {
    setImpactError(null);
    setImpact(null);
    let nodes: unknown;
    try {
      nodes = JSON.parse(nodesJSON);
    } catch {
      setImpactError("the nodes list is not valid JSON");
      return;
    }
    try {
      setImpact(await postJSON<ImpactResult>("/api/console/studio/impact", { proposed_body: body, nodes }));
    } catch (e) {
      setImpactError(e instanceof Error ? e.message : "impact analysis failed");
    }
  }, [body, nodesJSON]);

  const publish = useCallback(async () => {
    setPublishState({ kind: "publishing" });
    try {
      const data = await postJSON<{ version_id: string }>("/api/console/studio/publish", { name, body });
      setPublishState({ kind: "done", versionId: data.version_id });
      onPublished();
    } catch (e) {
      setPublishState({ kind: "error", message: e instanceof Error ? e.message : "publish failed" });
    }
  }, [name, body, onPublished]);

  return (
    <Section title="Editor" aside={<span>publishing is immutable — an edit is a new version</span>}>
      <Card>
        <div className="flex flex-col gap-3">
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            Name
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              list="studio-prompt-names"
              placeholder="triage_prompt"
              className="rounded-lg border border-border bg-canvas px-2 py-1 text-sm text-foreground"
            />
            <datalist id="studio-prompt-names">
              {names.map((n) => (
                <option key={n} value={n} />
              ))}
            </datalist>
          </label>
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            Body — variables are {"{{name}}"}; rendered as text, never markup
            <textarea
              value={body}
              onChange={(e) => setBody(e.target.value)}
              rows={6}
              className="rounded-lg border border-border bg-canvas px-2 py-2 font-mono text-xs text-foreground"
            />
          </label>
          {slots.length > 0 ? (
            <Row>
              <span className="text-xs text-muted-foreground">declared slots:</span>
              {slots.map((s) => (
                <Chip key={s} variant="count">
                  {s}
                </Chip>
              ))}
            </Row>
          ) : null}

          <BindingEditor slots={slots} nodesJSON={nodesJSON} />

          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            Nodes pinning this prompt (for impact analysis)
            <textarea
              value={nodesJSON}
              onChange={(e) => setNodesJSON(e.target.value)}
              rows={4}
              className="rounded-lg border border-border bg-canvas px-2 py-2 font-mono text-xs text-foreground"
            />
          </label>

          <Row>
            <button type="button" onClick={analyze} className="chip cursor-pointer">
              Analyze impact
            </button>
            <button
              type="button"
              onClick={publish}
              disabled={publishState.kind === "publishing" || name.trim() === ""}
              className="chip cursor-pointer border-primary/40 bg-primary/10 text-primary disabled:opacity-50"
            >
              Save as new version
            </button>
          </Row>

          {impactError ? (
            <Banner tone="warn" title="Impact analysis failed">
              {impactError}
            </Banner>
          ) : impact ? (
            <ImpactReport impact={impact} />
          ) : null}

          {publishState.kind === "done" ? (
            <Banner tone="info" title="Saved as a new version">
              <span className="font-mono">{publishState.versionId.slice(0, 16)}</span> — the prior version stays
              resolvable and unchanged.
            </Banner>
          ) : publishState.kind === "error" ? (
            <Banner tone="warn" title="Could not save">
              {publishState.message}
            </Banner>
          ) : null}
        </div>
      </Card>

      <RuntimeChangeableStatement />
    </Section>
  );
}

/** BindingEditor offers, per slot, a binding kind and value. Where the call site's in-scope symbols
 *  are known (from the nodes list) it offers them as a pick list for `expr` — a validated choice
 *  cannot be made wrong, a free-text box can (task 4.5). */
function BindingEditor({ slots, nodesJSON }: { slots: string[]; nodesJSON: string }) {
  // `null` means the in-scope symbols could not be read — which is NOT the same fact as "there are no
  // symbols in scope" (`[]`). Representing an unreadable nodes payload as an empty set is the exact
  // unknown-vs-empty collapse P9's fail-closed rule (and security.test.mjs) forbids: it would assert
  // there is nothing to bind to when the truth is we do not know. Both cases fall through to the
  // free-text input below, but they are kept as different facts.
  const inScope = useMemo<string[] | null>(() => {
    try {
      const nodes = JSON.parse(nodesJSON) as { CallSiteExprs?: string[] }[];
      const set = new Set<string>();
      for (const n of nodes) (n.CallSiteExprs ?? []).forEach((e) => set.add(e));
      return [...set].sort();
    } catch {
      return null;
    }
  }, [nodesJSON]);
  const [kinds, setKinds] = useState<Record<string, string>>({});
  const [values, setValues] = useState<Record<string, string>>({});

  if (slots.length === 0) return null;
  return (
    <div className="rounded-lg border border-border p-3">
      <h3 className="mb-2 text-sm font-medium text-foreground">Bindings</h3>
      <p className="mb-3 text-xs text-muted-foreground">
        Bind each slot to a literal, an expression, an environment variable, or an input.
        The kind is recorded explicitly — it is never guessed from the value.
      </p>
      <div className="flex flex-col gap-2">
        {slots.map((s) => {
          const kind = kinds[s] ?? "literal";
          return (
            <div key={s} className="flex flex-wrap items-center gap-2">
              <span className="w-36 shrink-0 font-mono text-xs text-foreground">{s}</span>
              <select
                value={kind}
                onChange={(e) => setKinds((k) => ({ ...k, [s]: e.target.value }))}
                className="rounded-lg border border-border bg-canvas px-2 py-1 text-xs text-foreground"
              >
                <option value="literal">literal</option>
                <option value="expr">expr</option>
                <option value="env">env</option>
                <option value="input">input</option>
              </select>
              {kind === "expr" && inScope && inScope.length > 0 ? (
                <select
                  value={values[s] ?? ""}
                  onChange={(e) => setValues((v) => ({ ...v, [s]: e.target.value }))}
                  className="min-w-0 flex-1 rounded-lg border border-border bg-canvas px-2 py-1 font-mono text-xs text-foreground"
                >
                  <option value="">choose an in-scope expression…</option>
                  {inScope.map((e) => (
                    <option key={e} value={e}>
                      {e}
                    </option>
                  ))}
                </select>
              ) : (
                <input
                  value={values[s] ?? ""}
                  onChange={(e) => setValues((v) => ({ ...v, [s]: e.target.value }))}
                  placeholder={kind === "env" ? "VARIABLE_NAME" : kind === "literal" ? "constant value" : "input field"}
                  className="min-w-0 flex-1 rounded-lg border border-border bg-canvas px-2 py-1 font-mono text-xs text-foreground"
                />
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function ImpactReport({ impact }: { impact: ImpactResult }) {
  const clean = impact.blocked.length === 0 && impact.unanalyzed.length === 0;
  return (
    <div className="flex flex-col gap-3">
      {clean ? (
        <Banner tone="info" title="No nodes blocked">
          Every node that pins this prompt can transform under the proposed slot set.
        </Banner>
      ) : null}
      {impact.blocked.length > 0 ? (
        <Card className="border-[color:var(--danger-border)]">
          <h3 className="mb-2 text-sm font-medium text-foreground">Would fail to transform</h3>
          <ul className="flex flex-col gap-2">
            {impact.blocked.map((b) => (
              <li key={b.node_id} className="text-sm">
                <Chip tone="bad">{b.node_id}</Chip>{" "}
                <span className="text-muted-foreground">{b.reason}</span>
              </li>
            ))}
          </ul>
        </Card>
      ) : null}
      {impact.unanalyzed.length > 0 ? (
        <Card className="border-[color:var(--warn-border)]">
          <h3 className="mb-2 text-sm font-medium text-foreground">Could not be analyzed</h3>
          <p className="mb-2 text-xs text-muted-foreground">
            These are named, not omitted — an absent entry would read as a clean result.
          </p>
          <ul className="flex flex-col gap-2">
            {impact.unanalyzed.map((u) => (
              <li key={u.node_id} className="text-sm">
                <Chip tone="warn">{u.node_id}</Chip> <span className="text-muted-foreground">{u.why}</span>
              </li>
            ))}
          </ul>
        </Card>
      ) : null}
    </div>
  );
}

/** RuntimeChangeableStatement states per node which facts are runtime-changeable and which need a new
 *  change (task 4.10). Every node here defaults to inline apply mode, so the honest statement is that
 *  changing any configured fact requires a new source change. */
function RuntimeChangeableStatement() {
  return (
    <Card>
      <h3 className="mb-2 text-sm font-medium text-foreground">What is runtime-changeable</h3>
      <p className="text-sm text-muted-foreground">
        These nodes use <span className="font-mono">inline</span> apply mode, so changing any configured
        fact needs a new source change.
      </p>
      <ReadOn href={AXIS_DOC.model}>Bound and inline, and what each one lets you change</ReadOn>
    </Card>
  );
}
