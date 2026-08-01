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

/**
 * BANNED_PHRASES are the sentences this product may not say ANYWHERE it ships — public surface, signed-in
 * console, error copy, all of it (docs/sales/P21-billing-copy.md §1.3).
 *
 * "Risk is controlled" is the head of the list and the reason the list exists: the platform does not
 * control a payment processor's outcomes, a provider's dunning schedule, or whether a card clears. What
 * it can honestly promise is that risk is OBSERVABLE — every charge idempotent, every correction
 * additive and audited, every figure traceable to the record that justified it. A claim of control is a
 * promise made with somebody else's system, and it is the claim that gets quoted back during an
 * incident.
 *
 * The rest are the billing-specific over-claims: a schedule the platform does not set, a deletion that
 * never happens, and a subscription confirmed before the provider confirmed it.
 */
const BANNED_PHRASES = [
  { phrase: "风险可控", say: "risk is observable — name what the system actually guarantees" },
  { phrase: "risk is controlled", say: "risk is observable" },
  { phrase: "risk is controllable", say: "risk is observable" },
  { phrase: "guaranteed not to be charged", say: "state the idempotency guarantee: a retry produces one charge" },
  { phrase: "your data will be deleted", say: "nothing is deleted — a plan change is audited and reversible" },
  /*
   * P24 task 7.3. These four were TRUE when they were written and the configuration now contradicts
   * them, which is the most dangerous kind of shipped claim: nobody edits a sentence that used to be
   * right, and a customer holds us to it.
   *
   * The vendor's own hosted surfaces run three third-party products. What is still true, and is what
   * these `say` clauses point at, is much more specific — and being specific is the whole difference
   * between a claim and a disclosure.
   */
  { phrase: "we run no third-party code", say: "name the three, the surfaces they run on and the consent that gates them — see /legal/sub-processors" },
  { phrase: "no third-party code runs", say: "say WHERE none runs: no analytics tag and no session recorder on a signed-in page or on the operator console" },
  { phrase: "we use no analytics", say: "usage analytics runs on the public site only, off until a visitor turns it on" },
  /*
   * 🔴 NOT the bare phrase "no third-party origin".
   *
   * That was the first version and it fired immediately — on `src/middleware.ts`'s own doc comment,
   * which DESCRIBES the rule it enforces. A fence that punishes the explanation makes the explanation
   * the thing people delete, and the repository has already learned that twice. So the banned form is
   * the one a READER could be handed as a promise: a product-level claim, not a description of a prefix
   * rule. The prefix rule itself is asserted by tests, which is where it belongs.
   */
  { phrase: "this product names no third-party origin", say: "state the PREFIX: a tenant prefix names none except the error-reporting one" },
  { phrase: "the console names no third-party origin", say: "state the prefix — the public prefix names allowlisted origins" },
];

/** SCAN_ALL_DIRS is every shipped source file, because a banned phrase is banned everywhere. */
const SCAN_ALL_DIRS = [join(ROOT, "src")];

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

/** bannedPhrases walks every shipped source and reports any phrase the product may not say. */
async function bannedPhrases() {
  const findings = [];
  let scanned = 0;
  for (const dir of SCAN_ALL_DIRS) {
    for await (const file of walk(dir)) {
      scanned += 1;
      const source = (await readFile(file, "utf8")).toLowerCase();
      const rel = relative(ROOT, file);
      for (const { phrase, say } of BANNED_PHRASES) {
        // The phrase is matched case-insensitively and the FILE is named, never the surrounding
        // sentence: a scan that quoted the copy back would make the CI log the second place it lives.
        if (source.includes(phrase.toLowerCase())) {
          findings.push(`BANNED PHRASE: ${rel} says "${phrase}" — say instead: ${say}`);
        }
      }
    }
  }
  return { findings, scanned };
}

async function main() {
  const claims = new Map(); // id -> [file, …]
  let scanned = 0;

  const banned = await bannedPhrases();

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

  if (banned.findings.length > 0) {
    console.error(`claim scan FAILED — ${banned.findings.length} banned phrase(s):`);
    for (const f of banned.findings) console.error(`  - ${f}`);
    process.exit(1);
  }

  if (claims.size === 0) {
    console.log(
      `claim scan passed: ${scanned} public file(s), no capability claim rendered yet; ` +
        `${banned.scanned} shipped file(s) carry no banned phrase.`,
    );
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

  console.log(
    `claim scan passed: ${claims.size} claim(s), all shipped and attributed to an owning phase; ` +
      `${banned.scanned} shipped file(s) carry no banned phrase.`,
  );
}

main().catch((error) => {
  console.error("claim scan errored:", error);
  process.exit(2);
});
