// harness.test.mjs — P18 QA guards on the harness surface, as FAILING TESTS rather than review notes.
//
// This surface has to do two things no other console surface does, and both are easy to soften into a
// lie the console is uniquely able to tell.
//
//   1. It has to sell the reader something that COSTS MORE. Every other axis changes what one call does;
//      this one changes how many happen, so a heavier scaffold multiplies what the node costs on every
//      case — including the ones that already pass. That multiplier is arithmetic and must be in front of
//      the reader BEFORE they choose, not in a bill afterwards.
//   2. It has to distinguish THREE answers where the sibling surfaces have two: applies, waiting on us,
//      and not-at-a-call-site-in-any-language. Telling a reader to wait for something that is not coming
//      is worse than telling them no.
//
// Plus the one every mirrored table needs: the strategy vocabulary agrees with the engine's, strategy
// for strategy. A mirror with no gate is a second source of truth whose failure is silent.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const repo = join(root, "..", "..");
const read = (rel) => readFile(join(root, rel), "utf8");
const readRepo = (rel) => readFile(join(repo, rel), "utf8");

const PAGE = "src/app/app/harness/page.tsx";
const AUTHORING = "src/app/app/harness/authoring.tsx";
const MIRROR = "src/app/app/harness/strategies.ts";
const LAYOUT = "src/app/app/layout.tsx";
const BUILTINS = "internal/registry/harness_builtins.go";
const ENGINE_REFUSAL = "internal/transform/harnessmaterialize_span.go";
const RUNTIME = "internal/harnessruntime/strategy.go";

/** engineStrategies parses the registry's closed builtin set: the wire name of each strategy. */
function engineStrategies(goSrc) {
  const out = new Set();
  for (const m of goSrc.matchAll(/func \([A-Za-z]+\) Name\(\) string\s*\{\s*return ([^}]+)\}/g)) {
    const v = m[1].trim();
    if (v === "StrategySingleShot") {
      out.add("single-shot");
      continue;
    }
    const q = v.match(/^"([a-z-]+)"$/);
    if (q) out.add(q[1]);
  }
  return out;
}

/** mirrorStrategies parses the console's mirror. */
function mirrorStrategies(tsSrc) {
  const out = new Set();
  for (const m of tsSrc.matchAll(/^\s*strategy:\s*"([a-z-]+)",/gm)) out.add(m[1]);
  return out;
}

test("the strategy vocabulary matches the engine's, strategy for strategy (P18 FR42)", async () => {
  const engine = engineStrategies(await readRepo(BUILTINS));
  const mirror = mirrorStrategies(await read(MIRROR));

  assert.equal(
    engine.size,
    5,
    `parsed ${engine.size} strategies from the registry, expected the closed set of 5 — parser drift, ` +
      `which would make every assertion below pass for the wrong reason`,
  );

  for (const s of engine) {
    assert.ok(
      mirror.has(s),
      `the registry ships "${s}" and the console does not offer it. A strategy the platform knows but ` +
        `the surface hides is a choice a user cannot make and cannot learn exists.`,
    );
  }
  for (const s of mirror) {
    assert.ok(
      engine.has(s),
      `the console offers "${s}" and the registry does not ship it. Offering a strategy that fails at ` +
        `seal teaches a user their configuration exists when it does not.`,
    );
  }
});

test("the turn ceiling the surface warns about is the one the schema enforces", async () => {
  const builtins = await readRepo(BUILTINS);
  const mirror = await read(MIRROR);

  const cap = builtins.match(/MaxTurnsCeiling = (\d+)/);
  assert.ok(cap, "the registry declares no MaxTurnsCeiling; the surface would have nothing to pin against");
  assert.ok(
    mirror.includes(`MAX_TURNS_CEILING = ${cap[1]}`),
    `the console's MAX_TURNS_CEILING does not match the registry's ${cap[1]}. A user warned about a ` +
      `different number than the one enforced has been told a fact that is not one.`,
  );
  // And the schemas really do carry it, so the constant is not decorative.
  assert.ok(
    builtins.includes(`"maximum":${cap[1]}`) || builtins.includes(`"maximum": ${cap[1]}`),
    "the ceiling is not expressed in any params schema's `maximum`, so it is enforced only by a runtime " +
      "check a spec can be authored against and then violate",
  );
});

