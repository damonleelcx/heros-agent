import { adminIdentityMode } from "@/lib/identity";

/**
 * The operator console component's machine-readable health (P19 admin-console-deploy; 🔴
 * `health-signal-surface`).
 *
 * # Why this endpoint exists separately from the rendered console
 *
 * A UI dashboard is never a health judgement. A probe asking "is the operator console up" must get an
 * answer from a machine-readable endpoint, on the box that is misbehaving, now — not by loading a page
 * and looking at it. The platform's `/readyz` reads THIS route (wired as `admin_console`) and reports
 * not-ready when it cannot, naming the operator console as the degraded component, so a healthy
 * platform in front of a dead operator console cannot report ready.
 *
 * # What it reports, and what it deliberately does not
 *
 * It reports the process's own liveness and its CONFIGURATION IDENTITY: which admin API it is pointed
 * at, which identity mode is in force, and whether its credential is configured (**a boolean, never
 * the value**). It does NOT probe the admin API — probing upstream on every health check would make an
 * admin-API latency spike read as a console outage, and the platform's own readiness already covers
 * the admin API. It is a DISTINCT origin from the customer console's `/api/health`, on its own port.
 *
 * It is public because it carries no tenant data and because a health endpoint behind authentication
 * cannot be probed by the thing that most needs to probe it.
 */
export const dynamic = "force-dynamic";

export function GET() {
  return Response.json(
    {
      component: "admin_console",
      status: "ok",
      // An origin, not a credential. "The operator console is up but pointed at the wrong admin API"
      // is a real incident otherwise diagnosed by reading someone's shell history.
      admin_api_base: process.env.ADMIN_API_BASE ?? "http://127.0.0.1:4311",
      identity_mode: adminIdentityMode(),
      credential_configured: Boolean(process.env.ADMIN_PLATFORM_CREDENTIAL),
    },
    { headers: { "cache-control": "no-store" } },
  );
}
