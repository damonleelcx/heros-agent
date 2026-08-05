// scan-ledger.mjs is the OPERATOR-OVERSIGHT fence (P26 tasks 1.2–1.5).
//
// # Why this exists as a machine and not as a review habit
//
// Fourteen phases landed after the operator console shipped, and the console did not move. Nothing
// failed while that happened, because the operator console was nobody's acceptance criterion. A
// checklist is what we already had; fourteen phases is its measured failure rate. So the rule became a
// build failure: a capability with no recorded operator story fails, naming itself.
//
// The same lesson the token scan and the bundle scan encode, and the same lesson the frontend scope
// guard records explicitly — a manual scan, and then an agent scan, still missed the fifth occurrence,
// which is the proof that the rule must be machine-enforced rather than remembered.
//
// # What it checks, precisely
//
//   A. openspec/specs/                             ⇄ ledger section A   (both directions, exact)
//   B. openspec/changes/p26-…/specs/               ⇄ ledger section B   (both directions, exact)
//   C. capabilities used in surfaces.ts            ⇄ ledger section C   (both directions, exact)
//   forward   every `surface` row's destination exists in surfaces.ts
//   reverse   every destination in surfaces.ts is named by at least one `surface` row
//   state     every row is one of three states, and no fourth is accepted
//   detail    `no-operator-surface` carries a reason AND a deciding phase;
//             `not-yet-readable` NAMES a collection after the word `requires`
//   scope     the ledger states that it governs operator surfaces only
//
// # Why the ledger is parsed rather than generated
//
// A generated ledger would record what the code already says, which is the one thing a fence does not
// need to be told. The rows that matter are the judgements — a reasoned absence, a named missing
// collection — and a fence cannot force judgement. It can force the judgement to be recorded, and
// force it to stay true.

import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";

const ROOT = process.cwd();
const REPO = join(ROOT, "..", "..");

const LEDGER = join(REPO, "openspec", "operator-surface-ledger.md");
const SPECS_DIR = join(REPO, "openspec", "specs");
/**
 * The UNARCHIVED changes this ledger governs, named one per line.
 *
 * 🔴 P27 added this list, because the constant it replaces was a single hard-coded P26 path — so the
 * fence covered exactly the change that introduced it and nothing after. P27's five capabilities were
 * invisible to it, and would have stayed invisible until the change archived into `openspec/specs/`,
 * which is months of oversight-free surface area for the one thing this file exists to prevent. A fence
 * scoped to its own author is the shape of the drift it was written against.
 *
 * # Why a NAMED LIST and not a derived one
 *
 * The obvious derivation — "a change's capability is landing if `openspec/specs/<name>` does not exist"
 * — was tried and is wrong. It reports 87 capabilities across 30 changes: every phase back to P0 keeps
 * its change directory after archiving, and most of their capabilities were archived under different
 * names or folded into others. Adopting it would demand 80 ledger rows for capabilities that shipped
 * years of phases ago, which is how a fence gets switched off.
 *
 * So the list is a decision, made once per phase, by the phase. Adding a change here is a line of diff
 * in a review; forgetting to is what section 10.5 of P27 exists to catch, and it now catches it for
 * whoever comes next rather than only for P27.
 */
const GOVERNED_CHANGES = ["p26-operator-console-refresh", "p27-account-system"];
const CHANGE_SPECS_DIRS = GOVERNED_CHANGES.map((c) => join(REPO, "openspec", "changes", c, "specs"));
const SURFACES = join(ROOT, "src", "lib", "surfaces.ts");

/** The three states. There is no fourth, and adding one here is a spec change, not a fix. */
const STATES = new Set(["surface", "no-operator-surface", "not-yet-readable"]);

/** Section headings, mapped to the source each is asserted against. */
const SECTION_A = "A. Built capabilities";
const SECTION_B = "B. Capabilities landing in this change";
const SECTION_C = "C. Operator destinations";
const LEDGER_SECTIONS = [SECTION_A, SECTION_B, SECTION_C];

