# PRD — P30: HEROS, the Platform Agent

| | |
|---|---|
| **Phase** | P30 |
| **OpenSpec change** | [`p30-heros-platform-agent`](../../openspec/changes/p30-heros-platform-agent/) |
| **Lead roles** | AI Engineer + Backend Dev |
| **Support roles** | Product Designer, System Designer, Frontend Dev, DevOps, QA, Sales Operations |
| **Upstream** | P1 (discovery frontends) · P2 (registries, config_hash) · P3.5 (pattern classifier) · P4 (eval harness) · P5.5 (proposals) · P13–P18 (the six axes) · P26 (operator console) · P29 (platform edge reach, linked-run fan-out) |
| **Unblocks** | A non-Go repository having any classified graph at all · P6's optimizer having an input on hosted tenants |
| **Status** | Proposed — awaiting sign-off on §14 |

---

## 1. Summary

An operator opened `/app/workflows/openclaw` on the production console and reported four things:
the Structure drawing is an unordered stack, no overall pattern is reported, the eval set's scenarios
are nowhere, and there are no proposals.

Three of those four are the same defect seen from three angles, and it is not a rendering defect.
**`openclaw` has 22 nodes and 0 edges, because no discovery frontend except Go has ever emitted an
edge.** Everything downstream of the graph is a function of the graph, so everything downstream is
empty — and each surface reports its emptiness in a way that reads like a finding rather than like a
missing input.

The fourth — eval-set scenarios — is a genuinely absent surface: `n_cases` is rendered as a count in
three places and the cases themselves are listed in none.

P30 introduces **HEROS**, the platform's own agent, and gives it one job with two halves:

- **Produce the graph the static frontends cannot.** A regex frontend cannot follow a value across a
  module boundary in TypeScript. A model reading the repository can. HEROS emits edges and labels for
  source the syntactic frontends could only enumerate.
- **Say whether each surface's answer is supported.** Structure, composition, eval set, variants, cost
  and quality, coverage, spend, proposals — HEROS reads what the platform is about to show and reports
  whether the evidence behind it exists, is stale, or is absent.

HEROS is configured entirely from the operator console — prompt, model, credential reference, skills,
tools, context, memory, harness — and it is configured **through the same six-axis vocabulary the
product already sells to customers**. HEROS is not a second configuration system bolted to the side of
the platform. It is a Variant Spec, resolved against the P2 registries, sealed to a `config_hash`. The
platform optimizes agentic workflows for a living; its own agent is one of them.

The design's centre of gravity is a single refusal: **HEROS's determinism does not come from the
model.** A language model is not reproducible, and a graph that changes under a customer between two
page loads is worse than no graph. So an inference runs **once** per `(source_revision, agent
config_hash)`, the result is content-addressed and pinned, and re-running is an explicit operator act
whose output is shown as a diff. Everything else in this phase depends on that being true.

---

## 2. Problem & context

### 2.1 What the operator saw, verified in the tree

| # | Observation | Surface | Verified cause |
|---|---|---|---|
| 1 | "the structure should try to fit into an ordered view" | `/app/workflows/openclaw/graph` | `layer`/`order` are derived from edges ([`graphview.go:131`](../../internal/patternclassifier/graphview.go)). Zero edges ⇒ every node is layer 0 ⇒ a vertical stack. There is no order to draw. |
| 2 | "should find overall pattern from all the nodes combined" | same | 7 of 8 detectors are topology predicates; the 8th needs registry-bound skills. With 0 edges and 0 bound skills, **0/22 can fire by construction**. Separately, the classifier's contract deliberately refuses one label per workflow — see §2.6. |
| 3 | "i don't see eval set scenarios used" | `/app/workflows/openclaw/board` | `n_cases` is rendered as an integer in three files and nowhere as a list. No route enumerates an eval set. |
| 4 | "i don't see any proposals" | `/app/workflows/openclaw/proposals` | **Three independent failures stacked.** See §2.5. |

### 2.2 Root cause: no frontend except Go emits an edge

`internal/discovery` ships six language frontends. Exactly one appends edges:

```
internal/discovery/frontend_go.go:65    res.Edges = append(res.Edges, g.Edges...)
```

`frontend_tsjs.go`, `frontend_python.go`, `frontend_rust.go`, `frontend_java.go` and
`frontend_kotlin.go` contain zero occurrences of `Edges`. `BuildGraph` — the only producer — takes
`*ParsedFile` carrying a `go/ast` tree, and `dataEdges` walks `*ast.AssignStmt`. It is a Go-typed
function; the other frontends have nothing to hand it.

The corroborating signal is on the screen already. Every one of `openclaw`'s 22 nodes reads
`openai/unresolved`, and `internal/runlink/workflowir.go:82` defines `unresolved` as *the model a node
calls when a syntactic frontend could not follow it*. Both symptoms have one author.

This is not a bug in the sense of a wrong line. `dataEdges`'s own comment states the boundary honestly
— *"Edge inference is best-effort and intra-procedural; what static analysis cannot see is simply not
an edge (it is never guessed)."* That was the right call for a syntactic frontend. The defect is that
**nothing was ever built to cover the residue**, and every surface downstream presents the residue as a
result.

### 2.3 Why "0 LLM calls" currently renders as coverage

The graph header reports:

> **LLM FALLBACK CALLS · 0** — *Fully rule-covered — no model was consulted*

`view.llm_calls === 0` is being read as *the rules covered everything*. On `openclaw` it means *the
rules covered nothing and the fallback did not run either*. The two states are opposite and the copy
cannot tell them apart. A surface that asserts full coverage over a graph with zero labels is the
single most misleading thing in this phase's blast radius, and it is four lines of TSX.

### 2.4 Eval-set scenarios: counted everywhere, listed nowhere

`n_cases` appears in `board/page.tsx`, `board/leaderboard.tsx` and `variants/[id]/scorecard/page.tsx`
— always as an integer. `CoverageView` ([`evalboard/view.go:214`](../../internal/evalboard/view.go))
already models the interesting properties: `OracleCoverage`, `NIndecisive` (*cases carrying an oracle
that can never fail — the most misleading cases in the set*), and the vacuous-dimension list. None of
it reaches a screen that shows **which** cases.

So the board can say `0.801 ± [0.744, 0.852], n=5 seeds · 8 cases` and a reader cannot answer the only
question that matters: *8 cases of what?* On `openclaw` the board additionally reports *"Coverage was
not measured for this eval set"*, which is honest and, without a case list, unactionable.

### 2.5 Proposals: three stacked failures, any one of which is sufficient

1. **The generation pass has no trigger.** `POST /api/v1/workflows/{workflow_id}/proposals/generate` is
   mounted in [`internal/api/proposalgen.go`](../../internal/api/proposalgen.go). Nothing in
   `web/console` calls it. There is no button.
2. **It is not reachable from the customer edge anyway.** `deploy/k8s/overlays/prod/ingress.yaml`
   publishes eleven `Exact` paths. `proposals/generate` is not one of them, and P29's proposal names it
   explicitly as a route a `Prefix: /api/v1/workflows/` rule would wrongly expose. So even a button
   would 404.
