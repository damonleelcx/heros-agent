// scan-strings.mjs is the BUILD-TIME language and locale fence (R4, FR23, task 5.5).
//
// It fails the build on two things:
//
//   1. A non-ASCII character in source — the `lang="zh-CN"` approval queue and the Chinese comment
//      that survives inside `p2.html` are what this prevents recurring. 🔴 `code-and-ui-language`
//      makes source, comments and UI strings English without exception.
//   2. An `Intl` formatter constructed anywhere but `src/lib/format.ts`, or constructed with no
//      locale at all.
//
// # Why the second one matters in an English-only product
//
// `Intl` follows the BROWSER's locale unless told otherwise. On a Chinese-locale browser
// `new Intl.RelativeTimeFormat().format(0, "second")` renders "现在" — next to an English label. The
// product ships a mixed-language string inside one sentence, on a machine nobody on the team owns,
// and no build, type-check or unit test can see it.
//
// The allowlist is one file. That is the point: R4's single swap point is the seam a future i18n
// phase changes, and a second formatter somewhere else is a place that phase would miss.

import { readdir, readFile } from "node:fs/promises";
import { join, relative } from "node:path";
import process from "node:process";

const ROOT = process.cwd();
const LOCALE_MODULE = join("src", "lib", "format.ts");

// What it looks for is a NON-LATIN SCRIPT, not a non-ASCII byte.
//
// The distinction is the difference between a fence that works and one that gets switched off. The
// defect being prevented is a Chinese UI string and a Chinese source comment; the characters that
// actually indicate it are CJK, Kana, Hangul, Cyrillic, Arabic, Hebrew, Devanagari and Thai. Typographic
// punctuation is not the defect: `—` is the null placeholder every legacy page already renders, `±`
// appears in `score ± [ci_low, ci_high]`, `→` in the score-breakdown copy P4-15 specifies, and `κ` in
// the judge-agreement label. A fence that failed on those would be failing on the product's own
// vocabulary, and the first person blocked by it would delete it.
const NON_LATIN_SCRIPT =
  /[぀-ヿ㐀-䶿一-鿿가-힯Ѐ-ӿ؀-ۿ֐-׿ऀ-ॿ฀-๿]/u;

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
    else if (/\.(tsx|ts|css)$/.test(entry.name)) yield full;
  }
}

async function main() {
  const findings = [];
  let scanned = 0;

  for await (const file of walk(join(ROOT, "src"))) {
    scanned += 1;
    const rel = relative(ROOT, file);
    const source = await readFile(file, "utf8");

    source.split("\n").forEach((line, index) => {
      const match = line.match(NON_LATIN_SCRIPT);
      if (match) {
        findings.push(
          `${rel}:${index + 1}: non-Latin script character ${JSON.stringify(match[0])} — source, comments and UI strings are English`,
        );
      }
    });

    if (rel === LOCALE_MODULE) continue;

    // An Intl formatter anywhere else, with or without a locale.
    const intl = source.match(/new Intl\.[A-Za-z]+\s*\(/);
    if (intl) {
      findings.push(
        `${rel}: constructs ${intl[0].trim()} outside src/lib/format.ts — every formatter resolves through the one swap point`,
      );
    }
    // toLocaleString / toLocaleDateString and friends follow the browser locale just as surely.
    const toLocale = source.match(/\.toLocale(?:String|DateString|TimeString)\s*\(/);
    if (toLocale) {
      findings.push(`${rel}: uses ${toLocale[0].trim()} — it follows the browser's locale; use src/lib/format.ts`);
    }
    if (/navigator\.language/.test(source)) {
      findings.push(`${rel}: reads navigator.language — the console's locale is pinned to en-US`);
    }
  }

  if (scanned === 0) {
    console.error("string scan: no source files found — is the working directory the console root?");
    process.exit(2);
  }

  if (findings.length > 0) {
    console.error(`string scan FAILED — ${findings.length} finding(s):`);
    for (const f of findings) console.error(`  - ${f}`);
    console.error("\nUI strings are English; all Intl formatting resolves through src/lib/format.ts (en-US).");
    process.exit(1);
  }

  console.log(`string scan passed: ${scanned} file(s), English-only, one pinned locale.`);
}

main().catch((error) => {
  console.error("string scan errored:", error);
  process.exit(2);
});
