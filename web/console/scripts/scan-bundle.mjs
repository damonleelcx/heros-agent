// scan-bundle.mjs is the BUILD-TIME credential + price fence (FR1/NFR1, FR34/NFR14, task 3.2).
//
// # Why a written rule is not enough
//
// "Don't put the platform credential in the client bundle" is a rule with a demonstrated failure rate:
// a barrel import three hops away, a debug log left in, a constant copied from the backend. The
// `server-only` import in `src/lib/platformApi.ts` is the first guard — it turns a bad import into a
// compile error. This is the second, independent one, and it looks at the JavaScript the browser
// actually downloads.
//
// The price half is the customer console's own requirement rather than an inheritance: this
// application contains a PUBLIC surface that names plans, and FR34 says plans are named and never
// priced. A number committed to a repository outlives the moment it was true, and it ships.
//
// It runs as the last step of `npm run build`, so a bundle that would leak cannot be produced.
//
// # The payload ceiling (R18 / FR38)
//
// The third check is a WEIGHT budget, and it is here rather than in its own script because it needs
// exactly the walk this one already does. Its argument is not performance folklore:
//
//   1. The sustainability trend's only enforceable content is "ship less" (trend-ledger.md, #13).
//      Everything else under that heading is a review habit; a byte ceiling is a build failure.
//   2. It protects the credential check's premise. The confidence this scan gives is proportional to
//      how auditable the bundle is; a payload that grows without limit is one nobody reads.
//   3. It is how the ledger's REJECTED trends stay rejected. A 3D viewer, an animation library or a
//      chatbot widget cannot be added quietly to a surface with a stated ceiling — the build says no
//      and names the overage.
//
// The budget is deliberately generous against today's bundle rather than tight. A ceiling set just
// above the current number turns every legitimate feature into a budget negotiation, and the first
// person to lose that argument raises the ceiling instead — at which point it has stopped meaning
// anything.

import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import process from "node:process";

const CLIENT_DIR = join(process.cwd(), ".next", "static");

// The env var NAMES, not their values — a value here would itself be the leak. If the build
// environment holds the real credential we compare against it directly.
//
// The identity entries are P22 task 3.3. They matter for a reason the platform credential does not
// illustrate: an OIDC client secret and a SAML SP private key MINT IDENTITIES, so a leak is not a
// disclosure to be rotated at leisure — it is an attacker able to complete somebody else's sign-in.
// `CONSOLE_IDP_TENANT_MAP` is here too, and it is not a secret: it is the deployment's federation
// topology (which IdP belongs to which tenant, and which domains that tenant owns), which is a
// reconnaissance gift and belongs on the server side with everything else.
//
// `CONSOLE_IDP_CLIENT_ID` is deliberately ABSENT. An OIDC client id is a public identifier that
// travels in the authorization URL by design; flagging it would be flagging the protocol, and a fence
// that cries wolf is a fence somebody switches off.
const CREDENTIAL_ENV = [
  "CONSOLE_PLATFORM_CREDENTIAL",
  "CONSOLE_TENANT_ASSERTIONS",
  "CONSOLE_IDP_CLIENT_SECRET",
  "CONSOLE_IDP_TENANT_MAP",
  "CONSOLE_SAML_SP_PRIVATE_KEY",
];

// Identity material by SHAPE, for the case the build environment does not hold the value (P22 task
// 3.3). The env comparison above catches "the deployment's own secret shipped"; these catch "a secret
// shipped" — a key pasted into a component during debugging, a fixture key committed with a test.
//
// A PEM private key header is unambiguous: there is no legitimate reason for one to be in a browser
// bundle, ever. The logical names are the `Secrets` seam's reserved names, and a bundle containing one
// means client code is reaching for a server-side credential path.
const IDENTITY_SECRET_PATTERNS = [
  { name: "a PEM private key", re: /-----BEGIN (RSA |EC |ENCRYPTED |)PRIVATE KEY-----/ },
  { name: "the OIDC client-secret logical name", re: /\bconsole_idp_client_secret\b/ },
  { name: "the SAML SP key logical name", re: /\bconsole_saml_sp_private_key\b/ },
];

