// agent-controls-reachable.test.mjs answers one question: can an operator PRESS what the platform exposes?
//
// 🔴 Three times in one day this repository shipped a mutating route that nothing could reach:
// `agent/prompt` (a definition could be composed and never published), `agent/publish` (the checklist
// named `/agent#publish` and no such control existed), and `agent/activate` (the gate ran, was tested,
// and had no button). Each was found by a person going to do the thing and discovering there was no
// thing to press.
//
// A route is not a capability until something can invoke it. This fence reads the routes the SERVER
// registers and asserts the console calls each one — so the next unreachable surface fails a test
// instead of a workflow.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

const ROOT = new URL("..", import.meta.url).pathname;
const REPO = join(ROOT, "../..");

/**
 * KNOWN_UNREACHABLE names routes that have no console control TODAY, each with the reason.
 *
 * 🔴 It is asserted to be MINIMAL — a route listed here that HAS gained a control fails the test. That
 * is the whole design: an exemption list nobody is forced to shrink rots into a list of things everyone
 * stopped seeing, and the failure mode of a stale register is that it stops pointing at anything.
 */
const KNOWN_UNREACHABLE = {
  "/admin/api/agent/preview":
    "deliberate — publish returns the same PublishPreview, so a separate preview control would be a " +
    "second way to render one view. Not a gap.",
  "/admin/api/agent/cap":
    "GAP — no control exists. A spend ceiling can only be set by calling the API directly.",
  "/admin/api/agent/placement":
    "GAP — no control exists. The spend page renders placements read-only, so enabling a tenant for " +
    "analysis cannot be done from the console, and a placement set another way carries no audit entry.",
};

async function serverAgentRoutes() {
  const src = await readFile(join(REPO, "internal/api/adminconsole.go"), "utf8");
  const found = [...src.matchAll(/POST (\/admin\/api\/agent[a-z/_-]*)/g)].map((m) => m[1]);
  assert.ok(found.length > 0, "no agent routes were read — this fence is measuring nothing");
  return [...new Set(found)];
}

async function consoleCalledRoutes() {
  const src = await readFile(join(ROOT, "src/lib/actions.ts"), "utf8");
  return new Set([...src.matchAll(/"(\/admin\/api\/agent[a-z/_-]*)"/g)].map((m) => m[1]));
}

test("🔴 every mutating agent route the server exposes can be pressed from the console", async () => {
  const routes = await serverAgentRoutes();
  const called = await consoleCalledRoutes();

  const unreachable = routes.filter((r) => !called.has(r) && !(r in KNOWN_UNREACHABLE));
  assert.deepEqual(
    unreachable,
    [],
    `these routes are registered by the server and invoked by NOTHING in this console:\n` +
      unreachable.map((r) => `  ${r}`).join("\n") +
      `\n\nA route is not a capability until something can press it. Either add the control, or add the ` +
      `route to KNOWN_UNREACHABLE with the reason it has none.`,
  );
});

test("🔴 the exemption list is minimal — nothing listed has quietly gained a control", async () => {
  const called = await consoleCalledRoutes();
  for (const [route, why] of Object.entries(KNOWN_UNREACHABLE)) {
    assert.equal(
      called.has(route),
      false,
      `${route} is listed as unreachable ("${why}") but the console now calls it. Remove it from ` +
        `KNOWN_UNREACHABLE — an exemption nobody is forced to shrink stops pointing at anything, which ` +
        `is how a register rots in the direction that looks safe.`,
    );
  }
});

// The one that had to exist for this session's last step: activation is reachable.
test("🔴 a published definition can be activated from the console", async () => {
  const called = await consoleCalledRoutes();
  assert.ok(
    called.has("/admin/api/agent/activate"),
    "nothing in this console activates a definition. The gate can run, is tested, and has no button — " +
      "so a published definition stays pending for ever and the refusal is indistinguishable from a " +
      "gate that measured it and said no.",
  );
  const page = await readFile(join(ROOT, "src/app/agent/page.tsx"), "utf8");
  assert.match(page, /activateAgentDefinition/, "the activate action is never wired into the agent page");
  // And the control must say that pressing it SPENDS. It runs a provider call per calibration fixture
  // at the moment of the press; a button that does that silently is the surprise on the next invoice.
  // 🔴 The USAGE, not the import. A first draft of this used indexOf("activateAgentDefinition"), which
  // matched the import line at the top of the file and then asserted against the file header — a fence
  // measuring the wrong 1200 characters, and green for the wrong reason had the copy been absent.
  const idx = page.indexOf("action={activateAgentDefinition}");
  assert.ok(idx > 0, "the activate action is imported but never used as a form action");
  const around = page.slice(Math.max(0, idx - 1200), idx + 400);
  assert.match(
    around,
    /spend/i,
    "the activate control does not tell the operator it spends at the press",
  );
});
