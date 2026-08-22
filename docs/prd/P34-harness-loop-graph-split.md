# PRD — P34: Harness / Loop / Graph — three axes, not one

| | |
|---|---|
| **Phase** | P34 |
| **Program** | [Graph Engineering Harness Agent (GEHA)](P31-P38-graph-engineering-agent-program.md) |
| **OpenSpec change** | [`p34-harness-loop-graph-split`](../../openspec/changes/p34-harness-loop-graph-split/) |
| **Lead roles** | System Designer + AI Engineer |
| **Support roles** | Backend Dev, Frontend Dev, QA, DevOps, Product Designer, Sales Operations |
| **Upstream** | P0 (config_hash, golden vectors) · P2 (registries) · P5 (typed contracts, re-arrangement) · P15 (wiring) · P18 (harness) · [ADR-004](../adr/ADR-004-runtime-config-binding.md) · [ADR-014](../adr/ADR-014-harness-loop-graph-axis-split.md) |
| **Unblocks** | [P33](P33-surface-assessment.md) reporting on `loop` and `graph` · [P36](P36-agent-self-configuration.md), the point of the program |
| **Status** | In implementation — §14 Q1–Q5 answered and folded in (see [`decisions.md`](../../openspec/changes/p34-harness-loop-graph-split/decisions.md)) |

---

## 1. Summary

Three customer sentences currently land on one axis, and one of them lands nowhere at all:

| The customer says | Should be | Lands on today |
|---|---|---|
| "stop after four turns; reflect between them" | loop | `harness` |
| "never spend more than a dollar and never reach the network" | harness | `harness` |
| "run these two calls in parallel and merge the results" | graph | **inexpressible** |

The third row is not a naming problem. `VariantSpec.Order` is a `[]string` — *"the node ordering the
executor walks"* — and a linear sequence cannot say two nodes are concurrent. `Edge.Kind` is a closed
two-value set, `"data" | "control"`, so there is no conditional edge. `internal/arrangements` optimizes
topology by calling `nextPermutation` over that list. **The platform's graph optimization is sequence
permutation**, which is a real capability and is not graph engineering.

The first two rows *are* a naming problem and an expensive one: an operator tightening a spend ceiling and
an engineer changing a reflection prompt edit the same registry kind, produce the same class of
`config_hash` change, and are reviewed by the same person with nothing in the axis to tell them apart.

P34 splits them. `loop` becomes a `Dimension`; `harness` narrows to the execution envelope; `graph`
becomes a spec-level axis that can express concurrency and conditionality. The reasoning is
[ADR-014](../adr/ADR-014-harness-loop-graph-axis-split.md); this PRD is what gets built and what it must
not break.

**The phase's centre is a refusal, and it is unusual: P34 refuses to finish.** The clean version of this
change moves the loop fields out of `HarnessSpec`. Doing so would change the `version_id` of every harness
entry that has one — `registry.Kind` is hashed into the `version_id` — which changes the `config_hash` of
every spec referencing it, which **orphans every measurement ever taken on a multi-turn node**. So the
expand half ships and the contract half is refused on the record. The residue is permanent and this
document does not pretend otherwise.

---

## 2. Problem & context

### 2.1 What the enum says

`Dimensions()` returns seven: `model`, `prompt`, `skills`, `context`, `tools`, `memory`, `harness`. There
is no wiring or graph dimension — node ordering lives outside the enum in `internal/arrangements`.

`DimHarness`'s own doc comment defines it as two things: *"the SCAFFOLD around a node's call — **how many
turns it runs and in what control loop**"*.

The five loops are real and already closed, in
[`strategy.go:88`](../../internal/harnessruntime/strategy.go): `single-shot`, `reflexion`, `react-loop`,
`plan-execute`, `critic-loop`, with a closed stop-condition vocabulary — `answer-marker`, `no-tool-call`,
`plan-complete`, `max-turns` — and `TurnCeiling = 16`.

### 2.2 What is already spec-level, so graph is an extension rather than greenfield

This is the finding that most changes the size of the phase:

- `VariantSpec` already carries `Edges []Edge`, and `Validate` already refuses an edge to a node the
  ordering does not know about — *"or the graph the executor walks is not the graph the author described"*.
- `InsertedAdapter` already models a synthetic node materialized onto an edge (P5 Decision 3), *"an
  EXPLICIT, inspectable node carrying its own io_contract — not a hidden runtime coercion"*.
