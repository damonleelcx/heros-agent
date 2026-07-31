// public-surface.test.mjs holds §7b.7 and §9.10 — the public home page's assertions (R15, FR32–FR35).
//
// The property that matters most is the one that is easiest to lose without noticing: this page makes
// **no upstream call**. Not because it is faster, but because an anonymous visitor must never cause
// the BFF to use the server-held credential, and because the page a prospect first sees must not go
// down with the platform API. A marketing page that 500s during an incident is the worst possible
// sample of the product.
//
// The decisive test below therefore stops the platform entirely and asks for the page.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir, writeFile, unlink } from "node:fs/promises";
import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";
import { startStubPlatform, startConsole } from "./support/harness.mjs";

const exec = promisify(execFile);
const ROOT = new URL("..", import.meta.url).pathname;
const read = (rel) => readFile(join(ROOT, rel), "utf8");

let platform;
let console_;

before(async () => {
  platform = await startStubPlatform();
  console_ = await startConsole(platform.base);
}, { timeout: 60_000 });

after(async () => {
  await console_?.close();
  await platform?.close();
});

// ── It renders with no session, no tenant data, no upstream call ─────────────

test("an anonymous visitor gets the whole page, with no redirect and no upstream call", async () => {
  const before = platform.requests.length;
  const res = await fetch(`${console_.base}/`, { redirect: "manual" });
  assert.equal(res.status, 200, "the public entry redirected or failed");
  const html = await res.text();
  assert.match(html, /ships on evidence/);
  assert.equal(platform.requests.length, before, "the public page made an upstream call");
});

test("🔴 the page still serves with the platform API stopped", async () => {
  // The decisive one. Everything else about "no upstream call" can be true by accident; this cannot.
  await platform.close();
  try {
    const res = await fetch(`${console_.base}/`);
    assert.equal(res.status, 200, "the public page went down with the platform");
    const html = await res.text();
    assert.match(html, /Sign in/);
  } finally {
    // Restarting on a new port would break the console's configured base, so the remaining tests are
    // ordered after this one deliberately and do not need the platform.
  }
});

test("the public surface is not a way into tenant data", async () => {
  const res = await fetch(`${console_.base}/app`, { redirect: "manual" });
  assert.equal(res.status, 307);
  assert.match(res.headers.get("location") ?? "", /\/signin/);
});

// ── Claim integrity ──────────────────────────────────────────────────────────

test("every claim on the page resolves to a shipped manifest entry", async () => {
  const { stdout } = await exec("node", ["scripts/scan-claims.mjs"], { cwd: ROOT });
  assert.match(stdout, /claim scan passed/);
  assert.match(stdout, /all shipped and attributed to an owning phase/);
});

test("🔴 the claim gate goes RED on an unlisted claim and on an unshipped one", async () => {
  // A gate nobody has seen fail is a gate nobody knows is connected — and this is the one gate standing
  // between a roadmap item and a sentence a customer will hold us to.
  const probe = join(ROOT, "src", "components", "marketing", "__claim_probe.tsx");

  await writeFile(probe, 'export const P = () => <Claim id="a-thing-we-have-not-built" />;\n');
  try {
    await assert.rejects(
      () => exec("node", ["scripts/scan-claims.mjs"], { cwd: ROOT }),
      (error) => {
        assert.equal(error.code, 1);
        assert.match(error.stderr, /UNLISTED/);
        return true;
      },
    );
  } finally {
    await unlink(probe);
  }

  // And an entry that exists but is not shipped.
  const manifestPath = join(ROOT, "src", "content", "capabilities.ts");
  const manifest = await read("src/content/capabilities.ts");
  await writeFile(
    manifestPath,
    manifest.replace(
      '    id: "console",\n    claim:',
      '    id: "not-yet",\n    claim: "Something that has not shipped",\n    phase: "PX",\n    shipped: false,\n    evidence: "none",\n    boundary: "none",\n  },\n  {\n    id: "console",\n    claim:',
    ),
  );
  await writeFile(probe, 'export const P = () => <Claim id="not-yet" />;\n');
  try {
    await assert.rejects(
      () => exec("node", ["scripts/scan-claims.mjs"], { cwd: ROOT }),
      (error) => {
        assert.equal(error.code, 1);
        assert.match(error.stderr, /UNSHIPPED/);
        return true;
      },
    );
  } finally {
    await unlink(probe);
    await writeFile(manifestPath, manifest);
  }
});