3. **The surface discards the reason.** `proposalgen` returns a closed `State` with five distinct
   "nothing to propose" answers — `no_linked_runs`, `no_per_node_metrics`, `no_discovered_graph`,
   `no_model_menu`, `no_bottleneck` — each with a different next action. But the console reads
   `hostedproposals`, which reads the proposal **store**, and
   [`hostedproposals.go:104`](../../internal/hostedproposals/hostedproposals.go) sets `State = "empty"`
   whenever the store has no rows. The reason is computed, documented, and thrown away.

Had a pass ever run on `openclaw`, it would have returned `no_discovered_graph` — which is §2.2 again.

### 2.6 The gap in "find overall pattern from all the nodes combined"

Taken literally, this asks for one label per workflow, and `internal/patternclassifier`'s package doc
refuses exactly that, for a stated reason:

> *the output is a set of per-subgraph labels and never one label for the whole workflow: a workflow
> with a router in one region and a RAG pipeline in another needs two DIFFERENT metric-sets.*

That refusal is load-bearing — the label **is** the dispatcher that selects which metric-set, failure
taxonomy and improvement operators are in scope. Collapsing 22 nodes to "this is a RAG workflow" would
pick one metric-set for a graph that needs several, and the wrongness would be invisible.

**So P30 does not add a workflow label. It adds a workflow *composition*** — the multiset of region
patterns, what fraction of nodes each covers, what is left unlabelled, and HEROS's plain-language read
of how the regions compose. That answers the operator's actual question ("what *is* this workflow?")
without breaking the dispatcher. The distinction is recorded here because the requirement as phrased
and the requirement as buildable are not the same, and the difference should be visible at sign-off.

### 2.7 Why now

Three reasons, in priority order under the arbitration ladder:

1. **A shipped surface is currently asserting something false** (§2.3). That is a level-2 stability
   defect in the sense that matters commercially: a confidently wrong health signal is worse than an
   absent one.
2. **The product's addressable market is presently Go.** Every non-Go customer receives a node list and
   nine surfaces that cannot fill. P29 built the bridge from the CLI to the console; this phase is why
   the bridge carries so little for most repositories.
3. **P6's optimizer has no hosted input.** It proposes against a classified graph. No hosted non-Go
   tenant has one.

---

## 3. Goals & non-goals

### Goals

1. **G1** — A repository in any language supported by a discovery frontend gets a graph with edges, or
   a named reason why not.
2. **G2** — Every workflow surface states whether its answer rests on measured evidence, on HEROS
   inference, or on nothing — and never renders the third as either of the first two.
3. **G3** — HEROS's prompt, model, credential reference, skills, tools, context, memory and harness are
   editable in the operator console, versioned, diffable, and sealed to a `config_hash`.
4. **G4** — HEROS is defined in the platform's **existing** six-axis vocabulary. No second config
   system, no second registry, no second hashing scheme.
5. **G5** — The same graph is shown on two consecutive loads of the same page for the same
   `source_revision`. Determinism by pinning, not by hoping the model repeats itself.
6. **G6** — An eval set's cases are listable, and each case says what it asserts and whether its oracle
   can fail.
7. **G7** — The proposals surface names why it is empty, and a pass can be triggered from the console
   through a published edge path.

### Non-goals (with the phase that owns them)

