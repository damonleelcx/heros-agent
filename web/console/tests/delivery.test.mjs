// delivery.test.mjs is the P9 §11c.2/§11c.3 acceptance surface: the delivery view renders each
// delivery's lifecycle state linked back to the proposal that produced it (11c.2), and a
// missing / degraded / revoked route renders as a CONDITION WITH A NEXT ACTION — never as an empty
// list, which is the rendering that makes an invisible failure look normal (11c.3, FR13).
//
// It runs the real console against a stub platform and asserts on rendered HTML, the same way
// link-coverage.test.mjs and the rest of the acceptance gate do — a property of the two-hop boundary,
// verified by rendering rather than asserted in a PR description.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { startStubPlatform, startConsole, signIn } from "./support/harness.mjs";

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

function answering(status, body) {
  platform.set((_req, res) => {
    res.writeHead(status, { "content-type": "application/json" });
    res.end(JSON.stringify(body));
  });
}

async function get(path) {
  const res = await fetch(`${console_.base}${path}`, { headers: { cookie }, redirect: "manual" });
  return { status: res.status, html: await res.text() };
}

// delivery builds one DeliveryView; only the fields a case cares about need overriding.
function delivery(over = {}) {
  return {
    delivery_id: "d1",
    state: "opened",
    config_hash: "cfg_abc",
    source_revision: "rev1",
    target: "acme/serviced",
    mode: "ci",
    forge_ref: "acme/serviced#42",
    proposal_ref: "/app/workflows/wf1/proposals/p1",
    ...over,
  };
}

// view wraps deliveries + a route condition into the DeliveriesView the page reads.
function view(deliveries, route = { kind: "configured" }) {
  return { deliveries, route };
}

test("11c.2 — each delivery renders its state, linked back to the proposal that produced it", async () => {
  answering(
    200,
    view([
      delivery({ delivery_id: "d1", state: "opened", proposal_ref: "/app/workflows/wf1/proposals/p1" }),
      delivery({ delivery_id: "d2", state: "merged", merge_commit: "abc123", forge_ref: "acme/serviced#7" }),
      delivery({ delivery_id: "d3", state: "superseded", forge_ref: "acme/serviced#3" }),
      delivery({ delivery_id: "d4", state: "closed", forge_ref: "acme/serviced#2" }),
    ]),
  );
  const { html } = await get("/app/delivery");

  // The lifecycle states are each named as a WORD, not only as a colour (the Status primitive maps
  // `opened` → "open"; the others render verbatim). A state must never read as colour alone.
  for (const word of ["open", "merged", "superseded", "closed"]) {
    assert.match(html, new RegExp(word, "i"), `the "${word}" state is not rendered as a word`);
  }
  // Row-specific proof the merged delivery rendered with its detail, not just the summary stat label.
  // (React splits `merge {commit}` around the interpolation, so the hash is its own text node.)
  assert.match(html, /abc123/, "the merged delivery did not render its merge commit");
  // 11c.2 — the loop from proposal to outcome is one link, not a search.
  assert.match(html, /href="\/app\/workflows\/wf1\/proposals\/p1"/, "a delivery does not link back to its proposal");
  assert.match(html, /Open evidence/, "the proposal link is not labelled");
  // The merged delivery is the only billable outcome, and says so — a settled fact stated plainly.
  assert.match(html, /shipped — the only billable outcome/i);
});

test("11c.3 — no configured route renders as a condition WITH a next action, not a bare empty", async () => {
  answering(
    200,
    view([], {
      kind: "no_route",
      detail: "No delivery route is configured for this repository.",
      next_action: "Connect the hosted Git App from the account view.",
    }),
  );
  const { html } = await get("/app/delivery");

  // The condition is stated, its next action is stated, and it is explicitly NOT an error or an empty
  // result — the three things FR13 requires so silence is never read as "the product found nothing".
  assert.match(html, /No delivery route configured/i, "the no-route condition is not surfaced");
  assert.match(html, /Next:/, "the no-route condition carries no next action");
  assert.match(html, /Connect the hosted Git App/i, "the next action text is missing");
  assert.match(html, /reported condition, not an error and not an empty result/i);
  // 🚫 The bare 'Empty' primitive ("This is a real state, not a failure to load") is the CONFIGURED-route
  // wording; it must NOT be what a reader sees when the route itself is the problem.
  assert.doesNotMatch(html, /it is delivered\s+as a pull request and appears here/i,
    "an unconfigured route fell through to the configured-route empty state");
});

