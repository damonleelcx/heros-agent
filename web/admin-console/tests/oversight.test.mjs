// oversight.test.mjs is the console half of P26 wave 26e.
//
// The platform half (internal/adminops/oversight_test.go) asserts the read model's absences. This file
// asserts what the screen does with them: that a missing legal record does not render as an empty
// acceptance table, that an unread readiness surface does not render as "not configured", that an
// unknown version is not shown as a version, and that the payments gap renders as a statement rather
// than as a page with a zero on it.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

const ROOT = new URL("..", import.meta.url).pathname;
const read = (rel) => readFile(join(ROOT, rel), "utf8");

test("🔴 6.1 — a session shows its verified factor, and the fixture IdP is not dressed as production", async () => {
  const page = await read("src/app/oversight/page.tsx");
  assert.match(page, /s\.factor/, "the session's factor is not rendered");
  assert.match(page, /s\.multi_factor/, "single-factor and multi-factor sessions are not distinguishable");
  assert.match(page, /s\.verified_at/, "the verification time is not rendered");
  assert.match(page, /identity_provider\.test_mode/, "the page does not render whether the IdP is the fixture");
  assert.match(page, /identity_provider\.note/, "the page does not render the identity provider's own caveat");
  // And no token or factor VALUE has a field to travel in.
  const types = await read("src/lib/types.ts");
  const sessionRow = types.match(/export type SessionRow = {([^}]*)}/);
  assert.ok(sessionRow, "SessionRow is not declared");
  for (const field of ["token", "secret", "code", "assertion"]) {
    assert.equal(
      new RegExp(`\\b${field}\\b`, "i").test(sessionRow[1]),
      false,
      `SessionRow carries a \`${field}\` field — the factor NAME is what a reviewer needs, never its value`,
    );
  }
});

test("🔴 6.2 — an accepted version links the ARCHIVED text at its content hash", async () => {
  const page = await read("src/app/oversight/page.tsx");
  assert.match(page, /l\.archive_href/, "the archived text is not linked");
  assert.match(page, /l\.accepted_hash/, "the accepted content hash is not rendered");
  assert.match(page, /l\.owed_version/, "versions owed after a material publication are not rendered");
  assert.match(page, /nothing owed/, "a tenant that owes nothing is not stated as such");
  // A missing legal record must not render as an empty acceptance table.
  assert.match(page, /!view\.legal_known/, "an absent legal record is not branched on");
  assert.match(
    page.replace(/\s+/g, " "),
    /would read as 'nobody owes anything'/,
    "the page does not say why an empty acceptance table would be a different claim",
  );
});

test("🔴 6.3 — an integration has three states, and an unread readiness surface is not 'not configured'", async () => {
  const types = await read("src/lib/types.ts");
  assert.match(
    types,
    /export type IntegrationState = "absent" \| "configured" \| "degraded";/,
    "IntegrationState is not three values — a boolean would call a decision a fault or a fault a decision",
  );
  const page = await read("src/app/oversight/page.tsx");
  const tones = page.match(/const INTEGRATION_TONE[^=]*=\s*{([^}]*)}/);
  assert.ok(tones, "the page declares no integration tone map");
  // `absent` is NEUTRAL: nothing is wrong, nothing is configured. Only `degraded` earns the hazard.
  assert.match(tones[1], /absent:\s*"neutral"/, "an unconfigured integration is painted as a hazard");
  assert.match(tones[1], /degraded:\s*"warn"/, "a degraded integration is not marked as a fault");
  assert.match(page, /!view\.integrations_known/, "an unread readiness surface is not branched on");
  assert.match(
    page.replace(/\s+/g, " "),
    /'nothing is configured' and 'we did not ask' are different answers/,
    "the page does not say why an unread readiness surface is not the same as 'not configured'",
  );
  assert.match(page, /n\.failure_class/, "a degraded integration does not name its failure class");
});

test("🔴 6.4 — an unknown deployed version is stated, never shown as a version", async () => {
  const page = await read("src/app/oversight/page.tsx");
  assert.match(page, /d\.unknown \?/, "the unknown case is not branched on");
  assert.match(page, /requires \$\{d\.missing_collection\}/, "an unknown version does not name what would make it readable");
  // No inference vocabulary anywhere.
  for (const word of ["estimated", "approximately", "inferred from", "probably"]) {
    assert.equal(
      new RegExp(word, "i").test(page.replace(/is inferred from an API contract version[^*]*/g, "")),
      false,
      `the oversight page offers an ${word} version — an inferred version rendered as a version is a ` +
        `wrong number that gets acted on during an incident`,
    );
  }
});

test("🔴 6.5 — the payments gap renders as a statement, with no count and no zero", async () => {
  const page = await read("src/app/oversight/page.tsx");
  assert.match(page, /not_yet_readable\.map/, "the not-yet-readable block is not rendered");
  assert.match(page, /n\.requires/, "the missing collection is not rendered");
  assert.match(page, /n\.statement/, "the statement is not rendered");
  // No table, no count, no Num in that panel: the whole point is that there is nothing to count.
  const panel = page.slice(page.indexOf('id: "not-yet"'));
  assert.equal(/<DataTable/.test(panel), false, "the not-yet-readable panel renders a table");
  assert.equal(/<Num /.test(panel), false, "the not-yet-readable panel renders a figure");
});

test("🔴 6.6 — the /oversight destination is registered, gated and in the nav", async () => {
  const surfaces = await read("src/lib/surfaces.ts");
  assert.match(surfaces, /capability: "audit\.read",\s*\n\s*href: "\/oversight"/,
    "the /oversight destination is not registered against audit.read");
  const page = await read("src/app/oversight/page.tsx");
  assert.match(page, /hasCapability\(identity, "audit\.read"\)/, "the page does not check the capability");
  assert.match(page, /<DeniedState/, "the page does not render a denial with an escalation path");
});
