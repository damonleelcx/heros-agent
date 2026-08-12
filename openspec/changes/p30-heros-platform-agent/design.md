# P30 design — HEROS

## Context

`internal/discovery` produces an IR from a pinned source revision. Six language frontends feed it;
one of them (`frontend_go.go`) emits edges. The other five are syntactic: they enumerate call sites by
pattern matching and cannot follow a value across a statement, let alone a module. So for every non-Go
repository the IR is a node list, and everything keyed off topology — the layout, seven of the eight
pattern detectors, the metric-set dispatch, cost attribution, and `proposalgen`'s admissibility checks —
degrades to its zero case simultaneously.

The platform already contains the machinery to describe an agent precisely: six axes with registries,
sealed params, and a content-addressed `config_hash`. P30 uses that machinery on the platform's own
agent rather than inventing a second one.

Everything below is a decision with an alternative that was actually considered.

---

## D1 — HEROS's definition is a Variant Spec

**Decision.** The agent is defined by a Variant Spec over the six axes, resolved against the P2
registries, identified by `internal/confighash`. Editing publishes a new immutable version.

**Alternative rejected.** A `heros_config` table with typed columns for prompt, model, temperature,
tool list. It is a week's less work and it forks the meaning of "harness strategy" permanently: two
vocabularies for one concept, drifting from the first release. The arbitration ladder puts
*inextensible design* (5) far above *implementation cost* (8), and the prohibition on splitting a source
of truth is not conditional.

**Consequence worth stating.** HEROS's definition is an ordinary spec, so the platform's own eval
harness can measure it. That is what makes the rehearsal gate (D7) buildable rather than aspirational.

**Not decided here.** Whether HEROS's definition is visible to customers. It is operator-only in P30;
nothing forecloses exposing it later.

---

## D2 — Determinism by content-addressed pinning

**Decision.** `inference_key = (workflow_id, source_revision, agent_config_hash)`. First request infers
and stores; every later request reads. Re-inference is explicit and diffed before it replaces.

**Alternative rejected.** Temperature 0 and a fixed seed, described as reproducible. It is not stable
across provider-side model revisions, and its failure is silent — a customer sees a different graph on
Tuesday and there is nothing in the record that says why.

**What this makes true.** "The same revision always shows you the same graph" is a property of the
store, provable by a test that counts provider calls. It survives changing vendors.

**What this deliberately does not claim.** Byte-identical model output. `patternclassifier`'s
`FallbackConfig` records seed and temperature "so a stored label can always be traced back" — *traced*,
not *reproduced*. P30 states the distinction rather than letting a reader infer determinism from the
presence of a seed field.

---

## D3 — First-class output, bounded by two fences

**Decision.** HEROS writes edges and labels into the IR as first-class facts, subject to:

1. It may not emit an edge between two nodes where a frontend already emitted one, and may not delete
   one. Rule-derived topology is immutable to HEROS.
2. Every fact it writes records provenance (D4).

**Why the fences and not candidate-only rendering.** Candidate-only was the safer default and was
explicitly overruled at sign-off (PRD §14 Q0): it leaves every non-Go surface as sparse as it is today,
which is the problem being solved. The fences preserve what candidate-only was protecting — no currently
correct graph can be made worse — while letting the residue actually fill.

**Why the residue is the right scope.** The requested capability is *filling what the parsers missed*.
Restricting HEROS to it costs nothing the operator asked for, makes cost proportional to the gap, and
turns "HEROS did not break the Go path" into a byte-comparison rather than an argument.

**Risk accepted, not mitigated away.** A confident wrong edge in the residue still misleads eval scope,
proposals and cost attribution. The confidence floor, abstention, per-fixture rehearsal and per-tenant
disable reduce it; nothing eliminates it. That is recorded as R1.

---

## D4 — Provenance on the fact, not the run

**Decision.** `provenance ∈ {frontend, detector, heros, operator}` on every edge, node field and label.
A `heros` value carries `agent_config_hash`, `source_revision`, confidence, inference id.

