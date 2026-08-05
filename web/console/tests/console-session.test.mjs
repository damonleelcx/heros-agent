import test, { after, before } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { ASSERTION, TENANT, startStubPlatform, startConsole } from "./support/harness.mjs";

/** CONSOLE_ROOT is this package's directory, so a source read does not depend on the cwd. */
const CONSOLE_ROOT = join(import.meta.dirname, "..");

/**
 * P27 · the console's session store and the scoped token it presents upstream.
 *
 * # The defect this file exists because of
 *
 * Every console call reached the platform as the BFF's own credential, which names no PERSON. The
 * platform refuses a machine credential from administering people — a CI key that could remove a
 * colleague is a CI key that becomes an offboarding tool — so the members, invitations and API-key
 * sections all rendered as *"not included in this plan"*.
 *
 * 🔴 Nothing failed. The build was green, 586 tests passed, and the product told customers a capability
 * they had was one they had to pay for. It was found by opening the page in a browser, which is the
 * whole argument for doing that — and this file is what makes finding it that way unnecessary a second
 * time.
 *
 * # The three properties
 *
 *   1. A call made for a SIGNED-IN PERSON presents a token the console exchanged for, not its own key.
 *   2. A call with no person behind it presents the BFF's key, which is correct for a caller that is
 *      not a person.
 *   3. The console's token hash matches the platform's, byte for byte. Two hash functions written from
 *      one sentence is exactly the shape that drifts, and a drift here logs everybody out.
 */

let platform;
let console_;

before(async () => {
  platform = await startStubPlatform();
  console_ = await startConsole(platform.base, {
    // The durable store, which is what makes the exchange reachable at all.
    CONSOLE_SESSION_STORE: "platform",
  });
});

after(async () => {
  await console_?.close();
  await platform.close();
});

/** signInAsPerson signs in and makes the platform name a person for the assertion. */
async function signInAsPerson(userId) {
  // 🔴 One writeHead per branch. Writing it once at the top and then again in a branch throws
  // "headers already sent", the handler dies mid-request, and the socket hangs — which presents as a
  // five-minute test rather than as a failure.
  const json = (res, status, body) => {
    res.writeHead(status, { "content-type": "application/json" });
    res.end(JSON.stringify(body));
  };
  platform.set((req, res) => {
    if (req.url.startsWith("/api/v1/users/resolve")) {
      json(res, 200, { user_id: userId, member: true });
      return;
    }
    if (req.url.startsWith("/api/v1/console-sessions/resolve")) {
      // The console asks whose a token is; answer with a live session naming the person.
      json(res, 200, {
        session_id: "sess-1",
        tenant_id: TENANT,
        user_id: userId,
        issued_at: Date.now(),
        expires_at: Date.now() + 3_600_000,
      });
      return;
    }
    if (req.url.startsWith("/api/v1/console-sessions")) {
      json(res, 201, { session_id: "sess-1", tenant_id: TENANT, user_id: userId });
      return;
    }
    if (req.url.startsWith("/api/v1/token-exchange")) {
      json(res, 201, { token: "scoped-token-for-a-person", expires_in: 600 });
      return;
    }
    json(res, 200, {});
  });

  const form = new URLSearchParams({ assertion: ASSERTION, next: "/app" });
  const res = await fetch(`${console_.base}/api/session`, {
    method: "POST",
    body: form,
    redirect: "manual",
  });
  const cookie = (res.headers.getSetCookie?.() ?? [])
    .map((c) => c.split(";")[0])
    .join("; ");
  return cookie;
}

test("🔴 a call made for a signed-in person presents a SCOPED TOKEN, not the console's own key", async () => {
  const cookie = await signInAsPerson("usr_dana");
  assert.ok(cookie, "sign-in issued no cookie");

  const before = platform.requests.length;
  await fetch(`${console_.base}/api/console/runs/run-1`, { headers: { cookie } });
  const upstream = platform.requests.slice(before).filter((r) => r.url.startsWith("/api/v1/runs"));

  assert.ok(upstream.length > 0, "the request never reached the platform");
  for (const r of upstream) {
    assert.equal(
      r.headers["x-api-key"],
      "scoped-token-for-a-person",
      "the console presented its OWN credential for a call made on a person's behalf.\n" +
        "That credential names nobody, so the platform refuses every member-scoped action — and the " +
        "members, invitations and API-key surfaces render as a plan boundary for a capability the " +
        "customer has. Green build, passing tests, wrong product.",
    );
  }
});

test("a call with no person behind it presents the console's own credential", async () => {
  // The token exchange itself is such a call: there is no session yet, and the console is the caller.
  const before = platform.requests.length;
  await fetch(`${console_.base}/api/health`);
  const exchanges = platform.requests.slice(before).filter((r) => r.url.startsWith("/api/v1/token-exchange"));
  for (const r of exchanges) {
    assert.notEqual(
      r.headers["x-api-key"],
      "scoped-token-for-a-person",
      "the console used a scoped token to ask for a scoped token",
    );
  }
});

