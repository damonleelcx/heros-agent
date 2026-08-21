// assessment.test.mjs is P33 §5 — the assessment surface, executed against RENDERED HTML.
//
// # Why rendered HTML and not the components
//
// Every property this section promises is a property of what a reader SEES, and each of them is
// compatible with a green component test:
//
//   5.1  nine axes × four states are thirty-six cells, and `not_measured` is a DIFFERENT MESSAGE —
//        not a dimmer `observed`;
//   5.2  `origin: inferred` is visible WITHOUT hovering, so a `title` attribute does not satisfy it;
//   5.3  findings appear in evidence-strength order, and the console does not re-sort;
//   5.4  decisiveness is BESIDE the score, not behind a link;
//   5.5  evidence links go to existing console surfaces;
//   5.6  the hazard palette is on `refused` only.
//
// A component test asserts a function returned an element. This asserts the sentence is on the page.
//
// # 🔴 The fixture is deliberately the WORST report
//
// One measured finding whose eval set CANNOT FAIL, one inference, two absences with different causes,
// a budget refusal, and both P34 axes refused. A fixture in which everything worked could not show a
// single one of the states this surface exists for.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { startStubPlatform, startConsole, signIn } from "./support/harness.mjs";

let platform;
let console_;
let cookie;

const WORKFLOW = "wf-checkout-agent";

const ASSESSMENT = {
  assessment_id: "as-1",
  workflow_id: WORKFLOW,
  source_revision: "9f2c1ab4c6d1f0e3a5b7c9d1e3f5a7b9",
  agent_config_hash: "cfg3d9a7f21bbbbcccc",
  agent_config_hash_short: "cfg3d9a7f21b",
  started_at_ms: 1_755_734_400_000,
  completed_at_ms: 1_755_734_462_000,
  spend_usd: 1.0,
  spend_cap_usd: 1.0,
  tally: { measured: 1, observed: 3, not_measured: 3, refused: 2, inferred: 1 },
  partial: true,
  all_not_measured: false,
  findings: [
    {
      axis: "prompt",
      state: "measured",
      origin: "structural",
      claim: "0.94 (0.90–0.98) over 5 seeds and 3 cases — but EVERY case in this set carries an oracle that can never fail",
      rank: 0,
      evidence_surface: "board", evidence_locator: WORKFLOW,
      evidence_path: `/api/v1/workflows/${WORKFLOW}/eval-board`,
      eval_set: {
        eval_set_hash: "set-4f1c",
        score: { mean: 0.94, ci_low: 0.9, ci_high: 0.98, n_seeds: 5 },
        n_cases: 3,
        oracle_coverage: 0,
        n_indecisive: 3,
        coverage_measured: true,
        vacuous_dimensions: ["path"],
        cases: [
          { case_id: "case-01", suite: "extraction", oracle: { decisive: false, kind: "schema", reason: "the output schema constrains nothing" } },
          { case_id: "case-02", suite: "extraction", oracle: { decisive: false, kind: "schema", reason: "the output schema constrains nothing" } },
          { case_id: "case-03", suite: "extraction", oracle: { decisive: false, kind: "regex", reason: "the pattern matches every string" } },
        ],
      },
      eval_set_cannot_fail: true,
    },
    {
      axis: "model", state: "observed", origin: "structural", rank: 1,
      claim: "4 call sites use openai/gpt-4o-mini",
      evidence_surface: "graph", evidence_locator: WORKFLOW, evidence_path: `/api/v1/workflows/${WORKFLOW}/pattern-graph`,
    },
    {
      axis: "skills", state: "observed", origin: "structural", rank: 2,
      claim: "no platform skills are bound at any of the discovered call sites",
      evidence_surface: "graph", evidence_locator: WORKFLOW, evidence_path: `/api/v1/workflows/${WORKFLOW}/pattern-graph`,
    },
    {
      axis: "context", state: "observed", origin: "structural", rank: 3,
      claim: "context assembly is system+last3 across 4 call sites",
      evidence_surface: "graph", evidence_locator: WORKFLOW, evidence_path: `/api/v1/workflows/${WORKFLOW}/pattern-graph`,
    },
    {
      axis: "memory", state: "observed", origin: "inferred", rank: 4,
      claim: "a per-session dictionary written on every turn and never pruned",
      provider_model_version: "anthropic/claude-opus-5-20260501",
      inference_address: "sha256:b1946ac92492d2347c6235b4d2611184",
      evidence_surface: "graph", evidence_locator: WORKFLOW, evidence_path: `/api/v1/workflows/${WORKFLOW}/pattern-graph`,
    },
    {
      axis: "tools", state: "not_measured", origin: "structural", rank: 5,
      claim: "the tool list is built at runtime from a value discovery could not resolve",
      missing_input: "unresolved_in_ir",
      evidence_surface: "graph", evidence_locator: WORKFLOW, evidence_path: `/api/v1/workflows/${WORKFLOW}/pattern-graph`,
    },
    {
      axis: "harness", state: "not_measured", origin: "structural", rank: 6,
      claim: "this assessment reached its $1.00 cap before harness could be inferred",
      missing_input: "budget_exhausted",
      evidence_surface: "graph", evidence_locator: WORKFLOW, evidence_path: `/api/v1/workflows/${WORKFLOW}/pattern-graph`,
    },
    {
      axis: "loop", state: "refused", origin: "structural", rank: 7,
      claim: "this build does not assess the control loop as its own surface; it lands with P34",
      refusal_cause: "analysis",
      evidence_surface: "graph", evidence_locator: WORKFLOW, evidence_path: `/api/v1/workflows/${WORKFLOW}/pattern-graph`,
    },
    {
      axis: "graph", state: "refused", origin: "structural", rank: 8,
      claim: "this build does not report topology as an assessment axis yet; the analysis exists and `graph` becomes a surface you can configure with P34",
      refusal_cause: "analysis",
      evidence_surface: "graph", evidence_locator: WORKFLOW, evidence_path: `/api/v1/workflows/${WORKFLOW}/pattern-graph`,
    },
  ],
};

