// scan-claims.mjs is the BUILD-TIME claim fence for the PUBLIC surface (R15, FR33, task 7b.2).
//
// # The problem it exists for
//
// A marketing page is the one file in a repository that no test reads and no engineer re-checks after
// the first week. It is therefore the page most likely to describe the roadmap in the present tense —
// and that drift is not caught internally. It is caught by a customer, after the sale, as a support
// ticket or a refund.
//
// 🔴 The sales-operations rule is "only promise what has been delivered". This turns it into a gate.
//
// # How it works
//
// Every capability claim on the public surface is rendered through the `<Claim id="…">` component, and
// every id must exist in `src/content/capabilities.ts` with `shipped: true` and a named owning phase.
// A claim that is unlisted, or listed but not shipped, FAILS THE BUILD — it cannot reach the page.
//
// The manifest is not a list of marketing copy. It is a list of things the platform DOES, each tied to
// the phase that delivers it, so the question "can we say this?" has a checkable answer that the person
// writing the sentence does not get to decide alone.
//
// # What it deliberately does not check
//
// Whether a sentence is *honest* in the wider sense — tone, emphasis, what it omits. No script can do
// that. The manifest makes the CLAIM checkable; the boundary statements beside it (FR34, R15) are a
// review responsibility, and saying so plainly is better than implying the gate covers more than it
// does.

import { readdir, readFile } from "node:fs/promises";
import { join, relative } from "node:path";
import process from "node:process";

const ROOT = process.cwd();
const MANIFEST = join(ROOT, "src", "content", "capabilities.ts");
// The public surface. Everything under /app is behind a session and makes no marketing claim.
const PUBLIC_DIRS = [join(ROOT, "src", "app", "(public)"), join(ROOT, "src", "components", "marketing")];

async function exists(path) {
  try {
    await readFile(path, "utf8");
    return true;
  } catch {
    return false;
  }
}

async function* walk(dir) {
  let entries;
  try {
    entries = await readdir(dir, { withFileTypes: true });
  } catch {
    return;
  }
  for (const entry of entries) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(full);
    else if (/\.(tsx|ts)$/.test(entry.name)) yield full;
  }
}

/**
 * shippedIds parses the manifest for the ids marked shipped.
 *
 * A regex over the source rather than an import, because this script runs before the build and must
 * not require a TypeScript toolchain to answer a question about a literal table. The manifest's shape
 * is asserted by a test, so a refactor that breaks this parse is caught there rather than silently
 * turning the gate into a no-op — which is the failure mode a lenient parser would have.
 */
async function manifestIds() {
  const source = await readFile(MANIFEST, "utf8");
  const shipped = new Set();
  const listed = new Set();
  const entry = /\{\s*id:\s*"([a-z0-9-]+)"([\s\S]*?)\}\s*,/g;
  let match;
  while ((match = entry.exec(source)) !== null) {
    const [, id, body] = match;
    listed.add(id);
    if (/\bshipped:\s*true\b/.test(body) && /\bphase:\s*"[^"]+"/.test(body)) shipped.add(id);
  }
  return { shipped, listed };
}

async function main() {
  const claims = new Map(); // id -> [file, …]
  let scanned = 0;

  for (const dir of PUBLIC_DIRS) {
    for await (const file of walk(dir)) {
      scanned += 1;
      const rel = relative(ROOT, file);
      const source = await readFile(file, "utf8");
      const claim = /<Claim\s[^>]*id="([^"]+)"/g;
      let match;
      while ((match = claim.exec(source)) !== null) {
        const list = claims.get(match[1]) ?? [];
        list.push(rel);
        claims.set(match[1], list);
      }
    }
  }

  if (claims.size === 0) {
    console.log(`claim scan passed: ${scanned} public file(s), no capability claim rendered yet.`);
    return;
  }

  if (!(await exists(MANIFEST))) {
    console.error(
      `claim scan FAILED — the public surface renders ${claims.size} capability claim(s) and there is no manifest at src/content/capabilities.ts.`,
    );
    process.exit(1);
  }

  const { shipped, listed } = await manifestIds();
  const findings = [];
  for (const [id, files] of claims) {
    if (!listed.has(id)) {
      findings.push(`UNLISTED: claim "${id}" (${files.join(", ")}) has no entry in the capability manifest`);
    } else if (!shipped.has(id)) {
      findings.push(
        `UNSHIPPED: claim "${id}" (${files.join(", ")}) is in the manifest but is not marked shipped with an owning phase`,
      );
    }
  }

  if (findings.length > 0) {
    console.error(`claim scan FAILED — ${findings.length} finding(s):`);
    for (const f of findings) console.error(`  - ${f}`);
    console.error(
      "\nThe public surface may only claim capabilities the platform has shipped.\n" +
        "Add the capability to src/content/capabilities.ts with its owning phase once it ships — or remove the claim.",
    );
    process.exit(1);
  }

  console.log(`claim scan passed: ${claims.size} claim(s), all shipped and attributed to an owning phase.`);
}

main().catch((error) => {
  console.error("claim scan errored:", error);
  process.exit(2);
});
