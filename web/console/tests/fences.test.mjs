// fences.test.mjs proves every P23 accuracy fence goes RED (tasks 4.14, 12.4).
//
// # The standing rule this test is
//
// *A fence without a failing fixture is not delivered.* A fence nobody has watched fail is a fence nobody
// knows is connected, and the failure mode is silent: a disconnected fence reports success on everything,
// forever, and the first person to find out is a customer reading a page it was supposed to have stopped.
//
// # Why each fixture must fail INDIVIDUALLY
//
// Running one corpus with ten defects through ten scans proves nothing about which scan caught which. One
// broken thing per fixture means a scan that started passing everything is caught by exactly one test,
// and the test name says what stopped working.
//
// # Why the message is asserted and not just the exit code
//
// A scan that CRASHED would also exit non-zero. Asserting a substring of the finding is what separates
// "the fence refused this" from "the fence threw" — a distinction the exit code alone cannot make, and
// the one that matters when the fence is later refactored.

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { dirname } from "node:path";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";

const exec = promisify(execFile);
const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const FIXTURES = join(ROOT, "tests", "support", "fences");

/**
 * runScan runs one scan against one fixture corpus and returns its exit code and output.
 *
 * `HEROS_CONTENT_ROOT` points the scan at the fixture. The real content tree is never touched, so a
 * fixture cannot leave the repository broken — which is the property that lets these run in CI.
 */
async function runScan(script, fixture, extraEnv = {}) {
  const env = {
    ...process.env,
    HEROS_CONTENT_ROOT: fixture ? join(FIXTURES, fixture) : undefined,
    ...extraEnv,
  };
  try {
    const { stdout, stderr } = await exec("node", [join(ROOT, "scripts", script)], { cwd: ROOT, env });
    return { code: 0, output: stdout + stderr };
  } catch (error) {
    return { code: error.code ?? 1, output: `${error.stdout ?? ""}${error.stderr ?? ""}` };
  }
}

/** slugManifestFor generates a manifest for a fixture corpus, into a temp dir, never over the real one. */
async function slugManifestFor(fixture) {
  const dir = await mkdtemp(join(tmpdir(), "heros-fence-"));
  const manifest = join(dir, "slug-manifest.json");
  await exec("node", [join(ROOT, "scripts", "gen-slug-manifest.mjs")], {
    cwd: ROOT,
    env: { ...process.env, HEROS_CONTENT_ROOT: join(FIXTURES, fixture), HEROS_SLUG_MANIFEST: manifest },
  });
  return { manifest, cleanup: () => rm(dir, { recursive: true, force: true }) };
}

const CASES = [
  {
    name: "scan-cli refuses a command the registry does not have",
    script: "scan-cli.mjs",
    fixture: "cli-unknown-command",
    expect: /heros frobnicate` is not a subcommand/,
  },
  {
    name: "scan-cli refuses a registry command with no reference entry",
    script: "scan-cli.mjs",
    fixture: "cli-missing-entry",
    expect: /the CLI reference has no `### \w+` entry/,
  },
  {
    name: "scan-docs-claims refuses a capability that is not in the manifest",
    script: "scan-docs-claims.mjs",
    fixture: "unshipped-claim",
    expect: /has no entry in the capability manifest/,
  },
  {
    name: "scan-links refuses an anchor that does not resolve",
    script: "scan-links.mjs",
    fixture: "dead-anchor",
    expect: /anchor #no-such-heading does not resolve/,
    needsSlugManifest: true,
  },
  {
    name: "scan-secrets refuses credential-shaped content",
    script: "scan-secrets.mjs",
    fixture: "fake-key",
    expect: /matches a .*provider key/i,
  },
  {
    name: "scan-content refuses raw markup",
    script: "scan-content.mjs",
    fixture: "raw-html",
    expect: /raw HTML/,
  },
  {
    name: "scan-metric refuses a unit that disagrees with the harness",
    script: "scan-metric.mjs",
    fixture: "metric-unit",
    expect: /says `latency_total_ms` is in seconds; the harness emits it in ms/,
  },
  {
    name: "scan-api refuses a documented endpoint while the artifact is absent",
    script: "scan-api.mjs",
    fixture: "undocumented-endpoint",
    expect: /documents `POST \/v1\/workflows\/discover`.*no\s+machine-readable API artifact/s,
  },
  {
    name: "scan-install refuses a hand-typed checksum",
    script: "scan-install.mjs",
    fixture: "hand-typed-checksum",
    expect: /64-hex checksum that is not in the published release manifest/,
  },
  {
    name: "scan-install refuses a path that reaches PATH before verifying",
    script: "scan-install.mjs",
    fixture: "path-before-verify",
    expect: /puts the binary where it can run before anything.*verified it/s,
  },
];

for (const testCase of CASES) {
  test(testCase.name, async () => {
    let manifest;
    let cleanup = async () => {};
    if (testCase.needsSlugManifest) {
      const built = await slugManifestFor(testCase.fixture);
      manifest = built.manifest;
      cleanup = built.cleanup;
    }
    try {
      const { code, output } = await runScan(
        testCase.script,
        testCase.fixture,
        manifest ? { HEROS_SLUG_MANIFEST: manifest } : {},
      );
      assert.equal(
        code,
        1,
        `${testCase.script} exited ${code} for fixture ${testCase.fixture}; a fence that does not fail here ` +
          `is not connected.\n${output}`,
      );
      assert.match(
        output,
        testCase.expect,
        `${testCase.script} failed, but not for the reason the fixture exists — this may be a crash rather ` +
          `than a refusal.\n${output}`,
      );
    } finally {
      await cleanup();
    }
  });
}

test("every fence passes against the real corpus", async () => {
  // The other half of the proof. Ten fences that fail on everything would pass every test above.
  for (const script of [
    "scan-content.mjs",
    "scan-secrets.mjs",
    "scan-docs-claims.mjs",
    "scan-cli.mjs",
    "scan-api.mjs",
    "scan-metric.mjs",
    "scan-install.mjs",
    "scan-links.mjs",
  ]) {
    const { code, output } = await runScan(script, null);
    assert.equal(code, 0, `${script} failed against the real content corpus:\n${output}`);
  }
});
