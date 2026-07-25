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
