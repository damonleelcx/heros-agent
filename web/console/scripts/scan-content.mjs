// scan-content.mjs keeps the reading surface MARKDOWN — no raw HTML, no inline handlers, no external
// script, font or stylesheet — and keeps a third-party origin out of public-surface markup (task 4.12).
//
// # Why this exists when the runtime CSP already refuses all of it
//
// It does. `middleware.ts` sets `default-src 'self'`, `connect-src 'self'`, `img-src 'self' data:` per
// request, so a hosted font, an analytics tag and a `fetch` to somebody's API are all refused by the
// browser today. This fence adds two things the CSP cannot:
//
//   1. **The refusal becomes visible at review rather than at deploy.** A CSP violation is a line in a
//      browser console on somebody's machine. A failed build is a conversation before the merge.
//   2. **Air-gapped parity becomes a MACHINE CHECK rather than a policy.** A P19 air-gapped deployment
//      has no egress. Under the CSP alone, a hosted font degrades to a fallback and nobody notices the
//      design is different there. Under this fence, it cannot be written.
//
// # What it refuses, and where
//
//   content/**            raw HTML tags, inline `on…=` handlers, external script/font/stylesheet URLs
//   public-surface markup any third-party ORIGIN at all — badge, widget, hosted font, cross-origin fetch
//
// The second is P23 task 7.3's rule generalised: the console's public page tells readers it contacts no
// third party, and a badge rendered by a third party sitting next to that sentence would undercut the
// exact claim beside it.
//
// # What it deliberately does not check
//
//   - Whether prose is HONEST. That is `scan-docs-claims` for capabilities, `scan-install` for trust
//     claims, and a human for tone, emphasis and omission.
//   - Markdown inside a code fence. A code sample showing an HTML tag is an EXAMPLE of the thing being
//     refused, not an instance of it — refusing it would make it impossible to document why the rule
//     exists.
//   - Whether a link's TARGET is reachable. `scan-links` owns that.

import { readdir, readFile } from "node:fs/promises";
import { join, relative } from "node:path";
import process from "node:process";
import { documents, fencedLines, report } from "./lib/corpus.mjs";

const ROOT = process.cwd();

/** PUBLIC_DIRS is the surface that may not name a third-party origin at all. */
const PUBLIC_DIRS = [
  join(ROOT, "src", "app", "(public)"),
  join(ROOT, "src", "app", "(reading)"),
  join(ROOT, "src", "components", "marketing"),
  join(ROOT, "src", "components", "reading"),
];

/**
 * ALLOWED_ORIGINS is the closed set of external origins the public surface may NAME.
 *
 * Naming is not fetching: these appear as `href` targets a reader clicks, which is the one interaction
 * the CSP does not govern and the only one that costs nothing until it happens. Anything that would make
 * the BROWSER reach out — src, url(), fetch, link rel=stylesheet — is refused regardless of origin.
 */
const ALLOWED_ORIGINS = [
  "https://github.com/", // the repository link (task 7.1), an anchor and nothing more
  "https://raw.githubusercontent.com/", // install script URLs, inside code samples a reader runs
];

const RAW_HTML = /<\/?[a-zA-Z][a-zA-Z0-9-]*(\s[^>]*)?>/;
const INLINE_HANDLER = /\son[a-z]+\s*=/i;
const EXTERNAL_ASSET = /(?:src|href)\s*=\s*["']https?:\/\//i;

async function* walkSource(dir) {
  let entries;
  try {
    entries = await readdir(dir, { withFileTypes: true });
  } catch {
    return;
  }
  for (const entry of entries) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) yield* walkSource(full);
    else if (/\.(tsx|ts|css)$/.test(entry.name)) yield full;
  }
}

function stripComments(source) {
  return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
}

async function main() {
  const findings = [];
  const docs = await documents();

  // ── Content: Markdown only ─────────────────────────────────────────────────
  for (const document of docs) {
    const fenced = fencedLines(document);
    for (const line of document.lines) {
      if (fenced.has(line.number)) continue;
      const html = RAW_HTML.exec(line.text);
      if (html) {
        findings.push(
          `${document.path}:${line.number}: raw HTML \`${html[0]}\` — this surface renders Markdown as React elements, ` +
            `and there is no path from content to markup at all`,
        );
      }
      if (INLINE_HANDLER.test(line.text)) {
        findings.push(`${document.path}:${line.number}: inline event handler`);
      }
      if (EXTERNAL_ASSET.test(line.text)) {
        findings.push(
          `${document.path}:${line.number}: an external asset reference — a font, script or stylesheet from ` +
            `another origin does not render here, it is REFUSED, and in an air-gapped deployment it never arrives`,
        );
      }
    }
  }

  // ── Public-surface markup: no third-party origin ──────────────────────────
  let sources = 0;
  for (const dir of PUBLIC_DIRS) {
    for await (const file of walkSource(dir)) {
      sources += 1;
      const rel = relative(ROOT, file);
      const source = stripComments(await readFile(file, "utf8"));
      for (const match of source.matchAll(/https?:\/\/[^\s"'`)]+/g)) {
        const url = match[0];
        if (url.startsWith("http://localhost") || url.startsWith("http://127.0.0.1")) continue;
        if (ALLOWED_ORIGINS.some((origin) => url.startsWith(origin))) continue;
        findings.push(
          `${rel}: names the third-party origin ${url} — the CSP refuses it at runtime, and the public page ` +
            `tells readers it contacts no third party. This is not the feature that gets an exception.`,
        );
      }
      // A badge, a widget or a browser-side call is refused by shape as well as by origin, because the
      // allowlist above is about ANCHORS and these are not anchors.
      for (const [pattern, what] of [
        [/shields\.io/i, "a shields.io badge"],
        [/buttons\.github\.io|github-buttons/i, "a GitHub buttons widget"],
        [/api\.github\.com/i, "a browser-side api.github.com call"],
        [/fonts\.(googleapis|gstatic)\.com/i, "a hosted font"],
      ]) {
        if (pattern.test(source)) {
          findings.push(
            `${rel}: uses ${what}. The CSP already refuses it; a star count is measured at BUILD time and ` +
              `rendered as a server-side string with its measurement date, or it is not rendered (task 7.3).`,
          );
        }
      }
    }
  }

  report(
    "content scan",
    findings,
    docs.length,
    `${sources} public source file(s), Markdown only, no third-party origin.`,
    "Content is Markdown rendered as React elements. There is no path from a document to raw markup, and no\n" +
      "external origin may be fetched from the public surface — the runtime CSP refuses both, and this makes\n" +
      "the refusal visible at review rather than at deploy.",
  );
}

main().catch((error) => {
  console.error("content scan errored:", error);
  process.exit(2);
});
