// platform-identity.test.mjs is the acceptance gate for the `platform` identity seam.
//
// # The defect this seam closes, stated so the test can be read against it
//
// `heros login` authenticates a platform tenant token; `heros link` transmits with it and then prints
// `view it at https://<platform>/app/runs/<run-id>`. Opening that URL asked for a credential — and the
// console's `configured` seam checked a DIFFERENT secret (`CONSOLE_TENANT_ASSERTIONS`) that no `heros`
// command mints, prints or derives. The success message pointed at a door the user had no key to. This
// file's happy path is exactly that journey: authenticate a token against the platform, then use the
// same token at the console.
//
// # The adversarial half is the load-bearing half
//
// The obvious wrong implementation is to verify the presented credential with the BFF's own
// `platformFetch` — which sends `CONSOLE_PLATFORM_CREDENTIAL`. That "works" in a happy-path test and
// authenticates EVERY input as the console's own tenant, including an empty one. So the sharpest test
// here is not that a good token signs in: it is that the whoami call carried the PRESENTED value, and
// that a bad token is refused while the platform is up and answering.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { startStubPlatform, startConsole } from "./support/harness.mjs";

const GOOD_TOKEN = "platform-token-that-the-platform-accepts";
const BAD_TOKEN = "platform-token-that-the-platform-rejects";
const BFF_CREDENTIAL = "harness-platform-credential-do-not-ship";
const TENANT = "tenant-from-the-platform";

let platform;
let console_;

/** whoami answers as the real platform does: bearer in, `{identity}` out, 401 for anything else. */
function whoamiHandler(req, res) {
  if (!req.url.startsWith("/api/v1/whoami")) {
    res.writeHead(200, { "content-type": "application/json" });
    res.end("{}");
    return;
  }
  const auth = req.headers.authorization ?? "";
  const token = auth.toLowerCase().startsWith("bearer ") ? auth.slice(7).trim() : "";
  if (token !== GOOD_TOKEN) {
    res.writeHead(401, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "unauthorized" }));
    return;
  }
  res.writeHead(200, { "content-type": "application/json" });
  res.end(JSON.stringify({ identity: TENANT }));
}

before(async () => {
  platform = await startStubPlatform();
  platform.set(whoamiHandler);
  console_ = await startConsole(platform.base, {
    CONSOLE_TENANT_IDENTITY: "platform",
    // Deliberately left set: the seam must not consult it, and leaving it configured is how a
    // regression that falls back to the old map would still pass its own tests.
    CONSOLE_TENANT_ASSERTIONS: JSON.stringify({ "some-other-secret": "tenant-that-must-not-win" }),
  });
});

after(async () => {
  await console_?.close();
  await platform?.close();
});

/** post signs in with a raw credential value, returning {status, location, session}. */
async function post(assertion, next = "/app") {
  const page = await fetch(`${console_.base}/signin`);
  const target = (await page.text()).match(/<form[^>]*action="([^"]+)"/)?.[1];
  assert.ok(target, "the sign-in page rendered no form action");
  const res = await fetch(`${console_.base}${target}`, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ assertion, next }),
    redirect: "manual",
  });
  const session = (res.headers.getSetCookie?.() ?? [])
    .map((c) => c.split(";")[0])
    .find((c) => c.startsWith("heros_console_session="));
  return { status: res.status, location: res.headers.get("location") ?? "", session };
}

// ── the journey the defect broke ────────────────────────────────────────────────────────────────

test("the token `heros login` authenticates also signs into the console", async () => {
  const { status, location, session } = await post(GOOD_TOKEN);
  assert.equal(status, 303, "sign-in did not redirect");
  assert.equal(location, "/app", `sign-in was refused: ${location}`);
  assert.ok(session, "no session cookie was issued");

  // And the session actually renders the tenant's surface, rather than bouncing back to /signin.
  const app = await fetch(`${console_.base}/app`, { headers: { cookie: session }, redirect: "manual" });
  assert.equal(app.status, 200, `the issued session did not open /app (status ${app.status})`);
});

test("the tenant is the one the PLATFORM named, not anything derived from the input", async () => {
  const { session } = await post(GOOD_TOKEN);
  const before = platform.requests.length;
  await fetch(`${console_.base}/api/console/runs/run-1`, { headers: { cookie: session } });
  const upstream = platform.requests.slice(before).find((r) => !r.url.startsWith("/api/v1/whoami"));
  assert.ok(upstream, "the session made no upstream call to attribute");
  assert.equal(
    upstream.headers["x-console-tenant"],
    TENANT,
    "the tenant on upstream calls is not the identity the platform returned",
  );
});

// ── the adversarial half ────────────────────────────────────────────────────────────────────────

