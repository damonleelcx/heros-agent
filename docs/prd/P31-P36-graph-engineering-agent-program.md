# Program — Graph Engineering Harness Agent (GEHA), P31 → P36

| | |
|---|---|
| **Program** | Graph Engineering Harness Agent (GEHA) |
| **Phases** | [P31](P31-conversational-console.md) · [P32](P32-repo-intake.md) · [P33](P33-surface-assessment.md) · [P34](P34-harness-loop-graph-split.md) · [P35](P35-autonomous-improvement-run.md) · [P36](P36-agent-self-configuration.md) |
| **ADRs opened** | [ADR-013](../adr/ADR-013-source-acquisition-posture.md) · [ADR-014](../adr/ADR-014-harness-loop-graph-axis-split.md) |
| **Upstream** | P1 (discovery) · P2 (config/runtime) · P4 (eval harness) · P5 (typed contracts, re-arrangement) · P5.5 (verification) · P6 (optimizer) · P12 (forge delivery) · P13–P18 (the six axes) · P29 (linked-run fan-out) · P30 (HEROS, the platform agent) |
| **Status** | Proposed — the four rulings in §3 are taken; the split line in §5.3 and the open questions in §8 are not |

---

## 1. Summary

Today a customer optimizes their agentic workflow by **operating the platform**: install a CLI, run
`discover`, push a bundle, open a console, read a board, click generate, wait, review a proposal, run
their CI. Every one of those steps is built and every one of them is a step. The product's own demo is
eleven of them.

GEHA collapses that into **one sentence typed into a conversation**, and it does so without inventing a
second copy of anything underneath. A person opens the console, points at a repository, asks a question
in English, and the platform's own agent — HEROS — does the work the eleven steps did: reads the
repository's agent-engineering surfaces, says what it found and what evidence backs each finding,
proposes changes, waits to be told yes, applies them, re-measures, and opens the pull request.

The program has a second half that is not a convenience. The platform sells the idea that an agentic
system is **a graph of configured nodes**, and its own agent is **one node** — `NodeID =
"heros_analyst"` ([`definition.go:19`](../../internal/herosagent/definition.go)). That is why HEROS's
wiring axis is not merely unused but *vacuous*, and says so in its own source: *"there is no second node
to order it against."* An agent that cannot be a graph cannot do graph engineering, and it certainly
cannot be trusted to advise anyone else's. **P36 makes the platform's own agent a graph.** That is what
"graph engineering capable" means here, and it is the phase the other five exist to make possible.

---

## 2. What is already true, so this program does not rebuild it

The single largest risk to this program is re-implementing what P0–P30 shipped. The pipeline the
conversation drives **exists**:

| Step in the conversation | Existing component | Evidence |
|---|---|---|
| extract the LLM call graph | `internal/discovery` → Workflow IR | P1, six language frontends |
| hold the repository's source | `internal/sourceingest` | `Source` iface + hardened `BundleSource` |
| configure a node on an axis | `internal/variantspec` | 7 closed `Dimension`s ([`spec.go:91`](../../internal/variantspec/spec.go)) |
| apply as a reviewable diff | `internal/transform` | ADR-001, AST codemod, `ErrUnsafeRewrite` |
| run it, sandboxed and traced | `internal/executor`, `sandbox`, `telemetry` | P2/P2.5/P3 |
| generate an eval set and score | `internal/evalgen`, `evalharness`, `evalstats` | P4, multi-seed with CIs |
| localize and explain a failure | `internal/attribution`, `diagnosis` | P4.5 |
| propose a change and verify it | `internal/proposal`, `verification` | P5.5, held-out gate |
| run the loop to convergence | `internal/optimizer/loop.go` | P6 |
| open the pull request | `internal/forgedelivery` | P12; **both** modes coded — `cimediated.go` and `hostedapp.go` |
| stream progress to a browser | `internal/api/monitor.go:96` | SSE, already mounted |
| configure the platform's own agent | `internal/herosagent` + admin `/agent`, `/axes` | P30 |

