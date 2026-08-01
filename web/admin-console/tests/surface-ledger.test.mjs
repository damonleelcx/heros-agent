// surface-ledger.test.mjs is the red-demonstration suite for the operator-surface fence (P26 §1).
//
// A fence nobody has ever seen fail is a fence nobody knows is connected. This repository has already
// found the consequence twice — a horizontal scan that missed the fifth occurrence, and a task list
// with `[x]` beside tasks that were never built — so each of the fence's four assertions is proved
// here by committing the exact violation it exists for and requiring a non-zero exit that NAMES the
// thing.
//
// Every probe is removed in a `finally`. The ledger and surfaces.ts are restored byte-for-byte, so a
// failing assertion cannot leave the tree mutated.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile, writeFile, mkdir, rm } from "node:fs/promises";
import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";

const exec = promisify(execFile);
const ROOT = new URL("..", import.meta.url).pathname;
const REPO = join(ROOT, "..", "..");

const LEDGER = join(REPO, "openspec", "operator-surface-ledger.md");
const SURFACES = join(ROOT, "src", "lib", "surfaces.ts");
const PROBE_CAPABILITY = "zz-fence-probe";
const PROBE_DIR = join(REPO, "openspec", "specs", PROBE_CAPABILITY);

/** runFence runs the fence and returns { code, stderr } without throwing on a non-zero exit. */
async function runFence() {
  try {
    const { stdout } = await exec("node", ["scripts/scan-ledger.mjs"], { cwd: ROOT });
    return { code: 0, stdout, stderr: "" };
  } catch (error) {
    return { code: error.code, stdout: error.stdout ?? "", stderr: error.stderr ?? "" };
  }
}

/** withProbeCapability creates a capability directory in openspec/specs and always removes it. */
async function withProbeCapability(body) {
  await mkdir(PROBE_DIR, { recursive: true });
  await writeFile(join(PROBE_DIR, "spec.md"), "# Fence probe\n\nA deliberate violation, removed by the test.\n");
  try {
    return await body();
  } finally {
    await rm(PROBE_DIR, { recursive: true, force: true });
  }
}

/** withLedgerRow appends a row to section A and always restores the ledger byte-for-byte. */
async function withLedgerRow(row, body) {
  const original = await readFile(LEDGER, "utf8");
  // Section A ends at the "## B." heading. Appending immediately before it keeps the row inside A.
  const marker = "\n## B. Capabilities landing in this change";
  assert.ok(original.includes(marker), "the ledger's section B heading moved — this test needs updating");
  const patched = original.replace(marker, `${row}\n${marker}`);
  await writeFile(LEDGER, patched);
  try {
    return await body();
  } finally {
    await writeFile(LEDGER, original);
  }
}

// ── 1.2 · A capability in no ledger row fails the build, naming it ───────────

test("the fence is GREEN on the current tree", async () => {
  const { code, stdout } = await runFence();
  assert.equal(code, 0, "the ledger fence is red on the checked-in tree");
  assert.match(stdout, /ledger scan passed/);
});

test("🔴 1.2 — a capability with no ledger row fails the build, naming the capability", async () => {
  await withProbeCapability(async () => {
    const { code, stderr } = await runFence();
    assert.equal(code, 1, "a capability with no ledger row did not fail the build");
    assert.match(stderr, new RegExp(PROBE_CAPABILITY), "the failure does not name the capability");
    // The failure must state what is required, or an engineer meeting it for the first time learns
    // only that something is wrong.
    assert.match(stderr, /surface|reasoned absence|named missing collection/i);
  });
});

// ── 1.3 · Forward: a row cannot name a surface that does not exist ───────────

test("🔴 1.3 — a row naming a destination absent from surfaces.ts fails, naming the row and the destination", async () => {
  await withProbeCapability(async () => {
    await withLedgerRow(`| ${PROBE_CAPABILITY} | surface | /this-destination-does-not-exist |`, async () => {
      const { code, stderr } = await runFence();
      assert.equal(code, 1, "a row naming a non-existent destination did not fail the build");
      assert.match(stderr, new RegExp(PROBE_CAPABILITY), "the failure does not name the row");
      assert.match(stderr, /\/this-destination-does-not-exist/, "the failure does not name the destination");
    });
  });
});

// ── 1.4 · Reverse: a surface cannot exist unjustified ────────────────────────

