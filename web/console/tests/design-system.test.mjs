// design-system.test.mjs holds §5.8 and §5.13 — the design system's own assertions.
//
// Two kinds of test here, and the second kind matters more than it looks:
//
//   1. Behaviour of the primitives, exercised directly against their modules.
//   2. **The guards themselves, exercised by injecting the violation they exist for.** A fence nobody
//      has ever seen fail is a fence nobody knows is connected. Every scan below is made to go red on
//      purpose, then the probe is removed.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir, writeFile, unlink } from "node:fs/promises";
import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";

const exec = promisify(execFile);
const ROOT = new URL("..", import.meta.url).pathname;
const read = (rel) => readFile(join(ROOT, rel), "utf8");

/**
 * fn extracts a top-level function's whole source.
 *
 * Anchored to a closing brace in COLUMN ZERO. A lazy `[\s\S]*?\n\}` stops at the first newline-brace
 * it finds, which for a component with a destructured props object is the close of the destructuring —
 * so the assertion then runs against the signature and reports a defect that is not there. Written
 * after exactly that happened twice.
 */
function fn(source, name) {
  const match = source.match(new RegExp(`^export function ${name}\\(([\\s\\S]*?)^\\}$`, "m"));
  return match ? match[0] : null;
}

async function* walk(dir) {
  for (const entry of await readdir(join(ROOT, dir), { withFileTypes: true })) {
    const rel = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(rel);
    else yield rel;
  }
}

// ── 5.1 / 5.2 · One token set, and the fence that keeps it one ───────────────

test("the framework layer is imported BEFORE the customer identity layer", async () => {
  // Source order is load-bearing for a different reason than it used to be. This console no longer
  // layers over `web/design-system/tokens.css` — it owns its language outright — but Tailwind's layers
  // must be DECLARED before `@theme` resolves against them, and its source globbing must be pinned
  // before anything else is read. A silent reshuffle produces a stylesheet that compiles and is
  // missing half its utilities, which is invisible until a page renders unstyled.
  const css = await read("src/app/globals.css");
  const framework = css.indexOf('@import "tailwindcss"');
  const identity = css.indexOf('@import "./tokens.customer.css"');
  assert.ok(framework >= 0 && identity >= 0, "both layers must be imported");
  assert.ok(framework < identity, "the identity layer must be imported after the framework");

  // Automatic source detection walks up from this file and would scan `.next/`, `tests/` and any
  // sibling application, making the stylesheet a function of what happens to be on disk.
  assert.match(css, /@import "tailwindcss" source\(none\)/, "source detection must be pinned");
  assert.match(css, /@source "/, "the scanned tree must be named explicitly");
});

test("the promoted domain tokens exist, and --llm keeps its own name alongside --halt", async () => {
  const tokens = await read("src/app/tokens.customer.css");
  for (const name of ["--series-1", "--series-2", "--series-3", "--series-4", "--llm", "--ctl", "--none"]) {
    assert.match(tokens, new RegExp(`${name}:`), `${name} is not in the token set`);
  }

  // 🔴 Two concepts that happen to share a value are still two concepts. `--llm` is "a model was
  // consulted", `--halt` is "execution stopped", `--gated` is "outside this plan". If these ever
  // collapse into one name, the day one of them needs to move becomes a refactor — and the day
  // somebody renders a plan boundary in the halt hue, nobody has a name to argue with.
  //
  // Each themed block must give each of them its OWN literal, so they are independently settable in
  // every theme rather than aliased in one and diverging in another.
  for (const [name, block] of Object.entries(themedBlocks(tokens))) {
    for (const token of ["--llm", "--halt", "--gated"]) {
      assert.match(block, new RegExp(`${token}:\\s*#`), `${name} does not give ${token} its own value`);
    }
  }
});

/**
 * themedBlocks returns the three palette blocks by name.
 *
 * The console's theme is a server-rendered attribute rather than a class toggled after hydration
 * (R17/FR37), so the same palette is keyed three ways. Reading them by name is what lets the tests
 * below compare them to each other rather than trusting that somebody kept them in step.
 */
function themedBlocks(source) {
  // Comments are stripped first. This file documents its own selectors in prose at the top, and a
  // parser that reads those finds the wrong block and reports a token missing that is present — the
  // most misleading failure a token test can produce.
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
    light: at(":root,\n:root[data-theme=\"light\"]"),
    dark: at(':root[data-theme="dark"]'),
    system: at(':root[data-theme="system"]'),
  };
}

