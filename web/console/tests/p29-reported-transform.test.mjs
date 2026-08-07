import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";

// p29-reported-transform.test.mjs is the regression fence for a SHIPPED 500.
//
// `GET /api/v1/transforms/{config_hash}/{source_revision}` answers a UNION: a transform the platform
// GENERATED (`transformView`, has a diff and a build gate) or one it was TOLD about (P29 §6.5,
// `reportedTransformView`, has per-node outcomes and three integers and no diff at all). §6.5 built the
// second answer and left this page reading the first shape, so opening the detail page for anything a
// customer produced with `heros apply --link-receipt` crashed the server render:
//
//     TypeError: Cannot read properties of undefined (reading 'trim')
//       at .next/server/app/app/transforms/[configHash]/[sourceRevision]/page.js
//
// It type-checked perfectly, because the generated type covered only the arm that has a `diff`. That is
// the same failure §8.3 caught on `/app/runs` (`reading 'slice'`), one surface over — which is why this
// fence checks the PROPERTY rather than the one line: no path may reach a diff without first
// discriminating on `origin`.

const ROOT = path.join(import.meta.dirname, "..");
const read = (p) => readFileSync(path.join(ROOT, p), "utf8");

/**
 * stripComments removes block and line comments before a scan looks for CODE.
 *
 * 🔴 Written because the first version of this fence failed on the comment that DOCUMENTS the defect —
 * the one quoting `transform.diff.trim()` as the thing that crashed. A fence that cannot tell code from
 * prose has exactly one cheap remedy available to whoever hits it next, and it is deleting the
 * explanation. That is a worse repository than the one with the bug in it.
 */
const stripComments = (src) => src.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");

const PAGE = "src/app/app/transforms/[configHash]/[sourceRevision]/page.tsx";
const TYPES = "src/lib/types.generated.ts";

test("§6.5 both arms of the transform union are declared", () => {
  const types = read(TYPES);
  assert.match(
    types,
    /export interface ReportedTransformView \{/,
    "the reported arm has no generated type. A console with a declaration for only one arm of a union " +
      "holds a claim that is false half the time — and it will be believed, because it type-checks.",
  );
  const reported = types.match(/export interface ReportedTransformView \{([\s\S]*?)\n\}/)[1];

  // 🚫 The receipt carries counts where a diff would go (§2.8). A `diff` field here could only ever be
  // filled by inventing content the platform was not sent.
  assert.doesNotMatch(
    reported,
    /^\s*diff\??:/m,
    "ReportedTransformView declares a `diff`. The receipt has no field a hunk could occupy; a diff on " +
      "this type could only be filled by inventing content that never crossed the boundary.",
  );

  // The discriminator has to be present and non-optional, or branching on it is a guess.
  assert.match(reported, /^\s*origin: string;/m, "ReportedTransformView has no non-optional `origin`");

  // And it must carry the things the surface is FOR, or the page renders a shell.
  for (const field of ["node_outcomes", "nodes_applied", "nodes_refused", "files_changed", "diff_absent_because"]) {
    assert.match(reported, new RegExp(`^\\s*${field}[?]?:`, "m"), `ReportedTransformView is missing ${field}`);
  }
});

test("§6.5 the page reads both arms and discriminates on origin", () => {
  const src = read(PAGE);
  assert.match(src, /ReportedTransformView/, `${PAGE} does not know the reported arm exists`);
  assert.match(
    src,
    /origin === "reported"/,
    "the page does not discriminate on `origin`. Deciding the shape by dereferencing and finding out " +
      "IS the 500.",
  );
});

// 🔴 The load contract. Asking for `diff` among the required keys would turn every reported transform
// into a load FAILURE instead of a crash — a different lie with the same effect on the reader.
test("§6.5 the required keys are the ones BOTH arms carry", () => {
  const src = read(PAGE);
  const call = stripComments(src).match(/load<[^>]*>\(\s*\([\s\S]*?\)\s*=>\s*paths\.transform[\s\S]*?\)\s*;/);
  assert.ok(call, "could not find the load() call for the transform view");
  assert.doesNotMatch(
    call[0],
    /"diff"/,
    "the page requires `diff` to load. A reported transform has none, so it would render as a failed " +
      "read — telling the customer their receipt never arrived when the platform is holding it.",
  );
  assert.match(call[0], /"config_hash"/);
  assert.match(call[0], /"source_revision"/);
});

// The load-bearing one: no unguarded dereference of a field only one arm has.
//
// Stated as "every `.diff` touch is inside the executor-only component" rather than as a line number,
// because the realistic regression is somebody adding a diff hash to the shared header — where it reads
// as harmless and crashes exactly the payloads that have no diff.
test("§6.5 nothing outside TransformBody touches a diff-only field", () => {
  const src = stripComments(read(PAGE));
  const start = src.indexOf("function TransformBody(");
  assert.ok(start > 0, "TransformBody is gone; this fence needs rewriting rather than deleting");
  const outside = src.slice(0, start);

  for (const field of ["diff", "diff_hash", "verification_strength", "requires_human_review", "build_log"]) {
    const hits = [...outside.matchAll(new RegExp(`transform\\.${field}\\b`, "g"))];
    assert.equal(
      hits.length,
      0,
      `transform.${field} is dereferenced outside TransformBody. Only the EXECUTOR arm carries it; on a ` +
        `reported transform it is undefined, and the page 500s on the server before the reader sees ` +
        `anything at all.`,
    );
  }
});

// The reported arm must SAY why the diff is absent. An empty panel and a stated boundary look the same
// to a type-checker and completely different to a person deciding whether the product is broken.
test("§6.5 the reported arm states why there is no diff", () => {
  const src = read(PAGE);
  const start = src.indexOf("function ReportedTransformBody(");
  assert.ok(start > 0, "the reported arm has no renderer");
  const body = src.slice(start, src.indexOf("function TransformBody("));
  assert.match(
    body,
    /diff_absent_because/,
    "the reported arm renders no reason for the missing diff. A transform page with a blank diff reads " +
      "as broken; one that says why it is absent reads as a boundary.",
  );
  assert.match(body, /node_outcomes/, "the reported arm does not render the per-node outcomes");
  assert.match(body, /files_changed/, "the reported arm does not render the diffstat");
});
