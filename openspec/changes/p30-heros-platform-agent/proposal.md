# P30 — HEROS: the platform agent that reads what the parsers cannot

## Why

An operator opened `/app/workflows/openclaw` and reported four problems: the Structure drawing is an
unordered stack, no overall pattern is reported, the eval set's scenarios are nowhere, and there are no
proposals. Three of the four are one defect wearing three faces.

**`openclaw` has 22 nodes and 0 edges, because exactly one discovery frontend has ever emitted an
edge.** `internal/discovery/frontend_go.go:65` appends `g.Edges`; `frontend_tsjs.go`,
`frontend_python.go`, `frontend_rust.go`, `frontend_java.go` and `frontend_kotlin.go` contain no
occurrence of `Edges` at all. `BuildGraph` walks a `go/ast` tree — it is a Go-typed function and the
other five frontends have nothing to hand it. The corroborating evidence is already on the screen: all
22 nodes read `openai/unresolved`, and `internal/runlink/workflowir.go:82` defines `unresolved` as *the
model a node calls when a syntactic frontend could not follow it*. One author, two symptoms.

Everything downstream is a function of the graph, so everything downstream is empty:

| Surface | Reported cause |
|---|---|
| Structure | `layer`/`order` derive from edges (`patternclassifier/graphview.go:131`). Zero edges ⇒ every node is layer 0 ⇒ a vertical stack. There is no order to draw. |
| Patterns | Seven of the eight detectors are topology predicates; the eighth needs registry-bound skills. With 0 edges and 0 bound skills **0/22 can fire by construction** — and the header then renders `llm_calls === 0` as *"Fully rule-covered — no model was consulted"*, asserting complete coverage over a graph with zero labels. |
| Eval set | `n_cases` is rendered as an integer in three files and as a list in none. `evalboard.CoverageView` already models oracle coverage, vacuous dimensions and indecisive cases; none of it reaches a screen naming **which** cases. |
| Proposals | **Three stacked failures.** (1) `POST /api/v1/workflows/{id}/proposals/generate` is mounted in `internal/api/proposalgen.go` and nothing in `web/console` calls it — there is no button. (2) It is absent from `deploy/k8s/overlays/prod/ingress.yaml`, so a button would 404. (3) `internal/hostedproposals/hostedproposals.go:104` sets `State = "empty"` whenever the store has no rows, discarding `proposalgen`'s five distinct reasons — `no_linked_runs`, `no_per_node_metrics`, `no_discovered_graph`, `no_model_menu`, `no_bottleneck` — each of which has a different next action. |

None of this is careless. `dataEdges`'s own comment states the boundary honestly: *"Edge inference is
best-effort and intra-procedural; what static analysis cannot see is simply not an edge (it is never
guessed)."* That was right for a syntactic frontend. The defect is that **nothing was built to cover the
residue**, and every surface downstream presents the residue as a result.

A regex cannot follow a value across a module boundary in TypeScript. A model reading the repository
can. P30 introduces **HEROS**, the platform's own agent: it produces the graph the static frontends
cannot, and it reports whether each surface's answer rests on evidence. It is configured entirely from
the operator console — prompt, model, credential reference, skills, tools, context, memory, harness —
**through the six-axis vocabulary the product already sells**. HEROS is a Variant Spec resolved against
the P2 registries, sealed to a `config_hash`. The platform optimizes agentic workflows for a living;
its own agent is one of them.

Product rationale, personas, role lenses and the open decisions: [`docs/prd/P30-heros-platform-agent.md`](../../../docs/prd/P30-heros-platform-agent.md).

## What Changes

- **The four surface defects are fixed independently of the agent, and first.** A graph with zero edges
  states that fact and its cause instead of drawing a positional stack. `llm_calls === 0` over an
  unclassified graph reads *"nothing was classified and no model was consulted"*, never *"fully
  rule-covered"*. Eval-set cases become listable. `hostedproposals` carries `proposalgen`'s state
  through, and `empty` is reserved for *a pass ran and found nothing*. A deployment with HEROS switched
  off is strictly more honest than today's.

- **HEROS's definition is a Variant Spec, not a new settings table.** Prompt and model (P13), skills and
  tools (P14), context (P16), memory (P17) and harness (P18) are edited in the operator console through
  the existing authoring surfaces; wiring (P15) is fixed and read-only. Publishing an edit mints a new
  immutable version — the P2 registry rule, unchanged — identified by a `config_hash` from
  `internal/confighash`. A definition cannot become active until it passes a **rehearsal** against the
  pinned fixture repositories, and a failed rehearsal names the failing fixture.

