// p37-destinations.test.mjs — every moved block landed somewhere real (tasks 4.4, 4.5, 6.10).
//
// # The claim
//
// `block-inventory.md` names a destination for every block that left a working surface. That document is
// the pull request's enumeration (§4.5) and the input review checks a diff against — so a destination
// that does not exist turns the whole enumeration into a list of assurances.
//
// # 🔴 Why this is a build fence and not a review step
//
// A link to a reading-surface section that does not exist yet is a 404 in production, and it is the
// specific 404 **nobody reports**, because it looks like a docs problem rather than a product one
// (design D4). Review catches a paragraph that reads badly; it does not catch a heading that was renamed
// three commits later in a different directory.
//
// # What it checks, in both directions
//
//   1. every `→ concepts/x §Heading` in the inventory resolves to a real page AND a real heading;
//   2. every `/docs/...` href in the console's source resolves to a real page;
//   3. the destination documents exist at all, and are not empty stubs that satisfy (1) by having a
//      heading and nothing under it.
//
// (3) is the one a naive version misses. A move that creates a heading and leaves the paragraph behind
// passes a link check and has lost the text.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const DOCS = join(ROOT, "content", "docs", "en");
const INVENTORY = join(ROOT, "..", "..", "openspec", "changes", "p37-source-bound-editors", "block-inventory.md");

/** slugify is the anchor rule the reading surface publishes — GitHub's, as `lib/reading/markdown.ts`. */
function slugify(text) {
  return text
    .toLowerCase()
    .replace(/[`*_[\]()]/g, "")
    .replace(/[^a-z0-9\s-]/g, "")
    .trim()
    .replace(/\s+/g, "-")
    .replace(/-+/g, "-");
}

/** corpus loads every reading-surface page, with its headings and the words under each one. */
async function corpus() {
  const pages = new Map();
  for (const section of await readdir(DOCS, { withFileTypes: true })) {
    if (!section.isDirectory()) continue;
    for (const file of await readdir(join(DOCS, section.name))) {
      if (!file.endsWith(".md")) continue;
      const slug = `${section.name}/${file.replace(/\.md$/, "")}`;
      const body = await readFile(join(DOCS, section.name, file), "utf8");
      const headings = [];
      const lines = body.split("\n");
      for (const [i, line] of lines.entries()) {
        const m = /^(#{2,4})\s+(.+)$/.exec(line);
        if (!m) continue;
        // 🔴 The words under this heading, up to the next heading AT THE SAME OR A HIGHER LEVEL — so a
        // `##` section counts its `###` children. Stopping at any heading would report a section whose
        // content is all in subsections as empty, and the fence would demand text be moved OUT of the
        // structure design D4 asks for.
        const level = m[1].length;
        let words = 0;
        for (let j = i + 1; j < lines.length; j += 1) {
          const next = /^(#{2,4})\s/.exec(lines[j]);
          if (next && next[1].length <= level) break;
          words += lines[j].split(/\s+/).filter((t) => /[A-Za-z]/.test(t)).length;
        }
        headings.push({ text: m[2].trim(), slug: slugify(m[2]), words });
      }
      pages.set(slug, { body, headings });
    }
  }
  return pages;
}

test("🔴 4.5 / 6.10 every destination the inventory names resolves to a real page and heading", async () => {
  const pages = await corpus();
  const inventory = await readFile(INVENTORY, "utf8");

  // `→ concepts/context-policies §What reaches your source`
  const destinations = [...inventory.matchAll(/→ `?(concepts\/[a-z0-9-]+)`? §([^.|\n]+)/g)].map((m) => ({
    page: m[1],
    heading: m[2].trim().replace(/[›].*$/, "").trim(),
  }));

  assert.ok(
    destinations.length >= 25,
    `parsed only ${destinations.length} destinations from the inventory — the pattern has drifted, and a ` +
      `parser that finds nothing passes this test for the wrong reason`,
  );

  const broken = [];
  for (const { page, heading } of destinations) {
    const doc = pages.get(page);
    if (!doc) {
      broken.push(`${page} — no such reading-surface page`);
      continue;
    }
    const wanted = slugify(heading);
    const found = doc.headings.find((h) => h.slug === wanted || h.slug.startsWith(wanted) || wanted.startsWith(h.slug));
    if (!found) {
      broken.push(`${page} §${heading} — no heading with that anchor (${wanted})`);
      continue;
    }
    // 🔴 The heading must have TEXT under it. A move that creates a heading and leaves the paragraph
    // behind passes every link check and has lost the content — which is the exact failure "nothing is
    // deleted" exists to prevent, arriving through the mechanism meant to prevent it.
    if (found.words < 20) {
      broken.push(`${page} §${heading} — the heading exists and carries ${found.words} words under it`);
    }
  }

  assert.deepEqual(
    broken,
    [],
    "a block was moved to a destination that does not hold it. Nothing is deleted in this phase: every " +
      "moved paragraph is on the reading surface or the move does not happen.\n  " + broken.join("\n  "),
  );
});