test("every manifest entry carries an owning phase, evidence, and a boundary", async () => {
  const manifest = await read("src/content/capabilities.ts");
  const entries = [...manifest.matchAll(/\{\s*id: "([a-z0-9-]+)",([\s\S]*?)\n  \},/g)];
  assert.ok(entries.length >= 10, `only ${entries.length} capabilities in the manifest`);
  for (const [, id, body] of entries) {
    // `\s*` after each colon: the manifest's prose wraps, so a value routinely starts on the next
    // line. An assertion that required one line would be checking the formatter, not the content.
    assert.match(body, /phase:\s*"[^"]+"/, `${id} has no owning phase — nobody keeps it true`);
    assert.match(body, /evidence:\s*"[^"]+"/, `${id} has no evidence — nobody can check it`);
    // The boundary is the part that usually gets left out, and it is the part that turns an
    // overstated claim into a qualified lead.
    assert.match(body, /boundary:\s*"[^"]+"/, `${id} states a benefit with no boundary`);
  }
});

// ── Plans by name, never priced ──────────────────────────────────────────────

test("plans are named and the page carries no price", async () => {
  const res = await fetch(`${console_.base}/`);
  const html = await res.text();
  for (const plan of ["Free", "Team", "Business", "Enterprise"]) {
    assert.ok(html.includes(plan), `the page does not name the ${plan} plan`);
  }
  // The rendered page, not just the bundle: a price interpolated at render time would pass the bundle
  // scan and still reach a reader.
  assert.doesNotMatch(html, /\$\s?\d[\d,]*\.\d/, "a currency amount is rendered on the public page");
  assert.doesNotMatch(html, /\bper\s?seat\b|\/mo\b|\bper month\b/i, "a pricing unit is rendered on the public page");
});

// ── No third-party REQUEST ───────────────────────────────────────────────────

/*
 * 🔴 The rule is "no third-party REQUEST", not "no third-party string in the HTML".
 *
 * This test used to require that every `src`/`href` in the document be same-origin, and its own
 * comment named what it was protecting: "a hosted font or a tag manager would also send a prospect's
 * address to a third party before they consented to anything." Both examples are SUBRESOURCES — the
 * browser fetches them on load, with no click and no consent. That is the harm.
 *
 * An `<a href>` is not that. It issues no request until the visitor chooses the destination, so it
 * leaks nothing on load. The old assertion could not tell the two apart, and when P23 added the
 * repository link (`social-proof-claims`: "The repository link SHALL always render", escalated and
 * answered at task 7.5) the blunt version failed on a page that still causes zero third-party
 * requests. The proxy had outlived the rule.
 *
 * Note also that the old test's title claimed "the CSP would refuse one". For a subresource that is
 * true. For an anchor it is FALSE — `default-src` does not govern link navigation — so the title was
 * asserting a protection that was not there. Hence the guards below, which are what actually make the
 * click non-leaking: `Referrer-Policy: no-referrer` (set for every route in `next.config.mjs`, so the
 * destination is never told where the visitor came from) and `rel="noreferrer noopener"` on the anchor
 * itself. Neither was checked before.
 *
 * The two ways this could have gone instead, and why not:
 *
 *   - Proxy the link through a first-party route so the rendered href stays same-origin. Rejected on
 *     the grounds P24 already wrote down for exactly this manoeuvre: "it would make a third-party flow
 *     look first-party, and the CSP's whole value is that it is a readable statement of where data
 *     goes." It also hides from the reader that the download they are about to take comes from the
 *     project's own public forge, which is the trust anchor, not an incidental detail.
 *   - Widen the pattern until GitHub passes. That is the "silently removes the guarantee from the
 *     surface it was for" option, also already rejected in P24's design.
 *
 * This composes with, and does not overlap, P24's pending amendment to the same P9 requirement: P24
 * widens it along the *allowlisted analytics origin* axis (subresources, gated on consent); this one
 * distinguishes a *navigation* from a subresource. A future external subresource still fails here.
 */
