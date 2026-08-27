// context.test.mjs — P16's guards on the context axis, RE-POINTED at their destinations by P37.
//
// # 🔴 The one fence P37 REMOVES, and why removing it is legitimate exactly once
//
// `the coverage table matches the engine's, policy for policy` guarded a HAND-TRANSCRIBED array in
// `page.tsx` (`const COVERAGE = [...]`) against `transform.ContextMaterializerCoverage()`. It was a
// correct fence for a real hazard: a transcription with no gate is a second source of truth whose
// failure is silent.
//
// P37 FR17 replaces the transcription with a LIVE PER-NODE READ. `internal/nodeaxisvalue` reads the
// engine's table at request time and the surface renders what it receives, so **there is no
// transcription left to drift**. That is the only acceptable reason to remove a fence, and design D5
// says so: *"Removing a fence whose subject still exists is a different act with the same diff, which is
// why the pull request must say which one it is doing."*
//
// What replaces it is NOT nothing:
//
//   · `internal/nodeaxisvalue.TestContextCoverageIsPerLanguageAndNeverFallsBack` — the read is per
//     language and never substitutes another language's rows;
//   · `internal/nodeaxisvalue.TestCoveredLanguagesAreTheEnginesOwn` — the covered set IS the engine's;
//   · `internal/api.TestTheAxisReadCarriesLiveContextCoverageForTheReportedLanguages` — it reaches the
//     wire, keyed by language, with an unrewritable language present-and-empty;
//   · `theCoverageTableIsNoLongerTranscribed` below — a fence against the transcription COMING BACK.
//
// Task 9.3 requires this removal to be reviewed on its own, separately from the surface rewrite that
// motivates it. This comment is the artifact that review reads.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const repo = join(root, "..", "..");
const read = (rel) => readFile(join(root, rel), "utf8");
const readRepo = (rel) => readFile(join(repo, rel), "utf8");
const flat = async (rel) => (await read(rel)).replace(/\s+/g, " ");

const PAGE = "src/app/app/context/page.tsx";
const EDITOR = "src/app/app/context/editor.tsx";
const DOC = "content/docs/en/concepts/context-policies.md";
const ENGINE = "internal/transform/contextmaterialize.go";

// ── 🔴 The replacement for the removed fence ────────────────────────────────────────────────────

test("the coverage table is no longer transcribed — it is read from the engine at request time", async () => {
  const page = await read(PAGE);

  // The transcription must not come back. A `const COVERAGE = [...]` here is a second source of truth
  // for a refusal, and it is exactly what the removed fence existed to catch.
  assert.ok(!/const COVERAGE\b/.test(page), "the hand-transcribed coverage table came back");
  assert.ok(!/const APPLIED_DIFF\b/.test(page), "the fixture diff came back into the reader's position");
  assert.ok(!/const DECLINES\b/.test(page), "the fixture declines came back into the reader's position");

  // What renders instead is the live read, per the reader's own node.
  assert.match(page, /coverageForNode\(values, subject\.node_id\)/,
    "the surface does not read coverage for the reader's own node");
  assert.match(page, /What each context policy does at this node's call site/,
    "the coverage table is not attributed to this node's call site");

  // And the read really is derived from the engine, on the platform side.
  const go = await readRepo("internal/nodeaxisvalue/nodeaxisvalue.go");
  assert.match(go, /transform\.ContextMaterializerCoverage\(\)/,
    "the live read does not come from the engine's own table");
  assert.match(go, /transform\.ContextMaterializerLanguages\(\)/,
    "the covered-language list is not the engine's own");
});

