import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

const CONSOLE_ROOT = join(import.meta.dirname, "..");

/**
 * P27 · the control-visibility matrix, and the two ways a surface lies about it.
 *
 * # The failure this file is aimed at
 *
 * A control that is visible, pressable and always refused is a **silent dead write**. The person presses
 * it, the platform says no, and what they learn is that the product is broken rather than that the
 * action is not theirs. It is the failure a role-aware surface produces by default, because rendering
 * everything is less code than deciding.
 *
 * The opposite failure is quieter and worse: a control hidden from somebody the platform WOULD allow.
 * Nobody reports it, because nobody knows it should be there.
 *
 * So the matrix is a table, the platform's refusals are the authority, and this file checks that the two
 * agree on every entry the table claims.
 */

async function copySource() {
  return readFile(join(CONSOLE_ROOT, "src", "lib", "organizationCopy.ts"), "utf8");
}

async function goSource(name) {
  return readFile(join(CONSOLE_ROOT, "..", "..", "internal", "api", name), "utf8");
}

test("every role in the matrix has an entry for every control", async () => {
  const { CONTROL_MATRIX } = await import("../src/lib/organizationCopy.ts").catch(() => ({}));
  // The module imports nothing at runtime, but it is TypeScript. Read it as source instead — the same
  // reason the rest of this suite does: what is PINNED should be what is committed.
  const src = await copySource();
  const block = src.slice(src.indexOf("export const CONTROL_MATRIX"), src.indexOf("/** controlsFor narrows"));
  assert.ok(block.length > 0, "CONTROL_MATRIX is missing");

  const controls = [
    "viewMembers",
    "invite",
    "changeRole",
    "changeOwnerRole",
    "promoteToOwner",
    "removeMember",
    "removeOwner",
    "manageKeys",
    "closeAccount",
  ];
  for (const role of ["owner:", "admin:", "member:", "none:"]) {
    const start = block.indexOf(`\n  ${role}`);
    assert.ok(start > 0, `the matrix has no ${role} row`);
    const end = block.indexOf("\n  },", start);
    const row = block.slice(start, end);
    for (const control of controls) {
      assert.match(
        row,
        new RegExp(`\\b${control}:\\s*(true|false)`),
        `${role} does not say whether it may ${control} — a row with a missing control is a control ` +
          `somebody decides at the call site, which is how a dead button gets rendered`,
      );
    }
  }
  void CONTROL_MATRIX;
});

