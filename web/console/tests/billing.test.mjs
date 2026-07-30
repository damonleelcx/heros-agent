// billing.test.mjs is the P21 §6 acceptance surface: the console billing page renders the plan BY
// NAME, this period's usage, the invoice breakdown with each line's basis, and the payment-method
// status — every figure from the API and none of them a price — and it renders the four unhappy states
// as four DIFFERENT facts rather than as one empty page.
//
// It runs the real console against a stub platform and asserts on rendered HTML, the same way
// delivery.test.mjs and the rest of the acceptance gate do. The properties under test live at the
// boundary: what the page does with a document it did not author, and what it does when there is not
// one.

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

/** answering makes every upstream call return one document. */
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

/** payment builds a PaymentView with the shape the generated contract describes. */
function payment(overrides = {}) {
  const { billing: billingOverrides, ...rest } = overrides;
  const billing = {
    customer_id: "tenant-hermes",
    period: "2026-07",
    sum: 432,
    sum_unit: "USD",
    sum_trend: [
      { period: "2026-06", sum: 505, baseline_sum: 505, optimized_sum: 432 },
      { period: "2026-07", sum: 432, baseline_sum: 505, optimized_sum: 432 },
    ],
    meters: [
      { metric: "sum", label: "Spend under management", value: 432, unit: "USD", allowed: 300, unlimited: false, over: true, reported_to_provider: true },
      { metric: "retention", label: "Retention", value: 30, unit: "days", allowed: 0, unlimited: true, over: false, reported_to_provider: true },
    ],
    plan_id: "team",
    plan_name: "Team",
    plan_config_version: "cfg-1",
    entitlements: [],
    invoice: {
      invoice_ref: "in_1",
      status: "open",
      lines: [
        { kind: "metered", label: "metered", basis: "usage_record:tenant-hermes/2026-07/sum", amount_ref: "stripe:amount:ii_1", quantity: 432, charge_ref: "ii_1" },
        { kind: "gainshare", label: "gainshare", basis: "billable_savings:tenant-hermes/2026-07", amount_ref: "stripe:amount:ii_2", quantity: 73, charge_ref: "ii_2",
          evidence: [{ kind: "merge", ref: "abc1234", label: "async_call_llm in agent/auxiliary_client.py", link: "https://example.test/commit/abc1234" }] },
      ],
      totals: [],
    },
    state: { invoice_status: "open", subscription_status: "active", payment_failed: false, past_due: false },
    savings: { gainshare_consent: true, consent_available: true, baseline_sum: 505, optimized_sum: 432, verified_savings: 73, unit: "USD", none_verified: false },
    empty: false,
    ...(billingOverrides ?? {}),
  };
  return {
    billing,
    plans: [
      { plan_id: "free", name: "Free", rank: 0, current: false, direction: "downgrade", subscribable: false },
      { plan_id: "team", name: "Team", rank: 1, current: true, direction: "current", subscribable: true },
      { plan_id: "business", name: "Business", rank: 2, current: false, direction: "upgrade", subscribable: true },
    ],
    payment_method: { present: true, brand: "visa", last4: "4242", status: "ok" },
    collection_available: true,
    ...rest,
  };
}

test("the billing page names the plan, and never prices it (FR34, P21 D7)", async () => {
  answering(200, payment());
  const { status, html } = await get("/app/billing");
  assert.equal(status, 200);

  assert.match(html, /Team/, "the plan is named");
  // 🔴 No price, no currency amount other than the SUM figure the server supplied, and no rate.
  // A page that showed a price would be showing a number the platform does not hold.
  assert.doesNotMatch(html, /\bper\s?month\b|\/mo\b|per\s?seat\b/i, "the page quotes no rate");
  assert.doesNotMatch(html, /price[_-]?(amount|value|band)/i, "the page carries no priced field");
  // The SUM is the customer's own spend under management, read back from the API — not a price.
  assert.match(html, /432/, "the period's SUM is rendered from the API");
});

test("every invoice line names the basis that justified it (FR6)", async () => {
  answering(200, payment());
  const { html } = await get("/app/billing");

  assert.match(html, /usage_record:tenant-hermes\/2026-07\/sum/, "the metered line names its usage record");
  assert.match(html, /billable_savings:tenant-hermes\/2026-07/, "the gainshare line names its basis");
  // 🔴 A gainshare line carries its EVIDENCE. A gainshare line without it is a defect, because the
  // whole claim is that the platform bills only for savings it verified and a human merged.
  assert.match(html, /async_call_llm in agent\/auxiliary_client\.py/, "the gainshare line carries its merge evidence");
  // The amount is a HANDLE. There is no amount on the page because there is none in the platform.
  assert.match(html, /There is no amount on this page/, "the page states why no amount is shown");
});