test("the engine's table still exists, so its removal from the page was a MOVE and not a loss", async () => {
  // The parser P16 used, kept, so this test goes red if the engine's shape changes under the live read.
  const goSrc = await readRepo(ENGINE);
  const table = goSrc.slice(goSrc.indexOf("var contextForms = map[string]contextForm{"));
  const policies = new Set();
  for (const m of table.matchAll(/"([a-z-]+)":\s*\{kind:\s*(ctxIdentity|ctxSelect|ctxNotAtCallSite)/g)) {
    policies.add(m[1]);
  }
  assert.ok(policies.size >= 8, `parsed only ${policies.size} policies from the engine table — parser drift`);

  // Every one of them is explained at the destination. The table left the working surface; it did not
  // leave the product.
  const doc = await read(DOC);
  for (const policy of policies) {
    assert.ok(doc.includes(`\`${policy}\``), `policy "${policy}" has no row on the reading surface`);
  }
});

// ── The claims that MOVED, asserted at their destinations ───────────────────────────────────────

test("the APPLIED case survives, on the reading surface, labelled as the platform's fixture", async () => {
  const doc = await read(DOC);
  // 🔴 The applied case must still exist somewhere: a surface that only ever says no teaches its reader
  // that the axis does not work. After P37 it is a WORKED EXAMPLE, and it must say so — because on the
  // working surface that position now belongs to the reader's own data.
  assert.match(doc, /### An applied window/, "the applied case was lost rather than moved");
  assert.match(doc, /MessageParam\{turnThree, turnFour\}/, "the engine's own diff was not carried over");
  assert.match(
    doc,
    /\*\*the platform's own fixture\*\*/i,
    "the worked examples are not labelled as the platform's fixture",
  );
  // Its invariant travels with it. A diff shown without the property that makes it safe is a diff a
  // reviewer stops reading.
  assert.match(doc, /Only turns were removed, and no line was added/,
    "the applied case lost the invariant that makes it readable");
  assert.ok(
    !/lines were reordered/.test(doc.slice(doc.indexOf("### An applied window"), doc.indexOf("### Four declines"))),
    "the context example must not claim the wiring axis's invariant — its diff deletes list elements",
  );
});

test("the decline order the engine uses is preserved at the destination", async () => {
  const doc = await read(DOC);
  // 🔴 The **kwargs decline must come BEFORE the language one. The engine's ordering exists because a
  // call site with no written message list is told about the kwargs, not asked to wait for a rewriter
  // that would refuse it too — and a document that listed them the other way teaches the old lesson.
  const kwargs = doc.indexOf("#### A call that unpacks its arguments");
  const language = doc.indexOf("#### A language without a rewriter");
  assert.ok(kwargs > 0, "the **kwargs decline — the most common one on a real repository — is missing");
  assert.ok(language > kwargs, "the language decline is listed before the **kwargs one");
  assert.match(doc, /property of the call site/, "the kwargs decline must say it is not about language support");

  // Exactly one decline may promise future work.
  const promises = [...doc.matchAll(/materializer is still being built/g)].length;
  assert.equal(promises, 1, `${promises} declines promise a pending rewriter; only the language one may`);
});

test("the languages whose selection rewriter has landed are named, from the engine's own list", async () => {
  const span = await readRepo("internal/transform/contextmaterialize_span.go");
  assert.ok(
    span.includes("discovery.ListSplitLanguages()"),
    "parser drift: the span materializer table is no longer derived from the shared splitter",
  );
  const splitters = await readRepo("internal/discovery/listsplit.go");
  const table = splitters.slice(splitters.indexOf("var listSyntaxes"), splitters.indexOf("// ListSplitLanguages"));
  const langs = new Set(["go"]);
  for (const m of table.matchAll(/^\t"([a-z]+)":\s*\{/gm)) langs.add(m[1]);
  assert.ok(langs.has("python"), "parser drift: the splitter table no longer lists python");

  const doc = await read(DOC);
  for (const shown of ["go", "python"]) {
    assert.ok(
      doc.includes(`\`${shown}\``),
      `the engine materializes a selection policy in ${shown} and the reading surface does not say so`,
    );
  }
});

test("the drop gate is explained where a reader can find it", async () => {
  const doc = await flat(DOC);
  assert.match(doc, /## What it costs to lose context/, "the drop-tolerance gate lost its section");
  assert.match(doc, /never becomes a diff and never consumes an evaluation run/i,
    "it must say the rejection happens before eval spend");
  assert.match(doc, /tolerance/i, "it must name the tolerance");
  assert.match(doc, /we have no data.{0,40}must not come to mean/i,
    "it must state that an unmeasured policy is admitted, not refused on ignorance");

  // 🔴 And the WORKING surface still states the boundary above the control (FR15). This half did NOT
  // move: it changes with the reader's node, and it is what stops them composing a change that cannot
  // land.
  const editor = await flat(EDITOR);
  assert.match(editor, /refused when it is proposed/i,
    "the working surface lost the boundary that must sit above the control");
  assert.match(editor, /Both numbers are shown/i, "the working surface lost the two-numbers rule");
});

test("retrieval tuning states held-out verification and pinning", async () => {
  const doc = await flat(DOC);
  assert.match(doc, /## Retrieval tuning/, "retrieval tuning lost its section");
  assert.match(doc, /held-out/i, "the held-out rule is the retrieval claim that is easiest to fake without");
  assert.match(doc, /refused/i, "an overlapping split is refused, and the document must say so");
  assert.match(doc, /pins the retriever/i, "the pinning claim must be stated");
  // 🔴 The promise is about the REQUEST, never the provider's bytes.
  assert.match(doc, /outside anything this platform controls/i,
    "the document must state the reproducibility ceiling rather than over-promise");
});

// ── The claims that STAYED, asserted on the working surface ─────────────────────────────────────

test("the axis note no longer claims context cannot be rewritten (P16 §3)", async () => {
  const src = await read("src/components/axisRefusal.tsx");
  const note = src.slice(src.indexOf("  context:"), src.indexOf("};", src.indexOf("  context:")));
  assert.ok(
    !/so there is no expression to rewrite here/.test(note),
    "the context axis note still says the rewrite is impossible; Go materialises selection policies now",
  );
  assert.match(note, /Go and Python do this/, "the note must say which languages apply a policy");
  assert.match(note, /still runs/, "the note must say the declined policy still runs");
});

test("the context surface is reachable from the nav and the command path", async () => {
  const layout = await read("src/app/app/layout.tsx");
  assert.ok(layout.includes('href: "/app/context"'), "the context surface must be in the nav rail");
  assert.ok(layout.includes('id: "s:context"'), "the context surface must be in the command path");
});

test("🔴 no fixture occupies the reader's data position (FR4)", async () => {
  const page = await read(PAGE);
  const editor = await read(EDITOR);
  // P16's rule was "say it is not the tenant's data". P37's is stronger and structural: the example is
  // not there at all, so there is nothing to label. `axis-node-projection` (P37 delta): "no worked
  // example appears in the position that data occupies".
  for (const fixture of ["recall", "turnThree", "pipeline.go", "n_24ee3c42", "n_9f1c04ab"]) {
    assert.ok(!page.includes(fixture), `the page still renders the fixture ${fixture}`);
    assert.ok(!editor.includes(fixture), `the editor still renders the fixture ${fixture}`);
  }
  // What is in that position is the resolved subject, and there is no code path without one.
  assert.match(page, /<AxisFrame axis="context"/, "the surface is not bound to a resolved subject");
});

test("the applied card's invariant is supplied by the caller, never asserted by the component", async () => {
  const src = await read("src/components/axisRefusal.tsx");
  // 🔴 It was a constant until P16, hard-coded to the wiring axis, and the first caller from another
  // axis rendered "the file's lines were reordered" over a diff that had deleted list elements.
  assert.ok(
    !/The file&apos;s lines were reordered/.test(src),
    "AxisApplied hard-codes the wiring invariant; another axis would inherit a guarantee that is false for it",
  );
  assert.match(src, /invariant: ReactNode/, "the invariant must be a REQUIRED prop, so it cannot be omitted");
});