**Alternative rejected.** A run-level `contains_inferred_facts` boolean. It cannot answer "who authored
*this* edge", which is the only question an incident asks, and it makes FR4.4 (a count that mixes
sources reports both parts) unimplementable.

**Enum, not boolean, deliberately.** `operator` is reserved and unused in P30. A fourth author is
foreseeable — a human correcting an inferred edge is the obvious next request — and `is_inferred: bool`
would have to be replaced rather than extended.

**Migration stance.** Additive and nullable. Pre-migration IRs read as `legacy`, **not** back-filled to
`frontend`. Back-filling would assert something about rows nobody examined; `legacy` is the honest value
and is distinguishable in a query.

---

## D5 — The credential is bound, never entered

**Decision.** Model from `internal/adminstore` model registry. Credential as a provider reference
resolved by `providergateway`'s configured `Secrets` source (`env` or `aws-secrets-manager`). No key
value is accepted, stored, logged or rendered. An unresolvable reference fails closed: `unavailable`,
zero provider calls, surfaces fall back to rule-derived facts.

**Alternative rejected.** A masked console input storing an encrypted value. It puts plaintext keys in
request bodies and audit trails and duplicates a secret store the deployment already runs. Level 1 on
the ladder is not tradeable against the convenience of a text field.

**Why fail-closed rather than fall back.** `NewSecretsFromEnv` already refuses to degrade on an
unrecognised source, on the grounds that a deployment believing it is on a secrets manager and is not is
worse than one that will not start. An unresolvable *reference* is the same failure one layer down and
gets the same answer.

**Honest naming.** This answers "set the API key" as *bind the key*, not *type the key*. It is narrower
than the words used and is flagged as such in the PRD.

---

## D6 — Two placements, one definition

**Decision.** Per-tenant `platform | customer | disabled`. One Variant Spec, one `config_hash`, two
hosts. Customer-side results enter through P29's structure ingest carrying provenance and are subject to
the same confidence floor. CI asserts both placements produce the same **edge set** on a fixture.

**Alternative rejected.** Platform-only. Simplest, and unsellable to any customer whose source may not
leave their network — which is a significant fraction of the private-deployment market this product
targets.

**The failure mode being designed against.** Two runners with one prompt is the classic shape that
produces train/serve skew: the context each assembles diverges, and the divergence is invisible because
both "work". Hence one context-assembly code path and the parity assertion. Narrative prose is not
byte-compared — asserting that a model phrases a paragraph identically on two hosts would be a test that
fails for the wrong reason.

---

## D7 — Activation is gated by a rehearsal, and the gate reads per-fixture

**Decision.** A published definition is inactive until it runs against the pinned fixture repositories
and meets the floor **on every fixture individually**, not on the mean.

**Why per-fixture.** Precision/recall over a fixture set is exactly the aggregate that hides a
per-repository catastrophe: an agent that is excellent on four languages and connects everything it sees
on the fifth passes a mean and ships a disaster to one language's customers. The mean is reported; the
gate reads the minimum.

**Fixture design.** Per language: at least one repository whose true graph is known, plus near-misses —
a linear chain that is not a router, a fan-out with no merge, two calls in one file with no data
dependency between them. The near-misses are the evidence the agent *discriminates* rather than
connecting whatever is nearby, and an agent that emits a complete graph over a repository with no
dependencies scores zero precision on a case that exists for that purpose.

**Ground truth from Go.** The Go frontend's real edge output is the only ground truth the platform
already owns, which is why Go fixtures matter even though Go is the language that needs HEROS least.

---

## D8 — Repository content is data, never instruction

