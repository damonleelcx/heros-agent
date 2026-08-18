# PRD — P36: The Agent Is a Graph

| | |
|---|---|
| **Phase** | P36 |
| **Program** | [Graph Engineering Harness Agent (GEHA)](P31-P36-graph-engineering-agent-program.md) |
| **OpenSpec change** | [`p36-agent-self-configuration`](../../openspec/changes/p36-agent-self-configuration/) |
| **Lead roles** | System Designer + Frontend Dev |
| **Support roles** | Backend Dev, AI Engineer, DevOps, QA, Product Designer, Sales Operations |
| **Upstream** | P30 (HEROS, the platform agent) · P8 / P26 (operator console) · [P34](P34-harness-loop-graph-split.md) (the axes to configure) · P2 (registries) · [ADR-014](../adr/ADR-014-harness-loop-graph-axis-split.md) |
| **Unblocks** | nothing — this is the program's terminus |
| **Status** | Proposed — awaiting sign-off on §14 |

---

## 1. Summary

P30 built HEROS and made a point of configuring it *through the same vocabulary the product sells to
customers*: prompt, model, skills, tools, context, memory, harness — registry references, resolved against
the P2 registries, sealed to a `config_hash`. Its own PRD puts it well: *"The platform optimizes agentic
workflows for a living; its own agent is one of them."*

It is one of them with one exception, and the exception is written into the source as a constant:

```go
const NodeID = "heros_analyst"
```

with the comment: *"One node, and that is what makes the wiring axis vacuous rather than merely unused:
there is no second node to order it against."*

So the platform's own agent is a single call site. It cannot fan out, cannot route conditionally, cannot
run a critic beside an analyst, and cannot be re-ordered — not because those were refused, but because
there is nothing there to arrange. **An agent that cannot be a graph cannot do graph engineering.** P36 is
where the platform's own agent becomes what it sells.

Concretely, P36 does three things:

1. **Adds `loop` and `graph` to the operator's axis vocabulary**, so the agent is configured across all
   nine axes [P34](P34-harness-loop-graph-split.md) defines.
2. **Makes HEROS multi-node**, which turns `AxisWiring`'s deliberate vacuity into a real axis and retires
   `ErrWiringOverride` as a refusal-by-absence.
3. **Keeps every P30 property that made HEROS trustworthy** — determinism by pinning, credentials by
   reference only, rehearsal before activation, spend caps before every provider call — across a
   configuration surface that is now considerably larger.

The phase's centre is a compatibility problem that is easy to miss and expensive to hit. **Every pinned
inference is keyed by `(source_revision, agent config_hash)`.** Changing the shape of the agent's
definition changes that hash, which orphans every pin, which means every assessment ever taken re-runs at
provider cost the next time anyone asks — and, until they do, the console shows results computed by a
configuration that no longer exists. §6.4 is that problem.

---

## 2. Problem & context

### 2.1 What the operator can configure today

`herosagent.Definition` is every axis as a **reference**, never an inlined value — *"a definition that
inlined a prompt body or a strategy's params would be a configuration whose content lives outside any
registry, so it could never be resolved back from a `config_hash` months later."* `AuthorableAxes()`
returns seven: prompt, model, skills, tools, context, memory, harness.

`AxisWiring` is in the vocabulary **on purpose**, rendered read-only with a reason rather than hidden,
because *"a hidden axis is indistinguishable from one that does not exist."* That was the right call while
it was vacuous. After P36 it is no longer vacuous, and the read-only rendering becomes wrong.

Around the definition sits real safety machinery, all of which P36 must preserve: `CredentialRef` is a
provider **name** resolved at use, with `ErrKeyValueOffered` refusing anything key-shaped and a reflective
fence asserting no field in the package could hold a key; `ErrHostServiceMissing` refuses a strategy whose
host service the runner cannot supply; `ErrRehearsalNotPassed` refuses activating a definition that has not
met its floor; `CapChecker` enforces a spend ceiling before every provider call; and `ErrNoChange` refuses
to mint a duplicate version for an edit that resolves to the active definition.

