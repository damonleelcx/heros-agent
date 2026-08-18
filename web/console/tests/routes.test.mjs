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
import { routes } from "../src/lib/routes.ts";

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

/*
 * 🔴 All four subject pages left the browser-session list, and the reason is a change in what those
 * pages ARE rather than a relaxation of the rule.
 *
 * The rule is "a selection surface must not choose a subject for the user", and it was written when the
 * only way to reach a subject was to already know its id — because the platform had no collection
 * routes. Nothing carried an owning organization, so "which of these are mine" was not a question the
 * API could be asked, and a page that fetched anything would have been fetching somebody's guess. All a
 * picker could offer was what THIS browser session had already opened.
 *
 * P27 gave runs an owner; P29 added the enumeration for all four subjects. That spec's opening line
 * names the old behaviour as the defect it fixed — "what do I have?" is the question the console "has
 * never been able to ask, which is why every subject picker offers only what the current browser
 * session already opened" (`openspec/specs/linked-subject-index/spec.md`).
 *
 * So fetching the COLLECTION is not choosing a subject — it is the page. Fetching a SPECIFIC subject
 * still is, and that half of the rule did not move. The session's own list survives as an ordering hint
 * only, and a remembered subject the enumeration does not contain is DISCARDED rather than offered
 * (`src/app/app/workflows/page.tsx`).
 *
 * ⚠️ This block asserted the pre-P29 world until 2026-08-18. P29 updated it for `/app/runs` and left
 * the other three, so three routes asserted "fetches nothing" against pages that now legitimately fetch
 * their collection, and all four still required copy ("Opened in this session") that now exists only on
 * `/app`. It survived because ci.yml's only console job runs `web/admin-console` — this suite has never
 * run in CI.
 *
 * ⚠️ These tests run `next start` against a PREBUILT `.next` (see support/harness.mjs). A mutation drill
 * on this file's assertions must REBUILD, or the mutation is not in the binary under test and the drill
 * reports a fence that cannot actually go red.
 */

/**
 * assertCollectionNotSubject holds the half of the no-default-subject rule that survived P29: a
 * selection page fetches the collection it lists, and NEVER an individual subject.
 *
 * 🔴 It is deliberately two-sided. The assertion this replaced was `requests.length === before`, which
 * also passes when the page fetches NOTHING — after P29 that is a broken page rendering an empty list.
 * A fence that cannot fail in both directions only guards the half somebody happened to think of.
 */
function assertCollectionNotSubject(fetched, route, collection) {
  assert.ok(
    fetched.some((u) => u === collection || u.startsWith(`${collection}?`)),
    `${route} did not fetch its collection ${collection} (asked for: ${fetched.join(", ") || "nothing"})`,
  );
  assert.ok(
    !fetched.some((u) => new RegExp(`^${collection}/[^?/]`).test(u)),
    `${route} opened a subject nobody asked for: ${fetched.join(", ")}`,
  );
}

const SELECTION_ROUTES = [
  ["/app/workflows", "workflow", "/api/v1/workflows"],
  ["/app/transforms", "transform", "/api/v1/transforms"],
  ["/app/variants", "variant", "/api/v1/variants"],
];

for (const [route, subject, collection] of SELECTION_ROUTES) {
  test(`${route} lists the caller's own subjects and still never picks one for them`, async () => {
    const before = platform.requests.length;
    const { status, html } = await page(route);
    assert.equal(status, 200);
    assertCollectionNotSubject(platform.requests.slice(before).map((r) => r.url), route, collection);
    const rendered = text(html);
    assert.match(rendered, new RegExp(`Open a ${subject} by identifier`, "i"), "no direct-entry accelerator");
    // The platform-backed list that REPLACED "Opened in this session" here. `SubjectPicker` titles it
    // `Your {subject}s`; asserting it is what stops the enumeration silently regressing to a browser list.
    assert.match(rendered, new RegExp(`Your ${subject}s`, "i"), "no platform-backed subject list");
    // And it must never contain the legacy default.
    assert.doesNotMatch(html, /wf-demo/, `${route} mentions the hardcoded legacy default`);
  });
}

test("/app/runs lists the caller's own runs and still never picks one for them", async () => {
  const before = platform.requests.length;
  const { status, html } = await page("/app/runs");
  assert.equal(status, 200);

  // The COLLECTION, and never a SPECIFIC run — the same two-sided property the three selection routes
  // assert, through the same helper, because it is one rule and not four.
  //
  // No organization in the collection URL: the scope comes from the credential, and there is no
  // parameter that could carry one.
  assertCollectionNotSubject(
    platform.requests.slice(before).map((r) => r.url),
    "/app/runs",
    "/api/v1/runs",
  );

  const rendered = text(html);
  assert.match(rendered, /Open a run by identifier/i, "the direct-entry accelerator was lost");
  assert.match(rendered, /Your runs/i, "no platform-backed run list");
  assert.doesNotMatch(html, /wf-demo/, "/app/runs mentions the hardcoded legacy default");
});

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

/**
 * P27 task 13.1 · the terminal-approval path is compiled into TWO artifacts and must not drift.
 *
 * The CLI prints a URL before any browser is open, so the platform hands it the path — `deviceVerificationPath`
 * in `internal/api/deviceauth.go`. The console owns the page at that path. Nothing connects them at
 * runtime: a rename on either side produces a terminal telling somebody to open a 404, and it fails for
 * whoever is signing in rather than for whoever renamed it.
 */
test("the device-approval path the platform prints is the path the console serves", async () => {
  const consoleRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
  const repoRoot = join(consoleRoot, "..", "..");
  const go = await readFile(join(repoRoot, "internal", "api", "deviceauth.go"), "utf8");
  const printed = go.match(/deviceVerificationPath\s*=\s*"([^"]+)"/)?.[1];
  assert.ok(printed, "internal/api/deviceauth.go no longer declares deviceVerificationPath");
  assert.equal(
    routes.device(),
    printed,
    "the console's device route and the path the platform prints to a terminal have diverged. " +
      "Nothing connects them at runtime: the CLI prints this before a browser is open, so a rename on " +
      "either side sends somebody to a 404 while they are trying to sign in.",
  );
  const page = join(consoleRoot, "src", "app", ...printed.replace(/^\//, "").split("/"), "page.tsx");
  await assert.doesNotReject(readFile(page, "utf8"), `no page is served at ${printed}`);
});