**Decision.** HEROS's output is validated against closed vocabularies before it is stored: edge `kind ∈
{data, control}`, labels from the 20-pattern taxonomy, node ids that must already exist in the IR. An
out-of-vocabulary output is **rejected**, not repaired.

**Why this is a design decision and not a detail.** The input is a customer's repository, which can
contain text addressed to a model. The defence is not a prompt instruction to ignore it — that is a
mitigation with no failure signal. The defence is that the only thing HEROS can express is a graph over
nodes that already exist, so the worst an injected instruction achieves is a wrong edge, which the
confidence floor, the fences and provenance already contain.

**Rejected, not repaired.** A validator that coerces a near-miss into the nearest legal value would turn
a detectable failure into an undetectable one.

---

## D9 — The composition is descriptive and never dispatches

**Decision.** The graph view gains a composition — patterns present, node coverage per pattern,
unlabelled remainder, provenance, and a one-paragraph narrative. It is not a workflow-level label and no
code path consumes it to select a metric set.

**Why not the workflow label that was asked for.** `patternclassifier`'s package doc refuses it, and the
refusal is load-bearing: the region label *is* the dispatcher for metric-set, failure taxonomy and
improvement operators. One label over a graph containing both a router and a RAG pipeline picks one
metric set for a workflow that needs two, and the wrongness is invisible.

**How the gap is closed.** The operator's actual question is "what *is* this workflow?" — which the
composition answers, in more detail than a single label would. The difference between the request as
phrased and the design as built is flagged at sign-off rather than resolved silently.

---

## D10 — Surface honesty ships first and stands alone

**Decision.** The zero-edge statement, the `llm_calls` copy fix, the eval-case list and the proposal
state pass-through are the first workstream and depend on nothing in HEROS.

**Why.** They are correct whether or not an agent is ever configured, they are level-2 correctness
(a surface currently asserts "fully rule-covered" over a graph with zero labels), and gating a true
statement behind an AI feature would be the wrong trade in both directions.

---

---

## D11 — The harness vocabulary available to HEROS is bounded by the services the runner supplies

**The constraint.** `internal/harnessruntime/host.go` refuses rather than degrading when a strategy's
host service is absent. Three of the five builtin strategies need one: `react-loop` a `ToolInvoker`,
`plan-execute` a `Planner`, `critic-loop` a `Critic`. The refusal is deliberate and its comment says why
— *"a critic-loop without a critic IS reflexion, and running it under critic-loop's `config_hash` would
report one strategy as another."*

**Why this is a design decision and not an implementation note.** "Manage the harness on the admin
console" reads as "offer the five strategies". Offering all five would mean an operator can save a
definition that the HEROS runner cannot execute, and the failure surfaces later, to whoever next triggers
an analysis, as a refusal naming a service they did not know was needed. The save succeeded; the agent is
broken; nothing in between said so.

**Decision.** The console computes availability from the services the HEROS runner actually supplies and
refuses the **selection**, naming the missing service. Each strategy is shown as available or unavailable
with what it would need. 🚫 No substitution is offered — the whole point of the runtime's refusal is that
the neighbouring strategy is a *different* strategy.

**Which are available in P30.** `single-shot` and `reflexion` need no host service and are available.
`critic-loop` becomes available if the runner is given a critic — which is a second model, a second
credential resolution and a second spend line, so FR1.9 makes all three visible rather than letting a
dropdown quietly double the cost of an analysis. `react-loop` and `plan-execute` need a tool executor and
a planner that the platform-side runner does not have in this phase; they are shown unavailable with the
reason rather than hidden, because a hidden option is indistinguishable from one that does not exist.

**Alternative rejected.** Validate at run and report the refusal on the analysis. It is less work and it
moves the discovery of a configuration error from the person who made it to the person who did not.

---

## D13 — HEROS's memory is scoped to one inference, and its session id is the inference id

Keeping memory as a managed axis (damon, this session) forces two decisions the rest of this design had
not made. Both are load-bearing and neither is obvious from "the operator can pick a memory strategy".

**The mechanism.** `memoryruntime.Key` is `{NodeID, SessionID}` and the runtime **never invents a
session id** — its own comment says a defaulted one *"silently merges conversations that should be
separate"*. So whatever HEROS supplies as the session id decides the blast radius. `Host` carries the
same shape of optional service as the harness runtime: `summary-buffer` needs a `Summarizer` or returns
`ErrNoSummarizer`, `vector-recall` needs an `Embedder` or returns `ErrNoEmbedder`, and both refuse rather
than degrading — *"a summary-buffer that quietly truncates IS scratchpad"*.

**Problem 1 — a platform agent with persistent memory reads every customer's source.** If the session id
spans inferences, content from one tenant's repository can surface in another tenant's analysis. There is
no benign version of that. It is level 1 on the ladder and it arrives through a dropdown.

**Problem 2 — persistent memory would make D2's cache key a lie.** D2 pins an inference to
`(workflow_id, source_revision, agent_config_hash)` on the claim that those three determine the result.
Memory carried between inferences adds a fourth, invisible input: *what HEROS happened to analyse
first*. Two tenants analysed in different orders would get different graphs, the stored result would no
longer be a function of its own key, and re-inference would diff against something the key cannot
explain. Determinism is this phase's centre of gravity and memory across inferences quietly removes it.

**Decision.** 🔴 `SessionID` is the **inference id**. Memory lives inside one analysis — turn to turn
within the harness loop, which is what memory strategies are actually for — and is discarded when the
inference completes. It never spans inferences, workflows or tenants.

**What this preserves.** Cross-tenant leakage becomes structurally impossible rather than
policy-prevented: there is no key under which two tenants' entries can meet. And D2's three-part key
stays honest, because nothing outside it enters the computation.

**What this costs, honestly.** HEROS cannot learn across analyses. A repository analysed twice starts
cold both times. That is a real capability given up, and it is the right trade — the alternative buys
learning with a cross-tenant surface and a false determinism claim, which is levels 1 and 2 spent on
level 5.

**Availability, computed like the harness axis.** `none`, `scratchpad` and `entity-memory` need no host
service and are available. `summary-buffer` needs a summarizer, which is a **model call** — a second
spend line, surfaced like the critic's. `vector-recall` needs an embedder and a pinned `embedding_ref`
(the runtime refuses without one, because *"recall is only reproducible against a pinned embedding"* —
the same reasoning as D2, arrived at independently by that package).

---

## D12 — No axis is a text box

**Decision.** Every axis binds to the vocabulary it already has: the prompt to a parsed template with
derived slots, skills to registered entries with hermetically compiled schemas, tools to the index with
scope and risk tier and approval, context and memory and harness to their named sets with params
validated against the declared `ParamsSchema` **at save**.

**Why.** A free-text field for a value with a closed vocabulary eventually holds a value nothing can
interpret, and the closed sets exist precisely so a stored `config_hash` still means something months
later. Validating at save rather than at run is the same argument as D11 in miniature: a malformed
strategy discovered when a run reaches it is discovered by the wrong person at the wrong time.

**Two constraints that fall out and are worth naming.** A skill schema carrying a remote `$ref` is
rejected rather than fetched — the registry is already hermetic and the console must not become the hole
in it. And 🚫 a tool declaring outbound network access is not bindable at all: HEROS reads a pinned
snapshot, and a tool reaching the network from inside the analysis loop would be an egress surface
created by a dropdown, which is the kind of thing the two-lane egress rule exists to prevent.

---

## Data model sketch

```
heros_agent_version                        -- immutable, one row per published definition
  config_hash          TEXT PK             -- internal/confighash over the resolved spec
  spec_json            TEXT NOT NULL       -- the Variant Spec, canonical form
  model_ref            TEXT NOT NULL       -- FK-by-value into the operator model registry
  credential_ref       TEXT NOT NULL       -- a PROVIDER NAME. never a key value.
  rehearsal_state      TEXT NOT NULL       -- pending | passed | failed
  rehearsal_report     TEXT                -- per-fixture precision/recall; null while pending
  activated_at_ms      INTEGER             -- null unless active; at most one active row
  created_at_ms        INTEGER NOT NULL

