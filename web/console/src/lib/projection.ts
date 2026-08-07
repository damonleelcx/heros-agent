import "server-only";
import { load } from "./view";
import { listWorkflows } from "./enumeration";
import type { Projection, ProjectionOutcome } from "@/components/axisProjection";

/**
 * projection.ts reads `coverage × your nodes` for whichever workflow the reader most recently reported.
 *
 * # Why the axis surfaces pick a workflow rather than asking for one
 *
 * `/app/wiring`, `/app/context`, `/app/memory`, `/app/harness`, `/app/coverage`, `/app/authoring` and
 * `/app/delivery` are AXIS pages: their subject is the axis, not a workflow. Putting a workflow picker
 * on each of them would make seven surfaces ask a question the reader did not come there with — and
 * `interaction-simplicity-first` says the input you can remove is the one you remove.
 *
 * So the panel projects the most recently reported workflow, names it, and links to it. A reader with
 * several picks a different one from `/app/workflows`; a reader with one — the common case — is asked
 * nothing at all.
 *
 * # 🔴 §5.11 — three transport treatments, and no 404 becomes a business state
 *
 * `not-mounted` is a policy answer. `read-failed` is ours. `not-reported` is the customer's own
 * boundary, and it arrives as a 200 carrying that word — never as a 404, because a 404 would be
 * indistinguishable from a transport failure and would send the reader to look for a broken deployment
 * when the truth is that they have not opted in.
 */

type ProjectionEnvelope = { state?: string; projection?: Projection; detail?: string; fill_with?: string };

/**
 * loadProjection resolves the workflow to project and reads it.
 *
 * The enumeration's own three states pass straight through: a reader whose workflow list could not be
 * read must not be told they have reported nothing.
 */
export async function loadProjection(): Promise<ProjectionOutcome> {
  const workflows = await listWorkflows();
  if (workflows.state === "not-mounted") {
    return { state: "not-mounted" };
  }
  if (workflows.state === "read-failed") {
    return { state: "read-failed", detail: workflows.detail };
  }
  if (workflows.subjects.length === 0) {
    return {
      state: "not-reported",
      detail:
        "This organization has reported no workflow structure at all, so there is nothing to cross this table with.",
      fill_with: "heros link --with-ir",
    };
  }

  const workflowId = workflows.subjects[0].id;
  const { outcome } = await load<ProjectionEnvelope>(
    (paths) => paths.axisProjection(workflowId),
  );
  if (!outcome.ok) {
    if (outcome.kind === "not-mounted") return { state: "not-mounted", detail: outcome.error };
    return { state: "read-failed", detail: outcome.error };
  }
  const body = outcome.data;
  if (body.state === "not-reported" || !body.projection) {
    return {
      state: "not-reported",
      workflow_id: workflowId,
      detail: body.detail,
      fill_with: body.fill_with ?? "heros link --with-ir",
    };
  }
  return { state: "ok", projection: body.projection };
}

/** DeliveryProjection is the delivery table crossed with this tenant's nodes. */
export type DeliveryProjection = {
  workflow_id: string;
  undeliverable: number;
  reported_cells: number;
  cells: number;
  node_count: number;
  coverage_version: string;
  stale: boolean;
  rows: {
    node_id: string;
    symbol?: string;
    language?: string;
    axis: string;
    deliverable: "source" | "runtime" | "both" | "neither" | "not-reported";
    cause?: string;
    owner?: string;
  }[];
};

export type DeliveryProjectionOutcome =
  | { state: "ok"; projection: DeliveryProjection }
  | { state: "not-reported"; detail?: string; fill_with?: string }
  | { state: "read-failed"; detail?: string }
  | { state: "not-mounted"; detail?: string };

/** loadDeliveryProjection reads the delivery-route projection, with its `undeliverable` count. */
export async function loadDeliveryProjection(): Promise<DeliveryProjectionOutcome> {
  const workflows = await listWorkflows();
  if (workflows.state === "not-mounted") return { state: "not-mounted" };
  if (workflows.state === "read-failed") return { state: "read-failed", detail: workflows.detail };
  if (workflows.subjects.length === 0) {
    return {
      state: "not-reported",
      detail: "This organization has reported no workflow structure, so no node can be counted as undeliverable.",
      fill_with: "heros link --with-ir",
    };
  }
  const workflowId = workflows.subjects[0].id;
  const { outcome } = await load<DeliveryProjection & { state?: string; detail?: string }>(
    (paths) => paths.deliveryProjection(workflowId),
  );
  if (!outcome.ok) {
    if (outcome.kind === "not-mounted") return { state: "not-mounted", detail: outcome.error };
    return { state: "read-failed", detail: outcome.error };
  }
  if (outcome.data.state === "not-reported") {
    return { state: "not-reported", detail: outcome.data.detail, fill_with: "heros link --with-ir" };
  }
  return { state: "ok", projection: outcome.data };
}

/**
 * LinkCoverageOutcome is the three-state link coverage, readable with no plan, no account and no
 * invoice (P29 §7.2).
 *
 * 🔴 `unknown` is its own state and is never an `ok` with zeros. A spend figure at 100% coverage and one
 * whose denominator could not be read look identical as a number and mean opposite things.
 */
export type LinkCoverageOutcome =
  | { state: "ok"; complete: boolean; runsLinked: number; runsReported: number }
  | { state: "unknown" }
  | { state: "not-mounted" };

/** loadLinkCoverage reads the coverage, outside the billing view. */
export async function loadLinkCoverage(): Promise<LinkCoverageOutcome> {
  const { outcome } = await load<{
    known?: boolean;
    complete?: boolean;
    runs_linked?: number;
    runs_reported?: number;
    state?: string;
  }>((paths) => paths.linkCoverage());

  if (!outcome.ok) {
    if (outcome.kind === "not-mounted") return { state: "not-mounted" };
    // A transport failure is UNKNOWN coverage, not zero. The runs are linked either way; what we cannot
    // do is claim a denominator.
    return { state: "unknown" };
  }
  const body = outcome.data;
  if (body.known !== true || body.state === "unknown") return { state: "unknown" };
  return {
    state: "ok",
    complete: body.complete === true,
    runsLinked: body.runs_linked ?? 0,
    runsReported: body.runs_reported ?? 0,
  };
}
