// craft.test.mjs holds the assertions for the experience layer above the interface floor (§15).
//
// The floor is asserted next door in interface-floor.test.mjs: keyboard reach, four states, contrast,
// escaped values, the pinned locale. This file asserts the things that make the console the surface an
// operator PREFERS — and, more importantly, the invariant that keeps "preferred" from meaning "easier
// to destroy something with" (FR37).
//
// Every test here is written so it can actually go red. Where a guard is itself the thing under test
// (the token scanner), the test injects a violation and asserts the guard fails — a fence nobody has
// ever seen fail is a fence nobody knows is connected.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir, writeFile, unlink } from "node:fs/promises";
import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";

const exec = promisify(execFile);
const ROOT = new URL("..", import.meta.url).pathname;

async function read(rel) {
  return readFile(join(ROOT, rel), "utf8");
}

async function* walk(dir) {
  for (const entry of await readdir(join(ROOT, dir), { withFileTypes: true })) {
    const rel = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(rel);
    else yield rel;
  }
}

async function sources(ext = /\.tsx?$/) {
  const out = [];
  for await (const file of walk("src")) if (ext.test(file)) out.push(file);
  return out;
}

// ── 15.14 · Design language conformance ──────────────────────────────────────

test("the token scan passes on the current tree", async () => {
  const { stdout } = await exec("node", ["scripts/scan-tokens.mjs"], { cwd: ROOT });
  assert.match(stdout, /token scan passed/);
});

test("the token scan goes RED on an injected literal", async () => {
  // A guard that has never been seen to fail is a guard nobody knows is wired up. This injects the
  // exact violation the guard exists for and requires a non-zero exit.
  const probe = join(ROOT, "src", "__token_probe.css");
  await writeFile(probe, ".probe { color: #ff0000; padding: 13px; border-radius: 3px; }\n");
  try {
    await assert.rejects(
      () => exec("node", ["scripts/scan-tokens.mjs"], { cwd: ROOT }),
      (error) => {
        assert.equal(error.code, 1, "the token scan did not fail on a literal");
        assert.match(error.stderr, /colour literal/);
        assert.match(error.stderr, /spacing literal/);
        assert.match(error.stderr, /radius literal/);
        return true;
      },
    );
  } finally {
    await unlink(probe);
  }
});

test("the shared token system is imported, not forked", async () => {
  const css = await read("src/app/globals.css");
  assert.match(css, /@import "\.\.\/\.\.\/\.\.\/design-system\/tokens\.css"/, "globals.css does not import the shared tokens");
  assert.match(css, /@import "\.\/tokens\.operator\.css"/, "globals.css does not import the operator identity layer");

  const shared = await readFile(join(ROOT, "..", "design-system", "tokens.css"), "utf8");
  // The shared layer must NOT carry the operator's identity — that is the whole of Decision 12.
  assert.equal(/--chrome-bg/.test(shared), false, "the shared token system carries operator chrome");
  assert.equal(/--accent:/.test(shared), false, "the shared token system carries a surface accent");
});

test("density changes the rhythm and never the information", async () => {
  const shared = await readFile(join(ROOT, "..", "design-system", "tokens.css"), "utf8");
  const block = shared.match(/:root\[data-density="compact"\]\s*{([^}]*)}/);
  assert.ok(block, "no compact density block");

  // Every declaration in the compact block must be a spacing/rhythm token. A `display: none`, a
  // `visibility`, or an overridden font-size would mean compact hides or shrinks CONTENT, which is a
  // second information architecture wearing a preference's clothes.
  const declarations = block[1]
    .split(";")
    .map((d) => d.trim())
    .filter(Boolean)
    .map((d) => d.split(":")[0].trim());
  for (const property of declarations) {
    assert.match(
      property,
      /^--(pad|stack)/,
      `compact density changes \`${property}\` — density may only change padding/stack rhythm`,
    );
  }
});

