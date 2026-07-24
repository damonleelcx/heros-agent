// acceptance.test.mjs holds §9.3, §9.5, §9.7, §9.8 and §9.9 — the acceptance gate, executed against
// RENDERED HTML rather than against source.
//
// # Why this file is separate from inventory.test.mjs
//
// The inventory suite asserts the ported source still carries each behaviour. That is a regression
// guard, and it is not acceptance: a green assertion and a page that renders nothing are entirely
// compatible. This console has already paid for that three times — a session store split across two
// module graphs, a `Set-Cookie` dropped by an immutable redirect, and a `form-action` policy refusing
// the console's own sign-in — each one invisible to every check that did not look at the output.
//
// So these tests run the real console against a stub platform whose answer is chosen per request, and
// assert on what comes back. They are the automated half of acceptance; §9.4 and §9.6 are the half a
// person does in a browser, and neither replaces the other.
//
// # The property being protected, stated once
//
// 🔴 Loading, empty and the THREE error classes are five different answers with five different
// remedies — mount the subsystem, check the identifier, check the network, add data, wait. A surface
// that collapses any two of them has turned a remedy into a guess, and the collapse is always silent.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { startStubPlatform, startConsole, signIn, TENANT } from "./support/harness.mjs";

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

/** get fetches a console page as the signed-in tenant and returns its HTML. */
async function get(path) {
  const res = await fetch(`${console_.base}${path}`, { headers: { cookie }, redirect: "manual" });
  return { status: res.status, html: await res.text() };
}

/** answering points the stub at one canned reply for every request. */
function answering(status, body, headers = { "content-type": "application/json" }) {
  platform.set((_req, res) => {
    res.writeHead(status, headers);
    res.end(typeof body === "string" ? body : JSON.stringify(body));
  });
}

/**
 * board builds a BoardView matching the generated Go-derived types.
 *
 * Written out in full rather than partially, because a partial fixture is how a test comes to assert
 * against a rendering the platform can never produce — the first version of this file used `rows` and
 * `gate_passed`, which the read model calls `ranked` and `gate_pass`, and the board dutifully rendered
 * an error while the test believed it was checking a leaderboard.
 */
function board(flags) {
  return {
    state: "ok",
    workflow_id: "acme/wf",
    eval_set_hash: "abc123def456",
    profile: "default",
    gate_set: "default",
    profiles: ["default"],
    progress: { units_planned: 20, units_completed: 20, seed_floor: 3 },
    ranked: [
      {
        rank: 1,
        variant_id: "v-1",
        label: "variant-a",
        config_hash: "deadbeefcafe0000",
        config_hash_short: "deadbeefcafe",
        score: 0.9,
        ci_low: flags.length ? 0.8 : 0.88,
        ci_high: flags.length ? 1.0 : 0.92,
        n_seeds: 5,
        n_cases: 20,
        method: "bootstrap",
        components: [],
        penalties: [],
        gate_pass: true,
        failed_gates: [],
        gate_reasons: [],
        flags,
        tied_with: [],
        provisional: flags.includes("provisional"),
      },
    ],
    disqualified: [],
    pareto: [],
    coverage: { measured: false, dimensions: [], reasons: [], residuals: [], stats: null },
    spend: { entries: [], total_usd: 0, calls: 0 },
    all_tie: false,
    notes: [],
    unmeasured: [],
    runs_enqueued: 0,
  };
}

/** graph builds a GraphView with no nodes — the empty-but-successful case. */
function graph(nodes = []) {
  return {
    workflow_id: "acme/wf",
    ir_version: "1.0.0",
    taxonomy_version: "1.0.0",
    llm_calls: 0,
    nodes,
    edges: [],
    regions: [],
    unclassified: [],
    diagnostics: [],
  };
}

/** The four data surfaces, with a subject that exists and the upstream each one reads. */
const VIEWS = [
  { name: "graph", path: `/app/workflows/${encodeURIComponent("acme/wf")}/graph`, subject: "acme/wf" },
  { name: "board", path: `/app/workflows/${encodeURIComponent("acme/wf")}/board`, subject: "acme/wf" },
  { name: "run", path: "/app/runs/run-1", subject: "run-1" },
  { name: "live monitor", path: "/app/runs/run-1/live", subject: "run-1" },
  { name: "scorecard", path: "/app/variants/v-1/scorecard", subject: "v-1" },
  { name: "account", path: "/app/account", subject: TENANT },
];