function serve(body, status = 200) {
  platform.set((req, res) => {
    if (req.url.startsWith("/api/v1/assessments")) {
      res.writeHead(status, { "content-type": "application/json" });
      return res.end(JSON.stringify(body));
    }
    res.writeHead(200, { "content-type": "application/json" });
    res.end("{}");
  });
}

async function page(path) {
  const res = await fetch(`${console_.base}${path}`, { headers: { cookie } });
  assert.equal(res.status, 200, `${path} answered ${res.status}`);
  return res.text();
}

/**
 * rendered strips the React flight payload and the SSR text-node separators, leaving what a reader can
 * actually see.
 *
 * 🔴 The `<!-- -->` strip is not cosmetic. React's server renderer inserts an empty comment between
 * adjacent text nodes, so a sentence written as `No case exercises {list} coverage` arrives in the HTML
 * as `No case exercises<!-- -->path<!-- --> coverage`. An assertion on the sentence then fails on a page
 * that renders it perfectly — which is a fence crying wolf, and the fastest route to a fence somebody
 * deletes.
 */
function rendered(html) {
  return html.replace(/<script\b[^>]*>[\s\S]*?<\/script>/gi, " ").replace(/<!--\s*-->/g, "");
}

before(async () => {
  platform = await startStubPlatform();
  console_ = await startConsole(platform.base);
  cookie = await signIn(console_.base);
}, { timeout: 60_000 });

after(async () => {
  await console_?.close();
  await platform?.close();
});

// ── 5.1 · nine axes, four messages ───────────────────────────────────────────────────────────────

