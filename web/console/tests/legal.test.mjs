// legal.test.mjs holds the P23 assertions a browser can make: availability, the gate's boundaries, and
// the print identity (tasks 10.5, 12.1, 12.9's print half).
//
// # The one that matters most
//
// **Consent never blocks reading.** Not the console, not an in-flight run, and not a legal document
// itself. That is Decision 4, and it is the difference between a legal update and an outage: a consent
// modal keyed to a deployment blocks every customer simultaneously, on release day.
//
// It is asserted with the gate flag turned ALL THE WAY ON, because a test that only passes with the flag
// off proves nothing about the flag.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { startStubPlatform, startConsole, signIn } from "./support/harness.mjs";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");

let platform;
let console_;
let cookie;

before(async () => {
  platform = await startStubPlatform();
  // 🔴 The gate is ON for every principal. Every assertion below about "reading is not blocked" is
  // therefore an assertion about the gate's SCOPE, not about it being switched off.
  console_ = await startConsole(platform.base, { CONSOLE_CONSENT_GATE: "all" });
  cookie = await signIn(console_.base);
}, { timeout: 60_000 });

after(async () => {
  await console_?.close();
  await platform?.close();
});

function text(html) {
  return html
    .replace(/<!--.*?-->/g, "")
    .replace(/<[^>]+>/g, " ")
    .replace(/\s+/g, " ");
}

// ── 12.1 · Availability (NFR1) ───────────────────────────────────────────────

const READING_ROUTES = [
  "/legal",
  "/legal/terms",
  "/legal/privacy",
  "/legal/terms/v/1.0.0",
  "/legal/privacy/v/1.0.0",
  "/legal/manifest.json",
  "/docs",
  "/docs/start/quickstart",
  "/docs/start/install",
  "/docs/reference/cli",
  "/docs/concepts/glossary",
];

test("12.1 — every legal and docs route serves with the platform stopped, and fetches nothing", async () => {
  /*
   * 🔴 The decisive assertion of this whole phase.
   *
   * The legal surface must serve WHEN THE PLATFORM DOES NOT — which is exactly when a customer goes
   * looking for it. So the stub is stopped outright, and then every route must still answer 200 while
   * the upstream-request counter does not move.
   *
   * A counter that moved would mean the surface has acquired a platform call: not a slow page, but a
   * page that goes dark during an incident.
   */
  await platform.close();

  const before = platform.requests.length;
  for (const route of READING_ROUTES) {
    const res = await fetch(`${console_.base}${route}`);
    assert.equal(res.status, 200, `${route} did not serve with the platform stopped`);
    const body = await res.text();
    assert.ok(body.length > 0, `${route} served an empty body`);
  }
  assert.equal(
    platform.requests.length,
    before,
    "a reading route made an upstream call — this surface must serve during a platform incident",
  );

  // Bring it back for the tests below.
  platform = await startStubPlatform();
});

test("12.1 — the reading surface serves with NO SESSION at all", async () => {
  // A legal document behind a sign-in is a legal document a prospective customer cannot read before
  // deciding, and one an auditor cannot read at all.
  for (const route of ["/legal/terms", "/legal/privacy", "/docs", "/docs/reference/cli"]) {
    const res = await fetch(`${console_.base}${route}`); // deliberately no cookie
    assert.equal(res.status, 200, `${route} required a session`);
  }
});

// ── 10.5 · Consent never blocks reading ──────────────────────────────────────

test("10.5 — the consent gate never blocks reading a legal document", async () => {
  // With the gate at "all", the documents themselves must still be readable. A gate that hides the text
  // it is asking you to agree to is not a gate.
  for (const route of ["/legal/terms", "/legal/privacy", "/legal/terms/v/1.0.0"]) {
    const res = await fetch(`${console_.base}${route}`, { headers: { cookie } });
    assert.equal(res.status, 200, `${route} was blocked by the consent gate`);
    assert.doesNotMatch(text(await res.text()), /Before you continue/, `${route} rendered the gate`);
  }
});

test("10.5 — the consent gate never blocks reading the console", async () => {
  platform.set((_req, res) => {
    res.writeHead(200, { "content-type": "application/json" });
    res.end("{}");
  });
  // Every console surface that is not a commitment. None of them may render the gate, and none may
  // redirect to it.
  for (const route of ["/app", "/app/workflows", "/app/runs", "/app/account"]) {
    const res = await fetch(`${console_.base}${route}`, { headers: { cookie }, redirect: "manual" });
    assert.equal(res.status, 200, `${route} redirected or refused under the consent gate`);
    const rendered = text(await res.text());
    assert.doesNotMatch(
      rendered,
      /Before you continue/,
      `${route} rendered the commitment gate — the gate belongs at a commitment, not on the console`,
    );
  }
});

