// password-field.test.mjs — every masked field offers a reveal, and offers it the same way.
//
// # Why this is a rendered test and not a unit test
//
// The defect this guards is not "the component is wrong" — it is "one of six call sites did not get
// it", which a unit test of the component cannot see. So it boots the real console on the PASSWORD
// seam and reads the HTML the browser would, the same way the rest of the acceptance gate does.
//
// # What it asserts beyond presence
//
// `type="button"`. A <button> inside a <form> with no explicit type SUBMITS it, so a toggle that
// forgets it means "let me check what I typed" also means "sign in with a half-typed password" — and
// on the sign-in form that spends a failed attempt against the lockout counter.
//
// It also asserts the inputs kept their `name` and `autocomplete`. Adding a control must not quietly
// restyle or re-wire what was already there; `autocomplete` in particular is what makes a password
// manager offer to fill and to save, and losing it is invisible until somebody's manager stops working.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { startStubPlatform, startConsole } from "./support/harness.mjs";

let platform;
let console_;

before(async () => {
  platform = await startStubPlatform();
  // `/create-account` asks the PLATFORM whether this deployment offers sign-up — it is a posture the
  // platform reports on /readyz, not a console env var — and renders "this install does not offer
  // sign-up" when the answer is no. Without this the page still answers 200 and carries no form, which
  // is exactly the shape that would let this suite pass while asserting nothing.
  platform.set((req, res) => {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify(
      req.url?.startsWith("/readyz")
        ? { status: "ready", account_system: { self_serve_signup: true, mail_configured: true } }
        : {},
    ));
  });
  // The password seam, which is what puts these screens on the network at all. `platform` for the
  // session store because identity.ts REFUSES TO LOAD under `password` with the in-memory store.
  console_ = await startConsole(platform.base, {
    CONSOLE_TENANT_IDENTITY: "password",
    CONSOLE_SESSION_STORE: "platform",
    HEROS_SELF_SERVE_SIGNUP: "1",
  });
}, { timeout: 60_000 });

after(async () => {
  await console_?.close();
  await platform?.close();
});

async function html(path) {
  const res = await fetch(`${console_.base}${path}`, { redirect: "manual" });
  return { status: res.status, body: await res.text() };
}

/** Every <input> that is masked on arrival. */
function maskedInputs(body) {
  return [...body.matchAll(/<input\b[^>]*>/g)].map((m) => m[0]).filter((t) => /type="password"/.test(t));
}

/** Every reveal toggle, by its accessible name rather than by a class or a DOM position. */
function revealToggles(body) {
  return [...body.matchAll(/<button\b[^>]*>/g)].map((m) => m[0]).filter((t) => /aria-label="Show password"/.test(t));
}

for (const path of ["/signin", "/create-account", "/forgot-password"]) {
  test(`${path} renders`, async () => {
    const { status } = await html(path);
    assert.equal(status, 200, `${path} should be reachable on the password seam`);
  });
}

test("every masked field on sign-in has a reveal toggle", async () => {
  const { body } = await html("/signin");
  const masked = maskedInputs(body);
  assert.ok(masked.length >= 1, "sign-in should render at least one masked field");
  assert.equal(revealToggles(body).length, masked.length,
    "one reveal toggle per masked field — a form with fewer is the call site somebody missed");
});

test("every masked field on create-account has a reveal toggle", async () => {
  const { body } = await html("/create-account");
  const masked = maskedInputs(body);
  assert.equal(masked.length, 1, "create-account has exactly one password field");
  assert.equal(revealToggles(body).length, 1);
});

test("🔴 the toggle is type=button, so revealing cannot submit the form", async () => {
  for (const path of ["/signin", "/create-account"]) {
    const { body } = await html(path);
    const toggles = revealToggles(body);
    // 🔴 The count is asserted BEFORE the loop, and that is not belt-and-braces. A `for` over an empty
    // list passes every assertion inside it — so with no toggles rendered at all, this test reported
    // PASS while the two beside it went red. Observed, not theorised: it is what happened on the
    // red-check run for this file. An empty collection satisfies a universal claim vacuously, which is
    // how a fence ends up green about a surface that is not there.
    assert.ok(toggles.length > 0, `${path}: expected at least one reveal toggle to check`);
    for (const tag of toggles) {
      assert.match(tag, /type="button"/,
        `${path}: a toggle without type="button" submits the form it sits in`);
    }
  }
});

test("the toggle starts masked and says so to assistive technology", async () => {
  const { body } = await html("/create-account");
  const [toggle] = revealToggles(body);
  assert.match(toggle, /aria-pressed="false"/, "every arrival is masked — never revealed");
  assert.match(toggle, /aria-label="Show password"/, "the name states the action, not the state");
});

test("🔴 the sign-in page says nothing about a tenant on the password seam", async () => {
  const { body } = await html("/signin");
  // The reader presented an email and a password — which name a PERSON, not an organization. Copy that
  // says signing in "binds this browser to that tenant" refers to something they did not do, and uses
  // the operator's word for it. The assurance list was split per seam; the sentence above it was not,
  // which is how it stayed wrong. This asserts the whole rendered page, not one string, because the
  // next copy to drift will not be the one that just did.
  const prose = body.replace(/<[^>]+>/g, " ");
  assert.ok(!/\btenants?\b/i.test(prose),
    "the password seam's sign-in page must not use the word 'tenant' — customer copy says 'organization'");
  assert.match(prose, /organization/i, "the isolation guarantee is still stated, in the customer's word");
});

test("🔴 the page does not claim the password is not stored", async () => {
  const { body } = await html("/signin");
  const prose = body.replace(/<[^>]+>/g, " ");
  // Something derived from the password IS stored. A heading that says otherwise is contradicted by
  // the sentence under it, and a reader who catches one overstatement discounts the two true lines
  // beside it. What may be claimed is irreversibility, which is what argon2id actually buys.
  assert.ok(!/password is never stored/i.test(prose),
    "the password is stored as a hash — claim irreversibility, not absence");
  assert.match(prose, /cannot be turned back into your password/i,
    "the property that IS true has to still be stated");
});

test("adding the toggle took nothing away from the fields", async () => {
  const { body } = await html("/create-account");
  const [input] = maskedInputs(body);
  assert.match(input, /name="password"/, "the field kept its name, or the form posts nothing");
  // Case-insensitive: this renderer emits the JSX spelling (`autoComplete`) rather than the lowercase
  // HTML one, for every field on the page including the ones this change did not touch. HTML attribute
  // names are case-insensitive so the browser and the password manager both read it — matching the
  // exact case here would be asserting a rendering detail, not the behaviour.
  assert.match(input, /autocomplete="new-password"/i,
    "the field kept its autocomplete, or password managers stop offering to save");
  assert.match(input, /minlength="12"/i, "the field kept the 12-character rule the copy promises");
  assert.match(input, /required/, "the field kept its required flag");
  assert.match(input, /\bpr-11\b/,
    "the input reserves room for the toggle — without it a long passphrase runs underneath the button");
});