**GEHA adds five things and re-shapes one.** Nothing above is forked, wrapped in a parallel path, or
re-implemented. Where a phase needs behaviour these components do not have, it changes *them* — a second
apply path is a second place for every safety gate to be wrong, and the gates are the platform.

### 2.1 What genuinely does not exist

1. **No conversational surface, anywhere.** `web/console/src` and `web/admin-console/src` contain no
   thread, turn, message or chat concept; all fifty routes are forms and dashboards. The word
   "conversation" appears only in copy describing *the customer's program's* conversation. → **P31**
2. **No clone from a remote.** [`source.go:32`](../../internal/sourceingest/source.go) names `GitSource`
   as the implementation that was deliberately not built. → **P32**
3. **No question-driven orchestration.** `optimizer/loop.go` enumerates, verifies, gates and merges; it
   is not driven by a question and has no approval dialogue. `internal/approval` is a gate, not a
   conversation. → **P35**
4. **No per-surface assessment of a repository.** Discovery extracts *call sites*. Nothing reports "this
   repository's memory strategy is X, and here is the evidence." → **P33**
5. **No loop axis and no graph axis.** See §5. → **P34**
6. **The platform's own agent is one node.** → **P36**

---

## 3. The four rulings this program is built on

These were decided before authoring, because each one changes the shape of most documents downstream.
They are recorded here so a reader in six months can see what was chosen *and what it cost*.

| # | Question | Ruling | What it costs |
|---|---|---|---|
| **R1** | How does the platform get source from a pasted repository URL? | **Bundle-push stays the default; a clone is opt-in, per-repository, read-only, least-privilege and customer-revocable.** | A standing read capability now exists as an option where previously none did. [ADR-013](../adr/ADR-013-source-acquisition-posture.md) states the cost rather than routing around it. |
| **R2** | Seven requested surfaces vs. the shipped six axes | **Loop engineering and graph engineering are separated from harness engineering.** | `DimHarness`'s current definition covers both turns *and* control loop, so this is not additive — see §5 and [ADR-014](../adr/ADR-014-harness-loop-graph-axis-split.md). |
| **R3** | Who commits, pushes and opens the PR? | **Hosted Git App becomes the default for console-driven runs**; CLI/CI runs keep the CI-mediated default of ADR-005. | The platform holds a write-scoped forge credential for console customers. ADR-005 is amended, not overturned: its argument was about a *default*, and there are now two surfaces with two defaults. |
| **R4** | What does "score the repo" mean? | **Evidence-backed per-surface findings; no composite score.** | No headline number for a buyer to quote. In exchange, nothing on the screen is a model's opinion wearing a metric's clothes. |

R4 deserves one more sentence because it is the ruling most likely to be re-litigated. Every score in
this codebase is *comparative and verified* — variant against variant, multi-seed, ties declared when
confidence intervals overlap. An absolute "your repository scores 62" is a different kind of claim, and
the platform's founding principle — *diagnosis proposes, verification decides* — exists precisely to
keep an unverified model judgement from being rendered as a result. P33 therefore reports **findings
with their evidence, and `not measured` where there is none**, and improvements still prove themselves
through the existing P5.5 gate.

---

## 4. The conversation, end to end

The twelve steps in the request map onto the six phases like this. The column that matters is the last
one: it is how much of each step already exists.

| # | Step | Phase | Existing substrate |
|---|---|---|---|
| 1 | conversational console UI | P31 | SSE (`monitor.go`), design system, BFF |
| 2 | import a forge URL or pick a local repo | P32 | `sourceingest.Source`, `heroslocallink` |
| 3 | ask a question | P31 | — |
| 4 | agent discovers the nine axes (§5.4) | P33 | `discovery`, `patternclassifier`, `skillindex`, `toolindex` |
| 5 | score by running generated eval sets | P33 | `evalgen`, `evalharness`, `evalstats` |
| 6 | propose improvements | P35 | `proposal`, `diagnosis` |
| 7 | apply on approval | P35 | `transform`, `worktree`, `approval` |
| 8 | re-run evals and re-measure | P35 | `evalrun`, `verification` |
| 9 | check the change is an improvement | P35 | P5.5 verified-delta gate |
| 10–12 | commit, push, open the PR | P35 | `forgedelivery/hostedapp.go` |
| — | admin configures the agent's own surfaces | P36 | `herosagent`, admin `/agent` + `/axes` |