// A currency amount, a percentage, or a named price-band value.
//
// The currency pattern deliberately requires a MONETARY form — a decimal amount, or a multi-digit
// figure with a separator — rather than a bare `$1`. Minified framework code is full of
// `.replace(re, "$1")` regex backreferences, and a fence that flagged those would cry wolf on every
// build until somebody disabled it. A real price in this domain always carries decimals.
// Stripe credential shapes (P21 task 3.3). The console holds NO Stripe secret: it receives a
// short-lived, server-minted Checkout session URL / client secret and nothing else. A secret key or a
// webhook signing secret in the bundle is the one place a secret may never be, and unlike a leaked
// price it cannot be fixed by an edit — the key is compromised the moment the bundle is served.
//
// Each pattern requires a long ALPHANUMERIC run after the prefix, which is what a Stripe key is, so
// prose naming the prefix does not trip it. A PUBLISHABLE key (`pk_test_` / `pk_live_`) is deliberately
// absent: it is designed to be in a browser, and flagging it would teach people to disable the scan.
const STRIPE_SECRET_PATTERNS = [
  { name: "Stripe secret key", re: /\bsk_(live|test)_[A-Za-z0-9]{16,}/ },
  { name: "Stripe restricted key", re: /\brk_(live|test)_[A-Za-z0-9]{16,}/ },
  { name: "Stripe webhook signing secret", re: /\bwhsec_[A-Za-z0-9]{16,}/ },
];

const PRICE_PATTERNS = [
  /\$\s?\d[\d,]*\.\d/,
  /\b\d[\d,]*\.\d+\s?(usd|eur|gbp)\b/i,
  /\bprice[_-]?(amount|value|band)\s*[:=]\s*['"]?\d/i,
  /\b(per\s?seat|per\s?month|\/mo|\/month)\b/i,
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
    if (entry.isDirectory()) yield* walk(full);
    else if (entry.name.endsWith(".js")) yield full;
  }
}

function credentialNeedles() {
  const needles = [];
  for (const name of CREDENTIAL_ENV) {
    const value = process.env[name];
    // Only compare against a real value if it is long enough to be a genuine secret; a short or
    // absent value would produce false positives against ordinary code.
    if (value && value.length >= 12) needles.push({ name, value });
  }
  return needles;
}

// The ceiling, in bytes of shipped JavaScript. Raising it is a decision with an
// argument attached, not a build fix.
const PAYLOAD_CEILING_BYTES = 1_400_000;

// Runtimes that only ever arrive as decoration. Named rather than inferred from size, because the
// point is WHAT shipped, not how big it was: a small 3D library is still a 3D library on a surface
// whose trend review rejected 3D (trend-ledger.md #1, #6, #14).
const DECORATIVE_RUNTIMES = [
  { name: "three.js", needle: "THREE.WebGLRenderer" },
  { name: "a WebGL context", needle: "getContext(\"webgl" },
  { name: "GSAP", needle: "gsap.registerPlugin" },
  { name: "Lottie", needle: "lottie-web" },
];

/**
 * shippedFiles returns the chunks the BUILD says the browser downloads, from the build manifests.
 *
 * # Why not just measure the directory
 *
 * Because `next dev` writes into the same directory, and its artifacts are not shipped: locally that
 * put a 7.6MB unhashed `main-app.js` beside the production chunks. A weight measured over the
 * directory is therefore whichever server ran last — which is not a measurement, it is a coin toss,
 * and the first time it read high somebody would raise the ceiling rather than investigate it.
 *
 * Filtering by filename shape (dev chunks are unhashed, production chunks carry a content hash) was
 * the first attempt and it is guesswork about a build tool's naming. The manifests are the build's own
 * statement of what each route loads, so this asks the question directly.
 *
 * A missing manifest returns null and the caller falls back to the directory walk with a warning: a
 * scan that silently measures nothing is worse than one that measures too much.
 */
async function shippedFiles() {
  const files = new Set();
  let found = false;

  for (const name of ["build-manifest.json", "app-build-manifest.json"]) {
    let manifest;
    try {
      manifest = JSON.parse(await readFile(join(process.cwd(), ".next", name), "utf8"));
    } catch {
      continue;
    }
    found = true;
    collect(manifest, files);
  }

  return found ? files : null;
}

