// scan-bundle.mjs is the BUILD-TIME credential + price fence (FR20, FR28).
//
// # Why a written rule is not enough
//
// "Don't put the platform credential in the client bundle" and "don't hardcode a price" are rules with
// a demonstrated failure rate: a barrel import three hops away, a debug log left in, a constant copied
// from the backend. This script is the machine that enforces them. It scans the SHIPPED client bundle
// — the JavaScript the browser actually downloads — and fails the build if it finds credential
// material or a priced literal. `server-only` is the first guard (it turns a bad import into a compile
// error); this is the second, independent one, because defense in depth on this surface is the whole
// point.
//
// It runs as the `postbuild` step of `npm run build`, so a bundle that would leak cannot ship.
//
// # The payload ceiling (R18 / FR40)
//
// The third check is a WEIGHT budget. On this console it also protects the first check's premise: the
// confidence a credential scan gives is proportional to how auditable the bundle is, and a payload
// that grows without limit is one nobody reads. It is equally how the trend review's REJECTED items
// stay rejected — a 3D viewer, an animation library or a chatbot widget cannot arrive quietly on a
// surface with a stated ceiling (see web/design-system/trend-ledger.md).
//
// The measurement comes from the build's own manifests rather than from the directory, because
// `next dev` writes its unminified chunks into the same place; a tree a dev server has touched is
// REFUSED rather than misreported, since a wrong number is worse than none — the wrong number gets
// acted on.

import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";
import { OBSERVABILITY_RUNTIMES } from "../../design-system/third-party-policy.ts";
import { guardedChunks } from "../../design-system/reachability.mjs";

/**
 * ROOT is the application directory whose build is measured. It defaults to the working directory —
 * the only value a build ever uses — and is overridable with `--root <dir>` for ONE purpose: making
 * this fence go red against a fixture build, without a probe file ever being written into a real
 * console. A fence nobody has seen fail is a fence nobody knows is connected, and the alternative
 * (injecting an import into a shipped layout and rebuilding) leaves a session recorder wired into the
 * root layout if the run crashes between injecting and reverting.
 */
const rootFlag = process.argv.indexOf("--root");
const ROOT = rootFlag >= 0 ? process.argv[rootFlag + 1] : process.cwd();

const CLIENT_DIR = join(ROOT, ".next", "static");

// The ceiling, in bytes of shipped JavaScript. Raising it is a decision with an argument attached.
const PAYLOAD_CEILING_BYTES = 1_400_000;

// Runtimes that only ever arrive as decoration, named rather than inferred from size: a small 3D
// library is still a 3D library on a console whose trend review rejected 3D.
const DECORATIVE_RUNTIMES = [
  { name: "three.js", needle: "THREE.WebGLRenderer" },
  { name: "a WebGL context", needle: 'getContext("webgl' },
  { name: "GSAP", needle: "gsap.registerPlugin" },
  { name: "Lottie", needle: "lottie-web" },
];

/*
 * # The INVERSE runtime scan (P24 task 1.5, design D9)
 *
 * The decorative list asks "did a 3D library ship". This one asks "did a recorder ship where a
 * recorder is refused" — and on THIS console the answer is every route. An operator screen renders
 * cross-tenant aggregates, tenant names, active impersonation state and audit rows; a session
 * recording of it is a copy of the material the whole platform exists to keep inside a boundary, and
 * an analytics tag would put an operator's navigation into a third party's logs.
 *
 * So the guarded partition here is the whole application, and the customer console's public exemption
 * has no counterpart. The needles come from the shared artefact rather than from a copy, so a runtime
 * added to that table is caught in both consoles rather than in whichever one somebody remembered.
 */

/** shippedFiles returns the chunks the BUILD says the browser downloads. */
async function shippedFiles() {
  const files = new Set();
  let found = false;
  for (const name of ["build-manifest.json", "app-build-manifest.json"]) {
    let manifest;
    try {
      manifest = JSON.parse(await readFile(join(ROOT, ".next", name), "utf8"));
    } catch {
      continue;
    }
    found = true;
    collect(manifest, files);
  }
  return found ? files : null;
}