test("the whoami call carries the PRESENTED credential, never the BFF's own", async () => {
  const before = platform.requests.length;
  await post(GOOD_TOKEN);
  const whoami = platform.requests.slice(before).filter((r) => r.url.startsWith("/api/v1/whoami"));
  assert.equal(whoami.length, 1, `expected exactly one whoami call, got ${whoami.length}`);

  const auth = whoami[0].headers.authorization ?? "";
  assert.equal(auth, `Bearer ${GOOD_TOKEN}`, "the presented credential was not the bearer");
  assert.ok(
    !JSON.stringify(whoami[0].headers).includes(BFF_CREDENTIAL),
    "the BFF's own credential was sent — every input would authenticate as the console's tenant",
  );
});

test("a credential the platform rejects is refused, while the platform is up and answering", async () => {
  const { status, location, session } = await post(BAD_TOKEN);
  assert.equal(status, 303);
  assert.match(location, /^\/signin\?reason=rejected/, `a rejected token was not refused: ${location}`);
  assert.equal(session, undefined, "a session was issued for a credential the platform rejected");
});

test("the old configured map is not consulted in platform mode", async () => {
  const { location, session } = await post("some-other-secret");
  assert.match(location, /reason=rejected/, "the CONSOLE_TENANT_ASSERTIONS map still authenticated somebody");
  assert.equal(session, undefined);
});

test("an empty credential is refused without asking the platform", async () => {
  const before = platform.requests.length;
  const { location, session } = await post("");
  assert.match(location, /reason=rejected/);
  assert.equal(session, undefined);
  const whoami = platform.requests.slice(before).filter((r) => r.url.startsWith("/api/v1/whoami"));
  assert.equal(whoami.length, 0, "an empty credential was sent upstream");
});

test("a platform that accepts the token but names nobody is refused", async () => {
  platform.set((req, res) => {
    if (req.url.startsWith("/api/v1/whoami")) {
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ identity: "" }));
      return;
    }
    res.writeHead(200, { "content-type": "application/json" });
    res.end("{}");
  });
  try {
    const { location, session } = await post(GOOD_TOKEN);
    assert.match(location, /reason=rejected/, "an empty identity produced a session scoped to nothing");
    assert.equal(session, undefined);
  } finally {
    platform.set(whoamiHandler);
  }
});

test("an unreachable platform fails CLOSED — not knowing is not a yes", async () => {
  platform.set((req, res) => {
    if (req.url.startsWith("/api/v1/whoami")) {
      // A socket that connects and then says nothing: the shape a timeout must catch.
      return;
    }
    res.writeHead(200, { "content-type": "application/json" });
    res.end("{}");
  });
  try {
    const { location, session } = await post(GOOD_TOKEN);
    assert.match(location, /reason=rejected/, "a hung platform authenticated somebody");
    assert.equal(session, undefined);
  } finally {
    platform.set(whoamiHandler);
  }
});

test("a platform 5xx does not sign anyone in", async () => {
  platform.set((req, res) => {
    res.writeHead(503, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "unavailable" }));
  });
  try {
    const { location, session } = await post(GOOD_TOKEN);
    assert.match(location, /reason=rejected/);
    assert.equal(session, undefined);
  } finally {
    platform.set(whoamiHandler);
  }
});

// ── health must be able to say no ───────────────────────────────────────────────────────────────

test("health reports the platform as the identity authority, and can report it UNREACHABLE", async () => {
  const up = await (await fetch(`${console_.base}/api/health`)).json();
  assert.equal(up.identity_provider.kind, "platform");
  assert.equal(up.identity_provider.reachable, true, "a healthy platform was not reported reachable");

  platform.set((req, res) => {
    if (req.url.startsWith("/api/v1/whoami")) return; // hang
    res.writeHead(200, { "content-type": "application/json" });
    res.end("{}");
  });
  try {
    const down = await (await fetch(`${console_.base}/api/health`)).json();
    assert.equal(
      down.identity_provider.reachable,
      false,
      "identity health reported reachable while the authority was hung — a signal that cannot fail",
    );
  } finally {
    platform.set(whoamiHandler);
  }
});

// ── the credential never comes back out ─────────────────────────────────────────────────────────

test("the credential appears in no response body, and in no cookie", async () => {
  const page = await fetch(`${console_.base}/signin`);
  const target = (await page.text()).match(/<form[^>]*action="([^"]+)"/)?.[1];
  const res = await fetch(`${console_.base}${target}`, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ assertion: GOOD_TOKEN, next: "/app" }),
    redirect: "manual",
  });
  const cookies = (res.headers.getSetCookie?.() ?? []).join(" ");
  assert.ok(!cookies.includes(GOOD_TOKEN), "the credential was written into a cookie");

  const session = cookies.split(";")[0];
  const app = await fetch(`${console_.base}/app`, { headers: { cookie: session } });
  const html = await app.text();
  assert.ok(!html.includes(GOOD_TOKEN), "the credential was rendered into the page");
});
