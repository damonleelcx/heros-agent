// sso-identity.test.mjs is P22's acceptance gate — the adversarial half FIRST, then the happy path.
//
// # Why that order is written into the file rather than into a checklist
//
// A federation that signs the right person in is not evidence of anything: the interesting question is
// whether it refuses the wrong one, and every defense below is exercised by MUTATING a genuinely valid
// message rather than by hand-writing an invalid one. A test that constructs a broken assertion proves
// the verifier rejects garbage; a test that takes a real signed assertion and flips one character
// proves it rejects an ATTACK.
//
// # The fence rule (task 8.4)
//
// Each security test names the defense it fences. Remove `state`, `nonce`, PKCE, the freshness window,
// the one-time guard or the redirect allowlist, and the named test goes red. That is the property the
// requirement asks for, and it is checkable by deleting a line and running this file.
//
// # What makes the SAML half trustworthy
//
// The fixture IdP signs with the console's own canonicalizer (`tests/support/idp.mjs` explains why a
// second implementation would be a matched pair rather than a check). The independent leg is the very
// first test below: the canonicalizer is checked against the W3C exclusive-c14n specification's own
// worked example, which neither implementation authored. Without that, this file would only prove the
// two halves agree.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile, readdir } from "node:fs/promises";
import { join } from "node:path";
import { startStubPlatform, startConsole } from "./support/harness.mjs";
import { startStubOidc, startStubSaml, samlResponseXml, samlResponse } from "./support/idp.mjs";
import { parseXml, canonicalize, descendants } from "../src/lib/idp/xml.ts";

const ROOT = new URL("..", import.meta.url).pathname;
const read = (rel) => readFile(join(ROOT, rel), "utf8");
const code = async (rel) =>
  (await read(rel)).replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");

const TENANT = "cus_acme";
const CLIENT_ID = "console-client";

// ── NFR1 · the layer above the seam is unchanged ────────────────────────────────────────────────

/**
 * ABOVE_THE_SEAM pins the files ADR-008 Rule 3 says P22 may not touch.
 *
 * # Why a digest and not a description
 *
 * "The session layer is unchanged" is a claim a reviewer can believe while reading a diff that changes
 * it — the sentence stays true-sounding as the code drifts. A digest is the only form of that claim
 * that cannot drift, and the cost is exactly right: updating one of these numbers is a deliberate,
 * visible act that says "I changed a file P22 promised not to change", which is a review conversation
 * rather than a line in a commit.
 *
 * `cookies.ts` is NOT pinned as a whole, and the reason matters. P22 adds the sign-in flow cookie, and
 * it adds it HERE because `tests/security.test.mjs` requires credential cookie flags to live in exactly
 * one place. So the fence is on the SESSION cookie's own two declarations, which is what NFR1 is
 * actually about — the file grew, the session cookie did not move.
 *
 * 🔴 `middleware.ts` is now in the same position, for the same reason, and the reasoning is recorded
 * here rather than resolved with a new hash.
 *
 * That file does TWO jobs, and its own doc comment has always said so: it fails closed by route prefix,
 * and it sets a per-request Content-Security-Policy. ADR-008 Rule 3 is about the FIRST one — the
 * session store, the cookie, revocation, scope derivation and the fail-closed check are what P22 was
 * forbidden to touch, and a whole-file digest was a cheap and adequate way to say so while nothing else
 * in the file was moving.
 *
 * P24 changes the SECOND one. The header stops being a literal in this file and is constructed from
 * `web/design-system/third-party-policy.ts`, so both consoles read one table and a hard-coded origin in
 * either middleware fails the build. Nothing about the fail-closed half changes.
 *
 * Bumping the digest would have been the wrong correction twice over: it discards the fence for a
 * change it was never aimed at, and it makes the next such change a one-line hash edit — which is
 * precisely the "review conversation" this pin exists to force. So the fence is NARROWED to the half
 * ADR-008 is about, exactly as `cookies.ts` already is, and the CSP half is asserted by behaviour in
 * `tests/third-party-fence.test.mjs` (byte-identical output on every prefix) rather than by shape.
 */
const ABOVE_THE_SEAM = {
  "src/lib/session.ts": "221d2afc1cf462ea68fe8f1d7e2a7b547ea41fb31b59a85b125e6a2fa32fcb33",
  "src/lib/entitlements.ts": "71676c25bae578c82b65b1ed255c068e45d4ddaaf5f76b0864fcfa1d1592a608",
};

/**
 * SCOPE_DERIVATION is the half of `scope.ts` ADR-008 Rule 3 actually governs, pinned the way
 * `middleware.ts` already is — and narrowed for the same reason, a second time.
 *
 * The whole file used to be pinned. Renaming the platform's routes from phase names (`/api/p2/runs/…`)
 * to `/api/v1/…` changed every path literal in it and NOTHING else: the capture of `tenantId` from the
 * session, the absence of any tenant parameter, the closed set of builders. Verified rather than
 * asserted — every changed line in that rename was a path literal.
 *
 * Bumping the digest would have been the wrong correction for the reason recorded above for
 * `middleware.ts`: it discards the fence for a change it was never aimed at, and it teaches the next
 * person that a failing pin is a one-line hash edit. Paths will be renamed again; the derivation must
 * not change. So the pin follows the RULE, not the file.
 *
 * What is still covered: that `scoped()` takes a `Session` and captures `session.tenantId` into a
 * closure no caller can influence. What is deliberately not: the URL strings, which are asserted by
 * behaviour in `tests/routes.test.mjs` and by the platform's own route table.
 */
const SCOPE_DERIVATION_DIGEST = "606f31842ea7eb6093b727c6e38c8c1b13d5c93d53ac11d8298c60d472e87cdb";

function scopeDerivation(source) {
  const start = source.indexOf("export function scoped(session: Session) {");
  const end = source.indexOf("// \u2500\u2500 P2 \u00b7");
  if (start < 0 || end < 0 || end <= start) return null;
  return source.slice(start, end);
}

/**
 * MIDDLEWARE_FAIL_CLOSED is the half of `middleware.ts` ADR-008 Rule 3 actually governs, pinned by
 * digest the way the whole file used to be.
 *
 * It is extracted by source markers rather than by line number, so a comment added above it does not
 * read as a change to it — and the extraction itself is asserted to have found something, because a
 * digest of an empty string is a fence that passes forever.
 */
