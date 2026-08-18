// viewport.test.mjs — P9 NFR17 viewport-first guards, as FAILING TESTS (task N4.1/N4.3). The
// browser-measured "no page scroll" acceptance (N4.2) is the automated half done in Chrome; these
// source guards keep the fixed-height model and the studio's tabs from regressing.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (rel) => readFile(join(root, rel), "utf8");

test("the app shell owns a fixed viewport height on desktop (NFR17)", async () => {
  const layout = await read("src/app/app/layout.tsx");
  // The shell must clip to the viewport on desktop, not grow with content.
  assert.ok(/md:h-dvh/.test(layout), "the shell must be h-dvh on desktop");
  assert.ok(/md:overflow-hidden/.test(layout), "the shell must not page-scroll on desktop");
  // main must be a bounded region so a page lays out inside it, not past it.
  assert.ok(/main[^>]*min-h-0[^>]*overflow-hidden|main[^>]*overflow-hidden[^>]*min-h-0/s.test(layout) ||
    /id="main"/.test(layout) && /md:overflow-hidden/.test(layout),
    "main must be a bounded region on desktop");
});

test("PageFrame is a viewport-fitting frame that owns its scroll, not the page", async () => {
  const prim = await read("src/components/primitives.tsx");
  // h-full so it fills the main region; overflow-y-auto body so the body scrolls, never the document.
  assert.ok(/h-full[^`]*flex-col/.test(prim), "PageFrame must fill the main region (h-full)");
  assert.ok(/overflow-y-auto/.test(prim), "PageFrame body must own the scroll (overflow-y-auto)");
});

test("a Tabs primitive exists and is a real tablist (NFR7)", async () => {
  const tabs = await read("src/components/tabs.tsx");
  assert.ok(/role="tablist"/.test(tabs) && /role="tab"/.test(tabs) && /role="tabpanel"/.test(tabs),
    "Tabs must be a real ARIA tablist");
  assert.ok(/aria-selected/.test(tabs) && /ArrowRight|ArrowLeft/.test(tabs),
    "Tabs must be keyboard operable with aria-selected");
});

test("the studio splits its sections into tabs with the matrix as the landing tab (N4.3)", async () => {
  const page = await read("src/app/app/studio/page.tsx");
  assert.ok(/<Tabs/.test(page), "the studio must use Tabs, not a vertical stack");
  // Matrix must be the first (landing) tab; all three sections must be present (no feature dropped).
  const order = ["matrix", "library", "bound"];
  const idx = order.map((id) => page.indexOf(`id: "${id}"`));
  assert.ok(idx.every((i) => i >= 0), "all three studio sections must remain as tabs");
  assert.ok(idx[0] < idx[1] && idx[1] < idx[2], "Matrix must be the landing tab");
});

// The proposal surfaces are the deepest pages in the console — a full source diff plus its evidence — so
// they are exactly the pages the fixed-height shell cannot rescue with a body scroll. These guard the
// split, and specifically the two things that must NOT move into a tab.
test("the proposal detail page splits its review into tabs, with the verdict OUTSIDE them", async () => {
  const page = await read("src/app/app/workflows/[workflowId]/proposals/[proposalId]/page.tsx");
  assert.ok(/<Tabs/.test(page), "the proposal detail page must use Tabs, not a vertical stack");
  assert.ok(/decisionTab\(/.test(page), "Decision must be a tab rather than a section below the fold");

  // 🔴 The refusal and the withheld banner must render BEFORE the tabs. A verdict a reader has to select
  // a tab to discover is a verdict that can be missed, and both of these say the change did not happen.
  const refusalAt = page.indexOf("<RefusalNotice");
  const bannerAt = page.indexOf("This proposal was withheld");
  const tabsAt = page.indexOf("<Tabs");
  assert.ok(refusalAt > -1 && refusalAt < tabsAt, "the refusal must render above the tabs, not inside one");
  assert.ok(bannerAt > -1 && bannerAt < tabsAt, "the withheld banner must render above the tabs");
});

test("the proposals list splits recommended from withheld into tabs, and keeps both counts visible", async () => {
  const page = await read("src/app/app/workflows/[workflowId]/proposals/page.tsx");
  assert.ok(/<Tabs/.test(page), "the proposals list must use Tabs, not two stacked grids");
  for (const id of ['id: "recommended"', 'id: "withheld"']) {
    assert.ok(page.includes(id), `both groups must remain as tabs (${id})`);
  }
  // The counts live in "This surface", above the tabs: a group nobody selects must still be countable.
  const countsAt = page.indexOf("withheld</Chip>");
  assert.ok(countsAt > -1 && countsAt < page.indexOf("<Tabs"),
    "the recommended/withheld counts must stay above the tabs so neither group can be missed");
});

test("a review presentation exposes its sections as tabs rather than rendering a stack", async () => {
  for (const [file, fn] of [
    ["src/components/skillsToolsOptimization.tsx", "skillsToolsReviewTabs"],
    ["src/components/promptModelOptimization.tsx", "promptModelReviewTabs"],
  ]) {
    const src = await read(file);
    assert.ok(src.includes(`export function ${fn}(`),
      `${file} must expose ${fn} so the page composes tabs instead of receiving a stack`);
    assert.ok(/TabItem/.test(src), `${file} must return TabItem[]`);
  }
});

// A tablist inside a tablist is two roving tab-stops fighting over the arrow keys. The preview pages
// pick a FIXTURE (navigation, a shareable URL) and tab its SECTIONS — never both with tabs.
test("no page nests one tablist inside another", async () => {
  for (const file of ["src/app/preview/prompt-model/page.tsx", "src/app/preview/skills-tools/page.tsx"]) {
    const src = await read(file);
    // Comments are stripped first: these files EXPLAIN why the picker is not a tablist, and prose that
    // names <Tabs> is not a second tablist. A guard that counted the explanation would punish the comment.
    const code = src.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
    const tabsUses = code.match(/<Tabs\b/g) ?? [];
    assert.equal(tabsUses.length, 1, `${file} must render exactly one Tabs, got ${tabsUses.length}`);
    assert.ok(/aria-label="Preview fixture"/.test(src),
      `${file} must pick its fixture with links, not a second tablist`);
  }
});
