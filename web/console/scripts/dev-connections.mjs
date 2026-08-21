// dev-connections.mjs runs the console's P32 source surface against a stub platform, for
// rendered-browser acceptance (R11).
//
// # Why this exists as a script rather than as a paragraph in a checklist
//
// The defects that matter on this surface are the ones a passing build cannot see, and every one of
// them is a click:
//
//   - the consent screen appearing AFTER the authorization form rather than before it;
//   - the revoke control firing on its first press rather than arming;
//   - a failure cause landing in a `default:` arm and rendering as nothing;
//   - `never read` rendering as an epoch date, which looks like a real answer.
//
// So the acceptance rule is a browser, and a browser needs a platform to talk to. This is that
// platform: a stub that serves the four P32 read models with a deliberately AWKWARD fixture — one
// connection that has never been read, one that is failing, one with a sub-path — because a fixture in
// which everything works is a fixture that cannot show the states this surface exists for.
//
//   npm run dev:connections
//
// 🚫 It is a DEV harness and serves no real data. The real end-to-end — connect, SELECT the row, clone,
// assert files on disk — is §7.10 and runs against the real platform.

import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { once } from "node:events";
import process from "node:process";

const CONSOLE_PORT = Number(process.env.PORT ?? 4520);
const TENANT = process.env.TENANT ?? "tenant-hermes";

// A fixed clock, so the rendered dates do not move between runs and a screenshot means something.
const NOW = 1_755_000_000_000;
const HOUR = 3_600_000;

const FORGES = [
  {
    forge: "bitbucket",
    host: "bitbucket.org",
    grant_kind: "access_token",
    grant_label: "a Bitbucket repository access token scoped to this one repository",
    permission: "repository:read",
    revoke_hint: "Bitbucket → Repository settings → Access tokens → Revoke",
  },
  {
    forge: "github",
    host: "github.com",
    grant_kind: "app_installation",
    grant_label: "a GitHub App installation limited to this one repository",
    permission: "contents: read, metadata: read",
    revoke_hint: "GitHub → Settings → Applications → Installed GitHub Apps → Configure → Uninstall",
  },
  {
    forge: "gitlab",
    host: "gitlab.com",
    grant_kind: "access_token",
    grant_label: "a GitLab project access token scoped to this one project",
    permission: "read_repository",
    revoke_hint: "GitLab → Project → Settings → Access Tokens → Revoke",
  },
];

// 🔴 The fixture covers every state the surface has to render, INCLUDING the ones nobody would build a
// happy-path fixture for: a connection that has never succeeded, and one whose newest read failed.
const CONNECTIONS = [
  {
    connection_id: "conn-1",
    workflow_id: "github.com/nousresearch/hermes-agent",
    mode: "connected",
    forge: "github",
    repository: "nousresearch/hermes-agent",
    grant_kind: "app_installation",
    grant_label: FORGES[1].grant_label,
    revoke_hint: FORGES[1].revoke_hint,
    created_by: "u_dev",
    created_at_ms: NOW - 72 * HOUR,
    last_success_at_ms: NOW - 2 * HOUR,
    last_success_revision: "a1b2c3d4e5f60718293a4b5c",
    last_failure_at_ms: 0,
    last_actor: "scheduled",
  },
  {
    connection_id: "conn-2",
    workflow_id: "acme/monorepo:services/router",
    mode: "connected",
    forge: "gitlab",
    repository: "acme/monorepo",
    sub_path: "services/router",
    grant_kind: "access_token",
    grant_label: FORGES[2].grant_label,
    revoke_hint: FORGES[2].revoke_hint,
    created_by: "u_dev",
    created_at_ms: NOW - 10 * HOUR,
    // Never read. Renders as `never read`, NOT as an epoch date.
    last_success_at_ms: 0,
    last_failure_at_ms: 0,
  },
  {
    connection_id: "conn-3",
    workflow_id: "acme/api",
    mode: "connected",
    forge: "bitbucket",
    repository: "acme/api",
    grant_kind: "access_token",
    grant_label: FORGES[0].grant_label,
    revoke_hint: FORGES[0].revoke_hint,
    created_by: "u_dev",
    created_at_ms: NOW - 200 * HOUR,
    last_success_at_ms: NOW - 50 * HOUR,
    last_success_revision: "99887766554433221100aabb",
    // Failing. One of the four causes, so the four-message rendering is exercised.
    last_failure_at_ms: NOW - HOUR,
    last_failure_cause: "credential_rejected",
    last_actor: "person",
  },
];

