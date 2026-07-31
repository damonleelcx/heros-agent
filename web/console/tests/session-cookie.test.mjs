/**
 * session-cookie.test.mjs — the flags on the customer session cookie, and the invariant that makes
 * one of them safe.
 *
 * This file exists because of a defect a live Stripe checkout found: the session cookie was
 * `SameSite=Strict`, so a customer returning from `checkout.stripe.com` arrived with no cookie and
 * was shown a sign-in page — after being charged. Nothing in the suite caught it, because nothing in
 * the suite ever left the origin, and leaving the origin is the entire point of the payment design.
 */

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

const cookies = readFileSync(new URL("../src/lib/cookies.ts", import.meta.url), "utf8");

test("the session cookie is httpOnly — page script must never read a credential", () => {
  assert.match(cookies, /httpOnly:\s*true/, "the session cookie must be httpOnly");
});

test("the session cookie is SameSite=Lax, so a customer returning from the payment provider is still signed in", () => {
  assert.match(
    cookies,
    /sameSite:\s*"lax"/,
    'the session cookie must be SameSite=Lax. `strict` is not sent on a cross-site top-level ' +
      "navigation, and the return from Stripe Checkout is exactly that — so `strict` signs out every " +
      "customer at the moment they finish paying.",
  );
});

test("the session cookie is Secure outside development", () => {
  assert.match(cookies, /secure:\s*process\.env\.NODE_ENV === "production"/);
});

/**
 * 🔴 The invariant that makes Lax safe.
 *
 * Lax sends the cookie on cross-site top-level navigations that use a safe method. That is harmless
 * only while no GET handler changes anything. The moment one does, it becomes a CSRF hole reachable by
 * a plain link — so this asserts the shape rather than trusting a convention.
 */
test("no API route mutates on GET, which is what makes SameSite=Lax safe", () => {
  const root = new URL("../src/app/api/", import.meta.url).pathname;

  const routes = [];
  (function walk(dir) {
    for (const entry of readdirSync(dir)) {
      const p = join(dir, entry);
      if (statSync(p).isDirectory()) walk(p);
      else if (entry === "route.ts") routes.push(p);
    }
  })(root);

  assert.ok(routes.length > 0, "found no API routes to check — the walk is broken, not the app");

  const offenders = [];
  for (const file of routes) {
    const src = readFileSync(file, "utf8");
    const get = src.match(/export async function GET[\s\S]*?(?=\nexport async function |$)/);
    if (!get) continue;
    // A GET body that sets or clears a cookie, or forwards a non-GET upstream, is mutating.
    if (/cookies\(\)\.set|\.cookies\.set|method:\s*"(POST|PUT|PATCH|DELETE)"/.test(get[0])) {
      offenders.push(file.replace(root, ""));
    }
  }

  assert.deepEqual(
    offenders,
    [],
    "these GET handlers mutate state, which SameSite=Lax would expose to a cross-site link: " +
      offenders.join(", "),
  );
});
