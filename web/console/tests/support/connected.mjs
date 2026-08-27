// connected.mjs is ONE stub of a platform that holds the reader's imported source.
//
// # Why this is shared rather than copied
//
// Two suites need it — P37's own acceptance and P13's, which after P37 renders against the reader's node
// instead of against a fixture — and a second copy would drift in the usual direction: the copy is the
// one that keeps answering the old shape, so the test that uses it keeps passing while the product
// changes underneath it.
//
// 🔴 It answers as a REAL platform would, field for field, and it is deliberately NOT minimal. A stub
// that returns only what the assertion reads lets the surface pass while crashing on a field it does not
// assert — which is how `/app/delivery` was found to take the whole page down on a partial 200.

/** WORKFLOW is the connected repository this acceptance runs against. */
export const WORKFLOW = "acme/agent";

/**
 * NODES are the reader's own call sites — two of them, deliberately.
 *
 * One node would let the resolver's `sole` path pass every assertion below without the ambiguous path
 * ever running, and the ambiguous path is the one that asks a question. Two nodes exercise both: the
 * shell asks once, and the answer applies everywhere.
 */
export const NODES = [
  { node_id: "answer", symbol: "handleAnswer", file: "agent/answer.py", language: "python", provider: "anthropic", model_id: "claude-sonnet-4-5" },
  { node_id: "classify", symbol: "classify", file: "agent/classify.py", language: "python", provider: "anthropic", model_id: "claude-haiku-4-5" },
];

