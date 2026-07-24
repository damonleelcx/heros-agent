// scan-tokens.mjs is the BUILD-TIME design-language fence (FR29, task 15.3).
//
// # Why this exists as a machine and not as a paragraph
//
// "Use the tokens" is a rule with a demonstrated failure rate. It is violated the same way every
// time: someone needs a colour that is *almost* `--border`, a gap that is *almost* `--space-3`, a
// heading that is *almost* `--text-lg`, and the literal goes in "just here". Six months later the
// console has four greys, three gaps and no way to restyle without visiting every file — and, on this
// surface specifically, a hazard colour that has quietly become decorative, which is the FR31 failure
// the kill switch's legibility depends on.
//
// The same lesson the credential scan encodes: a rule that cannot fail a build is a suggestion.
//
// # What it checks, precisely
//
// Inside the console's own stylesheets and components, these four categories must resolve through
// `var(--…)`:
//
//   colour        hex, rgb()/rgba(), hsl()/hsla() — anywhere
//   spacing       padding / margin / gap / row-gap / column-gap — a length literal
//   type size     font-size — a length literal
//   radius        border-radius — a length literal
//
// `0`, `auto`, percentages and `calc(...)` over tokens are always fine — they are not values that can
// drift. Widths, heights, offsets and flex bases are deliberately OUT of scope: they are layout
// measures rather than the design language's four categories, and a fence that cried wolf on
// `minmax(22rem, 1fr)` would be switched off within a week.
//
// # Where literals ARE allowed
//
// The two token layers, and only those:
//   web/design-system/tokens.css        the shared system
//   src/app/tokens.operator.css         the operator identity
//
// Adding a third exception here is a design-language change and should be argued for in
// web/design-system/README.md first.

import { readdir, readFile } from "node:fs/promises";
import { join, relative } from "node:path";
import process from "node:process";

const ROOT = process.cwd();

/** The token layers. A literal here is the point; everywhere else it is a defect. */
const TOKEN_LAYERS = [join("src", "app", "tokens.operator.css"), join("..", "design-system", "tokens.css")];

const SCAN_DIRS = [join(ROOT, "src")];

// A hex colour is 3, 4, 6 or 8 hex digits and nothing adjacent. The trailing lookahead is what keeps
// this from flagging URL fragments that happen to be hex-shaped — `/registry#add-model` is a link to a
// control, not a colour, and a fence that failed the build over it would be switched off by lunchtime.
const COLOUR = /#[0-9a-fA-F]{3,8}(?![0-9a-zA-Z_-])|\brgba?\s*\(|\bhsla?\s*\(/;
const SPACING = /\b(?:padding|margin|gap|row-gap|column-gap)(?:-(?:top|right|bottom|left|inline|block))?\s*:\s*([^;{}]+)/g;
const FONT_SIZE = /\bfont-size\s*:\s*([^;{}]+)/g;
const RADIUS = /\bborder-radius\s*:\s*([^;{}]+)/g;
const LENGTH_LITERAL = /(?<![\w-])\d*\.?\d+(px|rem|em|ch|vh|vw|pt)\b/;

function isTokenLayer(file) {
  const rel = relative(ROOT, file);
  return TOKEN_LAYERS.some((layer) => rel.endsWith(layer.replace(/^\.\.[\\/]/, "")));
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

/** stripComments removes CSS and line comments so prose about colours is not flagged as a colour. */
function stripComments(source) {
  return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
}

function checkProperty(source, pattern, category, findings, file) {
  pattern.lastIndex = 0;
  let match;
  while ((match = pattern.exec(source)) !== null) {
    const value = match[1];
    if (LENGTH_LITERAL.test(value)) {
      findings.push(`${file}: ${category} literal in \`${match[0].trim().slice(0, 60)}\``);
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

      checkProperty(source, SPACING, "spacing", findings, rel);
      checkProperty(source, FONT_SIZE, "type-size", findings, rel);
      checkProperty(source, RADIUS, "radius", findings, rel);
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
      "\nEvery colour, spacing, type-size and radius value resolves through the token layers:\n" +
        "  web/design-system/tokens.css     (shared system)\n" +
        "  src/app/tokens.operator.css      (operator identity)\n" +
        "See web/design-system/README.md for the language these tokens express.",
    );
    process.exit(1);
  }

  console.log(`token scan passed: ${scanned} file(s), no colour, spacing, type-size or radius literal.`);
}

main().catch((error) => {
  console.error("token scan errored:", error);
  process.exit(2);
});