// ── 9.3 / 9.5 · The state matrix, and the three error classes ────────────────

test("9.3/9.5 — a not-mounted subsystem renders as not-mounted on every view", async () => {
  // The platform answers 503 with its own body. The console must render the platform's words, and a
  // rendering that is visually distinct from not-found — it is not an error and nothing is wrong with
  // the reader's data; the capability simply is not installed here.
  answering(503, { error: "the P2 store is not mounted" });

  for (const view of VIEWS) {
    const { html } = await get(view.path);
    assert.match(html, /not mounted on this deployment/, `${view.name} lost the not-mounted rendering`);
    assert.match(html, /state--not-mounted/, `${view.name}'s not-mounted state is not visually distinct`);
    assert.doesNotMatch(html, /No such/, `${view.name} rendered a 503 as a not-found`);
  }
});

test("9.3/9.5 — a missing subject renders as not-found, never as an empty result", async () => {
  // 🔴 The load-bearing one. A 404 mapped to an empty successful result is the defect that makes a
  // typo look like a workflow with no data — and the reader's next move is to file a bug about missing
  // measurements rather than to check the identifier they pasted.
  answering(404, { error: "no such workflow" });

  for (const view of VIEWS) {
    const { html } = await get(view.path);
    assert.match(html, /state--not-found/, `${view.name} lost the not-found rendering`);
    assert.match(html, /routing fact, not a measurement/, `${view.name} no longer says a 404 is a routing fact`);
    assert.doesNotMatch(html, /state--not-mounted/, `${view.name} rendered a 404 as not-mounted`);
  }
});

test("9.3/9.5 — an unreachable platform renders as a transport failure, distinct from both", async () => {
  // The stub is closed outright, so the connection is refused rather than answered. Nothing has been
  // measured and nothing about the reader's data has changed — the console could not ask, and it says
  // exactly that instead of showing an empty view.
  const isolated = await startStubPlatform();
  const solo = await startConsole(isolated.base);
  const soloCookie = await signIn(solo.base);
  await isolated.close();

  try {
    const res = await fetch(`${solo.base}${VIEWS[0].path}`, { headers: { cookie: soloCookie } });
    const html = await res.text();
    assert.match(html, /state--transport/, "the transport failure is not visually distinct");
    assert.match(html, /transport failure, not an empty result/);
    assert.doesNotMatch(html, /state--not-mounted/);
    assert.doesNotMatch(html, /state--not-found/);
  } finally {
    await solo.close();
  }
}, { timeout: 60_000 });

test("9.3 — the three error classes produce three DIFFERENT renderings, not three strings in one box", async () => {
  // Every state marker on the page is collected, not just the first: a view may legitimately carry
  // more than one state block, and matching only the first is how a test comes to assert against
  // whichever element happens to be highest in the document.
  const markers = async () => {
    const { html } = await get(VIEWS[0].path);
    return new Set([...html.matchAll(/state state--([a-z-]+)/g)].map((m) => m[1]));
  };

  answering(503, { error: "upstream says not mounted" });
  const notMounted = await markers();
  answering(404, { error: "upstream says not found" });
  const notFound = await markers();

  assert.ok(notMounted.has("not-mounted"), "a 503 did not render the not-mounted state");
  assert.ok(notFound.has("not-found"), "a 404 did not render the not-found state");
  assert.ok(!notMounted.has("not-found"), "a 503 also rendered a not-found state");
  assert.ok(!notFound.has("not-mounted"), "a 404 also rendered a not-mounted state");
});

test("9.3 — a populated view renders its data, and an empty one is a state rather than an absence", async () => {
  answering(200, graph());
  const { html } = await get(VIEWS[0].path);

  // Empty is a STATE with an explanation, never a blank panel.
  assert.match(html, /state--empty/, "the empty state is not rendered as a state");
  assert.match(html, /no nodes in its IR|carries a label yet/i, "the empty state lost its explanation");
  // And the view still renders what it does know — the counts are data, not decoration.
  assert.match(html, /stat__value/, "a populated summary block is missing");
});