/** collect walks a manifest's nested shape and keeps every `static/...` path it names. */
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

/** contaminated reports whether `next dev` has written into this `.next` tree. */
async function contaminated() {
  try {
    await readFile(join(process.cwd(), ".next", "static", "development", "_buildManifest.js"), "utf8");
    return true;
  } catch {
    return false;
  }
}

async function main() {
  const needles = credentialNeedles();
  const findings = [];
  let scanned = 0;
  let bytes = 0;

  // A tree a dev server has written into cannot be weighed: `next dev` overwrites both the chunks and
  // the manifests with its own unhashed, unminified equivalents, so the number would describe the
  // development bundle while claiming to describe the shipped one. Refusing is the only honest answer
  // — a scan that reports a wrong number is worse than one that reports none, because the wrong number
  // gets acted on. `npm run build` runs this immediately after `next build`, so the tree is clean there
  // by construction.
  if (await contaminated()) {
    console.error(
      "bundle scan: `.next` has been written by a dev server, so the shipped payload cannot be measured.\n" +
        "Stop `next dev` and run `npm run build`, which runs this scan against the build it just produced.",
    );
    process.exit(2);
  }

  const manifest = await shippedFiles();
  if (!manifest) {
    console.warn("bundle scan: no build manifest found — measuring the whole static directory");
  }

  for await (const file of walk(CLIENT_DIR)) {
    scanned += 1;
    const content = await readFile(file, "utf8");
    // `file` is absolute; the manifest names paths relative to `.next/`.
    const relative = file.slice(file.indexOf(join(".next", "static")) + ".next/".length);
    const shipped = manifest ? manifest.has(relative) : true;
    if (shipped) bytes += Buffer.byteLength(content);

    for (const { name, needle } of DECORATIVE_RUNTIMES) {
      if (shipped && content.includes(needle)) {
        findings.push(`PAYLOAD: ${name} is present in shipped bundle ${file}`);
      }
    }

    for (const { name, value } of needles) {
      if (content.includes(value)) findings.push(`CREDENTIAL: ${name} value found in shipped bundle ${file}`);
    }
    // The env var NAME appearing in the client bundle means a client component read
    // `process.env.CONSOLE_*` — Next would have inlined the value at build time.
    for (const name of CREDENTIAL_ENV) {
      if (content.includes(name)) findings.push(`CREDENTIAL: reference to ${name} in shipped bundle ${file}`);
    }
    for (const { name, re } of STRIPE_SECRET_PATTERNS) {
      // The MATCH is never printed. Printing it would move the credential from the bundle into the CI
      // log, which is the same exposure one system downstream.
      if (re.test(content)) findings.push(`STRIPE SECRET: something shaped like a ${name} in shipped bundle ${file}`);
    }
    for (const { name, re } of IDENTITY_SECRET_PATTERNS) {
      // Same rule: the match is never printed. An identity secret in a CI log is an identity secret.
      if (re.test(content)) findings.push(`IDENTITY SECRET: something shaped like ${name} in shipped bundle ${file}`);
    }
    for (const pattern of PRICE_PATTERNS) {
      const match = content.match(pattern);
      if (match) findings.push(`PRICE: literal ${JSON.stringify(match[0])} in shipped bundle ${file}`);
    }
  }

  if (bytes > PAYLOAD_CEILING_BYTES) {
    const over = bytes - PAYLOAD_CEILING_BYTES;
    findings.push(
      `PAYLOAD: shipped client bundle is ${bytes} bytes, over the ${PAYLOAD_CEILING_BYTES}-byte ceiling by ${over}`,
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
      "\nThe console holds the platform credential server-side only, names plans without pricing them,\n" +
        "and ships under a stated payload ceiling. Remove the offending material and rebuild.",
    );
    process.exit(1);
  }

  const headroom = PAYLOAD_CEILING_BYTES - bytes;
  console.log(
    `bundle scan passed: ${scanned} client chunk(s) scanned, ${bytes} shipped bytes ` +
      `(${headroom} under the ${PAYLOAD_CEILING_BYTES}-byte ceiling), ` +
      `no credential material, Stripe secret, priced literal, or decorative runtime.`,
  );
}

main().catch((error) => {
  console.error("bundle scan errored:", error);
  process.exit(2);
});
