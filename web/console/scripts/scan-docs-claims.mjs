// scan-docs-claims.mjs extends the claim rule from the marketing page to `/docs/**` (task 4.6).
//
// # Why documentation needs the same gate the home page has
//
// `scan-claims.mjs` exists because a marketing page is the file nobody re-reads, so it drifts toward
// describing the roadmap in the present tense. Documentation drifts the same way for a different reason:
// it is written while a feature is being built, in the tense the writer is thinking in, and the feature
// slips. The result is a page describing something that does not ship — found by a customer, after the
// sale, as a support ticket.
//
// So a documentation page may not describe a capability that is not `shipped: true` with a named owning
// phase in `src/content/capabilities.ts`, and may not name an install channel the release pipeline does
// not publish.
//
// # How a page claims something
//
// By naming a capability id in front matter: `claims: discover, evaluate`. Explicit rather than inferred
// from prose, for the reason Decision 3 gives about materiality — a heuristic over English is plausible
// and wrong in the cases that matter, and it would fire on a sentence that says a capability does NOT
// do something, which is the sentence we most want people to write.
//
// # What it deliberately does not check
//
//   - Whether the page's prose is accurate ABOUT the capability it claims. The claim is checkable; the
//     paragraph is a review responsibility. That gap is the same one `scan-claims.mjs` names, and it is
//     stated here rather than implied.
//   - Tone, emphasis, and what a page omits. No script can do that, and a fence that implied otherwise
//     would stop the human review that catches the rest.
//   - Capability claims in LEGAL documents. Those are governed by counsel and by the terms
//     reconciliation, not by the capability manifest.

import { readFile } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";
import { documents, report } from "./lib/corpus.mjs";

const ROOT = process.cwd();

async function manifest() {
  const source = await readFile(join(ROOT, "src", "content", "capabilities.ts"), "utf8");
  const shipped = new Set();
  const listed = new Set();
  // The same parse `scan-claims.mjs` uses, and for the same reason: this runs before the build and must
  // not need a TypeScript toolchain to answer a question about a literal table.
  for (const match of source.matchAll(/\{\s*id:\s*"([a-z0-9-]+)"([\s\S]*?)\}\s*,/g)) {
    const [, id, body] = match;
    listed.add(id);
    if (/\bshipped:\s*true\b/.test(body) && /\bphase:\s*"[^"]+"/.test(body)) shipped.add(id);
  }
  return { shipped, listed };
}

async function main() {
  const { shipped, listed } = await manifest();
  const facts = JSON.parse(await readFile(join(ROOT, "src", "generated", "docs-facts.json"), "utf8"));
  const deliveredChannels = new Set(facts.channels.filter((c) => c.delivered).map((c) => c.label.toLowerCase()));
  const pendingChannels = facts.channels.filter((c) => !c.delivered);

  const findings = [];
  const docs = await documents();
  let claims = 0;

  for (const document of docs) {
    if (document.kind !== "docs") continue;

    const declared = (document.frontMatter.claims ?? "")
      .split(",")
      .map((id) => id.trim())
      .filter(Boolean);
    for (const id of declared) {
      claims += 1;
      if (!listed.has(id)) {
        findings.push(`${document.path}: claims "${id}", which has no entry in the capability manifest`);
      } else if (!shipped.has(id)) {
        findings.push(
          `${document.path}: claims "${id}", which is in the manifest but is not shipped with an owning phase`,
        );
      }
    }

    // An install channel named as available while the pipeline does not publish it. The command is what
    // a reader copies, so the check is on the command's own words rather than on the channel's label
    // appearing anywhere in prose — a page that EXPLAINS a channel is pending must remain writable.
    for (const line of document.lines) {
      for (const channel of pendingChannels) {
        const install = channel.install.split(/\s*;\s*/)[0].trim();
        // Take the first two words of the install command — `brew install`, `scoop bucket`, `winget
        // install` — which is what a copied line begins with.
        const signature = install.split(/\s+/).slice(0, 2).join(" ");
        if (!signature || signature.length < 6) continue;
        if (line.text.includes(signature)) {
          findings.push(
            `${document.path}:${line.number}: prints \`${signature} …\` for the ${channel.label} channel, ` +
              `which the pipeline does not publish yet — ${channel.blocker}. ` +
              `An install command that 404s is the worst possible first sentence of a product.`,
          );
        }
      }
    }
  }

  report(
    "docs claim scan",
    findings,
    docs.length,
    `${claims} declared capability claim(s), all shipped with an owning phase; ` +
      `${deliveredChannels.size} published channel(s), ${pendingChannels.length} pending and none of them printed as installable.`,
    "A documentation page may only describe a capability listed in src/content/capabilities.ts with\n" +
      "shipped: true and an owning phase, and may only print an install command for a channel the release\n" +
      "pipeline actually publishes.",
  );
}

main().catch((error) => {
  console.error("docs claim scan errored:", error);
  process.exit(2);
});