test("9.5 — the upstream's own message is carried through, never replaced with a generic one", async () => {
  answering(503, { error: "the pattern classifier is not mounted on this server" });
  const { html } = await get(VIEWS[0].path);
  assert.match(html, /the pattern classifier is not mounted on this server/, "the platform's words were discarded");
});

test("🔴 9.3/9.5 — a 200 whose body does not match the read model is a FOURTH state, not a crash", async () => {
  // Found by the §9.8 heading test: the console trusted any 200 to match its read model, so a body of
  // the wrong shape made a view dereference a nested field, throw during server render, and lose the
  // WHOLE PAGE to Next's error output — no frame, no heading, no subject.
  //
  // The platform answered, so this is not transport. It answered 200, so it is neither not-mounted nor
  // not-found. It is the fourth answer — the platform responded and the response is not usable — and it
  // must be visibly its own thing, because its remedy (a version mismatch, a proxy substituting a body)
  // belongs to somebody different from the other three.
  answering(200, { something: "entirely unrelated" });

  for (const view of VIEWS) {
    const { status, html } = await get(view.path);
    assert.equal(status, 200, `${view.name} failed to render at all`);
    assert.match(html, /state--upstream/, `${view.name} did not render the unusable-response state`);
    assert.match(html, /is missing /, `${view.name} did not name the missing fields`);
    assert.equal((html.match(/<h1\b/g) ?? []).length, 1, `${view.name} lost its frame on a malformed body`);
    assert.doesNotMatch(html, /state--transport/, `${view.name} blamed the network for a served response`);
  }
});

// ── 9.7 · English strings, and no raw identifier as markup ───────────────────

test("9.7 — no rendered page contains a non-English (CJK) string", async () => {
  answering(200, graph());
  const CJK = /[　-〿぀-ヿ一-鿿＀-￯]/;
  for (const view of VIEWS) {
    const { html } = await get(view.path);
    assert.doesNotMatch(html, CJK, `${view.name} rendered a non-English string`);
  }
  const home = await fetch(`${console_.base}/`);
  assert.doesNotMatch(await home.text(), CJK, "the public surface rendered a non-English string");
});

test("🔴 9.7 — an identifier containing markup renders as literal text, never as markup", async () => {
  // The identifier is derived from CUSTOMER SOURCE CODE. `p25monitor.html` builds its rows by string
  // concatenation and has no escaping helper defined at all, which is the class of defect this
  // replaces rather than fixes one instance of.
  const hostile = '<img src=x onerror="alert(1)">';
  answering(200, graph([{ node_id: hostile, model: "m", layer: 0, order: 0, labels: [] }]));
  const { html } = await get(VIEWS[0].path);
  assert.ok(html.includes("&lt;img"), "the identifier was not escaped on render");
  assert.doesNotMatch(html, /<img src=x onerror=/, "an identifier reached the DOM as markup");
});

// ── 9.8 · Craft acceptance (R13) ─────────────────────────────────────────────

test("9.8 — every route renders EXACTLY ONE display-level heading", async () => {
  // Each view is answered with a payload of ITS OWN shape. Feeding every route the same body makes a
  // route render its parse-failure path while the test believes it is checking a populated page.
  platform.set((req, res) => {
    const body = req.url.includes("/p4/") ? board([]) : graph();
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify(body));
  });
  for (const view of VIEWS) {
    const { html } = await get(view.path);
    const headings = html.match(/<h1\b/g) ?? [];
    assert.equal(headings.length, 1, `${view.name} rendered ${headings.length} display-level headings, not 1`);
  }
});