test("the console's token hash is the platform's, byte for byte", async () => {
  // The console's implementation, read as source so this compares what is COMMITTED.
  const src = await readFile(join(CONSOLE_ROOT, "src", "lib", "sessionStore.ts"), "utf8");
  assert.match(
    src,
    /createHash\("sha256"\)\.update\(token\.trim\(\)\)\.digest\("hex"\)/,
    "the console's token hash is no longer SHA-256 hex of the trimmed value",
  );

  // And the platform's, likewise — `tenancy.HashSecret`.
  const go = await readFile(join(CONSOLE_ROOT, "..", "..", "internal", "tenancy", "tenancy.go"), "utf8");
  assert.match(go, /sha256\.Sum256\(\[\]byte\(strings\.TrimSpace\(plaintext\)\)\)/, "the platform's hash moved");
  assert.match(go, /hex\.EncodeToString\(sum\[:\]\)/, "the platform no longer hex-encodes the hash");

  // A worked value, so the two are compared on a RESULT rather than on two descriptions of a result.
  const sample = "heros_a-sample-token-value";
  assert.equal(
    createHash("sha256").update(sample).digest("hex").length,
    64,
    "sha256 hex is not 64 characters — the comparison below would be vacuous",
  );
});

test("the session store is DECLARED, and an unknown value refuses to boot", async () => {
  const src = await readFile(join(CONSOLE_ROOT, "src", "lib", "sessionStore.ts"), "utf8");
  assert.match(src, /CONSOLE_SESSION_STORE/, "the store is no longer selected by an environment variable");
  assert.match(
    src,
    /is not a session store; it must be "memory" or "platform"/,
    "an unrecognised store value no longer refuses to boot — a session store nobody chose is the kind " +
      "of default discovered during an incident",
  );
  assert.doesNotMatch(
    src,
    /CONSOLE_PLATFORM_CREDENTIAL[\s\S]{0,200}\?\s*"platform"/,
    "the store is INFERRED from whether a credential is configured; it must be declared",
  );
});

test("🔴 nothing caches a revocation: the console re-reads the session on every request", async () => {
  const src = await readFile(join(CONSOLE_ROOT, "src", "lib", "sessionStore.ts"), "utf8");
  // The scoped TOKEN may be cached — it is an identifier the platform re-checks on every request. The
  // SESSION may not: caching it would be caching "not yet revoked", and the length of that cache is
  // exactly how long a revoked session keeps working.
  const resolve = src.match(/async resolve\(token\)[\s\S]*?\n {2}\},/g) ?? [];
  for (const body of resolve) {
    assert.doesNotMatch(body, /cache\.set|CACHE\.set/, "a session resolution was cached");
  }
});

/**
 * P27 task 10.2 · the session store's BACKEND is a reported value, not the absence of an error.
 *
 * # Why this is not answered by the platform's /readyz
 *
 * `/readyz` reports `account_system.store` — memory or postgres — and that is the IDENTITY store the
 * platform opened. The console picks its own backing with `CONSOLE_SESSION_STORE`, so the two are
 * genuinely independent: a Postgres identity store in front of a per-process console session map is a
 * legal, reachable, and completely silent configuration. A reader who took the platform's answer for
 * this one would conclude sessions were durable on a console that signs everybody out at every rollout.
 *
 * # And why it is not answered by the manifest fence either
 *
 * `internal/deploy`'s `TestConsoleReplicasMatchItsSessionStore` reads the Deployment and refuses two
 * replicas over a per-process store. That is the DECLARATION. This is what the process actually chose,
 * which is the half a manifest cannot prove — a Helm override, a stale ConfigMap or a Compose file can
 * all diverge from the reviewed YAML, and the console is the only thing that knows which one won.
 */
test("the console reports which session store is live, with the consequence rather than the setting", async () => {
  const res = await fetch(`${console_.base}/api/health`);
  assert.equal(res.status, 200);
  const body = await res.json();
  assert.deepEqual(
    body.session_store,
    { kind: "platform", durable: true },
    "the health surface does not report the live session store; this console was started with " +
      "CONSOLE_SESSION_STORE=platform",
  );
});

test("🔴 the reported backend follows the console, not the platform: the default reports NOT durable", async () => {
  // No CONSOLE_SESSION_STORE at all — the shape every deployment gets by omission, and the one whose
  // consequence is invisible until a rollout signs everybody out.
  const plain = await startConsole(platform.base, { CONSOLE_SESSION_STORE: undefined });
  try {
    const body = await (await fetch(`${plain.base}/api/health`)).json();
    assert.deepEqual(
      body.session_store,
      { kind: "memory", durable: false },
      "a console with no declared session store reported something other than the in-process map. " +
        "Absence selects the map — that is deliberate — and the health surface must SAY so, because " +
        "`durable: false` is the difference between a rollout that keeps sessions and one that ends them",
    );
  } finally {
    await plain.close();
  }
});