test("the cost of a heavier scaffold is stated BEFORE the choice, with who decides (FR45, NFR15)", async () => {
  const authoring = await read(AUTHORING);
  const mirror = await read(MIRROR);

  // The multiplier: a fact statable before anything runs.
  assert.ok(
    /multiply/i.test(mirror) && /multiply/i.test(authoring),
    "neither the mirror nor the picker says a heavier scaffold can MULTIPLY what the node costs. That " +
      "is the one thing this axis must say that no sibling has to, and a user who learns it from a bill " +
      "was not told it.",
  );
  // And who decides whether it was worth it, in the same breath.
  assert.ok(
    /verification/i.test(mirror) && /held-out/i.test(mirror),
    "the cost sentence does not name verification on held-out cases as the decider. Stating the cost " +
      "without naming the judge reads as discouragement; naming the judge without the cost reads as a " +
      "recommendation. Both halves, always.",
  );
  // 🚫 And the picker must not recommend.
  for (const forbidden of ["recommended", "you should", "we suggest"]) {
    assert.ok(
      !authoring.toLowerCase().includes(forbidden),
      `the picker says "${forbidden}"; an authoring control states facts and the ranking layer makes ` +
        `recommendations — this is not the ranking layer.`,
    );
  }
});

test("the boundary is PER CELL, and the surface can render three different answers", async () => {
  const authoring = await read(AUTHORING);

  // A language switch exists, because the answer genuinely differs by language.
  assert.ok(
    /LANGUAGES|setLanguage/.test(authoring),
    "the picker offers no way to say which language the reader wrote. A single verdict for the axis is " +
      "wrong in BOTH directions: it tells a Python reader reflexion is unavailable, and tells every " +
      "reader react-loop is merely pending.",
  );
  // Three distinct verdicts, not two.
  for (const label of ["applies", "not at a call site", "waiting on us"]) {
    assert.ok(
      authoring.includes(label),
      `the picker never renders "${label}". "Not yet" and "not ever, here" are different things to tell ` +
        `someone, and collapsing them sends a reader to wait for work that will not happen.`,
    );
  }
  // And the permanent refusal is visually distinct from the pending one.
  assert.ok(
    /permanent \? "halt"|a\.permanent \? "halt"/.test(authoring),
    "a permanent refusal renders in the same state as a pending one; a reader cannot tell which of the " +
      "two they are in from the badge alone",
  );
});

test("the three host-service strategies are refused in EVERY language, and the surface says so", async () => {
  const mirror = await read(MIRROR);
  const engine = await readRepo(ENGINE_REFUSAL);

  for (const s of ["react-loop", "plan-execute", "critic-loop"]) {
    assert.ok(
      mirror.includes(`"${s}"`),
      `the mirror does not name ${s} among the permanently refused strategies`,
    );
    assert.ok(
      engine.includes(`case "${s}":`),
      `the engine's harnessHostService has no branch for ${s}, so the surface's claim that it is ` +
        `refused everywhere would be a claim the engine does not make`,
    );
  }
  assert.ok(
    /permanentlyRefused/.test(mirror),
    "the mirror does not distinguish permanently-refused strategies from pending ones",
  );
});

test("the surface never presents the identity strategy as refused", async () => {
  const authoring = await read(AUTHORING);
  const mirror = await read(MIRROR);
  assert.ok(
    /identity: true/.test(mirror),
    "single-shot is not marked as the identity; it would then be badged like a strategy that can be " +
      "refused, and a user selecting a deliberate no-op would be told it failed",
  );
  assert.ok(
    /s\.identity/.test(authoring),
    "the picker does not branch on the identity, so it cannot render it as always-applicable",
  );
});