- **`HarnessGroups` already exist** — *"harnesses that wrap an ordered edge set rather than a single node
  (P18 FR15). Additive and omitempty: a spec that declares none serialises byte-identically to a pre-P18
  spec."*

P18 has already crossed from per-node to spanning-a-subgraph, and did it additively. P34 follows the
precedent rather than inventing one.

### 2.3 Why `TurnCeiling` decides the split line

`boundedCeiling` refuses a `max_turns` above the ceiling and says why: *"the ceiling is a policy about how
much autonomous tool-calling one node may do, and honouring a value the registry would not seal would make
this a second and looser gate."*

That sentence is the split line. A **policy about blast radius** belongs to the envelope; the **value
chosen within it** belongs to the loop. One is imposed on the author, the other chosen by them, and an
operator raising a ceiling is doing something categorically different from an engineer picking four
instead of two.

---

## 3. Goals & non-goals

### Goals

1. **G1** — `DimLoop` exists, sealed by a `registry.KindLoop`, carrying strategy, stop condition,
   `max_turns`, reflection prompt and critic binding.
2. **G2** — `DimHarness` narrows to the execution envelope: sandbox posture, host services, ceilings,
   retries, timeouts, concurrency limits, guardrail and approval gates.
3. **G3** — `graph` becomes a spec-level axis that can express **concurrency** and **conditional routing**,
   and can **merge** a fan-in.
4. **G4** — Every existing `config_hash` stays resolvable. The P0 golden vectors keep reproducing.
5. **G5** — A spec that sets both a legacy loop-bearing `harness_ref` and a new `loop_ref` is **refused at
   resolve**, naming both refs.
6. **G6** — The eval harness does not learn that any of these axes exist. Reduction shows up in the
   existing metric family.
7. **G7** — Every new topology form is gated by `internal/typedcontract`, unchanged, and refused with a
   typed `unsafeRewrite` where a codemod is not safe — never silently dropped.

### Non-goals (with the reason)

- **Removing the loop fields from `HarnessSpec`** — refused on the record in ADR-014. Not deferred:
  refused, because the contract step orphans stored measurements and nothing makes that acceptable later.
- **Rewriting existing registry entries.** Registry entries are content-addressed and immutable, enforced
  by DB trigger. An option that asks to defeat that is not an option.
- **Making `graph` a `Dimension`** — it would be the first member that is not a property of one node, which
  breaks the invariant letting the transform engine iterate `Dimensions()` and the harness stay
  axis-agnostic.
- **Distributed or cross-process concurrency.** Concurrency here is within one sandboxed run.
- **Reporting on these axes** — [P33](P33-surface-assessment.md).

---

## 4. Users & personas

| Persona | What the split gives them |
|---|---|
| Engineer authoring a change | a sentence lands on one axis, and the refusal when it cannot names that axis |
| Operator setting policy | ceilings and sandbox posture are a surface they own, separate from the loop an engineer picks inside it |
| Reviewer of a proposal diff | can tell from the axis whether a change altered the envelope or the iteration |
| The platform's own agent | can be more than one node — [P36](P36-agent-self-configuration.md) |

---

## 5. User stories

- **US1** As an engineer I set a loop strategy without touching anything about sandboxing or spend, so
  that my change's blast radius matches its intent.
- **US2** As an operator I raise a turn ceiling and no engineer's loop configuration changes, so that
  policy and choice are separately reviewable.
- **US3** As an engineer I declare two nodes concurrent and their results merged, so that fan-out is
  expressible at all.
- **US4** As an engineer I declare a conditional edge and it is validated against the program's lexical
  scope at resolve time, so that a predicate cannot name something that does not exist.
- **US5** As a reviewer I open a spec authored before P34 and it still resolves and still reproduces its
  measurement, so that history is not lost to a refactor.
- **US6** As an author who sets both a legacy harness ref and a loop ref, I am refused with both refs
  named, so that the ambiguity is mine to resolve rather than the resolver's to guess.
- **US7** As an engineer whose language cannot carry a concurrent group at the call site, I am refused by
  name, so that I do not get a diff nobody can trust.

---

## 6. Functional requirements

### 6.1 The loop axis (capability `loop-strategy`)

**FR1** — `DimLoop` is appended to the closed `Dimension` enum; `registry.KindLoop` seals a
`(strategy, params)` pair, and the Kind is hashed into the `version_id` as every other kind is.