heros_inference                            -- the pinned result. D2's whole guarantee.
  inference_id         TEXT PK
  workflow_id          TEXT NOT NULL
  source_revision      TEXT NOT NULL
  agent_config_hash    TEXT NOT NULL
  placement            TEXT NOT NULL       -- platform | customer
  edges_json           TEXT NOT NULL
  labels_json          TEXT NOT NULL
  narrative            TEXT
  tokens_in            INTEGER NOT NULL
  tokens_out           INTEGER NOT NULL
  created_at_ms        INTEGER NOT NULL
  UNIQUE (workflow_id, source_revision, agent_config_hash)   -- the idempotency fence

heros_abstention                           -- FR3.4: not knowing is an output
  inference_id         TEXT NOT NULL
  subject              TEXT NOT NULL       -- node id, or "a→b"
  reason               TEXT NOT NULL       -- from a closed enum, not prose
  confidence           REAL

heros_spend                                -- per tenant per inference; caps read this
  tenant_id, inference_id, tokens_in, tokens_out, estimated_cost, priced BOOLEAN

-- altered: IR storage gains provenance per fact.
--   provenance TEXT NULL   -- frontend | detector | heros | operator; NULL reads as `legacy`
```

`UNIQUE (workflow_id, source_revision, agent_config_hash)` is the idempotency fence, and its test is a
**concurrent** double-submit against real Postgres. A unique index is invisible to a test that never
contends — a lesson this codebase has already paid for once.

Timestamps are `int64` milliseconds, both dialects, per the standing rule.

## Interfaces

```go
// Runner performs one inference. It is the ONLY thing in the package that reaches a provider.
type Runner interface {
    Infer(ctx context.Context, in Input) (Result, error)
}

