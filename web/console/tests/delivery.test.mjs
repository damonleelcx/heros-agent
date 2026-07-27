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
