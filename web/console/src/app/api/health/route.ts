import { identityProvider } from "@/lib/identity";
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
 * It is public because it carries no tenant data and because a health endpoint behind authentication
 * cannot be probed by the thing that most needs to probe it.
 */
export const dynamic = "force-dynamic";

export function GET() {
  return Response.json(
    {
      component: "console",
      status: "ok",
      // An origin, not a credential. It is here because "the console is up but pointed at the wrong
      // platform" is a real incident that is otherwise diagnosed by reading someone's shell history.
      platform_api_base: platformApiBase(),
      identity_provider: identityProvider(),
      credential_configured: Boolean(process.env.CONSOLE_PLATFORM_CREDENTIAL),
      upstream_timeout_ms: upstreamTimeoutMs(),
    },
    { headers: { "cache-control": "no-store" } },
  );
}
