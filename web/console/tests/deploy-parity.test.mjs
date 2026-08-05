// deploy-parity.test.mjs holds §11 — the deployment properties P23's design rests on.
//
// # Why these are tests rather than a runbook paragraph
//
// Each one is a claim ADR-011 makes that a reader has no way to check:
//
//   11.1  content ships in the console container, so a bad copy change is reverted by redeploying
//         the previous image — no migration, no platform restart
//   11.2  "which text is live on this cluster" is a curl
//   11.3  docs and legal are byte-identical air-gapped and hosted, enforced by a scan rather than a policy
//   11.4  the consent endpoints are the ONLY new authenticated surface, and take exactly three fields
//
// A runbook that asserted these would be true on the day it was written. These fail when they stop
// being true, which is the day it matters.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { startStubPlatform, startConsole } from "./support/harness.mjs";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const REPO = join(ROOT, "..", "..");

let platform;
let console_;

before(async () => {
  platform = await startStubPlatform();
  console_ = await startConsole(platform.base);
}, { timeout: 60_000 });

after(async () => {
  await console_?.close();
  await platform?.close();
});

// ── 11.1 · Content ships in the console container ────────────────────────────

test("11.1 — the console image copies the content corpus, so a copy fix is an image revert", async () => {
  /*
   * 🔴 This COPY is load-bearing, and its absence is a failure that passes every test on a developer's
   * machine. The reading routes render per request (the nonce CSP cannot be satisfied by build-time
   * HTML), so the container reads these files at RUNTIME. Without the COPY, every /docs and /legal route
   * 404s in production while working perfectly in development.
   */
  const dockerfile = await readFile(join(REPO, "deploy", "Dockerfile.console"), "utf8");
  assert.match(
    dockerfile,
    /COPY --from=build \/app\/content \.\/content/,
    "the console image does not carry the content corpus — every /docs and /legal route would 404 in the " +
      "container while passing every test locally",
  );

  // And it must be in the RUNTIME stage, not only the build stage. A COPY before the second FROM is a
  // file that exists while the bundle is compiled and is gone when the process starts.
  const runtimeStage = dockerfile.slice(dockerfile.lastIndexOf("FROM "));
  assert.match(runtimeStage, /COPY --from=build \/app\/content/, "the content COPY is not in the runtime stage");
});

test("11.1 — reverting a copy change needs no migration and no platform restart", async () => {
  // The claim is structural: the corpus is FILES IN AN IMAGE, referenced by no table and by no platform
  // service. So "revert the copy change" is "deploy the previous console image", full stop.
  //
  // Asserted as the absence of the two things that would make it false.
  const migrations = await readdir(join(REPO, "db", "migrations", "postgres"));
  const contentMigrations = migrations.filter((name) => /content|docs|legal_document/i.test(name));
  assert.deepEqual(
    contentMigrations,
    [],
    "a migration references document CONTENT — content lives in the image, and a database copy would be a " +
      "second source of truth that a redeploy could not revert",
  );

  // 0019 stores acceptances, which reference a document IDENTITY (kind, version, hash) — never its text.
  const acceptance = await readFile(
    join(REPO, "db", "migrations", "postgres", "0019_p23_legal_acceptance.up.sql"),
    "utf8",
  );
  assert.doesNotMatch(
    acceptance,
    /\b(body|text_content|document_text|content TEXT)\b/,
    "the consent table stores document TEXT — it must store the identity triple only",
  );
});

// ── 11.2 · Which text is live is a curl ──────────────────────────────────────

test("11.2 — the running deployment reports which legal text is live, from two public surfaces", async () => {
  // The manifest: every kind, every version, every hash.
  const manifestRes = await fetch(`${console_.base}/legal/manifest.json`);
  assert.equal(manifestRes.status, 200);
  const manifest = await manifestRes.json();

  // The health endpoint: which version is in force right now, beside the deployment's other facts.
  const healthRes = await fetch(`${console_.base}/api/health`);
  assert.equal(healthRes.status, 200);
  const health = await healthRes.json();

  assert.ok(health.legal_documents, "the health endpoint does not report the live legal documents");
  assert.ok(!health.legal_documents.error, `the corpus could not be read: ${health.legal_documents.error}`);

  for (const kind of ["terms", "privacy"]) {
    const live = health.legal_documents[kind];
    assert.ok(live, `${kind} is not reported as live`);
    assert.match(live.hash, /^[0-9a-f]{64}$/, `${kind}'s live hash is not a sha256`);

    // 🔴 The two surfaces must AGREE. Two answers to "which text is live" is worse than one, because a
    // reader who checks both and gets different hashes has no way to know which to believe.
    const fromManifest = (manifest.kinds[kind] ?? []).find((v) => v.current);
    assert.ok(fromManifest, `${kind} has no current version in the manifest`);
    assert.equal(
      live.hash,
      fromManifest.hash,
      `${kind}: /api/health and /legal/manifest.json disagree about which text is live`,
    );
    assert.equal(live.version, fromManifest.version);
  }
});

test("11.2 — reporting the live documents costs no upstream call", async () => {
  // A health endpoint that reached the platform would report the platform's health as the console's,
  // which is the inversion this endpoint's own header refuses.
  const before = platform.requests.length;
  await fetch(`${console_.base}/api/health`);
  await fetch(`${console_.base}/legal/manifest.json`);
  assert.equal(platform.requests.length, before, "reporting the live documents made an upstream call");
});

// ── 11.3 · Air-gapped parity, by machine rather than by policy ───────────────

