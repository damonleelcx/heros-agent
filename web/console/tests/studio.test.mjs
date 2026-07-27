// studio.test.mjs — P10 §6 QA guards on the studio surface, as FAILING TESTS rather than review notes
// (task 6.2, 6.3, 6.5). The no-ranking guarantee is a product guarantee, so it is enforced here: a
// future edit that adds a score, a winner, or a promotion path turns this red.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (rel) => readFile(join(root, rel), "utf8");

const STUDIO = "src/app/app/studio/studio.tsx";

test("studio renders prompt bodies as text, never markup (task 4.11)", async () => {
  const src = await read(STUDIO);
  // Match the JSX prop form, so the word in this file's own doc comment does not trip the guard.
  assert.ok(
    !/dangerouslySetInnerHTML\s*=/.test(src),
    "studio must never render customer prompt content as markup",
  );
});

test("studio carries no ranking artefact and no promotion path (task 6.2)", async () => {
  const src = await read(STUDIO);

  // No JSX that would render a score, rank, winner, confidence interval, or a promotion action. Any of
  // these words may appear ONLY inside a negating disclaimer ("no score, no winner", "nothing here
  // promotes"). Every line that mentions one must also negate it — a bare mention is a ranking artefact.
  const ranked = /\b(score|winner|confidence interval|significance|promote|promotes|promotion)\b/i;
  const negated = /\b(no|not|never|without|unranked|exploratory|nothing)\b/i;
  for (const [i, line] of src.split("\n").entries()) {
    if (ranked.test(line) && !negated.test(line)) {
      assert.fail(`studio.tsx:${i + 1} mentions a ranking/promotion artefact without negating it: ${line.trim()}`);
    }
  }

  // The exploratory label must be present on the surface itself, not only in docs.
  // The exploratory label lives on the studio surface itself. Since the viewport-first redesign the
  // studio's sections are tabs and the label sits in the page header (the lede) rather than a banner
  // inside the prompt-library tab — assert it is present on the page.
  const page = await read("src/app/app/studio/page.tsx");
  assert.ok(
    /exploratory/i.test(page) && /no score, no winner, no promotion path/i.test(page),
    "the exploratory label must be present on the studio surface",
  );
});

test("studio states the runtime-changeable boundary per node (task 4.10)", async () => {
  const src = await read(STUDIO);
  assert.ok(
    /inline apply mode/i.test(src) && /new source change/i.test(src),
    "the studio must state which facts are runtime-changeable and which need a new change",
  );
});

test("bound-mode renders verified and unverified distinguishably (task 10.2)", async () => {
  const src = await read("src/app/app/studio/boundmode.tsx");
  // The two states must not look the same — different tone AND different words.
  assert.ok(/verified\s*\?/i.test(src), "the verified badge must branch on verified state");
  assert.ok(/proven better/i.test(src) && /selected, not proven/i.test(src),
    '"proven better" and "selected, not proven" must be distinct labels');
  // The two states use different tones (ok vs warn), so they never look the same.
  assert.ok(/tone="ok"/.test(src) && /tone="warn"/.test(src),
    "verified and unverified must render with different tones");
  // The degraded resolver state names the failed source.
  assert.ok(/failedSource/.test(src) && /degraded/i.test(src),
    "the degraded resolver state must be rendered, naming the failed source");
});

test("matrix ranks nothing — no score/rank/winner/best-cell (task M6.1)", async () => {
  const src = await read("src/app/app/studio/matrix.tsx");
  // Any ranking word may appear only in a negating line ("nothing ranked", "no winner", "not marked best").
  const ranked = /\b(score|ranked|rank|winner|best)\b/i;
  const negated = /\b(no|not|never|without|nothing|unranked|exploratory)\b/i;
  for (const [i, line] of src.split("\n").entries()) {
    if (ranked.test(line) && !negated.test(line)) {
      assert.fail(`matrix.tsx:${i + 1} ranks a cell: ${line.trim()}`);
    }
  }
  // No best-cell highlight logic keyed on figures: the only cell distinction is "in force".
  assert.ok(/in force/i.test(src), "the only cell distinction is in-force (bound), never best");
  assert.ok(!/dangerouslySetInnerHTML\s*=/.test(src), "matrix must render output as text, never markup");
});

test("matrix marks a bound cell in-force AND unverified, distinct from verified (task M6.2)", async () => {
  const src = await read("src/app/app/studio/matrix.tsx");
  assert.ok(/in force — unverified/i.test(src), '"in force — unverified" — a selection is not a proof');
  // The bind action sends no "verified" claim and the note disclaims proof.
  assert.ok(/not a proof/i.test(src), "binding must state it is not a proof");
});

test("studio nav entry is registered so the surface is reachable (task 4.1)", async () => {
  const layout = await read("src/app/app/layout.tsx");
  assert.ok(layout.includes("/app/studio"), "the studio must be reachable from the nav");
});

test("studio is covered by the entitlement mapping, and honestly (P9 §11b.5)", async () => {
  const src = await read("src/lib/entitlements.ts");
  // §11b.5 — the studio capability must exist in the map so the account view lists it (FR15).
  assert.match(src, /id:\s*"studio"/, "the studio capability is missing from the entitlement map");
  // 🔴 It must map to a feature the platform actually enforces. P10 gates on authentication only, so
  // the honest mapping is `feature: null` — claiming a plan boundary the gate will not honour is the
  // screen-vs-gate disagreement entitlements.ts exists to prevent. Assert the studio row's feature is
  // null, not a plan feature string.
  const row = src.match(/id:\s*"studio"[\s\S]*?\},/);
  assert.ok(row, "could not isolate the studio capability row");
  assert.match(row[0], /feature:\s*null/, "studio must map to feature:null while P10 enforces no entitlement");
  assert.doesNotMatch(
    row[0],
    /feature:\s*"(dashboard|assisted_pr|auto_merge)"/,
    "studio must not claim a platform gate P10 does not enforce",
  );
});