| Not in scope | Why / who owns it |
|---|---|
| Hosted **execution** of a customer workflow | P25's standing refusal is unchanged. HEROS reads source and reports; it does not run the customer's agent. |
| The platform holding a **customer's** provider key | Reverses a shipped promise (P29). Raised as **Q1** in §14 — not silently adopted. |
| Hosted eval, or failure attribution from a linked run | The eval cases and traces stay on the customer's machine by design (`internal/runlink`). |
| Rewriting the syntactic frontends into real parsers | Correct, far larger, and does not subsume HEROS — a parser still cannot infer intent. Backlog. |
| HEROS proposing prompt rewrites from diagnosis | Needs failing cases the platform is never given (`proposalgen`'s own boundary). Unchanged. |
| Autonomous merge of anything HEROS produces | Automation stays Advisory. A proposal opens a **draft** PR; a person merges. |

---

## 4. Users & personas

| Persona | What they need from P30 | Where they meet it |
|---|---|---|
| **Customer developer** (non-Go repo) | To see their workflow's shape at all, and to know which parts are inferred | `/app/workflows/{id}` and its four sub-views |
| **Customer developer** (Go repo) | Nothing to regress: their rule-derived graph must be untouched | Same surfaces; HEROS is additive |
| **Platform operator** (us) | To tune HEROS without a deploy, see what it costs, and stop it | `/axes`, new `/agent` surfaces, `/killswitch` |
| **Platform engineer** | To know which facts in an IR a model authored | `provenance` on every edge and label |
| **Sales** | A defensible sentence about what the graph is and is not | §9.8 |

---

## 5. User stories

- **S1** As a developer with a TypeScript agent, I push my source and see a graph with edges, each
  marked as inferred, instead of a column of 22 unconnected boxes.
- **S2** As a developer, I read a one-paragraph composition summary — *"a retriever chain feeding a
  generator, with a router over three branches; 4 nodes are outside any region"* — instead of 22 cards
  that each say "not yet classified".
- **S3** As a developer, I click through from `8 cases` to the eight cases and see which of them carry
  an oracle that can never fail.
- **S4** As a developer, I open Proposals and read *"no source snapshot has been pushed, so no node
  carries a pattern label"* with the action that fixes it — instead of *"Nothing is pending."*
- **S5** As an operator, I change HEROS's model from the console, see the new `config_hash`, and see
  which tenants' graphs were produced under the old one.
- **S6** As an operator, I see what HEROS spent this month per tenant, and I can cap it.
- **S7** As an operator, I arm the kill switch and HEROS stops inferring fleet-wide; surfaces fall back
  to rule-derived facts and say so.
- **S8** As a platform engineer reviewing an incident, I can tell for any edge in any IR whether a
  parser or a model put it there, under which `config_hash`, at which `source_revision`.

---

## 6. Functional requirements

### 6.0 The surface contract — what HEROS is asked about, and what it may answer

The operator asked for HEROS to "confirm or validate" eight things. Those eight are not one kind of
claim, and conflating them would produce an agent that speaks with equal confidence about a number it
measured and a number nobody measured. The contract is therefore per-surface:

| Surface | HEROS may **produce** | HEROS may **assess** | HEROS may never |
|---|---|---|---|
| Structure | edges, node metadata a syntactic frontend left `unresolved` | whether the drawn graph is connected, acyclic where expected, complete vs. the file set | invent a node that is not a call site |
| Pattern / composition | region proposals for the residue; the composition narrative | whether rule labels and inferred labels disagree | overwrite a rule-derived label (§8.2 D3) |
| Eval sets | a per-case readability summary | whether the set exercises the graph — which nodes no case reaches; which oracles cannot fail | score a case, or author a case's expected output |
| Variants | — | whether two variants differ in a way the eval set can detect | rank variants |
| Cost & quality | — | whether the reported score has an interval, seeds, and a gate | assert a score |
| Coverage | — | whether coverage was measured, and what its absence invalidates | compute a coverage number |
| Spend | — | whether spend is attributable per node | produce a spend figure |
| Proposals | candidate proposals for structural signals | whether an existing proposal's evidence still matches the current revision | mark a proposal verified |

**FR0.1** The system SHALL classify every HEROS output as `produced` or `assessed`, and SHALL render
the two differently.
**FR0.2** HEROS SHALL NOT emit a numeric quality, cost, coverage or spend figure. Where it has no
measurement it SHALL say the measurement is absent.

### 6.1 Agent definition (capability `heros-agent-definition`)

**FR1.1** HEROS's definition SHALL be a Variant Spec over the six axes, resolved against the P2
registries, with a `config_hash` computed by `internal/confighash`.
**FR1.2** The definition SHALL be editable from the operator console for: prompt (P13), model (P13),
skills and tools (P14), context policy (P16), memory strategy (P17), harness strategy (P18). Wiring
(P15) is fixed for HEROS and SHALL be read-only. **What "editable" means per axis — the vocabulary each
binds to and what is validated at save — is §6.2b, and it is the substance of this requirement rather
than a detail of it.**
**FR1.3** Editing SHALL publish a new version. No published definition is mutable — the P2 registry
rule, unchanged.
**FR1.4** The console SHALL show, for the active definition, the `config_hash` and the number of stored
inferences produced under it.
**FR1.5** A definition SHALL NOT become active until it passes a **rehearsal**: a run over the pinned
fixture repositories with the calibration thresholds of §6.3. A failed rehearsal blocks activation and
names the failing fixture.

### 6.2 Model and credential (capability `heros-agent-definition`)

**FR2.1** The model SHALL be selected from the operator model registry
(`internal/adminstore/modelregistry.go`). A model not in the registry SHALL NOT be selectable.
**FR2.2** The credential SHALL be stored and rendered as a **reference** — a provider name resolved
through `internal/providergateway`'s configured `Secrets` source (`env` or `aws-secrets-manager`).
**FR2.3** 🔴 The console SHALL NOT accept, store, log or render a provider key value. There is no field
to type one into. This is the arbitration ladder's level 1 and it is not tradeable against the
convenience of a text input.
**FR2.4** An unresolvable credential reference SHALL fail closed and loudly: HEROS is reported
`unavailable — credential reference <name> did not resolve`, surfaces fall back to rule-derived facts,
and no inference is attempted. It SHALL NOT degrade to a different provider.
**FR2.5** Platform-side HEROS SHALL use the **platform's** credential. Customer-side HEROS SHALL use the
customer's, on the customer's machine. 🔴 The platform SHALL NOT accept or store a customer provider key
value under any placement — P29's promise is carried forward unweakened. Decided, §14 Q1.

### 6.2b Per-axis authoring in the operator console (capability `operator-agent-authoring`)

"Manageable on the admin console" is only meaningful if it says *what* is editable and *against what*.
Every axis already has a vocabulary in this codebase. The console binds to those; **no axis is a text
box**, because a free-text field for a value with a closed vocabulary eventually holds a value nothing
can interpret.

| Axis | What the operator picks | Bound vocabulary | Validated at save against |
|---|---|---|---|
| **Prompt** | template body; slots are derived, not typed | `registry.PromptEntry` — content-addressed version, `Template` with parsed `Slots` | template parses; every bound slot exists |
| **Model** | primary model, and a critic model where the harness needs one | operator model registry (`internal/adminstore`) | membership; deprecation shown, never auto-switched |
| **Credential** | a provider **reference** | `providergateway` `Secrets` (`env` \| `aws-secrets-manager`) | resolves; 🔴 no value field exists |
| **Skills** | which registered skills HEROS may call | `registry.SkillEntry` — `impl_handle`, compiled input/output JSON Schema | schema compiles; 🚫 remote `$ref` rejected, not fetched |
| **Tools** | which indexed tools are bound | `toolindex` — id, tenant scope (`_global` or tenant), description, `risk_tier`, `approved` | approved only; scope displayed; 🚫 no network-reaching tool |
| **Context** | one policy + its params | 7 named policies: `full-history`, `sliding-window`, `summarization`, `rag-retrieval`, `semantic-compaction`, `hierarchical-summary`, `structured-extraction` | that policy's `ParamsSchema` |
| **Memory** | one strategy + its params | 5 named strategies: `none`, `scratchpad`, `summary-buffer`, `vector-recall`, `entity-memory` | that strategy's schema; set version recorded; host service available (FR1.15); 🔴 scope is one inference (FR1.16) |
| **Harness** | one strategy + its params | 5 named strategies: `single-shot`, `react-loop`, `plan-execute`, `reflexion`, `critic-loop` | schema; `max_turns` required for multi-turn and ≤ `MaxTurnsCeiling` (16); retry budget may not multiply past it |
| **Wiring** | — | — | 🚫 fixed for HEROS, read-only |

**FR1.6** Each axis SHALL be edited against the vocabulary above. Free text SHALL NOT be accepted where a
closed vocabulary exists.

**FR1.7** 🔴 **A harness strategy whose host service the HEROS runner cannot supply SHALL be refused at
selection time, naming the service.** `internal/harnessruntime/host.go` refuses rather than degrading
when a service is absent: `react-loop` needs a `ToolInvoker`, `plan-execute` needs a `Planner`,
`critic-loop` needs a `Critic`. Its own comment gives the reason — *"a critic-loop without a critic IS
reflexion, and running it under critic-loop's `config_hash` would report one strategy as another."* A
console that accepts the selection anyway converts a save into a failure discovered by whoever next
triggers an analysis. The editor therefore shows each strategy as available or unavailable **with the
service it needs**, and refuses the save rather than the run.

**FR1.8** 🚫 The console SHALL NOT offer to substitute a neighbouring strategy when one is unavailable.

**FR1.9** Where a critic model is required and available, it SHALL be selected from the operator model
registry, resolve its own credential reference, and have its spend metered and attributed alongside the
primary model's. A second model is a second cost and a second credential, and both are visible.

**FR1.10** Params SHALL be validated at **save** against the declared schema, never at run.

**FR1.11** The definition SHALL record the **set version** of every closed vocabulary it references, so a
stored `config_hash` stays interpretable after a vocabulary is versioned forward.

**FR1.12** Publication SHALL show the resolved diff against the active definition and the resulting
`config_hash` before it happens. An edit resolving to no change SHALL say so and create no version.

**FR1.13** The surface SHALL distinguish **set** from **defaulted** from **not in effect**. An axis value
the runner does not consume for the active placement SHALL be marked as not in effect with the reason —
the "configured but not working" visibility rule, which is where this class of surface usually fails.

**FR1.14** 🚫 A tool whose declared capability includes outbound network access SHALL NOT be bindable, and
the refusal SHALL NOT be overridable from the console. HEROS reads a pinned source snapshot; a tool that
reaches the network from inside the analysis loop would be an egress surface created by a dropdown.

**FR1.15** Memory strategy availability SHALL be computed from the host services the HEROS runner
supplies, on the same rule as FR1.7. `memoryruntime.Host` carries a `Summarizer` and an `Embedder`, and
the runtime refuses rather than degrading — *"a summary-buffer that quietly truncates IS scratchpad"*.
`none`, `scratchpad` and `entity-memory` need neither. `summary-buffer` needs a summarizer, which is a
**model call and therefore a second spend line**, surfaced like the critic model's. `vector-recall` needs
an embedder **and** a pinned `embedding_ref`, without which the runtime refuses because recall is only
reproducible against a pinned embedding.

**FR1.16** 🔴 **HEROS's memory SHALL be scoped to a single inference.** `memoryruntime.Key` is
`{NodeID, SessionID}` and the runtime never invents a session id, so the caller's choice sets the blast
radius. HEROS SHALL supply the **inference id** as the session id, and memory SHALL be discarded when the
inference completes. It SHALL NOT span inferences, workflows or tenants. Two reasons, either sufficient:

- **Cross-tenant.** HEROS reads many customers' repositories. Memory spanning inferences would let one
  tenant's source surface in another tenant's analysis. Level 1, arriving through a dropdown. Scoping to
  the inference makes it structurally impossible rather than policy-prevented — there is no key under
  which two tenants' entries can meet.
- **Determinism.** D2 pins a result to `(workflow_id, source_revision, agent_config_hash)` on the claim
  those three determine it. Persistent memory adds a fourth, invisible input — *what HEROS analysed
  first* — so two tenants analysed in different orders would get different graphs and the cache key would
  no longer explain its own contents.

**FR1.17** The cost SHALL be stated plainly in the console: HEROS does not learn across analyses, and a
repository analysed twice starts cold both times. This is a capability deliberately given up, not a gap.

### 6.3 Chain inference (capability `chain-inference`)

**FR3.1** HEROS SHALL run only on the **residue**: nodes and node pairs a frontend left without an
edge or with an `unresolved` field. A fully rule-covered file SHALL cost zero tokens.
**FR3.2** Every inferred edge SHALL carry `kind ∈ {data, control}` — the frozen IR vocabulary. HEROS
SHALL NOT introduce an edge kind.
**FR3.3** Every inferred edge and label SHALL carry a confidence and SHALL be discarded below a
configured floor. An inference below the floor SHALL be recorded as an abstention with its reason, not
dropped silently.
**FR3.4** HEROS SHALL abstain rather than guess when the evidence is a single unresolved identifier
with no reachable definition. Abstentions are a first-class output.
**FR3.5** Calibration: the fixture set SHALL contain, per supported language, at least one repository
whose true graph is known, plus near-misses. Discriminative power is reported as precision/recall on
edges against those fixtures, and a definition failing the threshold cannot activate (FR1.5).
**FR3.6** 🔴 HEROS SHALL NOT emit an edge between two nodes when a frontend has already emitted one, and
SHALL NOT delete one. Rule-derived topology is immutable to HEROS.

### 6.4 Provenance (capability `inference-provenance`)

**FR4.1** Every edge, node field and label in a stored IR SHALL record `provenance ∈ {frontend,
detector, heros, operator}`.
**FR4.2** A `heros` provenance SHALL additionally record the agent `config_hash`, the `source_revision`
it read, the confidence, and the inference id.
**FR4.3** The absence of provenance SHALL be treated as `frontend` **only** for IRs written before this
migration, and those SHALL be marked `legacy` rather than back-filled with a guess.
**FR4.4** Every surface rendering a HEROS-authored fact SHALL mark it, and the marking SHALL survive
aggregation: a count mixing rule and inferred facts SHALL report both parts.

### 6.5 Determinism and caching (capability `chain-inference`)

**FR5.1** An inference SHALL be keyed by `(workflow_id, source_revision, agent_config_hash)` and stored.
A second request with the same key SHALL return the stored result without calling a model.
**FR5.2** Re-inference SHALL be an explicit operator or customer action, never automatic, and its result
SHALL be presented as a **diff** against the stored one before it replaces it.
**FR5.3** 🔴 The system SHALL NOT claim byte-identical model output. Reproducibility is provided by the
cache key, and the documentation SHALL say so. Sampling settings are recorded because they explain a
result, not because they guarantee one.

### 6.6 Runtime placement (capability `agent-runtime-placement`)

**FR6.1** Placement SHALL be a per-tenant setting with values `platform`, `customer`, `disabled`, and
SHALL default to **`disabled`** (§14 Q2). A tenant is not analysed until an operator enables it, so no
existing tenant's data-handling posture changes when this phase deploys.
**FR6.2** The two placements SHALL share one agent definition and one `config_hash`.
**FR6.3** Customer-side results SHALL enter through the P29 ingest path as a first-class structure
payload carrying provenance, and SHALL be subject to the same confidence floor.
**FR6.4** A tenant set to `customer` SHALL NOT have platform-side inference run for it, and the console
SHALL say which placement produced what it is showing.
**FR6.5** Parity: for a fixture repository, both placements SHALL produce the same **set of edges** at
the same `config_hash`, asserted in CI. Byte-equality of narrative prose is not asserted.

### 6.7 Composition summary (capability `graph-composition-summary`)

**FR7.1** The graph view SHALL report a composition: each region pattern present, the node count it
covers, the unlabelled remainder, and per-pattern provenance.
**FR7.2** HEROS SHALL contribute a narrative of at most one paragraph describing how the regions
compose. It SHALL be marked as assessed, not measured.
**FR7.3** 🔴 The composition SHALL NOT be a single workflow-level pattern label, and SHALL NOT be
consumed as a metric-set selector. §2.6.
**FR7.4** A graph with zero edges SHALL render an explicit statement of that fact and its cause, and
SHALL NOT render a positional drawing implying a structure.
**FR7.5** 🔴 The `llm_calls === 0` copy SHALL be corrected: zero calls with zero labels SHALL read
*"nothing was classified and no model was consulted"*, never *"fully rule-covered"*.

### 6.8 Eval-set visibility (capability `eval-set-visibility`)

**FR8.1** A route SHALL list the cases of the eval set behind a board: case id, family, oracle kind,
and whether the oracle is indecisive.
**FR8.2** Each case SHALL show which nodes it exercises where that is known, and the set SHALL report
which graph nodes no case reaches.
**FR8.3** Vacuous dimensions already computed by `evalgen.CoverageReport` SHALL be listed, not just
counted.
**FR8.4** Where coverage was not measured, the surface SHALL state what that invalidates — reusing the
board's existing sentence rather than inventing a second one.

### 6.9 Proposal reach and honesty (capability `proposal-generation-reach`)

**FR9.1** `hostedproposals` SHALL carry `proposalgen`'s state through to the surface. `empty` SHALL be
reserved for "a pass ran and found nothing", and SHALL be distinguishable from "no pass has ever run".
**FR9.2** A generation pass SHALL be triggerable from the console.
**FR9.3** The generate action SHALL be published at the customer edge as a **flat** path
(`/api/v1/proposal-generations`, identifiers in the body), following P29's established shape. A
`Prefix` rule under `/api/v1/workflows/` SHALL NOT be added.
**FR9.4** The last pass's timestamp, state and sentence SHALL be stored and rendered.

### 6.10 Operator control (capability `operator-agent-control`)

**FR10.1** The existing kill switch SHALL gate HEROS inference fleet-wide, durably
(`internal/adminstore/killswitch.go`).
**FR10.2** HEROS token spend SHALL be metered per tenant per inference and readable in the operator
console.
**FR10.3** A per-tenant and a fleet-wide spend cap SHALL be configurable; reaching a cap SHALL stop
inference and report the cap as the reason.
**FR10.4** Every HEROS run SHALL emit a structured event through the central enumeration
(`internal/errorcode`, `internal/metricevent`) — never a literal event name.

---

## 7. Non-functional requirements

| # | Requirement | Scenario it is checked by |
|---|---|---|
| **NFR1** | Zero rule-covered work costs zero tokens | A Go fixture with a full graph produces 0 provider calls |
| **NFR2** | A page load never triggers an inference | Rendering a workflow with no stored inference shows `not analysed` with an action, and makes no provider call |
| **NFR3** | An inference is bounded in wall-clock and tokens; exceeding either aborts and records the abort | Fixture with a 5k-file repo aborts at the cap and reports it |
| **NFR4** | HEROS holds no customer source beyond the snapshot the customer already pushed | No new store; reads `workflow_source` |
| **NFR5** | HEROS's provider traffic goes through `providergateway`, never a bare `http.Client` | Static fence, mirrors the two-lane egress rule |
| **NFR6** | Failure is loud: an inference error surfaces as `analysis failed` with a cause, never as an empty graph | Fault injection on the provider returns a named failure |
| **NFR7** | The IR migration is reversible; provenance is additive and old IRs remain readable | Down-migration test |
| **NFR8** | Inference is idempotent under retry — a duplicate request with the same key writes once | Concurrent double-submit test against Postgres |

---

## 8. System design summary

### 8.1 Shape

```
                       ┌──────────────────────── operator console ────────────────────────┐
                       │  /agent   prompt · model · credential ref · skills · tools ·      │
                       │           context · memory · harness   →  Variant Spec            │
                       │  /agent/spend   caps, per-tenant meter    /killswitch  arm        │
                       └───────────────┬──────────────────────────────────────────────────┘
                                       │ publishes an immutable version → config_hash
                                       ▼
   source snapshot          ┌──────────────────────┐        ┌─────────────────────────┐
   (P29 push-source)  ───▶  │  HEROS runner        │  ───▶  │ inference store         │
                            │  residue only        │        │ key: (wf, rev, cfghash) │
   rule-derived IR    ───▶  │  abstains by default │        │ edges · labels · absten │
   (discovery frontends)    └──────────┬───────────┘        └───────────┬─────────────┘
                                       │ providergateway                │
                                       │ (platform credential)          │ merge, never overwrite
                                       ▼                                ▼
                            ┌──────────────────────┐        ┌─────────────────────────┐
                            │ customer-side runner │  ───▶  │  IR + provenance        │
                            │ CLI, customer key    │  P29   │  frontend|detector|heros│
                            └──────────────────────┘ ingest └───────────┬─────────────┘
                                                                        ▼
                                          graph · composition · eval set · board · proposals
```

### 8.2 Decisions

Each decision states, per the collaboration norm: the problem it solves, why this design, why it is the
right design, the comparison that decided it, and the effect.

---

**D1 — HEROS is a Variant Spec, not a new configuration system.**

*Problem.* The operator must manage prompt, model, skills, tools, context, memory and harness for the
platform's own agent.

*Design.* Reuse the six-axis vocabulary and the P2 registries. HEROS's definition is a Variant Spec;
publishing an edit mints a new immutable version; the identity is a `config_hash` from
`internal/confighash`.

*Why right.* The prohibition list's #13 (do not split the source of truth) and #15 (no
built-for-the-future tables) both point here, and the ladder ranks *inextensible design* (level 5) far
above *implementation cost* (level 8). A parallel `heros_config` table would be a second vocabulary for
concepts that already have one, drifting from the first the week after it ships.