test("11c.3 — a revoked route reads as revoked (a hazard), with its next action", async () => {
  answering(
    200,
    view([], {
      kind: "revoked",
      detail: "The hosted Git App was removed from this repository.",
      next_action: "Re-install the hosted Git App to resume delivery.",
    }),
  );
  const { html } = await get("/app/delivery");
  assert.match(html, /Delivery is revoked/i, "a revoked route is not surfaced as revoked");
  assert.match(html, /Re-install the hosted Git App/i, "the revoked condition carries no next action");
  // Verified proposals are not lost — the banner says so, because a revoked route is recoverable.
  assert.match(html, /verified proposals are not\s+lost/i);
});

test("11c.3 — a degraded route reads as degraded, distinct from revoked and from no-route", async () => {
  answering(
    200,
    view([], { kind: "degraded", detail: "Delivery is temporarily degraded.", next_action: "No action needed; it will resume." }),
  );
  const { html } = await get("/app/delivery");
  assert.match(html, /Delivery is degraded/i, "a degraded route is not surfaced");
  assert.doesNotMatch(html, /Delivery is revoked/i, "degraded was collapsed into revoked");
  assert.doesNotMatch(html, /No delivery route configured/i, "degraded was collapsed into no-route");
});

test("11c.2/11c.3 — a configured route with deliveries shows no condition banner", async () => {
  answering(200, view([delivery({ state: "merged", merge_commit: "abc123" })], { kind: "configured" }));
  const { html } = await get("/app/delivery");
  // A configured, working route is silent — no condition banner competes with the data.
  assert.doesNotMatch(html, /reported condition, not an error/i, "a configured route rendered a condition banner");
  assert.match(html, /merged/i, "the delivery did not render under a configured route");
});

test("degradation — an unmounted delivery subsystem (503) is not rendered as 'no deliveries'", async () => {
  // The R5 distinction that 11c.3 turns on at the transport level: a subsystem that is not mounted is a
  // condition to report, never an empty list. `load` classifies a 503 as not-mounted before any body.
  answering(503, { error: "forge-delivery is not mounted on this deployment" });
  const { html } = await get("/app/delivery");
  assert.doesNotMatch(html, /No deliveries yet\./i, "a 503 was rendered as an empty delivery list");
  // ...it is rendered as the not-mounted condition instead (the R5 distinction, carried two hops).
  assert.match(html, /not mounted on this deployment/i, "a 503 did not surface as the not-mounted condition");
});

// ── P13 13e · the route ledger ───────────────────────────────────────────────
//
// The tab exists because "no deliveries" and "this change can never be delivered" used to render the
// same way. These cases assert the second is now stated, with a cause and an owner, and that a
// permanent boundary cannot be mistaken for work in progress.

