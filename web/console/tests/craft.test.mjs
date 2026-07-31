// craft.test.mjs holds §5b.8 — the assertions behind R16–R20 (FR36–FR39).
//
// These come out of a trend review (web/design-system/trend-ledger.md) whose honest conclusion was
// that this console's problem was never a shortage of fashionable technique. It was an INVERTED
// HIERARCHY: measured values rendered at the smallest type size on the page while the frames that
// introduced them rendered larger. That is measurable, so it is tested rather than reviewed.
//
// The house rule applies here as everywhere: a guard nobody has seen fail is a guard nobody knows is
// connected. Where a scan is asserted, it is made to go red on purpose first.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, writeFile, mkdtemp, cp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";

const exec = promisify(execFile);
const ROOT = new URL("..", import.meta.url).pathname;
const read = (rel) => readFile(join(ROOT, rel), "utf8");

/** rem parses a `--token: 1.75rem;` declaration to a number, so sizes can be COMPARED, not eyeballed. */
function rem(css, token) {
  const match = css.match(new RegExp(`${token}\\s*:\\s*([0-9.]+)rem`));
  return match ? Number(match[1]) : null;
}

/** declared returns the custom-property names a CSS rule block assigns, in source order. */
function declared(css, selector) {
  const start = css.indexOf(selector);
  if (start < 0) return null;
  const open = css.indexOf("{", start);
  const close = css.indexOf("\n}", open);
  const body = css.slice(open, close);
  return [...body.matchAll(/^\s*(--[a-z0-9-]+)\s*:/gim)].map((m) => m[1]);
}

// ── R16 / FR36 · The measured value outranks its frame ───────────────────────

test("the stat scale outranks every frame size it can appear inside", async () => {
  // This is R16 expressed as arithmetic. The defect it replaces was real and measured in a browser:
  // `40 nodes` set at label scale under a section heading reading "This graph".
  //
  // The ramp is named by ROLE rather than by size for exactly this reason — `text-4xl` beside
  // `text-lg` is only an ordering if you already know the scale, and a rebuild that swapped the scale
  // would leave the names true and the ordering silently inverted.
  const tokens = await read("src/app/tokens.customer.css");

  const stat = rem(tokens, "--type-stat");
  const statDense = rem(tokens, "--type-stat-sm");
  const sectionTitle = rem(tokens, "--type-section");
  const pageTitle = rem(tokens, "--type-page");
  const label = rem(tokens, "--type-label");

  assert.ok(stat, "--type-stat must exist");
  assert.ok(statDense, "--type-stat-sm must exist");

  assert.ok(stat > sectionTitle, `a stat (${stat}rem) must outrank a section title (${sectionTitle}rem)`);
  assert.ok(stat > label, `a stat (${stat}rem) must outrank its own label (${label}rem)`);
  assert.ok(
    statDense > sectionTitle,
    `even the dense stat (${statDense}rem) must outrank a section title (${sectionTitle}rem)`,
  );

  // The page title is the ONE thing allowed to sit at or above a stat: it names the subject, and R13
  // requires exactly one display-level heading per view. A stat is not competing with it.
  assert.ok(pageTitle >= sectionTitle, "the page title must still outrank a section title");
});

