// corpus.mjs is the shared reader every content fence uses.
//
// One reader, so eight fences cannot disagree about which files are content, where a finding's line
// number is, or whether front matter counts as body. A fence that reported a line number two off from
// its neighbours would be a fence people stop reading carefully.
//
// It deliberately returns the RAW body with its true starting line, not a parsed tree: a fence's job is
// to refuse text the parser would accept, so parsing first would hide exactly what most of them look for.

import { readdir, readFile } from "node:fs/promises";
import { join, relative, sep } from "node:path";
import process from "node:process";

const ROOT = process.cwd();
export const CONTENT_ROOT = process.env.HEROS_CONTENT_ROOT ?? join(ROOT, "content");

async function* walk(dir) {
  let entries;
  try {
    entries = await readdir(dir, { withFileTypes: true });
  } catch {
    return;
  }
  for (const entry of entries.sort((a, b) => a.name.localeCompare(b.name))) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(full);
    else if (entry.name.endsWith(".md")) yield full;
  }
}

/**
 * documents reads every content file under `content/{docs,legal}/**`.
 *
 * Each entry carries `lines` — the body split into lines, each with its TRUE 1-indexed position in the
 * file — so a finding points where the author's editor does.
 */
export async function documents() {
  const out = [];
  for await (const file of walk(CONTENT_ROOT)) {
    const rel = relative(CONTENT_ROOT, file).split(sep).join("/");
    const source = (await readFile(file, "utf8")).replace(/\r\n?/g, "\n");
    let frontMatter = {};
    let body = source;
    let bodyStart = 1;
    if (source.startsWith("---\n")) {
      const end = source.indexOf("\n---\n", 3);
      if (end >= 0) {
        const header = source.slice(4, end + 1);
        for (const line of header.split("\n")) {
          const match = /^([a-z_]+):\s*(.*)$/.exec(line);
          if (match) frontMatter[match[1]] = match[2].trim().replace(/^"(.*)"$/, "$1");
        }
        body = source.slice(end + 5);
        bodyStart = header.split("\n").length + 2;
      }
    }
    const kind = rel.startsWith("legal/") ? "legal" : "docs";
    out.push({
      path: `content/${rel}`,
      rel,
      kind,
      frontMatter,
      generated: frontMatter.generated === "true",
      body,
      lines: body.split("\n").map((text, index) => ({ number: bodyStart + index, text })),
      source,
    });
  }
  return out;
}

/**
 * codeSpans marks which lines are inside a fenced code block.
 *
 * Several fences must treat code differently from prose — a `<script>` in a code sample is an example of
 * the thing being refused, not an instance of it — and getting that wrong in either direction is how a
 * fence becomes either useless or unusable.
 */
export function fencedLines(document) {
  const inside = new Set();
  let open = false;
  for (const line of document.lines) {
    if (/^```/.test(line.text.trim())) {
      open = !open;
      inside.add(line.number);
      continue;
    }
    if (open) inside.add(line.number);
  }
  return inside;
}

/** report prints findings in the house style and exits non-zero. */
export function report(name, findings, scanned, passMessage, guidance) {
  if (findings.length > 0) {
    console.error(`${name} FAILED — ${findings.length} finding(s):`);
    for (const finding of findings) console.error(`  - ${finding}`);
    if (guidance) console.error(`\n${guidance}`);
    process.exit(1);
  }
  console.log(`${name} passed: ${scanned} document(s), ${passMessage}`);
}