function collect(node, out) {
  if (typeof node === "string") {
    if (node.startsWith("static/")) out.add(node);
    return;
  }
  if (Array.isArray(node)) {
    for (const item of node) collect(item, out);
    return;
  }
  if (node && typeof node === "object") {
    for (const value of Object.values(node)) collect(value, out);
  }
}

/**
 * appManifest returns the route -> chunks map, or null if this build did not produce one.
 *
 * Separate from `shippedFiles` because the two ask different questions of the same data: that one asks
 * "is this chunk shipped", this one asks "which routes load it".
 */
async function appManifest() {
  try {
    return JSON.parse(await readFile(join(ROOT, ".next", "app-build-manifest.json"), "utf8"));
  } catch {
    return null;
  }
}

/** contaminated reports whether `next dev` has written into this `.next` tree. */
async function contaminated() {
  try {
    await readFile(join(ROOT, ".next", "static", "development", "_buildManifest.js"), "utf8");
    return true;
  } catch {
    return false;
  }
}

// Credential material the client bundle must never contain. The env var NAMES are here, not their
// values — a value would itself be a leak, and if the build environment has the real credential we
// compare against it directly below.
const CREDENTIAL_ENV = ["ADMIN_PLATFORM_CREDENTIAL"];

// Priced literals: a currency amount, a percentage, or a named price-band value. Plans are named and
// prices are references; a literal here means a business number reached the client (FR28).
//
// The currency pattern deliberately requires a MONETARY form — a decimal amount ($0.003, $12.50) or a
// multi-digit figure with a separator — rather than a bare `$1`. Minified framework code is full of
// `.replace(re, "$1")` regex backreferences, and a fence that flagged those would cry wolf on every
// build until somebody disabled it. A real price in this domain always carries decimals; that is what
// this matches, and nothing in our own source should carry even that.
const PRICE_PATTERNS = [
  /\$\s?\d[\d,]*\.\d/, // $0.003, $1,299.00
  /\b\d[\d,]*\.\d+\s?(usd|eur|gbp)\b/i,
  /\b\d+(\.\d+)?\s?%/,
  /price[_-]?(amount|value|band)\s*[:=]\s*['"]?\d/i,
];

async function* walk(dir) {
  let entries;
  try {
    entries = await readdir(dir, { withFileTypes: true });
  } catch {
    return;
  }
  for (const entry of entries) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      yield* walk(full);
    } else if (entry.name.endsWith(".js") || entry.name.endsWith(".map")) {
      // `.map` as well as `.js`: a source map is not a chunk, and the P24 rule is that one must not be
      // in the shipped tree at all — a walk that only looked at `.js` could never see it.
      yield full;
    }
  }
}

function credentialNeedles() {
  const needles = [];
  for (const name of CREDENTIAL_ENV) {
    const value = process.env[name];
    // Only compare against a real value if it is long enough to be a genuine secret; a short or absent
    // value would produce false positives against ordinary code.
    if (value && value.length >= 12) needles.push({ name, value });
  }
  return needles;
}