test("5.1 all nine axes are on the page, including the ones that could not be assessed", async () => {
  serve(ASSESSMENT);
  const html = rendered(await page(`/app/assess?workflow_id=${WORKFLOW}`));
  for (const axis of ["Model", "Prompt", "Skills", "Context", "Tools", "Memory", "Harness", "Loop", "Graph"]) {
    assert.ok(
      html.includes(`>${axis}</h3>`),
      `${axis} is not on the page. A report that omits the axes it could not assess is shorter, ` +
        "prettier, and lies by construction",
    );
  }
});

test("5.1 not_measured is a different message, not a dimmer observed", async () => {
  serve(ASSESSMENT);
  const html = rendered(await page(`/app/assess?workflow_id=${WORKFLOW}`));

  // Its own band with its own heading — not the same section rendered faintly.
  assert.ok(html.includes("Not measured"), "there is no band for the axes that could not be measured");
  assert.ok(
    html.includes("Read from your code"),
    "the structural findings do not have their own band, so `observed` and `not_measured` are one section",
  );
  // And it says WHAT WAS MISSING, in words rather than as an identifier.
  assert.ok(
    html.includes("a value we could not follow"),
    "the missing input is not rendered as a phrase — a raw `unresolved_in_ir` reads as a leaked internal",
  );
  assert.ok(!html.includes("unresolved_in_ir"), "the raw identifier leaked into the page");
  assert.ok(!html.includes("budget_exhausted"), "the raw identifier leaked into the page");
});

// ── 5.2 · an inference is visible without hovering ───────────────────────────────────────────────

test("5.2 an inferred finding is marked in the row, not in a tooltip", async () => {
  serve(ASSESSMENT);
  const html = rendered(await page(`/app/assess?workflow_id=${WORKFLOW}`));

  assert.ok(
    html.includes("inferred by a model"),
    "the inference marker is not rendered as text. A `title` attribute is invisible to touch, to " +
      "keyboard navigation, and to every screen reader that does not announce tooltips",
  );
  // The marker must be OUTSIDE any title attribute — i.e. present in the document with the attributes
  // stripped. This is what makes "without hovering" checkable rather than asserted.
  const withoutAttributes = html.replace(/\s(?:title|aria-label)="[^"]*"/g, " ");
  assert.ok(
    withoutAttributes.includes("inferred by a model"),
    "the marker exists only inside an attribute, so a reader scanning the page cannot see how much of " +
      "it a model wrote",
  );
  // And the model that wrote it is named — design D7.
  assert.ok(
    html.includes("anthropic/claude-opus-5-20260501"),
    "the provider model version is not on the page; without it a provider's routine upgrade renders " +
      "as the customer's repository getting worse",
  );
});

// ── 5.3 · order arrives from the platform ────────────────────────────────────────────────────────

test("5.3 findings appear in evidence-strength order", async () => {
  serve(ASSESSMENT);
  const html = rendered(await page(`/app/assess?workflow_id=${WORKFLOW}`));
  const order = ["Prompt", "Model", "Skills", "Context", "Memory", "Tools", "Harness", "Loop", "Graph"];
  const positions = order.map((axis) => html.indexOf(`>${axis}</h3>`));
  for (let i = 1; i < positions.length; i += 1) {
    assert.ok(
      positions[i] > positions[i - 1],
      `${order[i]} appears before ${order[i - 1]}. The order is the platform's, computed from the ` +
        "evidence-strength ladder; a console that re-sorted would eventually sort by a severity " +
        "somebody guessed, which is the one ordering FR5 forbids",
    );
  }
});

// ── 5.4 / 4.5 · decisiveness beside the score ────────────────────────────────────────────────────

