// scan-install.mjs is the install-honesty fence (task 4.13 · Decisions 12 and 13).
//
// Four refusals, each with a failure it has actually caused somewhere:
//
//   1. **A hand-typed checksum, filename or version.** A routinely-wrong checksum is how readers learn
//      to skip verification. Every one of these values must appear in the generated release artifact.
//   2. **A documented path that reaches `PATH` before verifying.** The CLI runs inside a customer's CI
//      with access to their repository, so the one-liner everyone copies is exactly where the control
//      must live. This is a PUBLICATION rule with a name to cite, not a preference to argue per review.
//   3. **A trust claim the pipeline did not earn.** "Signed" and "notarized" are claims about steps that
//      either ran or did not. The release publishes `trust.json` saying which; a page may not outrun it.
//   4. **An install command for a channel the pipeline does not publish.** An install command that 404s
//      is the worst possible first sentence of a product. (`scan-docs-claims` owns this one; it is named
//      here so the set is visible in one place.)
//
// # Why (3) matches only AFFIRMATIVE constructions
//
// A naive search for "notarized" flags the sentence that DISCLOSES the absence — "the macOS binaries are
// not notarized" — and the fastest way to make the gate green would be to delete the honest sentence. A
// gate whose easiest fix is removing a disclosure is worse than no gate. So the patterns are affirmative
// and a negation nearby is a guard, exactly as `internal/distribution/claims.go` does it.
//
// # What it deliberately does not check
//
//   - That the install commands WORK on a real machine. The release pipeline's smoke job runs them on
//     five OS images; this reads text.
//   - That the checksums are CORRECT for the bytes. They are copied from the release's own signed
//     manifest, so "correct" here means "the same string the manifest has" — which is the property that
//     matters for a page.
//   - Prose about security in general. A page can still be misleading in English; that is review.

import { readFile } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";
import { documents, fencedLines, report } from "./lib/corpus.mjs";

const ROOT = process.cwd();

/** VERIFY are the commands that establish the bytes are ours. */
const VERIFY = [/shasum\s+-a\s+256\s+-c/, /sha256sum\s+-c/, /heros\s+verify-release/, /Get-FileHash/i, /install\.sh/, /install\.ps1/];

/** REACHES_PATH are the commands that make a binary runnable. */
const REACHES_PATH = [
  /\binstall\s+-m\s+[0-7]{3,4}\s+\S+\s+\/usr\/local\/bin/,
  /\b(mv|cp)\s+\S+\s+\/usr\/local\/bin/,
  /\b(mv|cp)\s+\S+\s+\$HOME\/\.local\/bin/,
  /\bchmod\s+\+x\s+\S+\s*&&\s*\.?\/?\S*heros/,
  /Move-Item\s+.*\$env:LOCALAPPDATA/i,
];

/**
 * CLAIMS pairs an inventoried trust property with the affirmative constructions that assert it, and with
 * the attestation field that decides whether this release earned it.
 */
const CLAIMS = [
  {
    id: "macos-notarized",
    patterns: [/\b(is|are|been|fully)\s+notarized\b/i, /\bnotarized\s+(by\s+apple|binaries|build|release)\b/i],
    earned: (trust) => Boolean(trust?.macos?.Notarized),
  },
  {
    id: "macos-signed",
    patterns: [/\bmacos\s+binaries\s+(are|is)\s+(code[- ])?signed\b/i, /\bdeveloper\s+id\s+signed\b/i],
    earned: (trust) => Boolean(trust?.macos?.CodeSigned),
  },
  {
    id: "windows-signed",
    patterns: [/\bauthenticode\b/i, /\bwindows\s+binaries\s+(are|is)\s+(code[- ])?signed\b/i],
    earned: (trust) => Boolean(trust?.windows?.CodeSigned),
  },
  {
    id: "signed-manifest",
    patterns: [/\bmanifest\s+is\s+signed\b/i, /\bsigned\s+release\s+manifest\b/i],
    earned: (trust) => Boolean(trust?.signed_manifest),
  },
];

const NEGATED = /\b(not|never|no|without|unsigned|un-notarized)\b/i;

/**
 * DISCLOSURE marks a line that names a filename or a checksum in order to say it is ABSENT or
 * UNVERIFIABLE.
 *
 * Without this, the fence punishes exactly the sentence it wants written. The install page's refusal
 * block — "this channel's commands name `heros-0.20.0.x86_64.rpm`, and the release does not publish that
 * file" — is the most useful sentence on the page, and a naive filename check flags it. The rule is the
 * same one the trust claims follow: a gate whose easiest fix is deleting the honest sentence is worse
 * than no gate.
 */
