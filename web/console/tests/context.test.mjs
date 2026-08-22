// context.test.mjs — P16 QA guards on the context surface, as FAILING TESTS rather than review notes.
//
// This surface makes one claim a console is uniquely able to get wrong: it tells the reader WHICH
// context policies this platform writes into their source and which it declines. That claim lives in
// the engine (`transform.ContextMaterializerCoverage()`), and the page is a transcription of it. A
// transcription with no gate is a second source of truth, and the failure is silent — the page keeps
// rendering, confidently, a boundary that has moved.
//
// So the tests below read the engine's own Go source and assert the page agrees with it, policy by
// policy. They also pin the two things the page must never quietly become: a surface that only says no,
// and a surface whose axis note still claims context cannot be rewritten at all.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const repo = join(root, "..", "..");
const read = (rel) => readFile(join(root, rel), "utf8");
const readRepo = (rel) => readFile(join(repo, rel), "utf8");

const PAGE = "src/app/app/context/page.tsx";
const ENGINE = "internal/transform/contextmaterialize.go";

/** engineCoverage parses the engine's coverage table: policy → identity | select | not-at-call-site. */
function engineCoverage(goSrc) {
  const table = goSrc.slice(goSrc.indexOf("var contextForms = map[string]contextForm{"));
  const out = new Map();
  for (const m of table.matchAll(/"([a-z-]+)":\s*\{kind:\s*(ctxIdentity|ctxSelect|ctxNotAtCallSite)/g)) {
    out.set(m[1], m[2]);
  }
  return out;
}

/** pageCoverage parses the page's COVERAGE constant: policy → identity | applied | declined. */
function pageCoverage(tsxSrc) {
  const table = tsxSrc.slice(tsxSrc.indexOf("const COVERAGE"), tsxSrc.indexOf("const APPLIED_DIFF"));
  const out = new Map();
  for (const m of table.matchAll(/policy:\s*"([a-z-]+)",\s*\n\s*mode:\s*"(identity|applied|declined)"/g)) {
    out.set(m[1], m[2]);
  }
  return out;
}

const MODE_FOR_KIND = {
  ctxIdentity: "identity",
  ctxSelect: "applied",
  ctxNotAtCallSite: "declined",
};

test("the coverage table matches the engine's, policy for policy (P16 NFR7)", async () => {
  const engine = engineCoverage(await readRepo(ENGINE));
  const page = pageCoverage(await read(PAGE));

  assert.ok(engine.size >= 8, `parsed only ${engine.size} policies from the engine table — parser drift`);

  for (const [policy, kind] of engine) {
    const shown = page.get(policy);
    assert.ok(
      shown,
      `the engine has a row for "${policy}" and the console does not list it. A reader deciding whether ` +
        `to use that policy has no way to learn what this platform does with it.`,
    );
    assert.equal(
      shown,
      MODE_FOR_KIND[kind],
      `the console says "${policy}" is ${shown}; the engine says ${MODE_FOR_KIND[kind]}. The page is a ` +
        `transcription of the engine's table, and a transcription that has drifted is a confident lie.`,
    );
  }
  for (const policy of page.keys()) {
    assert.ok(
      engine.has(policy),
      `the console lists "${policy}", which the engine has no row for — a capability claimed for a ` +
        `policy this platform has no evidence about`,
    );
  }
});

test("the surface shows an APPLIED case, not only declines (P16 §2)", async () => {
  const src = await read(PAGE);
  // A surface that only ever says no teaches its reader that the axis does not work. The applied tab
  // must exist AND must come before the declines.
  const appliedAt = src.indexOf('id: "applied"');
  const declinesAt = src.indexOf("...DECLINES.map");
  assert.ok(appliedAt > 0, "the context surface must show an applied change, not only refusals");
  assert.ok(
    appliedAt < declinesAt,
    "the applied case must precede the declines: a reader who meets three refusals first learns the " +
      "wrong thing about the axis",
  );
  // And it carries a real diff, not a description of one.
  assert.match(src, /APPLIED_DIFF/, "the applied case must carry the engine's own diff");
  assert.match(src, /^\+.*MessageParam\{turnThree, turnFour\}/m, "the diff must show the windowed list");
});

test("the page names the languages whose selection rewriter has landed", async () => {
  const engine = await readRepo(ENGINE);
  const span = await readRepo("internal/transform/contextmaterialize_span.go");
  // 🔴 The engine's language set moved in wave 16d: spanContextMaterializers is now DERIVED from the
  // shared list splitter (discovery.ListSplitLanguages) rather than hand-listed, so this test reads the
  // splitter table — the new single source — instead of a literal map that no longer exists. A test that
  // kept parsing the old shape would have gone red for the right reason and been "fixed" by relaxing it.
  assert.ok(
    span.includes("discovery.ListSplitLanguages()"),
    "parser drift: the span materializer table is no longer derived from the shared splitter",
  );
  const splitters = await readRepo("internal/discovery/listsplit.go");
  const table = splitters.slice(splitters.indexOf("var listSyntaxes"), splitters.indexOf("// ListSplitLanguages"));
  const langs = new Set(["go"]);
  for (const m of table.matchAll(/^\t"([a-z]+)":\s*\{/gm)) langs.add(m[1]);
  assert.ok(langs.has("python"), "parser drift: the splitter table no longer lists python");
  assert.ok(langs.size > 2, `the splitter table should carry several languages, parsed ${[...langs]}`);

  const page = await read(PAGE);
  // The page must name the languages the engine materializes in. It states them as a set on the
  // coverage surface; here it must at least name the ones this surface claims for itself.
  for (const shown of ["Go", "Python"]) {
    assert.ok(
      page.includes(`>${shown}<`) || page.includes(`${shown}</span>`) || page.includes(`${shown} `),
      `the engine can materialize a selection policy in ${shown} and the console does not say so; a ` +
        `reader deciding whether this axis applies to their repository would conclude it does not`,
    );
  }
  assert.ok(engine.includes("ContextMaterializerLanguages"), "the engine must expose its language list");
});

test("the decline tabs are ordered the way the engine considers them (P16)", async () => {
  const src = await read(PAGE);
  // 🔴 The **kwargs decline must come BEFORE the language one. The engine's ordering fix exists because
  // a call site with no written message list is told about the kwargs, not asked to wait for a rewriter
  // that would refuse it too — and a page that listed them the other way would teach the old lesson.
  const kwargs = src.indexOf('id: "kwargs"');
  const language = src.indexOf('id: "language"');
  assert.ok(kwargs > 0, "the **kwargs decline — the most common one on a real repository — is missing");
  assert.ok(
    kwargs < language,
    "the language decline is listed before the **kwargs one; the engine considers the call site first " +
      "precisely because 'wait for a rewriter' is the less actionable answer",
  );
  assert.match(src, /property of the call site/, "the kwargs decline must say it is not about language support");
  // Exactly one decline may promise future work.
  const promises = [...src.matchAll(/materializer is still being built/g)].length;
  assert.equal(promises, 1, `${promises} declines promise a pending rewriter; only the language one may`);
});

test("the axis note no longer claims context cannot be rewritten (P16 §3)", async () => {
  const src = await read("src/components/axisRefusal.tsx");
  const note = src.slice(src.indexOf("  context:"), src.indexOf("};", src.indexOf("  context:")));
  // The pre-P16 sentence ended at "there is no expression to rewrite here", which stopped being true
  // when the Go materialiser landed. A console that keeps saying "impossible" about something the
  // platform now does teaches its reader to stop believing the notes.
  assert.ok(
    !/so there is no expression to rewrite here/.test(note),
    "the context axis note still says the rewrite is impossible; Go materialises selection policies now",
  );
  assert.match(note, /Go and Python do this/, "the note must say which languages apply a policy");
  assert.match(
    note,
    /still runs/,
    "the note must say the declined policy still runs, so the reader does not think the capability is lost",
  );
});

test("the drop gate is explained where a reader can find it (P16 §5)", async () => {
  const src = await read(PAGE);
  assert.match(src, /id: "drop"/, "the drop-tolerance gate needs its own tab; a gate nobody can find reads as a bug");
  // The three things the gate's explanation must contain, because none is guessable from a refusal.
  assert.match(src, /before any evaluation/i, "it must say the rejection happens before eval spend");
  assert.match(src, /tolerance/i, "it must name the tolerance");
  assert.match(
    src,
    /no data.*(must not|never).*mean|admitted and goes to verification/is,
    "it must state that an unmeasured policy is admitted, not refused on ignorance",
  );
});

test("the retrieval tab states held-out verification and pinning (P16 §6)", async () => {
  const src = await read(PAGE);
  assert.match(src, /id: "retrieval"/, "retrieval tuning needs a tab");
  assert.match(src, /held-out/i, "the held-out rule is the retrieval claim that is easiest to fake without");
  assert.match(src, /refused/i, "an overlapping split is refused, and the page must say so");
  assert.match(src, /pins the retriever/i, "the pinning claim must be stated");
  // 🔴 The promise is about the REQUEST, never the provider's bytes. A page that promised byte-level
  // reproducibility would be promising something the platform cannot keep.
  assert.match(
    src,
    /outside anything this platform controls/i,
    "the page must state the reproducibility ceiling rather than over-promise",
  );
});

test("the context surface is reachable from the nav and the command path", async () => {
  const layout = await read("src/app/app/layout.tsx");
  assert.ok(layout.includes('href: "/app/context"'), "the context surface must be in the nav rail");
  assert.ok(layout.includes('id: "s:context"'), "the context surface must be in the command path");
});

test("the surface says none of it is tenant data (P16, same honesty rule as wiring)", async () => {
  const src = await read(PAGE);
  assert.match(
    src,
    /None of it is this tenant/,
    "a worked example presented without saying so reads as the tenant's own data",
  );
});

test("the applied card's invariant is supplied by the caller, never asserted by the component", async () => {
  const src = await read("src/components/axisRefusal.tsx");
  // 🔴 It was a constant until P16, hard-coded to the wiring axis, and the first caller from another
  // axis rendered "the file's lines were reordered" over a diff that had deleted list elements. A
  // safety claim a component asserts on behalf of a change it knows nothing about is the sentence a
  // reviewer stops reading the diff because of.
  assert.ok(
    !/The file&apos;s lines were reordered/.test(src),
    "AxisApplied hard-codes the wiring invariant; another axis would inherit a guarantee that is false for it",
  );
  assert.match(src, /invariant: ReactNode/, "the invariant must be a REQUIRED prop, so it cannot be omitted");

  // And each caller states the property that actually holds for its own change.
  const wiring = await read("src/app/app/graph/page.tsx");
  assert.match(wiring, /Same line\s+count, same lines/, "the wiring page must keep its transposition invariant");
  const context = await read(PAGE);
  assert.match(
    context,
    /Only turns were removed, and no line was added or removed/,
    "the context page must state ITS invariant: a deletion of turns, not a reordering of lines",
  );
  assert.ok(
    !/lines were reordered/.test(context),
    "the context page must not claim the wiring axis's invariant — its diff deletes list elements",
  );
});