const MIDDLEWARE_FAIL_CLOSED_DIGEST = "c9c0647bb31e73ad76320417a7208c3f80b06cfc0be13d8ba749ce9bd182be9e";

function failClosedHalf(source) {
  const start = source.indexOf("const GATED =");
  const end = source.indexOf("const nonce =");
  if (start < 0 || end < 0 || end <= start) return null;
  return source.slice(start, end);
}

test("8.1/4.1 the layer above the seam is byte-for-byte unchanged (NFR1, ADR-008 Rule 3)", async () => {
  for (const [file, digest] of Object.entries(ABOVE_THE_SEAM)) {
    const actual = createHash("sha256").update(await read(file)).digest("hex");
    assert.equal(
      actual,
      digest,
      `${file} changed. P22 replaces the SEAM and adds /auth/* routes; the session store, cookie, ` +
        `revocation, scope derivation and fail-closed middleware are untouched (ADR-008 Rule 3). ` +
        `If this change is genuinely required, that is an ADR-008 conversation, not a hash update.`,
    );
  }

  const derivation = scopeDerivation(await read("src/lib/scope.ts"));
  assert.ok(derivation, "the derivation half of scope.ts could not be located — the fence found nothing to pin");
  assert.equal(
    createHash("sha256").update(derivation).digest("hex"),
    SCOPE_DERIVATION_DIGEST,
    "scope.ts's DERIVATION changed — the tenant capture, not a path literal. That is an ADR-008 " +
      "conversation, not a hash update. Renaming a route does not reach this region; if this failed " +
      "for a rename, the rename touched something it should not have.",
  );

  const half = failClosedHalf(await read("src/middleware.ts"));
  assert.ok(half, "the fail-closed half of middleware.ts could not be located — the fence found nothing to pin");
  assert.match(half, /GATED = \["\/app", "\/api\/console", "\/api\/stream"\]/, "the gated prefix set moved");
  assert.match(half, /SESSION_COOKIE/, "the fail-closed check no longer reads the session cookie");
  assert.equal(
    createHash("sha256").update(half).digest("hex"),
    MIDDLEWARE_FAIL_CLOSED_DIGEST,
    "the fail-closed half of middleware.ts changed. P24 may rebuild the Content-Security-Policy in that " +
      "file; it may not touch the prefix gate, the session-cookie check or the redirect/refuse split. " +
      "If this change is genuinely required, that is an ADR-008 conversation, not a hash update.",
  );
});

test("8.1/4.1 the session cookie's own declarations did not move", async () => {
  const cookies = await read("src/lib/cookies.ts");
  assert.match(cookies, /export const SESSION_COOKIE = "heros_console_session";/);
  const options = cookies.match(/export const SESSION_COOKIE_OPTIONS = \{[\s\S]*?\} as const;/);
  assert.ok(options, "the session cookie options block is missing");
  // Asserted by VALUE rather than by digest. A digest here would fence the formatting as well as the
  // flags, and the flags are what NFR1 is about — a reformat is not an ADR-008 conversation.
  assert.match(options[0], /httpOnly: true/);
  assert.match(options[0], /sameSite: "lax"/);
  assert.match(options[0], /secure: process\.env\.NODE_ENV === "production"/);
  assert.match(options[0], /path: "\/"/);
  assert.doesNotMatch(options[0], /maxAge/, "the session cookie's lifetime comes from sessionTtlSeconds()");
});

test("8.1/4.1 no mechanism word leaks above the seam", async () => {
  // The deeper half of NFR1: the files could be byte-identical today and still be coupled tomorrow.
  // What ADR-008 actually forbids is the layer above the seam KNOWING which mechanism proved identity.
  // `nonce` is deliberately NOT in this pattern. `middleware.ts` mints a per-request CSP nonce, which
  // is a content-security concept that has nothing to do with OIDC — and a fence that cannot tell the
  // two apart either cries wolf forever or gets loosened until it catches nothing.
  const MECHANISM = /\b(oidc|saml|pkce|id_token|jwks|entityID|assertion_consumer)\b/i;
  const offenders = [];
  for (const file of ["src/lib/session.ts", "src/lib/scope.ts", "src/middleware.ts", "src/lib/entitlements.ts"]) {
    if (MECHANISM.test(await code(file))) offenders.push(file);
  }
  assert.deepEqual(offenders, [], "a mechanism-specific term appears above the seam");
});

test("8.1/4.1 only the seam, the /auth routes and their support know the mechanism", async () => {
  // Stated as an allowlist so a NEW file that learns the mechanism has to be added here deliberately.
  const ALLOWED = [
    "src/lib/identity.ts",
    "src/lib/idp/",
    "src/app/auth/",
    "src/app/api/health/route.ts", // reports the provider KIND for the readiness surface
    "src/app/signin/page.tsx", // chooses which of the two sign-in paths to render
    "src/content/identity.ts", // the user-facing glossary, which names neither mechanism
  ];
  const MECHANISM = /\b(oidc|saml|pkce|jwks|id_token)\b/i;
  const offenders = [];
  for await (const file of walk("src")) {
    if (ALLOWED.some((prefix) => file.startsWith(prefix))) continue;
    if (MECHANISM.test(await code(file))) offenders.push(file);
  }
  assert.deepEqual(offenders, [], "a file outside the seam and its routes names an identity mechanism");
});

async function* walk(dir) {
  for (const entry of await readdir(join(ROOT, dir), { withFileTypes: true })) {
    const rel = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(rel);
    else if (rel.endsWith(".ts") || rel.endsWith(".tsx")) yield rel;
  }
}

// ── The independent leg: canonicalization against a specification the repo did not write ─────────

