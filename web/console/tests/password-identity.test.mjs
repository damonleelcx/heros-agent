// password-identity.test.mjs is P28's acceptance gate for the console half.
//
// # It drives a REAL console against a stub platform
//
// Every case below starts `next start` with `CONSOLE_TENANT_IDENTITY=password` and posts to the real
// `/api/session` route — the same handler a browser reaches. Nothing calls a seam function directly,
// because the properties being asserted are about the PATH: that the form works with no client
// JavaScript, that the cookie is set on the response, and that the password does not appear anywhere it
// should not. A test that called `verifyPassword()` would prove the function works and nothing about
// whether the page uses it.
//
// # The fence rule (inherited from sso-identity.test.mjs)
//
// Each security test names the defence it fences. Remove the kind-aware assurance list, the neutral
// forgot-password answer, or the "no password in a redirect" property, and the named test goes red.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { startStubPlatform, startConsole } from "./support/harness.mjs";

const ROOT = new URL("..", import.meta.url).pathname;
const read = (rel) => readFile(join(ROOT, rel), "utf8");
const code = async (rel) =>
  (await read(rel)).replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");

const TENANT = "cus_acme";
const EMAIL = "priya@example.com";
const PASSWORD = "a reasonable passphrase";

let platform;
let console_;

before(async () => {
  platform = await startStubPlatform();
  platform.set((req, res) => {
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      const json = (status, payload) => {
        res.writeHead(status, { "content-type": "application/json" });
        res.end(JSON.stringify(payload));
      };
      const parsed = body ? JSON.parse(body) : {};
      switch (req.url) {
        case "/api/v1/auth/password/signin":
          if (parsed.email === EMAIL && parsed.password === PASSWORD) {
            return json(200, {
              tenant_id: TENANT,
              organization_id: TENANT,
              organization_name: "Acme Inc",
              user_id: "usr_1",
              email: EMAIL,
              email_verified: false,
              role: "owner",
            });
          }
          return json(401, {
            error: "that email and password did not match",
            reason_code: "bad_credentials",
          });
        case "/api/v1/auth/password/forgot":
        case "/api/v1/auth/password/resend":
          // The platform's neutral answer, for every address.
          return json(200, { ok: true, message: "if that address has an account, we have sent it an email" });
        case "/api/v1/console-sessions":
          return json(201, { session_id: "sess_1", token: "upstream-token", expires_at: Date.now() + 3_600_000 });
        case "/api/v1/console-sessions/resolve":
          return json(200, { session_id: "sess_1", tenant_id: TENANT, user_id: "usr_1" });
        default:
          return json(200, {});
      }
    });
  });
  // 🔴 `platform`, not the default `memory`. The password seam refuses to serve on the in-memory store,
  // because a password reset runs on the platform and cannot revoke a session held in this process's map —
  // see identity.ts. The suite must run the combination a real deployment runs.
  console_ = await startConsole(platform.base, {
    CONSOLE_TENANT_IDENTITY: "password",
    CONSOLE_SESSION_STORE: "platform",
  });
});

after(async () => {
  await console_?.close();
  await platform?.close();
});

// ── the happy path, which is the whole point of the phase ────────────────────────────────────────

test("a person signs in with an email and a password and receives a session cookie", async () => {
  const form = new URLSearchParams({ email: EMAIL, password: PASSWORD, next: "/app" });
  const res = await fetch(`${console_.base}/api/session`, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: form,
    redirect: "manual",
  });
  assert.equal(res.status, 303, "sign-in did not redirect");
  assert.equal(res.headers.get("location"), "/app", "the destination is not relative — an absolute URL is refused by this console's own form-action policy");
  const cookie = res.headers.get("set-cookie") ?? "";
  assert.match(cookie, /HttpOnly/i, "the session cookie is readable by script");
  assert.ok(cookie.length > 0, "no session cookie was set on the response");
  // 🔴 The password appears in nothing the browser keeps or sends onward.
  assert.doesNotMatch(cookie, new RegExp(PASSWORD.replace(/ /g, "\\s")), "the password is in the cookie");
  assert.doesNotMatch(res.headers.get("location") ?? "", /password|passphrase/i, "the password is in the redirect");
});

