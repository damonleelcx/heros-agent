// honesty.test.mjs is the console half of P26's three corrections (tasks 2.2, 2.6, 2.7, 2.8).
//
// The platform half lives in `internal/adminops/honesty_test.go`, where a seeded unverified change is
// read back out of the real aggregate. This file asserts the thing the Go side cannot: that the
// corrections REACH THE SCREEN, in the same view, and that a later change cannot quietly render a
// derived figure without its coverage.
//
// Each test names the requirement it defends. A correction with no assertion has a half-life, and all
// three of these defects survived fourteen phases precisely because nothing asserted them.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir, writeFile } from "node:fs/promises";
import { join } from "node:path";

const ROOT = new URL("..", import.meta.url).pathname;

async function read(rel) {
  return readFile(join(ROOT, rel), "utf8");
}

async function pages() {
  const out = [];
  async function walk(dir) {
    for (const entry of await readdir(join(ROOT, dir), { withFileTypes: true })) {
      const rel = join(dir, entry.name);
      if (entry.isDirectory()) await walk(rel);
      else if (rel.endsWith(".tsx")) out.push(rel);
    }
  }
  await walk("src/app");
  return out;
}

// ── 2.2 · Link coverage beside every SUM-derived figure ──────────────────────

test("🔴 2.2 — a SUM-derived figure reaches the screen ONLY through the component that carries its coverage", async () => {
  // The fence, and the reason it is written this way: the requirement is "coverage beside EVERY
  // SUM-derived figure", and the way that requirement fails is by somebody adding a fourth figure and
  // rendering `{view.something.value}` directly. So the assertion is not "the billing page shows
  // coverage" — it is that no page can read a DerivedFigure's value at all except through <Derived>.
  const types = await read("src/lib/types.ts");
  const derivedFields = [...types.matchAll(/(\w+)\??:\s*DerivedFigure;/g)].map((m) => m[1]);
  assert.ok(
    derivedFields.length > 0,
    "no field is typed DerivedFigure — the pairing type has been removed from the wire contract",
  );

  for (const file of await pages()) {
    const src = await read(file);
    for (const field of derivedFields) {
      const direct = new RegExp(`\\.${field}\\.value`);
      assert.equal(
        direct.test(src),
        false,
        `${file} renders ${field}.value directly. A SUM-derived figure must go through <Derived>, ` +
          `which renders its link coverage in the same view — or withholds the figure when coverage ` +
          `is unknown (P26 §2.2, §2.3).`,
      );
    }
  }
});

test("🔴 2.2 — the billing surface states its coverage and renders both derived figures", async () => {
  const page = await read("src/app/billing/page.tsx");
  assert.match(page, /<CoverageStatement coverage={view\.link_coverage}/, "no coverage statement on the billing surface");
  assert.match(page, /figure={view\.metered_sum}/, "the metered SUM figure is not rendered");
  assert.match(page, /figure={view\.gainshare_savings}/, "the verified-savings figure is not rendered");
  // In the SAME view, not behind a link and not on a detail page: the coverage section must appear
  // before the tables it qualifies.
  const coverageAt = page.indexOf("What these figures count");
  const invoicesAt = page.indexOf('title="Invoices"');
  assert.ok(coverageAt > -1 && invoicesAt > -1, "the sections were renamed — this assertion needs updating");
  assert.ok(
    coverageAt < invoicesAt,
    "the coverage statement is rendered after the figures it qualifies — an operator about to act on " +
      "a figure must see the coverage without navigating or scrolling past it",
  );
});

// ── 2.3 · A figure with unknown coverage is not rendered ─────────────────────

