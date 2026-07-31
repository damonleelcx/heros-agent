import "server-only";

/**
 * telemetry.ts is the BFF's structured log surface (FR26, NFR11).
 *
 * # The rule that shapes every function here
 *
 * **Nothing that could be content is logged.** Not a prompt, not a diff, not a Variant Spec, not a
 * request body, not a response body, not a credential — on the error path as well as the success
 * path, which is where this rule is usually broken. The tempting line is `console.error(err, body)`
 * during a debugging session, and it survives into production because it never fails anything.
 *
 * What IS logged is the shape of the request: method, path template, status, failure class, duration,
 * and the platform's `trace_id` so a single request can be followed browser → BFF → platform. Those
 * are enough to diagnose a fault and insufficient to reconstruct a customer's intellectual property,
 * which is the correct trade for a surface that renders prompts and diffs for a living.
 *
 * # Why identifiers are redacted out of the path
 *
 * A path like `/api/p2/runs/run-8f3c/…` carries a subject id. On its own that is not content, but a
 * log aggregator holding every path a tenant ever opened is a behavioural record nobody asked for and
 * nobody owns. Paths are reduced to their TEMPLATE — `/api/p2/runs/{id}` — which is what a latency or
 * error-rate question actually needs.
 */

type UpstreamEvent = {
  method: string;
  path: string;
  status: number;
  kind: string;
  ms: number;
  traceId?: string;
};

/**
 * PATH_TEMPLATES reduces a concrete path to its route template.
 *
 * An allowlist rather than a heuristic: a regex that "looks like an id" will eventually decide a
 * meaningful path segment is an id and log a template nobody can search for. If a new upstream route
 * is added and does not match here, it is logged as `/unknown` — visibly wrong, which is the failure
 * mode to prefer over silently logging an identifier.
 */
const PATH_TEMPLATES: Array<[RegExp, string]> = [
  // P20 — the install/distribution contract, read by the PUBLIC install surface. Listed so its upstream
  // calls are searchable; an unlisted route logs as /unknown, which this file's own comment calls visibly
  // wrong, and the first real load of the install page produced exactly that.
  [/^\/api\/p20\/install$/, "/api/p20/install"],
  [/^\/api\/p2\/runs\/[^/]+$/, "/api/p2/runs/{run_id}"],
  [/^\/api\/p2\/transforms\/[^/]+\/[^/]+$/, "/api/p2/transforms/{config_hash}/{source_revision}"],
  [/^\/api\/p2\/specs\/resolve$/, "/api/p2/specs/resolve"],
  [/^\/api\/p2\/specs\/submit$/, "/api/p2/specs/submit"],
  [/^\/api\/p25\/runs\/[^/]+\/monitor$/, "/api/p25/runs/{run_id}/monitor"],
  [/^\/api\/p25\/runs\/[^/]+\/monitor\/stream$/, "/api/p25/runs/{run_id}/monitor/stream"],
  [/^\/api\/p35\/workflows\/[^/]+\/graph$/, "/api/p35/workflows/{workflow_id}/graph"],
  [/^\/api\/p4\/workflows\/[^/]+\/board/, "/api/p4/workflows/{workflow_id}/board"],
  [/^\/api\/p45\/variants\/[^/]+\/scorecard$/, "/api/p45/variants/{variant_id}/scorecard"],
  [/^\/api\/p55\/workflows\/[^/]+\/surface$/, "/api/p55/workflows/{workflow_id}/surface"],
  [/^\/api\/p55\/workflows\/[^/]+\/proposals\/[^/]+\/open-pr$/, "/api/p55/workflows/{workflow_id}/proposals/{proposal_id}/open-pr"],
  [/^\/api\/p7\/customers\/[^/]+\/billing/, "/api/p7/customers/{customer_id}/billing"],
  [/^\/api\/p13\/coverage$/, "/api/p13/coverage"],
  [/^\/healthz$/, "/healthz"],
  [/^\/readyz$/, "/readyz"],
];

/** template reduces a concrete upstream path to its route template, or `/unknown`. */
export function template(path: string): string {
  const bare = path.split("?")[0];
  for (const [pattern, name] of PATH_TEMPLATES) {
    if (pattern.test(bare)) return name;
  }
  return "/unknown";
}