test("11.3 — nothing in the corpus or the reading surface can reach a third party", async () => {
  /*
   * Air-gapped parity is not a promise here, it is an absence of anything to fetch. `scan-content.mjs`
   * refuses an external script, font, stylesheet or origin in the corpus AND in the public-surface
   * markup — so the air-gapped rendering cannot differ from the hosted one, because there is nothing
   * that would only arrive in one of them.
   *
   * This test asserts the SCAN IS WIRED IN. A fence that exists and does not run is the failure mode
   * this whole phase is written against.
   */
  const pkg = JSON.parse(await readFile(join(ROOT, "package.json"), "utf8"));
  assert.match(pkg.scripts.build, /scan:docs/, "the content fences do not run in the build");
  assert.match(pkg.scripts["scan:docs"], /scan:content/, "scan-content is not in the docs fence set");
});

test("11.3 — the air-gapped package carries the console image, so it carries the documents", async () => {
  // The packager saves the digest-pinned image set from images.env, and the console image is one of
  // them. Since the documents are IN that image (11.1), packaging them is not a separate step that can
  // be forgotten — which is the property worth having.
  const images = await readFile(join(REPO, "deploy", "images.env"), "utf8");
  assert.match(images, /^CONSOLE_IMAGE=/m, "images.env does not pin the console image");

  const packager = await readFile(join(REPO, "deploy", "scripts", "package-airgapped.sh"), "utf8");
  assert.match(
    packager,
    /images\.env/,
    "the air-gapped packager does not read the pinned image set, so console content could be omitted",
  );
});

// ── 11.4 · The consent endpoints are the only new authenticated surface ──────

test("11.4 — P23 adds exactly two authenticated endpoints, and no cross-tenant read", async () => {
  const source = await readFile(join(REPO, "internal", "api", "consent.go"), "utf8");

  const routes = [...source.matchAll(/s\.Mux\.HandleFunc\("([A-Z]+) ([^"]+)"/g)].map((m) => `${m[1]} ${m[2]}`);
  assert.deepEqual(
    routes.sort(),
    ["GET /api/v1/legal/acceptances", "POST /api/v1/legal/acceptances"],
    "P23 mounts a surface beyond the two consent endpoints",
  );

  // 🔴 The tenant and the principal come from the authenticated session, never from the request. There
  // must be no path that reads either from a body field or a query parameter.
  assert.match(source, /auth\.PrincipalFrom\(r\.Context\(\)\)/, "the handlers do not derive the caller from the session");
  assert.doesNotMatch(
    source,
    /r\.URL\.Query\(\)\.Get\("tenant|body\.TenantID|body\.PrincipalID/,
    "a handler reads a tenant or principal from the request — a tenant id a caller can type is one they can change",
  );

  // And the store offers no cross-tenant read to call.
  const service = await readFile(join(REPO, "internal", "legal", "service.go"), "utf8");
  const storeInterface = service.slice(service.indexOf("type Store interface"), service.indexOf("// ManifestSource"));
  assert.doesNotMatch(
    storeInterface,
    /ListAll|ListForTenantless|ListEverything/,
    "the store offers a cross-tenant read",
  );
  /*
   * Every method that READS or mutates by subject takes the tenant explicitly. `Insert` is the
   * exception and not a loophole: it carries one Acceptance, whose TenantID the caller set from the
   * session, and it writes exactly that row. There is nothing for it to read across.
   *
   * Checked as two separate claims rather than one loose regex, because the loose version passes on an
   * interface that has quietly grown a tenant-free reader.
   */
  for (const method of ["ListForPrincipal", "EraseSubject"]) {
    const line = storeInterface.split("\n").find((l) => l.trim().startsWith(method + "("));
    assert.ok(line, `${method} is not on the Store interface`);
    // `tenantID string` OR `tenantID, principalID string` — Go collapses shared types, and a regex that
    // did not know that would fail on correct code, which is how a fence gets deleted.
    assert.match(
      line,
      /\btenantID\b\s*(,|string)/,
      `${method} does not take a tenant — it could read across tenants`,
    );
  }

  // No method may return acceptances without having been given a tenant. `MarkSuperseded` is the one
  // cross-tenant operation and it is a WRITE keyed on a document version, returning a count — it
  // discloses nothing about any tenant.
  const readers = storeInterface
    .split("\n")
    .filter((l) => /\[\]Acceptance/.test(l) && l.trim().length > 0 && !l.trim().startsWith("//"));
  for (const reader of readers) {
    assert.match(
      reader,
      /\btenantID\b\s*(,|string)/,
      `a Store method returns acceptances without a tenant: ${reader.trim()}`,
    );
  }
});

test("11.4 — the acceptance request accepts exactly three fields plus the method", async () => {
  const source = await readFile(join(REPO, "internal", "api", "consent.go"), "utf8");
  const shape = source.slice(source.indexOf("type acceptanceRequest struct"), source.indexOf("// RegisterP23"));
  const fields = [...shape.matchAll(/json:"([a-z_]+)"/g)].map((m) => m[1]).sort();
  assert.deepEqual(
    fields,
    ["content_hash", "document_kind", "document_version", "method"],
    "the acceptance request carries a field this system has no column for",
  );

  // 🔴 Unknown fields are REFUSED, not ignored. A client sending `email` is a client with a
  // misunderstanding, and silently dropping it would let the misunderstanding ship.
  assert.match(
    source,
    /decoder\.DisallowUnknownFields\(\)/,
    "unknown request fields are ignored rather than refused",
  );
});