// routesView builds a ChangeDeliveryView shaped like the platform's, with the three interesting rows:
// a live cell, a permanent boundary, and a named platform gap.
function routesView(over = {}) {
  return {
    version: "cov_v1",
    routes: [
      { id: "source", label: "Pull request", permanence: "Permanent. A human merges it." },
      { id: "runtime", label: "Gradual rollout", permanence: "Temporary. Expires to the parent arm." },
    ],
    causes: [
      {
        id: "not-runtime-resolvable",
        owner: "nobody",
        permanent: true,
        label: "Not resolvable at run time — the change is program structure, not data.",
      },
      { id: "node-not-bound", owner: "you", permanent: false, label: "This node applies inline." },
      {
        id: "no-rollout-binding",
        owner: "the platform",
        permanent: false,
        label: "The binding document has no field for this axis yet.",
      },
    ],
    states: [
      { id: "undeliverable", label: "Undeliverable — every route refused, and each names why." },
      { id: "rollout-active", label: "A rollout is running. That is evidence, not a delivery." },
      { id: "delivered", label: "Delivered — a pull request carrying this change was merged." },
    ],
    languages: ["go", "python"],
    cells: [
      { axis: "model", change: "model-within-provider", route: "source", status: "delivers" },
      { axis: "model", change: "model-within-provider", route: "runtime", status: "delivers", bound_only: true },
      { axis: "wiring", change: "wiring", route: "source", status: "delivers" },
      {
        axis: "wiring",
        change: "wiring",
        route: "runtime",
        status: "refuses",
        cause: "not-runtime-resolvable",
        owner: "nobody",
        permanent: true,
        note: "Order and concurrency are compiled program structure.",
      },
      {
        axis: "memory",
        change: "memory-strategy",
        route: "source",
        status: "refuses",
        cause: "not-expressible-at-a-call-site",
        owner: "nobody",
        permanent: true,
        note: "Refused at transform.",
      },
      {
        axis: "memory",
        change: "memory-strategy",
        route: "runtime",
        status: "refuses",
        cause: "not-runtime-resolvable",
        owner: "nobody",
        permanent: true,
        note: "A memory strategy needs a store that persists between invocations.",
      },
      {
        axis: "tools",
        change: "tool-set",
        route: "source",
        status: "delivers",
      },
      {
        axis: "tools",
        change: "tool-set",
        route: "runtime",
        status: "refuses",
        cause: "no-rollout-binding",
        owner: "the platform",
        permanent: false,
        missing_artifact: "binding document field: nodes[].tool_set",
        note: "Selecting among already-constructed tools is a set.",
      },
    ],
    source_cells: [
      { change: "model-within-provider", language: "go", status: "delivers" },
      { change: "model-within-provider", language: "python", status: "refuses", cause: "call-site-cannot-carry-it", permanent: true },
      { change: "memory-strategy", language: "go", status: "refuses", cause: "no-materializer-for-this-language", missing_artifact: "a memory rewriter" },
      { change: "memory-strategy", language: "python", status: "refuses", cause: "no-materializer-for-this-language", missing_artifact: "a memory rewriter" },
      { change: "wiring", language: "go", status: "delivers" },
      { change: "wiring", language: "python", status: "delivers" },
      { change: "tool-set", language: "go", status: "delivers" },
      { change: "tool-set", language: "python", status: "refuses", cause: "no-materializer-for-this-language", missing_artifact: "a python tool splitter" },
    ],
    ...over,
  };
}

// answeringByPath routes the two calls the page makes to their own payloads. The single-handler helper
// above would hand the delivery list to the ledger, which is exactly the kind of shape confusion the
// generated types exist to prevent — so the stub is honest about there being two endpoints.
function answeringByPath(deliveriesBody, routesBody) {
  platform.set((req, res) => {
    const body = req.url.startsWith("/api/v1/change-delivery") ? routesBody : deliveriesBody;
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify(body));
  });
}

test("23.18 — the ledger states BOTH routes for every change, never one status word", async () => {
  answeringByPath(view([]), routesView());
  const { html } = await get("/app/delivery");

  // Both route columns are present, and each carries its permanence sentence — the line that keeps a
  // rollout from being read as a deployment.
  assert.match(html, /Pull request/, "the source route column is missing");
  assert.match(html, /Gradual rollout/, "the runtime route column is missing");
  assert.match(html, /Temporary\. Expires to the parent arm\./, "the runtime route does not state that it is temporary");
  assert.match(html, /rollout is evidence, not delivery/i, "the surface does not say a rollout is not a delivery");

  // Every change kind in the payload has a row. A change with no row renders as "not applicable".
  for (const change of ["model within provider", "wiring", "memory strategy", "tool set"]) {
    assert.match(html, new RegExp(change, "i"), `the ledger has no row for "${change}"`);
  }
});

test("23.18 — a change both routes refuse reads as undeliverable, never as pending or queued", async () => {
  answeringByPath(view([]), routesView());
  const { html } = await get("/app/delivery");

  assert.match(html, /undeliverable/i, "a change refused by both routes is not marked undeliverable");
  // 🚫 The words a dead end used to borrow. None of them may appear as a delivery state.
  for (const word of ["queued", "in review", "processing"]) {
    assert.doesNotMatch(
      html,
      new RegExp(`>\\s*${word}\\s*<`, "i"),
      `the delivery surface renders "${word}" as a state`,
    );
  }
});