### 2.2 Why one node is a product limit and not just a shape

Three of the platform's own surfaces are weaker because of it:

- **The classifier's own medicine.** P30 exists because seven of eight pattern detectors are topology
  predicates and cannot fire on a graph with no edges. HEROS is a graph with no edges.
- **No division of labour.** An analyst that reads source and a critic that checks its claims are two
  nodes. In one node, the critic is a turn — which is what `critic-loop` is, and it is a loop strategy
  standing in for a topology the model cannot express.
- **No parallelism over nine axes.** [P33](P33-surface-assessment.md) assesses nine axes per repository. In
  one node that is nine sequential inferences or one very large prompt, and neither is a choice anybody
  made — it is the shape choosing for them.

### 2.3 The mirror argument, and why it is not just rhetoric

The product's claim is that agentic systems get better when their prompts, loops, envelopes and topology
are configured deliberately and verified empirically. If the platform's own agent is a hardcoded single
node with an unconfigurable topology, the claim is one the vendor does not apply to itself.

That is a sales problem, and it is also a **testing** problem, which is the part that matters more: HEROS
is the only agentic workflow the platform can change and measure freely. A multi-node HEROS is the first
real fan-out, conditional edge and merge the graph axis will ever be exercised against — before a
customer's repository is.

---

## 3. Goals & non-goals

### Goals

1. **G1** — The operator configures the agent across all nine axes; `loop` and `graph` join the seven.
2. **G2** — HEROS is **multi-node**: a definition declares nodes, their per-node axis bindings, and a
   topology among them.
3. **G3** — `AxisWiring`'s read-only rendering is retired **because the axis became real**, not because the
   refusal was relaxed.
4. **G4** — Every P30 safety property survives: references not values, no field that can hold a key,
   host-service refusal, rehearsal before activation, spend caps before every call, `ErrNoChange`.
5. **G5** — Existing pinned inferences remain **attributable and readable**; the migration does not silently
   invalidate the platform's own history (§6.4).
6. **G6** — The agent's own definition is validated by the **same** typed-contract and topology rules P34
   imposes on a customer's spec. One validator, not a lookalike.
7. **G7** — A definition that cannot be executed by this build is refused **at publish**, naming the axis
   and the node — the P30 posture, extended to nine axes and N nodes.

### Non-goals

- **Letting customers configure HEROS.** It is an operator surface; the tenant-facing control stays
  placement (`platform` / `customer` / `disabled`).
- **Autonomous self-modification.** HEROS does not propose changes to its own definition. That is a loop
  the verification gate cannot referee, because the thing being changed is part of what does the
  measuring.
- **New axes** — [P34](P34-harness-loop-graph-split.md).
- **Changing what HEROS is for** — it emits graphs and reports whether a surface's answer is supported.

---

## 4. Users & personas

| Persona | What P36 gives them |
|---|---|
| **Operator** (primary, P8 console) | nine axes, N nodes, and a topology — configured, rehearsed, activated, and diffable |
| **Platform engineer** | a real agentic workflow to exercise the graph axis against before a customer does |
| **Support engineer** | an answer to "why did the agent say that" that includes which node said it |
| **Customer** | indirectly: better graphs and labels, and a vendor that runs what it sells |

---

## 5. User stories

- **US1** As an operator I configure a loop strategy for the agent as its own axis, so that iteration
  policy is separate from the execution envelope.
- **US2** As an operator I add a second node — a critic beside the analyst — and declare the edge between
  them, so that division of labour is a topology and not a prompt instruction.
- **US3** As an operator I declare that the nine axis assessments run concurrently and merge, so that an
  assessment's latency is not nine sequential inferences by default.
- **US4** As an operator I see the wiring axis as **editable with its history**, rather than read-only with
  an explanation, so the console reflects what is true.
- **US5** As an operator I rehearse a multi-node definition against the calibration set before activating
  it, so that activation is never the first time it ran.
- **US6** As an operator I publish an invalid topology and am refused **at publish**, naming the node and
  the axis, so that I do not discover it during a customer's assessment.