test("the control is LIVE, not disabled (FR44)", async () => {
  const authoring = await read(AUTHORING);
  // 🔴 The ATTRIBUTE, not the word: the file's own comment explains why a disabled control would be
  // wrong, and a substring check that flagged the explanation would be a fence that cries wolf.
  assert.ok(
    !/\bdisabled[=:]/.test(authoring),
    "a control on this surface is disabled. A greyed-out control says nothing about WHY, and invites " +
      "the belief that some other strategy, language, or plan would unlock it. The reason is stated " +
      "instead — that is the whole design.",
  );
  // 🔴 Comments stripped first, for the same reason: the file's own doc block says "No Force. No
  // advanced mode." — the explanation of why the escape hatch is absent must not read as the hatch.
  const code = authoring.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
  assert.ok(
    !/Force|ApplyAnyway|advanced mode/i.test(code),
    "the surface offers a way around the refusal. What is missing where a cell refuses is either an " +
      "artifact we owe or an injection point a call site does not have; wanting it does not build it.",
  );
});

test("an authored change claims nothing until the harness has run (FR45)", async () => {
  const authoring = await read(AUTHORING);
  assert.ok(
    /UnverifiedLabel/.test(authoring),
    "the surface does not stamp an authored change as unverified; an unverified change that looks " +
      "settled is one a reader will act on",
  );
  for (const forbidden of ["improved", "gain of", "faster by", "cheaper by"]) {
    assert.ok(
      !authoring.toLowerCase().includes(forbidden),
      `the surface attributes "${forbidden}" to an authored change. No metric improvement may be ` +
        `attributed before verification has run.`,
    );
  }
});

test("the boundary tab keeps the axis distinct from context and memory", async () => {
  const page = await read(PAGE);
  for (const axis of ["Context", "Memory", "Harness"]) {
    assert.ok(page.includes(`axis: "${axis}"`), `the boundary table omits the ${axis} row`);
  }
  assert.ok(
    /AROUND the call/.test(page),
    "the harness row does not say the axis wraps the call; without it a reader has no way to tell it " +
      "from the two rows above",
  );
});

test("bounded autonomy and containment are stated on the surface, not only in the engine", async () => {
  const page = await read(PAGE);
  assert.ok(
    /unbounded loop/i.test(page),
    "the surface never says that no strategy can express an unbounded loop; that is the containment a " +
      "reader is being asked to trust",
  );
  assert.ok(
    /observable/i.test(page),
    "the surface does not say the added turn surface is OBSERVABLE. The honest word is observable, " +
      "never controlled — the added surface is real, and the guarantee is that you can see it.",
  );
  assert.ok(
    /re-invoke|re-invokes/.test(page),
    "the surface does not say the added turns re-invoke the call the author already wrote, which is " +
      "what makes 'no new destination becomes reachable' true rather than asserted",
  );
});

test("the runtime and the surface name the same strategies", async () => {
  const runtime = await readRepo(RUNTIME);
  const mirror = mirrorStrategies(await read(MIRROR));
  for (const s of mirror) {
    assert.ok(
      runtime.includes(`"${s}": {`),
      `the surface offers "${s}" and the runtime defines no loop for it; a user could select a strategy ` +
        `that fails loudly the moment it runs`,
    );
  }
});

test("the harness surface is registered in the shell and reachable", async () => {
  const layout = await read(LAYOUT);
  assert.ok(
    layout.includes('href: "/app/harness"'),
    "the harness surface has no nav entry; a page nothing links to is a page nobody finds",
  );
  assert.ok(
    layout.includes('id: "s:harness"'),
    "the harness surface is absent from the command path, so it cannot be reached by search",
  );
});