test("🔴 1.4 — a destination in surfaces.ts named by no ledger row fails, naming the destination", async () => {
  const original = await readFile(SURFACES, "utf8");
  const probe = `
  {
    capability: "tenant.read",
    href: "/zz-unjustified-destination",
    label: "Fence probe",
    hint: "A deliberate violation, removed by the test",
  },
];`;
  try {
    await writeFile(SURFACES, original.replace(/\n\];/, `\n${probe}`));
    const { code, stderr } = await runFence();
    assert.equal(code, 1, "an unjustified destination did not fail the build");
    assert.match(stderr, /\/zz-unjustified-destination/, "the failure does not name the destination");
    assert.match(stderr, /nobody can justify/i, "the failure does not say why an unnamed destination is a defect");
  } finally {
    await writeFile(SURFACES, original);
  }
});

// ── 1.5 · A not-yet-readable row must NAME a collection ─────────────────────

test("🔴 1.5 — a not-yet-readable row with an empty detail fails, so the state cannot park a wish", async () => {
  await withProbeCapability(async () => {
    await withLedgerRow(`| ${PROBE_CAPABILITY} | not-yet-readable |  |`, async () => {
      const { code, stderr } = await runFence();
      assert.equal(code, 1, "a not-yet-readable row with no named collection did not fail the build");
      assert.match(stderr, new RegExp(PROBE_CAPABILITY), "the failure does not name the row");
      assert.match(stderr, /names no collection/i, "the failure does not say what is missing");
    });
  });
});

test("🔴 1.5 — a not-yet-readable row that names a collection passes, so the state stays usable", async () => {
  await withProbeCapability(async () => {
    const detail = "requires a probe collection carrying the identifier this read would need";
    await withLedgerRow(`| ${PROBE_CAPABILITY} | not-yet-readable | ${detail} |`, async () => {
      const { code } = await runFence();
      assert.equal(code, 0, "a properly-specified not-yet-readable row was rejected — the state is unusable");
    });
  });
});

// ── The fourth state does not exist ─────────────────────────────────────────

test("🔴 a fourth state is refused rather than tolerated", async () => {
  await withProbeCapability(async () => {
    await withLedgerRow(`| ${PROBE_CAPABILITY} | probably-fine | we will look at it later |`, async () => {
      const { code, stderr } = await runFence();
      assert.equal(code, 1, "a fourth state was accepted");
      assert.match(stderr, /There is no fourth state/);
    });
  });
});

// ── A reasoned absence must carry BOTH a reason and the deciding phase ──────

test("🔴 a no-operator-surface row without a deciding phase fails", async () => {
  await withProbeCapability(async () => {
    await withLedgerRow(`| ${PROBE_CAPABILITY} | no-operator-surface | there is nothing here worth an operator's attention |`, async () => {
      const { code, stderr } = await runFence();
      assert.equal(code, 1, "an unattributable absence was accepted");
      assert.match(stderr, /names no deciding phase/);
    });
  });
});

test("🔴 a no-operator-surface row without a reason fails", async () => {
  await withProbeCapability(async () => {
    await withLedgerRow(`| ${PROBE_CAPABILITY} | no-operator-surface | none (P26) |`, async () => {
      const { code, stderr } = await runFence();
      assert.equal(code, 1, "a reasonless absence was accepted");
      assert.match(stderr, /no reason/);
    });
  });
});

// ── The ledger must state its own boundary ──────────────────────────────────

test("🔴 the ledger must state that it governs operator surfaces only", async () => {
  const original = await readFile(LEDGER, "utf8");
  try {
    await writeFile(LEDGER, original.replace("**Scope: this ledger governs the OPERATOR console only**", "This ledger"));
    const { code, stderr } = await runFence();
    assert.equal(code, 1, "a ledger with no stated scope was accepted");
    assert.match(stderr, /scope statement is missing/);
  } finally {
    await writeFile(LEDGER, original);
  }
});

// ── The fence is wired where a build would actually run it ──────────────────

test("the fence runs in the console's build and is exposed as its own script", async () => {
  const pkg = JSON.parse(await readFile(join(ROOT, "package.json"), "utf8"));
  assert.equal(pkg.scripts["scan:ledger"], "node scripts/scan-ledger.mjs", "no scan:ledger script");
  assert.match(pkg.scripts.build, /scan:ledger/, "the build does not run the ledger fence");
  // Beside the existing token and bundle checks, not instead of them.
  assert.match(pkg.scripts.build, /scan:tokens/);
  assert.match(pkg.scripts.build, /scan:bundle/);
});

test("the fence runs in the repository's CI", async () => {
  const ci = await readFile(join(REPO, ".github", "workflows", "ci.yml"), "utf8");
  assert.match(ci, /scan:ledger|operator-console/, "CI does not run the operator-console fence");
});
