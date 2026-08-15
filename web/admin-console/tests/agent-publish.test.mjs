// agent-publish.test.mjs fences the console half of publishing a HEROS definition.
//
// 🔴 The contract this exists for. `DefinitionFromAxes` REFUSES an edit naming an axis the platform
// does not author — deliberately, because "an unknown key silently dropped is a definition published
// without the thing the operator thought they set". That refusal protects the platform and does
// nothing for this console: if the form sends `instruction` where the platform authors `prompt`, every
// publish fails, and if it sends a valid-but-wrong axis the operator publishes something they did not
// compose. Neither is visible from either side alone, so the parity is asserted here, against the Go
// source that defines it.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

const ROOT = new URL("..", import.meta.url).pathname;
const REPO = join(ROOT, "../..");
const read = (rel) => readFile(join(ROOT, rel), "utf8");
const readRepo = (rel) => readFile(join(REPO, rel), "utf8");

/** The axis names the PLATFORM authors, read from its own definition rather than restated here. */
async function authorableAxes() {
  const src = await readRepo("internal/herosagent/definition.go");
  // The constant block: AxisPrompt Axis = "prompt", …
  const consts = new Map();
  for (const m of src.matchAll(/(Axis[A-Za-z]+)\s+Axis\s*=\s*"([a-z_]+)"/g)) consts.set(m[1], m[2]);
  const fn = src.match(/func AuthorableAxes\(\) \[\]Axis \{\s*return \[\]Axis\{([^}]*)\}/);
  assert.ok(fn, "AuthorableAxes() could not be read — this fence is measuring nothing");
  return fn[1]
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
    .map((id) => {
      const v = consts.get(id);
      assert.ok(v, `AuthorableAxes names ${id}, whose value could not be resolved`);
      return v;
    });
}

test("🔴 every axis the form sends is one the platform authors", async () => {
  const authorable = new Set(await authorableAxes());
  const actions = await read("src/lib/actions.ts");
  const block = actions.match(/for \(const axis of \[([^\]]*)\]\)/);
  assert.ok(block, "the publish action's axis list could not be found");
  const sent = block[1].split(",").map((s) => s.trim().replace(/^["']|["']$/g, "")).filter(Boolean);
  assert.ok(sent.length > 0, "the form sends no axes at all");
  for (const axis of sent) {
    assert.ok(
      authorable.has(axis),
      `the form sends the axis "${axis}", which the platform does not author. ` +
        `DefinitionFromAxes refuses the whole edit, so EVERY publish from this console fails. ` +
        `Authorable: ${[...authorable].join(", ")}`,
    );
  }
});

test("🔴 skills and tools are sent as LISTS, not as axes", async () => {
  const actions = await read("src/lib/actions.ts");
  const block = actions.match(/for \(const axis of \[([^\]]*)\]\)/);
  const sent = block[1].split(",").map((s) => s.trim().replace(/^["']|["']$/g, "")).filter(Boolean);
  // They ARE authorable axes, and the wire still carries them separately: agentEditRequest has
  // `skill_refs` and `tool_names` as their own fields, and DefinitionFromAxes takes them as a ListEdit.
  // Putting them in the axes map sends a comma-joined STRING where a list is expected.
  for (const axis of ["skills", "tools"]) {
    assert.equal(
      sent.includes(axis),
      false,
      `"${axis}" is in the axes map. The wire carries it as its own list field; sending it as an axis ` +
        `hands the platform a joined string where a list belongs.`,
    );
  }
  assert.match(actions, /skill_refs:\s*list\("skill_refs"\)/, "skill_refs is not sent as a list");
  assert.match(actions, /tool_names:\s*list\("tool_names"\)/, "tool_names is not sent as a list");
});

test("🔴 the wiring axis is never offered for authoring", async () => {
  const page = await read("src/app/agent/page.tsx");
  // A form field named `wiring` would be refused at publish with ErrWiringOverride — HEROS is a single
  // node, so there is no ordering to author. Offering the field would invite an operator to fill in
  // something that can only ever fail.
  assert.equal(
    /name="wiring"/.test(page),
    false,
    "the publish form offers a `wiring` field, which is refused at publish rather than accepted",
  );
});

test("🔴 publishing is not described as taking effect", async () => {
  const page = await read("src/app/agent/page.tsx");
  const publishTab = page.slice(page.indexOf('id: "publish"'), page.indexOf('id: "rehearsal"'));
  assert.ok(publishTab.length > 0, "the publish tab could not be located");
  // A published definition is PENDING and serves nothing until it passes the gate and is activated.
  // A control that does not say so reads, on an operator console, like it changes what is running.
  assert.match(
    publishTab,
    /pending/i,
    "the publish control never says the version lands pending — an operator would read Publish as " +
      "changing what customers are analysed by",
  );
  assert.match(
    publishTab,
    /activat/i,
    "the publish control does not name activation as the separate act that serves a definition",
  );
});

test("🔴 a no-change publish is reported apart from a real one", async () => {
  const actions = await read("src/lib/actions.ts");
  const fn = actions.slice(actions.indexOf("export async function publishAgentDefinition"));
  assert.match(
    fn,
    /no_change/,
    "the publish action does not branch on no_change. A definition is identified by its content, so " +
      "an edit resolving to an existing one creates NOTHING — reporting that as published leaves the " +
      "operator waiting for a version that was never made.",
  );
  assert.match(
    fn,
    /refusals/,
    "the publish action ignores refusals, which arrive on a 200 — the definition was rejected on its " +
      "content and the operator would be told it was published",
  );
});
