// routes.test.mjs holds §7.6 — the shell, selection, and canonical-route assertions.
//
// Two properties, both of which the four legacy pages get wrong:
//
//   🔴 **No route substitutes a default subject.** `p4board.html` does
//      `params.get('workflow') || 'wf-demo'`, so opening the board bare shows a fully rendered,
//      confident board for a workflow that is not the user's. That is strictly worse than an empty
//      state: an empty state tells the truth, a wrong default asserts a falsehood with the full
//      authority of a populated UI.
//
//   **Every legacy entry point still resolves.** R8 removes hand-typed parameters as an ENTRY
//      MECHANISM; it must not remove shareability, which is a real and used capability.

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
  console_ = await startConsole(platform.base);
  cookie = await signIn(console_.base);
}, { timeout: 60_000 });

after(async () => {
  await console_?.close();
  await platform?.close();
});

async function page(path) {
  const res = await fetch(`${console_.base}${path}`, { headers: { cookie } });
  return { status: res.status, html: await res.text() };
}

/**
 * text strips React's SSR markers and tags, so an assertion is about what a READER sees.
 *
 * React splits a JSX text node around an interpolation and marks the boundary with an HTML comment —
 * `Open a <!-- -->workflow<!-- --> by identifier`. Asserting against the raw markup makes every copy
 * assertion depend on where the interpolations happen to fall, which is a property of the source
 * formatting rather than of the rendered page.
 */
function text(html) {
  return html
    .replace(/<!--.*?-->/g, "")
    .replace(/<[^>]+>/g, " ")
    .replace(/\s+/g, " ");
}

// ── No default subject ───────────────────────────────────────────────────────

const SELECTION_ROUTES = [
  ["/app/workflows", "workflow"],
  ["/app/runs", "run"],
  ["/app/transforms", "transform"],
  ["/app/variants", "variant"],
];

for (const [route, subject] of SELECTION_ROUTES) {
  test(`${route} with no parameters renders selection, never populated data`, async () => {
    const before = platform.requests.length;
    const { status, html } = await page(route);
    assert.equal(status, 200);
    // The decisive assertion: a selection surface makes NO upstream call, because there is no subject
    // to fetch. If it fetched something, it chose a subject for the user.
    assert.equal(platform.requests.length, before, `${route} fetched a subject it was not given`);
    const rendered = text(html);
    assert.match(rendered, new RegExp(`Open a ${subject} by identifier`, "i"), "no direct-entry accelerator");
    assert.match(rendered, /Opened in this session/, "no already-visited list");
    // And it must never contain the legacy default.
    assert.doesNotMatch(html, /wf-demo/, `${route} mentions the hardcoded legacy default`);
  });
}

test("the overview offers subjects and next actions, and fetches nothing", async () => {
  const before = platform.requests.length;
  const { status, html } = await page("/app");
  assert.equal(status, 200);
  assert.equal(platform.requests.length, before, "the overview made an upstream call");
  assert.match(text(html), /Overview/);
  assert.match(text(html), /Nothing opened yet|Opened in this session/);
});

// ── The phase-named entry points are GONE, and stay gone ─────────────────────
//
// `/p2`, `/p4/board`, `/p25/monitor` and `/p35/graph` were 308 shims from the pre-P9 hand-written
// pages to the canonical `/app/...` routes. They are deleted: a URL is a public name, and a phase
// number is an internal fact about the order we built things in — it tells a user nothing and dates
// the product. Nothing was deployed under them, so there are no bookmarks to keep alive.
//
// This asserts their ABSENCE rather than simply deleting the old cases. A deleted test proves nothing;
// this fails if anybody reintroduces the shape.

const RETIRED = ["/p2", "/p4/board", "/p25/monitor", "/p35/graph", "/p45/scorecard", "/p55/recommendations"];

for (const retired of RETIRED) {
  test(`${retired} is retired — a phase number is not a public name`, async () => {
    const res = await fetch(`${console_.base}${retired}`, { headers: { cookie }, redirect: "manual" });
    assert.equal(res.status, 404, `${retired} still resolves (${res.status}) — phase-named URLs are retired`);
  });
}

// ── §11c.4 — the CLI-emitted run reference opens exactly that run ─────────────
//
// The `link` CLI prints, and the platform stores, a run URL built as
// `https://heros-agent.space/app/runs/{run_id}` (internal/linkingest linkingest.go, and the pinned
// PlatformBaseURL in internal/runlink). When that URL is pasted from a terminal into a pull request,
// its PATH — `/app/runs/{run_id}` — must open that run in this console, not a picker and not a 404.
// So the console's canonical run route MUST be that exact segment. This guards the console half; the
// Go half is pinned by internal/linkingest's TestConsoleRoute_IsThePlatformCanonicalRunPath.