*Alternative rejected.* A dedicated settings table with typed columns. Cheaper this week; it forks the
meaning of "harness strategy" permanently, and it cannot be evaluated by the platform's own harness.

*Effect.* HEROS's definition is versioned, diffable, and — because it is an ordinary spec — measurable
by the eval harness the product already ships. The platform's own agent becomes the reference customer.

---

**D2 — Determinism by content-addressed pinning, not by model settings.**

*Problem.* A graph must not change between two page loads. Models are not reproducible.

*Design.* Key an inference by `(workflow_id, source_revision, agent_config_hash)`; store the result;
serve from the store. Re-inference is explicit and diffed.

*Why right.* This is the only construction that yields the guarantee without asserting something false
about the model. `patternclassifier`'s `FallbackConfig` already records seed and temperature "so a
stored label can always be traced back" — traced, not reproduced. P30 makes that distinction explicit
instead of letting a reader infer determinism from the presence of a seed field.

*Alternative rejected.* Temperature 0 plus a fixed seed, called reproducible. It is not, across
provider-side model updates, and the failure is silent.

*Effect.* Stability is a property of the store, checkable by a test, independent of the vendor.

---

**D3 — HEROS is additive to rule-derived facts and may never overwrite one.**

*Problem.* Sign-off granted HEROS first-class IR authority (§14 records this). Unbounded, that lets a
model's guess displace a parser's fact.

