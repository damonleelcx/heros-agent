// viewport.test.mjs — P8 viewport-first guards (A4). The operator console fits one screen: the shell
// owns a fixed viewport height on desktop, the chrome/alarm never scroll away, and a stacked page is
// split into in-page tabs. Source guards, kept RED-able so the fixed-height model cannot regress.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

const ROOT = new URL("..", import.meta.url).pathname;
const read = (rel) => readFile(join(ROOT, rel), "utf8");

test("the operator shell owns a fixed viewport height on desktop", async () => {
  const css = await read("src/app/globals.css");
  // A desktop media query makes the body a fixed-height flex column that clips (no page scroll).
  assert.ok(/@media\s*\(min-width:\s*768px\)/.test(css), "a desktop breakpoint must exist");
  assert.ok(/height:\s*100dvh/.test(css), "the shell must be 100dvh on desktop");
  assert.ok(/overflow:\s*hidden/.test(css), "the shell must not page-scroll on desktop");
  // main must be the bounded region; the page body owns the scroll so the header/chrome stay fixed.
  assert.ok(/\.page__body[\s\S]*overflow-y:\s*auto/.test(css), "the page body must own the scroll");
});

test("the chrome, impersonation banner and alarm never scroll away", async () => {
  const css = await read("src/app/globals.css");
  // These three must be shrink-0 flex items so an incident never scrolls the acting-principal band or
  // the kill-switch alarm out of view.
  assert.ok(/\.chrome,\s*\n?\s*\.impersonation-banner,\s*\n?\s*\.alarm-banner\s*\{[\s\S]*flex:\s*0 0 auto/.test(css),
    "the chrome, impersonation banner and alarm must be fixed (flex: 0 0 auto)");
});

test("a Tabs primitive exists and is a real tablist", async () => {
  const tabs = await read("src/components/tabs.tsx");
  assert.ok(/role="tablist"/.test(tabs) && /role="tab"/.test(tabs) && /role="tabpanel"/.test(tabs),
    "Tabs must be a real ARIA tablist");
  assert.ok(/aria-selected/.test(tabs) && /(ArrowRight|ArrowLeft)/.test(tabs),
    "Tabs must be keyboard operable");
});

test("the tenant detail page uses tabs, not a 6-section stack", async () => {
  const page = await read("src/app/tenants/[id]/page.tsx");
  assert.ok(/<Tabs/.test(page) && /from "@\/components\/tabs"/.test(page),
    "the tenant detail must split its sections into tabs");
  // No control was dropped: the four action forms and both read sections still render inside the tabs.
  for (const kept of ['title="State"', 'title="Lifecycle"', 'title="Quota override"', 'title="Entitlement override"', 'title="Impersonation"']) {
    assert.ok(page.includes(kept), `the redesign must keep ${kept}`);
  }
});

// The scroll region only scrolls if its children refuse to shrink. This is the other half of the
// fixed-height model, and leaving it implicit cost real records: on a 944px viewport the billing page
// rendered "INVOICES — 2 events" above a table showing NEITHER of them.
//
// `.page__body` is a flex column with `overflow-y: auto`. Flex children default to `flex-shrink: 1`,
// so when the page exceeded the viewport the sections COMPRESSED rather than overflowing. The region
// then measured `scrollHeight === clientHeight`, concluded it fitted, and produced no scrollbar; and
// `.section` clips (its rounded corners need `overflow: hidden`), so the squeezed-out rows were
// invisible AND unreachable. No error, no scrollbar, no cue — the worst way for an oversight surface
// to lose the records it exists to show.
test("the page body's children do not shrink, so the bounded scroll region actually scrolls", async () => {
  const css = await read("src/app/globals.css");
  assert.match(
    css,
    /\.page__body\s*>\s*\*\s*\{[^}]*flex-shrink:\s*0/,
    "`.page__body > *` must set flex-shrink: 0 — without it the sections compress to fit, the scroll " +
      "region never reports overflow, and content past the fold is clipped away silently",
  );
});