test("numbers are rendered for comparison: tabular figures, right-aligned, unit once", async () => {
  const css = await read("src/app/globals.css");
  assert.match(css, /\.num[^{]*{[^}]*text-align:\s*right/s, "numeric cells are not right-aligned");
  assert.match(css, /\.num[^{]*{[^}]*font-variant-numeric:\s*tabular-nums/s, "numeric cells are not tabular");
  assert.match(css, /\.stat__value\s*{[^}]*font-variant-numeric:\s*tabular-nums/s, "stat values are not tabular");

  const primitives = await read("src/components/primitives.tsx");
  // The unit belongs to the COLUMN, not the cell: DataTable renders it in the header.
  assert.match(primitives, /c\.unit \?/, "DataTable does not render a column unit");
  assert.match(primitives, /className={c\.numeric \? "num" : undefined}/, "numeric columns are not marked");
});

test("the hazard palette is reserved for hazard", async () => {
  const css = await read("src/app/globals.css");

  // Any rule that paints with --danger/--warn must either sit under a `HAZARD:` annotation or name a
  // hazard in its selector. Everything else is decoration, which is what FR31 forbids.
  const hazardSelectors =
    /danger|warn|caution|impersonation|alarm|degraded|unknown|stale|palette__danger|action--global/i;
  const blocks = [...css.matchAll(/([^{}]+){([^}]*)}/g)];
  const offenders = [];
  for (const [, rawSelector, body] of blocks) {
    if (!/var\(--(danger|warn)/.test(body)) continue;
    const selector = rawSelector.trim().split("\n").pop().trim();
    const index = css.indexOf(rawSelector);
    const preamble = css.slice(Math.max(0, index - 400), index);
    const annotated = /HAZARD:/.test(preamble.split("*/").slice(-2).join("*/"));
    if (!hazardSelectors.test(selector) && !annotated) offenders.push(selector);
  }
  assert.deepEqual(offenders, [], `these rules use the hazard palette without being a hazard: ${offenders.join(", ")}`);
});

test("the operator identity does not reuse a hazard hue, in EITHER theme", async () => {
  const operator = await read("src/app/tokens.operator.css");
  const shared = await readFile(join(ROOT, "..", "design-system", "tokens.css"), "utf8");
  // The hazard hues are looked up in the OPERATOR layer first and the shared layer second, because
  // the operator layer re-values the status palette for the near-black instrument ground. Checking
  // the shared values while the console renders different ones would be a test asserting a property
  // of a file nobody looks at.
  const hex = (source, name) => (source.match(new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`)) ?? [])[1];

  // Both files now define their colours as per-theme palettes and MAP them, so the check follows the
  // literals to where they live — and becomes stronger for it: the reservation is asserted in the light
  // theme and the dark one rather than against whichever set happened to be unconditional.
  //
  // The defect being prevented is concrete: the operator console used to be amber, which is `--warn`.
  // Painting the chrome, the links and the primary buttons in the hazard hue spends the signal the kill
  // switch depends on — by the time an operator reaches a control that really is dangerous, red and
  // amber have been ordinary for the whole session.
  const THEMES = [
    { name: "light", accent: "--ol-accent", warn: ["--ol-warn", "--light-warn"], danger: ["--ol-danger", "--light-danger"] },
    { name: "dark", accent: "--od-accent", warn: ["--od-warn", "--dark-warn"], danger: ["--od-danger", "--dark-danger"] },
  ];

  /** effective resolves a token to whichever layer actually supplies the rendered value. */
  const effective = ([operatorName, sharedName]) => hex(operator, operatorName) ?? hex(shared, sharedName);

  for (const theme of THEMES) {
    const accent = hex(operator, theme.accent);
    const warn = effective(theme.warn);
    const danger = effective(theme.danger);
    assert.ok(accent && warn && danger, `could not read the ${theme.name} accent and hazard hues`);

    assert.notEqual(accent, warn, `the ${theme.name} operator accent is the warning colour`);
    assert.notEqual(accent, danger, `the ${theme.name} operator accent is the danger colour`);

    // …and not merely a different shade of the same hue. A hue distance of at least 60° keeps them
    // distinguishable for the readers who most need them to be.
    assert.ok(
      hueDistance(accent, warn) > 60,
      `the ${theme.name} operator accent is within 60° of the warning hue`,
    );
    assert.ok(
      hueDistance(accent, danger) > 60,
      `the ${theme.name} operator accent is within 60° of the danger hue`,
    );
  }
});

test("🔴 the operator chrome stays distinguishable from the customer console in BOTH themes (FR23)", async () => {
  // FR23 is a safety requirement, not a style rule: an operator with both consoles open, acting
  // cross-tenant while believing the view is single-tenant, is its named failure. Adding a light theme
  // is exactly the kind of change that could reintroduce it by a new route, so the property is asserted
  // rather than assumed.
  //
  // # Why the two files are read differently, and why that was worth fixing rather than working around
  //
  // This assertion was committed reading `--cl-accent` / `--cd-accent` from the customer console. Those
  // names have never existed in any commit of `tokens.customer.css` — so the test failed on the value
  // being `undefined` and asserted nothing about hue from the day it landed. A fence that cannot read
  // its own inputs is not a weaker fence, it is an absent one wearing a 🔴.
  //
  // The two consoles key their palettes differently, and the fix follows that rather than renaming
  // anything: the OPERATOR prefixes per theme (`--ol-*` light, `--od-*` dark) in one block, while the
  // CUSTOMER declares the same name `--accent` inside a per-theme SELECTOR block. So the operator is
  // read by name and the customer is read by name WITHIN its block.
  const operator = await read("src/app/tokens.operator.css");
  const customer = await readFile(join(ROOT, "..", "console", "src", "app", "tokens.customer.css"), "utf8");
  const hex = (source, name) => (source.match(new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`)) ?? [])[1];

  /**
   * blockOf returns the declarations inside the rule whose selector line matches, up to its `}`.
   *
   * Deliberately naive — no CSS parser, no nesting — because the shape it reads is flat and a parser
   * would be a dependency inside a safety fence. It fails LOUD (returns undefined, and the assertion
   * below names the theme) rather than silently matching the wrong block, which is the failure mode
   * this whole test just spent a release demonstrating.
   */
  const blockOf = (source, selector) => {
    // Comments are stripped FIRST. `tokens.customer.css` documents its own three theme blocks in its
    // header, so both selectors appear in prose before they appear as rules — and an `indexOf` that
    // does not know the difference lands on the comment, walks to the next `{`, and reads the wrong
    // palette entirely. That is how this fence read a block with no `--accent` in it and reported
    // "could not read both accents" instead of a hue.
    const code = source.replace(/\/\*[\s\S]*?\*\//g, "");
    const start = code.indexOf(selector);
    if (start < 0) return undefined;
    const open = code.indexOf("{", start);
    const close = code.indexOf("\n}", open);
    return open < 0 || close < 0 ? undefined : code.slice(open, close);
  };

  // The customer's light palette is `:root, :root[data-theme="light"]`; its dark one is
  // `:root[data-theme="dark"]`. Anchored on the data-theme selector in both cases, so the bare `:root`
  // block above them (the public marketing surface, which is dark in BOTH themes and is not the
  // console's chrome) cannot be picked up by accident.
  const customerAccents = {
    light: hex(blockOf(customer, ':root[data-theme="light"]') ?? "", "--accent"),
    dark: hex(blockOf(customer, ':root[data-theme="dark"]') ?? "", "--accent"),
  };

  for (const [theme, operatorAccent] of [
    ["light", "--ol-accent"],
    ["dark", "--od-accent"],
  ]) {
    const op = hex(operator, operatorAccent);
    const cu = customerAccents[theme];
    assert.ok(op && cu, `could not read both accents in the ${theme} theme`);
    assert.notEqual(op, cu, `the two consoles share an accent in the ${theme} theme`);
    // 25° is the separation the SHIPPED pair already maintains (the dark accents measure 26° apart),
    // not a number invented for this test. Calibrating it to the existing design is what keeps it
    // meaningful: a threshold above what ships would fail on day one and be lowered; one far below it
    // would pass through a regression. This caught a real one — a light customer accent added in this
    // change sat 23° away, tighter in light mode than the product manages in dark.
    assert.ok(
      hueDistance(op, cu) > 25,
      `the ${theme} operator accent is ${hueDistance(op, cu).toFixed(0)}° from the customer accent — ` +
        "the consoles are not distinguishable at a glance",
    );
  }

  // The chrome band is DARKER than the page in both themes, which is what makes the console's identity
  // a horizon line the eye finds before it reads anything. A light theme whose chrome went pale would
  // have removed that cue precisely where it is easiest to lose.
  for (const band of ["--ol-chrome-bg", "--od-chrome-bg"]) {
    const value = hex(operator, band);
    assert.ok(value, `${band} is not defined`);
    const [r, g, b] = [1, 3, 5].map((i) => parseInt(value.slice(i, i + 2), 16));
    assert.ok((r + g + b) / 3 < 90, `${band} is not a dark band — the operator horizon line is gone`);
  }
});

test("🔴 everything painted on the chrome band resolves through the chrome tokens (FR23, FR39)", async () => {
  // The band is dark in BOTH themes while the page inverts, so the page's inks are contrast-checked
  // against the wrong background for anything sitting on it. That is not hypothetical: `.quiet` used
  // `--text-muted`, and in the light theme "Sign out" rendered as dark slate on deep teal — present
  // in the accessibility tree, invisible on screen, and impossible to notice while working in dark.
  //
  // The rule is therefore mechanical: inside the band, colour comes from `--chrome-*`.
  const css = await read("src/app/globals.css");
  const BAND = /^(\.chrome|\.nav\b|\.nav__|\.role-chip|\.palette-trigger)/;
  const COLOURED = /(?:^|\s|;)(?:color|background|background-color|border(?:-\w+)?-color|border(?:-\w+)?)\s*:\s*([^;]+)/g;

  const offenders = [];
  for (const [, rawSelector, body] of css.matchAll(/([^{}]+){([^}]*)}/g)) {
    const selector = rawSelector.trim().split("\n").pop().trim();
    if (!BAND.test(selector)) continue;
    for (const [, value] of body.matchAll(COLOURED)) {
      const tokens = [...value.matchAll(/var\((--[\w-]+)\)/g)].map((m) => m[1]);
      for (const token of tokens) {
        // Non-colour tokens (rule weights, radii) legitimately appear in shorthand borders.
        if (/^--(hairline|rule|rule-strong|rule-heavy|radius-|space-)/.test(token)) continue;
        if (!token.startsWith("--chrome-")) {
          offenders.push(`${selector}: ${token}`);
        }
      }
    }
  }
  assert.deepEqual(
    offenders,
    [],
    `these band rules take a PAGE colour, which is contrast-checked against the wrong background: ${offenders.join(", ")}`,
  );
});

function hueDistance(a, b) {
  const h = (hexColour) => {
    const [r, g, bl] = [1, 3, 5].map((i) => parseInt(hexColour.slice(i, i + 2), 16) / 255);
    const max = Math.max(r, g, bl);
    const min = Math.min(r, g, bl);
    if (max === min) return 0;
    const d = max - min;
    let hue;
    if (max === r) hue = ((g - bl) / d) % 6;
    else if (max === g) hue = (bl - r) / d + 2;
    else hue = (r - g) / d + 4;
    return ((hue * 60) % 360 + 360) % 360;
  };
  const diff = Math.abs(h(a) - h(b));
  return Math.min(diff, 360 - diff);
}

test("every view composes from the closed primitive set", async () => {
  const pages = (await sources()).filter((f) => f.startsWith(join("src", "app")) && f.endsWith("page.tsx"));
  assert.ok(pages.length >= 8, "expected the console's pages to be found");

  for (const page of pages) {
    const src = await read(page);
    if (page.includes("signin")) continue; // the sign-in page renders before an identity exists
    assert.match(src, /from "@\/components\/primitives"/, `${page} does not compose from the primitive set`);
    // An inline style object is a bespoke layout by another name.
    assert.equal(/style={{/.test(src), false, `${page} carries an inline style — use a primitive`);
  }
});

test("every class a view uses is defined in the stylesheet", async () => {
  // This is the "adding a primitive is deliberate" guard: a class invented in a page and never
  // defined is either a typo (silently unstyled) or a shape that skipped the design language.
  const css = await read("src/app/globals.css");
  const defined = new Set([...css.matchAll(/\.([a-zA-Z][\w-]*)/g)].map((m) => m[1]));
  const dynamic = new Set(["pill--accent", "stat--changed"]); // composed at runtime from props

  for (const file of await sources()) {
    const src = await read(file);
    for (const match of src.matchAll(/className=(?:"([^"]+)"|{`([^`]*)`})/g)) {
      const raw = (match[1] ?? match[2] ?? "").replace(/\$\{[^}]*\}/g, " ");
      for (const cls of raw.split(/\s+/).filter(Boolean)) {
        // `pill pill--${tone}` leaves a bare `pill--` once the interpolation is stripped. The base
        // class is checked; the modifier is a runtime value and is covered by the token/CSS pairing
        // rather than by this scan.
        if (cls.endsWith("-") || dynamic.has(cls)) continue;
        assert.ok(defined.has(cls), `${file} uses .${cls}, which the stylesheet does not define`);
      }
    }
  }
});

// ── The state family: nine answers, nine remedies (FR26, FR36) ───────────────

test("🔴 the console can render all NINE states, and no two mean the same thing", async () => {
  // The design brief names seven states every page must be able to render, and the admin console adds
  // Denied and Degraded on top. Four were missing entirely, and their absence was not neutral — each
  // one was being answered with another state's copy:
  //
  //   not_found    a mistyped id rendered as "degraded", i.e. "the platform is broken"
  //   unusable     an unparseable body was cast to the expected type and rendered as BLANK DATA
  //   not_mounted  a capability absent from this deployment rendered as an outage
  //   gated        an entitlement boundary rendered in the language of failure
  const states = await read("src/components/states.tsx");
  const css = await read("src/app/globals.css");

  const EXPECTED = [
    "LoadingState",
    "EmptyState",
    "NotMountedState",
    "GatedState",
    "NotFoundState",
    "DeniedState",
    "DegradedState",
    "UnusableState",
    "UnknownState",
  ];
  for (const component of EXPECTED) {
    assert.match(states, new RegExp(`export function ${component}\\b`), `${component} does not exist`);
  }

  const variants = [...css.matchAll(/\.state--([a-z-]+)\s*\{/g)].map((m) => m[1]);
  assert.equal(
    new Set(variants).size,
    EXPECTED.length,
    `the stylesheet defines ${new Set(variants).size} state variants for ${EXPECTED.length} states`,
  );
});

test("🔴 the states are distinguishable BEFORE the copy is read", async () => {
  // This is the design brief's actual complaint: "they currently differ only by border and tint".
  // Nine states cannot have nine legible tints, so each carries a MARK as well — and the marks must
  // all differ, or the second signal is not a signal.
  const states = await read("src/components/states.tsx");
  const marks = [...states.matchAll(/mark="([^"]+)"/g)].map((m) => m[1]);
  assert.ok(marks.length >= 9, `only ${marks.length} states carry a mark`);
  assert.equal(new Set(marks).size, marks.length, `two states share a mark: ${marks.join(" ")}`);

  // The mark is never the only carrier: it is aria-hidden, and the title states the answer in words.
  assert.match(states, /className="state__mark" aria-hidden="true"/, "the mark is exposed as content");
  for (const [, title] of states.matchAll(/title="([^"]+)"/g)) {
    assert.ok(title.trim().length > 3, `a state's title is not a sentence: ${title}`);
  }

  // And every state resolves through the ONE block, so a tenth cannot arrive with its own layout.
  // Exactly one container exists in the file — StateBlock's own — and it is parameterised.
  const containers = [...states.matchAll(/<div className=[{"]`?state state--/g)];
  assert.equal(
    containers.length,
    1,
    `${containers.length} state containers in states.tsx — every state must render through StateBlock`,
  );
  assert.match(states, /<div className={`state state--\$\{variant\}`}/, "the one container is not parameterised");
});

test("🔴 'nothing is wrong' never borrows the hazard palette (FR31)", async () => {
  // Empty, not-mounted and gated are ordinary answers. Gated in particular is an ENTITLEMENT — the
  // brief marks it "✋ not an error" — and rendering an entitlement in red teaches operators that the
  // console cries wolf, which costs the states that are real failures.
  const css = await read("src/app/globals.css");
  for (const variant of ["empty", "not-mounted", "gated", "not-found", "denied"]) {
    const rules = [...css.matchAll(new RegExp(`\\.state--${variant}[^{]*\\{([^}]*)\\}`, "g"))];
    assert.ok(rules.length > 0, `.state--${variant} is not styled`);
    for (const [, body] of rules) {
      assert.equal(
        /var\(--(danger|warn)/.test(body),
        false,
        `.state--${variant} uses the hazard palette, and it is not a hazard`,
      );
    }
  }

  // …and the three that ARE hazards do carry it, or the family has no severity signal at all.
  for (const variant of ["degraded", "unusable", "unknown"]) {
    const joined = [...css.matchAll(new RegExp(`\\.state--${variant}[^{]*\\{([^}]*)\\}`, "g"))]
      .map((m) => m[1])
      .join(" ");
    assert.match(joined, /var\(--(danger|warn)/, `.state--${variant} does not read as a hazard`);
  }
});

test("🔴 an unreadable response is never cast to data", async () => {
  // The most dangerous of the four gaps. `safeParse` returned null, null was cast to the expected
  // type, and the page rendered every field as undefined — a version mismatch presented as a tenant
  // with no plan. It does not look like a failure; it looks like data, and it is read as data.
  const api = await read("src/lib/adminApi.ts");
  assert.match(api, /UNPARSEABLE = Symbol/, "parse failure cannot be told from a body that parsed to null");
  assert.match(api, /throw new AdminApiError\(\s*"unusable"/s, "an unreadable 2xx body is not raised");

  // A 404 must not arrive as "the platform is broken", and the status map is what prevents it.
  assert.match(api, /404:\s*"not_found"/, "a 404 is not classified as not_found");
  assert.match(api, /402:\s*"gated"/, "a 402 is not classified as gated");
  assert.match(api, /501:\s*"not_mounted"/, "a 501 is not classified as not_mounted");

  // The tenant surface renders a bad identifier IN PLACE rather than leaving through the framework's
  // unstyled 404 — which has no chrome, no acting principal, and does not say which console you are on.
  // Checked on CODE, not on prose: the comment explaining why the framework's 404 was abandoned
  // naturally names the call it replaced.
  const detail = (await read("src/app/tenants/[id]/page.tsx"))
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/^\s*\/\/.*$/gm, "");
  assert.equal(
    /from "next\/navigation"[\s\S]{0,80}notFound|notFound\(\)/.test(detail),
    false,
    "a mistyped tenant id still leaves the console shell through the framework's unstyled 404",
  );
  assert.match(detail, /<NotFoundState/, "a mistyped tenant id does not render the not-found state");
});

test("the operating picture reports each panel's own verdict", async () => {
  const overview = await read("src/lib/overview.ts");
  for (const kind of ["not_mounted", "gated", "unusable"]) {
    assert.match(overview, new RegExp(`"${kind}"`), `a panel cannot report ${kind}`);
  }
  // `degraded` must be the FALLBACK, not the default — it is the answer that sends someone hunting an
  // outage, and it should only be given when there might be one.
  assert.match(overview, /PANEL_KINDS\[error\.kind\] \?\? "degraded"/, "every failure still flattens to degraded");

  const picture = await read("src/components/operatingPicture.tsx");
  for (const component of ["NotMountedState", "GatedState", "UnusableState"]) {
    assert.match(picture, new RegExp(`<${component}`), `the picture cannot render ${component}`);
  }
});

// ── 15.15 · Velocity: the palette, and URL-addressable views ─────────────────

test("the command palette opens on one keystroke from any view", async () => {
  const palette = await read("src/components/commandPalette.tsx");
  // The listener is on the document, not on a container: "from any view" must not mean "from any
  // view where the operator has already clicked something".
  assert.match(palette, /document\.addEventListener\("keydown"/, "the palette has no document-level shortcut");
  assert.match(palette, /metaKey \|\| event\.ctrlKey/, "the palette is not bound to ⌘K / Ctrl-K");
  assert.match(palette, /event\.key === "Escape"/, "the palette does not close on Escape");
  assert.match(palette, /inputRef\.current\?\.focus\(\)/, "the palette does not focus its input on open");

  const shell = await read("src/components/shell.tsx");
  assert.match(shell, /<CommandPalette/, "the palette is not mounted in the shell that wraps every view");
});

test("the palette offers exactly what the role grants — never offered-then-refused", async () => {
  const surfaces = await read("src/lib/surfaces.ts");
  // Both the nav and the palette filter the SAME map by the SAME capability list, which is the same
  // permission map the backend enforces. A capability the role lacks is ABSENT, not disabled.
  assert.match(surfaces, /export function navFor\(capabilities: string\[\]\)/);
  assert.match(surfaces, /export function commandsFor\(capabilities: string\[\]\)/);
  assert.match(surfaces, /capabilities\.includes\(s\.capability\)/);

  const shell = await read("src/components/shell.tsx");
  assert.match(shell, /navFor\(identity\.capabilities\)/, "the nav is not filtered by live capabilities");
  assert.match(shell, /commandsFor\(identity\.capabilities\)/, "the palette is not filtered by live capabilities");
});

test("a subject is reachable by name, never by recalling an identifier", async () => {
  const shell = await read("src/components/shell.tsx");
  assert.match(shell, /paletteSubjects/, "the palette is given no subjects to type-ahead over");
  // Subjects are listed with the OPERATOR's own session, so the palette cannot surface anything the
  // gate would not already have shown them.
  assert.match(shell, /adminFetch<\{ tenants: TenantView\[\] \}>\("\/admin\/api\/tenants", \{ sessionToken \}\)/);
});

test("every view's state lives in the URL, and nothing sensitive does", async () => {
  const addressable = {
    "src/app/tenants/page.tsx": /searchParams: Promise<\{ q\?: string \}>/,
    "src/app/audit/page.tsx": /seq\?: string; actor\?: string; action\?: string; result\?: string/,
    "src/app/fleet/page.tsx": /searchParams: Promise<\{ state\?: string \}>/,
    "src/app/registry/page.tsx": /searchParams: Promise<\{ q\?: string \}>/,
    "src/app/billing/page.tsx": /searchParams: Promise<\{ tenant\?: string \}>/,
    "src/app/crosstenant/page.tsx": /searchParams: Promise<\{ aggregate\?: string \}>/,
  };
  for (const [file, pattern] of Object.entries(addressable)) {
    const src = await read(file);
    assert.match(src, pattern, `${file} does not carry its view state in the URL`);
    // A GET form is what makes back/forward restore the view exactly, rather than resetting it.
    if (!file.includes("crosstenant")) {
      assert.match(src, /method="get"/, `${file} does not use a GET form, so back/forward would not restore it`);
    }
  }

  // Nothing may put session material or a subject's personal data in a URL.
  for (const file of await sources()) {
    const src = await read(file);
    assert.equal(
      /[?&](session|token|credential|assertion)=/.test(src),
      false,
      `${file} appears to put session material in a URL`,
    );
  }
});

// ── 15.16 · The operating picture ────────────────────────────────────────────

test("the operating picture answers halted/wrong without interaction", async () => {
  const page = await read("src/app/page.tsx");
  // The home is the picture itself, server-rendered — not a redirect to a list and not a skeleton.
  assert.match(page, /loadOperatingPicture/, "the home page does not load the operating picture");
  assert.equal(/redirect\(/.test(page), false, "the home page still redirects instead of showing state");

  const picture = await read("src/components/operatingPicture.tsx");
  for (const signal of ["Autonomous merges", "Worker fleet", "Tenants", "Active impersonations", "Anomalies"]) {
    assert.ok(picture.includes(signal), `the picture does not surface “${signal}”`);
  }
  // Halted state is a WORD as well as a colour.
  assert.match(picture, /ARMED — every tenant halted/, "the armed state is not stated in words");
});

test("a live figure carries its as-of time and announces staleness", async () => {
  const primitives = await read("src/components/primitives.tsx");
  assert.match(primitives, /Stale — could not refresh\. Last confirmed/, "staleness is not stated");
  assert.match(primitives, /As of \$\{asOf\}/, "no as-of time on live figures");

  const picture = await read("src/components/operatingPicture.tsx");
  assert.match(picture, /setStale\(true\)/, "a failed poll does not mark the picture stale");
  assert.match(picture, /STALE_AFTER_MS/, "an un-refreshed picture never becomes stale by age");

  const overview = await read("src/lib/overview.ts");
  assert.match(overview, /as_of: new Date\(\)\.toISOString\(\)/, "the picture is not stamped with its instant");
});

test("a refresh never blanks correct data and never reorders under the operator", async () => {
  const picture = await read("src/components/operatingPicture.tsx");
  // On failure the last-good picture is KEPT — the component must not clear its state.
  assert.equal(
    /setPicture\(null\)|setPicture\(undefined\)/.test(picture),
    false,
    "a failed poll clears the picture, replacing a known-old truth with none at all",
  );
  // No client-side sorting: row order is the server's, so an update cannot move a row under a cursor.
  assert.equal(/\.sort\(/.test(picture), false, "the picture sorts rows client-side, which can reorder under the operator");
  // A refresh in flight is a marker, not a loading state that replaces the numbers.
  assert.match(picture, /refreshing && !stale/, "a refresh in flight is not distinguished from a stale picture");
  assert.equal(/LoadingState/.test(picture), false, "a refresh renders a loading state over correct data");
});

test("each panel of the picture fails independently, and a denial is not an emptiness", async () => {
  const overview = await read("src/lib/overview.ts");
  assert.match(overview, /Promise\.allSettled/, "one unreachable subsystem would take the whole picture down");
  assert.match(overview, /state: "denied"/, "a panel cannot report a permission denial");
  assert.match(overview, /state: "degraded"/, "a panel cannot report a transport failure");

  const picture = await read("src/components/operatingPicture.tsx");
  assert.match(picture, /DeniedState/, "a denied panel does not name who holds the capability");
  assert.match(picture, /DegradedState/, "a degraded panel is not distinguished from an empty one");
});

// ── 15.17 · Truthful feedback ────────────────────────────────────────────────

test("a privileged command resolves to a receipt naming its audit entry", async () => {
  const form = await read("src/components/actionForm.tsx");
  assert.match(form, /Done — confirmed by the platform/, "success is not stated as confirmed by the platform");
  for (const field of ["Action", "Target", "Reason", "When", "Actor", "Result", "Audit"]) {
    assert.ok(form.includes(`<dt>${field}</dt>`), `the receipt does not state ${field}`);
  }
  // The audit reference must be REACHABLE, not merely printed.
  assert.match(form, /href={`\/audit\?seq=\$\{receipt\.outcome_seq\}`}/, "the audit reference is not a link");
  assert.match(form, /timestamp\(receipt\.at\)/, "the receipt's instant bypasses the locale swap point");
});

test("irreversibility is stated in words, not implied by a missing control", async () => {
  const form = await read("src/components/actionForm.tsx");
  assert.match(form, /There is no undo for this action/, "the receipt does not state when no undo exists");
  assert.match(form, /command\?\.undo \?/, "the receipt does not name the reversing action when one exists");

  const actions = await read("src/lib/actions.ts");
  // The one-way door must NOT carry an undo. If someone adds one, this fails.
  const gdpr = actions.slice(actions.indexOf("export async function executeGDPR"));
  const gdprCommand = gdpr.slice(gdpr.indexOf('action: "gdpr.execute"'), gdpr.indexOf('action: "gdpr.execute"') + 400);
  assert.equal(/undo:/.test(gdprCommand), false, "GDPR erasure claims a reversing action; it has none");
});

test("no optimistic success: in-flight, failed and unknown are three renderings", async () => {
  const form = await read("src/components/actionForm.tsx");
  assert.match(form, /pending \?[\s\S]{0,400}In flight/, "there is no in-flight rendering");
  assert.match(form, /state--unknown/, "there is no outcome-unknown rendering");
  assert.match(form, /Outcome unknown — do not retry yet/, "the unknown outcome does not warn against retrying");
  assert.match(form, /Open the audit log for this action/, "the unknown outcome offers no way to verify");
  // Failure states must SAY that nothing happened — the operator's next move depends on it.
  assert.match(form, /Denied — nothing happened/);
  assert.match(form, /One more step — nothing happened/);

  const actions = await read("src/lib/actions.ts");
  // Success is returned only after the platform answers: `ok: true` may not appear before the await.
  const post = actions.slice(actions.indexOf("async function post("), actions.indexOf("function form(fd: FormData)"));
  const awaitIndex = post.indexOf("await adminFetch");
  const okIndex = post.indexOf("ok: true");
  assert.ok(awaitIndex > 0 && okIndex > awaitIndex, "a command reports success before the platform confirms it");
});

test("a lost response on a COMMAND is unknown, while a lost read is degraded", async () => {
  const api = await read("src/lib/adminApi.ts");
  assert.match(
    api,
    /method === "GET" \? "degraded" : "unknown"/,
    "a transport failure does not distinguish a read from a command",
  );
});

// ── 15.10 / 14.8 · Friction survived the craft work ──────────────────────────

test("the palette navigates to a dangerous action and never performs one", async () => {
  const palette = await read("src/components/commandPalette.tsx");
  // Every selection is a navigation. A call to a server action here would be the FR37 defect.
  assert.match(palette, /router\.push\(entry\.href\)/, "the palette does not navigate on selection");
  assert.equal(/formAction|useActionState|fetch\(/.test(palette), false, "the palette can issue a request");
  for (const forbidden of ["armKillSwitch", "cancelJob", "executeGDPR", "suspendTenant", "issueRefund"]) {
    assert.equal(palette.includes(forbidden), false, `the palette imports ${forbidden} — it must only navigate`);
  }

  const surfaces = await read("src/lib/surfaces.ts");
  // Dangerous entries point at a confirmation's anchor, not at an endpoint.
  for (const entry of surfaces.matchAll(/href:\s*"([^"]+)"[\s\S]{0,200}?danger:\s*true/g)) {
    assert.equal(/\/api\//.test(entry[1]), false, `a dangerous palette entry points at an API path: ${entry[1]}`);
  }
});

test("nothing on the dangerous path is pre-filled", async () => {
  const form = await read("src/components/actionForm.tsx");
  const reason = form.slice(form.indexOf("name=\"reason\""), form.indexOf("name=\"reason\"") + 300);
  assert.equal(/defaultValue|value=/.test(reason), false, "the reason field is pre-filled");

  const typed = form.slice(form.indexOf('name="typed_target"'), form.indexOf('name="typed_target"') + 200);
  assert.equal(/defaultValue|value=/.test(typed), false, "the typed-target field is pre-filled");
});

test("the deliberate step count to a destructive effect is unchanged", async () => {
  const form = await read("src/components/actionForm.tsx");
  // Reason + explicit confirmation are both still REQUIRED, and the typed target still gates the
  // irreversible case. These are the steps FR24 defines; the craft work may not remove one.
  assert.match(form, /name="reason"[\s\S]{0,200}required/, "the reason is no longer required");
  assert.match(form, /name="confirmed" type="checkbox" required/, "the explicit confirmation is gone");
  assert.match(form, /name="typed_target"[\s\S]{0,120}required/, "the typed target is no longer required");
  // The global control stays visually heavier than its per-tenant counterpart.
  const css = await read("src/app/globals.css");
  const global = css.match(/form\.action--global\s*{([^}]*)}/)[1];
  const danger = css.match(/form\.action--danger\s*{([^}]*)}/)[1];
  assert.match(global, /--rule-heavy/, "the fleet-wide control is not visually heavier");
  assert.equal(/--rule-heavy/.test(danger), false, "the per-tenant control is as heavy as the fleet-wide one");
});

test("🔴 the three blast radii are three different-LOOKING controls (FR24)", async () => {
  // This test exists because the property it asserts was FALSE while the previous one passed. A
  // fleet-wide halt and a reversible per-tenant halt both rendered as a red panel with a red submit,
  // differing only in the width of a rule on the left edge — which is a difference in the stylesheet
  // and not a difference an operator scanning a page perceives. FR24's whole content is that the two
  // can never be confused, so the check is on the thing that separates them at a glance: the HUE.
  const css = await read("src/app/globals.css");
  const rule = (selector) => css.match(new RegExp(`${selector}\\s*{([^}]*)}`))?.[1];

  const tiers = {
    "--caution": rule("form\\.action--caution"),
    "--danger": rule("form\\.action--danger"),
    "--global": rule("form\\.action--global"),
  };
  for (const [name, body] of Object.entries(tiers)) {
    assert.ok(body, `there is no ${name} confirmation tier`);
  }

  // The reversible tier carries the ATTENTION hue and the irreversible tiers carry the ALARM hue.
  assert.match(tiers["--caution"], /var\(--warn/, "the reversible tier does not use the attention hue");
  assert.equal(/var\(--danger/.test(tiers["--caution"]), false, "the reversible tier borrows the alarm hue");
  assert.match(tiers["--danger"], /var\(--danger/, "the irreversible tier does not use the alarm hue");
  assert.match(tiers["--global"], /var\(--danger/, "the fleet-wide tier does not use the alarm hue");

  // …and the submit buttons differ too. The button is the thing under the pointer at the moment the
  // action is committed, so a per-tenant submit that looks like a fleet-wide submit defeats the
  // panel's distinction at exactly the wrong instant.
  assert.match(css, /button\.caution\s*{[^}]*background:\s*var\(--warn\)/, "there is no reversible-tier submit");
  assert.match(css, /button\.danger\s*{[^}]*background:\s*var\(--danger\)/, "there is no alarm-tier submit");

  // The tier is chosen ONCE, from the server's classification, in the confirmation component — never
  // per page. A page that picks its own weight is a page that can pick the wrong one.
  const form = await read("src/components/actionForm.tsx");
  assert.match(
    form,
    /const scope = global \? "global" : typedTarget \? "danger" : danger \? "caution" : null/,
    "the confirmation tier is not derived from the server's blast-radius classification in one place",
  );
});

// ── 15.11 · Motion ───────────────────────────────────────────────────────────

test("motion is budgeted, meaningful, and never on the action path", async () => {
  const shared = await readFile(join(ROOT, "..", "design-system", "tokens.css"), "utf8");
  assert.match(shared, /--motion-fast:\s*120ms/);
  assert.match(shared, /--motion-base:\s*180ms/);
  assert.match(shared, /--motion-slow:\s*240ms/);
  assert.match(shared, /--motion-alarm:\s*2000ms/);

  const css = await read("src/app/globals.css");
  // Every duration in the stylesheet comes from the budget: no bespoke timing.
  for (const match of css.matchAll(/(?:transition|animation)[^;]*?(\d+)ms/g)) {
    assert.fail(`a literal duration (${match[1]}ms) bypasses the motion budget`);
  }
  // Every animation uses a NAMED step, and the one step longer than an interaction — `--motion-alarm`
  // — is reserved for hazard exactly as the hazard palette is. A decorative two-second pulse would
  // spend the attention an armed kill switch depends on, which is the FR31 failure arriving by a new
  // route: this time through the motion budget rather than through the colour palette.
  const hazardRule = /danger|warn|alarm|impersonation|degraded|unknown|stale/i;
  for (const match of css.matchAll(/([^{}]*){[^}]*animation:\s*[\w-]+\s+var\(--([\w-]+)\)[^}]*}/g)) {
    const [, rawSelector, token] = match;
    assert.match(token, /^motion-(fast|base|slow|alarm)$/, `animation uses ${token}, which is not a motion token`);
    if (token === "motion-alarm") {
      const selector = rawSelector.trim().split("\n").pop().trim();
      assert.match(
        selector,
        hazardRule,
        `${selector} uses the alarm duration without being a hazard — the step is reserved`,
      );
    }
  }
  // Reduced motion collapses everything, and is declared in the shared layer so both consoles get it.
  assert.match(shared, /@media \(prefers-reduced-motion: reduce\)/);
  assert.match(shared, /animation-duration:\s*0ms\s*!important/);
  assert.match(shared, /transition-duration:\s*0ms\s*!important/);
});

test("no information is carried by motion alone", async () => {
  const css = await read("src/app/globals.css");
  // The value-change mark is a background flash on a value that is ALREADY rendered — remove the
  // animation and the number is still there and still correct.
  const changed = css.match(/@keyframes value-changed\s*{([^}]*}[^}]*)}/s)[1];
  assert.equal(/content:|opacity:\s*0/.test(changed), false, "the value-change mark hides content when not animating");

  // The armed-halt indicator breathes; it must never go out. An indicator that reaches zero opacity
  // is invisible for part of every cycle, so a reader who glances during the trough sees no alarm —
  // and under reduced motion, where the animation collapses to its END state, it would be invisible
  // permanently. The trough stays well clear of zero for both reasons.
  const breath = css.match(/@keyframes alarm-breath\s*{([\s\S]*?)\n}/)[1];
  const troughs = [...breath.matchAll(/opacity:\s*([\d.]+)/g)].map((m) => Number(m[1]));
  assert.ok(troughs.length > 0, "the alarm indicator has no keyframed opacity");
  assert.ok(
    Math.min(...troughs) >= 0.3,
    `the armed-halt indicator fades to ${Math.min(...troughs)} — it must stay visible throughout`,
  );

  // And the word is rendered by the banner regardless, so the animation adds emphasis and never
  // information (FR35).
  const primitives = await read("src/components/primitives.tsx");
  assert.match(primitives, /alarm-banner__word/, "the armed-halt banner states nothing in words");

  const picture = await read("src/components/operatingPicture.tsx");
  // Staleness is text, not a pulse.
  assert.match(picture, /stale=\{stale\}/, "staleness is not passed to a text rendering");
});

// ── §16 · R16–R20: hierarchy, theme, payload, agency (FR38–FR41) ─────────────

test("R16 / FR38 — the stat scale outranks every frame it can sit inside", async () => {
  // The requirement stated as arithmetic. A stat's value must outrank its own label AND the section
  // heading that introduces it — otherwise the frame is louder than the figure, which is the inverted
  // hierarchy this rule exists to correct.
  const shared = await readFile(join(ROOT, "..", "design-system", "tokens.css"), "utf8");
  const css = await read("src/app/globals.css");
  const rem = (source, token) => {
    const m = source.match(new RegExp(`${token}\\s*:\\s*([0-9.]+)rem`));
    return m ? Number(m[1]) : null;
  };

  // The console must resolve its stat size through the SHARED stat scale rather than through a local
  // size that happens to look right — the whole point is that the hierarchy is a property of the
  // token set. Which of the two steps it picks is a density judgement and is allowed to change; that
  // it picks one of them, and that the one it picks wins the arithmetic, is not.
  const used = css.match(/\.stat__value\s*\{[\s\S]*?font-size:\s*var\((--text-stat(?:-sm)?)\)/)?.[1];
  assert.ok(used, "the stat value does not resolve through the shared stat scale");

  const stat = rem(shared, used);
  const sectionTitle = rem(shared, "--text-lg");
  const label = rem(shared, "--text-xs");
  assert.ok(stat && sectionTitle && label, "the stat scale tokens are missing");
  assert.ok(stat > sectionTitle, `a stat (${stat}rem) must outrank a section title (${sectionTitle}rem)`);
  assert.ok(stat > label, `a stat (${stat}rem) must outrank its own label (${label}rem)`);

  // The frame must not out-shout the figure by another route either: the eyebrow treatment every
  // label, section title and column header shares is capped at the smallest step in the ramp, so no
  // amount of restyling the frame can overtake the number it frames.
  assert.match(
    css,
    /\.section__title,[\s\S]{0,200}?\bth\s*\{[\s\S]*?font-size:\s*var\(--text-2xs\)/,
    "the section title and column headers do not share the recessive eyebrow size",
  );
});

test("R17 / FR39 — theme is resolved on the server and lands in the first paint", async () => {
  const layout = await read("src/app/layout.tsx");
  assert.match(layout, /data-theme=\{theme\}/, "the root element does not carry the resolved theme");
  assert.match(layout, /await readTheme\(\)/, "the theme is not read on the server");

  // Same mechanism as density, deliberately: a second way to read a display preference is a second
  // place to get the same thing wrong.
  assert.match(layout, /await readDensity\(\)/);
  const prefs = await read("src/lib/prefs.ts");
  assert.match(prefs, /export async function readTheme/);
  assert.match(prefs, /THEME_COOKIE_OPTIONS = DENSITY_COOKIE_OPTIONS/, "the two preferences have diverged");
});

test("R17 / FR39 — the operator token layer maps both palettes by data-theme", async () => {
  const operator = await read("src/app/tokens.operator.css");
  for (const selector of [':root[data-theme="dark"]', ':root[data-theme="light"]', ':root[data-theme="system"]']) {
    assert.ok(operator.includes(selector), `${selector} has no mapping — the theme control cannot reach it`);
  }

  // The mapping blocks must assign the same names, or a token defined in one theme and forgotten in
  // the other renders unstyled in exactly one of them.
  const names = (selector) => {
    const start = operator.indexOf(selector);
    const open = operator.indexOf("{", start);
    const close = operator.indexOf("\n}", open);
    return [...operator.slice(open, close).matchAll(/^\s*(--[a-z0-9-]+)\s*:/gim)].map((m) => m[1]).sort();
  };
  assert.deepEqual(names(':root[data-theme="light"]'), names(':root[data-theme="dark"]'));
});

test("R18 / FR40 — the bundle scan enforces a payload ceiling and refuses a dev-written tree", async () => {
  const scan = await read("scripts/scan-bundle.mjs");
  assert.match(scan, /PAYLOAD_CEILING_BYTES\s*=\s*[\d_]+/, "there is no stated ceiling");
  assert.match(scan, /DECORATIVE_RUNTIMES/, "the rejected runtimes are not mechanically excluded");
  assert.match(scan, /contaminated\(\)/, "a dev-written tree would be misreported rather than refused");
  assert.match(scan, /shippedFiles\(\)/, "the measurement is not taken from what the build says ships");
});

test("🔴 R20 / FR41 — nothing acts on the operator's behalf", async () => {
  // The trend ledger rejects agentic task execution on this surface without qualification: an agent
  // that "handles multi-step tasks" over admin capabilities is a machine for producing unattributable
  // privileged actions. Stated as a check so it cannot arrive as a "quick action" without somebody
  // deleting this test, which is a conversation rather than a commit.
  const offenders = [];
  for await (const file of walk("src")) {
    if (!file.endsWith(".tsx") && !file.endsWith(".ts")) continue;
    const src = await read(file);
    // A form submitted on mount, or a privileged action fired from an effect rather than a click.
    if (/useEffect\([^)]*(?:requestSubmit|\.submit\(\))/s.test(src)) {
      offenders.push(`${file}: submits a form from an effect`);
    }
  }
  assert.deepEqual(offenders, [], `these surfaces act without the operator: ${offenders.join(", ")}`);

  // And the palette still only navigates — asserted here as well as in the friction-survival test,
  // because that one guards the step count while this one guards the principle.
  const palette = await read("src/components/commandPalette.tsx");
  assert.doesNotMatch(palette, /fetch\(|action=|onSubmit/, "the command palette performs rather than navigates");
});