test("🔴 2.3 — the derived component withholds the figure when coverage is unknown, and says so", async () => {
  const src = await read("src/components/derived.tsx");
  assert.match(
    src,
    /figure\.coverage === null/,
    "the derived component does not branch on unknown coverage",
  );
  assert.match(src, /derived--withheld/, "there is no withheld rendering — an unknown-coverage figure would vanish");
  assert.match(src, /link coverage is unknown/i, "the withheld rendering does not say why the figure is absent");
  // The withheld state must not be an emptiness. A slot that simply disappears is indistinguishable
  // from a page that failed to load, which is the distinction the nine states exist for.
  const css = await read("src/app/globals.css");
  assert.match(css, /\.derived--withheld\s*{/, "the withheld state has no rendering of its own in the stylesheet");
});

test("🔴 2.3 — the coverage sits at reading size, not at hint size", async () => {
  // A caveat set at hint scale beside a display-scale figure reads as decoration. This one changes
  // whether an operator issues a credit, so it is set at --text-sm in the page ink rather than
  // --text-xs in the muted ink.
  const css = await read("src/app/globals.css");
  const block = css.match(/\.derived__coverage\s*{([^}]*)}/);
  assert.ok(block, "no .derived__coverage rule");
  assert.match(block[1], /font-size:\s*var\(--text-sm\)/, "the coverage is not set at reading size");
  assert.match(block[1], /color:\s*var\(--text\)/, "the coverage is set in the muted ink — it reads as decoration");
});

// ── 2.6 · The exclusion is stated where the figure appears ───────────────────

test("🔴 2.6 — the cross-tenant surface states the unverified exclusion in the same view as the figures", async () => {
  const page = await read("src/app/crosstenant/page.tsx");
  assert.match(page, /authored_improvement/, "the improvement aggregate is not reachable from the console");
  assert.match(
    page,
    /model\.note/,
    "the aggregate's exclusion note is not rendered — an operator cannot tell a measured improvement " +
      "from an unverified estimate without leaving the page (P26 §2.6)",
  );
});

// ── 2.7 · The audit surface states its merge-path coverage ───────────────────

test("🔴 2.7 — the audit surface states which merge paths the chain covers and which it does not", async () => {
  const page = await read("src/app/audit/page.tsx");
  assert.match(page, /merge_coverage\.statement/, "the merge-coverage statement is not rendered");
  assert.match(page, /merge_coverage\.covered/, "the covered merge paths are not rendered");
  assert.match(
    page,
    /merge_coverage\.not_covered/,
    "the UNCOVERED merge paths are not rendered — which is the whole correction. Before P26 the " +
      "surface implied the chain recorded every merge, while it records the ones the P6 loop performs " +
      "itself (P26 §2.7).",
  );
  // A gap with a destination is a different thing from a gap.
  assert.match(page, /readable_at/, "an uncovered path does not link to where it IS readable");
  // And the page's own lede must not keep claiming what it no longer claims.
  const lede = page.match(/lede="([^"]*)"/);
  assert.ok(lede, "the audit page has no lede");
  assert.match(
    lede[1],
    /AUTONOMOUS merge|does not record every merge/,
    `the audit page's lede still implies it holds every merge: ${lede[1]}`,
  );
});

// ── 8.5 · The derived-figure fence, demonstrated RED ─────────────────────────

test("🔴 8.5 — the derived-figure fence goes RED when a page renders a figure without its coverage", async () => {
  // A fence nobody has seen fail is a fence nobody knows is connected. This commits the exact
  // violation — a page reading `.value` off a DerivedFigure directly, bypassing <Derived> and its
  // coverage — and requires the assertion above to catch it. The file is restored byte-for-byte.
  const path = join(ROOT, "src/app/billing/page.tsx");
  const original = await readFile(path, "utf8");
  const marker = "<CoverageStatement coverage={view.link_coverage} />";
  assert.ok(original.includes(marker), "the billing page changed shape — this demonstration needs updating");
  try {
    await writeFile(path, original.replace(marker, "<p>{view.metered_sum.value}</p>"));

    const types = await read("src/lib/types.ts");
    const derivedFields = [...types.matchAll(/(\w+)\??:\s*DerivedFigure;/g)].map((m) => m[1]);
    const patched = await readFile(path, "utf8");
    const caught = derivedFields.some((f) => new RegExp(`\\.${f}\\.value`).test(patched));
    assert.ok(
      caught,
      "the fence did NOT catch a SUM-derived figure rendered without its coverage. Its whole purpose " +
        "is that a fourth figure cannot be added bare (P26 §2.2).",
    );
  } finally {
    await writeFile(path, original);
  }
});
