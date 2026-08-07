import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import path from "node:path";

// p29-pickers.test.mjs is P29 §4.7 and §4.8.
//
// §4.8 is a REGRESSION fence, and it is the one that matters most here: `ui-redesign-feature-and-visual-
// consistency` — 「UI 改版不得丢失既有功能」 — is a 🔴 rule, and this change rewrote the component every
// picker surface renders. The failure it guards against is silent by construction: a control that
// disappears in a rewrite breaks nothing, compiles, type-checks, and is discovered by the one customer
// whose workflow depended on it.

const ROOT = path.join(import.meta.dirname, "..");
const read = (p) => readFileSync(path.join(ROOT, p), "utf8");

const PICKER_PAGES = [
  "src/app/app/workflows/page.tsx",
  "src/app/app/variants/page.tsx",
  "src/app/app/transforms/page.tsx",
  "src/app/app/runs/page.tsx",
];

// 🔴 §4.8 — EVERY CONTROL THAT WAS THERE IS STILL THERE.
//
// The list is spelled out rather than derived, deliberately: a derived list would be derived from the
// component as it is NOW, and would therefore agree with any removal. What a regression fence has to
// compare against is the shape BEFORE the change, which only a written record can supply.
test("§4.8 the picker keeps every control it had before this change", () => {
  const picker = read("src/components/subjectPicker.tsx");

  const controls = [
    // The identifier field — the accelerator, and the ONLY way in for a subject the enumeration does
    // not carry (one somebody else produced, or one from before ownership was recorded).
    { what: "the identifier input", match: /<input\b/ },
    { what: "the field's label", match: /<label htmlFor=\{field\.name\}/ },
    { what: "the submit button", match: /button--primary/ },
    { what: "the GET form that resolves an id to its canonical route", match: /method="get" action=\{action\}/ },
    { what: "the help text under the field", match: /id=\{`\$\{field\.name\}-help`\}/ },
    // `children` is how a TWO-PART subject (a transform's revision) adds its second field. Losing it
    // would make /app/transforms unable to accept a revision at all.
    { what: "the children slot for a second field", match: /\{children\}/ },
    // The subject list, its links, and the per-row hint.
    { what: "the subject list", match: /<ul\b/ },
    { what: "a link per subject", match: /<Link\b/ },
    { what: "the per-row hint", match: /subject\.hint/ },
    { what: "the chevron affordance", match: /ChevronRight/ },
    // The section the list lives in, and the labelled heading the form has.
    { what: "the list section", match: /<Section\b/ },
    { what: "the form's accessible heading", match: /aria-labelledby="open-title"/ },
  ];

  for (const c of controls) {
    assert.match(
      picker,
      c.match,
      `SubjectPicker lost ${c.what}. 「UI 改版不得丢失既有功能」 — a redesign adds; it does not remove. ` +
        `If this control is genuinely obsolete, that is a decision somebody records here, not one a ` +
        `rewrite makes by omission.`,
    );
  }
});

