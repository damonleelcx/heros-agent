// p37-subject.test.mjs gates P37 §2 — the subject's shape, its states, and the one spec conflict.
//
// # What is asserted here and why each is a fence rather than a review note
//
//   · a subject cannot be half-filled. D-37.1's whole safety argument is that `node_id` is unique
//     WITHIN a workflow; a type that tolerates a missing `workflow_id` is a type that lets the wrong
//     node be edited, silently and order-dependently.
//   · `not_connected` is a member of the state set. Task 2.5 — the moment it collapses into
//     `not_reported` the disconnected reader is sent to a terminal to run a command that cannot work.
//   · the `axis-node-projection` delta restates its requirement header BYTE FOR BYTE. OpenSpec matches
//     a MODIFIED requirement by its header; a near-miss folds as a SECOND requirement and the original
//     survives unmodified — which would leave P37 in violation of a requirement it believes it changed.
//     That failure is completely silent, which is why it is a test.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

import {
  AXIS_DOC,
  NOT_CONNECTED,
  SUBJECT_COOKIE,
  SUBJECT_COOKIE_OPTIONS,
  SUBJECT_STATES,
  decodeSubject,
  encodeSubject,
  isAxisSubject,
  subjectLabel,
} from "../src/lib/axisSubject.ts";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const REPO = join(ROOT, "..", "..");
const CHANGE = join(REPO, "openspec", "changes", "p37-source-bound-editors");

test("2.1 a subject carries BOTH identifiers, and a half-filled one is refused", () => {
  assert.ok(isAxisSubject({ workflow_id: "wf", node_id: "answer" }));
  assert.equal(isAxisSubject({ node_id: "answer" }), false, "a node id alone is not a subject (D-37.1)");
  assert.equal(isAxisSubject({ workflow_id: "wf" }), false, "a workflow alone names no node");
  assert.equal(isAxisSubject({ workflow_id: "", node_id: "answer" }), false, "empty is not present");
  assert.equal(isAxisSubject(null), false);
});

test("2.1 identity is not display: the workflow appears only when it disambiguates", () => {
  const a = { workflow_id: "wf-one", node_id: "answer" };
  const b = { workflow_id: "wf-two", node_id: "answer" };
  assert.equal(subjectLabel(a, [a]), "answer", "one candidate — the node alone, no second identifier");
  assert.equal(
    subjectLabel(a, [a, b]),
    "answer · wf-one",
    "two candidates share a display name — the workflow disambiguates",
  );
  assert.equal(
    subjectLabel({ ...a, symbol: "handleAnswer" }, [a]),
    "handleAnswer",
    "the symbol is what a reader recognises; the node id is the fallback",
  );
});

test("2.1 a subject survives the cookie round trip, and a mangled cookie renders nothing", () => {
  const subject = { workflow_id: "acme/agent", node_id: "n_9f1c04ab", symbol: "recall", file: "a.py" };
  const decoded = decodeSubject(encodeSubject(subject));
  assert.deepEqual(decoded, { workflow_id: "acme/agent", node_id: "n_9f1c04ab" });
  // 🔴 Only the two IDENTIFIERS survive. Symbol, file and language are platform facts and are
  // re-resolved every render — `subjects.ts`'s rule that a console-local record may never become a
  // cache of platform data.
  assert.equal("symbol" in decoded, false, "the cookie must not carry a cached platform fact");

  for (const bad of [undefined, "", "no-space", "%E0%A4%A", " "]) {
    assert.equal(decodeSubject(bad), null, `a hand-edited cookie (${JSON.stringify(bad)}) must not throw`);
  }
});

test("2.4 the subject cookie is per-browser UI state, with the theme cookie's flags", () => {
  assert.equal(SUBJECT_COOKIE, "heros_axis_subject");
  assert.equal(
    SUBJECT_COOKIE_OPTIONS.httpOnly,
    false,
    "not a credential — a flag that claims protection it does not provide stops meaning anything",
  );
  assert.equal(SUBJECT_COOKIE_OPTIONS.sameSite, "lax");
  assert.equal(SUBJECT_COOKIE_OPTIONS.path, "/");
  assert.ok(SUBJECT_COOKIE_OPTIONS.maxAge > 0, "a remembered subject must outlive the tab");
});

test("2.5 not_connected is a state of its own, beside the three transport treatments", () => {
  for (const state of ["resolved", "ambiguous", "not_connected", "read_failed", "not_mounted"]) {
    assert.ok(SUBJECT_STATES.includes(state), `${state} is missing from the closed set`);
  }
  assert.equal(
    SUBJECT_STATES.includes("not_reported"),
    false,
    "not_reported belongs to the PROJECTION's vocabulary; the subject resolver's fourth state is not_connected " +
      "and collapsing them sends a reader with no repository to a terminal (D-37.5)",
  );
});

test("1.4 / 2.5 the not_connected copy names the input and carries both links", () => {
  assert.match(NOT_CONNECTED.missingInput, /connected repository/);
  assert.equal(NOT_CONNECTED.connectHref, "/app/connections");
  assert.match(NOT_CONNECTED.body, /sample node/, "it must say WHY the position is empty, not merely that it is");
  // Every axis names a reading-surface document, and that is the second link.
  for (const [axis, href] of Object.entries(AXIS_DOC)) {
    assert.match(href, /^\/docs\/concepts\//, `${axis} must point at a reading-surface document`);
  }
});

test("2.3 the axis-node-projection delta restates its requirement header VERBATIM", async () => {
  const base = await readFile(join(REPO, "openspec", "specs", "axis-node-projection", "spec.md"), "utf8");
  const delta = await readFile(join(CHANGE, "specs", "axis-node-projection", "spec.md"), "utf8");

  const headers = [...base.matchAll(/^### Requirement: (.+)$/gm)].map((m) => m[1]);
  const modified = [...delta.matchAll(/^### Requirement: (.+)$/gm)].map((m) => m[1]);

  assert.equal(modified.length, 1, "P37 modifies exactly one folded requirement");
  assert.ok(
    headers.includes(modified[0]),
    `the delta's header "${modified[0]}" matches no requirement in the folded spec — OpenSpec would fold ` +
      `this as a SECOND requirement and the original would survive unmodified, silently`,
  );
  assert.ok(delta.includes("## MODIFIED Requirements"), "the delta must declare itself a modification");
});

test("2.3 the delta strengthens the second scenario structurally, not by labelling", async () => {
  const delta = await readFile(join(CHANGE, "specs", "axis-node-projection", "spec.md"), "utf8");
  assert.match(
    delta,
    /no worked example appears in the position that data occupies/,
    "after P37 example and live data are distinguishable by POSITION; labelling alone survives a copy edit",
  );
});

test("2.1–2.5 every decision is recorded with its rejected alternative", async () => {
  const doc = await readFile(join(CHANGE, "decisions.md"), "utf8");
  for (const id of ["D-37.1", "D-37.2", "D-37.3", "D-37.4", "D-37.5"]) {
    assert.ok(doc.includes(`## ${id}`), `decisions.md is missing ${id}`);
  }
  assert.match(doc, /no database table, no migration and no new endpoint shape/, "task 2.4 must be confirmed in writing");
  assert.match(doc, /Rejected/, "a decision with no rejected alternative is a preference");
});