test("gainshare shows what it did NOT bill, and how large it was", async () => {
  answering(
    200,
    payment({
      billing: {
        savings: {
          gainshare_consent: true,
          consent_available: true,
          baseline_sum: 1400,
          optimized_sum: 1260,
          verified_savings: 140,
          unit: "USD",
          none_verified: false,
          billed: [{ ref: "vd:small", baseline_sum: 500, optimized_sum: 400, savings: 100, merge_commit: "abc1234" }],
          excluded: [{ ref: "vd:big", reason: "verified but NOT merged — a saving that is not in effect bills nothing", would_have_been: 800 }],
        },
      },
    }),
  );
  const { html } = await get("/app/billing");

  assert.match(html, /vd:small/, "the billed saving is listed");
  // 🔴 The excluded one is the point. The platform's gainshare claim is only checkable if a customer
  // can see the savings it declined to bill — and here the largest one is the one that billed nothing.
  assert.match(html, /vd:big/, "the saving that was NOT billed is listed");
  assert.match(html, /800/, "and how large it would have been");
  assert.match(html, /verified but NOT merged/, "with the reason it was not billed");
  assert.match(html, /not billed/, "the row is labelled, not merely styled");
});

test("the payment-method status is rendered from the mirrored provider state", async () => {
  answering(200, payment());
  const { html } = await get("/app/billing");
  assert.match(html, /visa/, "the brand is shown");
  assert.match(html, /4242/, "the last four are shown");
  // Card data is four characters and a brand — never a PAN. The check is on the VISIBLE text rather
  // than on the whole document: a script nonce and a chunk hash are long digit runs too, and a fence
  // that flagged those would be switched off before it ever caught a real one.
  const text = html.replace(/<script[\s\S]*?<\/script>/g, " ").replace(/<[^>]+>/g, " ");
  assert.doesNotMatch(text, /\b\d{13,19}\b/, "no PAN-shaped number reaches the visible page");
  assert.match(html, /••••/, "the card is shown masked");
});

test("past due renders a NAMED REASON and a RESTORE PATH, not a bare error (FR12)", async () => {
  answering(
    200,
    payment({
      payment_method: {
        present: true,
        brand: "visa",
        last4: "4242",
        status: "payment_failed",
        reason: "The payment provider could not take payment on the card on file.",
        restore_path: "Add or update the payment method below.",
      },
      billing: {
        state: { invoice_status: "payment_failed", subscription_status: "past_due", payment_failed: true, past_due: true },
      },
    }),
  );
  const { html } = await get("/app/billing");

  assert.match(html, /A payment did not go through/, "the state is named");
  assert.match(html, /could not take payment on the card on file/, "the reason is the provider's, verbatim");
  assert.match(html, /To restore it/, "there is a restore path");
  assert.match(html, /update the payment method/i, "the restore path says what to do");
  // The provider's own words are mirrored, and the page says so rather than implying it computed them.
  assert.match(html, /mirrored here rather than recomputed/, "the page states that the status is mirrored");
  // 🔴 A dunning state does NOT claim the plan changed. It has not.
  assert.match(html, /Your plan has not been changed/, "the page says the plan is unchanged");
});

test("billing-unavailable is a DIFFERENT state from an empty invoice (FR12)", async () => {
  answering(200, payment({ unavailable: { detail: "the billing provider is unreachable", retryable: true } }));
  const { html } = await get("/app/billing");

  assert.match(html, /Billing is temporarily unavailable/, "the unavailable state is named");
  assert.match(html, /the billing provider is unreachable/, "the detail is rendered verbatim");
  assert.match(html, /provider outage, not a problem with your account/, "it distinguishes an outage from an account problem");
  // 🔴 The invoice is NOT rendered as empty beside it. Showing "no invoice lines" next to "we could not
  // reach billing" leaves the reader to work out which of the two to believe.
  assert.doesNotMatch(html, /No invoice lines for this period/, "an unreachable provider does not render as an empty invoice");
  // The product keeps working. That is the ADR-002 claim, said to the customer.
  assert.match(html, /Your product is unaffected/, "the page says the rest of the product is unaffected");
});

test("an empty period is a real state, not a failure (FR12)", async () => {
  answering(200, payment({ billing: { empty: true, sum: 0, sum_trend: [], meters: [], invoice: { lines: [], totals: [] } } }));
  const { html } = await get("/app/billing");
  assert.match(html, /Nothing has been recorded for this period yet/, "the empty period is named");
  assert.match(html, /real state, not a failure to load/, "it says so explicitly");
  assert.match(html, /No invoice lines for this period/, "an unbilled period is distinguished from an unreachable provider");
});

