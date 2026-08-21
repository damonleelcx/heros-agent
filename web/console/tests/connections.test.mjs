// connections.test.mjs is P32 §6 — the source surface, executed against RENDERED HTML.
//
// # Why rendered HTML and not the components
//
// Every property this section promises is a property of what a reader SEES, and each of them is
// compatible with a green component test:
//
//   §6.1  the mode, the last successful read, and the last failure with its cause;
//   §6.4  four causes render as four MESSAGES — not one message with four labels;
//   §6.5  `not reported` where there is no snapshot, and NO prompt-to-connect as a precondition;
//   §8.2  the consent screen states that a connection is usable when the customer is not present.
//
// A component test asserts a function returned an element. This asserts the sentence is on the page.
//
// # 🔴 The fixture is deliberately awkward
//
// One connection has never been read, one is failing, and one has a sub-path. A fixture in which
// everything works cannot show the states this surface exists for — and `never read` rendering as an
// epoch date is exactly the kind of defect that survives a happy-path fixture.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { startStubPlatform, startConsole, signIn } from "./support/harness.mjs";

let platform;
let console_;
let cookie;

const NOW = 1_755_000_000_000;
const HOUR = 3_600_000;

const FORGES = [
  {
    forge: "github",
    host: "github.com",
    grant_kind: "app_installation",
    grant_label: "a GitHub App installation limited to this one repository",
    permission: "contents: read, metadata: read",
    revoke_hint: "GitHub → Settings → Applications → Installed GitHub Apps → Configure → Uninstall",
  },
];

const CONNECTIONS = [
  {
    connection_id: "conn-read",
    workflow_id: "wf-read",
    mode: "connected",
    forge: "github",
    repository: "acme/read",
    grant_kind: "app_installation",
    grant_label: FORGES[0].grant_label,
    revoke_hint: FORGES[0].revoke_hint,
    created_by: "u1",
    created_at_ms: NOW - 72 * HOUR,
    last_success_at_ms: NOW - 2 * HOUR,
    last_success_revision: "a1b2c3d4e5f60718",
    last_failure_at_ms: 0,
    last_actor: "scheduled",
  },
  {
    connection_id: "conn-never",
    workflow_id: "wf-never",
    mode: "connected",
    forge: "github",
    repository: "acme/never",
    sub_path: "services/router",
    grant_kind: "app_installation",
    grant_label: FORGES[0].grant_label,
    revoke_hint: FORGES[0].revoke_hint,
    created_by: "u1",
    created_at_ms: NOW - HOUR,
    last_success_at_ms: 0,
    last_failure_at_ms: 0,
  },
];

/** withConnections answers the source surface's reads with `connections`. */
function withConnections(connections) {
  platform.set((req, res) => {
    res.writeHead(200, { "content-type": "application/json" });
    if (req.url.startsWith("/api/v1/repo-connections")) {
      return res.end(
        JSON.stringify({
          connections,
          forges: FORGES,
          local_mode_deployments: ["https://heros-agent.space"],
          retention_hours: 72,
        }),
      );
    }
    if (req.url.startsWith("/api/v1/repo-connection-reads")) {
      return res.end(JSON.stringify({ connection_id: "conn-read", records: [] }));
    }
    if (req.url.startsWith("/api/v1/local-pairings")) {
      return res.end(
        JSON.stringify({
          pairings: [],
          availability: { deployments: ["https://heros-agent.space"], available: true },
          command: "heros pair --code <the code above> --repo .",
        }),
      );
    }
    return res.end("{}");
  });
}

async function page(path = "/app/connections") {
  const res = await fetch(`${console_.base}${path}`, { headers: { cookie } });
  assert.equal(res.status, 200, `${path} answered ${res.status}`);
  return res.text();
}

/**
 * rendered strips the React flight payload, leaving what a reader can actually see.
 *
 * 🔴 This exists because the first version of the epoch-date assertion failed on a page that renders
 * correctly. Client components receive their props as SERIALIZED data in `<script>` blocks, so
 * `last_success_at_ms: 1755000000000` is in the document whether or not anything renders it — and an
 * assertion over the whole document was measuring the transport rather than the output.
 *
 * The distinction matters beyond this test: "is this value in the HTML" and "does a reader see this
 * value" are different questions, and a fence that conflates them either cries wolf (here) or, worse,
 * passes because a value it was looking for happened to be in a payload nobody displays.
 */
function rendered(html) {
  return html.replace(/<script\b[^>]*>[\s\S]*?<\/script>/gi, " ");
}

before(async () => {
  platform = await startStubPlatform();
  console_ = await startConsole(platform.base);
  cookie = await signIn(console_.base);
}, { timeout: 60_000 });

after(async () => {
  await console_?.close();
  await platform?.close();
});

// §6.1 — the connection list carries the mode, the last successful read, and the last failure.
test("§6.1 the list states the mode, the last read and the last failure with its cause", async () => {
  withConnections([
    ...CONNECTIONS,
    {
      ...CONNECTIONS[0],
      connection_id: "conn-fail",
      workflow_id: "wf-fail",
      repository: "acme/failing",
      last_failure_at_ms: NOW - HOUR,
      last_failure_cause: "credential_rejected",
      last_actor: "person",
    },
  ]);
  const html = await page();

  assert.ok(html.includes("acme/read"), "the connected repository is not named");
  assert.ok(html.includes("connected repository"), "the MODE is not rendered");
  // The last successful read, as a date rather than as a raw millisecond value — asserted over what
  // a reader SEES, not over the flight payload. See `rendered`.
  const visible = rendered(html);
  assert.ok(!visible.includes(String(NOW - 2 * HOUR)), "a raw epoch millisecond value is rendered to the reader");
  assert.ok(/Aug\s+\d+,\s+2025/.test(visible), "no formatted date reached the page");
  // The last failure, with its cause.
  assert.ok(html.includes("The forge refused our credential"), "the failure cause is not named");
});