async function directories(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  return entries.filter((e) => e.isDirectory()).map((e) => e.name).sort();
}

/**
 * parseLedger reads the ledger's three-column tables, remembering which `##` section each row is under.
 * Header rows and separator rows are skipped by shape, so a table can be reformatted without the fence
 * needing to know.
 */
function parseLedger(source) {
  const rows = [];
  let section = "";
  for (const [index, line] of source.split("\n").entries()) {
    const heading = line.match(/^##\s+(.*)$/);
    if (heading) {
      section = heading[1].trim();
      continue;
    }
    // Only the three lettered sections carry rows. The prose above them explains the states in a
    // table of its own, and a fence that read documentation as data would fail on its own manual.
    if (!LEDGER_SECTIONS.some((prefix) => section.startsWith(prefix))) continue;
    if (!line.trim().startsWith("|")) continue;
    const cells = line.split("|").slice(1, -1).map((c) => c.trim());
    if (cells.length !== 3) continue;
    if (/^-+$/.test(cells[0].replace(/[\s:]/g, ""))) continue; // separator
    if (cells[0] === "capability" || cells[0] === "State") continue; // header
    rows.push({ section, subject: cells[0], state: cells[1], detail: cells[2], line: index + 1 });
  }
  return rows;
}

/** hrefsIn extracts every `href:` string literal from surfaces.ts, in order, duplicates collapsed. */
function hrefsIn(source) {
  return [...new Set([...source.matchAll(/href:\s*"([^"]+)"/g)].map((m) => m[1]))];
}

/** capabilitiesIn extracts every `capability:` string literal from surfaces.ts. */
function capabilitiesIn(source) {
  return [...new Set([...source.matchAll(/capability:\s*"([^"]+)"/g)].map((m) => m[1]))].sort();
}

/** destinationsOf splits a `surface` row's detail into destinations. */
function destinationsOf(detail) {
  return detail
    .split(",")
    .map((d) => d.trim())
    .filter(Boolean);
}

function compareSets(findings, label, expected, actual, what) {
  for (const name of expected) {
    if (!actual.includes(name)) {
      findings.push(
        `${label}: ${what} \`${name}\` appears in NO ledger row. ` +
          `Add a row resolving it to a surface, a reasoned absence, or a named missing collection.`,
      );
    }
  }
  for (const name of actual) {
    if (!expected.includes(name)) {
      findings.push(`${label}: ledger row \`${name}\` names no ${what} that exists — a typo is not coverage.`);
    }
  }
}

async function main() {
  const findings = [];

  const ledgerSource = await readFile(LEDGER, "utf8");
  const surfacesSource = await readFile(SURFACES, "utf8");
  const rows = parseLedger(ledgerSource);

  if (rows.length === 0) {
    console.error("ledger scan: no rows parsed — is the ledger at openspec/operator-surface-ledger.md?");
    process.exit(2);
  }

  // The ledger must state its own boundary, so a `no-operator-surface` row cannot be misread as a
  // claim that no surface of any kind exists for that capability.
  if (!/\*\*Scope: this ledger governs the OPERATOR console only\*\*/.test(ledgerSource)) {
    findings.push("ledger: the scope statement is missing — a reader could mistake it for a claim about every console");
  }

  // ── State and detail, per row ──────────────────────────────────────────────
  for (const row of rows) {
    if (!STATES.has(row.state)) {
      findings.push(
        `ledger:${row.line}: \`${row.subject}\` has state \`${row.state}\`, which is not one of ` +
          `surface / no-operator-surface / not-yet-readable. There is no fourth state.`,
      );
      continue;
    }
    if (row.state === "no-operator-surface") {
      if (!/\(P\d+(?:\.\d+)?\)\s*$/.test(row.detail)) {
        findings.push(
          `ledger:${row.line}: \`${row.subject}\` is no-operator-surface but names no deciding phase ` +
            `— end the reason with the phase, e.g. \`(P26)\`, so the decision is attributable.`,
        );
      }
      if (row.detail.replace(/\(P\d+(?:\.\d+)?\)\s*$/, "").trim().length < 20) {
        findings.push(
          `ledger:${row.line}: \`${row.subject}\` is no-operator-surface with no reason — a reasoned ` +
            `absence and an oversight must not look the same.`,
        );
      }
    }
    if (row.state === "not-yet-readable") {
      // The whole point of this state: it names the collection that would make the read possible, so a
      // later phase finds a specified task rather than a wish. An empty detail would turn the state
      // into a place to park one.
      const named = row.detail.match(/\brequires\b\s+(.*)$/i);
      if (!named || named[1].trim().length < 20) {
        findings.push(
          `ledger:${row.line}: \`${row.subject}\` is not-yet-readable but names no collection — write ` +
            `\`requires <the collection, signal or store that would make this readable>\`. ` +
            `An unnamed missing input is a wish, not a task.`,
        );
      }
    }
    if (row.state === "surface" && destinationsOf(row.detail).length === 0) {
      findings.push(`ledger:${row.line}: \`${row.subject}\` claims a surface but names no destination.`);
    }
  }

  // ── A / B / C, each asserted against its own source, both directions ──────
  const sectionRows = (prefix) => rows.filter((r) => r.section.startsWith(prefix));

  compareSets(
    findings,
    "section A",
    await directories(SPECS_DIR),
    sectionRows(SECTION_A).map((r) => r.subject),
    "capability",
  );
  compareSets(
    findings,
    "section B",
    // Every governed change's capabilities, as one set. Sorted and de-duplicated so two changes landing
    // the same capability name is a single row rather than a mismatch nobody can act on.
    [...new Set((await Promise.all(CHANGE_SPECS_DIRS.map(directories))).flat())].sort(),
    sectionRows(SECTION_B).map((r) => r.subject),
    "capability",
  );
  compareSets(
    findings,
    "section C",
    capabilitiesIn(surfacesSource),
    sectionRows(SECTION_C).map((r) => r.subject),
    "gating capability",
  );

  // ── Forward: a row cannot name a surface that does not exist ──────────────
  const hrefs = hrefsIn(surfacesSource);
  const named = new Set();
  for (const row of rows) {
    if (row.state !== "surface") continue;
    for (const destination of destinationsOf(row.detail)) {
      named.add(destination);
      if (!hrefs.includes(destination)) {
        findings.push(
          `ledger:${row.line}: \`${row.subject}\` names destination \`${destination}\`, which is absent ` +
            `from surfaces.ts — a route without its registry entry is reachable from the nav and missing ` +
            `from the palette.`,
        );
      }
    }
  }

  // ── Reverse: a surface cannot exist unjustified ───────────────────────────
  for (const href of hrefs) {
    if (!named.has(href)) {
      findings.push(
        `surfaces.ts: destination \`${href}\` is named by no ledger row — a surface nobody can justify ` +
          `is a defect, not extra coverage.`,
      );
    }
  }

  if (findings.length > 0) {
    console.error(`ledger scan FAILED — ${findings.length} finding(s):`);
    for (const f of findings) console.error(`  - ${f}`);
    console.error(
      "\nEvery capability resolves to exactly one of three states in openspec/operator-surface-ledger.md:\n" +
        "  surface              a destination present in web/admin-console/src/lib/surfaces.ts\n" +
        "  no-operator-surface  a reason AND the deciding phase\n" +
        "  not-yet-readable     the collection that would make the read possible, named\n",
    );
    process.exit(1);
  }

  console.log(
    `ledger scan passed: ${rows.length} row(s), ${hrefs.length} destination(s), ` +
      `every capability resolved and both directions asserted.`,
  );
}

main().catch((error) => {
  console.error("ledger scan errored:", error);
  process.exit(2);
});