test("a provider that cannot collect payment offers no control that would fail", async () => {
  answering(200, payment({ collection_available: false, payment_method: { present: false } }));
  const { html } = await get("/app/billing");
  assert.doesNotMatch(html, /Add a payment method|Update payment method/, "no checkout control is offered");
  assert.match(html, /does not collect payment methods/, "the reason is stated instead");
});

test("no Stripe key or price reference reaches the rendered page", async () => {
  answering(200, payment());
  const { html } = await get("/app/billing");
  for (const shape of ["sk_live_", "sk_test_", "rk_live_", "whsec_"]) {
    assert.ok(!html.includes(shape), `the page must not contain ${shape}`);
  }
  // The console asks by plan NAME. A price reference on the page would mean the client had one, and the
  // step after having one is displaying it.
  assert.doesNotMatch(html, /price_ref_/, "no price reference reaches the client");
});

test("the plan controls name the DIRECTION the server computed, never one the client derived", async () => {
  answering(200, payment());
  const { html } = await get("/app/billing");
  assert.match(html, /Upgrade to Business/, "an upgrade is labelled as one");
  // React SSR puts a comment marker between `{plan.name}` and the text beside it, so the rendered
  // bytes are `Team<!-- --> — current plan`. Matching across it asserts the meaning, not the encoding.
  assert.match(html, /Team[\s\S]{0,24}current plan/, "the current plan is marked, not offered");
  assert.match(html, /Free[\s\S]{0,24}no subscription/, "a plan with no subscription price offers no purchase");
});

test("an upstream failure renders the failure, not an empty billing page (R5)", async () => {
  answering(503, { error: "the p21 payment surface is not mounted on this server" });
  const { html } = await get("/app/billing");
  assert.match(html, /not mounted/, "the upstream message is rendered verbatim");
  assert.doesNotMatch(html, /No invoice lines for this period/, "a not-mounted subsystem is not an empty invoice");
});

test("the BFF refuses a plan change that names no plan", async () => {
  const res = await fetch(`${console_.base}/api/console/billing/plan`, {
    method: "POST",
    headers: { cookie, "content-type": "application/json" },
    body: JSON.stringify({}),
  });
  assert.equal(res.status, 400);
  const body = await res.json();
  assert.match(body.error, /name of the plan/);
});

test("the checkout BFF builds its own return URLs and ignores a client-supplied one", async () => {
  // 🔴 A client-supplied return URL is an open redirect wearing a checkout costume. The route accepts a
  // PATH from a closed set and builds the origin from the request it actually received.
  let seen = null;
  platform.set((req, res) => {
    let raw = "";
    req.on("data", (c) => (raw += c));
    req.on("end", () => {
      seen = JSON.parse(raw || "{}");
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ url: "https://checkout.example.test/session" }));
    });
  });

  const res = await fetch(`${console_.base}/api/console/billing/checkout-session`, {
    method: "POST",
    headers: { cookie, "content-type": "application/json" },
    body: JSON.stringify({ plan_name: "Team", return_path: "https://attacker.example/steal" }),
  });
  assert.equal(res.status, 200);
  assert.ok(seen, "the platform was asked");
  assert.ok(!seen.success_url.includes("attacker.example"), "an attacker-supplied return URL is not forwarded");
  // The origin comes from the request the browser actually made, so it is the console's own host
  // whatever that is in this environment. What must be true is that it is NOT the supplied one and
  // that the path is from the closed set.
  assert.ok(!seen.cancel_url.includes("attacker.example"), "the cancel URL is not attacker-supplied either");
  assert.match(seen.success_url, /^https?:\/\/[^/]+\/app\/billing\?checkout=done$/);
  assert.match(seen.cancel_url, /^https?:\/\/[^/]+\/app\/billing\?checkout=canceled$/);
});

test("returning from checkout says the subscription is being confirmed, not that it succeeded", async () => {
  answering(200, payment());
  const { html } = await get("/app/billing?checkout=done");
  assert.match(html, /Your payment method was submitted/, "the return is acknowledged");
  // 🔴 It does NOT claim the subscription is active. The page shows what the provider has told the
  // platform, so it is briefly behind rather than briefly wrong.
  assert.match(html, /becomes active when the provider confirms it/, "the page does not claim more than it knows");
  assert.match(html, /briefly behind rather than briefly wrong/, "it says why");
});

test("a cancelled checkout says nothing was charged", async () => {
  answering(200, payment());
  const { html } = await get("/app/billing?checkout=canceled");
  assert.match(html, /Checkout was cancelled/);
  assert.match(html, /nothing was charged/i);
});
