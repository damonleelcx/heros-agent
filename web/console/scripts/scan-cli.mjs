// scan-cli.mjs is the CLI fence, and it runs in BOTH DIRECTIONS (task 4.7 · Decision 14).
//
// # Why both, and which one actually matters
//
//   docs → code   a `heros …` invocation naming a subcommand or flag the registry does not have
//   code → docs   a subcommand in the registry with no reference entry
//   exit codes    a documented code whose meaning disagrees with `internal/cli/exit.go`
//
// The first is the failure people expect. The second is the one that accumulates: adding a subcommand is
// a normal Tuesday and remembering the reference is not, so without this direction the product quietly
// grows commands nobody can look up. Six months later the honest answer to "how do I use this" is *read
// `internal/`*, which is where this phase started.
//
// The third is load-bearing for a different reason. `internal/cli/exit.go` says the codes are public
// "the moment a customer's pipeline branches on them" — so a documented meaning that drifts is a lie a
// CI job acts on. `1` (your gate failed) and `2` (our tool broke) have OPPOSITE remedies; swap them in
// the documentation and a customer disables the step that was telling them the truth.
//
// # Where the truth comes from
//
// `src/generated/docs-facts.json`, emitted by `cmd/docsfacts` from the Go registry, with `make
// docs-facts-check` failing the build when the Go changed and the artifact did not. This script never
// parses Go.
//
// # What it deliberately does not check
//
//   - That a documented command DOES what its page says. It checks the surface — names, flags, codes —
//     not behaviour. No static check can run `heros eval` and read the scorecard.
//   - That an example invocation is a GOOD example. `heros discover --repo .` and a twelve-flag
//     incantation both pass.
//   - Shell syntax around an invocation. `heros discover | jq` is checked for `discover`, and nothing is
//     asserted about `jq`.

import { readFile } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";
import { documents, report } from "./lib/corpus.mjs";

const ROOT = process.cwd();

/** REFERENCE is the page that must carry an entry per subcommand. */
const REFERENCE = "docs/en/reference/cli.md";

async function main() {
  const facts = JSON.parse(await readFile(join(ROOT, "src", "generated", "docs-facts.json"), "utf8"));
  const known = new Map(facts.commands.map((c) => [c.name, c]));
  const findings = [];
  const docs = await documents();

  // ── docs → code ────────────────────────────────────────────────────────────
  let invocations = 0;
  for (const document of docs) {
    for (const line of document.lines) {
      /*
       * Every `heros <word>` in the corpus, in prose and in code alike. A wrong command inside a code
       * fence is the one a reader will actually paste, so fences are IN scope here — unlike in
       * `scan-content`, where a fence is an illustration.
       *
       * 🔴 A PATH is not an invocation. The leading guard excludes `heros` preceded by `/`, a word
       * character, a dot or a hyphen — so `/usr/bin/heros matches it` and `./heros-0.20.0-darwin-arm64`
       * are prose about a file, not a command named `matches`.
       *
       * This was a real false positive: the install page's honest verification sentence names the
       * installed path, and the fence read the next word as a subcommand. A fence that forces prose to
       * avoid saying where the binary lives is a fence that makes the documentation worse.
       */
      for (const match of line.text.matchAll(/(^|[^/\w.-])heros\s+([a-z][a-z-]*)/g)) {
        const name = match[2];
        invocations += 1;
        if (!known.has(name)) {
          findings.push(
            `${document.path}:${line.number}: \`heros ${name}\` is not a subcommand. ` +
              `The registry has: ${[...known.keys()].join(", ")}`,
          );
          continue;
        }
        // The flags on the rest of the line belong to this invocation. Long flags only — a short flag
        // would need the FlagSet's own parsing to disambiguate, and guessing would produce false
        // findings, which is how a fence gets deleted.
        const rest = line.text.slice(match.index + match[0].length);
        const stop = rest.search(/\s(?:\||&&|;|>|<)/);
        const scope = stop >= 0 ? rest.slice(0, stop) : rest;
        const command = known.get(name);
        const allowed = new Set(command.flags.map((f) => f.name));
        for (const flagMatch of scope.matchAll(/--([a-z][a-z0-9-]*)/g)) {
          const flag = flagMatch[1];
          if (allowed.has(flag)) continue;
          // A flag that exists on ANOTHER command is a different, more useful message than one that does
          // not exist at all: the first is a misplacement, the second a typo or an invention.
          const elsewhere = facts.commands.filter((c) => c.flags.some((f) => f.name === flag)).map((c) => c.name);
          findings.push(
            elsewhere.length > 0
              ? `${document.path}:${line.number}: \`heros ${name} --${flag}\` — ${name} does not read --${flag}; ` +
                `it belongs to: ${elsewhere.join(", ")}`
              : `${document.path}:${line.number}: \`heros ${name} --${flag}\` — no such flag on any command`,
          );
        }
      }
    }
  }

  // ── code → docs ────────────────────────────────────────────────────────────
  const reference = docs.find((document) => document.rel === REFERENCE);
  if (!reference) {
    findings.push(
      `there is no CLI reference at content/${REFERENCE}. Every subcommand in the registry needs an entry, ` +
        `and with no page there are ${known.size} commands nobody can look up.`,
    );
  } else {
    for (const name of known.keys()) {
      // The entry is a heading, because a heading is what publishes an anchor — and a CLI error message
      // deep-linking to `/docs/reference/cli#eval` is the reason anchors are a contract at all.
      const heading = new RegExp(`^###\\s+${name}\\s*$`, "m");
      if (!heading.test(reference.body)) {
        findings.push(
          `the registry has \`heros ${name}\` and the CLI reference has no \`### ${name}\` entry. ` +
            `Adding a subcommand is a normal Tuesday; remembering the docs is not, which is why this is a build failure.`,
        );
      }
    }
  }

  // ── exit-code parity ───────────────────────────────────────────────────────
  //
  // Any page that states a code's meaning must agree with the contract. The pattern deliberately looks
  // for a code stated NEXT TO a name, which is the shape a reference table has, so prose that mentions
  // "exit 1" in passing is not dragged in.
  const byCode = new Map(facts.exit_codes.map((e) => [String(e.code), e]));
  for (const document of docs) {
    for (const line of document.lines) {
      for (const match of line.text.matchAll(/`(\d)`\s*\|\s*([a-z-]+)\s*\|/g)) {
        const [, code, name] = match;
        const expected = byCode.get(code);
        if (!expected) {
          findings.push(`${document.path}:${line.number}: documents exit code ${code}, which is not in the contract`);
          continue;
        }
        if (expected.name !== name) {
          findings.push(
            `${document.path}:${line.number}: exit code ${code} is documented as "${name}"; the contract says ` +
              `"${expected.name}". 1 and 2 have opposite remedies — swapping them makes a CI job act on a lie.`,
          );
        }
      }
    }
  }

  report(
    "cli scan",
    findings,
    docs.length,
    `${invocations} invocation(s) resolve to real commands and flags; all ${known.size} registry command(s) have a reference entry; exit codes agree with the contract.`,
    "The registry in internal/cli is the source. Run `make docs-facts` after changing it, then rebuild —\n" +
      "the reference regenerates itself, and this fence checks the other direction.",
  );
}

main().catch((error) => {
  console.error("cli scan errored:", error);
  process.exit(2);
});
