// gen-search-index.mjs builds the documentation search index at BUILD time (task 4.4, Decision 9).
//
// # 🔴 What this index ranks over, and what it therefore cannot find
//
//   RANKS OVER   page titles, section names, every heading, and each page's lead paragraph (`summary`)
//   DOES NOT     search full body text
//
// That limit is disclosed in three places on purpose — here, in the component that renders the box, and
// in the zero-result message a reader actually sees — because the failure it produces is silent and
// misleading: a reader searches for a term that appears in a paragraph, gets nothing, and concludes the
// product has no documentation about a thing it documents thoroughly.
//
// It is a first cut, and the trigger to revisit it is corpus size (PRD OQ6, left open deliberately). The
// index grows linearly with the corpus and is shipped to the browser, so the payload ceiling in
// `scan-bundle.mjs` is the backstop that makes "we should revisit this" arrive as a build failure rather
// than as an opinion.
//
// # Why there is no hosted search service
//
// ADR-011's two reasons, unchanged: a third party with a view of every query on the page where customers
// read what they are agreeing to (level 1, security), and an air-gapped P19 deployment with no egress at
// all, where a hosted search box is an input that spins forever (level 2, stability).
//
// # What this deliberately does not check
//
// Nothing. It is a GENERATOR, not a fence. It does not verify that a page is reachable (`scan-links`), or
// that its claims are shipped (`scan-docs-claims`). A generator that also validated would hide which of
// the two failed.

import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";
import { loadDocs, sectionLabel } from "../src/lib/reading/corpus.ts";

const OUT = join(process.cwd(), "src", "generated", "search-index.json");
const RANKS_OVER = "page titles, section names, headings and lead paragraphs";

async function main() {
  const pages = await loadDocs();
  const entries = pages
    .filter((page) => page.slug !== "index")
    .map((page) => ({
      route: page.route,
      title: page.frontMatter.title,
      section: sectionLabel(page.section),
      summary: page.frontMatter.summary,
      headings: page.headings.map((heading) => heading.text),
    }))
    .sort((a, b) => a.route.localeCompare(b.route));

  await mkdir(join(process.cwd(), "src", "generated"), { recursive: true });
  await writeFile(
    OUT,
    `${JSON.stringify(
      {
        generated_from: "web/console/content/docs/en/**",
        ranks_over: RANKS_OVER,
        entries,
      },
      null,
      2,
    )}\n`,
    "utf8",
  );
  console.log(`search index generated: ${entries.length} page(s), ranking over ${RANKS_OVER}.`);
}

main().catch((error) => {
  console.error("search index generation FAILED:", error.message);
  process.exit(1);
});