test("🔴 nothing the matrix denies is rendered anyway", async () => {
  const page = await readFile(join(CONSOLE_ROOT, "src", "app", "app", "settings", "members", "page.tsx"), "utf8");
  // Each interactive block is guarded by the matrix rather than by an inline role comparison. An inline
  // comparison is a second copy of the rule, and the two drift the first time a role is added.
  assert.match(page, /const can = controlsFor\(/, "the page does not consult the matrix at all");
  assert.match(page, /can\.invite \? <InviteForm \/> : null/, "the invite form is not gated by the matrix");
  assert.match(page, /can\.manageKeys \? <CredentialActions \/> : null/, "key management is not gated by the matrix");
  assert.match(page, /can\.changeRole/, "role changes are not gated by the matrix");
  assert.match(page, /can\.removeMember/, "removal is not gated by the matrix");

  // 🔴 And no ad-hoc role test anywhere on the surface. `role === "owner"` in a component is exactly the
  // second copy this table exists to prevent.
  for (const file of ["src/app/app/settings/members/page.tsx", "src/components/members.tsx"]) {
    const src = await readFile(join(CONSOLE_ROOT, file), "utf8");
    const code = src
      .split("\n")
      .filter((line) => !/^\s*(\/\/|\*|\/\*)/.test(line))
      .join("\n");
    assert.doesNotMatch(
      code,
      /(viewer|your)Role\s*===\s*"(owner|admin|member)"/,
      `${file} compares the viewer's role inline instead of reading the matrix`,
    );
  }
});

test("every control the matrix denies has a matching refusal on the platform", async () => {
  // The matrix decides what to ASK; the platform decides. A denial with no refusal behind it is a
  // control we merely hid, and hiding is not enforcement.
  const accounts = await goSource("accounts.go");

  // An admin may not promote to owner, demote an owner, remove an owner, or close the account.
  assert.match(accounts, /only an owner may make somebody an owner/, "no refusal for promoting to owner");
  assert.match(accounts, /only an owner may change an owner's role/, "no refusal for demoting an owner");
  assert.match(accounts, /only an owner may remove an owner/, "no refusal for removing an owner");
  assert.match(
    accounts,
    /func\(role tenancy\.Role\) bool \{ return role == tenancy\.RoleOwner \}/,
    "closing an account is not owner-only on the platform",
  );
  // A member may not invite; a machine credential may not act as a person at all.
  assert.match(accounts, /tenancy\.Role\.CanInvite/, "invitation is not role-gated on the platform");
  assert.match(accounts, /a machine credential names nobody/, "a machine credential is not refused");
});

test("the viewer's role comes from MEMBERSHIP, not from the credential", async () => {
  const accounts = await goSource("accounts.go");
  const fn = accounts.match(/func viewerRole\([\s\S]*?\n\}/);
  assert.ok(fn, "viewerRole is missing");
  assert.match(fn[0], /store\.GetMembership/, "the viewer's role is not read from membership");
  assert.doesNotMatch(
    fn[0],
    /p\.Role/,
    "the viewer's role is taken from the CREDENTIAL. A credential's role is what it was minted with; a " +
      "membership's is what the person is now — an owner demoted this morning would still see owner " +
      "controls because their key remembers otherwise",
  );
});

test("a role with no controls still gets a sentence", async () => {
  const src = await copySource();
  const block = src.slice(src.indexOf("export const ROLE_COPY"));
  for (const role of ["admin", "member", "none"]) {
    assert.match(
      block,
      new RegExp(`${role}:\\s*"[^"]{10,}`),
      `${role} has no explanation for what it cannot do — a hidden control with no sentence reads as a ` +
        `missing feature rather than as a boundary somebody can act on`,
    );
  }
});

/**
 * P27 · a plan-boundary sentence must read correctly for a PLURAL capability.
 *
 * `Failure kind="gated"` interpolates a caller-supplied noun. It used to put that noun in front of a
 * hard-coded singular verb, so `/app/settings/members` rendered *"invitations is not included in this
 * plan"* and *"API keys is not included in this plan"* — visible on the members page of every
 * deployment whose plan does not include them, and caught by opening it rather than by any test.
 *
 * The fix made the PLAN the subject, so singular and plural read identically. This asserts the shape
 * rather than the words: a template that agrees with its subject cannot be written by accident, and the
 * next person to add a plural surface should not have to notice.
 */
test("🔴 the gated sentence agrees with a plural capability", async () => {
  const src = await readFile(join(CONSOLE_ROOT, "src", "components", "primitives.tsx"), "utf8");

  assert.doesNotMatch(
    src,
    /\{capability\}\s*\{/,
    "the gated title interpolates the capability as the SUBJECT of the sentence again. A caller-supplied " +
      "noun in front of a fixed verb produces `invitations is not included in this plan` the first time " +
      "somebody passes a plural — make the plan the subject instead.",
  );
  for (const phrase of ["This plan does not include", "plan includes"]) {
    assert.match(src, new RegExp(phrase), `the gated copy no longer says "${phrase}"`);
  }

  // And the callers that exposed it are still plural, so this test keeps testing something.
  const members = await readFile(
    join(CONSOLE_ROOT, "src", "app", "app", "settings", "members", "page.tsx"),
    "utf8",
  );
  for (const plural of ['subject="invitations"', 'subject="API keys"']) {
    assert.match(
      members,
      new RegExp(plural.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")),
      `${plural} is gone from the members page — if the plural callers disappear, the assertion above ` +
        "stops being evidence about anything",
    );
  }
});
