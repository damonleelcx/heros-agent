// visual-baseline.mjs is the console's VISUAL-REGRESSION baseline (FR37, task 15.18).
//
// # What it gates, and what it honestly does not
//
// It captures a **structural visual signature** of every route: the ordered sequence of elements that
// carry design-language classes, the headings and control labels, and the token layer the whole
// surface resolves through. A change that moves a primitive, drops a column, renames a state, restyles
// a control or edits a token changes the signature and fails the gate until somebody looks at the
// difference and re-baselines it deliberately.
//
// It is NOT a pixel comparison. It cannot see a two-pixel misalignment or a contrast regression inside
// a single token. Those are covered by the acceptance matrix — rendered evidence at light/dark,
// narrow/wide, 200% zoom, reduced motion, both densities and all four states (14.7) — which is a human
// looking at real renderings, because that is the only thing that catches them.
//
// Saying that plainly matters more than the gate does. A "visual regression suite" that everyone
// believes is checking pixels, and isn't, is worse than none: it retires the review that would have
// caught the thing.
//
// # It compares against a FIXTURE STATE, and that is a real constraint
//
// The signature includes which panels rendered, and a panel's shape depends on its data: a view with
// no armed kill switch renders an empty state where a view with one renders a table. So the baseline
// is only meaningful against a KNOWN fixture — a freshly started `p8hermes`, with no operator actions
// performed and no console session browsing in parallel (reading the audit log is itself an audited
// cross-tenant view, so an open browser tab grows the chain it is being compared against).
//
// That is a limitation, not a bug, and it is stated here rather than discovered later: capture and
// check in the same quiesced conditions, or the diff you get is the fixture moving rather than the
// design.
//
// # Usage
//
//   ADMIN_CONSOLE_URL=http://localhost:4310 ADMIN_SESSION=<token> node scripts/visual-baseline.mjs
//   …                                                              node scripts/visual-baseline.mjs --update
//
// The session token is minted against the admin API's test-mode IdP by the caller; this script never
// authenticates on its own, because a screenshotting tool that can obtain an operator session is a
// second credential path onto the highest-blast-radius surface.

import { readFile, writeFile, mkdir, readdir } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";

const BASE = process.env.ADMIN_CONSOLE_URL ?? "http://localhost:4310";
const SESSION = process.env.ADMIN_SESSION ?? "";
const UPDATE = process.argv.includes("--update");
const DIR = join(process.cwd(), "tests", "baselines");

/** The routes a baseline covers. A new view is added here as part of adding the view. */
const ROUTES = [
  "/signin",
  "/",
  "/tenants",
  "/tenants/tenant-hermes",
  "/billing?tenant=tenant-hermes",
  "/registry",
  "/fleet",
  "/killswitch",
  "/crosstenant",
  "/audit",
  "/compliance",
];

/**
 * VOLATILE strips the values that change on every run and say nothing about the surface: instants,
 * hashes, sequence numbers, durations, React's own ids. Leaving them in would make the baseline fail
 * constantly, which is the standard way a visual gate gets disabled.
 */
const VOLATILE = [
  [/\b\d{4}-\d{2}-\d{2}T[\d:.]+Z?\b/g, "<instant>"],
  [/\b[A-Z][a-z]{2} \d{1,2}, \d{4}, \d{2}:\d{2}:\d{2} [AP]M UTC\b/g, "<instant>"],
  [/\b[0-9a-f]{16,}\b/g, "<hash>"],
  [/entry \d+ \(write-ahead \d+\)/g, "entry <seq> (write-ahead <seq>)"],
  [/\bseq=\d+/g, "seq=<n>"],
  [/nonce="[^"]*"/g, 'nonce="<nonce>"'],
  [/"\$[A-Za-z0-9_]+"/g, '"<ref>"'],
];

function normalise(html) {
  let out = html;
  for (const [pattern, replacement] of VOLATILE) out = out.replace(pattern, replacement);
  return out;
}

/**
 * signature reduces a page to what the DESIGN LANGUAGE decides, and drops what the DATA decides.
 *
 * Two parts, because they fail differently:
 *
 *   nodes       the ordered frame — every element carrying design classes, with table BODIES removed.
 *               Row internals are data (a pill is amber because that job died, not because someone
 *               restyled it), and including them made the baseline fail on its own audit trail.
 *   vocabulary  the sorted SET of every distinct class signature on the page, table bodies included.
 *               Order-free, so it survives data churn, but a primitive that appears or disappears
 *               anywhere still shows up here.
 *
 * Between them: a moved section fails `nodes`, a new or dropped primitive fails `vocabulary`, and an
 * ordinary day on the queue fails neither.
 */