// Input is the residue and nothing else. A caller cannot ask for a whole-repository pass:
// there is no field for it, which is how NFR1 stays true by construction rather than by review.
type Input struct {
    WorkflowID     string
    SourceRevision string
    RuleIR         discovery.IR      // what the frontends already established
    Residue        Residue           // nodes/pairs with no edge or an `unresolved` field
    Budget         Budget            // tokens and wall-clock; exceeded => abort, recorded
}

// Result separates what was produced from what was declined. Both are stored.
type Result struct {
    Edges       []ProvenancedEdge
    Labels      []patternclassifier.RegionProposal
    Abstentions []Abstention
    Narrative   string             // assessed, never measured
    Usage       providercall.Usage
}
```

`Labels` is `patternclassifier.RegionProposal` — the existing type — so HEROS proposals enter through
the same partitioner and the same precedence rule as every detector's. There is no second arbitration
path, which is what keeps "an LLM label never overrides a rule label" true by construction rather than
by a second implementation of the same rule.

## Risks carried into implementation

| Risk | Where it is held |
|---|---|
| A confident wrong edge misleads downstream consumers | R1 — accepted at sign-off; floor + abstention + rehearsal + per-tenant disable |
| Platform placement reads customer source with a platform credential | R3 — off by default, per tenant, disclosed; `customer` placement exists for tenants who refuse |
| Injection from repository content | D8 — closed output vocabulary, rejection not repair |
| Two runners drift | D6 — one definition, parity asserted in CI |
| Cost scales with repository rather than with value | Residue-only input type, per-run budget, per-tenant and fleet caps enforced pre-call |

## Decided

**Q1 — platform-side HEROS spends the platform's credential.** Decided by damon. The platform holds no
customer provider key under any placement, so P29's promise carries forward unweakened rather than
qualified — which matters because a qualified promise is the kind that gets quoted back without its
qualifier. A customer who wants their own key spent chooses placement `customer`, where the key never
leaves their machine. This is why D6's two placements are a security feature and not only a deployment
preference.

**Q2 — the default placement is `disabled`.** Decided by damon. Consequence accepted: deploying this
phase changes nothing for any existing tenant, no surface fills on its own, and the `openclaw` acceptance
run needs an explicit enablement step written into it. The alternative would have had the platform read
customer source under a platform-held credential without anyone acting, which is exactly the posture the
per-tenant switch exists to make deliberate.

## Open, not blocking

**Q4** — The per-language activation floor needs numbers. Proposed starting point: precision ≥ 0.90,
recall ≥ 0.70 per fixture — asymmetric because a missing edge degrades to today's behaviour while a
wrong edge actively misleads. The number must come from measurement before activation, not before build.

**Q3 / Q5 / Q6** — Customer visibility of the `inferred` mark (assumed yes), retention after a tenant
disables HEROS (assumed retained-and-stale), and whether HEROS assesses the variants surface at all
(assumed thinly, and droppable). None gates the build; each is recorded in PRD §14.