test("no external subresource: an external origin may appear only as an anchor the visitor clicks", async () => {
  const res = await fetch(`${console_.base}/`);
  const html = await res.text();
  const csp = res.headers.get("content-security-policy") ?? "";
  assert.match(csp, /default-src 'self'/);
  assert.doesNotMatch(csp, /https?:\/\//, "the CSP names a third-party origin");

  const isLocal = (url) => url.startsWith("/") || url.startsWith("#") || url.startsWith("data:");

  // The same document-wide sweep as before, so the coverage is not narrowed by the change.
  const referenced = [...html.matchAll(/(?:src|href)="([^"]+)"/g)]
    .map((m) => m[1])
    .filter((url) => !isLocal(url));

  // Now classify by the element carrying the attribute, which is the thing the flat sweep could not do.
  const offenders = [];
  const externalAnchors = [];
  for (const [, tag, attrs] of html.matchAll(/<([a-zA-Z][\w-]*)\b([^>]*)>/g)) {
    const src = /\bsrc="([^"]+)"/.exec(attrs)?.[1];
    const href = /\bhref="([^"]+)"/.exec(attrs)?.[1];
    // `src` is a subresource on every element that has one. No exceptions, including <link>.
    if (src && !isLocal(src)) offenders.push(`<${tag} src="${src}">`);
    if (href && !isLocal(href)) {
      if (tag.toLowerCase() === "a") {
        externalAnchors.push({ url: href, rel: /\brel="([^"]*)"/.exec(attrs)?.[1] ?? "" });
      } else {
        // A <link rel="stylesheet"> or <link rel="preload"> is fetched on load exactly like a font.
        offenders.push(`<${tag} href="${href}">`);
      }
    }
  }
  assert.deepEqual(
    offenders,
    [],
    `the public page loads an external subresource: ${offenders.join(", ")}`,
  );

  // Counts, not sets: if the same URL appeared as both an <img src> and an <a href>, comparing sets of
  // URLs would call them equal and wave the image through.
  assert.equal(
    referenced.length,
    externalAnchors.length,
    "an external reference is not accounted for as an anchor href — the element scan did not see it",
  );

  // What makes the click itself non-leaking. Without these the anchor is a referrer leak with extra
  // steps, and the requirement would be satisfied only on paper.
  assert.equal(
    res.headers.get("referrer-policy"),
    "no-referrer",
    "an external anchor is only safe while the page refuses to name itself to the destination",
  );
  for (const anchor of externalAnchors) {
    assert.match(anchor.rel, /\bnoreferrer\b/, `external anchor without rel=noreferrer: ${anchor.url}`);
    assert.match(anchor.rel, /\bnoopener\b/, `external anchor without rel=noopener: ${anchor.url}`);
  }
});

// ── The same floor as every other page ───────────────────────────────────────

test("the public page meets the console's floor", async () => {
  const res = await fetch(`${console_.base}/`);
  const html = await res.text();
  assert.match(html, /class="skip-link"/, "no skip link");
  assert.match(html, /<html lang="en"/, "the language is not declared");
  const h1s = html.match(/<h1/g) ?? [];
  assert.equal(h1s.length, 1, "the page has more than one display heading — that is more than one subject");
});

test("the public surface uses the same token set as the console", async () => {
  // No page-local palette: the surface a prospect meets and the surface they sign in to must be
  // recognisably one product, and the token scan is what keeps them so.
  const { stdout } = await exec("node", ["scripts/scan-tokens.mjs"], { cwd: ROOT });
  assert.match(stdout, /token scan passed/);

  // Inline styles are permitted ONLY where every value in them resolves through a token.
  //
  // A flat ban was the first rule and it was wrong in one direction: this page's three background
  // figures — the grid, the two glows and the bottom fade — are gradients, and a gradient cannot be a
  // utility class without becoming an arbitrary value that the token scan cannot see into. Declared as
  // `--marketing-grid` and referenced with `var()`, they are as governed as any colour on the page.
  //
  // What stays banned is the thing the rule was written for: a literal in a style attribute, which is
  // a page-local palette by another name and is invisible to every fence this console has.
  for await (const file of walk("src/app/(public)")) {
    const src = await read(file);
    for (const [, body] of src.matchAll(/style=\{\{([^}]*)\}\}/g)) {
      const values = [...body.matchAll(/:\s*("[^"]*"|`[^`]*`|[^,]+)/g)].map((m) => m[1]);
      for (const value of values) {
        assert.match(
          value,
          /var\(--/,
          `${file} carries an inline style with a literal value (${value.trim()}) — use the token set`,
        );
      }
    }
  }
});

async function* walk(dir) {
  for (const entry of await readdir(join(ROOT, dir), { withFileTypes: true })) {
    const rel = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(rel);
    else yield rel;
  }
}