test("5.4 the score carries its decisiveness, and a set that cannot fail says so", async () => {
  serve(ASSESSMENT);
  const html = rendered(await page(`/app/assess?workflow_id=${WORKFLOW}`));

  assert.ok(html.includes("This eval set cannot fail"), "a set of three indecisive oracles renders no warning");
  assert.ok(
    html.includes("not evidence of quality"),
    "the page reports 0.94 without withdrawing it — which is exactly the failure this capability exists for",
  );
  assert.ok(html.includes("3 cases"), "the number of cases is not beside the score");
  assert.ok(html.includes("5 seeds"), "the seed count is not beside the score");
  assert.ok(html.includes("No case exercises path coverage"), "the vacuous dimension is not NAMED");

  // 4.3 — the cases are enumerable, and each says what decides it.
  for (const id of ["case-01", "case-02", "case-03"]) {
    assert.ok(html.includes(id), `${id} is not listed. A count is not a case list`);
  }
  assert.ok(
    html.includes("the output schema constrains nothing"),
    "an indecisive case does not say WHY — a count without a reason is not a task",
  );
});

// ── 5.5 · evidence links into existing surfaces ──────────────────────────────────────────────────

test("5.5 evidence links navigate into the existing console surfaces", async () => {
  serve(ASSESSMENT);
  const html = rendered(await page(`/app/assess?workflow_id=${WORKFLOW}`));
  assert.ok(
    html.includes(`/app/workflows/${WORKFLOW}/graph`),
    "the graph evidence does not link to the graph surface",
  );
  assert.ok(
    html.includes(`/app/workflows/${WORKFLOW}/board`),
    "the board evidence does not link to the board surface",
  );
});

// 🔴 The regression the live run against `nousresearch/hermes-agent` surfaced. That workflow's id is
// `github.com/nousresearch/hermes-agent` — three slashes — and the console built its link by splitting
// the platform path and taking the fourth segment, which is `github.com`. The link went to a workflow
// that does not exist, nothing errored, and nothing looked wrong.
test("5.5 a workflow id containing slashes still links to the right subject", async () => {
  const slashy = "github.com/nousresearch/hermes-agent";
  serve({
    ...ASSESSMENT,
    workflow_id: slashy,
    findings: ASSESSMENT.findings.map((f) => ({
      ...f,
      evidence_locator: slashy,
      evidence_path: `/api/v1/workflows/${encodeURIComponent(slashy)}/${f.evidence_surface === "board" ? "eval-board" : "pattern-graph"}`,
    })),
  });
  const html = rendered(await page(`/app/assess?workflow_id=${encodeURIComponent(slashy)}`));

  assert.ok(
    !html.includes(`/app/workflows/github.com/graph`),
    "the evidence link points at a workflow called `github.com` — the console is parsing the subject " +
      "back out of a route instead of reading it from its own field",
  );
  assert.ok(
    html.includes(`/app/workflows/${encodeURIComponent(slashy)}/graph`),
    "the evidence link does not address the whole workflow id",
  );
});

// ── 5.6 · the hazard palette is refused only ─────────────────────────────────────────────────────

test("5.6 the hazard palette is on refused and NOT on not_measured", async () => {
  const source = await readFile(join(process.cwd(), "src", "lib", "assessment.ts"), "utf8");
  const block = source.slice(source.indexOf("export const STATE_TONE"), source.indexOf("export const STATE_LABEL"));
  const tone = (state) => new RegExp(`${state}:\\s*"([a-z_-]+)"`).exec(block)?.[1];

  assert.equal(
    tone("refused"),
    "halt",
    "`refused` does not use the hazard tone. It is the only state that may: it names a limit somebody " +
      "has to act on",
  );
  for (const state of ["not_measured", "observed", "measured"]) {
    const t = tone(state);
    assert.ok(
      t !== "halt" && t !== "bad",
      `${state} uses the hazard tone \`${t}\`. An assessment of a repository the platform has just met ` +
        "is MOSTLY absence, so painting it in hazard colours makes the first report a wall of red — and " +
        "a palette that is not rare means nothing",
    );
  }
});

// ── R4 · no composite anywhere on the surface ────────────────────────────────────────────────────