test("11c.4 — the canonical run route is the exact segment the CLI emits (/app/runs/{id})", async () => {
  const routesSrc = await readFile(join(ROOT, "src/lib/routes.ts"), "utf8");
  // A refactor that changed this segment (say to /app/run/ or /app/runs/{id}/view) would silently
  // break every run URL already pasted into a pull request. Pin it against the CLI's literal.
  assert.match(
    routesSrc,
    /run:\s*\(id:[^)]*\)\s*=>\s*`\/app\/runs\/\$\{encodeURIComponent\(id\)\}`/,
    "routes.run must resolve to /app/runs/{id} — the literal the CLI/linkingest emits",
  );
});

test("11c.4 — pasting the CLI's exact run path opens that run, not a picker or a redirect", async () => {
  // The stub answers with a body the run view cannot render (an empty object); the point is not the
  // data but the RESOLUTION — the pasted path must land on the run SUBJECT page (which names the run
  // in first paint per R13), never bounce to the runs picker.
  platform.set((_req, res) => {
    res.writeHead(200, { "content-type": "application/json" });
    res.end("{}");
  });
  const res = await fetch(`${console_.base}/app/runs/run_from_cli`, { headers: { cookie }, redirect: "manual" });
  assert.equal(res.status, 200, "the CLI's run path redirected or 404'd instead of opening the run");
  const html = await res.text();
  assert.match(html, /run_from_cli/, "the run subject was not named on its own page");
  assert.doesNotMatch(text(html), /Open a run by identifier/i, "the CLI's run path fell through to the picker");
});

test("a half-specified transform key goes to the picker, not to a not-found", async () => {
  // A config hash without a source revision does not NAME a transform. That is a different fact from
  // a transform that does not exist, and P2-12 already distinguishes them.
  //
  // This used to reach the property through the retired `/p2?cfg=` shim. The shim is gone; the
  // property is not, so it is asserted directly on the canonical route — which is where it always
  // mattered. A test that dies with its entry point was testing the entry point.
  const res = await fetch(`${console_.base}/app/transforms?config_hash=abc123`, {
    headers: { cookie },
    redirect: "manual",
  });
  assert.equal(res.status, 200, "a half-specified transform key did not render the picker");
  const html = await res.text();
  assert.doesNotMatch(text(html), /not found/i, "a half-specified key was reported as a missing transform");
  assert.match(html, /abc123/, "the picker discarded the config hash the user had already supplied");
});

test("a typed identifier resolves to the canonical route, so the subject ends up in the PATH", async () => {
  // This is what keeps R9's shareable link and R8's no-hand-typed-entry compatible: the picker's form
  // is a GET that redirects, so the address bar ends up carrying the subject rather than a query.
  const res = await fetch(`${console_.base}/app/runs?run_id=run-9`, { headers: { cookie }, redirect: "manual" });
  assert.equal(res.status, 307);
  assert.equal(new URL(res.headers.get("location"), console_.base).pathname, "/app/runs/run-9");
});

// ── The shell reaches every surface ──────────────────────────────────────────

test("the shell's navigation reaches every surface — none is reachable only by URL", async () => {
  const { html } = await page("/app");
  for (const href of ["/app", "/app/workflows", "/app/runs", "/app/transforms", "/app/variants", "/app/configure", "/app/account"]) {
    assert.ok(html.includes(`href="${href}"`), `the shell does not link to ${href}`);
  }
});

test("a subject carries across surfaces without being re-asked", async () => {
  platform.set((req, res) => {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ workflow_id: "owner/repo", nodes: [], edges: [], regions: [], unclassified: [], llm_calls: 0, ir_version: "1", taxonomy_version: "1" }));
  });
  const { html } = await page("/app/workflows/owner%2Frepo/graph");
  // The subject strip links the other two surfaces for the SAME workflow, so moving between them is a
  // link rather than a URL edit — which is the defect the four legacy pages share most obviously.
  assert.match(html, /href="\/app\/workflows\/owner%2Frepo\/board"/);
  assert.match(html, /href="\/app\/workflows\/owner%2Frepo\/proposals"/);
});

test("an opened subject is offered back from the session, never re-typed", async () => {
  // FR30. The console cannot enumerate a tenant's workflows — the platform exposes no such endpoint —
  // but it must never ask for one it has already been given.
  const { html } = await page("/app/workflows");
  assert.match(text(html), /owner\/repo/, "a workflow opened moments ago is not offered back");
});

// ── Every view names its subject before its data resolves (R13) ─────────────

test("a data view names its subject even when the upstream call fails", async () => {
  platform.set((_req, res) => {
    res.writeHead(503, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "the pattern classifier is not mounted on this server" }));
  });
  const { html } = await page("/app/workflows/owner%2Frepo/graph");
  // A failure that also erased the subject would leave the reader unable to tell WHICH workflow failed
  // to load — which is the difference between an actionable error and a mystifying one.
  assert.match(html, /<h1[^>]*>owner\/repo<\/h1>/);
  assert.match(text(html), /not mounted on this deployment/);
});
