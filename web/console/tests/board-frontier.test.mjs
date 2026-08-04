// board-frontier.test.mjs is the console half of the cost/latency fix.
//
// # What went wrong, and why a Go-side test could not have caught it
//
// The assembler declined to compute a cost frontier for boards built from linked runs, and expressed
// that by leaving `cost_usd` and `latency_ms` at zero. Its own tests were satisfied: nothing there
// asserted a number it had deliberately not produced. The damage happened one layer up, where this
// console rendered those zeros as measurements — a `$0.00` column beside a real quality, an axis
// labelled "Cost (USD) — lower is better" spanning $-1.00 to $1.00, and a legend calling the point
// "on the frontier: nothing beats it on both quality and cost".
//
// So the fence has to live where the claim is made. These tests read the rendered HTML and assert that
// the console does not print a cost it was not given.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { startStubPlatform, startConsole, signIn } from "./support/harness.mjs";

let platform;
let console_;
let cookie;

const WF = "acme/wf";
const PATH = `/app/workflows/${encodeURIComponent(WF)}/board`;

before(async () => {
  platform = await startStubPlatform();
  console_ = await startConsole(platform.base);
  cookie = await signIn(console_.base);
}, { timeout: 60_000 });

after(async () => {
  await console_?.close();
  await platform?.close();
});

/** boardView builds a complete BoardView; `pareto` and `cost_latency` are what each test varies. */
function boardView({ costLatency, pareto }) {
  return {
    state: "complete",
    workflow_id: WF,
    eval_set_hash: "eval-set-1",
    profile: "",
    gate_set: "",
    profiles: [],
    progress: { total: 1, completed: 1, running: 0, queued: 0, failed: 0 },
    ranked: [
      {
        rank: 1,
        variant_id: "5eb397521d07",
        label: "5eb397521d07",
        config_hash: "5eb397521d07aaaa",
        config_hash_short: "5eb397521d07",
        score: 0.8139,
        ci_low: 0.7639,
        ci_high: 0.8611,
        n_seeds: 5,
        n_cases: 8,
        method: "reported-by-cli",
        components: [],
        penalties: [],
        gate_pass: true,
        failed_gates: [],
        gate_reasons: [],
        flags: [],
        tied_with: [],
        provisional: false,
      },
    ],
    disqualified: [],
    pareto,
    coverage: { measured: false, dimensions: [], reasons: [], residuals: [], stats: null },
    spend: { entries: [], total_usd: 0, calls: 0 },
    all_tie: false,
    tie_analysis: "unavailable",
    cost_latency: costLatency,
    notes: [],
    unmeasured: [],
    runs_enqueued: 0,
  };
}

function point({ cost, latency }) {
  return {
    variant_id: "5eb397521d07",
    label: "5eb397521d07",
    quality: 0.8139,
    cost_usd: cost,
    latency_ms: latency,
    non_dominated: true,
    composite: 0,
  };
}

function answering(body) {
  platform.set((_req, res) => {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify(body));
  });
}

async function html() {
  const res = await fetch(`${console_.base}${PATH}`, { headers: { cookie }, redirect: "manual" });
  assert.equal(res.status, 200, "the board did not render");
  return res.text();
}

// ── the defect ──────────────────────────────────────────────────────────────────────────────────

test("an unavailable frontier prints no cost, no zero latency, and no dollar axis", async () => {
  answering(boardView({ costLatency: "unavailable", pareto: [point({ cost: 0, latency: 0 })] }));
  const page = await html();

  assert.ok(
    !page.includes("$0.00"),
    "the board printed $0.00 as a cost — the platform reported no cost at all",
  );
  assert.ok(
    !page.includes("$-1.00"),
    "the board drew a negative-dollar axis tick, which is the zero-width-domain padding bug",
  );
  assert.ok(
    !page.includes("Cost (USD)"),
    "the board drew a cost axis for a board with no cost",
  );
  assert.ok(
    !/nothing beats it on both/i.test(page),
    "the board claimed two-dimensional dominance over a one-dimensional ordering",
  );
});

test("an unavailable frontier says why, rather than rendering nothing", async () => {
  answering(boardView({ costLatency: "unavailable", pareto: [point({ cost: 0, latency: 0 })] }));
  const page = await html();
  assert.match(
    page,
    /cost and latency were not reported/i,
    "the board hid the cost section without saying the data was missing — an unexplained absence " +
      "reads as a broken page",
  );
});

// ── the frontier still renders when it is real ──────────────────────────────────────────────────

test("a measured frontier renders the chart with the reported values", async () => {
  answering(
    boardView({
      costLatency: "measured",
      pareto: [point({ cost: 0.2957294117647059, latency: 15459.985337243397 })],
    }),
  );
  const page = await html();

  assert.ok(page.includes("Cost (USD)"), "a measured board did not draw its cost axis");
  assert.ok(
    !page.includes("$-1.00"),
    "a measured board drew a negative-dollar tick — the domain padding is still wrong",
  );
  assert.match(page, /\$0\.30|\$0\.2957/, "the reported cost of $0.2957 is nowhere on the page");
});
