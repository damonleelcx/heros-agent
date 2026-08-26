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
 * A path like `/api/v1/runs/run-8f3c/…` carries a subject id. On its own that is not content, but a
 * log aggregator holding every path a tenant ever opened is a behavioural record nobody asked for and
 * nobody owns. Paths are reduced to their TEMPLATE — `/api/v1/runs/{id}` — which is what a latency or
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
// 🔴 These patterns match what `scope.ts` EMITS, which is `/api/v1/…`. Every entry here once matched
// `/api/pNN/…`, and when the paths moved to `/api/v1` this map was left behind — so every matcher was
// dead and every upstream call logged as `/unknown`, the outcome the comment above calls visibly
// wrong. It was invisible precisely because `/unknown` is a valid value that never throws. Adding an
// upstream route means adding a line here; `platform-routes.test.mjs` fails when one is missing.
const PATH_TEMPLATES: Array<[RegExp, string]> = [
  // P20 — the install/distribution contract, read by the PUBLIC install surface. Listed so its upstream
  // calls are searchable; an unlisted route logs as /unknown, which this file's own comment calls visibly
  // wrong, and the first real load of the install page produced exactly that.
  [/^\/api\/v1\/install$/, "/api/v1/install"],
  [/^\/api\/v1\/runs\/[^/]+$/, "/api/v1/runs/{run_id}"],
  [/^\/api\/v1\/runs\/[^/]+\/link$/, "/api/v1/runs/{run_id}/link"],
  [/^\/api\/v1\/transforms\/[^/]+\/[^/]+$/, "/api/v1/transforms/{config_hash}/{source_revision}"],
  [/^\/api\/v1\/specs\/resolve$/, "/api/v1/specs/resolve"],
  [/^\/api\/v1\/specs\/submit$/, "/api/v1/specs/submit"],
  [/^\/api\/v1\/runs\/[^/]+\/monitor$/, "/api/v1/runs/{run_id}/monitor"],
  [/^\/api\/v1\/runs\/[^/]+\/monitor\/stream$/, "/api/v1/runs/{run_id}/monitor/stream"],
  [/^\/api\/v1\/workflows\/[^/]+\/pattern-graph$/, "/api/v1/workflows/{workflow_id}/pattern-graph"],
  [/^\/api\/v1\/workflows\/[^/]+\/eval-board/, "/api/v1/workflows/{workflow_id}/eval-board"],
  // P30 §1.12 — the eval set behind the board's denominator. Anchored, so it cannot be shadowed by the
  // eval-board matcher above (which is deliberately unanchored to absorb the `?profile=` query).
  [/^\/api\/v1\/workflows\/[^/]+\/eval-set$/, "/api/v1/workflows/{workflow_id}/eval-set"],
  [/^\/api\/v1\/variants\/[^/]+\/scorecard$/, "/api/v1/variants/{variant_id}/scorecard"],
  // Ordered before the open-PR pattern below only for readability; the two cannot both match, because
  // this one is anchored and that one has two more segments.
  [/^\/api\/v1\/workflows\/[^/]+\/proposals$/, "/api/v1/workflows/{workflow_id}/proposals"],
  // P30 §1.8 — the FLAT generation action. Flat so it can be published `Exact`; listed here so its
  // latency and error rate are queryable, which for a POST that runs a read-heavy pass is the one
  // signal that says whether the button is worth pressing.
  [/^\/api\/v1\/proposal-generations$/, "/api/v1/proposal-generations"],
  [/^\/api\/v1\/workflows\/[^/]+\/proposals\/[^/]+\/open-pr$/, "/api/v1/workflows/{workflow_id}/proposals/{proposal_id}/open-pr"],
  [/^\/api\/v1\/workflows\/[^/]+\/nodes$/, "/api/v1/workflows/{workflow_id}/nodes"],
  [/^\/api\/v1\/workflows\/[^/]+\/bindings$/, "/api/v1/workflows/{workflow_id}/bindings"],
  [/^\/api\/v1\/workflows$/, "/api/v1/workflows"],
  // P29 §4 — the subject index. Listed so their latency and error rates are queryable: an unlisted
  // route logs as /unknown, and a picker that is slow or failing is exactly the thing this console has
  // no other way to notice.
  [/^\/api\/v1\/variants$/, "/api/v1/variants"],
  [/^\/api\/v1\/transforms$/, "/api/v1/transforms"],
  [/^\/api\/v1\/link-coverage$/, "/api/v1/link-coverage"],
  // P32 — the source surface. Listed so its latency and error rates are queryable, and one of the
  // three matters more than the other two: `repo-connections` is the POST that creates a STANDING READ
  // GRANT, so "how often does this refuse, and how long does it take" is a question somebody will ask
  // during a security review rather than during an incident. An unlisted route answers it with
  // `/unknown`.
  [/^\/api\/v1\/repo-connections$/, "/api/v1/repo-connections"],
  // Anchored to the bare path: `scope.ts` appends `?connection_id=…`, and `template()` strips the
  // query before matching, so the anchor is safe and keeps this from shadowing anything.
  [/^\/api\/v1\/repo-connection-reads$/, "/api/v1/repo-connection-reads"],
  [/^\/api\/v1\/repo-connection-revocations$/, "/api/v1/repo-connection-revocations"],
  [/^\/api\/v1\/local-pairings$/, "/api/v1/local-pairings"],
  // P33 — the assessment. ONE template for both methods, because the path is one path: `GET` reads the
  // latest report and `POST` runs one. Anchored to the bare path; `scope.ts` appends `?workflow_id=…`
  // and `template()` strips the query before matching.
  //
  // 🔴 This is the matcher that matters most in the file, and for a reason none of the others has: the
  // POST SPENDS the platform's own provider money. "How often is this called, how long does it take,
  // and how often does it refuse" is a question somebody asks while looking at a provider bill, and an
  // unlisted route answers it with `/unknown`.
  [/^\/api\/v1\/assessments$/, "/api/v1/assessments"],
  // P35 — the improvement run. THREE flat routes, three matchers, and the reason each is here differs.
  //
  // 🔴 `/api/v1/improvement-runs` is the one an unlisted entry would hurt worst, for the assessment
  // route's reason with more money behind it: the POST runs a bounded search that can spend several
  // dollars of the platform's own provider budget, and it is also the slowest call this console makes.
  // "How long does a run take and how often does it refuse" is asked while looking at a bill.
  //
  // `/api/v1/improvement-plans` is FREE, and that is exactly why it is measured separately. Plans
  // created versus runs started is the product signal that says whether the disclosure threshold is
  // stopping people — a divergence between the two is invisible if only the expensive call is counted.
  //
  // `/api/v1/improvement-decisions` is the one that authorizes a WRITE to a customer's repository. Its
  // refusal rate is a security signal, not a performance one.
  [/^\/api\/v1\/improvement-plans$/, "/api/v1/improvement-plans"],
  [/^\/api\/v1\/improvement-runs$/, "/api/v1/improvement-runs"],
  [/^\/api\/v1\/improvement-decisions$/, "/api/v1/improvement-decisions"],
  // P31 — the conversational console. Five FLAT routes, five matchers.
  //
  // 🔴 `conversation-stream` matters most of the five and is the one an unlisted entry would hurt worst:
  // it is the only long-lived connection this console opens, so its error rate is the ONE signal that
  // distinguishes "streaming works" from "the stream dies every ninety seconds and the client hides it
  // by reconnecting". Logged as /unknown, that signal is unqueryable and the failure is invisible —
  // which is exactly the shape design.md D3 warns about for buffering proxies, one layer up.
  [/^\/api\/v1\/conversations$/, "/api/v1/conversations"],
  [/^\/api\/v1\/conversation-turns$/, "/api/v1/conversation-turns"],
  [/^\/api\/v1\/conversation-approvals$/, "/api/v1/conversation-approvals"],
  [/^\/api\/v1\/conversation-trace/, "/api/v1/conversation-trace"],
  [/^\/api\/v1\/conversation-stream/, "/api/v1/conversation-stream"],
  [/^\/api\/v1\/workflows\/[^/]+\/axis-projection$/, "/api/v1/workflows/{workflow_id}/axis-projection"],
  [/^\/api\/v1\/workflows\/[^/]+\/delivery-projection$/, "/api/v1/workflows/{workflow_id}/delivery-projection"],
  [/^\/api\/v1\/models$/, "/api/v1/models"],
  [/^\/api\/v1\/prompts$/, "/api/v1/prompts"],
  [/^\/api\/v1\/prompts\/diff/, "/api/v1/prompts/diff"],
  [/^\/api\/v1\/prompts\/impact$/, "/api/v1/prompts/impact"],
  [/^\/api\/v1\/prompts\/publish$/, "/api/v1/prompts/publish"],
  [/^\/api\/v1\/prompts\/[^/]+\/timeline$/, "/api/v1/prompts/{name}/timeline"],
  [/^\/api\/v1\/studio\/(preview|run|bind)$/, "/api/v1/studio/{action}"],
  [/^\/api\/v1\/customers\/[^/]+\/billing/, "/api/v1/customers/{customer_id}/billing"],
  [/^\/api\/v1\/customers\/[^/]+\/payment$/, "/api/v1/customers/{customer_id}/payment"],
  [/^\/api\/v1\/customers\/[^/]+\/plan$/, "/api/v1/customers/{customer_id}/plan"],
  [/^\/api\/v1\/customers\/[^/]+\/checkout-session$/, "/api/v1/customers/{customer_id}/checkout-session"],
  [/^\/api\/v1\/coverage$/, "/api/v1/coverage"],
  [/^\/api\/v1\/change-delivery$/, "/api/v1/change-delivery"],
  // P27 — the organization, its people, and the work it owns. Every one of these is a fixed path with
  // no subject in it, which is why they are exact matches rather than patterns: the scope comes from the
  // credential, so there is no id to wildcard.
  [/^\/api\/v1\/runs$/, "/api/v1/runs"],
  [/^\/api\/v1\/organization$/, "/api/v1/organization"],
  [/^\/api\/v1\/organization\/members$/, "/api/v1/organization/members"],
  [/^\/api\/v1\/organization\/members\/[^/]+\/removal-preview$/, "/api/v1/organization/members/{user_id}/removal-preview"],
  [/^\/api\/v1\/organization\/members\/[^/]+\/role$/, "/api/v1/organization/members/{user_id}/role"],
  [/^\/api\/v1\/organization\/members\/[^/]+$/, "/api/v1/organization/members/{user_id}"],
  [/^\/api\/v1\/organization\/invitations\/[^/]+$/, "/api/v1/organization/invitations/{invitation_id}"],
  [/^\/api\/v1\/organization\/invitations$/, "/api/v1/organization/invitations"],
  [/^\/api\/v1\/organization\/credentials\/[^/]+$/, "/api/v1/organization/credentials/{credential_id}"],
  [/^\/api\/v1\/organization\/credentials$/, "/api/v1/organization/credentials"],
  [/^\/api\/v1\/organization\/close$/, "/api/v1/organization/close"],
  [/^\/api\/v1\/deliveries$/, "/api/v1/deliveries"],
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