test("23.19 — a permanent boundary carries no artifact and no date; a platform gap names one", async () => {
  answeringByPath(view([]), routesView());
  const { html } = await get("/app/delivery");

  // The gap names its missing artifact and its owner.
  assert.match(html, /binding document field: nodes\[\]\.tool_set/, "the platform gap does not name its missing artifact");
  assert.match(html, /the platform/i, "the platform gap does not name its owner");

  // 🔴 …and the boundary does not borrow that treatment. The wiring/runtime cell is permanent, so no
  // "Missing:" label may be attached to it, and no completion language may appear anywhere.
  assert.match(html, /Order and concurrency are compiled program structure/, "the boundary's own sentence is missing");

  // 🔴 Exactly ONE ledger cell may name a missing artifact — the tool-set gap. The fixture contains
  // three permanent refusals (wiring/runtime, memory/source, memory/runtime); if any of them rendered
  // an artifact, the count would rise and a boundary would be wearing a backlog item's clothes.
  //
  // The match is deliberately the LEDGER's own markup (`Missing: <span class="mono">`) rather than the
  // bare word: the per-language table carries `title="Missing: …"` tooltips, and the RSC flight payload
  // repeats every string with escaped quotes. Counting the bare word would measure the serialiser.
  const ledgerArtifacts = (html.match(/Missing: <span class="mono">/g) ?? []).length;
  assert.equal(
    ledgerArtifacts,
    1,
    `expected exactly one ledger cell to name a missing artifact, found ${ledgerArtifacts}`,
  );

  // No date, milestone or quarter is attached to any refusal.
  assert.doesNotMatch(html, /\bQ[1-4]\b|\bcoming soon\b|\bon the roadmap\b/i,
    "a refusal carries a date or a roadmap promise");
});

test("23.18 — the ledger is unavailable rather than partial when the platform cannot be read", async () => {
  platform.set((req, res) => {
    if (req.url.startsWith("/api/v1/change-delivery")) {
      res.writeHead(503, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: "upstream" }));
      return;
    }
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify(view([])));
  });
  const { html } = await get("/app/delivery");

  assert.match(html, /route ledger is unavailable/i, "an unreadable ledger did not say so");
  // 🚫 And it did not fall back to a plausible table nobody verified.
  assert.doesNotMatch(html, /binding document field/, "an unreadable ledger rendered a local fallback table");
});

// ── An unusable 200 is not a usable one ──────────────────────────────────────
//
// # 🔴 Three reads, one class of defect, and two of them took the page down
//
// `load()`'s `requires` mechanism exists because "the platform answered 200 and the body is not
// something I can render" is a FOURTH outcome, distinct from not-mounted, not-found and transport
// failure. `view.ts` records what happens without it: a view dereferences a nested field, throws during
// server render, and Next replaces the WHOLE PAGE with its error output — no frame, no heading, no
// subject.
//
// It happened again, three times, and the cases are deliberately kept apart below because they have
// different severities and different fixes:
//
//   · `axis-projection` → `{state:"ok","projection":{}}`   HTTP 500. `ProjectionBody` opens with
//     `projection.totals.find(...)`, and EVERY axis surface renders that panel.
//   · `delivery-projection` → `{}`                          HTTP 500. `YourNodesTab` opens with
//     `p.rows.filter(...)`.
//   · `change-delivery` → `{}`                              HTTP 200, and WRONG: every dereference in
//     `deliveryRoutes.tsx` carries a `??`, so a partial body rendered as a COMPLETE answer — an empty
//     ledger and `read against coverage table undefined`.
//
// The third is the one this file's own banner already described: *"Showing a partial answer would be
// worse than showing none, because a missing row reads as 'not applicable' — a claim about your code
// that nobody made."* The copy was right and the code implemented it only for a failed fetch.

test("🔴 a partial route ledger is unavailable rather than partial — a 200 is not a usable body", async () => {
  // The exact shape: a 200 with an empty object. Before the fix this rendered as a complete ledger.
  platform.set((req, res) => {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify(req.url.startsWith("/api/v1/change-delivery") ? {} : view([])));
  });
  const { status, html } = await get("/app/delivery");

  assert.equal(status, 200, "an unusable ledger body took the page down");
  assert.match(html, /route ledger is unavailable/i, "a partial ledger rendered as a complete one");
  // 🚫 And specifically NOT the heading that states a version it does not have.
  assert.doesNotMatch(html, /coverage table undefined/, "the page printed a version the body never carried");
});