**Steps 9 and 10 are one gate, not two.** The request lists "if everything is good and there are
improvements" as its own step; in this codebase that is the P5.5 verified-delta gate, and delivery is
*downstream of verification, never a path around it*. P35 does not add a second check.

---

## 5. The axis split (R2), and why it is the hard part

### 5.1 What the enum says today

`Dimensions()` returns exactly seven ([`spec.go:91`](../../internal/variantspec/spec.go)):
`model`, `prompt`, `skills`, `context`, `tools`, `memory`, `harness`. **There is no wiring or graph
dimension.** Node ordering lives outside the enum, in `internal/arrangements`, and what it actually does
is permute a **linear list** — `nextPermutation` over a `[]string` order, scored against typed-contract
edges. So "graph optimization" today means *re-ordering a sequence*.

`DimHarness` is documented in its own source as *"the SCAFFOLD around a node's call — **how many turns it
runs and in what control loop**"*. Those are two different things, and R2 says they must become two axes.

The five loops are real and already closed
([`strategy.go:88`](../../internal/harnessruntime/strategy.go)): `single-shot`, `reflexion`,
`react-loop`, `plan-execute`, `critic-loop`, with a closed stop-condition vocabulary
(`answer-marker`, `no-tool-call`, `plan-complete`, `max-turns`) and `TurnCeiling = 16`.

### 5.2 Why this is not an additive change

The OAX contract says `config_hash` is append-only-compatible: *"the no-override (`none`) case SHALL hash
byte-identically to before the field existed."* Adding `DimLoop` alone satisfies that. **Removing the
loop fields from `DimHarness` does not** — every existing spec that carries a harness override would
hash differently, and a stored `config_hash` that no longer resolves is a measurement nobody can
reproduce. P34 therefore uses expand-contract, and the contract half is explicitly *not* in this
program. See [ADR-014](../adr/ADR-014-harness-loop-graph-axis-split.md).

### 5.3 The proposed split line — **this is what §8 asks you to sign off**

| Axis | Owns | Concretely, today's code |
|---|---|---|
| **Loop** | the iteration *policy*: which control loop, the stop condition, `max_turns` within the ceiling, the reflection prompt, the critic binding | `harnessruntime/strategy.go`'s five loops; `registry.KindHarness`'s `Strategy` + loop params |
| **Harness** | the execution *envelope*: sandbox posture, host services (`HostToolInvoker` / `HostPlanner` / `HostCritic`), turn and spend ceilings, retries, timeouts, concurrency, guardrail and approval gates | `internal/sandbox`, `harnessruntime.HostService`, `TurnCeiling`, `herosagent/caps.go`, `internal/runqueue`, `internal/approval` |
| **Graph** | the *topology*: nodes, edges, ordering, fan-out / fan-in, conditional routing, merge, subgraph extraction | `internal/arrangements` (ordering only, today), `internal/typedcontract`, the reserved-and-unimplemented `OpMerge` |

### 5.4 The nine axes, and how your seven map onto them

After P34 the platform has **eight `Dimension`s** — `model`, `prompt`, `skills`, `tools`, `context`,
`memory`, `harness`, `loop` — plus **one spec-level axis**, `graph`. Nine in total. The seven surfaces in
the original request map onto them without remainder:

| Requested surface | Axis / axes |
|---|---|
| prompts | `prompt` + `model` |
| context strategies | `context` |
| memory strategies | `memory` |
| harness strategies | `harness` |
| loop strategies | `loop` **(new in P34)** |
| graph strategies | `graph` **(new in P34)** |
| skills, tools, workflows | `skills` + `tools`; **"workflow" is not an axis** — in this repository a *workflow* is the target program's LLM call graph, the thing being optimized. A workflow is configured *by* the nine axes; it is not one of them. |

That last row is a noun-dictionary correction, and it is the kind the sales-ops lens exists to force: one
word, one meaning, across the console, the CLI and the docs. Where the request said "workflows" as a
configurable surface, the platform's word for that surface is **skills and tools** — the reusable units a
node binds — and `workflow` stays the name of the graph being improved.