/**
 * logUpstream records one upstream call.
 *
 * One line, JSON, on stdout — the shape a log collector reads without a parser of its own. The event
 * name is a constant so it can be aggregated; the failure `kind` is the taxonomy, so "the classifier
 * is not mounted on this deployment" and "the platform is unreachable" do not become one alert.
 */
export function logUpstream(event: UpstreamEvent): void {
  emit({
    event: "console.upstream",
    method: event.method,
    route: template(event.path),
    status: event.status,
    kind: event.kind,
    duration_ms: event.ms,
    trace_id: event.traceId,
  });
}

/**
 * logSession records a session lifecycle event.
 *
 * The session id is logged; the assertion that produced it is NOT, and neither is anything derived
 * from it. A denial is logged without recording any credential value — which is the explicit
 * requirement in the revoked-session scenario, and the case where a well-meaning "log what was
 * presented so we can debug it" does the damage.
 */
export function logSession(event: {
  action: "issued" | "denied" | "revoked" | "expired";
  sessionId?: string;
  tenantId?: string;
  reason?: string;
}): void {
  emit({
    event: "console.session",
    action: event.action,
    session_id: event.sessionId,
    tenant_id: event.tenantId,
    reason: event.reason,
  });
}

/**
 * IdentityEvent is the CENTRAL enum of identity outcomes (P22 tasks 2.2 / 5.1, 🔴 logging-conventions).
 *
 * A union rather than a free string, for the reason that principle gives: an `event.name` typed at
 * three call sites is three names, and a monitor alerting on "unmapped identity" then silently stops
 * matching the day somebody writes `identity_unmapped` instead. These are the names an operator's
 * alert rule is allowed to depend on.
 */
export type IdentityEvent =
  /** A federated identity resolved to a tenant and a session was issued. */
  | "sign_in"
  /** A verified identity matched no mapping rule. A SECURITY EVENT, not a signup (NFR9). */
  | "unmapped_identity"
  /** An identity was provisioned into a tenant under an explicit JIT allow rule. */
  | "jit_provisioned"
  /** The assertion did not verify — signature, issuer, audience, nonce, or freshness. */
  | "assertion_refused"
  /** A callback failed its CSRF/replay guards: missing/forged/replayed `state`, or a spent assertion. */
  | "replay_refused"
  /** A redirect or ACS target was not on the allowlist. */
  | "redirect_refused"
  /** The IdP could not be reached or its metadata could not be validated — fail closed, no session. */
  | "idp_unreachable"
  /** An identity secret could not be sourced. Fail closed. */
  | "secret_unavailable";

/**
 * logIdentity records one identity-path outcome.
 *
 * # What is deliberately not a parameter
 *
 * There is no field for the assertion, the ID token, the authorization code, the PKCE verifier, the
 * `state`, or the email. NFR2 says the assertion is never logged, and the way that rule survives a
 * debugging session at 2am is that the function has nowhere to put it. What IS recorded is the shape
 * of the refusal — which guard, which issuer, which tenant — because an operator investigating "who
 * is failing to sign in" needs exactly that and nothing more.
 *
 * `cause` is the SERVER-SIDE detail. The browser is told one generic reason (`federation.REFUSAL`);
 * the cause lands here, where an operator can read it and an attacker cannot.
 */
export function logIdentity(event: {
  event: IdentityEvent;
  provider?: string;
  issuer?: string;
  tenantId?: string;
  cause?: string;
}): void {
  emit({
    event: "console.identity",
    outcome: event.event,
    provider: event.provider,
    issuer: event.issuer,
    tenant_id: event.tenantId,
    cause: event.cause,
  });
}

function emit(fields: Record<string, unknown>): void {
  const line: Record<string, unknown> = { ts: new Date().toISOString() };
  for (const [k, v] of Object.entries(fields)) {
    if (v !== undefined) line[k] = v;
  }
  // eslint-disable-next-line no-console -- stdout IS the log transport for a containerised process.
  console.log(JSON.stringify(line));
}