test("🔴 a partial axis projection renders read-failed, and does not take the page down", async () => {
  // 🔴 The worst of the three: every axis surface renders `AxisProjectionPanel`, so this one 500'd
  // `/app/context`, `/app/memory`, `/app/harness`, `/app/graph`, `/app/studio`, `/app/authoring`,
  // `/app/delivery` and `/app/coverage` at once.
  platform.set((req, res) => {
    res.writeHead(200, { "content-type": "application/json" });
    if (req.url.includes("axis-projection")) return res.end(JSON.stringify({ state: "ok", projection: {} }));
    if (req.url === "/api/v1/workflows") return res.end(JSON.stringify({ workflows: [{ workflow_id: "wf1", nodes: 1 }] }));
    res.end(JSON.stringify(view([])));
  });

  for (const route of ["/app/delivery", "/app/coverage"]) {
    const { status, html } = await get(route);
    assert.equal(status, 200, `${route} was taken down by an unusable projection body`);
    // 🚫 NOT `not-reported`. Telling the reader they have reported nothing would be a claim about THEM
    // drawn from a defect on OUR side, and it names the wrong next action.
    assert.doesNotMatch(
      html,
      /has not been told this organization|reported no workflow structure/i,
      `${route} rendered our own defect as the customer having reported nothing`,
    );
  }
});

test("🔴 a partial delivery projection renders read-failed, and does not take the page down", async () => {
  platform.set((req, res) => {
    res.writeHead(200, { "content-type": "application/json" });
    if (req.url.includes("delivery-projection")) return res.end(JSON.stringify({}));
    if (req.url === "/api/v1/workflows") return res.end(JSON.stringify({ workflows: [{ workflow_id: "wf1", nodes: 1 }] }));
    res.end(JSON.stringify(view([])));
  });
  const { status, html } = await get("/app/delivery");

  assert.equal(status, 200, "an unusable delivery-projection body took the page down");
  assert.match(html, /could not be read/i, "the unusable body was not reported as a read failure");
});

// ── P14 12.8 · the two tools cells are separate rows ─────────────────────────
//
// One row reading "tools are not rollout-eligible" would be true of both cells and useful for neither:
// binding is a boundary (stop asking), tool-set selection is a schema gap (ask again once the field
// lands). This axis already made exactly this mistake once for language coverage.
test("12.8 — skill binding and tool-set render as separate rows with different causes", async () => {
  answeringByPath(
    view([]),
    routesView({
      cells: [
        { axis: "skills", change: "skill-binding", route: "source", status: "varies-by-language" },
        {
          axis: "skills",
          change: "skill-binding",
          route: "runtime",
          status: "refuses",
          cause: "not-runtime-resolvable",
          owner: "nobody",
          permanent: true,
          note: "Binding a skill CONSTRUCTS a provider SDK tool value.",
        },
        { axis: "tools", change: "tool-set", route: "source", status: "varies-by-language" },
        {
          axis: "tools",
          change: "tool-set",
          route: "runtime",
          status: "refuses",
          cause: "no-rollout-binding",
          owner: "the platform",
          permanent: false,
          missing_artifact: "binding document field: nodes[].tool_set",
          note: "Selecting among already-constructed tools is a set.",
        },
      ],
      source_cells: [
        { change: "skill-binding", language: "go", status: "delivers" },
        { change: "skill-binding", language: "python", status: "refuses", cause: "no-materializer-for-this-language", missing_artifact: "a python binder" },
        { change: "tool-set", language: "go", status: "delivers" },
        { change: "tool-set", language: "python", status: "refuses", cause: "no-materializer-for-this-language", missing_artifact: "the python frontend recording each tool's declaration" },
      ],
    }),
  );
  const { html } = await get("/app/delivery");

  // Two rows, not one.
  assert.match(html, /skill binding/i, "there is no skill-binding row");
  assert.match(html, /tool set/i, "there is no tool-set row");

  // …carrying DIFFERENT treatments: the boundary states its reason with no artifact, the gap names one.
  assert.match(html, /Binding a skill CONSTRUCTS a provider SDK tool value/, "the boundary's own sentence is missing");
  assert.match(html, /binding document field: nodes\[\]\.tool_set/, "the tool-set gap does not name its missing field");

  // 🔴 Exactly one of the two names a missing artifact in the ledger. If the boundary borrowed the gap's
  // rendering, this would be two.
  const ledgerArtifacts = (html.match(/Missing: <span class="mono">/g) ?? []).length;
  assert.equal(ledgerArtifacts, 1, `expected one ledger artifact, found ${ledgerArtifacts}`);

  // 🚫 And the axis is never collapsed into one verdict about "tools".
  assert.doesNotMatch(html, /tools are not rollout-eligible/i, "the two tools cells were collapsed into one row");
});

