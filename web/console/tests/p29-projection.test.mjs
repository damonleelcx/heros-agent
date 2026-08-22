import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";

// p29-projection.test.mjs is P29 §5.10, §5.11 and §5.12.

const ROOT = path.join(import.meta.dirname, "..");
const read = (p) => readFileSync(path.join(ROOT, p), "utf8");

// The surfaces the projection panel must appear on.
// 🔴 EIGHT since P34 split the harness axis: `/app/loop` is a new surface and needs the panel for the
// same reason every other axis does — its table is a total build fact and says nothing about the
// reader's own nodes.
const AXIS_SURFACES = [
  "src/app/app/graph/page.tsx",
  "src/app/app/context/page.tsx",
  "src/app/app/memory/page.tsx",
  "src/app/app/harness/page.tsx",
  "src/app/app/loop/page.tsx",
  "src/app/app/coverage/page.tsx",
  "src/app/app/authoring/page.tsx",
  "src/app/app/delivery/page.tsx",
];

test("§5.10 every axis surface carries the projection panel", () => {
  for (const page of AXIS_SURFACES) {
    const src = read(page);
    assert.match(
      src,
      /<AxisProjectionPanel\b/,
      `${page} has no projection panel. Its table is a TOTAL BUILD FACT and it is correct — and it ` +
        `says nothing about the reader's nodes, which is the defect this phase exists to close.`,
    );
    assert.match(src, /loadProjection|loadDeliveryProjection/, `${page} does not read a projection`);
  }
});

// 🔴 §5.10 / D7 — THE WORKED EXAMPLES STAY.
//
// It is tempting to replace them now that the pages have real rows, and it would be wrong: a reader
// meeting a refusal for the first time needs the APPLIED case beside it to read "declined" as a boundary
// rather than as a failure. That is the stated reason those pages exist. 「UI 改版不得丢失既有功能」.
test("§5.10 the worked examples are still there beside the projection", () => {
  // Each page's example is named by what it ACTUALLY is, not by one component every page happens to
  // share — two of these carry their vocabulary as data (`STRATEGIES`, `HARNESS_BOUNDARY`) rather than
  // through the shared refusal card, and a fence that only knew about the card would have "passed" on
  // those two by never looking.
  const withExamples = {
    "src/app/app/graph/page.tsx": /AxisRefusal|AxisApplied/,
    "src/app/app/context/page.tsx": /AxisRefusal|AxisApplied/,
    "src/app/app/memory/page.tsx": /STRATEGIES|BOUNDARY/,
    // P34: the strategy vocabulary moved to `/app/loop` with the axis. `/app/harness` carries the
    // ENVELOPE's vocabulary instead — same discipline, different data.
    "src/app/app/loop/page.tsx": /HARNESS_STRATEGIES|HARNESS_BOUNDARY/,
    "src/app/app/harness/page.tsx": /ENVELOPE_FIELDS|ENVELOPE_BOUNDARY/,
  };
  for (const [page, pattern] of Object.entries(withExamples)) {
    assert.match(
      read(page),
      pattern,
      `${page} lost its worked example. The projection is added BESIDE it, never instead of it: a ` +
        `refusal with no applied case next to it reads as a failure rather than as a boundary.`,
    );
  }
});

test("§5.10 the panel carries its own heading, its own denominator, and says it is live", () => {
  const panel = read("src/components/axisProjection.tsx");
  assert.match(panel, /title="Your nodes, crossed with this table"/, "the panel has no heading of its own");
  assert.match(panel, /aside="live data for this organization"/, "the panel does not say it is live data");
  assert.match(
    panel,
    /\{value\}[\s\S]{0,200}\/ \{of\}/,
    "the panel renders a count without its denominator. A proportion with no count behind it is a " +
      "number the reader cannot check — 68% over three nodes and over four hundred are the same string.",
  );
});

// 🔴 §5.11 — THREE TREATMENTS, and no 404 is mapped to a business state.
test("§5.11 not-reported, refused and a transport failure are three different treatments", () => {
  const panel = read("src/components/axisProjection.tsx");
  for (const state of ["not-mounted", "read-failed", "not-reported"]) {
    assert.ok(
      panel.includes(`outcome.state === "${state}"`),
      `the panel has no branch for ${state}`,
    );
  }
  // The read-failure copy must distinguish itself from "you sent none".
  assert.match(
    panel,
    /not<\/strong> the same as having sent\s*\n?\s*none/,
    "the read-failure copy does not distinguish itself from having reported nothing",
  );
  // `not-reported` must name the next action.
  assert.match(panel, /heros link --with-ir/, "the not-reported state names no next action");

  const server = read("../../internal/api/axisprojection.go");
  assert.match(
    server,
    /writeJSON\(w, http\.StatusOK, map\[string\]any\{\s*\n\s*"state":\s*"not-reported"/,
    "the platform answers a not-reported workflow with something other than 200. A 404 here is " +
      "indistinguishable from a transport failure and would send the reader to look for a broken " +
      "deployment when the truth is that they have not opted in.",
  );
});

// 🔴 §5.12 — the fourth state gets a TOKEN, not an improvised colour.
test("§5.12 not-reported has its own design token, distinct from unknown", () => {
  const tokens = read("src/app/tokens.customer.css");
  assert.match(tokens, /--not-reported:/, "there is no --not-reported token");
  assert.match(tokens, /--color-not-reported: var\(--not-reported\)/, "the token is not exposed as a colour");

  // It must not simply alias --unknown. They are different facts: "we could not determine this" and
  // "you did not tell us" — one is an outage, the other is a boundary the customer chose.
  assert.doesNotMatch(
    tokens,
    /--not-reported:\s*var\(--unknown\)/,
    "--not-reported aliases --unknown. Two concepts that happen to share a value are still two " +
      "concepts, and showing an egress decision in an outage's colour is the wrong sentence.",
  );

  const panel = read("src/components/axisProjection.tsx");
  assert.match(
    panel,
    /var\(--color-not-reported\)/,
    "the panel does not use the token for the fourth state",
  );
  assert.doesNotMatch(
    panel,
    /#[0-9a-fA-F]{3,8}\b/,
    "the panel carries a colour literal. The token scan enforces this too; asserting it here names " +
      "the fourth state specifically, which is the one somebody would improvise.",
  );
});

// The projection never renders a verdict the platform derived: the console shows the CAUSE identifier's
// own copy, from its own catalogue, and never a sentence that crossed the wire.
test("§5.3 the refusal sentence comes from the console's catalogue, not from the wire", () => {
  const panel = read("src/components/axisProjection.tsx");
  assert.match(panel, /CAUSE_COPY/, "there is no cause catalogue");
  assert.match(
    panel,
    /CAUSE_COPY\[cell\.cause\] \?\? cell\.cause/,
    "the panel does not render its own copy keyed by the stable identifier",
  );
  for (const cause of [
    "not-expressible-at-a-call-site",
    "call-site-cannot-carry-it",
    "no-materializer-for-this-language",
  ]) {
    assert.ok(panel.includes(cause), `the catalogue has no entry for ${cause}`);
  }
});
