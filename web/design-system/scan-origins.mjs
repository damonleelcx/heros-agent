// scan-origins.mjs is the BUILD-TIME fence that keeps the third-party allowlist the only way in
// (P24 task 1.3, design D11).
//
// # What it forbids, and why that exact thing
//
// A third-party origin may be added to this product in exactly one place: the table in
// `third-party-policy.ts`, where each row states which integration needs the origin, which consent
// category gates it, which directive it appears under and what its transfer budget is. A reviewer
// reading that diff is reading a decision.
//
// The alternative — an origin typed into a `middleware.ts` or a `next.config.mjs` — is a decision
// nobody reads. Those files already contain nine plausible header lines, and a tenth plausible header
// line is invisible in review. This script makes that edit a build failure in both consoles, so the
// table is not merely the recommended path but the only one.
//
// # Why it scans all four files from one script rather than two copies
//
// Because the rule is about the pair. Two copies of this scanner is the same failure class it exists
// to prevent: the copy that gets edited is the one that stops catching things. Both consoles' builds
// invoke THIS file, so a console that skipped the gate is visible as a missing line in its
// package.json rather than as a scanner that quietly disagrees with its twin.
//
// # Comments are stripped first
//
// A URL in prose is documentation, not an egress path, and a fence that cannot tell code from
// commentary either cries wolf or — worse — gets "fixed" by loosening the pattern until it stops
// catching the real thing. The same lesson is already written into `tests/security.test.mjs`.
//
//   node scan-origins.mjs                     # the gate
//   node scan-origins.mjs --self-test         # prove it goes red, without touching a shipped file
//   node scan-origins.mjs <file>...           # scan named files instead of the governed four
//
// The third form is how the red demonstration is run against a COPY of a real middleware with an
// origin injected into it. Demonstrating the fence by editing the shipped file would mean a crashed
// run can leave a security header modified on disk, which is a worse outcome than an unproven fence.

import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import process from "node:process";

const HERE = dirname(fileURLToPath(import.meta.url));
const WEB = join(HERE, "..");

/** The files a header, a rewrite or a script tag can be written into. */
const GOVERNED = [
  "console/src/middleware.ts",
  "console/next.config.mjs",
  "admin-console/src/middleware.ts",
  "admin-console/next.config.mjs",
];

/**
 * ORIGIN is an absolute http(s) URL.
 *
 * Deliberately NOT "any string containing a dot": that would flag `next.config.mjs` in a string and
 * teach people to disable the scan. What makes an origin an egress path is the scheme.
 */
const ORIGIN = /\bhttps?:\/\/[^\s"'`)]+/g;

/** stripComments removes block and line comments so prose does not trip the gate. */
function stripComments(source) {
  return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
}

/**
 * findings returns every origin literal in `source`, with the line it is on.
 *
 * The line number is part of the message on purpose: "an origin literal in your middleware" sends a
 * reader to a 120-line file; "line 84" sends them to the line.
 */
function findings(relative, source) {
  const out = [];
  const code = stripComments(source);
  const lines = code.split("\n");
  for (let i = 0; i < lines.length; i += 1) {
    for (const match of lines[i].matchAll(ORIGIN)) {
      out.push({ file: relative, line: i + 1, origin: match[0] });
    }
  }
  return out;
}

async function scan(files, base) {
  const all = [];
  for (const relative of files) {
    const source = await readFile(base ? join(base, relative) : relative, "utf8");
    all.push(...findings(relative, source));
  }
  return all;
}

/**
 * selfTest proves the gate goes red, against a synthetic source rather than a shipped file.
 *
 * A fence nobody has seen fail is a fence nobody knows is connected — but demonstrating it by editing
 * `middleware.ts` means a failed run can leave a security header modified on disk. So the violation is
 * injected into a string.
 */
function selfTest() {
  const violation = 'const csp = "connect-src \'self\' https://www.googletagmanager.com";';
  const hits = findings("<self-test>", violation);
  if (hits.length !== 1 || !hits[0].origin.includes("googletagmanager")) {
    console.error("origin scan SELF-TEST FAILED: the gate did not catch an injected origin literal");
    process.exit(1);
  }
  const prose = "// see https://example.com/csp for the reasoning\nconst x = 1;\n";
  if (findings("<self-test>", prose).length !== 0) {
    console.error("origin scan SELF-TEST FAILED: the gate flagged a URL in a comment");
    process.exit(1);
  }
  console.log(
    "origin scan self-test passed: an injected origin literal is caught, a URL in prose is not.",
  );
}

async function main() {
  if (process.argv.includes("--self-test")) {
    selfTest();
    return;
  }

  const named = process.argv.slice(2).filter((arg) => !arg.startsWith("--"));
  const targets = named.length > 0 ? named : GOVERNED;
  const hits = await scan(targets, named.length > 0 ? null : WEB);
  if (hits.length > 0) {
    console.error(`origin scan FAILED — ${hits.length} hard-coded origin(s):`);
    for (const hit of hits) {
      console.error(`  - ${hit.file}:${hit.line} names ${hit.origin}`);
    }
    console.error(
      "\nA third-party origin is added to web/design-system/third-party-policy.ts, where the row states\n" +
        "which integration needs it, which consent category gates it, which directive it appears under\n" +
        "and its transfer budget. Both middlewares construct their header from that table.",
    );
    process.exit(1);
  }

  console.log(
    `origin scan passed: ${targets.length} governed file(s), no hard-coded origin — ` +
      "every third-party origin comes from third-party-policy.ts.",
  );
}

main().catch((error) => {
  console.error("origin scan errored:", error);
  process.exit(2);
});