**FR2** — The loop vocabulary is the existing five strategies and the existing four stop conditions. P34
adds no strategy; it relocates the ones that exist.

**FR3** — A loop entry naming a strategy this build does not implement **fails to resolve**, exactly as
`HarnessEntry` does today — never falling back to `single-shot`, because that would run one turn under a
multi-turn `config_hash`.

**FR4** — `max_turns` is validated against the harness's ceiling at resolve time. Zero and negative are
refused, not defaulted.

### 6.2 The harness axis, re-scoped (capability `harness-envelope`)

**FR5** — `DimHarness` carries the envelope: sandbox posture, host service requirement, turn ceiling,
spend ceiling, retries, timeouts, concurrency limit, guardrail and approval-gate bindings.

**FR6** — The ceiling is imposed on the loop, not chosen by it: a loop whose `max_turns` exceeds the
envelope's ceiling is refused at resolve, naming both values.

**FR7** — A loop requiring a host service (`react-loop` needs a tool executor, `plan-execute` a planner,
`critic-loop` a critic) is refused at resolve when the envelope does not provide it. Today `Run` refuses
at execution; moving it left makes it a preflight answer rather than a runtime failure.

### 6.3 Backward compatibility

**FR8** — Legacy loop-bearing `KindHarness` entries remain **resolvable indefinitely**. **FR9** — New
authoring cannot create one. **FR10** — A spec setting both a legacy loop-bearing `harness_ref` and a
`loop_ref` is refused at resolve, naming both. **FR11** — A spec with neither a `loop_ref` nor a graph
declaration serialises **byte-identically** to its pre-P34 form, and the P0 golden vectors reproduce.

### 6.4 The graph axis (capability `graph-topology`)

**FR12 — Concurrency.** A spec may declare that members of a group may run concurrently. `Order` remains
the deterministic walk; a group is declared **over** it, not instead of it, so a replay visits nodes in a
defined sequence even when the live run overlapped them.

**FR13 — Conditionality.** `Edge.Kind` grows a predicate member. A predicate is an expression, and it is
subject to the **same** binding rules ADR-004 already imposes on `expr`: declared and validated at
spec-resolve time, never inferred, and refused when a name is not in the program's lexical scope.

**FR14 — Merge.** A fan-in declares how its inputs are combined. A fan-in with no declared merge is
refused at validate — a fan-in whose results are dropped is not a topology, it is a bug with a diagram.

**FR15** — Every new form is validated through `internal/typedcontract`, unchanged, **before** any codemod
is generated. **FR16** — Where a call-site codemod is not safe for a language, the node carrying the
override is refused at transform with a typed `unsafeRewrite` naming node and axis — never silently
dropped, because a dropped override would let a variant's `config_hash` be scored against unchanged source.

**FR17** — Graph declarations are additive and `omitempty`; a spec declaring none hashes as before (FR11).

### 6.5 Coverage

**FR18** — Each of the three axes declares its honest `EXISTS / PARTIAL / ABSENT` status per language,
through the existing coverage contract, and a refusal names which of the frontend, the analysis or the
language support is missing.

---

## 7. Non-functional requirements

**7.1 Reproducibility.** FR11 is the hard one. The P0 golden vectors are the fence, and they must pass
unchanged before and after.

**7.2 Determinism under concurrency.** Concurrency makes wall-clock interleaving non-deterministic while
`config_hash` stays deterministic. P4's multi-seed statistics already assume run-to-run variance, so
ranking is unaffected — but **tracing and attribution must stop assuming a single linear span sequence**,
and that assumption is currently implicit in more than one place.

**7.3 Safety of the new gate surface.** A conditional edge is the first place a customer-authored
*expression* affects control flow. It reuses ADR-004's `expr` grammar and validation rather than getting
its own, because a second, looser expression path is a second place for the scope check to be wrong.

**7.4 No eval change.** G6. An axis needing a bespoke oracle is designed wrong.

---

## 8. System design summary

### 8.1 Shape

```
VariantSpec
  source_revision
  order[]            ← unchanged: the deterministic walk
  edges[]            ← + predicate kind (FR13)
  nodes{}            ← NodeOverride: … + loop_ref (FR1), harness_ref (re-scoped, FR5)
  harness_groups[]   ← unchanged (P18 FR15)
  graph_groups[]     ← NEW, omitempty: concurrency + merge (FR12, FR14)
        │
        ▼
   typedcontract validation (unchanged gate, FR15)
        │
        ▼
   transform → codemod, or typed unsafeRewrite naming node + axis (FR16)
```