*Design.* First-class, with two fences: HEROS may not emit an edge where a frontend emitted one, and may
not delete one (FR3.6); and everything it writes carries provenance (FR4.1).

*Why right.* The requested capability is *filling the residue*, and the residue is by definition where
no rule spoke. Restricting HEROS to it costs nothing the operator asked for and removes the only path by
which this phase could make a currently-correct Go graph worse.

*Alternative rejected.* Candidate-only rendering. Explicitly overruled at §14 Q0 — it leaves every
non-Go surface as sparse as it is today, which is the problem.

*Effect.* Non-Go graphs fill. Go graphs are bit-for-bit unchanged, which is an assertable regression
test.

---

**D4 — Two placements, one definition.**

*Problem.* Source and keys should stay local for customers who require it; operators need one control
point.

*Design.* `platform | customer | disabled` per tenant. Same spec, same `config_hash`, two hosts.
Customer-side results arrive via P29's ingest.

*Why right.* Level 1 (security) is satisfied for the strict tenant without denying the convenience
tenant the hosted path. One definition keeps level 5/6 (evolvability, extensibility) intact — the
alternative is two agents that drift.

*Alternative rejected.* Platform-only. Simplest, and unsellable to any customer whose source may not
leave their network.

*Effect.* The parity test (FR6.5) becomes the contract that keeps them one agent.

---

**D5 — The credential is a reference; the console has no key field.**

*Problem.* "Admin should set the model and API key of HEROS."

*Design.* Model from the operator registry. Credential as a provider name resolved by
`providergateway`'s `Secrets` source. No value is entered, stored, logged or rendered.

