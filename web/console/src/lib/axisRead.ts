import "server-only";
import { load } from "./view";
import { listWorkflows } from "./enumeration";
import type { Projection } from "@/components/axisProjection";

/**
 * axisRead.ts reads ONE node's current value on each axis, plus the live per-node context coverage
 * (P37 FR1, FR17).
 *
 * # Why it reads the same endpoint `loadProjection` does
 *
 * Because the platform answers both from ONE read of ONE structure. `internal/api/axisprojection.go`
 * returns the P29 verdicts and the P37 values together, and its comment says why: two reads are two
 * chances to disagree about which nodes exist, and the reader would see a node in one list and not the
 * other with nothing on the screen to explain it.
 *
 * So this module is a second VIEW of one read, not a second read. `loadProjection` keeps its shape
 * unchanged for the callers that only want verdicts.
 *
 * # 🔴 The four states are carried through unflattened
 *
 * `not_connected`, `read_failed` and `not_mounted` mean three different things with three different next
 * actions, and `view.ts` exists so a caller cannot render one as another. A `not_measured` VALUE is a
 * fourth thing again — the transport succeeded and the platform answered honestly that it cannot say —
 * and it travels inside the payload rather than as a transport state.
 */

/** AxisState is the shared four-valued vocabulary (FR8). Never re-spelled per surface. */
export type AxisState = "measured" | "observed" | "not_measured" | "refused";

/** AxisValue is one node's current value on one axis, as the platform resolved it. */
export type AxisValue = {
  node_id: string;
  axis: string;
  state: AxisState;
  /** current is the value, present only when `state` is `observed`. Rendered verbatim. */
  current?: string;
  /** detail is one clause of context — the provider behind a model id, the language behind a policy. */
  detail?: string;
  /** missing_input names what would resolve this, present only when `state` is `not_measured`. */
  missing_input?: string;
  /** because is the sentence saying WHY the input is missing. Required beside `missing_input`. */
  because?: string;
};

export type NodeValues = {
  node_id: string;
  symbol?: string;
  file?: string;
  language?: string;
  values: AxisValue[];
};

/** PolicyCoverage is what the engine does with one context policy at a call site in one language. */
export type PolicyCoverage = { policy: string; mode: string; reason?: string };

export type AxisReadOutcome =
  | {
      state: "ok";
      workflow_id: string;
      coverage_version: string;
      nodes: NodeValues[];
      /** contextCoverage is keyed by language — never a single table a surface has to pick a row from. */
      contextCoverage: Record<string, PolicyCoverage[]>;
      coveredLanguages: string[];
      projection?: Projection;
    }
  | { state: "not_connected" }
  | { state: "read_failed"; detail?: string }
  | { state: "not_mounted"; detail?: string };

type Envelope = {
  state?: string;
  detail?: string;
  projection?: Projection;
  values?: { workflow_id: string; coverage_version: string; nodes: NodeValues[] };
  context_coverage?: Record<string, PolicyCoverage[]>;
  covered_languages?: string[];
};

/** loadAxisValues reads the per-node current values for the workflow the subject belongs to. */
export async function loadAxisValues(workflowId?: string): Promise<AxisReadOutcome> {
  let id = workflowId;
  if (!id) {
    const workflows = await listWorkflows();
    if (workflows.state === "not-mounted") return { state: "not_mounted", detail: workflows.detail };
    if (workflows.state === "read-failed") return { state: "read_failed", detail: workflows.detail };
    if (workflows.subjects.length === 0) return { state: "not_connected" };
    id = workflows.subjects[0].id;
  }

  const { outcome } = await load<Envelope>((paths) => paths.axisProjection(id as string));
  if (!outcome.ok) {
    if (outcome.kind === "not-mounted") return { state: "not_mounted", detail: outcome.error };
    return { state: "read_failed", detail: outcome.error };
  }
  const body = outcome.data;
  if (body.state === "not-reported" || !body.values) {
    // 🔴 A workflow whose structure was never reported has no node of the reader's to bind to, which is
    // the same position as no connection at all FROM AN AXIS SURFACE'S POINT OF VIEW. The connections
    // surface distinguishes them, because there the difference is the whole subject.
    return { state: "not_connected" };
  }
  return {
    state: "ok",
    workflow_id: body.values.workflow_id,
    coverage_version: body.values.coverage_version,
    nodes: body.values.nodes ?? [],
    contextCoverage: body.context_coverage ?? {},
    coveredLanguages: body.covered_languages ?? [],
    projection: body.projection,
  };
}

/**
 * valueFor picks one node's value on one axis out of a read.
 *
 * 🔴 Returns `null` only when the NODE is absent, never when the axis is. The platform sends every axis
 * for every node precisely so a surface cannot silently omit one — and a helper that collapsed "no such
 * node" into "no such axis" would let it.
 */
export function valueFor(read: AxisReadOutcome, nodeId: string, axis: string): AxisValue | null {
  if (read.state !== "ok") return null;
  const node = read.nodes.find((n) => n.node_id === nodeId);
  if (!node) return null;
  return node.values.find((v) => v.axis === axis) ?? null;
}

/** coverageForNode returns the live context coverage for a node's own language, or an empty list. */
export function coverageForNode(read: AxisReadOutcome, nodeId: string): PolicyCoverage[] {
  if (read.state !== "ok") return [];
  const node = read.nodes.find((n) => n.node_id === nodeId);
  if (!node?.language) return [];
  return read.contextCoverage[node.language] ?? [];
}
