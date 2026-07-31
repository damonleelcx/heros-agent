import { identityHealth, identitySecretsSource } from "@/lib/identity";
import { platformApiBase, upstreamTimeoutMs } from "@/lib/platformApi";

/**
 * The console component's machine-readable health (FR25, 🔴 `health-signal-surface`).
 *
 * # Why this endpoint exists separately from the rendered console
 *
 * A UI dashboard is never a health judgement. An operator or a probe asking "is the console up" must
 * get an answer from a machine-readable endpoint, on the box that is misbehaving, now — not by
 * loading a page and looking at it. The platform's `/readyz` reads THIS route and reports not-ready
 * when it cannot, naming the console as the degraded component, so a healthy Go service in front of a
 * dead console cannot report ready.
 *
 * # What it reports, and what it deliberately does not
 *
 * It reports the console process's own liveness and its CONFIGURATION IDENTITY: which platform origin
 * it is pointed at, which identity provider is in force, and whether a credential is configured
 * (**a boolean, never the value**). It does NOT probe the platform API. Probing upstream on every
 * health check would make a platform latency spike look like a console outage — the opposite of the
 * signal being asked for — and the platform's own readiness already covers the platform.
 *
 * # Why it DOES probe the identity provider (P22 task 7.1)
 *
 * The IdP is the one upstream this console genuinely depends on for its primary function, and the
 * exception is deliberate rather than an inconsistency with the paragraph above. The distinction is
 * which way the failure runs: a slow platform API makes pages slow, while an unreachable IdP means
 * **nobody can sign in at all**, and the platform's `/readyz` reads this block to report
 * `identity_provider` as its own named component.
 *
 * It measures REACHABILITY, not traffic — a console with no logins all night is not unhealthy — and it
 * does not depend on the traffic it gates, so readiness cannot deadlock on the very sign-ins it admits.
 *
 * `configured` and `dev` federate with nobody and are always reachable: the statement is "this
 * console's identity mechanism is serviceable", and for a static map it always is. Reporting `false`
 * there would page an operator about a dependency the deployment does not have.
 *
 * It is public because it carries no tenant data and because a health endpoint behind authentication
 * cannot be probed by the thing that most needs to probe it.
 */
export const dynamic = "force-dynamic";

export async function GET() {
  // The KIND, the ISSUER and the verdict — never a client id, never an allowlist, never a secret's
  // logical name. This endpoint is public by necessity, so everything it says is said to everybody.
  const identity = await identityHealth();
  return Response.json(
    {
      component: "console",
      status: "ok",
      // An origin, not a credential. It is here because "the console is up but pointed at the wrong
      // platform" is a real incident that is otherwise diagnosed by reading someone's shell history.
      platform_api_base: platformApiBase(),
      identity_provider: identity,
      // Which SOURCE identity credentials resolve from, so the claim is checkable from the running
      // system rather than from a manifest (secrets-baseline.md §1.1). The source, never a secret.
      identity_secrets_source: identitySecretsSource(),
      credential_configured: Boolean(process.env.CONSOLE_PLATFORM_CREDENTIAL),
      upstream_timeout_ms: upstreamTimeoutMs(),
    },
    { headers: { "cache-control": "no-store" } },
  );
}