*Why right.* Level 1, and prohibition-list #66/#69. `NewSecretsFromEnv` already fails closed on an
unrecognised source *"rather than a silent fall back to env"*; FR2.4 extends the same posture to an
unresolvable reference.

*Alternative rejected.* A masked input storing an encrypted value. It puts plaintext keys in request
bodies and audit logs, and duplicates a secret store the deployment already has.

*Effect.* The operator picks *which* credential; the deployment owns the value. **Note:** this answers
"set the API key" as *bind the key*, not *type the key*. Called out because it is narrower than the
words used, and §14 **Q1** carries the related question of *whose* key.

---

**D6 — Composition, not a workflow label.** See §2.6. The composition is descriptive and is never read
as a metric-set selector; the per-region labels remain the dispatcher.

---

**D7 — Fix the four surfaces even where HEROS is absent.**

*Problem.* Three of the four observations are honesty defects that persist whether or not HEROS runs.

*Design.* The empty-graph statement (FR7.4), the `llm_calls` copy (FR7.5), the eval-case list (§6.8) and
the proposal state pass-through (§6.9) land independently of the agent and do not depend on it.

*Why right.* They are cheap, they are level-2 correctness, and shipping them behind an AI feature would
make a true statement contingent on a model being configured.

*Effect.* A deployment with HEROS disabled is strictly more honest than today's.

### 8.3 Design key points

- **The residue is the unit of work.** Not the file, not the repository. It is what makes NFR1 checkable
  and what keeps cost proportional to what the rules could not do.
- **Abstention is an output.** FR3.4. An agent that cannot say "I don't know" will say something else.
- **Provenance is on the fact, not on the run.** A run-level flag cannot answer "who authored *this*
  edge", which is the question an incident asks.
- **Every emptiness gets a cause.** The pattern this phase repeats from P29: an empty surface that
  cannot say why is indistinguishable from a broken one.

---

## 9. Design by role lens

### 9.1 Senior Product Designer — *reduce the input, never the truth*

The term dictionary, fixed here and used verbatim in UI, CLI, API and code:

| Interface term | Entity | Code | Note |
|---|---|---|---|
| **HEROS** | the platform agent | `herosagent` | One agent. Never "the AI", "the assistant", "our model". Singular, always capitalised. |
| **Analysis** | one inference run | `inference` | Not "scan", not "audit". |
| `agentic chain(workflow graph)` | `workflow_ir` | `discovery.IR` | The operator's phrase maps to the existing noun; the UI says **graph**. |
| `scenario(case)` | `evalharness.Case` | `Case` | The board already says "cases". The eval-set list keeps that word. |
| `overall pattern(composition)` | — | `Composition` | New noun, §2.6. Never "the workflow's pattern". |
| **Inferred** | `provenance = heros` | `ProvenanceHeros` | The single word every surface uses for a HEROS-authored fact. Never "AI-generated", "guessed", "predicted". |
| **Not analysed** | no stored inference | — | Distinct from "no pattern found". |

Interaction-simplicity rules applied: the customer takes **zero** new steps — placement is an operator
setting and analysis is triggered by the existing `push-source`. The operator's edit path is one screen
per axis reusing the authoring surfaces, not a new form.

The *"configured but not in effect"* visibility rule is where a surface like this normally fails, and it
drives four requirements rather than one. Three states must always be distinguishable per axis — **set**,
**defaulted**, **not in effect** — and "not in effect" always carries its reason (FR1.13). A published
definition that has not passed rehearsal renders as *pending rehearsal* and the screen still names which
definition is actually serving inference (FR1.4). An unresolvable credential renders `unavailable` rather
than looking configured (FR2.4). And a harness strategy the runner cannot serve is refused **at save**
with the missing service named, rather than accepted and then refused at run (FR1.7) — the difference
between the operator learning it and a stranger learning it.

One naming consequence worth stating: an unavailable harness strategy is shown **unavailable with its
reason**, never hidden. A hidden option is indistinguishable from one that does not exist, and the
operator's next question — *"why can't I use react-loop?"* — has an answer we already know.

UI-redesign rule: the graph page keeps every element it has today. The composition is added above
Patterns; nothing is removed.

### 9.2 Senior System Designer — *arbitrate by level; do not open a one-way door*

The one-way doors in this phase and how each is held open:

| Door | Held open by |
|---|---|
| `provenance` on every IR fact | An enum with an `operator` member reserved but unused — a fourth author is foreseeable, and a boolean `is_inferred` would have to be replaced |
| Inference cache key | Includes `agent_config_hash`, so changing the definition does not invalidate the concept, only the entries |
| The wire for customer-side results | Extends P29's structure payload rather than minting a second one |
| Placement enum | Three named values, not a boolean — `disabled` is a real state, not the absence of `platform` |

Ladder arbitration on the two live trade-offs. *Platform-side inference reads customer source with a
platform key*: level 1 concern, resolved by making placement explicit and per-tenant rather than by
declining the capability. *First-class IR authority*: level 2 (a wrong graph misleads), traded down by
D3's two fences rather than by refusing the request.

Control/data plane: HEROS is control-plane. It reads a snapshot and writes annotations. It never sits in
a customer's request path, which is what keeps NFR2 ("a page load never triggers an inference")
achievable at all.

### 9.3 Senior Backend Dev — *a 200 is not evidence of a write*

- Schema ↔ migration ↔ code land together. New: `heros_agent_version`, `heros_inference`,
  `heros_abstention`, `heros_spend`; altered: IR storage gains `provenance`. Dual dialect
  (SQLite/Postgres) or it does not ship.
- Adding a column to a deployed table is the rule broken three times before; `provenance` is added
  nullable, back-filled to nothing, and read with the `legacy` semantics of FR4.3 — never back-filled
  with a guessed value.
- Idempotency: the inference key is a unique index, and the writer takes the conflict path. NFR8's test
  is a concurrent double-submit against real Postgres — the goroutine-race lesson from earlier phases is
  that a unique index is invisible to a test that never contends.
- Four-layer live assertion for the acceptance run: trigger a real analysis → row in `heros_inference`
  → edge count changes in the served IR → the graph page draws edges. A 200 from the generate endpoint
  is not any of those four.
- Errors: `DomainError` with codes from the central enumeration. No literal event names.

### 9.4 Senior Frontend Dev — *states stay distinct*

Four states must never collapse into each other on any surface:

| State | Sentence | Not to be confused with |
|---|---|---|
| `measured` | the platform has evidence | — |
| `inferred` | HEROS authored this | `measured` |
| `not analysed` | no inference exists yet | `no pattern found` |
| `unavailable` | HEROS is off, capped, or its credential did not resolve | `not analysed` |

No improvised styling: `inferred` reuses the existing `model-labelled region` treatment already in the
graph legend, which is the anchor for "a candidate, not a fact". The gateway-unreachable rule applies —
if the HEROS surface fails, the console keeps its shell and degrades that panel, never a full-screen
error. English only, per the standing repo rule. URL remains the state source of truth: the eval-set
list is a real route, deep-linkable, not a modal.