- **🚫 No axis is a text box.** Each editor binds to the vocabulary that axis already has: the prompt to a
  parsed template with *derived* slots; skills to registered `SkillEntry` versions with hermetically
  compiled schemas (a remote `$ref` is rejected, not fetched); tools to `toolindex` with tenant scope,
  `risk_tier` and approval state, where unapproved is not bindable and 🚫 a tool declaring outbound
  network access is not bindable at all — HEROS reads a pinned snapshot, and a tool reaching the network
  from inside the analysis loop would be an egress surface created by a dropdown. Context, memory and
  harness bind to their named sets (7, 5 and 5 members), with params validated against the declared
  `ParamsSchema` **at save** rather than at run, and `max_turns` required for any multi-turn harness and
  bounded by `MaxTurnsCeiling`. Every definition records the **set version** of each closed vocabulary it
  references, so a stored `config_hash` stays interpretable after a set is versioned forward.

- **🔴 HEROS's memory is scoped to one inference.** Memory stays a managed axis, and keeping it settles
  two things. `memoryruntime.Key` is `{NodeID, SessionID}` and the runtime **never invents a session id**
  — a defaulted one *"silently merges conversations that should be separate"* — so HEROS supplies the
  **inference id** and discards entries when the analysis ends. Memory never spans inferences, workflows
  or tenants. Either reason alone would force this: a platform agent reads many customers' repositories,
  so persistent memory is a cross-tenant path created by a dropdown; and it would add an invisible fourth
  input to a result the cache key claims `(workflow_id, source_revision, agent_config_hash)` determines,
  making two tenants analysed in different orders produce different graphs. The cost is stated rather
  than hidden: **HEROS does not learn across analyses**, and a repository analysed twice starts cold both
  times. Availability follows the same host-service rule as the harness — `none`, `scratchpad` and
  `entity-memory` need nothing; `summary-buffer` needs a summarizer, which is a model call and so a
  second spend line; `vector-recall` needs an embedder and a pinned `embedding_ref`, without which the
  runtime refuses because recall is only reproducible against a pinned embedding.

- **🔴 A harness strategy the runner cannot serve is refused at selection, not at run.**
  `internal/harnessruntime/host.go` refuses rather than degrading when a host service is absent —
  `react-loop` needs a `ToolInvoker`, `plan-execute` a `Planner`, `critic-loop` a `Critic` — because
  *"a critic-loop without a critic IS reflexion, and running it under critic-loop's `config_hash` would
  report one strategy as another."* Offering all five in a dropdown would let an operator save a
  definition the runner cannot execute, with the failure surfacing later to whoever next triggers an
  analysis. So availability is computed from the services the HEROS runner actually supplies; each
  strategy renders as available or unavailable **with the service it needs**; and 🚫 no substitution is
  offered. In P30 `single-shot` and `reflexion` are available; `critic-loop` becomes available if a
  critic is supplied, which is a second model, a second credential resolution and a second spend line,
  all three made visible rather than hidden behind a dropdown.

- **The model is selected; the credential is bound, never entered.** The model comes from the operator
  model registry (`internal/adminstore/modelregistry.go`). The credential is a **reference** resolved
  through `internal/providergateway`'s configured `Secrets` source. 🔴 **The console has no field to type
  a key into, and never stores, logs or renders a key value.** An unresolvable reference fails closed and
  loudly — HEROS reports `unavailable`, surfaces fall back to rule-derived facts, and no provider call is
  made. It does not degrade to another provider, for the same reason `NewSecretsFromEnv` refuses to.

- **Inference runs on the residue only.** Nodes and node pairs a frontend left without an edge or with an
  `unresolved` field. A fully rule-covered file costs zero tokens, asserted. Every inferred edge carries
  a `kind` from the frozen IR vocabulary and a confidence, and is discarded below a configured floor;
  an inference below the floor is recorded as an **abstention with its reason**, not dropped silently.

- **HEROS output is first-class IR, with two fences.** 🔴 It may not emit an edge between two nodes where
  a frontend already emitted one, and may not delete one — rule-derived topology is immutable to HEROS.
  And every edge, node field and label in a stored IR records `provenance ∈ {frontend, detector, heros,
  operator}`; a `heros` provenance additionally records the agent `config_hash`, the `source_revision`,
  the confidence and the inference id. Provenance is on the **fact**, not on the run, because "who
  authored this edge" is the question an incident asks. **Breaking** for the IR storage shape;
  additive and down-migratable, with pre-migration IRs marked `legacy` rather than back-filled with a
  guess.

- **Determinism comes from pinning, not from the model.** An inference is keyed by `(workflow_id,
  source_revision, agent_config_hash)` and stored; a second request with that key returns the stored
  result and makes zero provider calls. Re-inference is an explicit act and its result is shown as a
  **diff** before it replaces anything. 🔴 The system does not claim byte-identical model output — seed
  and temperature are recorded because they explain a result, not because they guarantee one.