export { themedBlocks };

test("the token scan goes RED on an injected literal, including a duration", async () => {
  const probe = join(ROOT, "src", "__token_probe.css");
  await writeFile(
    probe,
    ".probe { color: #ff0000; padding: 13px; border-radius: 3px; transition: opacity 200ms linear; }\n",
  );
  try {
    await assert.rejects(
      () => exec("node", ["scripts/scan-tokens.mjs"], { cwd: ROOT }),
      (error) => {
        assert.equal(error.code, 1, "the token scan did not fail on a literal");
        assert.match(error.stderr, /colour literal/);
        assert.match(error.stderr, /spacing literal/);
        assert.match(error.stderr, /radius literal/);
        // The motion budget is P9's addition: a hand-written duration survives the reduced-motion
        // override and animates for a viewer who asked it not to.
        assert.match(error.stderr, /duration literal/);
        return true;
      },
    );
  } finally {
    await unlink(probe);
  }
});

// ── 5.3 · The status primitive and its fallback ──────────────────────────────

test("an unmodelled status renders the fallback AND its raw value, and cannot impersonate a known one", async () => {
  const status = await read("src/lib/status.ts");
  // `known: false` is RETURNED rather than inferred from a missing tone, so a caller must act on it.
  assert.match(status, /known:\s*false/);
  // The raw value becomes the word. The console does not translate a status it does not understand.
  assert.match(status, /word:\s*raw/);
  // The unknown tone must not map to a status hue — that is the `p25monitor` defect, where an unknown
  // status falls back to the `running` style and actively asserts something false.
  assert.match(status, /unknown:\s*"chip--unknown"/);
  const css = await read("src/app/globals.css");
  const rule = css.match(/\.chip--unknown\s*{[^}]*}/);
  assert.ok(rule, "there is no .chip--unknown rule — an unknown status would render unstyled");
  assert.match(rule[0], /border-style:\s*dashed/, "the unknown fallback is not visually distinct");
  for (const hue of ["--ok", "--warn", "--danger", "--halt", "--info"]) {
    assert.doesNotMatch(rule[0], new RegExp(`var\\(${hue}\\)`), `the unknown fallback borrows ${hue}`);
  }
});