- **US7** As a support engineer I trace a finding to the node that produced it, so that "the agent was
  wrong" becomes "this node was wrong".
- **US8** As an operator I upgrade to a multi-node definition and the platform's prior assessments stay
  readable and attributable to the definition that produced them, so that history survives the upgrade.

---

## 6. Functional requirements

### 6.1 Nine axes (capability `operator-agent-authoring`, modified)

**FR1** — `AuthorableAxes()` returns nine: prompt, model, skills, tools, context, memory, harness, loop,
graph. **FR2** — `loop` and `graph` are registry references like every other axis; no axis may be inlined.
**FR3** — Loop and harness follow P34's split: the envelope imposes ceilings, the loop chooses within them,
and a violation is refused at publish naming both values. **FR4** — `ErrHostServiceMissing` extends to the
loop axis: a loop needing a host service this runner does not supply is refused at publish, not at run.

### 6.2 Multi-node

**FR5** — A definition declares one or more nodes, each with its own axis bindings. **FR6** — `NodeID` stops
being a package constant; node identity is data. A single-node definition remains expressible and remains
the default. **FR7** — The definition declares a topology over its nodes — ordering, and (per P34)
concurrency, conditional edges and merge. **FR8** — Topology is validated by the **same** typed-contract
and validation path P34 applies to a customer's Variant Spec. A second validator for the platform's own
agent would be a second place for the rules to be weaker. **FR9** — A fan-in with no declared merge is
refused at publish, as it is for a customer.

### 6.3 The wiring axis becomes real

**FR10** — `AxisWiring` becomes authorable and `ErrWiringOverride` is retired. **FR11** — The retirement is
**because the axis became real**; a single-node definition still refuses an ordering, because there is
still nothing to order — the refusal narrows from "always" to "when there is one node", which is the
condition that always justified it.

### 6.4 🔴 Pinned inferences survive the change

**FR12** — Every pinned inference records the `agent config_hash` that produced it, and remains readable
and attributable after the definition shape changes. **FR13** — A definition change SHALL NOT silently
re-run pinned inferences. Re-inference is an explicit act, presented as a diff — the P30 rule, which now
also governs a *configuration* change and not only a source change. **FR14** — Where a stored inference's
`config_hash` refers to a definition shape no longer authorable, the surface renders it as **stale with its
producing configuration named**, never as absent and never as current. **FR15** — A single-node definition
carrying no loop ref, no graph declaration and no second node SHALL hash **byte-identically** to its
pre-P36 form, so the platform's own history is not invalidated by a shape it did not use.

### 6.5 Safety properties preserved

**FR16** — Every axis is a reference; no field can hold a credential value; the reflective fence asserting
this extends to the new fields. **FR17** — Rehearsal before activation, per definition, including
multi-node. **FR18** — `CapChecker` enforces the spend ceiling before every provider call, on every node.
**FR19** — `ErrNoChange` still refuses to mint a duplicate version. **FR20** — A definition this build
cannot execute is refused at publish, naming axis and node.

### 6.6 Attribution

**FR21** — Every inference records the node that produced it. **FR22** — A finding surfaced to a customer
([P33](P33-surface-assessment.md)) resolves to the node and definition version that produced it, on the
operator side. The customer sees a finding; the operator can see which node made it.

---

## 7. Non-functional requirements

**7.1 Determinism.** Unchanged and now harder: an inference runs once per `(source_revision, agent
config_hash)`, content-addressed and pinned. With concurrency inside the agent, the *interleaving* varies
while the pinned result must not. Anything order-dependent in the merge is a determinism bug.

**7.2 Cost.** A multi-node agent can multiply provider spend by node count per assessment. `CapChecker`
enforces the ceiling before every call; the ceiling is per assessment, not per node, or adding a node
silently raises the budget.

**7.3 Blast radius.** A bad agent definition degrades every tenant's assessments at once. Rehearsal (FR17)
and staged rollout are the controls, and both already exist in `internal/herosagent`.