test("10.5 — the gate is not mounted in a layout, so it cannot become a console-wide wall", async () => {
  // Asserted structurally as well as behaviourally: a future change that moved the gate into
  // `app/layout.tsx` would pass the behavioural test only until the flag population widened.
  const layout = await readFile(join(ROOT, "src/app/app/layout.tsx"), "utf8");
  assert.doesNotMatch(
    layout,
    /CommitmentGate/,
    "the commitment gate is mounted in the console layout — that is a gate on the console, which is the " +
      "outage Decision 4 refuses",
  );
});

// ── 10.4 · No optimistic checkmark ───────────────────────────────────────────

test("10.4 — the gate has no path that renders acceptance before the platform confirms it", async () => {
  const source = await readFile(join(ROOT, "src/components/commitmentGate.tsx"), "utf8");

  // The `recorded` state may only be set after a non-ok response has been handled and returned from.
  const setRecorded = source.indexOf('setOutcome({ state: "recorded"');
  const okGuard = source.indexOf("if (!res.ok)");
  assert.ok(okGuard > 0, "the gate does not check the response status");
  assert.ok(
    okGuard < setRecorded,
    "the gate renders acceptance before checking the response — an optimistic checkmark over an " +
      "unrecorded acceptance is the worst failure available to this phase",
  );

  // And the failure copy must say what did NOT happen.
  assert.match(
    source,
    /nothing has been agreed/,
    "a failed write must return the button to rest with a plain sentence saying nothing has been agreed",
  );
});

// ── 3.4 / 12.9 · The print stylesheet emits the document identity ────────────

test("3.4 — a legal page emits its identity for print: kind, version, effective date and hash", async () => {
  const res = await fetch(`${console_.base}/legal/terms`);
  const html = await res.text();

  const footer = /data-print-identity="true"[^>]*>([^<]+)</.exec(html);
  assert.ok(footer, "the legal page emits no print identity block");
  const identity = footer[1];

  assert.match(identity, /Terms of Service/, "the print identity does not name the document");
  assert.match(identity, /version \d+\.\d+\.\d+/, "the print identity does not carry the version");
  assert.match(identity, /effective \d{4}-\d{2}-\d{2}/, "the print identity does not carry the effective date");
  assert.match(identity, /sha256:[0-9a-f]{64}/, "the print identity does not carry the content hash");
});

test("3.4 — the print stylesheet drops the chrome and expands the measure", async () => {
  const css = await readFile(join(ROOT, "src/app/globals.css"), "utf8");
  const print = css.slice(css.indexOf("@media print"));
  assert.ok(print.length > 0, "there is no print stylesheet");

  for (const dropped of [".reading__header", ".reading__footer", ".toc", ".docsearch"]) {
    assert.ok(print.includes(dropped), `the print stylesheet does not drop ${dropped}`);
  }
  assert.match(print, /--print-measure/, "the print stylesheet does not expand the measure");
  assert.match(print, /\.print-footer/, "the print stylesheet does not place the running footer");
});

// ── 8.4 · A superseded version says so and does not redirect ─────────────────

test("8.4 — a permanent version route serves its own text and never redirects", async () => {
  const res = await fetch(`${console_.base}/legal/terms/v/1.0.0`, { redirect: "manual" });
  assert.equal(res.status, 200, "the permanent version route redirected — a consent record links here");
  const rendered = text(await res.text());
  assert.match(rendered, /Terms of Service/);
  assert.match(rendered, /archived version 1\.0\.0/i, "the archived route does not identify itself as one");
});

// ── 8.5 · The manifest resolves with no session ──────────────────────────────

test("8.5 — /legal/manifest.json resolves with no session and lists every version with its hash", async () => {
  const res = await fetch(`${console_.base}/legal/manifest.json`);
  assert.equal(res.status, 200);
  const manifest = await res.json();
  assert.equal(manifest.schema, "heros.legal-manifest/v1");
  for (const kind of ["terms", "privacy"]) {
    const versions = manifest.kinds[kind];
    assert.ok(Array.isArray(versions) && versions.length > 0, `${kind} has no versions in the manifest`);
    for (const version of versions) {
      assert.match(version.hash, /^[0-9a-f]{64}$/, `${kind} ${version.version} has no content hash`);
      assert.match(version.route, /^\/legal\/[a-z]+\/v\/\d+\.\d+\.\d+$/);
      assert.equal(typeof version.material, "boolean", "materiality is not declared in the manifest");
    }
  }
});