const LEDGERS = {
  "conn-1": [
    { record_id: "r1", repository: "nousresearch/hermes-agent", revision: "a1b2c3d4e5f60718293a4b5c", actor: "scheduled", reason: "nightly assessment", outcome: "succeeded", bytes: 4_200_000, entries: 812, duration_ms: 3_100, at_ms: NOW - 2 * HOUR },
    { record_id: "r2", repository: "nousresearch/hermes-agent", revision: "0f1e2d3c4b5a69788796a5b4", actor: "person", actor_id: "u_dev", reason: "conversation c-77", outcome: "succeeded", bytes: 4_190_000, entries: 810, duration_ms: 2_950, at_ms: NOW - 26 * HOUR },
  ],
  "conn-3": [
    { record_id: "r3", repository: "acme/api", revision: "77665544332211009988aabb", actor: "scheduled", reason: "nightly assessment", outcome: "credential_rejected", bytes: 0, entries: 0, duration_ms: 120, at_ms: NOW - HOUR },
    { record_id: "r4", repository: "acme/api", revision: "99887766554433221100aabb", actor: "person", actor_id: "u_dev", outcome: "succeeded", bytes: 900_000, entries: 210, duration_ms: 1_400, at_ms: NOW - 50 * HOUR },
  ],
};

const PAIRINGS = {
  pairings: [
    {
      pairing_id: "pair-1",
      workflow_id: "local/experiment",
      state: "paired",
      machine_name: "dev-laptop",
      revision: "cafebabedeadbeef01234567",
      created_at_ms: NOW - 3 * HOUR,
      claimed_at_ms: NOW - 3 * HOUR,
      expires_at_ms: NOW - 3 * HOUR + 600_000,
    },
  ],
  availability: { deployments: ["https://heros-agent.space"], available: true },
  command: "heros pair --code <the code above> --repo .",
};

const server = createServer((req, res) => {
  const url = new URL(req.url, "http://stub");
  const json = (code, body) => {
    res.writeHead(code, { "content-type": "application/json" });
    res.end(JSON.stringify(body));
  };
  if (url.pathname === "/api/v1/repo-connections" && req.method === "GET") {
    return json(200, { connections: CONNECTIONS, forges: FORGES, local_mode_deployments: ["https://heros-agent.space"], retention_hours: 72 });
  }
  if (url.pathname === "/api/v1/repo-connections" && req.method === "POST") {
    // 🔴 The stub performs the BREADTH REFUSAL, because it is the behaviour the screen exists to make
    // visible. A stub that accepted everything would make the connect form look like it works and hide
    // the one refusal ADR-013 Option B rests on.
    let raw = "";
    req.on("data", (c) => (raw += c));
    return req.on("end", () => {
      let body = {};
      try {
        body = JSON.parse(raw);
      } catch {
        return json(400, { error: "malformed request" });
      }
      const covers = body.covers ?? [];
      if (body.account_wide || covers.length !== 1 || covers[0] !== body.repository) {
        return json(422, {
          error: `sourceingest: the authorization covers repositories that were not named: the ${body.forge} grant covers ${covers.length} repositories (${covers.join(", ") || "none"}) and exactly one was named ("${body.repository}")`,
        });
      }
      return json(201, { connection_id: "conn-new", repository: body.repository, mode: "connected", forge: body.forge, grant_kind: body.grant_kind, workflow_id: body.workflow_id, created_at_ms: NOW, last_success_at_ms: 0, last_failure_at_ms: 0 });
    });
  }
  if (url.pathname === "/api/v1/repo-connection-revocations") {
    return json(200, { connection_id: "conn-1", snapshots_deleted: 2 });
  }
  if (url.pathname === "/api/v1/repo-connection-reads") {
    const id = url.searchParams.get("connection_id");
    return json(200, { connection_id: id, records: LEDGERS[id] ?? [] });
  }
  if (url.pathname === "/api/v1/local-pairings" && req.method === "GET") {
    return json(200, PAIRINGS);
  }
  if (url.pathname === "/api/v1/local-pairings" && req.method === "POST") {
    return json(201, { pairing_id: "pair-new", workflow_id: "local/experiment", state: "pending", user_code: "ACDE-FGHJ", created_at_ms: NOW, expires_at_ms: NOW + 600_000 });
  }
  // Every other read model this shell touches. `{}` rather than 404, so the rail and the header render
  // and the surface under test is the only thing that can fail.
  return json(200, {});
});

server.listen(0, "127.0.0.1");
await once(server, "listening");
const { port } = server.address();
const base = `http://127.0.0.1:${port}`;
console.log(`stub platform on ${base}`);
console.log(`console on http://127.0.0.1:${CONSOLE_PORT}/app/connections`);

const child = spawn("npx", ["next", "dev", "--port", String(CONSOLE_PORT)], {
  stdio: "inherit",
  env: {
    ...process.env,
    PLATFORM_API_BASE: base,
    CONSOLE_PLATFORM_CREDENTIAL: "local-dev-credential-not-a-secret",
    CONSOLE_TENANT_IDENTITY: "configured",
    CONSOLE_TENANT_ASSERTIONS: JSON.stringify({ "local-dev-assertion": TENANT }),
  },
});
const stop = () => {
  child.kill("SIGTERM");
  server.close();
};
process.on("SIGINT", stop);
process.on("SIGTERM", stop);
child.on("exit", (code) => {
  server.close();
  process.exit(code ?? 0);
});