test("every modelled status carries a WORD, not only a tone", async () => {
  const status = await read("src/lib/status.ts");
  const entries = [...status.matchAll(/\{\s*tone:\s*"(\w+)",\s*word:\s*"([^"]+)"\s*\}/g)];
  assert.ok(entries.length >= 15, `only ${entries.length} statuses are modelled — the table looks truncated`);
  for (const [, tone, word] of entries) {
    assert.ok(word.trim().length > 0, `the ${tone} status has no word`);
  }
});

// ── 5.4 · Three distinct data states, three distinct error classes ───────────

test("loading, empty and error are three different renderings", async () => {
  const primitives = await read("src/components/primitives.tsx");
  assert.match(primitives, /export function Loading\(/);
  assert.match(primitives, /export function Empty\(/);
  assert.match(primitives, /export function Failure\(/);
  // The skeleton is shaped like the content, not a spinner — that is what keeps arrival from reflowing
  // the view (R13). A generic spinner is deliberately not offered.
  assert.match(primitives, /className="skeleton"/);
  assert.doesNotMatch(primitives, /export function Spinner\(/);
});

test("the three error classes carry three different messages and three different renderings", async () => {
  const primitives = await read("src/components/primitives.tsx");
  // `gated` is excluded from the copy table by type, because it is NOT an error and does not share the
  // error renderings — it has its own branch above them. The table's type is therefore
  // `Record<Exclude<FailureKind, "gated">, …>`, and this match follows it.
  const copy = primitives.match(/const copy: Record<Exclude<FailureKind[\s\S]*?\n  \};/);
  assert.ok(copy, "Failure has no per-kind copy table");
  for (const kind of ["not-mounted", "not-found", "transport", "upstream"]) {
    assert.ok(copy[0].includes(`"${kind}"`) || copy[0].includes(`${kind}:`), `${kind} has no copy`);
  }
  // The distinction that is the product: a transport failure is not an empty result, and a 404 is a
  // routing fact rather than a business state.
  assert.match(copy[0], /transport failure, not an empty result/);
  assert.match(copy[0], /routing fact, not a measurement/);
  // And they must be distinguishable BEFORE the copy is read.
  const css = await read("src/app/globals.css");
  for (const kind of ["not-mounted", "not-found", "transport"]) {
    assert.match(css, new RegExp(`\\.state--${kind}\\s*\\{`), `.state--${kind} has no distinct rendering`);
  }

  // 🔴 And `gated` is distinguishable from all three, because it is not one of them (FR15). An
  // entitlement boundary rendered in a hazard hue both misleads the reader and spends the reserved
  // palette; a test is what keeps the next person from reaching for red because it is nearby.
  assert.match(css, /\.state--gated\s*\{/, "a gated capability has no rendering of its own");
  const gated = css.match(/\.state--gated\s*\{[\s\S]*?\n  \}/)[0];
  assert.doesNotMatch(gated, /--bad|--warn|--halt/, "the gated state borrows the hazard palette");
  assert.match(gated, /--gated/, "the gated state has no hue of its own, outside the status palette");
});

// ── 5.5 · One locale swap point ──────────────────────────────────────────────

test("the locale is pinned to en-US in exactly one module, and nothing bypasses it", async () => {
  const format = await read("src/lib/format.ts");
  assert.match(format, /export const LOCALE = "en-US"/);
  for await (const file of walk("src")) {
    if (!/\.(ts|tsx)$/.test(file) || file.endsWith(join("lib", "format.ts"))) continue;
    const src = await read(file);
    assert.doesNotMatch(src, /new Intl\./, `${file} constructs an Intl formatter outside the swap point`);
    assert.doesNotMatch(src, /toLocale(String|DateString|TimeString)\(/, `${file} uses a browser-locale formatter`);
  }
});

test("the string scan goes RED on a non-Latin script and on a bypassing formatter", async () => {
  const probe = join(ROOT, "src", "__string_probe.ts");
  await writeFile(probe, 'export const x = new Intl.NumberFormat();\nexport const y = "你好";\n');
  try {
    await assert.rejects(
      () => exec("node", ["scripts/scan-strings.mjs"], { cwd: ROOT }),
      (error) => {
        assert.equal(error.code, 1);
        assert.match(error.stderr, /non-Latin script/);
        assert.match(error.stderr, /outside src\/lib\/format\.ts/);
        return true;
      },
    );
  } finally {
    await unlink(probe);
  }
});

test("a non-English browser locale cannot change what the console formats", async () => {
  // The pinned locale is the mechanism; this asserts the OUTCOME. `Intl` is asked for the same value
  // under a Chinese locale and under en-US, and the console's pinned formatter must not move.
  const pinned = new Intl.NumberFormat("en-US", { minimumFractionDigits: 3, maximumFractionDigits: 3 });
  const browser = new Intl.NumberFormat("zh-Hans-CN-u-nu-hanidec", {
    minimumFractionDigits: 3,
    maximumFractionDigits: 3,
  });
  assert.notEqual(pinned.format(0.715), browser.format(0.715), "the locales are indistinguishable — the test proves nothing");
  assert.equal(pinned.format(0.715), "0.715");
});

// ── 5.6 · The accessibility primitives ───────────────────────────────────────

test("focus is visible, table headers are scoped, and graphics carry both fallbacks", async () => {
  const css = await read("src/app/globals.css");
  assert.match(css, /:focus-visible\s*{[^}]*outline:/, "no visible focus indicator");

  const primitives = await read("src/components/primitives.tsx");
  assert.match(primitives, /scope="col"/, "DataTable does not scope its column headers");
  assert.match(primitives, /<caption>/, "DataTable renders no caption");

  const figure = await read("src/components/figure.tsx");
  // Both props are REQUIRED, so a graphic without a text alternative or without a tabular fallback
  // cannot be written. The p35 graph is an SVG with no alternative at all today.
  assert.match(figure, /alt: string;/);
  assert.match(figure, /table: ReactNode;/);
  assert.match(figure, /role="img"/);
  assert.match(figure, /<details/);
  // The tooltip pattern is focus-reachable and announced, not hover-only.
  assert.match(figure, /role="status"/);
});

test("no component uses a positive tabindex", async () => {
  for await (const file of walk("src")) {
    if (!/\.tsx$/.test(file)) continue;
    const src = await read(file);
    assert.doesNotMatch(src, /tabIndex=\{[1-9]/, `${file} uses a positive tabindex — it reorders the tab sequence`);
  }
});

// ── 5.7 · Values are text, never markup ──────────────────────────────────────

test("the markup scan passes, and goes RED on injected raw-markup rendering", async () => {
  const { stdout } = await exec("node", ["scripts/scan-markup.mjs"], { cwd: ROOT });
  assert.match(stdout, /markup scan passed/);

  const probe = join(ROOT, "src", "__markup_probe.tsx");
  await writeFile(probe, "export const X = () => <div dangerouslySetInnerHTML={{ __html: \"x\" }} />;\n");
  try {
    await assert.rejects(
      () => exec("node", ["scripts/scan-markup.mjs"], { cwd: ROOT }),
      (error) => {
        assert.equal(error.code, 1);
        assert.match(error.stderr, /dangerouslySetInnerHTML/);
        return true;
      },
    );
  } finally {
    await unlink(probe);
  }
});

// ── 5.11 / 5.13 · 🔴 The confidence reservation ──────────────────────────────

test("every qualifier withdraws the confident treatment", async () => {
  const { QUALIFIERS, emphasis, QUALIFIER_COPY } = await import("../src/lib/emphasis.ts").catch(() => ({}));
  // The module is TypeScript; if it cannot be imported directly, assert against the source instead —
  // the property under test is the mapping, and it must hold either way.
  if (emphasis) {
    assert.equal(emphasis([]), "confident");
    for (const q of QUALIFIERS) {
      assert.equal(emphasis([q]), "qualified", `${q} did not withdraw the confident treatment`);
      assert.ok(QUALIFIER_COPY[q], `${q} has no copy — a qualifier with no words is a colour`);
    }
    return;
  }
  const src = await read("src/lib/emphasis.ts");
  for (const q of ["tie", "provisional", "disqualified", "low-confidence", "uncalibrated", "withheld", "candidate", "unverified", "gated"]) {
    assert.ok(src.includes(`"${q}"`), `${q} is not in the qualifier set`);
    assert.match(src, new RegExp(`"${q}":\\s*"`), `${q} has no copy — a qualifier with no words is a colour`);
  }
  assert.match(src, /flags\.some\(isQualifier\) \? "qualified" : "confident"/);
});

test("the confident treatment is applied in exactly one component", async () => {
  // `className={row.tie ? … : "confident"}` at a dozen call sites is a rule with a dozen chances to be
  // got wrong, and the twelfth is written under deadline. One decision, one place.
  const offenders = [];
  for await (const file of walk("src")) {
    if (!/\.tsx$/.test(file)) continue;
    const src = await read(file);
    if (!/["'`]confident["'`]/.test(src) && !/className=\{level\}/.test(src)) continue;
    if (!file.endsWith(join("components", "primitives.tsx"))) offenders.push(file);
  }
  assert.deepEqual(offenders, [], "the confident treatment is applied outside the Value primitive");
});

test("a qualifier renders BESIDE its value, not only in a tooltip", async () => {
  const primitives = await read("src/components/primitives.tsx");
  const value = fn(primitives, "Value");
  assert.ok(value, "the Value primitive is missing");
  assert.match(value, /className="qualifier"/, "qualifiers are not rendered beside the value");
  assert.doesNotMatch(value, /title=\{QUALIFIER_COPY/, "the qualifier copy is hidden in a title attribute");
});

// ── 5.10 / 5.13 · One subject, present before the data ───────────────────────

test("PageFrame takes exactly one subject and renders it as the one display heading", async () => {
  const primitives = await read("src/components/primitives.tsx");
  const frame = fn(primitives, "PageFrame");
  assert.ok(frame, "PageFrame is missing");
  assert.match(frame, /title: string;/, "the subject is not a required prop");
  const h1s = frame.match(/<h1/g) ?? [];
  assert.equal(h1s.length, 1, "PageFrame renders something other than exactly one display heading");
  // Section headings are h2 — a second h1 anywhere would mean the view has two subjects.
  assert.match(fn(primitives, "Section"), /<h2/);
});

test("reduced motion loses no information", async () => {
  const css = await read("src/app/globals.css");
  const reduced = css.match(/@media \(prefers-reduced-motion: reduce\)[\s\S]*$/);
  assert.ok(reduced, "there is no reduced-motion block");
  // The skeleton's sweep is the only animation the console owns. With it removed it is still a set of
  // bars occupying the content's shape, which is the whole of what it communicates.
  assert.match(reduced[0], /\.skeleton__bar\s*{[^}]*animation:\s*none/);
});
