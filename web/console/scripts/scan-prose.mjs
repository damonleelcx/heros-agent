// scan-prose.mjs fails the build when a working route grows back into a document (P37 FR10, task 6.1).
//
// # The failure this exists to stop
//
// The console's working routes reached 31,628 words while everyone involved believed they were being
// concise. That is not a discipline problem — it is what happens when the only rule is "be concise",
// which has a different answer for every reviewer and every deadline. A number a build enforces has one
// answer, and the argument happens once, in `prose-budgets.mjs`, in front of a reviewer.
//
// # 🔴 What this scan CANNOT see, stated here so nobody cites it past its limits (design D3)
//
// It measures VOLUME. It cannot tell text that MOVED from text that was REARRANGED. The same content
// passes as three shorter blocks, and — before the label threshold in `prose.mjs` — as a run of
// four-word fragments.
//
// So it is paired, not trusted:
//
//   · FR11 forbids tooltips, accordions and modals as destinations, and this scan refuses one on a
//     working route outright. Those are the three ways text gets hidden while appearing to be kept.
//   · §4.5 requires the pull request to enumerate every moved block and where it landed, and a block
//     with no destination fails review.
//   · fence 6.10 fails the build on a destination link that does not resolve.
//
// A green run here is evidence about how many words a route ships. It is not evidence that anything was
// moved rather than rearranged, and it must never be cited as though it were.
//
//   npm run scan:prose

import { readdir, readFile } from "node:fs/promises";
import { join, relative } from "node:path";
import process from "node:process";

import { proseBlocks, totalWords, stripComments } from "./lib/prose.mjs";
import { DISCLOSURE_TAGS, LEDE_WORDS, PROSE_BUDGETS } from "./lib/prose-budgets.mjs";

const ROOT = process.cwd();
// HEROS_APP_ROOT exists for ONE caller: `tests/p37-prose.test.mjs`, which must watch this fence go red
// against a deliberately over-budget fixture without editing the real tree. A fixture that had to write
// into `src/` to prove a fence works is a fixture that can leave the tree broken.
const APP = process.env.HEROS_APP_ROOT?.trim() || join(ROOT, "src", "app", "app");

/** routes walks the app directory and returns every route that has a `page.tsx`, with its own files. */
async function routes(dir = APP, base = "/app") {
  const entries = await readdir(dir, { withFileTypes: true });
  const out = [];
  if (entries.some((e) => e.isFile() && e.name === "page.tsx")) {
    out.push({
      route: base,
      // A route's own files only. A nested directory is its own route with its own budget, and counting
      // a child's words against a parent would make a route fail for text nobody put on it.
      files: entries
        .filter((e) => e.isFile() && /\.(tsx|ts)$/.test(e.name))
        .map((e) => join(dir, e.name)),
    });
  }
  for (const entry of entries) {
    if (entry.isDirectory()) out.push(...(await routes(join(dir, entry.name), `${base}/${entry.name}`)));
  }
  return out;
}

/**
 * bodiesOf returns the inner text of every `<Tag …>…</Tag>` in a source file.
 *
 * A brace-free scan rather than a parse: this runs before the build and must not need a TypeScript
 * toolchain to answer a question about markup. Nesting of the SAME tag inside itself would confuse it,
 * and no surface in this console nests a tooltip in a tooltip — stated so the limit is known rather than
 * discovered.
 */
function bodiesOf(source, tag) {
  const out = [];
  const open = new RegExp(`<${tag}(\\s[^>]*)?>`, "g");
  for (const match of source.matchAll(open)) {
    const from = (match.index ?? 0) + match[0].length;
    const close = source.indexOf(`</${tag}>`, from);
    out.push(close < 0 ? source.slice(from) : source.slice(from, close));
  }
  return out;
}