// ── P15 21.6 · the wiring boundary cannot acquire a date ─────────────────────
//
// This refusal degrades slowly and quietly: first into a roadmap item, then into an exception. The
// assertion is that no wiring cell carries an artifact, a milestone or a "not yet" rendering, and that
// it stays visually distinct from a cell that legitimately owes work.
test("21.6 — the wiring row is a boundary: no artifact, no date, distinct from a platform gap", async () => {
  answeringByPath(
    view([]),
    routesView({
      cells: [
        { axis: "wiring", change: "wiring", route: "source", status: "varies-by-language" },
        {
          axis: "wiring",
          change: "wiring",
          route: "runtime",
          status: "refuses",
          cause: "not-runtime-resolvable",
          owner: "nobody",
          permanent: true,
          note: "Order and concurrency are compiled program structure. No document reorders statements in a built binary.",
        },
        { axis: "tools", change: "tool-set", route: "source", status: "varies-by-language" },
        {
          axis: "tools",
          change: "tool-set",
          route: "runtime",
          status: "refuses",
          cause: "no-rollout-binding",
          owner: "the platform",
          permanent: false,
          missing_artifact: "binding document field: nodes[].tool_set",
          note: "A set is data.",
        },
      ],
      source_cells: [
        { change: "wiring", language: "go", status: "delivers" },
        { change: "wiring", language: "python", status: "delivers" },
        { change: "tool-set", language: "go", status: "delivers" },
        { change: "tool-set", language: "python", status: "refuses", cause: "no-materializer-for-this-language", missing_artifact: "a python splitter" },
      ],
    }),
  );
  const { html } = await get("/app/delivery");

  assert.match(html, /Order and concurrency are compiled program structure/, "the wiring boundary's reason is missing");
  // 🔴 Exactly one ledger cell names an artifact — the tool-set gap. If wiring borrowed that rendering
  // this would be two, and "never" would have started reading as "not yet".
  const ledgerArtifacts = (html.match(/Missing: <span class="mono">/g) ?? []).length;
  assert.equal(ledgerArtifacts, 1, `expected one ledger artifact, found ${ledgerArtifacts}`);
  // No date, milestone or roadmap language anywhere on the surface.
  assert.doesNotMatch(html, /\bQ[1-4]\b|coming soon|on the roadmap|will land/i,
    "a refusal carries a date or a roadmap promise");
  // The two causes render as different states, so a reader can branch without reading prose.
  assert.match(html, /NOT DATA/i, "the boundary does not render its own state label");
  assert.match(html, /NOT YET — OURS/i, "the platform gap does not render its own state label");
});