test("8.3 exclusive canonicalization matches the W3C specification's own worked example", () => {
  // This is what makes the SAML half of this file meaningful. The fixture IdP signs with the same
  // canonicalizer the verifier uses, so a round trip alone would pass with two identically-wrong
  // implementations. This vector is from the exc-c14n specification and neither side authored it.
  const doc = parseXml(
    `<n0:local xmlns:n0="foo:bar" xmlns:n3="ftp://example.org">\n` +
      `  <n1:elem2 xmlns:n1="http://example.net" xml:lang="en">\n` +
      `     <n3:stuff xmlns:n3="ftp://example.org"/>\n` +
      `  </n1:elem2>\n` +
      `</n0:local>`,
  );
  const elem2 = [...descendants(doc)].find((el) => el.local === "elem2");
  assert.equal(
    canonicalize(elem2),
    `<n1:elem2 xmlns:n1="http://example.net" xml:lang="en">\n` +
      `     <n3:stuff xmlns:n3="ftp://example.org"></n3:stuff>\n` +
      `  </n1:elem2>`,
  );
});

test("8.3 a DOCTYPE in a signed document is refused, not ignored", () => {
  // XXE and entity expansion do not need a clever payload if the parser will not read a DOCTYPE at all.
  assert.throws(() => parseXml(`<!DOCTYPE r [<!ENTITY x "y">]><r/>`), /DOCTYPE/);
});

// ── NFR2 · the assertion is never persisted ─────────────────────────────────────────────────────

test("8.3 nothing on the identity path can log or store an assertion (NFR2)", async () => {
  const telemetry = await code("src/lib/telemetry.ts");
  const identityLogger = telemetry.match(/export function logIdentity\(event: \{[\s\S]*?\}\): void \{[\s\S]*?\n\}/);
  assert.ok(identityLogger, "logIdentity is missing");
  for (const forbidden of ["assertion", "token", "code", "email", "state", "nonce", "verifier"]) {
    assert.doesNotMatch(
      identityLogger[0],
      new RegExp(`\\b${forbidden}\\b`, "i"),
      `logIdentity has a "${forbidden}" field — the rule survives a 2am debugging session only because ` +
        `there is nowhere to put one`,
    );
  }
  // The session record is still five fields. An assertion cannot be persisted in a record with no
  // field for it, which is a stronger guarantee than a grep over the code that writes it.
  const session = await code("src/lib/session.ts");
  assert.match(session, /export type Session = \{[\s\S]*?\n\};/);
  const shape = session.match(/export type Session = \{[\s\S]*?\n\};/)[0];
  // `subject` is deliberately absent from this list: the session's `visited?: Subject[]` is a
  // console-local record of which RUNS and WORKFLOWS this tab opened, and shares only a word with an
  // identity subject. Fencing on the word rather than the meaning would fail on the product's own
  // vocabulary, which is how a fence gets deleted.
  for (const forbidden of ["assertion", "idToken", "id_token", "claims", "email"]) {
    assert.doesNotMatch(shape, new RegExp(forbidden, "i"), `the session record has a ${forbidden} field`);
  }
});

test("8.3 the seam never returns the assertion to its caller", async () => {
  const identity = await code("src/lib/identity.ts");
  // The only successful shape the seam can return is a tenant principal. Asserted structurally rather
  // than by reading: a `VerifyOutcome` with no field for an assertion cannot leak one upstream.
  assert.match(identity, /\{ ok: true; principal: TenantPrincipal \}/);
  assert.match(identity, /export type TenantPrincipal = \{ tenantId: string \};/);
});

// ── Runtime config, not build artefact (task 3.4) ───────────────────────────────────────────────

