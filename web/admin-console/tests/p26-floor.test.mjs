// p26-floor.test.mjs is the interface floor applied to P26's four new surfaces (§7).
//
// The per-surface tests next door assert what each page MEANS. This file asserts what all four have to
// have in common — the floor the console already had, extended to the pages this phase added, so a
// fifth page cannot arrive below it.
//
// It is written to iterate the PAGE LIST rather than name four files four times: a page added later
// picks the floor up automatically, which is the difference between a floor and four coincidences.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

const ROOT = new URL("..", import.meta.url).pathname;
const read = (rel) => readFile(join(ROOT, rel), "utf8");

/** The surfaces P26 added. Named once; every assertion below iterates them. */
const P26_PAGES = [
  "src/app/delivery/page.tsx",
  "src/app/releases/page.tsx",
  "src/app/axes/page.tsx",
  "src/app/oversight/page.tsx",
];

const P26_DESTINATIONS = ["/delivery", "/releases", "/axes", "/oversight"];

test("7.1 — every new page composes from the closed primitive set and holds no visual literal", async () => {
  for (const file of P26_PAGES) {
    const src = await read(file);
    assert.match(src, /from "@\/components\/primitives"/, `${file} does not compose from the primitive set`);
    // An inline style object is a bespoke layout by another name, and it is also how a colour literal
    // gets past the token scan.
    assert.equal(/style=\{\{/.test(src), false, `${file} uses an inline style object`);
  }
  // The token scan itself runs as its own test in craft.test.mjs; this asserts the new files are IN
  // its scan path, which is the part a new directory could silently miss.
  const scan = await read("scripts/scan-tokens.mjs");
  assert.match(scan, /join\(ROOT, "src"\)/, "the token scan no longer walks the whole src tree");
});

test("7.2 — every new page formats through the single en-US swap point", async () => {
  for (const file of P26_PAGES) {
    const src = await read(file);
    // No page may construct its own formatter: `new Intl.X()` with no locale follows the BROWSER's,
    // so the same timestamp reads differently on two operators' screens mid-incident.
    assert.equal(/new Intl\./.test(src), false, `${file} constructs its own Intl formatter`);
    if (/timestamp\(|quantity\(|count\(|percent\(/.test(src)) {
      assert.match(src, /from "@\/lib\/format"/, `${file} formats without importing the swap point`);
    }
  }
});

test("7.4 — every new page groups its dense subjects with Tabs rather than stacking below the fold", async () => {
  for (const file of P26_PAGES) {
    const src = await read(file);
    assert.match(src, /from "@\/components\/tabs"/, `${file} does not use the Tabs primitive`);
    const panels = [...src.matchAll(/\n\s+id: "[a-z-]+",\n\s+label: "/g)].length;
    assert.ok(panels >= 3, `${file} has ${panels} tab panels — a dense subject was left stacked`);
  }
});

test("7.5 — no charting library arrives for this phase", async () => {
  const pkg = JSON.parse(await read("package.json"));
  const deps = { ...pkg.dependencies, ...pkg.devDependencies };
  for (const name of Object.keys(deps)) {
    assert.equal(
      /chart|d3|recharts|plotly|vega|echarts|visx/i.test(name),
      false,
      `${name} was added as a dependency. The existing chart.tsx primitive is the answer, and the ` +
        `trend ledger's rejections stay rejected (P26 §7.5).`,
    );
  }
  // And the dependency set did not otherwise grow: this phase adds four read-only pages.
  assert.deepEqual(
    Object.keys(pkg.dependencies).sort(),
    ["next", "react", "react-dom"],
    "the console's runtime dependencies changed in a phase that adds four read-only pages",
  );
});

test("7.6 — the hazard palette is not used for volume or novelty on any new page", async () => {
  // Every state on these surfaces is ordinary: a closed pull request, an unchecked artefact, an
  // unconfigured integration, a refused coverage cell. `--danger` appears ONLY where something has
  // genuinely failed, and `--warn` only for a fault or an obligation. A large count never earns either.
  const allowed = {
    "src/app/releases/page.tsx": ['failed: "danger"'], // a failed verification / a failed smoke
    "src/app/oversight/page.tsx": ['degraded: "warn"', 'tone="warn">re-acceptance owed'],
  };
  for (const file of P26_PAGES) {
    const src = await read(file);
    const hazards = [...src.matchAll(/tone=\{?["']?(danger|warn)/g)].map((m) => m[0]);
    const declared = [...src.matchAll(/(?:failed|degraded):\s*"(danger|warn)"/g)].map((m) => m[0]);
    const permitted = allowed[file] ?? [];
    for (const use of [...hazards, ...declared]) {
      const ok = permitted.some((p) => p.includes(use) || use.includes(p.split(">")[0]));
      assert.ok(
        ok,
        `${file} uses the hazard palette in \`${use}\`, which is not one of its declared hazards. ` +
          `Hazard is legible only while it is rare (FR31).`,
      );
    }
  }
});

test("7.7 — three failure classes stay three, and an empty aggregate is never rendered as 0", async () => {
  for (const file of P26_PAGES) {
    const src = await read(file);
    // Subsystem-not-mounted, not-found and transport failure are three messages. Every new page
    // branches on the error KIND rather than collapsing everything into "degraded".
    assert.match(src, /NotMountedState/, `${file} cannot render "this deployment does not carry it"`);
    assert.match(src, /DegradedState/, `${file} cannot render a transport failure`);
    assert.match(src, /failure\.kind/, `${file} does not branch on the error kind`);
  }
  // And the one page with fleet counts states "no records" rather than rendering a measured-looking 0.
  const delivery = await read("src/app/delivery/page.tsx");
  assert.match(delivery, /c\.value === 0 \?/, "a zero count is rendered as a figure");
  assert.match(delivery, /no records/, "the zero case does not say 'no records'");
});

test("7.8 — every new destination is capability-filtered through the ONE map the backend enforces", async () => {
  const surfaces = await read("src/lib/surfaces.ts");
  // The nav and the palette both read surfaces.ts and both filter it by the operator's live
  // capabilities, so a destination the role does not grant is absent from BOTH — never offered and
  // then refused.
  assert.match(surfaces, /export function navFor\(capabilities: string\[\]\)/);
  assert.match(surfaces, /export function commandsFor\(capabilities: string\[\]\)/);
  assert.match(surfaces, /s\.nav && capabilities\.includes\(s\.capability\)/);
  assert.match(surfaces, /capabilities\.includes\(s\.capability\)/);

  const hrefs = [...surfaces.matchAll(/capability: "([^"]+)",\s*\n\s*href: "([^"]+)"/g)];
  for (const destination of P26_DESTINATIONS) {
    const entry = hrefs.find((m) => m[2] === destination);
    assert.ok(entry, `${destination} is not registered in surfaces.ts`);
    assert.ok(entry[1].length > 0, `${destination} is registered with no capability`);
  }
  // And each page checks the SAME capability its registry entry names, so the nav and the page cannot
  // disagree about who may open it.
  for (const [file, destination] of P26_PAGES.map((f, i) => [f, P26_DESTINATIONS[i]])) {
    const src = await read(file);
    const capability = hrefs.find((m) => m[2] === destination)[1];
    assert.match(
      src,
      new RegExp(`hasCapability\\(identity, "${capability.replace(".", "\\.")}"\\)`),
      `${file} does not gate on ${capability}, which is what its registry entry names`,
    );
    assert.match(src, /<DeniedState/, `${file} does not render a denial with an escalation path`);
  }
});

test("7.x — every new surface declares itself read-only on the wire", async () => {
  // The phase's blast radius is bounded by construction: these pages can render a wrong number, which
  // is a bug, and cannot take an action, which would be an incident.
  const types = await read("src/lib/types.ts");
  for (const view of ["DeliveryView", "ReleaseView", "AxisView", "OversightView"]) {
    const block = types.match(new RegExp(`export type ${view} = \\{([^}]*)\\}`));
    assert.ok(block, `${view} is not declared`);
    assert.match(block[1], /read_only: boolean;/, `${view} does not carry the read-only declaration`);
  }
  for (const file of P26_PAGES) {
    const src = await read(file);
    assert.equal(/from "@\/lib\/actions"/.test(src), false, `${file} imports a server action`);
    assert.equal(/ActionForm/.test(src), false, `${file} imports the action form`);
  }
});
