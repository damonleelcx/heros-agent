// delivery.test.mjs is the console half of P26 wave 26b.
//
// The platform half (internal/adminops/delivery_test.go) proves the read model preserves the three
// merge outcomes and exposes no writer. This file asserts the properties that live only on the screen:
// that the page renders all three outcomes, that none of them is painted with the hazard palette, that
// no control on it acts, and that an empty aggregate is not rendered as `0`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

const ROOT = new URL("..", import.meta.url).pathname;
const read = (rel) => readFile(join(ROOT, rel), "utf8");

test("🔴 3.3 — the delivery surface renders all THREE merge outcomes, and `unknown` as itself", async () => {
  const page = await read("src/app/delivery/page.tsx");
  const types = await read("src/lib/types.ts");

  assert.match(
    types,
    /export type MergeState = "merged" \| "closed_unmerged" \| "unknown";/,
    "MergeState is not three values on the wire",
  );
  for (const [state, label] of [
    ["merged", "merged (observed)"],
    ["closed_unmerged", "closed unmerged"],
    ["unknown", "state unknown"],
  ]) {
    assert.match(page, new RegExp(`${state}:\\s*"${label.replace(/[()]/g, "\\$&")}"`),
      `the page has no label for the ${state} outcome`);
  }
  // The word "observed" has to reach the reader: a merge is observed, never inferred from a pull
  // request closing, and the label is where that distinction is actually made visible.
  assert.match(page, /merged \(observed\)/, "the merged outcome does not say it was observed");
});

test("🔴 3.8 — no control on the delivery surface acts", async () => {
  const page = await read("src/app/delivery/page.tsx");
  // No server action, no form that posts, no ActionForm. The one <form> on the page is the GET-method
  // scope selector, which navigates and performs nothing.
  assert.equal(/ActionForm/.test(page), false, "the delivery page imports the action form");
  assert.equal(/from "@\/lib\/actions"/.test(page), false, "the delivery page imports a server action");
  const forms = [...page.matchAll(/<form([^>]*)>/g)].map((m) => m[1]);
  for (const attrs of forms) {
    assert.match(attrs, /method="get"/, `a form on the delivery page is not a GET: ${attrs}`);
  }
  // Whitespace-tolerant: the sentence is wrapped across JSX lines, and a fence that broke on a
  // reflow is a fence people learn to edit rather than obey.
  assert.match(
    page.replace(/\s+/g, " "),
    /This surface reads and does nothing/,
    "the page does not state its read-only boundary to the reader",
  );
});

test("🔴 3.7 — every aggregate on the delivery surface offers its drill-down", async () => {
  const page = await read("src/app/delivery/page.tsx");
  assert.match(page, /c\.drill_down/, "the counts do not render their drill-down");
  assert.match(page, /view\.counts\.map/, "the counts are not rendered from the platform's own list");
});

test("🔴 7.7 — an empty delivery aggregate renders as 'no records', never as 0", async () => {
  const page = await read("src/app/delivery/page.tsx");
  assert.match(
    page,
    /c\.value === 0 \?/,
    "a zero count is rendered as a figure — a reader takes a zero for a measured value (P26 §7.7)",
  );
  assert.match(page, /no records/, "the zero case does not say 'no records'");
});

test("🔴 7.6 — the delivery surface does not use the hazard palette for volume or novelty", async () => {
  const page = await read("src/app/delivery/page.tsx");
  // `unknown` and `closed_unmerged` are ordinary states of an ordinary pull request. Painting either
  // with --danger would spend the colour the kill switch needs on the most common row on the page.
  assert.equal(
    /tone="danger"/.test(page),
    false,
    "the delivery page uses the danger tone — hazard stays legible only while it stays rare (FR31)",
  );
  assert.equal(/tone="warn"/.test(page), false, "the delivery page uses the warn tone");
});

test("🔴 7.4 — the delivery surface groups its dense subjects with Tabs rather than stacking below the fold", async () => {
  const page = await read("src/app/delivery/page.tsx");
  assert.match(page, /from "@\/components\/tabs"/, "the delivery page does not use the Tabs primitive");
  const ids = [...page.matchAll(/id: "([a-z]+)",\s*\n\s*label: "/g)].map((m) => m[1]);
  assert.ok(ids.length >= 3, `expected the page's subjects to be tabbed, found ${ids.length}`);
});

test("🔴 7.8 — the delivery destination is gated by the same capability the backend enforces", async () => {
  const surfaces = await read("src/lib/surfaces.ts");
  assert.match(surfaces, /capability: "delivery\.read",\s*\n\s*href: "\/delivery"/,
    "the /delivery destination is not registered against delivery.read");
  const page = await read("src/app/delivery/page.tsx");
  assert.match(page, /hasCapability\(identity, "delivery\.read"\)/, "the page does not check the capability");
  assert.match(page, /<DeniedState/, "the page does not render a denial with an escalation path");
});