### 9.5 Senior AI Engineer — *signal, baseline, discriminative power, before any architecture*

Written first, per the standing rule that detection design may not be reverse-engineered from a clean
architecture:

```
signal source:   The repository source at a pinned source_revision, plus the rule-derived IR and
                 the frontend's own diagnostics naming what it could not follow. Fully controlled:
                 the same revision yields the same input. No runtime traces.

baseline:        (a) The Go frontend's real edge output on repositories that have one — the only
                     ground truth the platform already owns, and the reason the Go fixtures matter
                     even though Go is the language that needs HEROS least.
                 (b) A hand-labelled fixture set per language, with near-misses: a linear chain that
                     is not a router, a fan-out with no merge, two calls in one file with no data
                     dependency between them.

discriminative   Precision/recall on EDGES against (b), reported per language, with the near-misses
power:           as the evidence that the agent discriminates rather than connects everything it
                 sees. A definition that cannot clear the threshold cannot be activated (FR1.5).
                 An agent that emits a complete graph on a repository with no dependencies scores
                 zero precision, and that case is in the fixture set on purpose.
```

Ablation discipline: changing prompt, model, or context policy is a change to the detection chain, so
it runs on the fixture set before activation — never "the code compiles, ship it". One variable at a
time; the rehearsal reports the delta against the previous `config_hash`.

Aggregate-hides-single-sample: precision/recall over a fixture set is exactly the aggregate that hides
a per-repository catastrophe. The rehearsal therefore reports **per-fixture** results and fails on any
single fixture below its own floor, not on the mean.

Cost: HEROS spend is an **estimate** and labelled as such wherever it is shown, consistent with the
standing pricing rule. An unpriced model yields `unpriced`, never `0`.

Train/serve skew: the platform-side and customer-side runners must assemble context by the same code
path. FR6.5's parity test exists because two runners with one prompt is exactly the shape that produces
skew.

### 9.6 Senior DevOps Engineer — *blast radius, reversible, observable, least privilege*

- **Blast radius.** HEROS is off by default fleet-wide. Enablement is per tenant. The existing durable
  kill switch gates it; the in-memory failure mode that shipped before (a brake that forgot it was
  pulled across a restart) is why FR10.1 names the durable store explicitly.
- **Reversible.** The IR migration is additive and down-migratable. Disabling HEROS returns every
  surface to rule-derived facts; stored inferences are retained and marked stale, not deleted.
- **Observable externally.** `/readyz` gains HEROS's resolved state — `disabled`, `ready`,
  `credential_unresolved`, `capped`. A health signal that requires reading logs is not a health signal.
- **Least privilege.** The platform credential is provider-scoped and read through the existing secrets
  source. No new secret mechanism.
- **Spend.** Caps are enforced before the call, not reconciled after it. Reaching a cap is an event, not
  a silent stop.
- **Rollout.** Internal tenant → one design-partner tenant → opt-in → default-on, with the rehearsal
  gate between each. No stage is verified by hand.

### 9.7 Senior QA Engineer — *green is worth having only if green can be red*

Every fence in this phase is verified by breaking it first:

| Fence | Made red by |
|---|---|
| HEROS never overwrites a frontend edge (FR3.6) | A fixture where the agent is prompted toward an edge the frontend already emitted; assert the stored IR keeps the frontend's |
| Go graphs unchanged | Byte-compare the served IR for a Go fixture with HEROS on and off |
| Zero rule-covered tokens (NFR1) | A recording provider fake with an injectable error; assert **zero** calls, not "no error" |
| Cache honoured (FR5.1) | Second request asserts `provider_calls == 0` and an identical body |
| Provenance survives aggregation (FR4.4) | A mixed graph asserts the count reports both parts |
| Credential unresolved fails closed (FR2.4) | Point the reference at a missing secret; assert `unavailable` and zero calls |
| Parity (FR6.5) | Run both placements on one fixture; assert equal edge sets |

Anti-vacuity is mandatory: a fixture set that fails to load must fail the test, not pass over an empty
set — the failure mode that has already cost this codebase a green suite more than once. The provider
fake is **recording with injectable errors**, never a silent-return stub.

Failure-path density: ≥30% of new test functions target error and boundary paths — abstention, cap
reached, credential missing, provider timeout, oversized repository, conflicting edge, unresolvable
model ref.

Live acceptance: the terminal assertion is `openclaw` itself. Push its source, run an analysis, and the
graph page draws edges with an `inferred` marking and a composition paragraph. Anything short of the
page rendering is not acceptance — the end of end-to-end is the user's eyes.

### 9.8 Senior Sales Operations — *only promise what shipped; state the boundary out loud*

Sayable on delivery:

> For repositories in languages where static analysis cannot follow the call chain, HEROS reads the
> source and proposes the missing structure. Every fact it authors is marked as inferred and carries the
> exact agent version that produced it. It runs on your infrastructure with your own key if you require
> that.

Not sayable, and the reason:

| Do not say | Because |
|---|---|
| "HEROS understands your codebase" | It infers a graph on a pinned revision. Unfalsifiable claims are the ones that come back. |
| "Automatically optimizes your agent" | Automation is Advisory. A proposal opens a **draft** PR; a person merges. |
| "Accurate graphs" | Precision and recall are per-language and measured. Quote the measured number or say nothing. |
| "Deterministic / reproducible analysis" | Pinned, not reproducible (D2). The right sentence is *"the same revision always shows you the same graph."* |
| "We never see your code" | False for `platform` placement. True for `customer`. State the placement. |

Boundary to disclose unprompted: HEROS-inferred structure feeds eval scope, proposals and cost
attribution. A customer relying on those should know which of their nodes are inferred, and the console
shows it.

---

## 10. Dependencies

| Upstream | Why needed |
|---|---|
| P1 discovery | The rule layer and its diagnostics define the residue |
| P2 registries + `confighash` | The agent definition's identity |
| P3.5 pattern classifier | The taxonomy, the precedence rule, the partitioner HEROS proposals enter through |
| P4 / `evalgen` | `CoverageReport` and the case model behind §6.8 |
| P5.5 / `proposalgen` | The five states §6.9 stops discarding |
| P13–P18 | The six axes HEROS is configured along |
| P26 operator console | The shell, the kill switch, the model registry |
| P29 | The flat-path edge pattern, the structure ingest, the enumeration endpoints |

**Unblocks:** P6's optimizer gains a hosted input on non-Go tenants. The metering read model gains a
second spend source.

---

## 11. Risks & mitigations