// 🔴 §6.4 — FOUR causes are FOUR messages. This is the assertion that catches the shape where one
// apologetic sentence carries a different label, which is what gets built when nobody checks.
test("§6.4 each of the four causes renders its own message and its own next action", async () => {
  const causes = [
    ["credential_rejected", "The forge refused our credential", "Yours to fix, on the forge."],
    ["repository_not_found", "The repository could not be found", "Yours to fix, on the forge."],
    ["revision_not_found", "That revision is not in the repository", "Yours to fix, in the repository."],
    ["network", "We could not reach the forge", "Ours to fix."],
  ];
  const seen = new Set();
  for (const [cause, title, whose] of causes) {
    withConnections([
      { ...CONNECTIONS[0], connection_id: `c-${cause}`, last_failure_at_ms: NOW - HOUR, last_failure_cause: cause },
    ]);
    const html = await page();
    assert.ok(html.includes(title), `${cause} does not render its own title`);
    assert.ok(html.includes(whose), `${cause} does not say whose problem it is`);
    assert.ok(!seen.has(title), `${cause} reuses a title another cause already used`);
    seen.add(title);
  }
  assert.equal(seen.size, 4, "the four causes did not produce four distinct messages");
});

// 🔴 §6.5 — `never read` is not an epoch date, and `not reported` is used where nothing was sent.
test("§6.5 a connection that has never been read says so, rather than rendering 1970", async () => {
  withConnections(CONNECTIONS);
  const visible = rendered(await page());
  assert.ok(visible.includes("never read"), "a never-read connection does not say so");
  assert.ok(!visible.includes("1970"), "a zero timestamp rendered as an epoch date");
  assert.ok(visible.includes("not reported"), "an unreported actor does not render as `not reported`");
  // The sub-path is rendered, because what we READ is narrower than what the grant covers.
  assert.ok(visible.includes("services/router"), "the sub-path is not rendered");
});

// 🔴 §6.5 second half, and FR12 — an empty state is a STATED ABSENCE, never a prompt.
//
// This is the screen a Mode 1 customer sees forever. The failure it guards against is a page that
// reads as a setup step they have skipped, on a platform where connecting is an upgrade most
// customers should decline.
test("§6.5 the empty state does not prompt for a connection as a precondition", async () => {
  withConnections([]);
  const html = await page();
  assert.ok(html.includes("No repository is connected"), "the empty state is missing");
  assert.ok(html.includes("Nothing is missing"), "the empty state does not say the absence is fine");
  assert.ok(
    html.includes("nothing on this platform requires a connection") ||
      html.includes("Every surface in this console works from a pushed bundle"),
    "the empty state does not state that no feature is gated on a connection",
  );
  for (const nag of ["You need to connect", "required to continue", "Set up your", "Finish setting up"]) {
    assert.ok(!html.includes(nag), `the empty state nags: ${nag}`);
  }
});

// §8.2 — the boundary, out loud, on the consent screen's own copy.
//
// The screen is client-rendered on a press, so this asserts the SENTENCE ships in the bundle the page
// serves. A sentence that is not in the payload cannot be shown by any press.
test("§8.2 the consent copy states that a connection is usable when the customer is not present", async () => {
  withConnections(CONNECTIONS);
  const html = await page();
  const chunks = [...html.matchAll(/src="([^"]+\.js)"/g)].map((m) => m[1]);
  let bundled = "";
  for (const src of chunks) {
    const res = await fetch(src.startsWith("http") ? src : `${console_.base}${src}`, { headers: { cookie } });
    if (res.ok) bundled += await res.text();
  }
  const haystack = html + bundled;
  assert.ok(
    haystack.includes("usable when you are not present"),
    "the consent copy does not state the unattended-use boundary — §8.2's whole requirement",
  );
  assert.ok(
    haystack.includes("What you are about to permit"),
    "the consent screen's heading is not in anything the page serves",
  );
  // §6.3 — the revoke confirmation states that derived trees are deleted.
  assert.ok(
    haystack.includes("every tree we derived from it"),
    "the revoke confirmation does not state that derived trees are deleted",
  );
});

// 🚫 §4 / design D5 — there is no file picker anywhere on this surface.
//
// A control that read a folder and posted it would be Mode 1 wearing Mode 3's clothes. This asserts
// the absence directly, because the defect arrives as somebody adding a convenience.
test("D5 the local mode offers a pairing flow and no file picker", async () => {
  withConnections(CONNECTIONS);
  const html = await page();
  assert.ok(!/type="file"/.test(html), "a file input is on the source surface");
  assert.ok(!/webkitdirectory/.test(html), "a directory picker is on the source surface");
  assert.ok(
    html.includes("read on your machine") || html.includes("Read on your machine"),
    "the local-mode tab is missing",
  );
});
