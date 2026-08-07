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
    run: (runId: string) => `/api/v1/runs/${encode(runId)}`,
    transform: (configHash: string, sourceRevision: string) =>
      `/api/v1/transforms/${encode(configHash)}/${encode(sourceRevision)}`,
    resolveSpec: () => `/api/v1/specs/resolve`,
    submitSpec: () => `/api/v1/specs/submit`,

    // ── P10 · prompt & model studio ───────────────────────────────────────
    // The prompt name is a SUBJECT (like a run_id), not an authority: the platform scopes it to this
    // tenant server-side, and answers with only this tenant's prompts. `publish`, `impact` and
    // `preview` are the studio's write/compute actions; `names`, `timeline` and `diff` are reads.
    promptNames: () => `/api/v1/prompts`,
    promptTimeline: (name: string) => `/api/v1/prompts/${encode(name)}/timeline`,
    promptDiff: (a: string, b: string) => `/api/v1/prompts/diff?a=${encode(a)}&b=${encode(b)}`,
    publishPrompt: () => `/api/v1/prompts/publish`,
    promptImpact: () => `/api/v1/prompts/impact`,
    studioPreview: () => `/api/v1/studio/preview`,
    // Matrix surface: rows (models), columns (a workflow's nodes), test-run and bind a cell.
    studioWorkflows: () => `/api/v1/workflows`,
    studioModels: () => `/api/v1/models`,
    studioNodes: (workflowId: string) => `/api/v1/workflows/${encode(workflowId)}/nodes`,
    studioWorkflowBindings: (workflowId: string) => `/api/v1/workflows/${encode(workflowId)}/bindings`,
    studioRun: () => `/api/v1/studio/run`,
    studioBind: () => `/api/v1/studio/bind`,

    // ── P11 · a run the CLI transmitted, read back ────────────────────────
    // Distinct from `run` above on purpose. That one reads the EXECUTOR's record — attempt groups and
    // per-node I/O from a sandboxed run. A LINKED run is an allowlisted summary the developer's own
    // machine produced and transmitted; it has no per-node I/O and never will. Two subjects that share
    // an identifier, so two routes, and the page says which one it is showing.
    linkedRun: (runId: string) => `/api/v1/runs/${encode(runId)}/link`,

    // ── P2.5 · live run monitor ───────────────────────────────────────────
    monitor: (runId: string) => `/api/v1/runs/${encode(runId)}/monitor`,
    monitorStream: (runId: string) => `/api/v1/runs/${encode(runId)}/monitor/stream`,

    // ── P3.5 · pattern-classified graph ───────────────────────────────────
    //
    // 🔴 The leaf is `pattern-graph`, not `graph`. When these moved from `/api/p35/…` to `/api/v1/…`
    // the prefix was rewritten and the leaf was carried over, but the platform had renamed the leaf
    // after what the route DOES. The result was a 404, and a 404 classifies as `not-found`, so the
    // graph view of a workflow that exists rendered "No such workflow — the identifier does not
    // resolve". That is the one sentence this console must not print when the real cause is a
    // subsystem that is absent (which answers 503 and reads as "not mounted on this deployment").
    // `platform-routes.test.mjs` now fails on any path here that the Go mux does not register.
    graph: (workflowId: string) => `/api/v1/workflows/${encode(workflowId)}/pattern-graph`,

    // ── P4 · eval board ───────────────────────────────────────────────────
    // `profile` is VIEW STATE, not identity: it selects a weight profile on a board whose subject is
    // already fixed. It is forwarded because the platform owns the profile list and the console must
    // not invent one — the board response carries `profiles` for exactly that reason.
    board: (workflowId: string, profile?: string) =>
      `/api/v1/workflows/${encode(workflowId)}/eval-board` + (profile ? `?profile=${encode(profile)}` : ""),

    // ── P4.5 · attribution scorecard ──────────────────────────────────────
    scorecard: (variantId: string) => `/api/v1/variants/${encode(variantId)}/scorecard`,

    // ── P5.5 · proposals and their verified deltas ────────────────────────
    proposals: (workflowId: string) => `/api/v1/workflows/${encode(workflowId)}/proposals`,
    openPR: (workflowId: string, proposalId: string) =>
      `/api/v1/workflows/${encode(workflowId)}/proposals/${encode(proposalId)}/open-pr`,

    // ── P7 · plan, entitlements, spend ────────────────────────────────────
    // The ONLY route keyed by the tenant itself — and it takes the session's tenant, which is the
    // whole point of this module. A customer id from the client would be a scope-widening attempt.
    billing: (period?: string) =>
      `/api/v1/customers/${encode(tenantId)}/billing` + (period ? `?period=${encode(period)}` : ""),

    // ── P21 · payment collection ──────────────────────────────────────────
    // Tenant-keyed, like the P7 billing read and for the same reason: these are the only routes whose
    // subject IS the tenant, and the tenant comes from the session. A customer id from the client would
    // be a scope-widening attempt, and there is no parameter here that could carry one.
    payment: (period?: string) =>
      `/api/v1/customers/${encode(tenantId)}/payment` + (period ? `?period=${encode(period)}` : ""),
    checkoutSession: () => `/api/v1/customers/${encode(tenantId)}/checkout-session`,
    changePlan: () => `/api/v1/customers/${encode(tenantId)}/plan`,

    // ── P12 · forge delivery state ────────────────────────────────────────
    // Tenant-scoped server-side from the session's credential + X-Console-Tenant header, like every
    // other read. The console never sends a customer id in the path.
    deliveries: () => `/api/v1/deliveries`,

    // ── P13 13e · how an accepted change reaches a running agent ──────────
    // Plan-invariant and tenant-invariant: what a route can carry is a property of the CHANGE, not of
    // what someone paid. The handler takes no tenant at all; the session still scopes the call so
    // there is one call convention rather than two.
    deliveryRoutes: () => `/api/v1/change-delivery`,

    // ── P27 · the organization, its people, and the work it owns ──────────
    //
    // 🔴 Not one of these takes an organization. The platform reads it from the credential it verified,
    // and there is no parameter here a caller could put one in — which is the structural version of the
    // rule rather than a note asking somebody to keep it.
    //
    // `X-Console-Tenant` used to carry the scope and the platform never read it. It is deleted; see
    // `platformApi.ts`.
    organization: () => `/api/v1/organization`,
    members: () => `/api/v1/organization/members`,
    memberRole: (userId: string) => `/api/v1/organization/members/${encode(userId)}/role`,
    memberRemovalPreview: (userId: string) =>
      `/api/v1/organization/members/${encode(userId)}/removal-preview`,
    member: (userId: string) => `/api/v1/organization/members/${encode(userId)}`,
    invitations: () => `/api/v1/organization/invitations`,
    invitation: (invitationId: string) => `/api/v1/organization/invitations/${encode(invitationId)}`,
    credentials: () => `/api/v1/organization/credentials`,
    credential: (credentialId: string) => `/api/v1/organization/credentials/${encode(credentialId)}`,
    closeOrganization: () => `/api/v1/organization/close`,

    // The RUN COLLECTION — the question this API could not be asked before P27. `before` is a cursor
    // the platform hands back, never an offset the client computes: an offset page shifts under a
    // concurrent write and silently skips or repeats a row.
    runs: (opts?: { limit?: number; before?: string }) => `/api/v1/runs` + runQuery(opts),

    // ── The SUBJECT INDEX (P29 §4) ──────────────────────────────────────────────────────────────
    //
    // 🔴 These three are what `subjects.ts` opens by saying did not exist: *"the platform exposes no
    // enumeration endpoint for any of them"*. Every picker in this console offered only what THIS
    // BROWSER SESSION had already opened, so a developer who linked a run and came back the next day
    // found a console that had forgotten their workflow. The data was durable the whole time.
    //
    // Each takes NO organization parameter in any position: the scope is the verified principal's, and
    // there is nothing to get wrong.
    workflows: () => `/api/v1/workflows`,
    variants: () => `/api/v1/variants`,
    transforms: () => `/api/v1/transforms`,

    // P29 §5.8 — `coverage × your nodes`, and the delivery table crossed with them. Both are READS
    // computed at request time; neither is a stored projection, which would go stale the moment the
    // coverage table version moved and become a second source of truth for a refusal.
    axisProjection: (workflowId: string) => `/api/v1/workflows/${encode(workflowId)}/axis-projection`,
    deliveryProjection: (workflowId: string) => `/api/v1/workflows/${encode(workflowId)}/delivery-projection`,

    // P29 §7.2 — link coverage, OUTSIDE the billing view. It needs no plan, no account and no invoice:
    // it is the one number a link certainly produces, and it was unreadable for exactly as long as it
    // lived inside `BillingView`.
    linkCoverage: () => `/api/v1/link-coverage`,
  };
}

export type Scoped = ReturnType<typeof scoped>;

/**
 * runQuery builds the collection's query string.
 *
 * It is a function rather than an interpolation INSIDE the path literal, and that is not a style
 * choice: `tests/platform-routes.test.mjs` reads every `/api/v1/…` literal out of this file as TEXT and
 * checks it against the routes the platform registers. A template that interpolated the query would
 * present `/api/v1/runs${suffix ? ` to that scanner — a path no server answers, reported as a defect
 * that is really a parser artefact. Keeping the literal literal keeps the fence able to read it.
 */
function runQuery(opts?: { limit?: number; before?: string }): string {
  const query = new URLSearchParams();
  if (opts?.limit) query.set("limit", String(opts.limit));
  if (opts?.before) query.set("before", opts.before);
  const suffix = query.toString();
  return suffix ? `?${suffix}` : "";
}