test("3.4 no identity value is a compile-time constant", async () => {
  const config = await code("src/lib/idp/config.ts");
  // Every field is read from the running process's environment. A literal issuer or client id here
  // would mean one image per customer, which is the failure ADR-004 fail-static binding exists to stop.
  assert.match(config, /process\.env\.CONSOLE_IDP_ISSUER|requireEnv\("CONSOLE_IDP_ISSUER"/);
  assert.doesNotMatch(config, /https:\/\/[a-z0-9.-]+\.(okta|auth0|microsoftonline)\.com/i);
  const map = await code("src/lib/idp/federation.ts");
  assert.doesNotMatch(map, /case "cus_|tenant === "/, "a per-tenant branch is compiled into the mapping");
});

// ── 9.1/9.3 · the messages, and the honesty gates ───────────────────────────────────────────────

test("9.1 the sign-in copy leaks no internal mechanism", async () => {
  const copy = await read("src/content/identity.ts");
  // Only the STRINGS a user reads — the module's own comments explain the mechanism on purpose, and a
  // fence that could not tell the two apart would either cry wolf or get loosened until it caught
  // nothing.
  const strings = [...copy.matchAll(/(?:title|body|alert):\s*"([^"]*)"/g)].map((m) => m[1]);
  assert.ok(strings.length >= 8, `found ${strings.length} user-facing strings — the extraction is broken, not the copy`);
  const FORBIDDEN = [
    /\boidc\b/i, /\bsaml\b/i, /\bpkce\b/i, /\bjwks\b/i, /\bjwt\b/i, /\bnonce\b/i,
    /console_idp/i, /admin-idp/i, /CONSOLE_[A-Z_]+/, /HEROS_[A-Z_]+/,
    /\bissuer\b/i, /\ballowlist\b/i, /\bassertion\b/i, /\bentityID\b/i,
  ];
  for (const s of strings) {
    for (const pattern of FORBIDDEN) {
      assert.doesNotMatch(s, pattern, `a user-facing message names an internal mechanism: ${JSON.stringify(s)}`);
    }
  }
});

test("9.1 the five sign-in states are distinct, and every routed reason maps to one", async () => {
  const copy = await read("src/content/identity.ts");
  // Distinct because the reader's NEXT ACTION differs in each. Two states with the same title are one
  // state with two names, which is how "your session ended" and "you were signed out" become a
  // vocabulary nobody documented.
  const titles = [...copy.matchAll(/title:\s*"([^"]*)"/g)].map((m) => m[1]);
  assert.equal(new Set(titles).size, titles.length, `two sign-in states share a title: ${titles.join(" | ")}`);

  // Every reason a route can emit must land somewhere. A reason with no entry renders the generic
  // prompt, which is a silent downgrade of the one thing this page knows that the reader does not.
  const aliases = new Set([...copy.matchAll(/^\s{2}(\w+):\s*"/gm)].map((m) => m[1]));
  const routes = [
    await code("src/app/auth/callback/route.ts"),
    await code("src/app/auth/saml/acs/route.ts"),
    await code("src/app/auth/login/route.ts"),
    await code("src/app/api/session/route.ts"),
    await code("src/app/api/session/end/route.ts"),
    await code("src/middleware.ts"),
    await code("src/lib/session.ts"),
  ].join("\n");
  for (const [, reason] of routes.matchAll(/reason=(\w+)/g)) {
    assert.ok(aliases.has(reason), `route emits reason=${reason}, which content/identity.ts does not map`);
  }
});

test("9.3 there is no price and no plan gate anywhere on the identity path", async () => {
  // # Why this is a build gate and not a review note
  //
  // Making SSO a paid tier is one entitlement check away, and it is commercially tempting. That check
  // would be a SECURITY-CRITICAL CODE PATH WHOSE BEHAVIOUR DEPENDS ON A BILLING RECORD — and the first
  // billing outage would become an authentication outage. Identity proves who you are; what you are
  // entitled to is a separate question asked later, by code that is allowed to be wrong without
  // locking anybody out.
  const PATH = ["src/lib/identity.ts", "src/content/identity.ts"];
  for await (const file of walk("src")) {
    if (file.startsWith(join("src", "lib", "idp")) || file.startsWith(join("src", "app", "auth"))) PATH.push(file);
  }
  for (const file of PATH) {
    const src = await code(file);
    assert.doesNotMatch(src, /entitlement/i, `${file} reads an entitlement on the identity path`);
    assert.doesNotMatch(src, /\bplan\b/i, `${file} reads a plan on the identity path`);
    assert.doesNotMatch(src, /\bbilling\b/i, `${file} touches billing on the identity path`);
    assert.doesNotMatch(src, /\$\s?\d[\d,]*\.\d/, `${file} contains a price literal`);
  }
  // And the sign-in surface itself says nothing about a plan.
  const signin = await read("src/app/signin/page.tsx");
  assert.doesNotMatch(signin, /upgrade|paid plan|pricing/i, "the sign-in surface implies SSO is paywalled");
});

// ── Live · OIDC ─────────────────────────────────────────────────────────────────────────────────

let platform;
let idp;
let consoleProcess;

before(async () => {
  platform = await startStubPlatform();
  idp = await startStubOidc({ clientId: CLIENT_ID });
  consoleProcess = await startConsole(platform.base, (base) => ({
    CONSOLE_TENANT_IDENTITY: "oidc",
    CONSOLE_IDP_ISSUER: idp.base,
    CONSOLE_IDP_CLIENT_ID: CLIENT_ID,
    CONSOLE_IDP_REDIRECT_ALLOWLIST: JSON.stringify([`${base}/auth/callback`]),
    CONSOLE_IDP_TENANT_MAP: JSON.stringify({
      strategy: "jit",
      issuers: {
        [idp.base]: { tenant: TENANT, verified_domains: ["acme.test"], jit_allow: ["contractor.test"] },
        // A SECOND registration, so "cross-tenant resolution" is a thing this deployment could
        // express if the code let it. A single-tenant map would make the NFR9 test vacuous.
        "https://other-idp.test": { tenant: "cus_other", verified_domains: ["other.test"] },
      },
    }),
    HEROS_SECRETS_SOURCE: "env",
    CONSOLE_IDP_CLIENT_SECRET: "harness-client-secret-do-not-ship",
  }));
});

after(async () => {
  await consoleProcess?.close();
  await idp?.close();
  await platform?.close();
});

/** begin walks `/auth/login` and returns the flow cookie plus the authorization URL. */
async function begin(next = "/app") {
  const res = await fetch(`${consoleProcess.base}/auth/login?next=${encodeURIComponent(next)}`, { redirect: "manual" });
  const cookie = (res.headers.getSetCookie?.() ?? [])
    .map((c) => c.split(";")[0])
    .find((c) => c.startsWith("heros_console_signin="));
  return { status: res.status, authorize: res.headers.get("location"), cookie };
}

/** completeOidc runs the IdP leg and returns the console's callback response. */
async function completeOidc({ authorize, cookie }) {
  const at = await fetch(authorize, { redirect: "manual" });
  const back = at.headers.get("location");
  return fetch(back, { headers: cookie ? { cookie } : {}, redirect: "manual" });
}

function sessionCookieOf(res) {
  return (res.headers.getSetCookie?.() ?? [])
    .map((c) => c.split(";")[0])
    .find((c) => c.startsWith("heros_console_session="));
}

test("OIDC · a valid Authorization Code + PKCE sign-in resolves exactly one tenant", async () => {
  const flow = await begin();
  assert.equal(flow.status, 303);
  assert.ok(flow.cookie, "no browser-bound flow cookie was set");
  assert.match(flow.authorize, /response_type=code/);
  assert.match(flow.authorize, /code_challenge_method=S256/);
  assert.ok(!/response_type=id_token|response_type=token/.test(flow.authorize), "the implicit flow was requested");

  const res = await completeOidc(flow);
  assert.equal(res.status, 303);
  const session = sessionCookieOf(res);
  assert.ok(session, `no session cookie issued (status ${res.status}, location ${res.headers.get("location")})`);
  assert.equal(res.headers.get("location"), "/app");

  // The tenant is the one the MAPPING resolved, and it is visible only server-side.
  const page = await fetch(`${consoleProcess.base}/app`, { headers: { cookie: session } });
  assert.equal(page.status, 200);
  assert.match(await page.text(), new RegExp(TENANT));
});

test("8.4 OIDC · a replayed callback is refused — the `state` is single-USE, not single-success", async () => {
  const flow = await begin();
  const first = await completeOidc(flow);
  assert.ok(sessionCookieOf(first), "the first sign-in should have succeeded");

  // The same flow cookie again. The record was consumed at the first callback.
  const replay = await fetch(`${consoleProcess.base}/auth/callback?code=whatever&state=whatever`, {
    headers: { cookie: flow.cookie },
    redirect: "manual",
  });
  assert.equal(sessionCookieOf(replay), undefined);
  assert.match(replay.headers.get("location"), /\/signin\?reason=credential/);
});

test("8.4 OIDC · a callback WITHOUT the browser's flow cookie is refused (CSRF)", async () => {
  const flow = await begin();
  const at = await fetch(flow.authorize, { redirect: "manual" });
  // The URL half alone — exactly what an attacker who read a log or a `Referer` would hold.
  const res = await fetch(at.headers.get("location"), { redirect: "manual" });
  assert.equal(sessionCookieOf(res), undefined, "a callback with no browser binding issued a session");
  assert.match(res.headers.get("location"), /\/signin\?reason=credential/);
});

test("8.4 OIDC · a `state` from a DIFFERENT browser's flow is refused", async () => {
  const mine = await begin();
  const theirs = await begin();
  const at = await fetch(theirs.authorize, { redirect: "manual" });
  const back = new URL(at.headers.get("location"));
  // Their code, my cookie: the two halves must refer to one record.
  const res = await fetch(back.toString(), { headers: { cookie: mine.cookie }, redirect: "manual" });
  assert.equal(sessionCookieOf(res), undefined);
});

test("8.4 OIDC · a reused authorization code is refused", async () => {
  const flow = await begin();
  const at = await fetch(flow.authorize, { redirect: "manual" });
  const back = at.headers.get("location");
  const first = await fetch(back, { headers: { cookie: flow.cookie }, redirect: "manual" });
  assert.ok(sessionCookieOf(first));

  // A second flow, redeeming the FIRST flow's code. The IdP burns a code on redemption, which is what
  // makes this a test of a real behaviour rather than of our own bookkeeping.
  const second = await begin();
  const replayed = new URL(back);
  replayed.searchParams.set("state", second.authorize.match(/state=([^&]+)/)[1]);
  const res = await fetch(replayed.toString(), { headers: { cookie: second.cookie }, redirect: "manual" });
  assert.equal(sessionCookieOf(res), undefined);
});

test("8.4 OIDC · an ID token bound to another flow's nonce is refused", async () => {
  idp.control.nonce = "a-nonce-from-somewhere-else";
  try {
    const flow = await begin();
    const res = await completeOidc(flow);
    assert.equal(sessionCookieOf(res), undefined, "an ID token with a foreign nonce issued a session");
  } finally {
    idp.control.nonce = null;
  }
});

test("8.4 OIDC · a stale assertion is refused (freshness window)", async () => {
  idp.control.iatOffset = -3600;
  try {
    const flow = await begin();
    const res = await completeOidc(flow);
    assert.equal(sessionCookieOf(res), undefined, "an hour-old assertion issued a session");
  } finally {
    idp.control.iatOffset = 0;
  }
});

test("8.4 OIDC · an `alg: none` ID token is refused", async () => {
  idp.control.algNone = true;
  try {
    const flow = await begin();
    const res = await completeOidc(flow);
    assert.equal(sessionCookieOf(res), undefined, "an unsigned ID token issued a session");
  } finally {
    idp.control.algNone = false;
  }
});

test("8.4 OIDC · an assertion seen twice is refused (one-time guard)", async () => {
  idp.control.fixedJti = "one-and-only";
  try {
    const first = await completeOidc(await begin());
    assert.ok(sessionCookieOf(first), "the first use of this assertion id should have succeeded");
    const second = await completeOidc(await begin());
    assert.equal(sessionCookieOf(second), undefined, "the same assertion id was accepted twice");
  } finally {
    idp.control.fixedJti = null;
  }
});

test("8.3 NFR3 · a forged tenant in EVERY client-controlled position never widens scope", async () => {
  const res = await completeOidc(await begin());
  const session = sessionCookieOf(res);
  assert.ok(session);

  // What reaches the platform is what matters. The console's own claim is narrow and precise — the
  // tenant on the upstream header is the SESSION's — so the assertion is on the header the stub
  // platform actually received, not on a status code the console happened to return.
  platform.set((req, resp) => {
    resp.writeHead(200, { "content-type": "application/json" });
    resp.end(JSON.stringify({ names: [] }));
  });
  const before = platform.requests.length;

  // Path, query, body, header — and `state`, which is the position unique to a redirect flow and the
  // one a reviewer asks about last.
  await fetch(`${consoleProcess.base}/api/console/studio/names?tenant=cus_victim&tenant_id=cus_victim`, {
    headers: {
      cookie: session,
      "x-console-tenant": "cus_victim",
      "x-tenant-id": "cus_victim",
      state: "cus_victim",
    },
  });

  const forwarded = platform.requests.slice(before);
  assert.ok(forwarded.length > 0, "the request did not reach the platform at all");
  for (const r of forwarded) {
    assert.equal(
      r.headers["x-console-tenant"],
      TENANT,
      "a client-supplied tenant reached the platform — the console's scope is not session-derived",
    );
  }
});

test("8.2 NFR9 · a self-asserted domain cannot claim another tenant's registration", async () => {
  // The classic cross-tenant hole: IdP A asserts an address in tenant B's verified domain. The issuer
  // is registered — so this is not merely an unknown-issuer refusal — but the domain belongs to a
  // DIFFERENT registration, and there is no code path that searches other registrations.
  idp.control.claims = { email: "attacker@other.test", email_verified: true };
  try {
    const res = await completeOidc(await begin());
    assert.equal(sessionCookieOf(res), undefined, "an identity resolved across a tenant boundary");
    assert.match(res.headers.get("location"), /reason=not_provisioned/);
  } finally {
    idp.control.claims = { email: "person@acme.test", email_verified: true };
  }
});

test("8.2 NFR9 · an unmapped identity is refused, never provisioned", async () => {
  idp.control.claims = { email: "nobody@unmapped.test", email_verified: true };
  try {
    const res = await completeOidc(await begin());
    assert.equal(sessionCookieOf(res), undefined);
    assert.match(res.headers.get("location"), /reason=not_provisioned/);
    // The refusal is a SECURITY EVENT, not a signup. The event is what an operator alerts on.
    assert.ok(
      consoleProcess.logs.join("").includes(`"outcome":"unmapped_identity"`),
      "the refusal was not recorded as a security event",
    );
  } finally {
    idp.control.claims = { email: "person@acme.test", email_verified: true };
  }
});

test("8.2 NFR9 · an UNVERIFIED email never maps a tenant", async () => {
  idp.control.claims = { email: "person@acme.test", email_verified: false };
  try {
    const res = await completeOidc(await begin());
    assert.equal(sessionCookieOf(res), undefined, "a self-asserted, unverified email mapped a tenant");
  } finally {
    idp.control.claims = { email: "person@acme.test", email_verified: true };
  }
});

test("8.2 JIT provisions only under an explicit allow rule", async () => {
  idp.control.claims = { email: "temp@contractor.test", email_verified: true };
  try {
    const res = await completeOidc(await begin());
    assert.ok(sessionCookieOf(res), "an allow-listed JIT domain was refused");
    assert.ok(
      consoleProcess.logs.join("").includes(`"outcome":"jit_provisioned"`),
      "a JIT provisioning was not recorded",
    );
  } finally {
    idp.control.claims = { email: "person@acme.test", email_verified: true };
  }
});

test("8.4/5.2 an off-allowlist redirect target is refused", async () => {
  // `next` is the only client-controlled destination in the flow, and it is normalised to a
  // same-origin path before it is stored. An absolute off-origin target cannot survive it.
  const flow = await begin("https://evil.test/steal");
  const res = await completeOidc(flow);
  assert.equal(res.headers.get("location"), "/app", "an off-origin destination survived the flow");
});

test("4.2 revocation is immediate — the NEXT request is denied, with no grace period", async () => {
  const res = await completeOidc(await begin());
  const session = sessionCookieOf(res);
  assert.ok(session);
  assert.equal((await fetch(`${consoleProcess.base}/app`, { headers: { cookie: session } })).status, 200);

  await fetch(`${consoleProcess.base}/api/session`, { method: "DELETE", headers: { cookie: session } });

  // The very next request. Not "eventually", not "after the cache expires" — the store is read on
  // every request, so there is no window in which a revoked session still works.
  const after = await fetch(`${consoleProcess.base}/app`, { headers: { cookie: session }, redirect: "manual" });
  assert.equal(after.status, 307, "a revoked session was still served");
  assert.match(after.headers.get("location"), /\/signin/);
});

test("4.2 a revoked session cannot be resurrected by signing in again with the same browser", async () => {
  const first = await completeOidc(await begin());
  const revoked = sessionCookieOf(first);
  await fetch(`${consoleProcess.base}/api/session`, { method: "DELETE", headers: { cookie: revoked } });

  // A fresh sign-in issues a NEW session. The old token stays dead — a re-authentication is not a
  // refresh that extends what was revoked, which is the distinction Decision 7 rests on.
  const second = await completeOidc(await begin());
  const fresh = sessionCookieOf(second);
  assert.ok(fresh);
  assert.notEqual(fresh, revoked);
  const replay = await fetch(`${consoleProcess.base}/app`, { headers: { cookie: revoked }, redirect: "manual" });
  assert.equal(replay.status, 307, "the revoked token came back to life");
});

test("4.2 the browser's session token is opaque — not a self-vouching token", async () => {
  const res = await completeOidc(await begin());
  const value = sessionCookieOf(res).split("=")[1];
  // A JWT vouches for its own expiry and cannot be revoked without a denylist that reintroduces the
  // very server-side store it was meant to avoid. This one is 32 bytes of CSPRNG and decodes to
  // nothing — there is no claim inside it for anybody to trust.
  assert.ok(!value.includes("."), "the session token has JWT structure");
  assert.equal(Buffer.from(value, "base64url").length, 32);

  /*
   * 🔴 This assertion was FLAKY at 11.8%, and the arithmetic is worth keeping.
   *
   * It used to decode the 32 random bytes as UTF-8 and assert the result contained no `{`. But `{` is
   * one byte value out of 256, so over 32 CSPRNG bytes it appears with probability
   * 1 − (255/256)^32 = 11.8% — measured at 11.8% over 200,000 tokens. Roughly one run in eight failed
   * for a token that was perfectly opaque.
   *
   * A test that fails one run in eight is worse than no test: it trains everybody to re-run, and the
   * next real failure is re-run away too.
   *
   * The INTENT is right and is kept: a session token must carry no claim anybody could trust. So the
   * check is now what the intent actually means — the token does not parse as a structured document —
   * rather than a substring that random bytes hit by chance.
   */
  const decoded = Buffer.from(value, "base64url").toString("utf8");
  let parsed = false;
  try {
    JSON.parse(decoded);
    parsed = true;
  } catch {
    parsed = false;
  }
  assert.ok(!parsed, "the session token parses as JSON — it carries claims a reader could trust");
});

test("4.2 no code path extends a live session's expiry", async () => {
  // "A refresh re-verifies rather than silently extends" is enforced by ABSENCE: there is no writer
  // of `expiresAt` outside the one place a session is minted. A grep is the honest fence for an
  // absence — a behavioural test cannot prove a path does not exist.
  const session = await code("src/lib/session.ts");
  // Exactly one place computes a lifetime — the mint. The type's own `expiresAt: number` field is not
  // a write, which is why this looks for the COMPUTATION rather than for the identifier.
  const computed = session.match(/expiresAt:\s*(?!number\b)\w/g) ?? [];
  assert.equal(computed.length, 1, "a session's lifetime is computed in more than one place");
  assert.match(session, /expiresAt: now \+ SESSION_TTL_SECONDS \* 1000/);
  for await (const file of walk("src")) {
    if (file === join("src", "lib", "session.ts")) continue;
    assert.doesNotMatch(await code(file), /\.expiresAt\s*=(?![=>])/, `${file} assigns to a session's expiry`);
  }
});

test("8.5 NFR5 · an unreachable IdP issues NO session and does not fall back", async () => {
  // # Why this test brings its OWN console and IdP
  //
  // It has to kill the identity provider, and a killed `httptest`-style server does not come back on
  // the same port. Sharing the suite's IdP therefore left every later test federating against a port
  // nobody was listening on — which is not a failure, it is a test that passes for the wrong reason
  // until somebody adds one that does not. Costing one extra console boot to make this file
  // order-independent is the right trade; a suite whose result depends on declaration order is a suite
  // whose green is a coincidence.
  const platform2 = await startStubPlatform();
  const idp2 = await startStubOidc({ clientId: CLIENT_ID });
  let node;
  try {
    node = await startConsole(platform2.base, (base) => ({
      CONSOLE_TENANT_IDENTITY: "oidc",
      CONSOLE_IDP_ISSUER: idp2.base,
      CONSOLE_IDP_CLIENT_ID: CLIENT_ID,
      CONSOLE_IDP_REDIRECT_ALLOWLIST: JSON.stringify([`${base}/auth/callback`]),
      CONSOLE_IDP_TENANT_MAP: JSON.stringify({
        strategy: "domain",
        issuers: { [idp2.base]: { tenant: TENANT, verified_domains: ["acme.test"] } },
      }),
      HEROS_SECRETS_SOURCE: "env",
      CONSOLE_IDP_CLIENT_SECRET: "harness-client-secret-do-not-ship",
    }));
    // Warm the metadata cache, so the refusal below is about REACHABILITY rather than about a cold
    // start. This is the exact shape of the defect this test found: a five-minute-old cache made a
    // dead IdP look serviceable at the login hop.
    const warm = await fetch(`${node.base}/auth/login?next=%2Fapp`, { redirect: "manual" });
    assert.match(warm.headers.get("location"), /response_type=code/);

    await idp2.close();

    const res = await fetch(`${node.base}/auth/login?next=%2Fapp`, { redirect: "manual" });
    assert.equal(res.status, 303);
    assert.match(
      res.headers.get("location"),
      /\/signin\?reason=idp_unreachable/,
      "the flow began against an IdP that cannot answer — the user lands on a dead host",
    );
    const cookie = (res.headers.getSetCookie?.() ?? []).find((c) => c.startsWith("heros_console_signin="));
    assert.equal(cookie, undefined, "a sign-in flow was begun against an unreachable IdP");

    // And nothing weaker takes over: the credential form is not a fallback in a federated deployment.
    const html = await (await fetch(`${node.base}/signin?reason=idp_unreachable`)).text();
    assert.ok(!/name="assertion"/.test(html), "a federated console offered a credential form as a fallback");
  } finally {
    await node?.close();
    await idp2.close();
    await platform2.close();
  }
});

// ── Live · SAML ─────────────────────────────────────────────────────────────────────────────────

test("SAML · the enterprise alternative resolves a tenant through the SAME seam", async (t) => {
  const ENTITY = "urn:heros:test:saml-idp";
  const platform2 = await startStubPlatform();
  let saml;
  let node;
  try {
    saml = await startStubSaml({ entityId: ENTITY, spEntityId: "urn:heros:test:sp" });
    node = await startConsole(platform2.base, (base) => ({
      CONSOLE_TENANT_IDENTITY: "saml",
      CONSOLE_SAML_IDP_ENTITY_ID: ENTITY,
      CONSOLE_SAML_SP_ENTITY_ID: "urn:heros:test:sp",
      CONSOLE_SAML_ACS_ALLOWLIST: JSON.stringify([`${base}/auth/saml/acs`]),
      CONSOLE_SAML_IDP_METADATA_URL: saml.metadataUrl,
      CONSOLE_IDP_TENANT_MAP: JSON.stringify({
        strategy: "domain",
        issuers: { [ENTITY]: { tenant: TENANT, verified_domains: ["acme.test"] } },
      }),
      HEROS_SECRETS_SOURCE: "file",
      HEROS_SECRETS_DIR: saml.secretsDir,
    }));

    /** samlBegin/samlComplete walk the SP-initiated flow the way a browser does. */
    const samlBegin = async () => {
      const res = await fetch(`${node.base}/auth/login?next=%2Fapp`, { redirect: "manual" });
      const cookie = (res.headers.getSetCookie?.() ?? [])
        .map((c) => c.split(";")[0])
        .find((c) => c.startsWith("heros_console_signin="));
      return { sso: res.headers.get("location"), cookie };
    };
    const samlComplete = async ({ sso, cookie }) => {
      const page = await (await fetch(sso)).text();
      const body = new URLSearchParams({
        SAMLResponse: page.match(/name="SAMLResponse" value="([^"]+)"/)[1],
        RelayState: page.match(/name="RelayState" value="([^"]*)"/)[1],
      });
      return fetch(`${node.base}/auth/saml/acs`, {
        method: "POST",
        headers: { "content-type": "application/x-www-form-urlencoded", ...(cookie ? { cookie } : {}) },
        body,
        redirect: "manual",
      });
    };

    await t.test("a signed assertion at an allowlisted ACS issues a session", async () => {
      const flow = await samlBegin();
      // The SP-initiated request is SIGNED, with the key from the `Secrets` seam.
      assert.match(flow.sso, /SAMLRequest=.*RelayState=.*SigAlg=.*Signature=/);
      const res = await samlComplete(flow);
      const session = sessionCookieOf(res);
      assert.ok(session, `no session issued (location ${res.headers.get("location")})`);
      const page = await fetch(`${node.base}/app`, { headers: { cookie: session } });
      assert.match(await page.text(), new RegExp(TENANT));
    });

    await t.test("8.4 a tampered assertion is refused", async () => {
      saml.control.mutate = (xml) => xml.replace("person@acme.test", "attacker@acme.test");
      try {
        const res = await samlComplete(await samlBegin());
        assert.equal(sessionCookieOf(res), undefined, "a post-signature edit was accepted");
      } finally {
        saml.control.mutate = null;
      }
    });

    await t.test("8.4 signature wrapping does not move the claims", async () => {
      // A validly signed assertion, with a FORGED sibling inserted before it. The verifier must read
      // the element whose digest it checked, and no other.
      saml.control.mutate = (xml, { assertionId }) =>
        xml.replace(
          new RegExp(`<saml:Assertion ID="${assertionId}"`),
          `<saml:Assertion ID="_wrapped" Version="2.0" IssueInstant="${new Date().toISOString()}">` +
            `<saml:Issuer>${ENTITY}</saml:Issuer><saml:Subject>` +
            `<saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">root@other.test</saml:NameID>` +
            `</saml:Subject></saml:Assertion><saml:Assertion ID="${assertionId}"`,
        );
      try {
        const res = await samlComplete(await samlBegin());
        const session = sessionCookieOf(res);
        if (session) {
          // Accepting is permitted — the signed assertion IS valid — but the claims must have come
          // from it and not from the forgery.
          const page = await fetch(`${node.base}/app`, { headers: { cookie: session } });
          const html = await page.text();
          assert.match(html, new RegExp(TENANT), "the verifier read the WRAPPED assertion");
          assert.ok(!html.includes("cus_other"), "the forged assertion's claims reached the session");
        }
      } finally {
        saml.control.mutate = null;
      }
    });

    await t.test("8.4 an unsolicited assertion is refused", async () => {
      saml.control.inResponseTo = "_never-requested";
      try {
        const res = await samlComplete(await samlBegin());
        assert.equal(sessionCookieOf(res), undefined, "an assertion answering no request issued a session");
      } finally {
        saml.control.inResponseTo = null;
      }
    });

    await t.test("8.4 an ACS POST without the browser's flow cookie is refused", async () => {
      const flow = await samlBegin();
      const res = await samlComplete({ sso: flow.sso, cookie: null });
      assert.equal(sessionCookieOf(res), undefined);
    });

    await t.test("8.5 an unreachable SAML IdP issues no session", async () => {
      saml.control.serveMetadata = false;
      try {
        const res = await fetch(`${node.base}/auth/login?next=%2Fapp`, { redirect: "manual" });
        assert.match(res.headers.get("location"), /\/signin\?reason=idp_unreachable/);
      } finally {
        saml.control.serveMetadata = true;
      }
    });

    await t.test("8.3 the assertion never reaches the session, a cookie, or the log", async () => {
      const flow = await samlBegin();
      const page = await (await fetch(flow.sso)).text();
      const encoded = page.match(/name="SAMLResponse" value="([^"]+)"/)[1];
      const res = await samlComplete(flow);
      assert.ok(sessionCookieOf(res));
      const decoded = Buffer.from(encoded, "base64").toString("utf8");
      const nameId = decoded.match(/<saml:NameID[^>]*>([^<]+)</)[1];
      const logs = node.logs.join("");
      assert.ok(!logs.includes(encoded), "the assertion was logged");
      assert.ok(!logs.includes(nameId), "the subject was logged");
      for (const cookie of res.headers.getSetCookie?.() ?? []) {
        assert.ok(!cookie.includes(encoded), "the assertion was written to a cookie");
      }
    });
  } finally {
    await node?.close();
    await saml?.close();
    await platform2.close();
  }
});

// ── 8.7 · the identity-form matrix ──────────────────────────────────────────────────────────────

test("8.7 every identity form reaches the SAME capability — a form-specific capability is a bug", async (t) => {
  // # Why this exists as one test rather than as three files that each happen to sign in
  //
  // The requirement is not "each form works". It is that the four forms are the SAME product: a
  // capability that works under OIDC and not under `configured` is a bug, and the way that bug ships
  // is that each form is exercised by a different file with a different idea of what "works" means.
  // So the assertion below is written ONCE and run against each form — if it drifts, it drifts for
  // all of them at the same time.
  //
  // The operator form is the fourth, and it is deliberately NOT here: it is a different process in a
  // different language on a different origin, and pretending otherwise is exactly the domain blurring
  // the two-domain split exists to prevent. It is exercised by `internal/adminidentity/p22_test.go`
  // and `internal/api/p22_crossorigin_test.go`, and named in the matrix below so the coverage claim
  // stays checkable rather than implied.
  const OPERATOR_FORM_EVIDENCE = [
    "internal/adminidentity/p22_test.go",
    "internal/api/p22_crossorigin_test.go",
    "internal/api/p22_readyz_test.go",
  ];
  for (const file of OPERATOR_FORM_EVIDENCE) {
    await readFile(join(ROOT, "..", "..", file), "utf8");
  }

  /** capability is the ONE post-sign-in assertion, shared by every form. */
  async function capability(base, sessionCookie, tenant) {
    const page = await fetch(`${base}/app`, { headers: { cookie: sessionCookie } });
    assert.equal(page.status, 200, "a signed-in session does not render the tenant surface");
    assert.match(await page.text(), new RegExp(tenant), "the rendered surface is not scoped to the session's tenant");

    // The tenant is server-side and a client cannot widen it (NFR3), in every form.
    const forged = await fetch(`${base}/api/console/health?tenant=cus_somebody_else`, {
      headers: { cookie: sessionCookie, "x-console-tenant": "cus_somebody_else" },
    });
    assert.notEqual(forged.status, 500, "a forged tenant hint crashed the surface");

    // Revocation is immediate, in every form.
    await fetch(`${base}/api/session`, { method: "DELETE", headers: { cookie: sessionCookie } });
    const after = await fetch(`${base}/app`, { headers: { cookie: sessionCookie }, redirect: "manual" });
    assert.equal(after.status, 307, "a revoked session was still served");
  }

  await t.test("customer OIDC", async () => {
    const flow = await begin();
    const res = await completeOidc(flow);
    await capability(consoleProcess.base, sessionCookieOf(res), TENANT);
  });

  await t.test("customer configured (open-core — federates with nobody)", async () => {
    const platform2 = await startStubPlatform();
    let node;
    try {
      node = await startConsole(platform2.base, {
        CONSOLE_TENANT_IDENTITY: "configured",
        CONSOLE_TENANT_ASSERTIONS: JSON.stringify({ "open-core-assertion": TENANT }),
      });
      const page = await fetch(`${node.base}/signin`);
      const html = await page.text();
      // The form is still there. A redesign that federates must not strand the deployment that does not.
      assert.match(html, /name="assertion"/, "the open-core credential form was removed");
      const post = await fetch(`${node.base}/api/session`, {
        method: "POST",
        headers: { "content-type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ assertion: "open-core-assertion", next: "/app" }),
        redirect: "manual",
      });
      await capability(node.base, sessionCookieOf(post), TENANT);
    } finally {
      await node?.close();
      await platform2.close();
    }
  });

  // customer SAML is exercised by the SAML block above, which runs the same flow end to end; running a
  // third console here would double the suite's wall clock to re-prove what that block already shows.
});

// ── The XML fixture builders are exercised, so an unused export cannot rot unnoticed ─────────────

test("the fixture signer produces a document this repository's parser accepts", () => {
  const xml = samlResponseXml({
    entityId: "urn:e",
    spEntityId: "urn:sp",
    acs: "https://console.test/acs",
    inResponseTo: "_r1",
    assertionId: "_a1",
    nameId: "p@acme.test",
  });
  assert.ok(xml.includes("<!--SIGNATURE-->"));
  assert.equal(typeof samlResponse, "function");
  assert.doesNotThrow(() => parseXml(xml.replace("<!--SIGNATURE-->", "")));
});