### 8.2 Decisions

**D1 — Expand only; the contract half is refused, not deferred.** ADR-014 §Decision 1. Stated as a refusal
so that a later phase proposing it is amending an ADR.

**D2 — Ceiling is harness, value is loop.** §2.3.

**D3 — Graph is spec-level, not a `Dimension`.** ADR-014 §Decision 3.

**D4 — Concurrency is declared over `Order`, never instead of it.** Replacing the linear order with a DAG
would be the honest data model and would change the serialization of every spec, violating FR11. Declaring
groups over the order keeps the byte-identical guarantee and keeps replay deterministic.

**D5 — A predicate is an `expr` binding.** FR13 / §7.3.

**D6 — A fan-in without a merge is invalid, not defaulted.** Defaulting to "first result wins" or
"concatenate" would make a silent choice about semantics the author did not make.

---

## 9. Design by role lens

### 9.1 Senior Product Designer — *reduce the input, never the truth*

The input reduction is that a sentence lands on one axis. The truth that must not reduce is that `wiring`
was previously rendered read-only **with a reason** rather than hidden — HEROS's own axis editor argues it:
*"a hidden axis is indistinguishable from one that does not exist."* Every surface that gains `loop` and
`graph` inherits that obligation: where an axis is unavailable, say so and say why.

### 9.2 Senior System Designer — *arbitrate by level; do not open a one-way door*

The door is `config_hash`. ADR-014 shows the naive split trades a stability catastrophe for a maintenance
saving — an L1 violation of the eight-level rule, since level 2 may not be degraded to buy level 7.

The subtler door is **the predicate**. Once a customer-authored expression influences control flow, that
expression is a permanent part of the platform's evaluation surface, and every future question about
sandboxing and about what a predicate may reference traces back to the grammar chosen here. Reusing
ADR-004's is the decision that keeps it one grammar instead of two.

### 9.3 Senior Backend Dev — *schema, migration and code must land together*

A new `registry.Kind` touches the seal path, the DB trigger's argument, and every exhaustive switch over
kinds. **A `Kind` switch that compiles without the new case is a consumer that would have silently
mis-sealed a loop** — so exhaustiveness must be enforced, not hoped for.

The migration adds no column to a deployed table for the legacy path; it adds a new kind and new
`omitempty` fields. Every migration is repeatable and returns success on a second run, and the commit body
names the idempotency guard used.

### 9.4 Senior Frontend Dev — *do not lose a feature in a rename*

`/app/harness` and `/app/wiring` exist and describe today's meanings. After P34 there are three surfaces
where there were two, and the risk is that re-cutting the pages drops content — the standing failure mode
of a UI revision. Anything currently on those pages either moves to a named destination or is deliberately
removed with the user's agreement; nothing evaporates in the re-cut.

Where an axis is unavailable in a build, render it read-only with the reason (§9.1). No improvised
styling: the three axis pages reuse the existing axis-page structure.

### 9.5 Senior AI Engineer — *an aggregate hides the single-sample defect*

The proposal operators for `loop` and `graph` are new search spaces, and the honest measurement of a new
operator is *proposals that pass the P5.5 verification gate*, per axis, not a mean across axes. A graph
operator with a 5% pass rate hidden inside a healthy average is an operator that is not working.

Concurrency also changes what a trace looks like. Attribution localizes failures from spans; overlapping
spans are a new shape, and the ablation discipline applies — prove on a holdout that attribution does not
degrade under concurrency before it ships, with no pure-refactor exemption.

### 9.6 Senior DevOps Engineer — *blast radius, reversible, observable*

Concurrency multiplies a run's peak resource use by the group width. The envelope's concurrency limit
(FR5) is the control, and it is enforced by the sandbox rather than trusted from the spec.

Reversibility: the whole phase is additive, so rollback is ceasing to author the new fields. Existing
specs keep resolving under either version of the binary, which is the property that makes a rollback safe
and is worth asserting rather than assuming.

### 9.7 Senior QA Engineer — *green is worth having only if green can be red*

