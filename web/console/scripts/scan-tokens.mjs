// scan-tokens.mjs is the BUILD-TIME design-language fence (R1, FR18, task 5.2; motion budget, FR28).
//
// # Why this exists as a machine and not as a paragraph
//
// The design language in this repository has already forked THREE WAYS across four files — `--muted`
// at two values, `--line` at two, card radius at 8px and 10px, chip radius at 4px and 999px, px
// against rem, a three-family font stack against a five-family one, and an entire second status
// vocabulary. Nobody decided that. It happened one "just here" literal at a time, and the rule
// against it existed the whole while as a comment.
//
// The lesson this repository keeps re-learning: a rule that cannot fail a build is a suggestion.
//
// # What it checks
//
//   colour        hex, rgb()/rgba(), hsl()/hsla() — anywhere
//   spacing       padding / margin / gap / row-gap / column-gap — a length literal
//   type size     font-size — a length literal
//   radius        border-radius — a length literal
//   font family   font-family — a literal stack
//   duration      transition-duration / animation-duration, and the duration slot of the shorthands
//
// The last one is P9's addition and it is not cosmetic: FR28 requires every duration to come from the
// motion budget, so that `prefers-reduced-motion` can collapse ALL of them at once. A hand-written
// `200ms` survives the reduced-motion override and animates for a viewer who asked it not to.
//
// `0`, `auto`, percentages and `calc(...)` over tokens are always fine — they cannot drift. Widths,
// heights and offsets are deliberately out of scope: they are layout measures rather than the design
// language's categories, and a fence that cried wolf on `minmax(22rem, 1fr)` would be switched off
// within a week.
//
// # Where literals ARE allowed
//
//   src/app/tokens.customer.css        this surface's whole design language
//
// It used to be two files: the shared `web/design-system/tokens.css` plus an identity layer over it.
// This console no longer draws from the shared system — its type ramp, spacing rhythm, radius family
// and component set come from the design system adopted in the P9 rebuild, and a partial override of a
// different system produces a third system nobody designed. The operator console keeps the shared
// file; this one owns its language outright, in one place.
//
// Adding a second exception is a design-language change, argued for before it is written.

import { readdir, readFile } from "node:fs/promises";
import { join, relative } from "node:path";
import process from "node:process";

const ROOT = process.cwd();

const TOKEN_LAYERS = [join("src", "app", "tokens.customer.css")];
const SCAN_DIRS = [join(ROOT, "src")];

// The lookbehind and lookahead keep this from flagging two things that are hex-SHAPED and not colours:
// a URL fragment (`/app#board-section` is a control) and an HTML numeric entity (`&#8984;` is a
// character). A fence that failed the build over either would be switched off by lunchtime — and the
// entity case is not hypothetical, it was the first thing this scan caught that it should not have.
const COLOUR = /(?<!&)#[0-9a-fA-F]{3,8}(?![0-9a-zA-Z_-])|\brgba?\s*\(|\bhsla?\s*\(/;
const SPACING =
  /\b(?:padding|margin|gap|row-gap|column-gap)(?:-(?:top|right|bottom|left|inline|block))?(?:-(?:start|end))?\s*:\s*([^;{}]+)/g;
const FONT_SIZE = /\bfont-size\s*:\s*([^;{}]+)/g;
const RADIUS = /\bborder-radius\s*:\s*([^;{}]+)/g;
const FONT_FAMILY = /\bfont-family\s*:\s*([^;{}]+)/g;
const DURATION = /\b(?:transition|animation)(?:-duration)?\s*:\s*([^;{}]+)/g;

const LENGTH_LITERAL = /(?<![\w-])\d*\.?\d+(px|rem|em|ch|vh|vw|pt)\b/;
const TIME_LITERAL = /(?<![\w-])\d*\.?\d+m?s\b/;
const FAMILY_LITERAL = /(?:^|,)\s*(?:"[^"]+"|'[^']+'|(?:ui-|system-|-apple-)?[a-zA-Z][\w-]*)\s*(?:,|$)/;

function isTokenLayer(file) {
  const rel = relative(ROOT, file);
  return TOKEN_LAYERS.some((layer) => rel.endsWith(layer));
}

async function* walk(dir) {
  let entries;
  try {
    entries = await readdir(dir, { withFileTypes: true });
  } catch {
    return;
  }
  for (const entry of entries) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(full);
    else if (/\.(css|tsx|ts)$/.test(entry.name)) yield full;
  }
}