// ── P16 11.5 · the context axis splits, and the split is legible ─────────────
//
// "Context strategy" is one name for two facts that land in different columns. A retrieval parameter is
// a number the document was built to carry; a selection policy is a DELETION no document performs in
// built code. Collapsing them into one "context" verdict tells one reader to stop asking about something
// we can build, and the other to wait for something that will never arrive.
test("11.5 — retrieval params and selection policy render as separate cells with different causes", async () => {
  answeringByPath(
    view([]),
    routesView({
      cells: [
        { axis: "context", change: "retrieval-params", route: "source", status: "varies-by-language" },
        {
          axis: "context",
          change: "retrieval-params",
          route: "runtime",
          status: "refuses",
          cause: "no-rollout-binding",
          owner: "the platform",
          permanent: false,
          missing_artifact: "binding document field: nodes[].retrieval (top_k, token budget, similarity floor)",
          note: "A retrieval parameter is a NUMBER — exactly the kind of fact the document exists to carry.",
        },
        { axis: "context", change: "selection-policy", route: "source", status: "varies-by-language" },
        {
          axis: "context",
          change: "selection-policy",
          route: "runtime",
          status: "refuses",
          cause: "not-runtime-resolvable",
          owner: "nobody",
          permanent: true,
          note: "A selection policy is applied by DELETING the turns it does not retain from a constructed message list.",
        },
      ],
      source_cells: [
        { change: "retrieval-params", language: "go", status: "delivers" },
        { change: "retrieval-params", language: "python", status: "varies-by-language", cause: "call-site-cannot-carry-it" },
        { change: "selection-policy", language: "go", status: "delivers" },
        { change: "selection-policy", language: "python", status: "delivers" },
      ],
    }),
  );
  const { html } = await get("/app/delivery");

  // Two rows, two treatments.
  assert.match(html, /retrieval params/i, "there is no retrieval-params row");
  assert.match(html, /selection policy/i, "there is no selection-policy row");
  assert.match(html, /NOT YET — OURS/i, "the retrieval cell does not read as a gap we own");
  assert.match(html, /NOT DATA/i, "the policy cell does not read as a boundary");

  // The retrieval cell reads as one that CAN gain a row: it names the field, and it is the only one
  // that does. The policy cell must not borrow that rendering.
  assert.match(html, /nodes\[\]\.retrieval/, "the retrieval gap does not name its missing field");
  const ledgerArtifacts = (html.match(/Missing: <span class="mono">/g) ?? []).length;
  assert.equal(ledgerArtifacts, 1, `expected one ledger artifact, found ${ledgerArtifacts}`);

  // 🚫 And the axis is never collapsed into a single verdict about "context".
  assert.doesNotMatch(html, /context is not rollout-eligible/i, "the two context cells were collapsed");
});

// ── the per-language table distinguishes "some" from "never" ─────────────────
test("11.5 — a partially covered language reads as 'some', not as a flat refusal", async () => {
  answeringByPath(
    view([]),
    routesView({
      source_cells: [
        { change: "retrieval-params", language: "go", status: "delivers" },
        {
          change: "retrieval-params",
          language: "python",
          status: "varies-by-language",
          cause: "call-site-cannot-carry-it",
          note: "3 of 8 call-site forms in python materialize this axis; the call site's own form decides.",
        },
        { change: "retrieval-params", language: "rust", status: "refuses", cause: "no-materializer-for-this-language", missing_artifact: "a rust splitter" },
      ],
      cells: [
        { axis: "context", change: "retrieval-params", route: "source", status: "varies-by-language" },
        { axis: "context", change: "retrieval-params", route: "runtime", status: "refuses", cause: "no-rollout-binding", owner: "the platform", missing_artifact: "nodes[].retrieval" },
      ],
      languages: ["go", "python", "rust"],
    }),
  );
  const { html } = await get("/app/delivery");

  // Three distinct marks, because there are three distinct facts. A partially covered language rendered
  // as a flat refusal would tell a reader with a covered SDK that nothing can ship.
  assert.match(html, />diff</, "a fully covered language does not read as producing a diff");
  assert.match(html, />some</, "a partially covered language does not read as partial");
  assert.match(html, />not yet</, "an uncovered language does not read as ours to close");
});

