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
};

async function serverAgentRoutes() {
  const src = await readFile(join(REPO, "internal/api/adminconsole.go"), "utf8");
  const found = [...src.matchAll(/POST (\/admin\/api\/agent[a-z/_-]*)/g)].map((m) => m[1]);
  assert.ok(found.length > 0, "no agent routes were read — this fence is measuring nothing");
  return [...new Set(found)];
}

/**
 * code strips comments, so an assertion about what a page DOES cannot be satisfied by prose about it.
 *
 * 🔴 This exists because it happened twice. The activate assertion once anchored on an identifier that
 * also appeared on its import line; then the placement assertion below went green while the editor was
 * mutated to ignore the platform's option set entirely — because the identifier it matched also appears
 * in the function's own doc comment, three lines above the code. A fence that reads documentation is a
 * fence that passes because somebody described the behaviour, which is the one thing a fence is for
 * NOT trusting. Both drills are in the session record; this makes the class impossible rather than
 * fixing the two instances.
 */
function code(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .filter((line) => !line.trim().startsWith("//"))
    .join("\n");
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

// ── The two routes that were listed as gaps, and now are not ────────────────────────────────────
//
// Both were recorded in KNOWN_UNREACHABLE above rather than fixed, on the argument that activation was
// the ask. These assert the controls that replaced those entries — because a gap that is closed and
// unfenced is a gap that reopens the next time somebody restructures a page.

test("🔴 a tenant's analysis placement can be set from the console", async () => {
  const called = await consoleCalledRoutes();
  assert.ok(
    called.has("/admin/api/agent/placement"),
    "nothing in this console sets a placement. Analysis is `disabled` for every tenant by default, so " +
      "with no control here the platform cannot be enabled for ANY customer from the console — and a " +
      "placement set another way carries no audit entry at all.",
  );
  const page = code(await readFile(join(ROOT, "src/app/agent/spend/page.tsx"), "utf8"));
  const idx = page.indexOf("action={setAgentPlacement}");
  assert.ok(idx > 0, "the placement action is never used as a form action on the spend page");

  // 🔴 The options must come from the PLATFORM. A `<select>` with the three values typed into the page
  // is a fourth copy of a closed set, and it fails in the quiet direction: a placement added to
  // `herosagent.Placements` never appears on the surface that exists to set it, and nothing looks
  // broken. This asserts the copy does not exist rather than that the fetch does — the fetch could be
  // present AND a hardcoded fallback beside it.
  for (const literal of ['"platform"', '"customer"', '"disabled"']) {
    assert.equal(
      page.includes(`<option value=${literal}`),
      false,
      `the placement editor hardcodes ${literal} as an option. The set belongs to the Go package that ` +
        `parses it and arrives on AgentSpendView.placements; a copy here goes stale silently.`,
    );
  }
  // Two anchors, both code-shaped: the options are BOUND from the wire, and they are what the editor
  // actually renders. Either one alone passes a page that fetches the set and then ignores it.
  assert.match(
    page,
    /const placements = view\.placements \?\? \[\]/,
    "the placement editor does not bind its options from the platform's placement set",
  );
  assert.match(
    page,
    /placements\.map\(/,
    "the placement editor does not render its options from the platform's set — it fetches them and " +
      "lists something else.",
  );
});

test("🔴 an analysis cap can be set from the console, at both scopes", async () => {
  const called = await consoleCalledRoutes();
  assert.ok(
    called.has("/admin/api/agent/cap"),
    "nothing in this console sets a cap. A cap is checked BEFORE the provider call, so it is the only " +
      "thing that bounds an analysis storm — and it could only be set by calling the API by hand.",
  );
  const page = code(await readFile(join(ROOT, "src/app/agent/spend/page.tsx"), "utf8"));
  assert.equal(
    (page.match(/action=\{setAgentCap\}/g) ?? []).length,
    2,
    "there are not exactly two cap controls. The fleet ceiling and a tenant ceiling are different " +
      "blast radii and must be two controls at two friction tiers — one form with a scope field would " +
      "make them the same control, which is the confusion FR24 exists to prevent.",
  );
  assert.match(
    page,
    /<input type="hidden" name="scope" value="fleet" \/>/,
    "the fleet cap control does not declare its scope explicitly. The platform spells `the fleet` as " +
      "an EMPTY tenant id, so without this a per-tenant form submitted with a blank tenant sets the " +
      "fleet-wide ceiling and reports success.",
  );

  // 🔴 And the action must refuse that blank rather than forward it. This is the assertion that would
  // catch the scope field being read but not enforced.
  const actions = code(await readFile(join(ROOT, "src/lib/actions.ts"), "utf8"));
  const start = actions.indexOf("export async function setAgentCap");
  assert.ok(start > 0, "setAgentCap does not exist");
  const body = actions.slice(start, start + 3000);
  assert.match(
    body,
    /if \(!fleet && !tenantId\)/,
    "setAgentCap does not refuse a per-tenant submission with no tenant — it would fall through to the " +
      "empty tenant id that means FLEET-WIDE.",
  );
  // Zero removes the ceiling and `Number("")` is zero, so a blank field must not reach the platform.
  assert.match(
    body,
    /\/\^\\d\+\$\//,
    "setAgentCap does not parse the token count strictly. `0` removes the cap and an empty field " +
      "coerces to `0`, so a blank submission would remove a ceiling and report success.",
  );
});

/**
 * 🔴 Every command the palette advertises must exist on the page it points at.
 *
 * `surfaces.ts` had named "Set a tenant's analysis placement" at `/agent/spend#placements` since the
 * page was written, and no element on that page carried the id. An operator could find the command by
 * name in the palette, press it, and arrive at a page that rendered placements read-only — which is
 * WORSE than the route simply having no button, because the console had told them it did.
 *
 * The other six anchors resolve. This fence is why that stays true.
 */
test("🔴 every anchor the command palette names exists on the page it points at", async () => {
  const surfaces = await readFile(join(ROOT, "src/lib/surfaces.ts"), "utf8");
  const anchored = [...surfaces.matchAll(/href: "(\/[a-z/-]*)#([a-z-]+)"/g)];
  assert.ok(anchored.length > 0, "no anchored surfaces were read — this fence is measuring nothing");

  for (const [, path, fragment] of anchored) {
    const file = join(ROOT, "src/app", path === "/" ? "" : path.slice(1), "page.tsx");
    const page = code(await readFile(file, "utf8"));
    // Either form of id declaration counts: an element attribute, or a Tabs entry's `id:` — the
    // Instruction/Publish tabs are addressed the same way and are just as real a destination.
    const declared = page.includes(`id="${fragment}"`) || page.includes(`id: "${fragment}"`);
    assert.ok(
      declared,
      `the palette offers "${path}#${fragment}" and nothing on ${path} declares that id. The command ` +
        `is findable by name and lands on a page with no such control — an advertised capability that ` +
        `does not exist, which reads to an operator as the console being broken rather than the ` +
        `feature being absent.`,
    );
  }
});
