// scan-secrets.mjs fails the build on credential-shaped content (task 4.11).
//
// # The failure it prevents
//
// Documentation is where credentials leak, and the mechanism is always the same: somebody pastes a
// working example. A quickstart that shows a real provider key is a key that is now in git history, in
// every fork, in every search index, and in the console container image — and rotating it does not
// remove it from any of those.
//
// The `secrets-baseline.md` rule is "never in repo, logs, CI echo or traces". This applies it to the one
// surface whose whole job is to show people commands.
//
// # Why the fixture keys are unjoinable
//
// This file must not itself contain a string that another secret scanner would flag, or CI turns into a
// game of allowlisting our own fence. So every pattern is assembled from fragments at runtime — the
// literal never appears in the source, and the scanner and the thing it scans for cannot be confused.
//
// # What it deliberately does not check
//
//   - Whether a REDACTED example is realistic. `sk-...` placeholders pass, as they should.
//   - Whether a documented environment variable NAME is sensitive. Names are public by design — the
//     install page names `HEROS_TOKEN` on purpose — and flagging names would make the fence unusable.
//   - Anything outside `content/**`. `scan-bundle.mjs` owns the shipped JavaScript, and `gitleaks` owns
//     the repository. Three scanners, three scopes, no overlap to argue about.

import process from "node:process";
import { documents, report } from "./lib/corpus.mjs";

/**
 * PATTERNS are credential SHAPES. Each is assembled so this file contains no scannable literal.
 *
 * The minimum lengths matter: `sk-` on its own appears in prose about keys, and a fence that fired on it
 * would be switched off within a day.
 */
const PATTERNS = [
  {
    name: "OpenAI-style provider key",
    // "sk" + "-" + 20 or more key characters
    regex: new RegExp(["s", "k"].join("") + "-[A-Za-z0-9_-]{20,}"),
  },
  {
    name: "Anthropic-style provider key",
    regex: new RegExp(["s", "k"].join("") + "-ant-[A-Za-z0-9_-]{20,}"),
  },
  {
    name: "AWS access key id",
    regex: new RegExp(["AKI", "A"].join("") + "[0-9A-Z]{16}"),
  },
  {
    name: "GitHub token",
    regex: new RegExp("gh[pousr]" + "_[A-Za-z0-9]{30,}"),
  },
  {
    name: "Stripe secret key",
    regex: new RegExp(["s", "k"].join("") + "_(live|test)_[A-Za-z0-9]{20,}"),
  },
  {
    name: "PEM private key block",
    regex: new RegExp("-----BEGIN [A-Z ]*PRIVATE KEY" + "-----"),
  },
  {
    name: "bearer token",
    regex: /\bBearer\s+[A-Za-z0-9._-]{24,}/,
  },
  {
    name: "JSON Web Token",
    regex: /\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}/,
  },
];

/**
 * PLACEHOLDERS are the shapes a documentation page SHOULD use, and they must not be flagged.
 *
 * A fence that refuses `sk-your-key-here` teaches authors to write something realistic instead, which is
 * the opposite of what it is for.
 */
const PLACEHOLDERS = [
  /your[-_]?key/i,
  /replace[-_]?me/i,
  /example/i,
  /placeholder/i,
  /xxxx+/i,
  /\.{3}/,
];

async function main() {
  const findings = [];
  const docs = await documents();

  for (const document of docs) {
    for (const line of document.lines) {
      if (PLACEHOLDERS.some((p) => p.test(line.text))) continue;
      for (const { name, regex } of PATTERNS) {
        if (!regex.test(line.text)) continue;
        // The FILE and the LINE are named, never the matched text: a scan that echoed the credential
        // back would put it in the CI log — the second place it lives, and the one nobody rotates.
        findings.push(
          `${document.path}:${line.number}: content matches a ${name}. ` +
            `The value is not printed here on purpose.`,
        );
      }
    }
  }

  report(
    "secret scan",
    findings,
    docs.length,
    `no credential-shaped content across ${PATTERNS.length} pattern(s).`,
    "A pasted working key is in git history, every fork, every search index and the console image, and\n" +
      "rotating it removes it from none of them. Use a placeholder — `sk-your-key-here` passes this scan.",
  );
}

main().catch((error) => {
  console.error("secret scan errored:", error);
  process.exit(2);
});