- **Two placements, one definition, `disabled` by default.** `platform | customer | disabled` per tenant.
  Platform-side reads the snapshot the customer already pushed, spending **the platform's** credential;
  customer-side runs on the customer's machine with their key and arrives through P29's structure ingest.
  🔴 The platform accepts and stores **no customer provider key under any placement** — P29's promise is
  carried forward unweakened rather than qualified. Defaulting to `disabled` means no existing tenant's
  data-handling posture changes when this phase deploys, and nothing fills until an operator enables it.
  Both placements share one `config_hash`, and CI asserts they produce the same **edge set** on a fixture
  — narrative prose is not byte-compared.

- **A composition, not a workflow label.** `internal/patternclassifier`'s contract deliberately refuses
  one label per workflow, because the label *is* the metric-set dispatcher and a graph with a router in
  one region and a RAG pipeline in another needs two. So the graph view gains a **composition**: each
  region pattern present, the node count it covers, the unlabelled remainder, per-pattern provenance, and
  a one-paragraph HEROS narrative marked as assessed. 🔴 It is never read as a metric-set selector. This
  is narrower than the request as phrased and the difference is flagged for sign-off.

- **HEROS produces or assesses; it never asserts a number nobody measured.** Every output is typed
  `produced` or `assessed`. HEROS may not emit a quality, cost, coverage or spend figure; where a
  measurement is absent it says so. It may not score a case, rank a variant, or mark a proposal verified.

- **Operator control is real.** The durable kill switch gates inference fleet-wide. Spend is metered per
  tenant per inference; per-tenant and fleet caps are enforced **before** the call, and reaching one is
  an event. `/readyz` reports HEROS's resolved state — `disabled`, `ready`, `credential_unresolved`,
  `capped`.

- **The generate action becomes reachable.** Published at the customer edge as the flat path
  `/api/v1/proposal-generations` with identifiers in the body, following P29's established shape. 🚫 No
  `Prefix` rule under `/api/v1/workflows/` is added.

## Impact

- **Affected capabilities:** `heros-agent-definition` (new), `operator-agent-authoring` (new),
  `chain-inference` (new), `inference-provenance` (new), `agent-runtime-placement` (new),
  `graph-composition-summary` (new), `eval-set-visibility` (new), `proposal-generation-reach` (new),
  `operator-agent-control` (new).
  Modified behaviour in `run-linking` (structure payload carries provenance) and `model-selection`
  (the operator registry gains an agent-model binding).
- **Affected code/systems:** `internal/discovery` (residue diagnostics), new `internal/herosagent`
  (runner, residue selection, output validation, cache), `internal/patternclassifier` (composition,
  accepting HEROS region proposals through the existing partitioner), `internal/evalboard` +
  `internal/evalgen` (case listing), `internal/hostedproposals` + `internal/api/proposalgen.go` (state
  pass-through, flat path), `internal/adminstore` (agent versions, spend, caps),
  `internal/providergateway` (HEROS as a caller; no new secret mechanism), `internal/launch`
  (capability mounting, `/readyz`), `internal/runlink` (customer-side result transport),
  `internal/registry` + `internal/skillindex` + `internal/toolindex` (read paths for the axis editors;
  no vocabulary changes), `internal/harnessruntime` (host-service availability exposed for FR1.7),
  `web/console` (graph, board, eval set, proposals), `web/admin-console` (agent overview + six axis
  editors),
  `deploy/k8s/overlays/prod/ingress.yaml`, migrations 0044–0047.
- **Dependencies:** P1 (frontends and their diagnostics define the residue), P2 (registries,
  `confighash`), P3.5 (taxonomy, partitioner, precedence), P4 (`evalgen.CoverageReport`), P5.5
  (`proposalgen`'s five states), P13–P18 (the six axes), P26 (operator console, kill switch, model
  registry), P29 (flat-path edge pattern, structure ingest, enumeration). **Unblocks:** P6's optimizer
  gains a hosted input on non-Go tenants; the P7 metering read model gains a second spend source.
- **Not in scope, deliberately:** hosted execution of a customer workflow (P25's refusal is unchanged —
  HEROS reads source and reports, it does not run the customer's agent); hosted eval or failure
  attribution from a linked run (the cases and traces do not cross the boundary); rewriting the
  syntactic frontends into real parsers (correct, larger, and it does not subsume HEROS — a parser still
  cannot infer intent); HEROS proposing prompt rewrites from diagnosis (needs failing cases the platform
  is never given); autonomous merge of anything HEROS produces (automation stays Advisory — a proposal
  opens a **draft** PR).
- **Decided before implementation** (PRD §14, damon): platform-side HEROS spends the **platform's**
  credential and the platform stores no customer provider key under any placement; the default placement
  is **`disabled`**. Both were the design's assumptions and are now normative, in
  `heros-agent-definition` and `agent-runtime-placement`.
