// scan-legal.mjs guards the ONE-WAY DOOR at the centre of this phase (tasks 8.3, 8.6 · Decision 2).
//
// # What a deleted archive costs, and why no amount of care prevents it
//
// A consent record stores `(kind, version, content_hash)`. Delete the archived document for a version
// and every record referencing it is orphaned: the row still says "they agreed to v1.0.0" and **nothing
// can any longer say what v1.0.0 said**. There is no recovery, no backup that helps after the fact, and
// no way to reconstruct the text from the record.
//
// It will not be deleted maliciously. It will be deleted in a routine cleanup, years from now, by
// somebody who sees an old file and no reason to keep it. That is exactly the failure a machine has to
// prevent, because the reason to keep it lives in a decision record they have never read.
//
// So four things fail the build:
//
//   1. a legal document missing any required front-matter field
//   2. a `material` field that is not exactly `true` or `false` — an ambiguous value is not a decision
//   3. a version that was published in the last commit and no longer resolves
//   4. a version whose CONTENT changed while its version number did not
//
// (4) is the subtler half of the same door. Editing text under an unchanged version number makes the
// consent record's hash disagree with what the page now says — which is detectable rather than invisible
// precisely because the hash is stored, and this is where it gets detected.
//
// # Why the comparison is against `git show HEAD:`
//
// The slug manifest is checked in, so the previous commit's version of it is what was last published.
// That makes "you removed something a consent record may point at" a question with a real answer rather
// than a reviewer's recollection. With no git history — a container build from a tarball — the check is
// SKIPPED AND SAYS SO, because silently passing a check you did not run is how a fence becomes
// decorative.
//
// # What it deliberately does not check
//
//   - Whether the legal text is CORRECT, complete, or enforceable. That is counsel's, and no script is
//     going to develop an opinion about a limitation-of-liability clause.
//   - Whether `material` was declared CORRECTLY. No machine can judge materiality; this fence forces the
//     declaration to exist and to be attributable, which is Decision 3's entire claim.
//   - Whether the effective date is sensible. A date in 2099 parses.

import { readFile } from "node:fs/promises";
import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";
import process from "node:process";
import { documents, report } from "./lib/corpus.mjs";

const exec = promisify(execFile);
const ROOT = process.cwd();

const REQUIRED = ["kind", "version", "effective_date", "authoritative_language", "supersedes", "material", "title"];

async function publishedAtHead() {
  try {
    const { stdout } = await exec("git", ["show", "HEAD:web/console/docs/slug-manifest.json"], { cwd: ROOT });
    return JSON.parse(stdout);
  } catch {
    return null;
  }
}

async function main() {
  const findings = [];
  const docs = (await documents()).filter((document) => document.kind === "legal");

  const routes = new Set();
  for (const document of docs) {
    for (const field of REQUIRED) {
      if (!document.frontMatter[field]) {
        findings.push(
          `${document.path}: front matter is missing \`${field}\`. Every legal document declares all ` +
            `${REQUIRED.length} fields — a document that does not cannot be published.`,
        );
      }
    }
    const material = document.frontMatter.material;
    if (material !== undefined && material !== "true" && material !== "false") {
      findings.push(
        `${document.path}: \`material: ${material}\` is neither true nor false. No machine can judge ` +
          `materiality, so this field records a PERSON's decision — and an ambiguous value is not one.`,
      );
    }
    if (document.frontMatter.kind && document.frontMatter.version) {
      routes.add(`/legal/${document.frontMatter.kind}/v/${document.frontMatter.version}`);
    }
  }

  const head = await publishedAtHead();
  let note;
  if (!head) {
    note =
      "the deleted-version check was SKIPPED — no git history here (expected in a container build from a " +
      "tarball, and it means a deletion would not be caught in THIS build)";
  } else {
    let checked = 0;
    for (const page of head.pages ?? []) {
      if (!page.route.startsWith("/legal/")) continue;
      checked += 1;
      if (!routes.has(page.route)) {
        findings.push(
          `${page.route} was published in the last commit and no longer resolves.\n` +
            `      🔴 Deleting an archived legal version ORPHANS every consent record referencing it: the row ` +
            `still says a customer agreed to that version, and nothing can any longer say what it said. ` +
            `There is no recovery. Restore the file.`,
        );
      }
    }
    note = `${checked} previously published version(s) still resolve`;
  }

  /*
   * (4) — content changed under an unchanged version.
   *
   * 🔴 The absence of hashes at HEAD is REPORTED, not treated as a pass. The first run of this check
   * found nothing because the committed manifest predated the `content_hash` field — and a fence that
   * reports success when it had nothing to compare against is the exact "green light with no bulb"
   * failure the fixture rule exists to prevent. So a comparison that could not run says so.
   */
  let hashNote = "";
  if (head) {
    const current = JSON.parse(await readFile(join(ROOT, "docs", "slug-manifest.json"), "utf8"));
    let compared = 0;
    const headHashes = new Map();
    for (const page of head.pages ?? []) {
      if (page.route.startsWith("/legal/") && page.content_hash) headHashes.set(page.route, page.content_hash);
    }
    for (const page of current.pages ?? []) {
      if (!page.route.startsWith("/legal/") || !page.content_hash) continue;
      const was = headHashes.get(page.route);
      if (!was) continue;
      compared += 1;
      if (was !== page.content_hash) {
        findings.push(
          `${page.route}: the text changed but the version did not.\n` +
            `      A consent record stores the hash it was shown. Editing text under an unchanged version ` +
            `makes every existing record disagree with the page. Publish a NEW version instead — the old ` +
            `one stays where it is, forever.`,
        );
      }
    }
    hashNote =
      compared > 0
        ? `, ${compared} version(s) hash-compared against the last commit`
        : ", and the changed-text-under-an-unchanged-version check COMPARED NOTHING (the committed manifest carries no content hash yet — it will from the next commit)";
  }

  report(
    "legal scan",
    findings,
    docs.length,
    `every document declares all ${REQUIRED.length} required fields with an unambiguous materiality; ${note}${hashNote}.`,
    "A legal document's identity is (kind, version, content_hash) and a consent record points at that\n" +
      "triple. Every version stays published forever, and text never changes under a version number.",
  );
}

main().catch((error) => {
  console.error("legal scan errored:", error);
  process.exit(2);
});
