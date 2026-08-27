// envelope.test.mjs is the fence `src/app/app/harness/envelope.ts` promised and never had.
//
// # 🔴 The defect this closes, which is the defect the missing test described
//
// `envelope.ts` mirrored `registry.EnvelopeHarness{}.ParamsSchema()` and said, in its own header:
//
//   *"A mirror with no gate is a second source of truth, and its failure is silent: the page keeps
//   rendering, confidently, a contract that has moved. `tests/envelope.test.mjs` reads the engine's own
//   Go source and asserts this file agrees with it, field for field and required-flag for
//   required-flag. That test is the gate, and it is the reason this comment is not just a promise."*
//
// The file did not exist. The mirror was ungated from the day it shipped, nothing went red for the whole
// time, and the paragraph explaining the hazard was the hazard.
//
// P37 moved the nine-field table off the working surface to `/docs/concepts/execution-envelope`
// (block-inventory H3). A transcription that MOVES keeps its gate — otherwise the move would launder an
// ungated mirror into an ungated document, which is the same defect one directory further from anyone
// who would notice.
//
// So this file gates the DOCUMENT against the Go schema. Same claim, same strictness, new location.
//
// # What it deliberately does not check
//
// Whether the document's PROSE about each field is accurate. The field set, the required set and the
// bounds are checkable; "concurrency multiplies a run's peak resource use" is a review responsibility.
// Saying so here is the same discipline `scan-metric.mjs` applies to its own limits.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const repo = join(root, "..", "..");
const read = (rel) => readFile(join(root, rel), "utf8");
const readRepo = (rel) => readFile(join(repo, rel), "utf8");

const SCHEMA = "internal/registry/harness_envelope.go";
const DOC = "content/docs/en/concepts/execution-envelope.md";
const CONSTANTS = "src/app/app/harness/envelope.ts";

/**
 * engineSchema parses the envelope's params schema out of the Go source.
 *
 * The schema is a raw JSON literal in a Go function, so it is extracted and parsed as JSON rather than
 * pattern-matched field by field. A regex over nine fields would pass on a tenth nobody added here.
 */
async function engineSchema() {
  const go = await readRepo(SCHEMA);
  const start = go.indexOf("func (EnvelopeHarness) ParamsSchema()");
  assert.ok(start > 0, "parser drift: ParamsSchema is no longer declared on EnvelopeHarness");
  const open = go.indexOf("`", start);
  const close = go.indexOf("`", open + 1);
  assert.ok(open > 0 && close > open, "parser drift: the schema is no longer a raw string literal");
  return JSON.parse(go.slice(open + 1, close));
}

/**
 * documentedFields parses the document's fenced field listing.
 *
 * The listing is `name  required|optional  where it is enforced`, one per line, inside the ```text fence
 * that follows "The params schema the registry declares".
 */
async function documentedFields() {
  const doc = await read(DOC);
  const anchor = doc.indexOf("The params schema the registry declares");
  assert.ok(anchor > 0, "the document no longer carries the params-schema listing");
  const open = doc.indexOf("```text", anchor);
  const close = doc.indexOf("```", open + 7);
  assert.ok(open > 0 && close > open, "the params-schema listing is no longer a fenced block");

  const fields = new Map();
  for (const line of doc.slice(open + 7, close).split("\n")) {
    const match = /^([a-z_]+)\s+(required|optional)\s+(.+)$/.exec(line.trim());
    if (match) fields.set(match[1], { required: match[2] === "required", enforcedAt: match[3].trim() });
  }
  return fields;
}

test("the documented envelope fields are the engine's, field for field", async () => {
  const schema = await engineSchema();
  const documented = await documentedFields();
  const engine = new Set(Object.keys(schema.properties));

  assert.equal(
    engine.size,
    9,
    `parsed ${engine.size} fields from the schema, expected nine — parser drift, which would make every ` +
      `assertion below pass for the wrong reason`,
  );

  for (const field of engine) {
    assert.ok(
      documented.has(field),
      `the registry accepts "${field}" and the documentation does not list it. An operator setting an ` +
        `envelope has no way to learn the field exists, and a field nobody knows about is a blast-radius ` +
        `bound nobody sets.`,
    );
  }
  for (const field of documented.keys()) {
    assert.ok(
      engine.has(field),
      `the documentation lists "${field}", which the registry has no property for. An envelope declaring ` +
        `it is refused by \`additionalProperties: false\`, so the page documents a field that cannot be set.`,
    );
  }
});