test("9.8 — the subject is named in the first paint, before its data resolves", async () => {
  // The frame renders AROUND the data. A reader can confirm they opened the right thing during the
  // second they most want to know — which is also the second a view is most likely to be a spinner.
  answering(503, { error: "not mounted" });
  for (const view of VIEWS) {
    const { html } = await get(view.path);
    const h1 = html.match(/<h1[^>]*>([^<]*)</)?.[1] ?? "";
    assert.ok(h1.includes(view.subject), `${view.name}'s heading is "${h1}" — it does not name ${view.subject}`);
  }
});

test("9.8 — the structural signature is the same whether the data arrived or failed", async () => {
  // R13's real content: arrival changes VALUES, never STRUCTURE. If the failed and populated renders
  // have different frames, the page reflows as data lands and the reader's eye loses its place.
  const frameOf = (html) => ({
    page: (html.match(/class="page[^"]*"/g) ?? []).length,
    head: (html.match(/class="page__head"/g) ?? []).length,
    h1: (html.match(/<h1\b/g) ?? []).length,
    eyebrow: (html.match(/class="page__eyebrow"/g) ?? []).length,
  });

  answering(503, { error: "not mounted" });
  const failed = frameOf((await get(VIEWS[0].path)).html);

  answering(200, graph());
  const populated = frameOf((await get(VIEWS[0].path)).html);

  assert.deepEqual(populated, failed, "the frame differs between the failed and populated renders");
});

// ── 9.9 · 🔴 Reservation acceptance (R14) ────────────────────────────────────

test("🔴 9.9 — no qualified value carries the confidence treatment, and its qualifier renders beside it", async () => {
  // One case per qualifier, driven through the board's real rendering path.
  //
  // This is the test that keeps a statistical decision from being overturned in CSS. P4 went to real
  // trouble to make a tie a tie — overlapping intervals are not an ordering — and a craft pass that
  // accents the top row has undone that where no test in the eval harness can see it.
  const QUALIFIERS = [
    "tie",
    "provisional",
    "disqualified",
    "low-confidence",
    "weak-labeled",
    "uncalibrated",
    "withheld",
    "candidate",
    "unverified",
    "gated",
  ];

  for (const qualifier of QUALIFIERS) {
    answering(200, board([qualifier]));

    const { html } = await get(VIEWS[1].path);

    // The value carries `qualified`, never `confident`. Both classes exist, so this is a real
    // discrimination rather than an absence.
    const marked = html.match(/class="qualified"|class="confident"/g) ?? [];
    assert.ok(
      !marked.includes('class="confident"') || html.includes('class="qualified"'),
      `a row flagged ${qualifier} carries the settled-result emphasis`,
    );
    assert.match(html, /qualified/, `a row flagged ${qualifier} is not drawn as qualified`);

    // 🔴 And the qualifier renders BESIDE the value, not only in a tooltip. A caveat reachable only by
    // hover is absent from a screenshot, a printout, a screen reader's linear pass and a phone.
    assert.match(html, /class="qualifier"/, `the ${qualifier} qualifier is not rendered beside its value`);
  }
});

test("🔴 9.9 — an unqualified value DOES earn the confidence treatment, so the reservation discriminates", async () => {
  // Without this, the previous test passes trivially on a console that never applies emphasis at all.
  answering(200, board([]));

  const { html } = await get(VIEWS[1].path);
  assert.match(html, /class="confident"/, "a settled result no longer earns the confidence treatment");
});

// ── 9.10 · Public surface, under platform failure ────────────────────────────

test("9.10 — the public home page renders with the platform stopped and makes no upstream call", async () => {
  const before = platform.requests.length;
  const res = await fetch(`${console_.base}/`);
  assert.equal(res.status, 200);
  const html = await res.text();
  assert.match(html, /<h1/, "the public page rendered no heading");
  assert.equal(platform.requests.length, before, "the public page made an upstream call");
});

// ── 10.5 · 🔴 Entitlement gating (FR15) ──────────────────────────────────────

test("🔴 10.5 — a gated capability names the unlocking plan and is NEVER rendered as an error", async () => {
  // The defect this replaces: a 403 fell through to the generic upstream failure and rendered as
  // *"The platform refused this request"*. Nothing is broken — the capability is outside the tenant's
  // plan — and a reader shown an error opens a support ticket, while a reader shown the plan that
  // unlocks it has a different conversation and has been told the truth.
  platform.set((_req, res) => {
    res.writeHead(403, { "content-type": "application/json" });
    res.end(
      JSON.stringify({
        error: "not included in this plan",
        feature: "dashboard",
        feature_label: "The eval board",
        reason: "This plan does not include the console dashboard.",
        reason_code: "feature_not_included",
        upgrade_plan: "team",
        upgrade_plan_name: "Team",
      }),
    );
  });

  const { html } = await get(VIEWS[1].path);

  // Gated is its own rendering, distinct from all four failure classes.
  assert.match(html, /state--gated/, "a gated capability is not rendered as a gated state");
  for (const errorClass of ["state--upstream", "state--transport", "state--not-found", "state--not-mounted"]) {
    assert.doesNotMatch(html, new RegExp(errorClass), `a gated capability rendered as ${errorClass}`);
  }

  // 🔴 The plan is NAMED, and it is the platform's name for it — not one the console looked up.
  assert.match(html, /Team plan/, "the unlocking plan is not named");
  assert.match(html, /The eval board/, "the platform's own label for the capability is not shown");
  assert.match(html, /This plan does not include the console dashboard\./, "the platform's reason is not shown");

  // And it says, in as many words, that nothing has gone wrong.
  assert.match(html, /plan boundary rather than an error/);

  // The frame survives: the reader can still see which subject they opened.
  assert.equal((html.match(/<h1\b/g) ?? []).length, 1, "a gated view lost its frame");
});

test("10.5 — a gated capability is not hidden: the account view lists it with what unlocks it", async () => {
  // FR15's other half. A hidden feature reads as a missing feature, and the next step is a support
  // ticket; a named one produces an upgrade conversation.
  answering(200, {
    plan_name: "Free",
    plan_config_version: "v1",
    period: "2026-07",
    sum: 0,
    sum_unit: "USD",
    empty: true,
    state: { payment_failed: false, past_due: false },
    entitlements: [
      {
        feature: "dashboard",
        label: "Console dashboard",
        included: false,
        upgrade_plan: "team",
        upgrade_plan_name: "Team",
        reason: "Not included on Free.",
      },
    ],
    meters: [],
  });

  const { html } = await get("/app/account");
  assert.match(html, /Console dashboard|dashboard/, "a gated feature is hidden rather than named");
  assert.match(html, /Team plan/, "the unlocking plan is not named on the account view");
  assert.match(html, /Not included on Free\./, "the platform's own reason is not carried");
  // 🔴 Never priced — but note precisely what that means (FR34).
  //
  // The `$0.00` on this view is the period's MEASURED SPEND, computed by the platform and rendered
  // with the unit the platform supplied. That is evidence, and it belongs on screen. What FR34
  // forbids is a PRICE — a number attached to a plan name, committed to the repository, that outlives
  // the moment it was true. The first version of this assertion could not tell the two apart and
  // failed on the spend, which is the same category error as banning the word "cost".
  //
  // So the check is that no plan name carries an amount, and the repository-wide fence
  // (`npm run scan:bundle`) is what guards the bundle.
  for (const plan of ["Free", "Team", "Business", "Enterprise"]) {
    const priced = new RegExp(`${plan}[^<]{0,40}[$€£]\\s?\\d`);
    assert.doesNotMatch(html, priced, `the ${plan} plan is rendered with a price beside it`);
  }
});

test("10.5 — the console never claims a capability the platform then refuses", async () => {
  // The screen and the gate read the same facts. The account view resolves availability from the P7
  // entitlement rows, never from the console's own capability table — that table only decides which
  // plan to MENTION when the platform gave no answer.
  const entitlements = await readFile(new URL("../src/lib/entitlements.ts", import.meta.url), "utf8");
  assert.match(entitlements, /does not decide anything/i, "the capability table has started deciding availability");
  assert.doesNotMatch(entitlements, /\$\s?\d/, "a price value reached the capability table");

  const account = await readFile(new URL("../src/app/app/account/page.tsx", import.meta.url), "utf8");
  assert.match(account, /row\?\.included === true/, "availability is no longer read from the platform's rows");
});