// Every picker surface still renders the picker, and still offers hand entry.
test("§4.8 all four picker surfaces still mount the picker with an identifier field", () => {
  for (const page of PICKER_PAGES) {
    const src = read(page);
    assert.match(src, /<SubjectPicker\b/, `${page} no longer renders the picker`);
    assert.match(
      src,
      /field=\{\{/,
      `${page} no longer passes an identifier field. Hand entry is RETAINED by §4.7 — it is the only ` +
        `way in for a subject the enumeration does not carry.`,
    );
  }
});

// 🔴 §4.7 — the pickers populate from the ENUMERATION, and `visited` is only an ordering hint.
test("§4.7 every picker surface populates from the platform enumeration", () => {
  for (const page of PICKER_PAGES) {
    const src = read(page);
    assert.match(
      src,
      /available=\{available\}/,
      `${page} does not pass an enumeration to the picker. Before P29 the pickers offered ONLY the ` +
        `subjects this browser session had opened, so a developer who came back the next day found a ` +
        `console that had forgotten their workflow existed.`,
    );
    assert.match(
      src,
      /orderByRecentlyVisited/,
      `${page} does not use the session list as an ordering hint — it is demoted, not deleted.`,
    );
    assert.match(
      src,
      /discardedVisits/,
      `${page} does not discard remembered subjects the enumeration lacks. A picker that offers a door ` +
        `which does not open is worse than one that offers fewer doors.`,
    );
    assert.doesNotMatch(
      src,
      /visited=\{visitedSubjects/,
      `${page} still passes the session list AS the picker's list. That is the defect §4.7 closes.`,
    );
  }
});

// A read failure must never reach the screen as "you have none".
test("§4.5 the picker renders three distinct states and never collapses them", () => {
  const picker = read("src/components/subjectPicker.tsx");
  assert.match(picker, /available\.state === "not-mounted"/, "no not-mounted branch");
  assert.match(picker, /available\.state === "read-failed"/, "no read-failed branch");
  assert.match(
    picker,
    /This is not the same as having none/,
    "the read-failed copy does not distinguish itself from an empty list — which is the whole point of " +
      "having a separate state",
  );

  const enumeration = read("src/lib/enumeration.ts");
  assert.match(
    enumeration,
    /return \{ state: "read-failed", subjects: \[\], detail: outcome\.error \};/,
    "enumeration.ts does not map a fetch failure to read-failed",
  );
  assert.doesNotMatch(
    enumeration,
    /catch[\s\S]{0,120}state: "empty"/,
    "enumeration.ts turns a caught failure into `empty`, which tells a returning customer their data " +
      "is gone when it is not",
  );
});

// The four enumeration paths are real platform routes, spelled as literals the route fence can read.
test("§4.1–4.3 the console addresses the enumeration routes by literal path", () => {
  const scope = read("src/lib/scope.ts");
  for (const literal of ["`/api/v1/workflows`", "`/api/v1/variants`", "`/api/v1/transforms`", "`/api/v1/runs`"]) {
    assert.ok(
      scope.includes(literal),
      `scope.ts does not carry ${literal} as a literal. The route fence reads these as TEXT; an ` +
        `interpolated path is a path no scanner can check and no server may answer.`,
    );
  }
});

// subjects.ts's own doc comment must stop claiming the gap it describes still exists — a sentence that
// quietly stops being true is worse than one that never said much.
test("§4.7 subjects.ts no longer claims the platform exposes no enumeration", () => {
  const subjects = read("src/lib/subjects.ts");
  assert.doesNotMatch(
    subjects,
    /exposes \*\*no enumeration endpoint for any of them\*\*/,
    "subjects.ts still states the gap P29 closed. The claim is now false, and a reader who believes it " +
      "will not go looking for the endpoints that exist.",
  );
});

// Nothing outside the picker surfaces started depending on the session list as a data source.
test("§4.7 no surface treats the session list as platform data", () => {
  const appDir = path.join(ROOT, "src/app/app");
  const offenders = [];
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        walk(full);
        continue;
      }
      if (!entry.name.endsWith(".tsx")) continue;
      const src = readFileSync(full, "utf8");
      if (/visited=\{visitedSubjects/.test(src)) offenders.push(path.relative(ROOT, full));
    }
  };
  walk(appDir);
  assert.deepEqual(
    offenders,
    [],
    `these surfaces still pass the session's own list where platform data belongs:\n  ${offenders.join("\n  ")}`,
  );
});

// 🔴 The merged runs list renders BOTH origins without assuming either one's fields.
//
// This is a regression fence for a defect the browser caught and the type-check did not: `RunSummary`
// was hand-written as if every field were always present, the merged list started returning linked rows
// whose fields live under `linked`, and `/app/runs` answered 500 with
// `Cannot read properties of undefined (reading 'slice')`.
//
// The lesson is the general one: a hand-written type is a CLAIM about a payload, and a claim that has
// stopped being true type-checks perfectly.
test("§4.2 the runs list never dereferences a field a linked row does not carry", () => {
  const src = read("src/app/app/runs/page.tsx");

  // Every optional field must be guarded before use. `.slice(` on a possibly-absent string is the exact
  // shape that failed.
  const unguarded = [...src.matchAll(/run\.(\w+)\.slice\(/g)].map((m) => m[0]);
  assert.deepEqual(
    unguarded,
    [],
    `the runs list calls .slice() directly on a run field: ${unguarded.join(", ")}. A LINKED row does ` +
      `not carry the executor's fields — they are under \`linked\` — and dereferencing one is a 500 on ` +
      `the page a customer opens first.`,
  );

  // The row must branch on origin rather than assuming one shape.
  assert.match(src, /run\.origin === "linked"/, "the runs list does not branch on origin");
  assert.match(
    src,
    /not observed/,
    "a linked row shows no honest status. It has none — the platform learned of the run, it did not " +
      "perform it — and a blank cell reads as a rendering bug rather than as the boundary it is.",
  );

  // And the type must say the fields are optional, so the next reader is warned by the type system.
  assert.match(src, /config_hash\?: string;/, "RunSummary still claims config_hash is always present");
  assert.match(src, /status\?: string;/, "RunSummary still claims status is always present");
});

// ── §4.7 addendum: the CLIENT picker was missed, and only a browser could tell ────────────────────
//
// 🔴 P29 §4.1 moved `GET /api/v1/workflows` onto the reported-structure store and its rows became
// OBJECTS. §4.7 updated the four server-rendered pickers. The studio's matrix is a CLIENT component and
// was not updated, so it kept `useState<string[]>` and rendered each row directly as a React child.
//
// Everything green: tsc passed (the type was a claim, not a check against the wire), every test passed,
// and the page server-rendered correctly — because the fetch happens after hydration. In a real browser
// it threw React #31 (`Objects are not valid as a React child`, keys
// {workflow_id, source_revision, source_revision_display, reported_at, nodes, edges, coverage_version})
// and the studio rendered "A transport failure, not an empty result" — a sentence that sends the reader
// to check a network which is working perfectly.
//
// This fence exists because the class recurs: `/app/runs` ('slice'), `/app/transforms` ('trim'), and now
// the studio. Every one is a consumer holding a stale claim about a payload's shape.
test("§4.7 the studio's client picker reads enumeration ROWS, not a list of ids", () => {
  const src = read("src/app/app/studio/matrix.tsx");

  assert.doesNotMatch(
    src,
    /useState<string\[\]\s*\|\s*null>/,
    "the studio picker still holds `string[]`. /api/v1/workflows returns objects; rendering one as a " +
      "React child throws #31 at hydration and the whole matrix reports a transport failure.",
  );
  assert.match(
    src,
    /useState<WorkflowRow\[\]\s*\|\s*null>/,
    "the studio picker does not use the shared WorkflowRow type. A third local definition of this shape " +
      "is a third thing that can fall behind the wire.",
  );
  // The row must be dereferenced, never rendered whole.
  assert.match(src, /w\.workflow_id/, "the picker does not read `workflow_id` off the row");
  assert.doesNotMatch(
    src,
    /\{w\}/,
    "the picker renders a whole row as a child. That is exactly React #31.",
  );
});