1. **P0 golden vectors pass unchanged.** The single most important fence in the phase.
2. A spec with no `loop_ref` and no graph groups serialises byte-identically to its pre-P34 bytes.
3. A pre-P34 spec with a loop-bearing harness ref still resolves and reproduces its `config_hash`.
4. Both refs set → refused at resolve, **naming both**.
5. `max_turns` above the envelope ceiling → refused, naming both values.
6. A loop needing a host service the envelope does not provide → refused at **resolve**, not at run.
7. A fan-in with no merge → refused at validate.
8. A predicate naming an out-of-scope symbol → refused, through the ADR-004 path.
9. A concurrent group in a language whose transform cannot carry it → typed `unsafeRewrite` naming node and
   axis; assert the override was **not** silently dropped.
10. A `registry.Kind` switch missing the new case → build fails.
11. Attribution under overlapping spans, on a holdout, does not degrade.

### 9.8 Senior Sales Operations — *only promise what shipped; state the boundary out loud*

Sayable when it ships: three named axes, and — for the first time — parallel steps and conditional routing
as things the platform can configure and verify.

Not sayable: that the platform "orchestrates" anything. It configures and verifies a customer's own graph.

The boundary to state out loud is the residue: **specs authored before this change keep working and keep
their measurements**, which is a stronger claim than most platforms can make and is worth saying — and the
honest half is that a legacy path exists permanently as the price of it.

---

## 10. Dependencies

| Needs | From | Hard? |
|---|---|---|
| `Dimension` enum, `config_hash` | P0 / P2 | hard |
| golden vectors | P0 | hard — the fence |
| typed contracts | P5 | hard |
| `expr` binding grammar | ADR-004 | hard |
| the five loops, `TurnCeiling` | P18 / `harnessruntime` | hard |
| `HarnessGroups` precedent | P18 FR15 | soft — precedent, not code dependency |
| coverage/refusal contract | P13 `language-coverage` | hard |

---

## 11. Risks & mitigations

| Risk | Severity | Mitigation |
|---|---|---|
| A "cleanup" later removes the legacy path and orphans measurements | **high** | Refused on the record in ADR-014; QA fence 3 asserts a legacy spec still resolves. |
| `config_hash` drifts for an unchanged spec | **high** | FR11, QA fences 1–2, golden vectors unchanged. |
| The predicate grammar diverges from `expr` | **high** | FR13/D5 — one grammar, one validation path. |
| A fan-in silently drops results | **high** | FR14/D6 — refused at validate, never defaulted. |
| Attribution silently degrades under concurrency | med | §9.5 — holdout before ship, no refactor exemption. |
| Content is lost re-cutting `/app/harness` and `/app/wiring` | med | §9.4 — every item has a named destination or an agreed removal. |
| A `Kind` switch silently misses the new case | med | QA fence 10 — exhaustiveness enforced. |
| Peak resource use multiplies with group width | med | §9.6 — sandbox-enforced concurrency limit. |

---

## 12. Rollout & test strategy

1. **`DimLoop` + `KindLoop`, modeled and authorable, nothing removed.** Golden vectors green; legacy specs
   resolve. Nothing on any surface changes yet.
2. **Harness re-scoped**, ceilings enforced at resolve, host-service refusals moved left from run.
3. **Graph: concurrency and merge**, behind the typed-contract gate, in the language with the strongest
   frontend first; every other language refuses by name.
4. **Graph: conditional edges**, through the ADR-004 `expr` path.

Rollback at each stage is ceasing to author the new fields — nothing is removed, so no rollback needs a
migration.

---

## 13. Success metrics & acceptance criteria

| # | Criterion | How it is checked |
|---|---|---|
| A1 | P0 golden vectors pass unchanged | the fence |
| A2 | A no-override spec serialises byte-identically to pre-P34 | byte comparison |
| A3 | A pre-P34 loop-bearing spec resolves and reproduces its `config_hash` | fixture from before the change |
| A4 | Both refs set → refused, naming both | resolve test |
| A5 | `max_turns` above ceiling → refused, naming both values | resolve test |
| A6 | Missing host service → refused at resolve, not at run | preflight test |
| A7 | Fan-in without merge → refused at validate | validate test |
| A8 | Out-of-scope predicate → refused via ADR-004 | binding test |
| A9 | Unsupported language → typed `unsafeRewrite`, override not dropped | per-language coverage test |
| A10 | No eval, scorer or metric change | diff review of P4 surfaces |
| A11 | Attribution does not degrade under concurrency | holdout ablation |
| A12 | `Kind` exhaustiveness enforced | build fails on a missing case |