**7.4 Operator surface integrity.** P26's product is a **build fence** that makes oversight drift fail
rather than accumulate. Nine axes and N nodes is a large increase in operator surface, and the fence must
grow with it — an axis or a node kind that has no operator surface should fail the build, not go unnoticed.

---

## 8. System design summary

### 8.1 Shape

```
Definition (operator-authored)
  nodes[]                      ← NEW: was one implicit node
    node_id                    ← NEW: data, not a package constant
    prompt_ref  model_ref  credential_ref
    skill_refs  tool_names
    context_ref memory_ref
    harness_ref                ← envelope (P34)
    loop_ref                   ← NEW (P34)
  order[]                      ← NEW: meaningful once N > 1
  edges[]                      ← NEW: incl. conditional (P34)
  graph_groups[]               ← NEW: concurrency + merge (P34)
        │
        ▼
  same typed-contract validation as a customer Variant Spec   (FR8)
        │
        ▼
  publish → rehearsal → activate → pinned inference per (source_revision, config_hash)
```

### 8.2 Decisions

**D1 — One validator, shared with customers (FR8).** The platform's own agent is validated by the code that
validates a customer's spec. A parallel validator for our own configuration would be the place where a rule
is quietly weaker, and it would be discovered by a customer.

**D2 — `NodeID` becomes data, and single-node stays the default.** The constant is a shape assumption
threaded through the package. Making it data is the change; making multi-node *mandatory* is not, and
single-node must keep hashing identically (FR15).

**D3 — The wiring refusal narrows rather than disappears (FR11).** `ErrWiringOverride`'s reason was *"there
is no second node to order it against"*. That reason is still true for a single-node definition. Deleting
the refusal outright would discard a correct rule because a different case appeared.

**D4 — A configuration change is treated exactly like a source change for pinning (FR13).** P30's rule was
written about source revisions. The same argument applies verbatim to configuration: a result that changes
under a customer between two page loads is worse than no result, and it does not matter which input moved.

**D5 — HEROS does not propose changes to itself.** The verification gate cannot referee a change to the
thing that produces the measurements. This is a non-goal stated as a decision because it will be proposed —
it is the obvious next step and it is a circularity, not a feature.

**D6 — The spend ceiling is per assessment, not per node.** Otherwise adding a node raises the budget as a
side effect of a topology change, which is the least visible way to spend more money.

---

## 9. Design by role lens

### 9.1 Senior Product Designer — *reduce the input, never the truth*

Nine axes and N nodes is a large surface, and the temptation is progressive disclosure that hides axes an
operator has not used. P30's own rule forbids it: *"a hidden axis is indistinguishable from one that does
not exist."* Collapse, do not omit — and where an axis is unavailable in this build, say so with its reason.

The default must stay a single node. An operator who wants what they have today should not have to build a
graph to get it.

### 9.2 Senior System Designer — *arbitrate by level; do not open a one-way door*

The door is **the definition's shape**, because the shape is hashed and the hash keys every pinned
inference. FR15's byte-identical guarantee for the single-node case is the mechanism that keeps the door
open, and it is the same expand-contract discipline ADR-014 uses one layer down. Getting it wrong is not a
bug that shows up in a test — it shows up as every assessment silently re-running at cost, weeks later.

The second door is **self-modification** (D5). Once an agent may change its own definition, "verification
decides" has a circularity in it, and no amount of gating fixes an evaluator grading its own configuration.

### 9.3 Senior Backend Dev — *schema, migration and code must land together*

`NodeID` as a constant is a shape assumption threaded through `definition.go`, `axiseditor.go`,
`inferencestore.go`, `placement.go` and the fences. Every one must move together, and the reflective fence
asserting no field can hold a credential must be extended to the new fields — a fence that enumerates the
old shape passes vacuously on the new one.

Migration: the definition store gains node and topology structures. Repeatable, success on a second run,
idempotency guard named in the commit body. Existing single-node rows must read back byte-identically
(FR15) — assert it, do not assume it.

### 9.4 Senior Frontend Dev — *do not lose a feature in a rename*

