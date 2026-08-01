// releases.test.mjs is the console half of P26 wave 26c.
//
// The platform half (internal/adminops/release_test.go) scans the serialised read model for anything
// key-shaped. This file asserts the things that only exist on the screen: that the page offers no
// control that generates or exports a key, that a QUEUED smoke run is not painted with the hazard
// palette, and that a not-yet-verified artefact is not painted as a failure either.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

const ROOT = new URL("..", import.meta.url).pathname;
const read = (rel) => readFile(join(ROOT, rel), "utf8");

test("🔴 4.4 — the release surface offers no operation whose output is key material", async () => {
  const page = await read("src/app/releases/page.tsx");
  // No server action, no POST form, and no vocabulary that would belong to a key-producing control.
  assert.equal(/from "@\/lib\/actions"/.test(page), false, "the releases page imports a server action");
  assert.equal(/ActionForm/.test(page), false, "the releases page imports the action form");
  assert.equal(/<form/.test(page), false, "the releases page carries a form at all");
  for (const forbidden of ["keygen", "generateKey", "exportKey", "downloadKey", "reveal", "private"]) {
    assert.equal(
      new RegExp(forbidden, "i").test(page.replace(/private half/gi, "")),
      false,
      `the releases page mentions ${forbidden} — this surface must not become a disclosure path`,
    );
  }
  // And the wire type has no field that could carry material: only an identifier and a fingerprint.
  const types = await read("src/lib/types.ts");
  const keyRow = types.match(/export type SigningKeyRow = {([^}]*)}/);
  assert.ok(keyRow, "SigningKeyRow is not declared");
  for (const field of ["hex", "public_key", "pem", "material", "secret"]) {
    assert.equal(
      new RegExp(`\\b${field}\\b`, "i").test(keyRow[1]),
      false,
      `SigningKeyRow carries a \`${field}\` field — a key is an identifier and a fingerprint`,
    );
  }
});

test("🔴 4.6 — a queued smoke run is not rendered as a failure, and not with the hazard palette", async () => {
  const page = await read("src/app/releases/page.tsx");
  const types = await read("src/lib/types.ts");

  assert.match(
    types,
    /"passed" \| "failed" \| "queued_until_timeout"/,
    "SmokeState is not three values on the wire",
  );
  const tones = page.match(/const SMOKE_TONE[^=]*=\s*{([^}]*)}/);
  assert.ok(tones, "the page declares no smoke tone map");
  assert.match(tones[1], /queued_until_timeout:\s*"neutral"/,
    "a queued smoke run is painted with a hazard tone — the job never started, and there is nothing " +
      "in the build to debug (P26 §4.6, FR31)");
  assert.match(tones[1], /failed:\s*"danger"/, "a genuinely failed smoke run is not marked as a hazard");

  const labels = page.match(/const SMOKE_LABEL[^=]*=\s*{([^}]*)}/);
  assert.ok(labels, "the page declares no smoke label map");
  assert.match(labels[1], /queued_until_timeout:\s*"queued until timeout"/,
    "the queued outcome does not render as itself");
});

test("🔴 4.5 — a not-yet-verified artefact is neither passed nor failed on screen", async () => {
  const page = await read("src/app/releases/page.tsx");
  const tones = page.match(/const VERIFY_TONE[^=]*=\s*{([^}]*)}/);
  assert.ok(tones, "the page declares no verification tone map");
  assert.match(tones[1], /not_yet_verified:\s*"neutral"/, "unchecked is painted as a hazard");
  assert.match(tones[1], /verified:\s*"ok"/, "verified is not marked as passing");
  const labels = page.match(/const VERIFY_LABEL[^=]*=\s*{([^}]*)}/);
  assert.match(labels[1], /not_yet_verified:\s*"not yet verified"/, "unchecked does not render as itself");
});

test("🔴 4.7 — the surface shows where the publish sequence stopped, not only its final state", async () => {
  const page = await read("src/app/releases/page.tsx");
  assert.match(page, /s\.completed/, "the completed steps are not rendered");
  assert.match(page, /s\.stopped_at/, "the stopping point is not rendered");
  assert.match(page, /s\.reason/, "the reason is not rendered");
  assert.match(page, /stopped at publish/, "the step labels are missing");
});

test("🔴 4.9 — the release surface halts, unpublishes and changes nothing", async () => {
  const page = await read("src/app/releases/page.tsx");
  for (const control of ["halt", "unpublish", "re-sign", "republish", "rerun", "retry"]) {
    assert.equal(
      new RegExp(`${control}`, "i").test(page.replace(/halts nothing|re-signs nothing|unpublishes nothing/gi, "")),
      false,
      `the releases page offers a ${control} control — the channel halt is a separate decision with its own design (PRD open question 1)`,
    );
  }
});

test("🔴 4.8 — the /releases destination is registered and gated by release.read", async () => {
  const surfaces = await read("src/lib/surfaces.ts");
  assert.match(surfaces, /capability: "release\.read",\s*\n\s*href: "\/releases"/,
    "the /releases destination is not registered against release.read");
  const page = await read("src/app/releases/page.tsx");
  assert.match(page, /hasCapability\(identity, "release\.read"\)/, "the page does not check the capability");
});