// ── P17 14.2 · contingent is rendered differently from permanent ─────────────
//
// Memory and wiring both refuse as "not data" and carry the same cause. Wiring is a property of
// compiled code; memory waits on a runtime component that could exist. A reader who cannot tell them
// apart either stops asking about something merely unbuilt, or keeps waiting on something that cannot
// be built. So the difference is rendered, and it is rendered from a boolean rather than from prose.
test("14.2 — the memory row names a missing component; the wiring row names none, and neither carries a date", async () => {
  answeringByPath(
    view([]),
    routesView({
      cells: [
        { axis: "memory", change: "memory-strategy", route: "source", status: "refuses", cause: "unsafe-rewrite", owner: "nobody", permanent: true, note: "Refused at transform." },
        {
          axis: "memory",
          change: "memory-strategy",
          route: "runtime",
          status: "refuses",
          cause: "not-runtime-resolvable",
          owner: "nobody",
          contingent: true,
          missing_component: "a memory store running in the customer's process, and the document schema to point at it",
          note: "A memory strategy needs a STORE that persists between invocations.",
        },
        { axis: "wiring", change: "wiring", route: "source", status: "varies-by-language" },
        {
          axis: "wiring",
          change: "wiring",
          route: "runtime",
          status: "refuses",
          cause: "not-runtime-resolvable",
          owner: "nobody",
          permanent: true,
          note: "Order and concurrency are compiled program structure.",
        },
      ],
      source_cells: [
        { change: "memory-strategy", language: "go", status: "refuses", cause: "unsafe-rewrite" },
        { change: "memory-strategy", language: "python", status: "refuses", cause: "unsafe-rewrite" },
        { change: "wiring", language: "go", status: "delivers" },
        { change: "wiring", language: "python", status: "delivers" },
      ],
    }),
  );
  const { html } = await get("/app/delivery");

  // The two states render distinguishably — different labels, so a reader can branch at a glance.
  assert.match(html, /needs a component/i, "the contingent refusal does not render its own state");
  assert.match(html, /NOT DATA/i, "the permanent boundary does not render its own state");

  // The contingent one names WHAT it waits on…
  assert.match(html, /Waiting on:/, "the contingent refusal does not name the missing component");
  assert.match(html, /a memory store running in the customer's process/, "the component is not named");

  // 🚫 …and neither carries a date, a milestone, or a commitment. Naming a missing component is not a
  // promise to build it.
  assert.doesNotMatch(html, /\bQ[1-4]\b|coming soon|on the roadmap|will land|next release/i,
    "a refusal carries a commitment");

  // Memory is refused by BOTH routes and by every language, so it reads as undeliverable — the state
  // that used to be an empty list nobody could explain.
  assert.match(html, /undeliverable/i, "memory does not read as undeliverable");
});

// ── P18 15.6 · scaffold, params and host condition are three distinct states ──
//
// `hostAbsent` and `notRuntimeResolvable` both mention the runtime and mean opposite things. One is
// answered by starting a service; the other cannot be answered. A surface that rendered them alike
// would send an operator to restart something that was never the problem.
test("15.6 — the harness strategy cell, the params cell and a host condition read as three different things", async () => {
  answeringByPath(
    view([]),
    routesView({
      cells: [
        { axis: "harness", change: "harness-strategy", route: "source", status: "varies-by-language" },
        {
          axis: "harness",
          change: "harness-strategy",
          route: "runtime",
          status: "refuses",
          cause: "not-runtime-resolvable",
          owner: "nobody",
          permanent: true,
          note: "A strategy swap changes how many calls the program makes and in what control flow. That is a loop.",
        },
        { axis: "harness", change: "harness-params", route: "source", status: "varies-by-language" },
        {
          axis: "harness",
          change: "harness-params",
          route: "runtime",
          status: "refuses",
          cause: "no-rollout-binding",
          owner: "the platform",
          permanent: false,
          missing_artifact: "binding document field: nodes[].harness_params (turn ceiling, retry budget, stop condition)",
          note: "max_turns, the retry budget and the stop condition are parameters of a loop ALREADY WRITTEN.",
        },
      ],
      source_cells: [
        { change: "harness-strategy", language: "go", status: "refuses", cause: "unsafe-rewrite" },
        { change: "harness-strategy", language: "python", status: "refuses", cause: "unsafe-rewrite" },
        { change: "harness-params", language: "go", status: "varies-by-language", cause: "unsafe-rewrite" },
        { change: "harness-params", language: "python", status: "varies-by-language", cause: "unsafe-rewrite" },
      ],
    }),
  );
  const { html } = await get("/app/delivery");

  // Two harness rows, two treatments.
  assert.match(html, /harness strategy/i, "there is no harness-strategy row");
  assert.match(html, /harness params/i, "there is no harness-params row");
  assert.match(html, /NOT DATA/i, "the strategy cell does not read as a boundary");
  assert.match(html, /NOT YET — OURS/i, "the params cell does not read as a gap we own");
  assert.match(html, /nodes\[\]\.harness_params/, "the params gap does not name its missing field");

  // 🚫 No delivery refusal on this surface mentions a host service or offers restarting one — that is a
  // different condition with a different remedy, and conflating them wastes an operator's afternoon.
  assert.doesNotMatch(html, /host service is not running/i,
    "a delivery refusal rendered an execution condition");
  assert.doesNotMatch(html, /restart the (host|service)/i,
    "a delivery refusal offered starting a service as a remedy");

  // Only the params cell names an artifact; the strategy boundary must not borrow that rendering.
  const ledgerArtifacts = (html.match(/Missing: <span class="mono">/g) ?? []).length;
  assert.equal(ledgerArtifacts, 1, `expected one ledger artifact, found ${ledgerArtifacts}`);
});