Read the §5.3 table as a claim about *where a change lands*, not about vocabulary. "I want two model calls to
run in parallel and their outputs merged" is a **graph** change. "I want it to stop after four turns" is
a **loop** change. "I want it to never spend more than a dollar and never touch the network" is a
**harness** change. Each of those sentences currently has to be answered by the same axis, which is why
the axis cannot give a good answer to any of them.

---

## 6. Sequencing and the critical path

```
        ADR-013 ────▶ P32 (intake) ─────┐
                                        ├──▶ P33 (assessment) ──▶ P35 (run + delivery)
        P31 (conversation) ─────────────┘                              │
                                                                       │
        ADR-014 ────▶ P34 (axis split) ───────────────────────────────┴──▶ P36 (agent is a graph)
```

- **P31 and P32 are independent** and can be built in parallel; P31 has a fixture-driven path that does
  not need a real repository.
- **P33 depends on both** — there is nothing to assess without source, and nothing to say it to without
  a conversation.
- **P34 is on nobody's blocking path until P36**, but it must land before P33 reports on a "loop" or
  "graph" surface, or the report names axes the configuration layer does not have. That is the
  ordering constraint people will get wrong.
- **P36 is last** and is the point of the program.

## 7. Staged plan

Authoring order, so this program can be picked up mid-way in a later session:

- [x] **S0** — this document, plus [ADR-013](../adr/ADR-013-source-acquisition-posture.md) and
      [ADR-014](../adr/ADR-014-harness-loop-graph-axis-split.md), which are the two one-way doors.
- [ ] **S1** — P31 PRD + `openspec/changes/p31-conversational-console/`
- [ ] **S2** — P32 PRD + `openspec/changes/p32-repo-intake/`
- [ ] **S3** — P33 PRD + `openspec/changes/p33-surface-assessment/`
- [ ] **S4** — P34 PRD + `openspec/changes/p34-harness-loop-graph-split/`
- [ ] **S5** — P35 PRD + `openspec/changes/p35-autonomous-improvement-run/`
- [ ] **S6** — P36 PRD + `openspec/changes/p36-agent-self-configuration/`
- [ ] **S7** — fold the program into [`docs/prd/README.md`](README.md) and the implementation timeline

**This program is documents only.** No Go code, no console code, and no migration ships with it — the
same scope the OAX program (P13–P18) was authored under. Every phase's `tasks.md` carries its
implementation as unchecked tasks.

## 8. Open questions — sign-off required

| # | Question | Why it cannot be defaulted |
|---|---|---|
| **Q1** | Is the §5.3 split line right? Specifically: does **harness** keep the spend/turn ceilings, or do ceilings belong to **loop** because `max_turns` is a loop parameter? | The answer decides whether `TurnCeiling` is a policy the envelope imposes or a value the loop chooses, and those have different blast radii. My recommendation: the **ceiling** is harness (a policy about blast radius), the **value within it** is loop. |
| **Q2** | Does the graph axis get a `Dimension`, or does it stay spec-level like `arrangements` does today? | A `Dimension` is *per node*; topology is *between* nodes. Making graph a `Dimension` would be the first member of that enum that is not a property of one node. My recommendation: **spec-level**, hashed into `config_hash` as a sibling of `ordering`. |
| **Q3** | When a console customer enables the hosted Git App (R3), does the agent push to a **branch on their repository** or to a **fork the platform owns**? | ADR-005 listed the fork as option B and never decided it. Pushing a branch needs write on their repo; a fork needs none but produces a cross-repository PR many CI setups will not run. |
| **Q4** | Does the conversation persist? A thread that survives a page reload is a durable store of the customer's questions — which may quote their source. | P23's data inventory would need a new row. My recommendation: persist the **run** and its evidence; keep the **turns** ephemeral until Q4 is answered. |
| **Q5** | R4 forbids a composite score. Does Sales Operations accept a first-touch surface with no headline number, and if not, what is the honest substitute? | Discipline 1 of the sales-ops lens is *only promise what shipped*; a number that cannot be defended is the exact failure it names. |
