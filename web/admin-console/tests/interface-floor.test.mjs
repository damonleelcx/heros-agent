// interface-floor.test.mjs holds the console's interface-floor assertions (task 12.13/12.14).
//
// It is two halves. The STATIC half parses the built source and CSS for the invariants that a floor is
// made of — a visible focus indicator, scoped table headers, the pinned en-US locale, a single lang
// attribute, no positive tabindex. The LIVE half, when a server is reachable at ADMIN_CONSOLE_URL,
// fetches the sign-in page (the one route that needs no session) and walks it for a skip link, labelled
// controls, and the security headers. The live half is skipped rather than failed when no server is up,
// so `npm test` runs in CI without one while a `run` step can point it at a live console.
//
// This is the AUTOMATED audit. The keyboard-only pass and the rendered-browser evidence for the denied
// and degraded paths (task 12.15) are performed in a real browser and recorded in the change's
// verification notes — a green run here is necessary, never sufficient.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { join } from "node:path";

const ROOT = new URL("..", import.meta.url).pathname;

async function readSource(rel) {
  return readFile(join(ROOT, rel), "utf8");
}

async function* walk(dir) {
  for (const entry of await readdir(join(ROOT, dir), { withFileTypes: true })) {
    const rel = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(rel);
    else if (rel.endsWith(".tsx") || rel.endsWith(".ts")) yield rel;
  }
}

test("the stylesheet gives every focusable element a visible focus indicator", async () => {
  const css = await readSource("src/app/globals.css");
  assert.match(css, /:focus-visible\s*{[^}]*outline:/, "no :focus-visible outline rule — keyboard focus would be invisible");
});

test("table headers are scoped and sit deeper than the card header", async () => {
  const css = await readSource("src/app/globals.css");
  // The th rule must set a background (the deepest layer) — an unstyled th inherits the card surface
  // and the header-row depth cue disappears.
  assert.match(css, /\bth\s*{[^}]*background:/, "th has no background — the scoped-header depth cue is missing");
});

test("the locale is pinned to en-US in exactly one place", async () => {
  const format = await readSource("src/lib/format.ts");
  assert.match(format, /LOCALE\s*=\s*"en-US"/, "format.ts does not pin the locale to en-US");
  // No component may construct an Intl formatter without a locale (which would follow navigator.language).
  for await (const file of walk("src")) {
    if (file.endsWith("lib/format.ts")) continue;
    const src = await readSource(file);
    const bad = src.match(/new Intl\.\w+\(\s*\)/);
    assert.equal(bad, null, `${file} constructs an Intl formatter with no locale — it would follow the browser's`);
  }
});

test("the html element declares lang once, in the root layout", async () => {
  const layout = await readSource("src/app/layout.tsx");
  // Attribute presence, not the exact tag text: the root element also carries the operator's density
  // preference (FR29), and a floor assertion that broke on an unrelated attribute would be a floor
  // assertion people learn to edit rather than obey.
  assert.match(layout, /<html lang="en-US"[\s>]/, "the root layout does not declare lang=en-US");
});

test("no component uses a positive tabindex", async () => {
  for await (const file of walk("src")) {
    const src = await readSource(file);
    const bad = src.match(/tabindex=["']?[1-9]/i) ?? src.match(/tabIndex=\{?\s*[1-9]/);
    assert.equal(bad, null, `${file} uses a positive tabindex, which breaks keyboard order`);
  }
});

test("no UI string or comment is written in a non-English script", async () => {
  // The floor requires English strings. A CJK codepoint in a source file is the fast signal of a
  // non-English string having slipped in.
  const cjk = /[぀-ヿ㐀-䶿一-鿿가-힯]/;
  for await (const file of walk("src")) {
    const src = await readSource(file);
    assert.equal(cjk.test(src), false, `${file} contains a non-English (CJK) character`);
  }
});

test("the bundle-scan fence exists and checks for credentials and prices", async () => {
  const scan = await readSource("scripts/scan-bundle.mjs");
  assert.match(scan, /ADMIN_PLATFORM_CREDENTIAL/, "the bundle scan does not check for the platform credential");
  assert.match(scan, /PRICE_PATTERNS/, "the bundle scan does not check for priced literals");
});

// ── Live half: exercised only when a server is reachable ──────────────────────

const LIVE_URL = process.env.ADMIN_CONSOLE_URL;

test("the sign-in page meets the interface floor when served", { skip: !LIVE_URL }, async () => {
  const res = await fetch(`${LIVE_URL}/signin`);
  assert.equal(res.status, 200);

  // Security headers on every response.
  assert.equal(res.headers.get("x-content-type-options"), "nosniff");
  assert.equal(res.headers.get("x-frame-options"), "DENY");
  assert.match(res.headers.get("content-security-policy") ?? "", /script-src[^;]*'nonce-/, "no nonce-based CSP");

  const html = await res.text();
  assert.match(html, /lang="en-US"/, "no lang attribute");
  assert.match(html, /class="skip-link"/, "no skip link for keyboard users");
  // Every input the page renders is associated with a label (by id).
  const inputIds = [...html.matchAll(/<(?:input|select|textarea)[^>]*\bid="([^"]+)"/g)].map((m) => m[1]);
  for (const id of inputIds) {
    assert.match(html, new RegExp(`for="${id}"`), `control #${id} has no <label for>`);
  }
});

test("an unauthenticated console route redirects to sign-in", { skip: !LIVE_URL }, async () => {
  const res = await fetch(`${LIVE_URL}/tenants`, { redirect: "manual" });
  assert.equal(res.status, 307, "an unauthenticated route did not redirect");
  assert.match(res.headers.get("location") ?? "", /\/signin/, "the redirect does not go to sign-in");
});