test("the documented required set is the engine's required set, exactly", async () => {
  const schema = await engineSchema();
  const documented = await documentedFields();
  const required = new Set(schema.required);

  // 🔴 The three required fields are each a blast-radius statement, and there is deliberately no default
  // for any of them: an omitted ceiling reads as "unbounded" to a person and has to be read as SOME
  // number by the code. Documenting one of them as optional is how a policy stops being a policy.
  assert.equal(required.size, 3, `the schema requires ${required.size} fields; the document says three`);

  for (const [field, { required: saysRequired }] of documented) {
    assert.equal(
      saysRequired,
      required.has(field),
      `the documentation says "${field}" is ${saysRequired ? "required" : "optional"} and the registry ` +
        `says ${required.has(field) ? "required" : "optional"}`,
    );
  }
});

test("the two ceilings the surface quotes in prose are the engine's own numbers", async () => {
  const schema = await engineSchema();
  const constants = await read(CONSTANTS);

  // The turn ceiling's maximum, quoted on the reading surface and enforced by the registry.
  const turnMax = schema.properties.turn_ceiling.maximum;
  assert.match(
    constants,
    new RegExp(`TURN_CEILING_MAX = ${turnMax}\\b`),
    `the console's turn-ceiling maximum is not the schema's (${turnMax})`,
  );
  const doc = await read(DOC);
  assert.ok(
    doc.includes(`1–${turnMax}`),
    `the document states a turn-ceiling range the schema does not enforce (schema maximum ${turnMax})`,
  );

  // 🔴 The SANDBOX's own concurrency ceiling is a DIFFERENT number from the schema's `concurrency_limit`
  // maximum, and the difference is the point: the schema bounds what a spec may ASK FOR, and the sandbox
  // caps what may actually overlap whatever the spec said. A test that conflated them would license
  // documenting one as the other, which is the misreading the "checked twice" section exists to prevent.
  // 🔴 NOT `if (declared)`. A conditional assertion whose condition is a parse is an assertion that
  // stops asserting the day the parse breaks, silently — the exact shape of the fence this whole file
  // was written to replace.
  const sandbox = await readRepo("internal/sandbox/concurrency.go");
  const declared = /SandboxConcurrencyCeiling\s*=\s*(\d+)/.exec(sandbox);
  assert.ok(declared, "parser drift: the sandbox no longer declares SandboxConcurrencyCeiling");
  assert.match(
    constants,
    new RegExp(`SANDBOX_CONCURRENCY_CEILING = ${declared[1]}\\b`),
    `the console's sandbox ceiling is not the sandbox's own (${declared[1]})`,
  );
  assert.ok(
    doc.includes(`**${declared[1]}**`),
    `the document states a sandbox ceiling the sandbox does not enforce (${declared[1]})`,
  );
});

test("the console holds no copy of the field table any more", async () => {
  const constants = await read(CONSTANTS);
  // The DECLARATION, not the word: the file's header explains what used to be here and why it left, and
  // a fence that fired on the explanation of a rule is a fence somebody deletes.
  assert.ok(
    !/export const ENVELOPE_FIELDS/.test(constants),
    "the field mirror came back into the console. It lives on the reading surface, gated by this file; a " +
      "second copy is a second answer to what an envelope accepts.",
  );
  // What stays is the boundary and the two ceilings — facts about the BUILD that the surface renders.
  assert.match(constants, /ENVELOPE_BOUNDARY/, "the boundary constant is gone, so the surface states no boundary");
  assert.match(
    constants,
    /materializesAnywhere: false/,
    "the boundary no longer says the envelope reaches source nowhere — the one sentence this axis cannot lose",
  );
});

test("the harness surface still says refused-everywhere is not unenforced", async () => {
  // 🔴 Protected text (§6.4 FR15). A reader who took "refused at every call site" to mean "ignored"
  // would be wrong about their own blast radius, and that is the one misreading this axis cannot afford.
  const page = await read("src/app/app/harness/page.tsx");
  assert.match(
    page,
    /That is not the same as unenforced/i,
    "the harness surface lost the sentence that stops `refused` being read as `ignored`",
  );
  assert.match(page, /checked twice/i, "the surface no longer says the concurrency limit is checked twice");
  assert.match(
    page,
    /<CurrentValue/,
    "the surface renders no per-node state, so absence on this axis is invisible rather than drawn",
  );
});