The axis editor gains two axes and a node dimension — the highest-risk UI revision in the program. Inventory
the current editor first; every control either has a destination or is deliberately removed with agreement.

`AxisWiring` moves from read-only-with-a-reason to editable, and the reason text must not simply be deleted:
for a single-node definition it is **still correct** (FR11), so the surface renders the refusal
conditionally rather than dropping it.

P26's build fence (§7.4) must cover the new axes and node kinds, or the operator console drifts out of
coverage exactly where the surface grew.

### 9.5 Senior AI Engineer — *an aggregate hides the single-sample defect*

Multi-node HEROS must be evaluated **per node**, not as one agent. A definition whose analyst is excellent
and whose critic never disagrees scores well as a whole and is broken in the half that matters — the critic
exists to disagree, and a critic that never does is a passthrough with a budget.

Rehearsal (FR17) is the control-variable instrument the platform already has: run the calibration set,
compare against the active definition, per node and per axis. And FR12's attribution is what makes a
regression traceable to a node instead of to "the agent".

Determinism under internal concurrency (§7.1) needs its own check: run the same pinned inference repeatedly
and assert byte-identical output. Anything order-dependent in a merge fails this, and it fails
intermittently, which is the worst way to find out.

### 9.6 Senior DevOps Engineer — *blast radius, reversible, observable, least privilege*

Blast radius is every tenant at once — this is the platform's own agent, not a per-tenant configuration.
Controls: rehearsal before activation, staged rollout, and the kill switch, all of which
`internal/herosagent` already carries (`rehearsal.go`, `rollout.go`, `readiness.go`).

Observable: per-node inference counts, latencies, spend and failure rates on a readable health endpoint.
Per-node, because an aggregate over a graph tells you the agent is slow and not which node is.

Reversible: activating a previous definition version is the rollback, and it must be one act — not a
re-authoring of the older shape.

### 9.7 Senior QA Engineer — *green is worth having only if green can be red*

1. A single-node definition with no loop ref and no graph declaration hashes **byte-identically** to
   pre-P36. **The most important fence in the phase.**
2. An existing pinned inference remains readable and attributable after the shape change.
3. A definition change does **not** silently re-run pinned inferences.
4. A stale pin renders as stale **with its producing configuration named** — not absent, not current.
5. The reflective no-credential fence covers the **new** fields; add a key-shaped field and it must fail.
6. A single-node definition still refuses an ordering (FR11).
7. A fan-in with no merge is refused at publish.
8. A loop needing an unavailable host service is refused at **publish**, not at run.
9. Rehearsal is required before activating a multi-node definition.
10. Spend ceiling is per assessment: adding a node does not raise the budget.
11. The same pinned inference re-run repeatedly under concurrency → byte-identical output.
12. P26's build fence covers every new axis and node kind — remove an operator surface and the build fails.
13. The customer's spec validator and the agent's are the **same code path** — assert it, because two
    lookalikes is the failure D1 is about.

### 9.8 Senior Sales Operations — *only promise what shipped; state the boundary out loud*

This is the strongest thing the program produces for a buyer, and it must be said precisely. Sayable:
*"The platform's own agent is configured through the same nine axes we expose to you — including its
topology — and it is rehearsed and version-pinned before activation."*

Not sayable: that it optimizes itself. It does not (D5), deliberately, and the reason is worth saying out
loud because it is the more credible statement: *an evaluator that grades its own configuration is not an
evaluator.* A vendor that names that circularity is more trustworthy than one that markets past it.

---

## 10. Dependencies

| Needs | From | Hard? |
|---|---|---|
| `loop` and `graph` as axes | [P34](P34-harness-loop-graph-split.md) | **hard — P36 cannot start first** |
| shared topology validator | P34 / `internal/typedcontract` | hard |
| the agent, its pinning, rehearsal, rollout, caps | P30 | hard — exists |
| operator console + build fence | P8 / P26 | hard |
| registries | P2 | hard |

---

## 11. Risks & mitigations