test("🔴 6.10 every /docs link in the console's source resolves — break one and this fails", async () => {
  const pages = await corpus();

  async function* walk(dir) {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const full = join(dir, entry.name);
      if (entry.isDirectory()) yield* walk(full);
      else if (/\.(tsx|ts)$/.test(entry.name)) yield full;
    }
  }

  const broken = [];
  let checked = 0;
  for await (const file of walk(join(ROOT, "src"))) {
    const source = await readFile(file, "utf8");
    for (const match of source.matchAll(/"\/docs\/([a-z0-9/-]+)"/g)) {
      const target = match[1];
      // `/docs` itself and the section indexes are routes rather than pages.
      if (!target.includes("/")) continue;
      checked += 1;
      const [path, fragment] = target.split("#");
      if (!pages.has(path)) {
        broken.push(`${file.replace(ROOT, "")}: /docs/${target} — no such page`);
        continue;
      }
      if (fragment && !pages.get(path).headings.some((h) => h.slug === fragment)) {
        broken.push(`${file.replace(ROOT, "")}: /docs/${target} — no such anchor`);
      }
    }
  }

  assert.ok(checked >= 8, `only ${checked} /docs links were found in the console source — the scan is broken`);
  assert.deepEqual(broken, [], `these links 404 in production:\n  ${broken.join("\n  ")}`);
});

test("4.1 the destination documents exist, with a table of contents and real content", async () => {
  const pages = await corpus();
  // 🔴 The seven P37 authored, each named. Ranged over a directory this would pass on a corpus with
  // none of them — the shape of test that is green from the day it is written to the day it matters.
  const authored = [
    "concepts/context-policies",
    "concepts/memory-strategies",
    "concepts/execution-envelope",
    "concepts/graph-and-wiring",
    "concepts/authored-changes",
    "concepts/delivery-routes",
    "concepts/prompt-and-model-studio",
  ];
  for (const slug of authored) {
    const doc = pages.get(slug);
    assert.ok(doc, `${slug} was never authored, so every block routed to it has nowhere to land`);
    // A table of contents is generated from the headings, so "has one" means "has headings".
    assert.ok(
      doc.headings.filter((h) => h.slug).length >= 4,
      `${slug} has ${doc.headings.length} heading(s) — moved text is EDITED INTO a document with a table ` +
        `of contents, not appended in the order it was cut (design D4's stated risk R3)`,
    );
    const words = doc.body.split(/\s+/).filter((t) => /[A-Za-z]/.test(t)).length;
    assert.ok(words > 400, `${slug} carries ${words} words — that is a stub, not a destination`);
  }
});

test("4.3 every worked example on the reading surface says it is the platform's fixture", async () => {
  const pages = await corpus();
  const withExamples = [
    "concepts/context-policies",
    "concepts/memory-strategies",
    "concepts/graph-and-wiring",
    "concepts/authored-changes",
    "concepts/prompt-and-model-studio",
  ];
  for (const slug of withExamples) {
    const doc = pages.get(slug);
    const heading = doc.headings.find((h) => /^worked examples?$/i.test(h.text));
    assert.ok(heading, `${slug} carries no worked-example section, so an example that left a surface is lost`);
    assert.match(
      doc.body,
      /the platform's own fixture|labelled as the platform's fixture/i,
      `${slug}'s worked example does not say it is the platform's fixture — a reader must be able to tell ` +
        `it from their own data without inspecting values`,
    );
  }
});
