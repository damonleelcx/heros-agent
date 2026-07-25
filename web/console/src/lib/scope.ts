import "server-only";
import type { Session } from "./session";

/**
 * scope.ts derives every upstream path from the SESSION, never from client input (NFR12, FR-scope).
 *
 * # Why the paths are built here rather than at each call site
 *
 * The rule is that a tenant identifier supplied by the client cannot widen, change or override the
 * session's scope. Stated as a rule it is obeyed until somebody adds a call site in a hurry. Stated
 * as a MODULE — where every upstream path is constructed from a `Session` and the caller has no way
 * to pass a tenant — it is obeyed structurally, because there is no parameter to get wrong.
 *
 * Note what this does and does not claim. The platform's customer routes are keyed by subject
 * (`run_id`, `workflow_id`, `variant_id`), not by tenant, so tenant isolation is ultimately enforced
 * by the platform against the credential and the `X-Console-Tenant` header the forwarder attaches.
 * What this module guarantees is the console's half: the tenant on that header is the session's, and
 * a client cannot influence it. Saying that precisely matters — a comment claiming the console
 * enforces isolation would be a claim about somebody else's code.
 *
 * # Subject identifiers ARE client input, and that is fine
 *
 * A `run_id` in a URL is a subject, not an authority. The client may name any subject it likes; the
 * platform decides whether this tenant may see it, and answers 404 when it may not. That is the
 * correct split — the console must not pretend to adjudicate access it cannot see the rules for, and
 * a 404 for "not yours" is deliberately indistinguishable from "does not exist".
 */

/** encode makes a subject identifier safe as a single path segment. */
function encode(segment: string): string {
  return encodeURIComponent(segment);
}

/**
 * ScopedPaths is the whole set of upstream routes the console reads, bound to one session.
 *
 * It is a closed set on purpose: adding a route to the console means adding it here, in front of a
 * reviewer, rather than assembling a string inline three files away.
 */
export function scoped(session: Session) {
  // `tenantId` is captured, not exposed as a parameter. Every call below inherits it and no caller
  // can supply a different one.
  const tenantId = session.tenantId;

  return {
    tenantId,

    // ── P2 · configure / diff / run ────────────────────────────────────────
    run: (runId: string) => `/api/p2/runs/${encode(runId)}`,
    transform: (configHash: string, sourceRevision: string) =>
      `/api/p2/transforms/${encode(configHash)}/${encode(sourceRevision)}`,
    resolveSpec: () => `/api/p2/specs/resolve`,
    submitSpec: () => `/api/p2/specs/submit`,

    // ── P10 · prompt & model studio ───────────────────────────────────────
    // The prompt name is a SUBJECT (like a run_id), not an authority: the platform scopes it to this
    // tenant server-side, and answers with only this tenant's prompts. `publish`, `impact` and
    // `preview` are the studio's write/compute actions; `names`, `timeline` and `diff` are reads.
    promptNames: () => `/api/p10/prompts`,
    promptTimeline: (name: string) => `/api/p10/prompts/${encode(name)}/timeline`,
    promptDiff: (a: string, b: string) => `/api/p10/prompts/diff?a=${encode(a)}&b=${encode(b)}`,
    publishPrompt: () => `/api/p10/prompts/publish`,
    promptImpact: () => `/api/p10/prompts/impact`,
    studioPreview: () => `/api/p10/studio/preview`,
    // Matrix surface: rows (models), columns (a workflow's nodes), test-run and bind a cell.
    studioWorkflows: () => `/api/p10/workflows`,
    studioModels: () => `/api/p10/models`,
    studioNodes: (workflowId: string) => `/api/p10/workflows/${encode(workflowId)}/nodes`,
    studioWorkflowBindings: (workflowId: string) => `/api/p10/workflows/${encode(workflowId)}/bindings`,
    studioRun: () => `/api/p10/studio/run`,
    studioBind: () => `/api/p10/studio/bind`,

    // ── P2.5 · live run monitor ───────────────────────────────────────────
    monitor: (runId: string) => `/api/p25/runs/${encode(runId)}/monitor`,
    monitorStream: (runId: string) => `/api/p25/runs/${encode(runId)}/monitor/stream`,

    // ── P3.5 · pattern-classified graph ───────────────────────────────────
    graph: (workflowId: string) => `/api/p35/workflows/${encode(workflowId)}/graph`,

    // ── P4 · eval board ───────────────────────────────────────────────────
    // `profile` is VIEW STATE, not identity: it selects a weight profile on a board whose subject is
    // already fixed. It is forwarded because the platform owns the profile list and the console must
    // not invent one — the board response carries `profiles` for exactly that reason.
    board: (workflowId: string, profile?: string) =>
      `/api/p4/workflows/${encode(workflowId)}/board` + (profile ? `?profile=${encode(profile)}` : ""),

    // ── P4.5 · attribution scorecard ──────────────────────────────────────
    scorecard: (variantId: string) => `/api/p45/variants/${encode(variantId)}/scorecard`,

    // ── P5.5 · proposals and their verified deltas ────────────────────────
    proposals: (workflowId: string) => `/api/p55/workflows/${encode(workflowId)}/surface`,
    openPR: (workflowId: string, proposalId: string) =>
      `/api/p55/workflows/${encode(workflowId)}/proposals/${encode(proposalId)}/open-pr`,

    // ── P7 · plan, entitlements, spend ────────────────────────────────────
    // The ONLY route keyed by the tenant itself — and it takes the session's tenant, which is the
    // whole point of this module. A customer id from the client would be a scope-widening attempt.
    billing: (period?: string) =>
      `/api/p7/customers/${encode(tenantId)}/billing` + (period ? `?period=${encode(period)}` : ""),

    // ── P12 · forge delivery state ────────────────────────────────────────
    // Tenant-scoped server-side from the session's credential + X-Console-Tenant header, like every
    // other read. The console never sends a customer id in the path.
    deliveries: () => `/api/p12/deliveries`,
  };
}

export type Scoped = ReturnType<typeof scoped>;