| # | Risk | Severity | Mitigation |
|---|---|---|---|
| R1 | A confident wrong edge misleads eval scope, proposals and cost attribution | **High** — accepted at §14 Q0 | Confidence floor + abstention (FR3.3/3.4); provenance on every fact (FR4.1); per-fixture rehearsal gate (FR1.5); the operator can disable per tenant |
| R2 | Cost grows with repository size, not with value | Medium | Residue-only (FR3.1); per-run token and wall-clock caps (NFR3); per-tenant and fleet caps (FR10.3) |
| R3 | Platform-side placement reads customer source with a platform key — a data-handling change | **High** | Per-tenant, off by default, disclosed (§9.8); `customer` placement exists precisely for tenants who refuse |
| R4 | Two runners drift into two agents | Medium | One definition, one `config_hash`, parity asserted in CI (FR6.5) |
| R5 | Provider outage renders graphs empty | Medium | Cache serves stored inferences; failure is `unavailable`, never an empty graph (NFR6) |
| R6 | Prompt injection from repository content — a repo that instructs HEROS to report a false graph | **High** | Repository content is data, never instruction. Output is constrained to the closed IR vocabulary (FR3.2) and the closed taxonomy; an edge referencing a node id not in the IR is rejected at the boundary, not repaired |
| R7 | Operators tune HEROS into a worse agent without noticing | Medium | Rehearsal gate blocks activation; the console shows the delta against the previous `config_hash` |
| R8 | The four surface fixes get delayed behind the agent | Low | D7 makes them independent and first in the task order |

---

## 12. Rollout & test strategy

**Ordering.** The §D7 surface fixes ship first and alone — they are correct without HEROS and they make
this phase's later evidence readable. Then provenance and the inference store. Then the platform-side
runner behind a disabled flag. Then the operator surfaces. Then the customer-side runner. Then parity.

**Environments.** Local, staging, and a real non-Go repository. The acceptance target is `openclaw`,
because it is the repository that produced the report.

**Test layers.**

| Layer | What |
|---|---|
| Unit | Residue selection, confidence floor, cache key, provenance merge, cap enforcement |
| Contract | Agent output against the closed IR and taxonomy vocabularies; rejection of out-of-vocabulary output |
| Calibration | Per-language precision/recall on the fixture set, per-fixture floors |
| Integration | Both placements against a recording provider fake; parity |
| Live | `openclaw` end to end, terminating at the rendered page |

**Gates.** No activation without a passing rehearsal. No default-on without a design-partner tenant
running for a full billing period. No stage verified by hand.

---

## 13. Success metrics & acceptance criteria

| # | Criterion | How it is checked |
|---|---|---|
| **A1** | `openclaw` renders a graph with edges | The page, in a browser |
| **A2** | Every inferred fact is marked, and the mark survives counting | Mixed-provenance fixture |
| **A3** | A Go workflow's served IR is byte-identical with HEROS on and off | Byte comparison |
| **A4** | A second page load makes zero provider calls | Recording fake |
| **A5** | Disabling HEROS leaves every surface honest and no surface broken | Fixture run with placement `disabled` |
| **A6** | The graph header never says "fully rule-covered" over an unclassified graph | Assertion on the rendered string |
| **A7** | An eval set's cases are listed, with indecisive oracles named | The page |
| **A8** | The proposals surface names its state, and a pass is triggerable end to end through the published edge path | Live run |
| **A9** | An unresolvable credential yields `unavailable` and zero provider calls | Fault injection |
| **A10** | Per-fixture calibration meets its floor for every supported language | Rehearsal report |

**Metrics after 30 days:** fraction of hosted workflows with a non-empty graph (today: Go only); median
HEROS cost per analysis; abstention rate per language; number of proposals generated on previously
`no_discovered_graph` workflows.

---

## 14. Open questions

**Q0 — HEROS's authority over the IR. DECIDED.** damon, this session: HEROS supplies edges and labels as
**first-class IR**, with provenance recorded. The concern raised before the decision — that a model's
inference about a customer's code then drives eval scope, proposals and cost attribution — was
acknowledged and the option chosen anyway. Recorded here rather than in chat, because it is the
assumption the rest of this document is built on. D3's two fences are the mitigation added on top; they
do not change the decision.

**Q1 — Whose key does platform-side HEROS spend? DECIDED: the platform's.** damon, this session.
Platform-side inference resolves the credential reference on the active agent definition, which names a
**platform** provider credential. The platform continues to hold **no customer provider key**, so P29's
promise (*"the platform holds no customer provider key and will not"*) is intact and unqualified — this
phase does not weaken it. A customer who wants their own key spent uses placement `customer`, where the
key never leaves their machine. FR2.5 and the `agent-runtime-placement` spec state this normatively.

**Q2 — Default placement? DECIDED: `disabled`.** damon, this session. A tenant is not analysed until an
operator sets `platform` or `customer` for it. Consequences accepted: every existing tenant's surfaces
stay exactly as they are today until someone acts, so this phase fills nothing by default and the
`openclaw` acceptance run (A1) requires an explicit enablement step. The alternative — defaulting to
`platform` — would have read customer source under a platform-held credential without an explicit act,
which is the data-handling posture this decision declines.

**Q3 — Does a customer see that a fact is inferred, or only the operator?**
This PRD assumes the customer sees it (FR4.4). Hiding it renders better and is the kind of omission that
becomes a support incident the first time an inferred edge is wrong.

**Q4 — What is the per-language activation floor?**
Precision/recall thresholds must be numbers before FR1.5 can gate anything. Proposed: precision ≥ 0.90,
recall ≥ 0.70 per fixture, on the grounds that a missing edge degrades to today's behaviour while a
wrong edge actively misleads. **Needs a number, from measurement, before activation — not before
build.**

**Q5 — Retention of stored inferences after a tenant disables HEROS.**
Retained-and-marked-stale is assumed. Deletion is defensible and is a different answer for a customer who
disabled the feature over data handling.

**Q6 — Does HEROS assess the *variants* surface at all?**
§6.0 gives it only "whether two variants differ in a way the eval set can detect". That may be too thin
to be worth building in P30, and dropping it costs nothing else.

---

## 15. Written-alignment record (two-stakeholder points)

Per the seven-stage process, the following require a written conclusion in this document before
implementation begins. They are ⚫🔴 (engineering ↔ customer) unless noted.

| # | Point | State |
|---|---|---|
| 1 | **Product form** — HEROS as one platform agent, configured through the six-axis vocabulary, marked-inferred output | Proposed; §8.2 D1, §9.1 |
| 2 | **Technical approach** — residue-only, pinned cache, two placements, additive provenance | Proposed; §8.2 |
| 3 | **Design conflict** ⚫🟢 — "overall pattern" as requested vs. composition as buildable | Proposed; §2.6 — **explicitly flag at review** |
| 4 | **Compatibility** — Go graphs bit-identical; old IRs readable as `legacy` | Proposed; A3, FR4.3 |
| 5 | **Requirement expectation** 🟢🔴 — what may be promised (§9.8); platform key; `disabled` default | **Signed off** — damon, Q1 + Q2 |

**Q1 and Q2 are closed.** The two decisions that gated implementation are recorded in §14 with their
consequences. The remaining alignment points are ⚫ engineering-side and are settled by this document.

🔴 **Point 3 is the one still worth a deliberate yes.** "Overall pattern from all the nodes combined" was
the request; §2.6 explains why a workflow-level label cannot be built without breaking the metric-set
dispatcher, and what is built instead is a composition. That substitution is the only place this phase
delivers something other than what was asked for, and it should be accepted knowingly rather than by
silence.