const DISCLOSURE =
  /\b(does not publish|do not publish|not usable|withheld|is not an asset|are not listed|not listed in|cannot be verified|does not cover|not covered)\b/i;

/** codeBlocks groups a document's fenced blocks so verification order can be judged within one block. */
function codeBlocks(document) {
  const blocks = [];
  let current = null;
  for (const line of document.lines) {
    if (/^```/.test(line.text.trim())) {
      if (current) {
        blocks.push(current);
        current = null;
      } else {
        current = { start: line.number, lines: [] };
      }
      continue;
    }
    if (current) current.lines.push(line);
  }
  return blocks;
}

async function main() {
  let release = null;
  try {
    release = JSON.parse(await readFile(join(ROOT, "src", "generated", "release-assets.json"), "utf8"));
  } catch {
    release = null;
  }

  const checksums = new Set((release?.assets ?? []).map((a) => a.sha256));
  const filenames = new Set([
    ...(release?.assets ?? []).map((a) => a.name),
    ...(release?.unverifiable_assets ?? []).map((a) => a.name),
    ...(release?.signatures ?? []),
    release?.manifest,
    "trust.json",
    "allowed_signers",
  ].filter(Boolean));

  const findings = [];
  const docs = await documents();

  for (const document of docs) {
    if (document.kind !== "docs") continue;
    const fenced = fencedLines(document);

    // ── 1 · hand-typed values ──────────────────────────────────────────────
    for (const line of document.lines) {
      if (DISCLOSURE.test(line.text)) continue;
      for (const match of line.text.matchAll(/\b[0-9a-f]{64}\b/g)) {
        if (checksums.has(match[0])) continue;
        findings.push(
          `${document.path}:${line.number}: a 64-hex checksum that is not in the published release manifest. ` +
            `Every checksum on this surface is generated from SHA256SUMS — a hand-typed one is wrong the day ` +
            `the next release ships, and a routinely-wrong checksum teaches readers to skip verification.`,
        );
      }
      for (const match of line.text.matchAll(/\bheros[-_][0-9]+\.[0-9]+\.[0-9]+[A-Za-z0-9._-]*/g)) {
        if (filenames.has(match[0])) continue;
        findings.push(
          `${document.path}:${line.number}: \`${match[0]}\` is not an asset of the published release ` +
            `(${release?.release ?? "none"}). Asset filenames are generated, never typed.`,
        );
      }
    }

    // ── 2 · verification before PATH ───────────────────────────────────────
    for (const block of codeBlocks(document)) {
      let verifiedAt = -1;
      for (const line of block.lines) {
        if (VERIFY.some((p) => p.test(line.text))) {
          verifiedAt = line.number;
          break;
        }
      }
      for (const line of block.lines) {
        if (!REACHES_PATH.some((p) => p.test(line.text))) continue;
        if (verifiedAt >= 0 && verifiedAt < line.number) continue;
        findings.push(
          `${document.path}:${line.number}: this path puts the binary where it can run before anything ` +
            `verified it.\n` +
            `      🔴 A documented path that reaches PATH before verification is NOT PUBLISHED — the CLI runs ` +
            `inside a customer's CI with access to their repository, so the shortest line on the page must be ` +
            `the verified one. Cite this rule; it is not a preference.`,
        );
      }
    }

    // ── 3 · trust claims the pipeline did not earn ─────────────────────────
    for (const line of document.lines) {
      if (fenced.has(line.number)) continue;
      for (const claim of CLAIMS) {
        if (!claim.patterns.some((p) => p.test(line.text))) continue;
        if (NEGATED.test(line.text)) continue; // a disclosure of ABSENCE is the sentence we want
        if (claim.earned(release?.trust)) continue;
        findings.push(
          `${document.path}:${line.number}: claims "${claim.id}", which release ` +
            `${release?.release ?? "(none published)"} did not deliver. The reader meets the truth at the ` +
            `Gatekeeper dialog either way; the only question is whether we told them first.`,
        );
      }
    }
  }

  report(
    "install scan",
    findings,
    docs.length,
    release?.release
      ? `every checksum and filename traces to ${release.release}; no path reaches PATH before verifying; ` +
        `no trust claim outruns the attestation.`
      : `no release is published, and no page states a checksum, a filename or a trust claim.`,
    "Asset filenames, versions and checksums come from the published release via\n" +
      "scripts/gen-release-assets.mjs. Verification is a step of the install, never an appendix.",
  );
}

main().catch((error) => {
  console.error("install scan errored:", error);
  process.exit(2);
});