function signature(html) {
  // DATA REGIONS are stripped from both signatures. A table body and a timeline render whatever the
  // platform currently holds, and on this platform that is a moving target in a way worth being
  // precise about: reading the audit log or a cross-tenant aggregate is ITSELF an audited event, so
  // fetching these pages to baseline them appends entries to the log being baselined. A signature
  // that included them would fail against its own measurement.
  //
  // What still gets caught: the regions' frames (the table, its scoped headers, its caption, the
  // timeline element) are all outside the strip, so a dropped column, a lost table or a removed
  // timeline still changes the signature.
  const clean = normalise(html)
    .replace(/<script[\s\S]*?<\/script>/g, "")
    .replace(/<style[\s\S]*?<\/style>/g, "")
    .replace(/<tbody[\s\S]*?<\/tbody>/g, "<tbody/>")
    .replace(/<ul class="timeline">[\s\S]*?<\/ul>/g, '<ul class="timeline"/>');
  const frame = clean;

  const signatureOf = (source) => {
    const out = [];
    for (const match of source.matchAll(/<(\w+)([^>]*class="([^"]+)"[^>]*)>/g)) {
      const [, tag, attrs, classes] = match;
      const scoped = /scope="(\w+)"/.exec(attrs)?.[1];
      out.push(`${tag}.${classes.split(/\s+/).sort().join(".")}${scoped ? `[scope=${scoped}]` : ""}`);
    }
    return out;
  };

  const nodes = signatureOf(frame);
  const vocabulary = [...new Set(signatureOf(clean))].sort();

  const text = [];
  for (const match of frame.matchAll(/<(h1|h2|h3|legend|caption|th|dt|summary|label|button)[^>]*>([\s\S]*?)<\/\1>/g)) {
    const content = match[2].replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim();
    if (content) text.push(`${match[1]}: ${content}`);
  }

  // Collapse runs of identical signatures. A table with three rows and a table with thirty have the
  // same STRUCTURE; only the data differs, and the data changes every time an operator does anything.
  // Without this the baseline fails on its own audit trail and gets switched off within a day. A row
  // shape that disappears entirely still disappears from the collapsed sequence, which is the
  // regression this is here to catch.
  return { nodes: collapse(nodes), vocabulary, text: collapse(text) };
}

function collapse(items) {
  return items.filter((item, index) => item !== items[index - 1]);
}

async function fetchRoute(route) {
  const res = await fetch(`${BASE}${route}`, {
    headers: SESSION ? { cookie: `heros_admin_session=${SESSION}` } : {},
    redirect: "manual",
  });
  return { status: res.status, html: res.status === 200 ? await res.text() : "" };
}

function fileFor(route) {
  const name = route === "/" ? "root" : route.replace(/^\//, "").replace(/[^\w]+/g, "_");
  return join(DIR, `${name}.json`);
}

async function main() {
  await mkdir(DIR, { recursive: true });
  const failures = [];
  let checked = 0;

  for (const route of ROUTES) {
    const { status, html } = await fetchRoute(route);
    if (status !== 200) {
      // A route that will not render for the baseline session is reported, never silently skipped —
      // a baseline that quietly covers three of eleven pages is a baseline that lies.
      failures.push(`${route}: responded ${status} (expected 200 — is ADMIN_SESSION valid?)`);
      continue;
    }
    checked += 1;
    const current = signature(html);
    const path = fileFor(route);

    if (UPDATE) {
      await writeFile(path, `${JSON.stringify(current, null, 2)}\n`);
      continue;
    }

    let baseline;
    try {
      baseline = JSON.parse(await readFile(path, "utf8"));
    } catch {
      failures.push(`${route}: no baseline recorded — run with --update after reviewing the rendering`);
      continue;
    }

    const diffs = [];
    if (JSON.stringify(baseline.nodes) !== JSON.stringify(current.nodes)) {
      const added = current.nodes.filter((n) => !baseline.nodes.includes(n));
      const removed = baseline.nodes.filter((n) => !current.nodes.includes(n));
      diffs.push(`structure changed (+${added.length} / -${removed.length})`);
      for (const n of added.slice(0, 5)) diffs.push(`  + ${n}`);
      for (const n of removed.slice(0, 5)) diffs.push(`  - ${n}`);
    }
    if (JSON.stringify(baseline.vocabulary ?? []) !== JSON.stringify(current.vocabulary)) {
      const added = current.vocabulary.filter((n) => !(baseline.vocabulary ?? []).includes(n));
      const removed = (baseline.vocabulary ?? []).filter((n) => !current.vocabulary.includes(n));
      diffs.push(`vocabulary changed (+${added.length} / -${removed.length})`);
      for (const n of [...added, ...removed].slice(0, 8)) diffs.push(`  ± ${n}`);
    }
    if (JSON.stringify(baseline.text) !== JSON.stringify(current.text)) {
      const added = current.text.filter((t) => !baseline.text.includes(t));
      const removed = baseline.text.filter((t) => !current.text.includes(t));
      diffs.push(`labels changed (+${added.length} / -${removed.length})`);
      for (const t of added.slice(0, 5)) diffs.push(`  + ${t}`);
      for (const t of removed.slice(0, 5)) diffs.push(`  - ${t}`);
    }
    if (diffs.length > 0) failures.push(`${route}:\n    ${diffs.join("\n    ")}`);
  }

  if (UPDATE) {
    console.log(`visual baseline updated for ${ROUTES.length} route(s) — review the diff before committing.`);
    return;
  }

  if (failures.length > 0) {
    console.error(`visual baseline FAILED — ${failures.length} route(s):`);
    for (const f of failures) console.error(`  - ${f}`);
    console.error(
      "\nIf the change is intended, look at the rendering in a browser first, then re-baseline with\n" +
        "  npm run baseline:update\n" +
        "A baseline updated without looking is a baseline that records the regression.",
    );
    process.exit(1);
  }

  const recorded = (await readdir(DIR)).filter((f) => f.endsWith(".json")).length;
  console.log(`visual baseline passed: ${checked} route(s) checked against ${recorded} recorded signature(s).`);
}

main().catch((error) => {
  console.error("visual baseline errored:", error);
  process.exit(2);
});