test("a wrong password is refused, and the refusal carries nothing about the value", async () => {
  const form = new URLSearchParams({ email: EMAIL, password: "not the passphrase", next: "/app" });
  const res = await fetch(`${console_.base}/api/session`, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: form,
    redirect: "manual",
  });
  assert.equal(res.status, 303);
  const location = res.headers.get("location") ?? "";
  assert.equal(location, "/signin?reason=rejected", "a refused sign-in did not return to the form with a reason");
  assert.doesNotMatch(location, /passphrase|priya/i, "the refusal redirect carries the value or the address");
  assert.equal(res.headers.get("set-cookie"), null, "a refused sign-in set a cookie");
});

// 🔴 An unknown address and a wrong password are ONE answer. A console that distinguished them would
// rebuild the enumeration oracle the platform closes, one layer up, where nobody would look for it.
test("an unknown address and a wrong password are indistinguishable at the console", async () => {
  const post = async (email, password) => {
    const res = await fetch(`${console_.base}/api/session`, {
      method: "POST",
      headers: { "content-type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({ email, password, next: "/app" }),
      redirect: "manual",
    });
    return { status: res.status, location: res.headers.get("location") };
  };
  const unknown = await post("nobody@example.com", PASSWORD);
  const wrong = await post(EMAIL, "not the passphrase");
  assert.deepEqual(unknown, wrong, "the two refusals differ");
});

// ── the pages render, and say the right things ───────────────────────────────────────────────────

test("the sign-in page offers email and password, and no tenant-credential field", async () => {
  const html = await (await fetch(`${console_.base}/signin`)).text();
  assert.match(html, /name="email"/, "no email field");
  assert.match(html, /name="password"/, "no password field");
  assert.doesNotMatch(html, /name="assertion"/, "the tenant-credential field is still rendered on the password seam");
  assert.match(html, /forgot-password/, "no way to recover a forgotten password");
  assert.match(html, /\/signup|create-account/i, "no way to create an account");
  // 🔴 The credential path carries no client JavaScript: the form posts natively, exactly as the
  // credential form did. Sign-in must work before hydration — it is the one surface a user cannot pass.
  // Both attributes, matched independently: React emits `action` before `method`, and an assertion that
  // pinned the order would have failed for a rendering detail rather than for the property it names.
  const form = html.match(/<form[^>]*>/g)?.find((tag) => tag.includes("/api/session")) ?? "";
  assert.match(form, /action="\/api\/session"/, "the sign-in form does not post to the session route");
  assert.match(form, /method="post"/i, "the sign-in form is not a native post");
});

// 🔴 The assurance panel must not claim "No password database" beside a password field. This is the
// fence for the kind-aware list — revert `assurances` to the federated array and this goes red.
test("the sign-in page does not claim there is no password store", async () => {
  const html = await (await fetch(`${console_.base}/signin`)).text();
  assert.doesNotMatch(
    html,
    /no password (database|store)/i,
    "the sign-in page tells the reader we run no password store, three centimetres from a password field",
  );
  assert.match(html, /argon2id/i, "the page does not say how the password is actually stored");
});

test("forgot-password answers identically for every address", async () => {
  const page = await (await fetch(`${console_.base}/forgot-password`)).text();
  assert.match(page, /name="email"/, "no address field");
  const sent = await (await fetch(`${console_.base}/forgot-password?sent=1`)).text();
  assert.match(sent, /if that address has an account/i, "the confirmation is not the neutral one");
  assert.doesNotMatch(sent, /no such|not registered|unknown address/i, "the page discloses whether the address exists");
});

test("a reset page with no token refuses instead of rendering a form", async () => {
  const html = await (await fetch(`${console_.base}/reset-password`)).text();
  assert.doesNotMatch(html, /name="password"/, "a password field was offered with no link to spend");
  assert.match(html, /no longer usable|request a new/i, "the page does not say what to do");
});

// The consequence a reader most needs BEFORE they act, not after.
test("the reset form states that it ends every session and personal credential", async () => {
  const html = await (await fetch(`${console_.base}/reset-password?t=some-token`)).text();
  assert.match(html, /name="password"/, "no password field on a page reached with a link");
  assert.match(html, /ends every session/i, "the form does not disclose what completing it will do");
  assert.match(html, /machine credentials/i, "the form does not say what it will leave running");
});

// ── the other seams are untouched ────────────────────────────────────────────────────────────────

// 🔴 P28 may not change what any other kind renders. `ui-redesign-feature-and-visual-consistency` is
// explicit that a change may not drop a capability, and a new capability does not get to rebuild the
// page around itself either.
test("the credential seam still renders its own form", async () => {
  const other = await startConsole(platform.base, { CONSOLE_TENANT_IDENTITY: "configured" });
  try {
    const html = await (await fetch(`${other.base}/signin`)).text();
    assert.match(html, /name="assertion"/, "the configured seam lost its credential field");
    assert.doesNotMatch(html, /name="password"/, "the configured seam grew a password field");
    assert.match(html, /no password (database|store)/i, "the federated assurance list was lost");
  } finally {
    await other.close();
  }
});

// ── source-level properties ──────────────────────────────────────────────────────────────────────

// The seam is the only thing that knows about passwords. Everything above ADR-008's line stays put.
test("nothing above the seam learned about passwords", async () => {
  for (const file of ["src/lib/session.ts", "src/lib/cookies.ts", "src/middleware.ts", "src/lib/scope.ts"]) {
    const src = await code(file).catch(() => "");
    if (!src) continue;
    assert.doesNotMatch(src, /password/i, `${file} mentions a password — it is above the identity seam`);
  }
});

// 🔴 The console holds no verifier. If a hash or a comparison ever appears here, there are two places
// that can decide whether a password is correct — and the second is the one a revocation misses.
test("the console implements no password verification of its own", async () => {
  const src = await code("src/lib/idp/password.ts");
  for (const banned of ["argon2", "bcrypt", "scrypt", "pbkdf2", "createHash", "timingSafeEqual"]) {
    assert.doesNotMatch(
      src,
      new RegExp(banned, "i"),
      `idp/password.ts contains ${banned} — verification belongs to the platform, and a second verifier is a second thing a revocation has to reach`,
    );
  }
});

// The account-lifecycle copy is deliberately NOT on the identity path, because two of its strings name a
// paid plan and `sso-identity.test.mjs` fences that path against the word. This asserts the separation is
// real rather than filed: if the identity seam imports it, the fence's protection is gone.
test("the identity path does not import the account-lifecycle copy", async () => {
  for (const file of ["src/lib/identity.ts", "src/lib/idp/password.ts", "src/lib/idp/config.ts"]) {
    const src = await read(file);
    assert.doesNotMatch(src, /content\/passwordAccount/, `${file} imports the account copy onto the identity path`);
  }
});

// 🔴 The `password` seam will not SERVE on the in-memory session store.
//
// The assurance panel promises: "When one is revoked — by signing out, by a password reset, or by an owner
// removing you — the very next request is denied, with no grace period." On `CONSOLE_SESSION_STORE=memory`
// (the DEFAULT) the middle clause is false: the cookie is a Map in the console's process, a password reset
// runs on the platform, and the platform cannot reach that Map. A browser signed in on a lost device stays
// signed in until the cookie expires — which is the commonest reason somebody resets a password at all.
//
// Sign-out still works on memory, which is what made the gap dangerous: two thirds of the sentence keep
// working, so nothing looks broken.
//
// ⚠️ The guard is at MODULE SCOPE in identity.ts, and Next lazy-loads route modules — so the process starts
// and the readiness probe passes. What this asserts is the property that actually matters: on that
// combination nobody can sign in, and the sign-in page does not render a form promising otherwise. An
// earlier version of this test asserted "refuses to start" and failed against a correct guard, which is how
// the imprecision was found.
test("the password seam will not come up on the in-memory session store", async () => {
  let started;
  try {
    started = await startConsole(platform.base, {
      CONSOLE_TENANT_IDENTITY: "password",
      CONSOLE_SESSION_STORE: "memory",
    });
  } catch (err) {
    // The console never became ready: `/api/health` imports the identity seam to report the provider, so
    // the guard runs on the first readiness probe and the probe never returns 200. That is the strongest
    // form this can take without a boot hook — the deployment fails its health check rather than serving a
    // sign-in page whose promise it cannot keep.
    assert.match(
      String(err),
      /CONSOLE_SESSION_STORE=platform/,
      "the console failed to come up but its logs do not name the setting that fixes it, so an operator " +
        "gets a health-check failure with no next action",
    );
    return;
  }
  await started.close();
  assert.fail(
    "the console came up with CONSOLE_TENANT_IDENTITY=password and CONSOLE_SESSION_STORE=memory. On that " +
      "combination a password reset cannot revoke this console's own session cookie — it lives in this " +
      "process and the reset happens on the platform — while the sign-in page promises the very next " +
      "request is denied.",
  );
});
