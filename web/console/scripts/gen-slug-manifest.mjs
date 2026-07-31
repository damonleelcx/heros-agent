// gen-slug-manifest.mjs emits `docs/slug-manifest.json` — the published anchor contract (task 4.1,
// Decision 8).
//
// # Why anchors are a contract and not a detail
//
// CLI error messages, console empty states and API error bodies deep-link into documentation. A renamed
// heading therefore breaks a link that ships INSIDE A BINARY THE CUSTOMER ALREADY INSTALLED — there is no
// deploy that fixes it for them. The console already applies this discipline to its own legacy routes,
// which resolve by permanent redirect rather than 404.
//
// # 🔴 Why this runs from the same code the pages render from
//
// The manifest is emitted by importing `src/lib/reading/corpus.ts` — the exact module the pages use — so
// a slug in this file and a slug on the page cannot be produced by two different rules. A generator with
// its own copy of `slugify` is a generator that eventually publishes an anchor the page does not have,
// and the first person to find out is a customer following an error message.
//
// # The manifest is CHECKED IN
//
// Not gitignored. The whole point is that removing or renaming a slug shows up as a DIFF in review, and
// `scan-links.mjs` fails the build on a slug that disappeared without a redirect being added in the same
// change. A build product nobody can diff cannot carry that rule.
//
// # What this deliberately does not check
//
// That the anchors are GOOD — descriptive, stable-by-intent, or worth citing. It records what exists.
// Whether a heading should have been named something a CLI message can point at for years is a review
// judgement, and no generator can make it.

import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import process from "node:process";
import { loadDocs, loadLegal } from "../src/lib/reading/corpus.ts";

// HEROS_SLUG_MANIFEST exists for ONE caller: the fence fixtures, which must generate a manifest for a
// deliberately broken corpus without overwriting the real, checked-in one.
const OUT = process.env.HEROS_SLUG_MANIFEST ?? join(process.cwd(), "docs", "slug-manifest.json");
const OUT_DIR = dirname(OUT);

async function main() {
  const [docs, legal] = await Promise.all([loadDocs(), loadLegal()]);

  const pages = [
    ...docs.map((page) => ({
      route: page.route,
      source: page.sourcePath,
      content_hash: page.contentHash,
      anchors: page.headings.map((heading) => heading.slug),
    })),
    // A legal version carries its content hash INTO the manifest, because that is what makes
    // "the text changed and the version did not" a checkable statement rather than a review habit
    // (scan-legal.mjs). It is the same hash a consent record stores.
    ...legal.map((document) => ({
      route: document.versionRoute,
      source: document.sourcePath,
      content_hash: document.contentHash,
      anchors: document.parsed.headings.map((heading) => heading.slug),
    })),
  ].sort((a, b) => a.route.localeCompare(b.route));

  await mkdir(OUT_DIR, { recursive: true });
  await writeFile(
    OUT,
    `${JSON.stringify(
      {
        schema: "heros.slug-manifest/v1",
        generated_from: "web/console/content/{docs,legal}/en/**",
        note: "Anchors are a published contract. A removed or renamed slug fails the build unless the same change adds a redirect.",
        pages,
      },
      null,
      2,
    )}\n`,
    "utf8",
  );

  const anchors = pages.reduce((total, page) => total + page.anchors.length, 0);
  console.log(`slug manifest generated: ${pages.length} page(s), ${anchors} anchor(s).`);
}

main().catch((error) => {
  console.error("slug manifest generation FAILED:", error.message);
  process.exit(1);
});