---

## 14. Open questions — ADR-014 deferred these here; all five are now ANSWERED

**Status: settled.** Every answer is recorded, with its rejected alternatives and the level each was
rejected on, in [`decisions.md`](../../openspec/changes/p34-harness-loop-graph-split/decisions.md). The
table below is the summary; that file is the contract of record.

| # | Question | **Answer** | Recorded as |
|---|---|---|---|
| **Q1** | Do **spend** ceilings sit with harness, or with loop? | **Harness.** The split line is *imposed vs chosen*, not *who consumes it* — by the consumption test the turn ceiling would belong to the loop too, and §2.3 already rejected that reading. Spend is inexpressible on a loop entry. Exhaustion is reported as a named **stopping condition**, not an error. | D-34.1 |
| **Q2** | Is a predicate restricted to the `expr` grammar, or does it get a narrower one? | **Reuse `expr`.** A second grammar is a second scope validator, and the looser one becomes the way in. One grammar means one place to narrow it if `expr` proves too permissive — which is only possible because there is one place. | D-34.2 |
| **Q3** | What is a concurrent group's failure semantics — fail fast, or run all and merge partials? | **Declared on the merge, required, from a closed set** `{fail-fast, collect-partial}`. No default, no global rule. 🔴 A `collect-partial` merge whose downstream input contract makes every member required is **refused at validate** — otherwise it is a promise the type system does not keep. | D-34.3 |
| **Q4** | Do `/app/harness` and `/app/wiring` become three pages, or one axis page with three sections? | **Three sibling axis pages** — `/app/harness` (envelope), `/app/loop` (new), `/app/graph` (supersedes `/app/wiring`, which redirects rather than 404s). Confirmed with the user, together with §7.2's rule: carry everything, ask before removing anything. | D-34.4 |
| **Q5** | Does the legacy loop-bearing harness path get an end-of-life **date**, or is it genuinely permanent? | **Permanent. No date.** A date does not shorten ADR-014's orphaning chain, it hands it to somebody else on a day when the reasoning has been forgotten. Removal is an **amendment to ADR-014**, re-confirmed at the end of the phase by task 11.2. | D-34.5 |

Two further contracts were settled at the same time and are recorded beside them, because neither was
safe to leave to whoever got there first:

| # | Question | **Answer** |
|---|---|---|
| **D-34.6** | How is the still-open P18 change reconciled with this one? | P18 defines the harness axis as carrying **both** the scaffold and the control loop. Folding it unchanged after this change would restore the conflation. Whichever change folds second reconciles; both changes now carry the instruction, and `tasks.md` §2.6 owns it. |
| **D-34.7** | What does each of the three axes declare about its own coverage? | `EXISTS`/`PARTIAL`/`ABSENT` per language through `transform.StatusFor`, **derived from the table each rewriter dispatches on** — never hand-declared. `graph`'s refusal names which of the frontend, the analysis or the language support is missing (FR18), never a generic unsupported state. |

<details>
<summary>The original framing of each question, kept for the record</summary>

| # | Question | Why it was open |
|---|---|---|
| **Q1** | Do **spend** ceilings sit with harness, or with loop? | §2.3 settles *turn* ceilings by `boundedCeiling`'s own argument. Spend is less obvious: a spend cap is a blast-radius policy (harness) but is consumed almost entirely by loop iterations. **Recommendation: harness**, for the same reason as turns — it is imposed, not chosen. |
| **Q2** | Is a predicate restricted to the `expr` grammar, or does it get a narrower one? | `expr` was designed for a value binding, not a control-flow decision. A narrower grammar is safer and is a second grammar. **Recommendation: reuse `expr`, and if it proves too permissive, narrow it in one place.** |
| **Q3** | What is a concurrent group's failure semantics — fail fast, or run all and merge partials? | This is a semantic choice that must not be defaulted (D6's reasoning applied to failure). It probably belongs in the merge declaration rather than as a global rule. |
| **Q4** | Do `/app/harness` and `/app/wiring` become three pages, or one axis page with three sections? | §9.4's real risk is content loss in the re-cut; the answer decides how large that re-cut is. |
| **Q5** | Does the legacy loop-bearing harness path get an end-of-life **date**, or is it genuinely permanent? | ADR-014 says permanent. A date would be more honest to maintainers and would re-open the orphaning problem on that date, which is the argument for having none. |

</details>