test("Stat renders the value at stat scale with its label subordinate, and states the unit once", async () => {
  const primitives = await read("src/components/primitives.tsx");
  const css = await read("src/app/globals.css");

  assert.match(primitives, /export function Stat\(/, "the Stat primitive must exist");
  assert.match(css, /\.stat__value\s*\{[^}]*font-size:\s*var\(--type-stat\)/s);
  assert.match(css, /\.stat__label\s*\{[^}]*font-size:\s*var\(--type-label\)/s);

  // `unit` is a PROP, not something a caller concatenates into the value. Concatenation is how
  // `1.2s`, `1.2 s` and `1.2 sec` end up on one screen.
  assert.match(primitives, /unit\?:\s*string/, "unit must be a prop so it is stated once, by the primitive");
});

test("\u{1F534} a stat is governed by the confidence reservation, at display scale", async () => {
  // The reservation matters MORE here than in a table cell. Size reads as certainty, so a provisional
  // number set at 2.25rem asserts more than the same number set at 0.875rem. A Stat that skipped
  // `emphasis()` would be a hole in R14 exactly where it is most expensive.
  const primitives = await read("src/components/primitives.tsx");
  const stat = primitives.slice(primitives.indexOf("export function Stat("));

  assert.match(stat, /emphasis\(flags\)/, "Stat must resolve its emphasis from the server's flags");
  assert.match(stat, /qualifiersOf\(flags\)/, "Stat must render the qualifiers the server attached");
  assert.match(stat, /QUALIFIER_COPY\[q\]/, "a qualifier must render its meaning beside the value");

  // Never hand-written. `className={row.tie ? … : "confident"}` at a dozen call sites is a rule with a
  // dozen chances to be got wrong, and the twelfth is written under deadline.
  assert.doesNotMatch(stat, /"confident"/, "Stat must not write the confident class by hand");
});

test("a section heading is subordinate to both the subject and the values it frames", async () => {
  // The rhythm assertion this replaces was written against a card-based section that no longer exists.
  // What it was protecting is unchanged and is stated directly: a heading is a signpost, and a signpost
  // set larger than the thing it points at is the inverted hierarchy R16 exists to prevent.
  const css = await read("src/app/globals.css");
  const primitives = await read("src/components/primitives.tsx");

  assert.match(css, /\.section__title\s*\{[^}]*font-size:\s*var\(--type-section\)/s);
  assert.match(css, /\.page__title\s*\{[^}]*font-size:\s*var\(--type-page\)/s);

  // And the roles are applied by the primitives rather than chosen per page, so a view cannot promote
  // its own section heading past the subject.
  assert.match(primitives, /className="page__title/, "PageFrame must carry the page role");
  assert.match(primitives, /className="section__title/, "Section must carry the section role");
});

test("a form field is measured to its content, and a one-form page to the form", async () => {
  // The sign-in credential field rendered 1440px wide for a token of about forty characters.
  const css = await read("src/app/globals.css");
  const tokens = await read("src/app/tokens.customer.css");
  const signin = await read("src/app/signin/page.tsx");

  assert.match(tokens, /--measure-field:/, "a field measure must be a token, not a per-page decision");
  assert.match(tokens, /--measure-form:/);
  assert.match(css, /\.field\s*\{[^}]*max-inline-size:\s*var\(--measure-field\)/s);
  assert.match(
    css,
    /\.field--wide\s*\{[^}]*max-inline-size:\s*none/s,
    "an opt-out must exist for genuinely long values",
  );
  assert.match(signin, /var\(--measure-form\)/, "the one-form page must be measured to its form");
});

test("the shell applies the page frame exactly once", async () => {
  // `<main className="page">` around a page that renders its own PageFrame applied the frame twice:
  // doubled top and bottom padding on every view under /app.
  const layout = await read("src/app/app/layout.tsx");
  assert.doesNotMatch(layout, /<main className="page"/, "main must not carry the frame the PageFrame owns");
});

test("a state body separates its paragraphs", async () => {
  // `p { margin: 0 }` in the base layer removes the user agent's paragraph spacing so the design's own
  // rhythm is the only one in play. Nothing put it back, so two paragraphs in one state collided —
  // visible on the Overview's empty state. Every flow container now spaces itself, deliberately.
  const css = await read("src/app/globals.css");
  const body = css.match(/\.state__body\s*\{[\s\S]*?\n  \}/)?.[0] ?? "";
  assert.ok(body, "there is no .state__body rule");
  assert.match(body, /gap/, "the state body must space its own flow");
  assert.match(body, /flex-col/, "the state body must be a flow container for the gap to apply");
});

// ── R17 / FR37 · Theme is chosen, not assumed ────────────────────────────────

test("the theme is resolved server-side and lands in the first paint", async () => {
  const layout = await read("src/app/layout.tsx");
  assert.match(layout, /data-theme=\{theme\}/, "the root element must carry the resolved theme");
  assert.match(layout, /await readTheme\(\)/, "the theme must be read on the server, before the first byte");

  // A blocking inline script in <head> is the usual way to do this and is not available here: the CSP
  // has no unsafe-inline, and buying a theme toggle with a CSP relaxation is a bad trade.
  const globals = await read("src/app/globals.css");
  assert.doesNotMatch(globals, /^html\s*\{[^}]*color-scheme:\s*dark/ms, "the console must not force one scheme");
});

/**
 * themedBlocks returns the three palette blocks by name.
 *
 * The console's theme is a server-rendered attribute rather than a class toggled after hydration
 * (R17/FR37), so the same palette is keyed three ways: the default, an explicit dark choice, and the
 * OS-following case. Reading them by name is what lets the tests below compare them to each other
 * rather than trusting that somebody kept them in step.
 *
 * Comments are stripped first. The token file documents its own selectors in prose, and a parser that
 * reads those finds the wrong block and reports a token missing that is present — the most misleading
 * failure a token test can produce.
 */
function themedBlocks(source) {
  const css = source.replace(/\/\*[\s\S]*?\*\//g, "");
  const at = (selector) => {
    const start = css.indexOf(selector);
    if (start < 0) return null;
    const open = css.indexOf("{", start);
    let depth = 0;
    for (let i = open; i < css.length; i += 1) {
      if (css[i] === "{") depth += 1;
      else if (css[i] === "}") {
        depth -= 1;
        if (depth === 0) return css.slice(open, i);
      }
    }
    return null;
  };
  return {
    light: at(':root,\n:root[data-theme="light"]'),
    dark: at(':root[data-theme="dark"]'),
    system: at(':root[data-theme="system"]'),
  };
}

/** names lists the custom properties a block assigns, in source order. */
function names(block) {
  return [...block.matchAll(/^\s*(--[a-z0-9-]+)\s*:/gim)].map((m) => m[1]);
}

test("🔴 the three theme mapping blocks carry exactly the same tokens", async () => {
  // A token assigned in one block and forgotten in another is invisible until somebody switches
  // theme — and then a control renders unstyled in one theme only. This is the check that makes "every
  // theme carries the same token set" a property rather than a habit.
  //
  // It is also 🔴 "no information is carried by a hue that exists in only one theme", which used to be
  // a second test against a second file: with one identity layer there is one place to assert it.
  const identity = await read("src/app/tokens.customer.css");
  const blocks = themedBlocks(identity);

  for (const [name, block] of Object.entries(blocks)) {
    assert.ok(block, `the ${name} palette block is missing`);
  }

  const light = names(blocks.light);
  assert.ok(light.length > 25, "the palette is expected to cover the whole themed surface");

  for (const other of ["dark", "system"]) {
    assert.deepEqual(
      [...names(blocks[other])].sort(),
      [...light].sort(),
      `the ${other} block does not assign the same token names as the light block`,
    );
  }
});

test("the customer identity carries both palettes, and they are genuinely different", async () => {
  const identity = await read("src/app/tokens.customer.css");
  const blocks = themedBlocks(identity);

  // Each themed block declares its own `color-scheme`, beside the palette it belongs to. Forcing one
  // on the root while the tokens switch is a disagreement no test can see until somebody opens the
  // console on the other OS — which is how this console shipped a light-mode contradiction once.
  assert.match(blocks.light, /color-scheme:\s*light/);
  assert.match(blocks.dark, /color-scheme:\s*dark/);
  assert.match(blocks.system, /color-scheme:\s*dark/);

  // And the two palettes are not the same palette with a different name on it.
  const value = (block, token) => block.match(new RegExp(`${token}:\\s*([^;]+);`))?.[1]?.trim();
  for (const token of ["--background", "--foreground", "--primary", "--ok", "--bad"]) {
    assert.notEqual(
      value(blocks.light, token),
      value(blocks.dark, token),
      `${token} is identical in both palettes — one of them is not doing its job`,
    );
  }

  // The dark and system blocks ARE the same palette, deliberately: "follow the OS, and the OS is dark"
  // must render identically to "I chose dark". A drift between them is a bug nobody would look for.
  for (const token of ["--background", "--foreground", "--primary", "--ok", "--bad"]) {
    assert.equal(
      value(blocks.dark, token),
      value(blocks.system, token),
      `${token} differs between the explicit dark palette and the OS-following one`,
    );
  }
});

test("the theme control is a form, and returns the reader to the page they were on", async () => {
  const control = await read("src/components/themeControl.tsx");
  const route = await read("src/app/api/theme/route.ts");

  assert.match(control, /method="post"/, "a client toggle cannot make the FIRST paint correct");
  assert.match(control, /aria-pressed=/, "the active option must be carried by a word, not only by colour");

  // The defect a browser found: this console sends `Referrer-Policy: no-referrer`, so a Referer-based
  // redirect silently returned the reader to `/` while responding a perfectly correct 303.
  assert.match(control, /x-pathname/, "the return path must come from the middleware header");
  assert.doesNotMatch(route, /headers\.get\("referer"\)/, "Referer is never sent by this console");
  assert.match(route, /samePath\(/, "a form field is client-supplied wherever the server put it");
});

test("the middleware publishes the current path for server components", async () => {
  const middleware = await read("src/middleware.ts");
  assert.match(middleware, /requestHeaders\.set\("x-pathname"/);
});

// ── R18 / FR38 · The payload has a ceiling ───────────────────────────────────

test("the bundle scan enforces a stated payload ceiling and reports its headroom", async (t) => {
  const scan = await read("scripts/scan-bundle.mjs");
  assert.match(scan, /PAYLOAD_CEILING_BYTES\s*=\s*[\d_]+/, "the ceiling must be a stated number");
  assert.match(scan, /DECORATIVE_RUNTIMES/, "the rejected trends must stay rejected mechanically");

  // The measurement is taken from the build's own manifests rather than from the directory, because
  // `next dev` writes ~7.6MB of unminified chunks into the same place. A weight measured over the
  // directory is whichever server ran last.
  assert.match(scan, /shippedFiles\(\)/, "the measurement must come from what the build says ships");

  // A tree a dev server has touched cannot be weighed, and the scan says so rather than reporting a
  // number that describes the development bundle. That refusal IS the behaviour under test here:
  // these tests run against a production build, so a contaminated tree means the harness is wrong.
  assert.match(scan, /contaminated\(\)/, "the scan must refuse a dev-written tree rather than misreport it");

  if (await devTree()) return t.skip(SKIP_REASON);
  const { stdout } = await exec("node", ["scripts/scan-bundle.mjs"], { cwd: ROOT });
  assert.match(stdout, /under the [\d]+-byte ceiling/, "a passing scan must state its headroom");
});

/**
 * inSandbox copies the build tree to a temporary directory, applies `mutate`, and runs the scan there.
 *
 * # Why a copy and not the real tree
 *
 * The first version appended to a chunk in `.next/` in place. It proved the fence, and it also made
 * the whole suite flaky: `node --test tests/*.test.mjs` runs the FILES concurrently, and the other
 * files boot a Next server that serves those exact chunks. So the probe was corrupting the bundle
 * under a live server, and seven unrelated security tests failed — intermittently, depending on
 * timing, which is the worst way for a test to be wrong.
 *
 * A sandbox has the same evidentiary value and touches nothing anybody else is reading.
 *
 * The mutation must land in a chunk the MANIFEST names: the scan measures what the build says ships,
 * so a stray new file is correctly ignored and a probe written to one would silently prove nothing.
 */
async function inSandbox(mutate, body) {
  const sandbox = await mkdtemp(join(tmpdir(), "heros-payload-"));
  const next = join(sandbox, ".next");
  await cp(join(ROOT, ".next", "static"), join(next, "static"), { recursive: true });
  for (const name of ["build-manifest.json", "app-build-manifest.json"]) {
    await cp(join(ROOT, ".next", name), join(next, name)).catch(() => {});
  }

  const manifest = JSON.parse(await readFile(join(next, "app-build-manifest.json"), "utf8"));
  const paths = new Set();
  const collect = (node) => {
    if (typeof node === "string") {
      if (node.startsWith("static/") && node.endsWith(".js")) paths.add(node);
    } else if (node && typeof node === "object") {
      for (const value of Object.values(node)) collect(value);
    }
  };
  collect(manifest);
  const target = [...paths][0];
  assert.ok(target, "the build manifest must name at least one shipped chunk");

  const file = join(next, target);
  await writeFile(file, (await readFile(file, "utf8")) + mutate);

  try {
    await body(sandbox);
  } finally {
    await rm(sandbox, { recursive: true, force: true });
  }
}

const SCAN = join(ROOT, "scripts", "scan-bundle.mjs");

/**
 * devTree reports whether a dev server has written into `.next`.
 *
 * The payload checks below cannot run against such a tree — `next dev` replaces the chunks and the
 * manifests with its own — and the scan refuses rather than misreporting. That refusal is correct, but
 * a test that goes RED for an environmental reason teaches people to ignore red, so these skip with a
 * stated reason instead. `npm run build` runs the scan against the build it just produced, so the gate
 * is enforced where it matters and the skip cannot hide a real overage.
 */
async function devTree() {
  try {
    await readFile(join(ROOT, ".next", "static", "development", "_buildManifest.js"), "utf8");
    return true;
  } catch {
    return false;
  }
}

const SKIP_REASON = "`.next` was written by a dev server — run `npm run build`, which scans its own output";

test("the payload ceiling can actually go red", async (t) => {
  if (await devTree()) return t.skip(SKIP_REASON);
  await inSandbox(`\n// probe\n${"x".repeat(2_000_000)}\n`, async (cwd) => {
    try {
      await exec("node", [SCAN], { cwd });
      assert.fail("the payload ceiling did not fail the scan");
    } catch (error) {
      assert.match(String(error.stderr), /PAYLOAD: shipped client bundle is/);
      assert.match(String(error.stderr), /over the .* ceiling by/, "the failure must name the overage");
    }
  });
});

test("a decorative runtime fails the scan", async (t) => {
  if (await devTree()) return t.skip(SKIP_REASON);
  await inSandbox("\nnew THREE.WebGLRenderer({antialias:true});\n", async (cwd) => {
    try {
      await exec("node", [SCAN], { cwd });
      assert.fail("a 3D runtime did not fail the scan");
    } catch (error) {
      assert.match(String(error.stderr), /PAYLOAD: three\.js is present/);
    }
  });
});

// ── R19 / R20 / FR39 · Acceleration is additive, and nothing acts for the user ──

test("every capability in the command path is also reachable by navigation", async () => {
  // R19. The article's own caveat about experimental navigation is "risk of confusing users; must
  // maintain discoverability", and the way this console avoids it is by never REMOVING the
  // conventional path — the palette is an accelerator, never the only route.
  //
  // The navigation is now a rail built from two declared arrays rather than a hand-written row of
  // links, which is the shape that makes this checkable: the palette's surfaces and the rail's
  // surfaces are two lists, and they must agree.
  const layout = await read("src/app/app/layout.tsx");

  const palette = [...layout.matchAll(/group:\s*"Surface",\s*label:\s*"[^"]+",\s*href:\s*([^\s,}]+)/g)].map(
    (m) => m[1].replace(/^"|"$/g, ""),
  );
  assert.ok(palette.length >= 7, "the palette is expected to offer every surface");

  const rail = [...layout.matchAll(/\{\s*href:\s*"([^"]+)"/g)].map((m) => m[1]);
  assert.ok(rail.length >= 7, "the rail is expected to declare every surface");

  // `routes.overview()` and the like resolve to a literal the rail also uses; comparing the
  // EXPRESSION would pass on two different routes that happen to be written the same way, so the
  // helper calls are resolved to their value first.
  //
  // The resolution is read from routes.ts rather than hard-coded here. A hand-maintained list of "the
  // few helpers we know about" fails the day a surface is added through a helper the list has not
  // heard of — which is a red build about the TEST, not about the console, and the fix is always to
  // extend the list rather than to look at what it was guarding.
  const routesSource = await read("src/lib/routes.ts");
  const zeroArg = new Map(
    [...routesSource.matchAll(/(\w+):\s*\(\)\s*=>\s*"([^"]+)"/g)].map((m) => [`routes.${m[1]}()`, m[2]]),
  );
  assert.ok(zeroArg.size >= 5, "routes.ts is expected to declare the console's fixed surfaces");

  for (const href of palette) {
    const expected = zeroArg.get(href) ?? href;
    assert.ok(
      rail.includes(expected),
      `${expected} is offered in the command path but is not in the navigation (R19)`,
    );
  }

  // And the rail is rendered twice — once as a sidebar, once in flow below the `md` breakpoint —
  // rather than collapsing into a menu button. A surface reachable only after opening a drawer is one
  // most readers never learn exists.
  assert.match(layout, /aria-label="Console"/);
  assert.match(layout, /aria-label="Console \(compact\)"/, "the narrow viewport must keep the same routes");
});

test("🔴 no input carrying user intent is pre-filled or auto-submitted", async () => {
  // R20 / FR39. On this product the pre-filled form is not a style question: the trend ledger rejects
  // "progressive lead nurturing" outright because a pre-filled confirmation is a confirmation that
  // confirms nothing. The customer console's write path is the configurator and the sign-in form.
  const signin = await read("src/app/signin/page.tsx");
  assert.match(signin, /name="assertion"/);
  assert.doesNotMatch(signin, /name="assertion"[\s\S]{0,300}?defaultValue=/, "a credential field must open empty");
  assert.doesNotMatch(signin, /autoComplete="on"/);

  // Nothing may submit a form on the reader's behalf on load.
  for (const rel of ["src/app/signin/page.tsx", "src/app/app/configure/configurator.tsx"]) {
    const source = await read(rel);
    assert.doesNotMatch(source, /\.submit\(\)/, `${rel} must not submit a form for the user`);
    assert.doesNotMatch(source, /useEffect\([^)]*requestSubmit/s, `${rel} must not auto-submit on mount`);
  }
});

test("🔴 the console ships no agentic affordance over its write path", async () => {
  // The ledger rejects "AI chatbots" as a write-path affordance. Stated as a check so a future
  // "quick action" or "suggested fix" button cannot arrive without somebody deleting this test, which
  // is a conversation rather than a commit.
  //
  // Note the assertion is on the JSX ATTRIBUTE, not on the identifier: `primitives.tsx` names
  // `dangerouslySetInnerHTML` in a comment explaining why it is never used, and a check that cannot
  // tell a prohibition from its own rationale fails on the documentation.
  const primitives = await read("src/components/primitives.tsx");
  assert.doesNotMatch(primitives, /dangerouslySetInnerHTML=\{/, "raw markup must never be rendered");

  const scan = await read("scripts/scan-bundle.mjs");
  assert.match(scan, /Lottie|GSAP/, "the decorative-runtime list is what keeps the rejections mechanical");
});

// ── R17 / FR37 · Contrast, computed rather than asserted ─────────────────────

/**
 * srgb → relative luminance → contrast ratio, per WCAG 2.1.
 *
 * Implemented here rather than taken on trust from a palette comment, because a comment saying "meets
 * AA" is exactly the kind of claim that stops being true the first time somebody nudges a hue. The
 * numbers below were first measured in a live browser; this is that measurement made repeatable.
 */
function luminance(hex) {
  const n = hex.replace("#", "");
  const full = n.length === 3 ? [...n].map((c) => c + c).join("") : n;
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(full.slice(i, i + 2), 16) / 255);
  const f = (x) => (x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4));
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

function contrast(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** palette pulls the `--name: #hex` definitions out of one themed block. */
function palette(block) {
  const out = {};
  for (const [, name, value] of block.matchAll(/--([a-z0-9-]+):\s*(#[0-9a-fA-F]{3,8});/g)) {
    out[`--${name}`] = value;
  }
  return out;
}

test("🔴 every token pair meets WCAG 2.1 AA in BOTH resolved themes", async () => {
  const identity = await read("src/app/tokens.customer.css");
  const blocks = themedBlocks(identity);
  const themes = {
    light: palette(blocks.light),
    dark: palette(blocks.dark),
  };

  // 4.5 for text; 3.0 for large text and for non-text boundaries (SC 1.4.11) — a control's outline is
  // in the second group, and that is the one this test was written after failing.
  //
  // Every status is checked against BOTH surfaces it can appear on, because a chip sits on a tint of
  // its own hue over one of them, and a tint at ~12% moves the effective background by very little.
  // Checking against the base surface is therefore the honest floor rather than a convenient one.
  const STATUSES = ["--ok", "--warn", "--bad", "--halt", "--info", "--neutral", "--unknown", "--gated", "--llm"];
  const SURFACES = ["--background", "--card"];

  const TEXT = [
    ["--foreground", "--background"],
    ["--foreground", "--card"],
    ["--muted-foreground", "--background"],
    ["--muted-foreground", "--card"],
    ["--primary", "--background"],
    ["--primary", "--card"],
    ["--primary-foreground", "--primary"],
    ["--graph-text", "--graph-canvas"],
    ["--graph-text-muted", "--graph-canvas"],
    ...STATUSES.flatMap((status) => SURFACES.map((surface) => [status, surface])),
  ];
  const NON_TEXT = [
    ["--ring", "--background"],
    ["--ring", "--card"],
    ["--ctl", "--graph-canvas"],
    ["--none", "--graph-canvas"],
  ];

  const failures = [];
  for (const [theme, colors] of Object.entries(themes)) {
    for (const [group, floor] of [
      [TEXT, 4.5],
      [NON_TEXT, 3.0],
    ]) {
      for (const [fg, bg] of group) {
        if (!colors[fg] || !colors[bg]) continue; // a pair the theme genuinely does not define
        const ratio = contrast(colors[fg], colors[bg]);
        if (ratio < floor) failures.push(`${theme}: ${fg} on ${bg} is ${ratio.toFixed(2)}:1, below ${floor}`);
      }
    }
  }

  assert.deepEqual(failures, [], `contrast floor violations:\n  ${failures.join("\n  ")}`);
});

test("🔴 the public surface's fixed palette meets AA too", async () => {
  // It does not follow the reader's theme — it is a poster with one contrast — so nothing else in this
  // file covers it, and "the marketing page is exempt" is how a 2.8:1 headline ships.
  const identity = await read("src/app/tokens.customer.css");
  const invariants = themedBlocks(identity).light; // the invariants block precedes it; both are :root
  const all = palette(identity.replace(/\/\*[\s\S]*?\*\//g, ""));
  assert.ok(invariants, "the palette blocks must be readable");

  const pairs = [
    ["--marketing-ink", "--marketing-canvas"],
    ["--marketing-ink", "--marketing-panel"],
    ["--marketing-accent", "--marketing-canvas"],
    ["--marketing-accent-ink", "--marketing-accent"],
  ];
  const failures = [];
  for (const [fg, bg] of pairs) {
    assert.ok(all[fg] && all[bg], `${fg} or ${bg} is not defined`);
    const ratio = contrast(all[fg], all[bg]);
    if (ratio < 4.5) failures.push(`${fg} on ${bg} is ${ratio.toFixed(2)}:1`);
  }
  assert.deepEqual(failures, [], `the public surface falls below AA:\n  ${failures.join("\n  ")}`);
});