async function main() {
  const findings = [];
  const found = await routes();
  let counted = 0;

  for (const { route, files } of found) {
    const budget = PROSE_BUDGETS[route];
    if (budget === undefined) {
      findings.push(
        `${route}: no prose budget. Add one to scripts/lib/prose-budgets.mjs — a route with no stated ` +
          `ceiling is where the prose goes, and nothing goes red while it does.`,
      );
      continue;
    }

    let words = 0;
    let lede = null;
    for (const file of files) {
      const source = await readFile(file, "utf8");
      words += totalWords(source);

      // FR10 — at most ONE lede, at most LEDE_WORDS. The lede is the `PageFrame` prop, so it is checked
      // where it is declared rather than by taking the first block on the page.
      const stripped = stripComments(source);
      for (const match of stripped.matchAll(/lede=(?:"([^"]*)"|\{"([^"]*)"\})/g)) {
        const text = (match[1] ?? match[2] ?? "").trim();
        const count = text.split(/\s+/).filter((t) => /[A-Za-z]/.test(t)).length;
        if (count > LEDE_WORDS) {
          findings.push(
            `${relative(ROOT, file)}: the lede is ${count} words and the cap is ${LEDE_WORDS}. One ` +
              `sentence saying what the surface is for; the rest belongs on the reading surface.`,
          );
        }
        lede = lede ?? count;
      }

      // 🔴 FR11 — the destinations a word count cannot see.
      //
      // The ban is on a disclosure widget CARRYING PROSE, not on the element existing: a readout that
      // renders the reader's own values in flow hides nothing, and deleting one to please a scan is the
      // fix being applied at the wrong unit. See `prose-budgets.mjs`'s note on the false positive that
      // forced this distinction.
      for (const [tag, what] of DISCLOSURE_TAGS) {
        for (const body of bodiesOf(stripped, tag)) {
          const hidden = proseBlocks(body);
          if (hidden.length === 0) continue;
          findings.push(
            `${relative(ROOT, file)}: ${what} carries ${hidden.reduce((n, b) => n + b.words, 0)} words of ` +
              `static prose — "${hidden[0].text.slice(0, 60)}…". Moved text lands in a named ` +
              `reading-surface section, never in a disclosure widget: a tooltip is unreachable by ` +
              `keyboard on half the controls that have one, an accordion is a paragraph nobody opens, ` +
              `and a modal is a paragraph that interrupts. The word count above cannot see any of them.`,
          );
        }
      }
    }

    counted += 1;
    if (words > budget) {
      findings.push(
        `${route}: ${words} words of static prose, over its ${budget}-word budget by ${words - budget}. ` +
          `A block whose content is identical for every reader is documentation and belongs on the ` +
          `reading surface, with one link back per section.`,
      );
    }
  }

  // Both directions: a budget for a route that no longer exists is a number nobody agreed to.
  const live = new Set(found.map((r) => r.route));
  for (const route of Object.keys(PROSE_BUDGETS)) {
    if (!live.has(route)) findings.push(`${route}: budgeted, but there is no such route any more`);
  }

  if (findings.length > 0) {
    console.error(`prose scan FAILED — ${findings.length} finding(s):`);
    for (const finding of findings) console.error(`  - ${finding}`);
    console.error(
      "\nThe budgets are in scripts/lib/prose-budgets.mjs, and the rule that decides what moves is one " +
        "sentence: if a block is the same for every reader it is documentation; if it changes with the " +
        "reader's data it is the product.\n" +
        "🔴 This scan measures VOLUME ONLY. It cannot tell moved text from rearranged text, and a green " +
        "run is never evidence that content found a destination.",
    );
    process.exit(1);
  }

  console.log(
    `prose scan passed: ${counted} working route(s) within budget, ` +
      `${LEDE_WORDS}-word lede cap respected, no disclosure widget used as a destination. ` +
      `Volume only — see the header for what this cannot see.`,
  );
}

main().catch((error) => {
  console.error("prose scan errored:", error);
  process.exit(2);
});