test("R4 nothing on this surface reduces nine axes to one number", async () => {
  serve(ASSESSMENT);
  const html = rendered(await page(`/app/assess?workflow_id=${WORKFLOW}`));

  // The page must SAY there is no overall score. An absence somebody has to notice is not an answer to
  // the manager in §4; saying it first is what makes the refusal read as rigour.
  assert.ok(
    html.includes("There is no overall score"),
    "the page does not state that there is no overall score, so its absence reads as an unfinished feature",
  );

  const source = await readFile(join(process.cwd(), "src", "components", "assessment.tsx"), "utf8");
  const stripped = source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
  for (const shape of [
    // The arithmetic a composite arrives as. Each of these is one line somebody writes under deadline.
    /tally\.\w+\s*\/\s*/,
    /\/\s*(?:findings|axes)\.length/,
    /Math\.round\([^)]*tally/,
    /toFixed\([^)]*tally/,
  ]) {
    assert.ok(
      !shape.test(stripped),
      `assessment.tsx contains ${shape} — a number derived from the tally spans axes, which is the ` +
        "composite ruling R4 refuses. The honest summary is the distribution itself",
    );
  }
});

// ── The empty and partial states ─────────────────────────────────────────────────────────────────

test("a partial report says it stopped early, and does not present itself as complete", async () => {
  serve(ASSESSMENT);
  const html = rendered(await page(`/app/assess?workflow_id=${WORKFLOW}`));
  assert.ok(html.includes("This report stopped early"), "a partial report does not say so");
  assert.ok(
    html.includes("not findings about"),
    "the partial banner does not distinguish our limit from a finding about the reader's repository",
  );
});

test("a workflow nobody has assessed is not an error and not an empty report", async () => {
  serve({ error: "this workflow has not been assessed yet" }, 404);
  const html = rendered(await page(`/app/assess?workflow_id=${WORKFLOW}`));
  assert.ok(
    html.includes("has not been assessed yet"),
    "a never-assessed workflow does not say so, so it is indistinguishable from one we assessed and " +
      "could tell nothing about",
  );
  assert.ok(html.includes("Assess this workflow"), "there is no way to start one from the empty state");
});

test("no workflow named offers a picker rather than a default subject", async () => {
  serve(ASSESSMENT);
  const html = rendered(await page("/app/assess"));
  assert.ok(
    !html.includes(">Model</h3>"),
    "a page with no workflow named rendered a report anyway — a wrong default asserts a falsehood with " +
      "the full authority of a populated UI",
  );
  assert.ok(html.includes("Pick a workflow") || html.includes("Workflow"), "no picker is offered");
});

// ── The vocabulary cannot drift from the platform's ──────────────────────────────────────────────

test("every missing input the platform can name has console copy", async () => {
  const [go, ts] = await Promise.all([
    readFile(join(process.cwd(), "..", "..", "internal", "assessment", "state.go"), "utf8"),
    readFile(join(process.cwd(), "src", "lib", "assessment.ts"), "utf8"),
  ]);
  const fromGo = [...go.matchAll(/Missing\w+\s+MissingInput\s*=\s*"([a-z_]+)"/g)].map(([, v]) => v).sort();
  const block = ts.slice(ts.indexOf("MISSING_INPUT_LABEL"), ts.indexOf("REFUSAL_CAUSE_LABEL"));
  const fromTs = [...block.matchAll(/^\s{2}([a-z_]+):/gm)].map(([, k]) => k).sort();

  assert.ok(fromGo.length > 0, "this fence's scan found no missing inputs in Go — the scan is broken, not the code");
  assert.deepEqual(
    fromTs,
    fromGo,
    "the console's missing-input copy has drifted from the platform's closed set. A member added in Go " +
      "and missing here renders as its raw snake_case identifier — on the one axis a reader most needs " +
      "to understand.\n" +
      `  go: ${fromGo.join(", ")}\n  ts: ${fromTs.join(", ")}`,
  );
});