/** connected answers as a platform that holds the reader's imported source. */
export function connected(req, res) {
  const url = req.url ?? "";
  const json = (body) => {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify(body));
  };

  if (url.startsWith("/api/v1/workflows?") || url === "/api/v1/workflows") {
    return json({ workflows: [{ workflow_id: WORKFLOW, nodes: NODES.length, reported_at: "2026-08-27T00:00:00Z" }] });
  }
  if (url.includes("/nodes")) {
    return json({ state: "ok", workflow_id: WORKFLOW, nodes: NODES, source_revision: "rev1" });
  }
  if (url.includes("/axis-projection")) {
    return json({
      state: "ok",
      projection: {
        workflow_id: WORKFLOW,
        coverage_version: "cov-1",
        stale: false,
        axes: ["context", "memory", "harness", "wiring", "graph", "prompt", "model"],
        node_count: NODES.length,
        verdicts_reported: 1,
        totals: [
          // 🔴 ONE uncovered node among many — task 6.8's fixture. The assertion is that it stays
          // visible rather than being averaged into a percentage.
          { axis: "context", applies: 1, refused: 0, not_applicable: 0, not_reported: 1, nodes: 2, stale_excluded: 0 },
          { axis: "memory", applies: 0, refused: 0, not_applicable: 0, not_reported: 2, nodes: 2, stale_excluded: 0 },
        ],
        nodes: NODES.map((n, i) => ({
          node_id: n.node_id,
          symbol: n.symbol,
          file: n.file,
          language: n.language,
          cells: [{ node_id: n.node_id, axis: "context", state: i === 0 ? "applies" : "not-reported" }],
        })),
      },
      values: {
        workflow_id: WORKFLOW,
        coverage_version: "cov-1",
        nodes: NODES.map((n) => ({
          node_id: n.node_id,
          symbol: n.symbol,
          file: n.file,
          language: n.language,
          values: [
            { node_id: n.node_id, axis: "model", state: "observed", current: n.model_id, detail: n.provider },
            { node_id: n.node_id, axis: "context", state: "observed", current: "sliding-window", detail: "python" },
            {
              node_id: n.node_id,
              axis: "memory",
              state: "not_measured",
              missing_input: "not_visible_in_static_ir",
              because:
                "a memory strategy is a store read and written BETWEEN turns, and the reported structure describes one call site at a time",
            },
            {
              node_id: n.node_id,
              axis: "harness",
              state: "not_measured",
              missing_input: "not_visible_in_static_ir",
              because: "an execution envelope is a property of how this node is deployed",
            },
            {
              node_id: n.node_id,
              axis: "prompt",
              state: "not_measured",
              missing_input: "not_visible_in_static_ir",
              because: "the reported structure carries no field for a prompt reference",
            },
            {
              node_id: n.node_id,
              axis: "wiring",
              state: "not_measured",
              missing_input: "unresolved_in_ir",
              because: "no wiring verdict was reported for this node",
            },
            {
              node_id: n.node_id,
              axis: "graph",
              state: "not_measured",
              missing_input: "frontend_emits_no_edges",
              because: "topology is a property BETWEEN nodes",
            },
          ],
        })),
      },
      context_coverage: {
        python: [
          { policy: "full", mode: "identity", reason: "Passes the whole conversation through." },
          { policy: "sliding-window", mode: "applied", reason: "Keeps the most recent N turns." },
          { policy: "summarization", mode: "declined", reason: "There is no summary in your source to select." },
        ],
      },
      covered_languages: ["go", "python"],
    });
  }
  if (url.startsWith("/api/v1/memory")) {
    return json({
      language: "python",
      dimension: "memory",
      boundary: {
        applicable: true,
        language_is_the_blocker: false,
        authorable_anyway: true,
        missing_artifact: "",
        reason: "The memory runtime has landed and Python call sites materialize it.",
      },
      strategies: [
        { strategy: "none", title: "No memory", description: "The node carries nothing across invocations.", identity: true, applies: true, params_schema: { type: "object", properties: {} } },
        {
          strategy: "scratchpad",
          title: "Scratchpad",
          description: "Recent working notes, kept verbatim and bounded by count.",
          applies: true,
          params_schema: {
            type: "object",
            properties: { max_entries: { type: "integer", description: "How many notes to retain before the oldest is dropped." } },
            required: ["max_entries"],
          },
        },
      ],
    });
  }
  if (url.startsWith("/api/v1/models")) {
    return json({
      models: [
        { version_id: "mv-1", name: "sonnet", provider: "anthropic", model_id: "claude-sonnet-4-5" },
        // 🔴 A foreign-provider model, present so the disabled-with-a-reason path (FR7) is exercised
        // rather than merely available.
        { version_id: "mv-2", name: "gpt", provider: "openai", model_id: "gpt-4o" },
      ],
    });
  }
  if (url.startsWith("/api/v1/authoring/history")) {
    return json({ changes: [] });
  }
  // The preflight and the save. 🔴 The `config_hash` and the verdict are the PLATFORM's — the surface
  // renders them as received and computes nothing (NFR7.3), so a fixture here is a fixture of the
  // platform's answer rather than of the reader's data.
  if (url.startsWith("/api/v1/authoring/preflight")) {
    return json({ verdict: "admissible", config_hash: "7c2f91ab04de", dimensions: ["memory"], nodes: ["answer"] });
  }
  if (url.startsWith("/api/v1/authoring/submit")) {
    return json({
      change_id: "ac_9d31f7c0e5a24b6688af",
      config_hash: "7c2f91ab04de",
      verification_state: "unverified",
      origin: "user",
      axis: "memory",
      actor_id: "you",
    });
  }
  // The delivery surface's own reads. Realistic rather than empty: `/api/v1/change-delivery` answering
  // a partial 200 crashes that page's server render today, which is a pre-existing defect outside this
  // change's scope — flagged, not fixed here, and not worked around by weakening an assertion either.
  if (url.includes("delivery-projection")) {
    return json({ state: "not-reported", detail: "no structure reported", fill_with: "heros link --with-ir" });
  }
  if (url.includes("/api/v1/change-delivery")) {
    return json({
      version: "cov-1",
      routes: [{ id: "runtime", label: "Runtime" }, { id: "source", label: "Source" }],
      cells: [],
      source_cells: [],
      languages: ["python"],
      states: [],
      causes: [],
    });
  }
  if (url.includes("/api/v1/deliveries")) {
    return json({ route: { state: "configured" }, deliveries: [] });
  }
  res.writeHead(200, { "content-type": "application/json" });
  res.end("{}");
}

/** disconnected answers as a platform that holds nothing of the reader's. */
export function disconnected(req, res) {
  res.writeHead(200, { "content-type": "application/json" });
  if ((req.url ?? "").startsWith("/api/v1/workflows")) {
    res.end(JSON.stringify({ workflows: [] }));
    return;
  }
  res.end("{}");
}

