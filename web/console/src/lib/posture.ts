import "server-only";
import { platformApiBase } from "./platformApi";

/**
 * posture.ts reads what the PLATFORM says about itself, from its own readiness surface.
 *
 * # Why the console asks rather than being told
 *
 * Self-serve sign-up is a deployment posture, declared on the platform and off by default. The console
 * could have been given the same environment variable — and then two places would hold the answer, which
 * is one more than can be right. `/readyz` reports it as a value precisely so nobody has to re-derive it.
 *
 * # 🔴 Unknown is not "on"
 *
 * Every failure — unreachable, non-200, malformed — resolves to `false`. A console that cannot ask does
 * not know, and "I do not know" must not render a registration form on a deployment that refuses one.
 * The cost of being wrong in this direction is a page that says "ask whoever runs this install"; in the
 * other it is a form that collects a name and then refuses, which teaches somebody the product is broken.
 */

type ReadyzBody = {
  account_system?: {
    store?: string;
    self_serve_signup?: boolean;
  };
};

/** SELF_SERVE_PROBE_TIMEOUT_MS bounds the probe. A page must not hang on a readiness check. */
const SELF_SERVE_PROBE_TIMEOUT_MS = Number(process.env.CONSOLE_UPSTREAM_TIMEOUT_MS ?? 5_000);

/**
 * selfServeEnabled reports whether this deployment creates organizations on request.
 *
 * `/readyz` is unauthenticated by design — it is a health surface — so this carries no credential. That
 * is deliberate: the sign-up page has no session, and a posture read that needed one could not run where
 * it is needed.
 */
export async function selfServeEnabled(): Promise<boolean> {
  const timer = new AbortController();
  const timeout = setTimeout(() => timer.abort(), SELF_SERVE_PROBE_TIMEOUT_MS);
  try {
    const response = await fetch(`${platformApiBase()}/readyz`, {
      cache: "no-store",
      signal: timer.signal,
    });
    // A DEGRADED platform still answers this document, with a 503. The posture is a configured value
    // rather than a health verdict, so it is read either way — refusing to read it because something
    // else is unwell would hide a fact that has not changed.
    const body = (await response.json()) as ReadyzBody;
    return body.account_system?.self_serve_signup === true;
  } catch {
    return false;
  } finally {
    clearTimeout(timeout);
  }
}