/** stripComments removes CSS and line comments so prose ABOUT a colour is not read as a colour. */
function stripComments(source) {
  return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
}

function checkProperty(source, pattern, category, literal, findings, file) {
  pattern.lastIndex = 0;
  let match;
  while ((match = pattern.exec(source)) !== null) {
    const value = match[1];
    // A value that resolves entirely through tokens is correct however it is composed.
    if (/var\(--/.test(value) && !literal.test(value.replace(/var\([^)]*\)/g, ""))) continue;
    if (literal.test(value)) {
      findings.push(`${file}: ${category} literal in \`${match[0].trim().slice(0, 70)}\``);
    }
  }
}

/**
 * checkApplyTargets catches `@apply` of a PROJECT CLASS rather than a Tailwind utility.
 *
 * # 🔴 The failure this exists for, which cost a real afternoon
 *
 * Tailwind v4's `@apply` takes utilities. Given a class this file defines itself — `@apply chip;`,
 * `@apply stat__label;` — it does not error the build: it produces NOTHING, and the whole
 * `@layer components` block collapses with it. `npm run build` stays green, `scan-tokens` stays green,
 * every test stays green, and the product renders as unstyled HTML on every page.
 *
 * That is the worst shape a defect can have here: total, invisible to every gate, and discovered only
 * by opening the console in a browser — which is exactly what happened. The 114KB stylesheet shipped
 * as 5KB of font declarations.
 *
 * The remedy is not "remember not to". It is this: every class this stylesheet defines is collected,
 * and an `@apply` naming one of them fails, saying what to write instead.
 */
function checkApplyTargets(source, findings, rel) {
  // Every class this file defines. `\.name {` at the start of a rule.
  const defined = new Set([...source.matchAll(/^\s*\.([a-zA-Z][\w-]*)/gm)].map(([, name]) => name));
  if (defined.size === 0) return;
  for (const [, args] of source.matchAll(/@apply\s+([^;{}]+);/g)) {
    for (const token of args.trim().split(/\s+/)) {
      // Strip a variant prefix (`hover:`, `motion-safe:`) and any trailing `!`.
      const bare = token.split(":").pop().replace(/!$/, "");
      if (!defined.has(bare)) continue;
      findings.push(
        `${rel}: \`@apply ${bare}\` names a class this stylesheet defines, not a Tailwind utility. ` +
          "Tailwind v4 compiles it to NOTHING and takes the surrounding @layer with it — the build " +
          "stays green and every page renders unstyled. Spell out the declarations, or put the class " +
          "in the markup beside its modifier.",
      );
    }
  }
}

async function main() {
  const findings = [];
  let scanned = 0;

  for (const dir of SCAN_DIRS) {
    for await (const file of walk(dir)) {
      if (isTokenLayer(file)) continue;
      scanned += 1;
      const rel = relative(ROOT, file);
      const source = stripComments(await readFile(file, "utf8"));

      const colour = source.match(COLOUR);
      if (colour) findings.push(`${rel}: colour literal \`${colour[0]}\` — use a token`);

      checkProperty(source, SPACING, "spacing", LENGTH_LITERAL, findings, rel);
      checkProperty(source, FONT_SIZE, "type-size", LENGTH_LITERAL, findings, rel);
      checkProperty(source, RADIUS, "radius", LENGTH_LITERAL, findings, rel);
      checkProperty(source, FONT_FAMILY, "font-family", FAMILY_LITERAL, findings, rel);
      checkProperty(source, DURATION, "duration", TIME_LITERAL, findings, rel);
      checkApplyTargets(source, findings, rel);
    }
  }

  if (scanned === 0) {
    console.error("token scan: no source files found — is the working directory the console root?");
    process.exit(2);
  }

  if (findings.length > 0) {
    console.error(`token scan FAILED — ${findings.length} finding(s):`);
    for (const f of findings) console.error(`  - ${f}`);
    console.error(
      "\nEvery colour, spacing, type-size, radius, font-family and duration resolves through the token layer:\n" +
        "  src/app/tokens.customer.css      (this console's design language)\n" +
        "That file documents the language these tokens express, and is the only place a literal belongs.",
    );
    process.exit(1);
  }

  console.log(
    `token scan passed: ${scanned} file(s), no colour, spacing, type-size, radius, font or duration ` +
      "literal, and no `@apply` of a project class.",
  );
}

main().catch((error) => {
  console.error("token scan errored:", error);
  process.exit(2);
});