| Risk | Severity | Mitigation |
|---|---|---|
| Definition shape change orphans every pinned inference | **critical** | FR15 byte-identical single-node; QA fences 1–4. This fails weeks later as silent re-running at cost. |
| A second, weaker validator for our own agent | **high** | D1/FR8; QA fence 13 asserts one code path. |
| The credential fence passes vacuously on new fields | **high** | §9.3; QA fence 5 adds a key-shaped field and requires failure. |
| Non-determinism from internal concurrency | **high** | §7.1; QA fence 11 — and it fails intermittently, so it must be run repeatedly. |
| Cost multiplies silently with node count | med | D6/FR18 — ceiling per assessment, not per node. |
| A bad definition degrades every tenant at once | med | Rehearsal, staged rollout, kill switch — all already built. |
| The axis editor loses controls in the re-cut | med | §9.4 — inventory first, named destination or agreed removal. |
| Self-modification proposed as the obvious next step | med | D5 states the circularity now, so it is a decision rather than a debate later. |

---

## 12. Rollout & test strategy

1. **Nine axes, still one node.** `loop` and `graph` authorable; graph refuses on a single node (FR11).
   Fence 1 green — nothing may change hash.
2. **Multi-node, sequential.** N nodes with an ordering; no concurrency, no conditional edges. Rehearsal
   required.
3. **Concurrency and merge**, with the determinism fence (11) run repeatedly, not once.
4. **Conditional edges**, through the shared `expr` path.

Rollback at every stage is activating a previous definition version — one act, and the reason it must not
require re-authoring the older shape.

---

## 13. Success metrics & acceptance criteria

| # | Criterion | How it is checked |
|---|---|---|
| A1 | Single-node definition hashes byte-identically to pre-P36 | the fence |
| A2 | Existing pins readable and attributable after the change | fixture from before |
| A3 | A definition change does not silently re-run pins | call-count assertion |
| A4 | A stale pin renders stale with its producing configuration named | render test |
| A5 | Credential fence covers new fields | add a key-shaped field; must fail |
| A6 | Single-node definition still refuses an ordering | publish test |
| A7 | Fan-in with no merge refused at publish | publish test |
| A8 | Loop with an unavailable host service refused at publish, not at run | publish test |
| A9 | Rehearsal required before activating multi-node | activation test |
| A10 | Spend ceiling per assessment, not per node | add a node; budget unchanged |
| A11 | Repeated pinned inference under concurrency is byte-identical | repeated-run test |
| A12 | P26 build fence covers new axes and node kinds | remove a surface; build fails |
| A13 | Agent and customer specs share one validator | code-path assertion |
| A14 | Per-node inference counts, latency, spend and failures on a health endpoint | endpoint assertion |

---

## 14. Open questions

| # | Question | Why it is open |
|---|---|---|
| **Q1** | Does a multi-node HEROS have **per-node credentials**, or one credential for the definition? | Per-node lets a cheap model triage and an expensive one analyse — the main reason to want a graph. It also multiplies the credential surface and complicates `CapChecker`'s per-assessment ceiling. **Recommendation: per-node, since `CriticModelRef` already established the precedent for a second model.** |
| **Q2** | Is the customer ever shown that a finding came from a specific agent node? | FR22 keeps it operator-side. Showing it is more transparent and leaks the platform's internal topology. **Recommendation: operator-side only; the customer sees evidence, not our architecture.** |
| **Q3** | Should `placement` be per node? A definition could run cheap extraction customer-side and analysis platform-side. | Powerful, and it makes the placement gate — currently one function both runners call and neither can skip — a per-node decision. That is exactly the kind of change that turns an unskippable check into a skippable one. |
| **Q4** | What happens to an in-flight assessment when a new definition is activated mid-run? | Finishing under the old definition is consistent; switching mid-run produces a report with two configurations in it. **Recommendation: finish under the definition it started with, and record which.** |
| **Q5** | Does the rehearsal calibration set need to grow for a multi-node definition? | A calibration set sized for one node may not exercise a fan-in or a conditional edge at all, in which case rehearsal passes without testing the new capability — which is the shape of a fence that cannot go red. |
