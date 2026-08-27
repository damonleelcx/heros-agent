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

/**
 * The axis names the PLATFORM authors, read from its own definitions rather than restated here.
 *
 * 🔴 P36 made `AuthorableAxes()` DERIVED rather than a literal list — eight from
 * `variantspec.Dimensions()` plus `graph`, so a dimension added there cannot be silently missing from
 * the agent's vocabulary. That is the right shape and it broke the old parser, which read a literal
 * `return []Axis{…}`. It broke LOUDLY, on an anti-vacuity assertion ("could not be read — this fence is
 * measuring nothing") rather than by returning an empty set and passing, which is the whole reason that
 * assertion is there.
 *
 * So this reads the two sources the Go code reads: variantspec's closed dimension enum, and
 * herosagent's `AxisGraph`.
 */
async function authorableAxes() {
  const spec = await readRepo("internal/variantspec/spec.go");
  const fn = spec.match(/func Dimensions\(\) \[\]Dimension \{\s*return \[\]Dimension\{([^}]*)\}/);
  assert.ok(fn, "variantspec.Dimensions() could not be read — this fence is measuring nothing");
  const consts = new Map();
  for (const m of spec.matchAll(/(Dim[A-Za-z]+)\s+Dimension\s*=\s*"([a-z_]+)"/g)) consts.set(m[1], m[2]);
  const dims = fn[1]
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
    .map((id) => {
      const v = consts.get(id);
      assert.ok(v, `Dimensions() names ${id}, whose value could not be resolved`);
      return v;
    });

  const agent = await readRepo("internal/herosagent/definition.go");
  const graph = agent.match(/AxisGraph\s+Axis\s*=\s*"([a-z_]+)"/);
  assert.ok(graph, "herosagent.AxisGraph could not be read");
  // 🔴 Asserted to be NINE. A count is what catches a dimension quietly disappearing from either
  // source — the parser above would happily return eight and every assertion below would still pass.
  const all = [...dims, graph[1]];
  assert.equal(
    all.length,
    9,
    `the platform authors ${all.length} axes (${all.join(", ")}), and P36 made it nine. A count that ` +
      `drifts means one of the two sources this reads has changed shape.`,
  );
  return all;
}

/** The axes the publish form sends PER NODE, read from the action rather than restated. */
async function perNodeAxesSent() {
  const actions = await read("src/lib/actions.ts");
  const block = actions.match(/const PER_NODE_AXES = \[([^\]]*)\]/);
  assert.ok(block, "the publish action's per-node axis list could not be found");
  return block[1]
    .split(",")
    .map((s) => s.trim().replace(/^["']|["']$/g, ""))
    .filter(Boolean);
}

test("🔴 every axis the form sends is one the platform authors", async () => {
  const authorable = new Set(await authorableAxes());
  const sent = await perNodeAxesSent();
  assert.ok(sent.length > 0, "the form sends no axes at all");
  for (const axis of sent) {
    assert.ok(
      authorable.has(axis),
      `the form sends the axis "${axis}", which the platform does not author. ` +
        `DefinitionFromNodes refuses the whole edit, so EVERY publish from this console fails. ` +
        `Authorable: ${[...authorable].join(", ")}`,
    );
  }
});

test("🔴 P36 — every one of the nine axes has an operator surface", async () => {
  const authorable = await authorableAxes();
  const sent = new Set(await perNodeAxesSent());
  const page = await read("src/app/agent/page.tsx");

  for (const axis of authorable) {
    if (axis === "graph") {
      // Definition-level, so it is not in the per-node list. It reaches the platform as the topology
      // edit, and it is RENDERED in both states.
      assert.match(
        page,
        /name="topology\.order"/,
        "the `graph` axis has no operator surface. It is the ninth axis and it became authorable in " +
          "P36; a console that does not offer it makes the capability unreachable from the one place " +
          "that exists to set it.",
      );
      continue;
    }
    if (axis === "skills" || axis === "tools") {
      // Sent as their own list fields — see the next test for why.
      assert.match(
        page,
        new RegExp(`name=\\{p \\+ "${axis === "skills" ? "skill_refs" : "tool_names"}"\\}`),
        `the ${axis} axis has no per-node field on the publish form`,
      );
      continue;
    }
    assert.ok(
      sent.has(axis),
      `the ${axis} axis is authorable and the form does not send it. An axis with no operator surface ` +
        `is a capability nothing can reach — which is how three routes in this console shipped ` +
        `unpressable.`,
    );
    assert.match(
      page,
      new RegExp(`name=\\{p \\+ "${axis}"\\}`),
      `the ${axis} axis is sent by the action and has no field on the publish form`,
    );
  }
});

test("🔴 P36 — the node dimension is rendered, not collapsed to the first node", async () => {
  const page = await read("src/app/agent/page.tsx");
  // The axes table groups by node rather than listing one node's rows.
  assert.match(
    page,
    /groupByNode\(/,
    "the axes table does not group by node. A console rendering only the first node's axes shows a " +
      "configuration that is not the one running, and nothing on the page says so.",
  );
  // Every node's rows are rendered — `.map` over the groups, not a slice or a find.
  assert.equal(
    /groupByNode\([^)]*\)\.(slice|find|filter)/.test(page),
    false,
    "the axes table truncates or filters the node groups. Collapse, do not omit: a hidden node is " +
      "indistinguishable from one that does not exist.",
  );
  // The publish form offers more than one node.
  const fieldsets = [...page.matchAll(/<NodeFieldset index=\{\d+\}/g)].length;
  assert.ok(
    fieldsets >= 2,
    `the publish form offers ${fieldsets} node fieldset(s). With one, the graph axis is authorable in ` +
      `the API and unreachable from the console.`,
  );
  // And the per-node observability surface exists.
  assert.match(
    page,
    /id: "nodes"/,
    "there is no per-node view. Task 6.4 is `which node produced which inference`, and an aggregate " +
      "over a graph says the agent is slow rather than which node is.",
  );
});

test("🔴 P36 — the graph axis is rendered in BOTH states and its single-node reason survives", async () => {
  const page = await read("src/app/agent/page.tsx");
  // 🔴 Task 6.3 verbatim: "Do not delete the reason; render it conditionally."
  assert.match(
    page,
    /no second node to order it against|second node to order it against/,
    "the single-node reason text is gone. It is still TRUE whenever one node is declared — which is " +
      "still the default — so removing it because multi-node definitions now exist discards a correct " +
      "explanation for the majority of definitions.",
  );
  assert.match(
    page,
    /refused at publish/,
    "the topology fieldset does not say a single-node topology is refused rather than ignored",
  );
});

test("🔴 skills and tools are sent as LISTS, not as axes", async () => {
  const actions = await read("src/lib/actions.ts");
  const sent = await perNodeAxesSent();
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
  assert.match(actions, /skill_refs: skills/, "skill_refs is not sent as a list");
  assert.match(actions, /tool_names: tools/, "tool_names is not sent as a list");
});

test("🔴 the legacy `wiring` spelling is never offered for authoring", async () => {
  const page = await read("src/app/agent/page.tsx");
  // 🔴 The axis is called `graph` now — the product's own noun, shared with `assessment.AxisGraph`,
  // `variantspec`'s graphDim and the improvement-run synonym table. `wiring` is refused BY NAME at the
  // platform with the rename stated, so a form field named `wiring` would invite an operator to fill in
  // something that can only ever fail.
  assert.equal(
    /name="wiring"/.test(page),
    false,
    "the publish form offers a `wiring` field. The axis is `graph`; the old spelling is refused by " +
      "name so the rename actually finishes rather than leaving two entries in the noun dictionary.",
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