async function main() {
  const needles = credentialNeedles();
  const findings = [];
  let scanned = 0;
  let bytes = 0;

  if (await contaminated()) {
    console.error(
      "bundle scan: `.next` has been written by a dev server, so the shipped payload cannot be measured.\n" +
        "Stop `next dev` and run `npm run build`, which runs this scan against the build it just produced.",
    );
    process.exit(2);
  }

  const manifest = await shippedFiles();

  // A missing app manifest is REFUSED rather than treated as "nothing is guarded". Passing by default
  // would mean a session recorder could reach an operator route with a green build, which is the exact
  // outcome this scan exists to make impossible.
  const routes = await appManifest();
  if (!routes) {
    console.error(
      "bundle scan: no .next/app-build-manifest.json, so which chunks an operator route loads is\n" +
        "unknown. The observability-runtime rule cannot be evaluated and is not passed by default.\n" +
        "Run `next build` and re-run this scan.",
    );
    process.exit(2);
  }
  const guarded = guardedChunks(routes, "operator");

  for await (const file of walk(CLIENT_DIR)) {
    const content = await readFile(file, "utf8");
    if (file.endsWith(".map")) {
      // Counted as a FINDING, never as payload: a map is not something the browser is meant to
      // download, so adding its bytes to the ceiling would be measuring the wrong thing twice.
      findings.push(
        `SOURCE MAP: ${file} is in the shipped tree — a source map is a readable copy of our own source ` +
          `and is never served from a customer-facing origin`,
      );
      continue;
    }
    scanned += 1;
    const relative = file.slice(file.indexOf(join(".next", "static")) + ".next/".length);
    const shipped = manifest ? manifest.has(relative) : true;
    if (shipped) bytes += Buffer.byteLength(content);

    for (const { name, needle } of DECORATIVE_RUNTIMES) {
      if (shipped && content.includes(needle)) {
        findings.push(`PAYLOAD: ${name} is present in shipped bundle ${file}`);
      }
    }

    // 🔴 SOURCE MAPS (P24 task 3.6). A `.map` beside a shipped chunk is a readable copy of our own
    // source, served from a customer-facing origin to anybody who opens dev tools. Frames are made
    // readable for the platform's own hosted deployment by uploading maps out of band, from CI, with a
    // release-scoped token — never by shipping them. The `sourceMappingURL` check is the half that
    // matters most: a map file can be deleted after the build and the POINTER left behind, which turns
    // every page load into a 404 and tells a reader exactly what to go looking for.
    const pointer = /\/\/#\s*sourceMappingURL=(\S+)/.exec(content);
    if (shipped && pointer && !pointer[1].startsWith("data:")) {
      findings.push(
        `SOURCE MAP: ${relative} points at ${pointer[1]} — the pointer ships even when the map does not, ` +
          `which names the file an attacker should ask for`,
      );
    }

    for (const runtime of OBSERVABILITY_RUNTIMES) {
      if (!runtime.tenantForbidden) continue;
      if (!guarded.has(relative)) continue;
      if (!content.includes(runtime.needle)) continue;
      findings.push(
        `OBSERVABILITY RUNTIME: ${runtime.name} (${runtime.kind}) is in ${relative}, which a browser ` +
          `downloads on an operator route. Every route of this console is an operator route; a ` +
          `${runtime.kind} runtime is refused here structurally, not configured off.`,
      );
    }

    for (const { name, value } of needles) {
      if (content.includes(value)) {
        findings.push(`CREDENTIAL: ${name} value found in shipped bundle ${file}`);
      }
    }
    for (const pattern of PRICE_PATTERNS) {
      const match = content.match(pattern);
      if (match) {
        findings.push(`PRICE: literal ${JSON.stringify(match[0])} in shipped bundle ${file}`);
      }
    }
  }

  if (bytes > PAYLOAD_CEILING_BYTES) {
    findings.push(
      `PAYLOAD: shipped client bundle is ${bytes} bytes, over the ${PAYLOAD_CEILING_BYTES}-byte ceiling ` +
        `by ${bytes - PAYLOAD_CEILING_BYTES}`,
    );
  }

  if (scanned === 0) {
    console.error("bundle scan: no client bundle found under .next/static — run `next build` first");
    process.exit(2);
  }

  if (findings.length > 0) {
    console.error(`bundle scan FAILED — ${findings.length} finding(s):`);
    for (const f of findings) console.error(`  - ${f}`);
    console.error(
      "\nThe operator console holds the platform credential server-side only, and carries plan names " +
        "and price references, never values. Remove the offending material and rebuild.",
    );
    process.exit(1);
  }

  console.log(
    `bundle scan passed: ${scanned} client chunk(s) scanned, ${bytes} shipped bytes ` +
      `(${PAYLOAD_CEILING_BYTES - bytes} under the ${PAYLOAD_CEILING_BYTES}-byte ceiling), ` +
      `no credential material, priced literal, or decorative runtime.`,
  );
  console.log(
    `  reachability: ${guarded.size} chunk(s) reachable from an operator route (every route is one); ` +
      `no analytics or session-replay runtime among them.`,
  );
}

main().catch((error) => {
  console.error("bundle scan errored:", error);
  process.exit(2);
});
