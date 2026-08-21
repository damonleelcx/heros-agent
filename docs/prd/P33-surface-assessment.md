# PRD — P33: Surface Assessment

| | |
|---|---|
| **Phase** | P33 |
| **Program** | [Graph Engineering Harness Agent (GEHA)](P31-P38-graph-engineering-agent-program.md) |
| **OpenSpec change** | [`p33-surface-assessment`](../../openspec/changes/p33-surface-assessment/) |
| **Lead roles** | AI Engineer + Product Designer |
| **Support roles** | System Designer, Backend Dev, Frontend Dev, QA, DevOps, Sales Operations |
| **Upstream** | [P32](P32-repo-intake.md) (source) · [P31](P31-conversational-console.md) (somewhere to report) · P1 (IR) · P3.5 (pattern classifier) · P4 (evalgen, harness, stats) · P29 (the `not reported` vocabulary) · P30 (HEROS, pinned inference) |
| **Unblocks** | [P35](P35-autonomous-improvement-run.md) — nothing can be proposed without a finding to propose against |
| **Status** | Proposed — awaiting sign-off on §14 |

---

## 1. Summary

The customer asked: *score the repo*. This phase is the answer, and the answer is deliberately not a score.

Ruling **R4** of the program is that assessment produces **evidence-backed per-surface findings and no
composite number**. That is not caution; it is the platform's founding principle applied to a new surface.
Every score this system has ever produced is *comparative and verified* — variant against variant,
multi-seed, ties declared when confidence intervals overlap — because *diagnosis proposes, verification
decides*. An absolute "your repository is 62 out of 100" is a model's judgement rendered in a metric's
typeface, and there is no held-out set that could make it true.

So P33 reports, for each of the nine axes: **what is there, what evidence says so, and where there is no
evidence, that there is none.** The last clause is the phase's real product. P29 already established the
vocabulary for it — a node the platform was not told about renders `not reported`, never `0` — and P33
extends that discipline from one field to an entire assessment.

There is one genuinely new measurement in the phase and it is worth naming because it is what makes the
findings more than an inventory: for surfaces where the platform can construct a **baseline eval set**
from the repository's own behaviour, a finding may carry a measured number. Where it cannot, the finding
says so. Both are results; only one is a measurement.

---

## 2. Problem & context

### 2.1 Discovery finds call sites; nobody has ever asked it what the repository's *strategy* is

`internal/discovery` extracts every LLM call site into the Workflow IR — nodes with source spans, models,
prompts, tools, context. That is a parts list. "This repository's memory strategy is a per-session store
that is never pruned" is a claim about the *design*, and nothing produces it.

The closest thing is `internal/patternclassifier`, which labels agentic patterns from graph topology. P30
documented exactly how far that gets: **seven of its eight detectors are topology predicates and the
eighth needs registry-bound skills**, so on a repository with no edges *"0/22 can fire by construction"*.
The classifier is not weak; it was starved.

### 2.2 The three ways an assessment can lie, and which one this phase is most exposed to

1. **Assert what was not measured.** A finding stated with the confidence of a measurement, produced by a
   model reading code. This is the default failure and R4 exists because of it.
2. **Report an aggregate that hides a broken part.** P30's own example is on the screen today: the graph
   header renders `LLM FALLBACK CALLS · 0` as *"Fully rule-covered — no model was consulted"*, when on
   `openclaw` it actually means *the rules covered nothing and the fallback did not run either*. Two
   opposite states, one number, four lines of TSX.
3. **Render absence as zero.** A surface with no evidence showing `0` reads as "measured, and it is zero".

**(3) is the one this phase is most exposed to**, because an assessment is mostly absence on first
contact. A repository with no memory strategy at all and a repository whose memory strategy could not be
determined are different findings, and the difference is the whole value of the report.

### 2.3 What eval generation can and cannot do here

`internal/evalgen` generates eval sets and `evalharness` runs them multi-seed with confidence intervals.
That machinery is real and P33 uses it unchanged. What it needs is a **task** — an input and a way to
judge an output. For a repository whose call sites the platform can execute in a sandbox, that exists.
For one it cannot execute — no runnable entry point, missing credentials, an external dependency the
sandbox refuses — it does not, and no amount of generation invents it.

`evalboard`'s `CoverageView` already models the interesting failure: `NIndecisive`, *"cases carrying an
oracle that can never fail — the most misleading cases in the set"*. An eval set that cannot fail scores
perfectly. P33 must surface that property, not the score it produces.

---

## 3. Goals & non-goals

### Goals

1. **G1** — For each of the nine axes, report what the repository does, with the evidence that supports it.
2. **G2** — Where a surface cannot be determined or measured, report `not_measured` with the **named
   missing input**, never a zero and never silence.
3. **G3** — Distinguish **structural** findings (read from the IR and the tree — deterministic) from
   **inferred** findings (produced by HEROS reading source — pinned, and labelled as inference).
4. **G4** — Where a runnable baseline exists, generate an eval set and report measured numbers with
   confidence intervals, using P4 unchanged.
5. **G5** — Report each eval set's **decisiveness**: how many of its cases carry an oracle that can never
   fail. A set that cannot fail is reported as such, beside the score it produced.
6. **G6** — Produce **no composite score**, no letter grade, and no ranking of one repository against
   another.
7. **G7** — An assessment is reproducible: same source revision, same agent config, same findings.

### Non-goals (with the phase that owns them)

- **Proposing or applying improvements** — [P35](P35-autonomous-improvement-run.md).
- **Getting source in** — [P32](P32-repo-intake.md).
- **Rendering the conversation** — [P31](P31-conversational-console.md).
- **Making `loop` and `graph` configurable** — [P34](P34-harness-loop-graph-split.md). P33 may *report* on
  them only once P34 has landed, or it names axes the configuration layer does not have.
- **Benchmarking against other customers.** A cross-tenant comparison is a cross-tenant read; the platform
  has spent P27 making that structurally hard, and this phase does not undo it.
- **A maturity model.** Levels are composites with extra steps.

---

## 4. Users & personas

| Persona | The question they are really asking | What P33 owes them |
|---|---|---|
| Application engineer | "is anything obviously wrong?" | findings ordered by evidence strength, not by severity guessed |
| Staff engineer | "what do we actually do for memory across these twelve nodes?" | a structural answer they can check against the code |
| Engineering manager | "how bad is it?" | the honest answer that there is no single number, and what to read instead |
| Skeptical reviewer | "how do you know?" | every finding's evidence, reachable in one click |

The manager's row is the uncomfortable one, and §9.8 treats it as a product problem rather than pretending
it is not.

---

## 5. User stories

- **US1** As an engineer I ask "what's wrong with this agent?" and get one finding per axis, each with its
  evidence, so that I can check the claim rather than trust it.
- **US2** As an engineer I see that my repository's context strategy is `not_measured` **because the
  sandbox could not run node X**, so that I know what to fix to get an answer.
- **US3** As a staff engineer I can tell which findings were read from the code and which were inferred by
  a model, so that I weigh them differently.
- **US4** As an engineer I see that the generated eval set has 8 cases of which 3 carry an oracle that
  cannot fail, so that I do not read 0.94 as a strong result.
- **US5** As an engineer I re-run the assessment tomorrow on an unchanged revision and get identical
  findings, so that the report is a fact about my repository rather than about the day.
- **US6** As a manager I ask for an overall score and am told there is not one, **and why**, so that the
  absence reads as rigour rather than as an unfinished feature.
- **US7** As a reviewer I open a finding's evidence and reach the existing surfaces — the graph, the board,
  the scorecard — rather than a second copy of them.

---

## 6. Functional requirements

### 6.1 The assessment (capability `surface-assessment`)

**FR1** — An assessment covers all nine axes. An axis is never omitted; an axis with nothing to say
reports `not_measured` with its missing input.

**FR2** — Every finding carries: axis, claim, **evidence reference**, `origin` ∈ {`structural`,
`inferred`}, and `state` ∈ {`measured`, `observed`, `not_measured`, `refused`}.

- `measured` — a number from an eval run, with its interval.
- `observed` — read deterministically from the IR or the tree; true by construction, not by measurement.
- `not_measured` — the named missing input.
- `refused` — this build cannot assess this axis for this language/target, named the way the coverage
  contract already names refusals.

**FR3** — A finding with `origin: inferred` SHALL be labelled as an inference wherever it is rendered, and
SHALL carry the pinned inference's content address.

**FR4** — The system SHALL NOT emit a composite score, grade, level or ranking across axes, and SHALL NOT
rank one tenant's repository against another's.

**FR5** — Findings are ordered by **evidence strength** (`measured` → `observed` → `inferred` →
`not_measured`), not by a guessed severity. A severity that is itself an inference must not order a list
that a reader will treat as a priority queue.

### 6.2 Structural extraction

**FR6** — For each axis the platform extracts what the IR and the tree already determine: models and
params per node; prompt sources; bound skills and offered tools; context assembly at each call site;
memory reads and writes between turns; loop structure (turn counts, stop conditions); harness envelope
(sandboxing, ceilings, retries); graph topology (nodes, edges, concurrency, conditional routing).

**FR7** — Where the topology is empty because no frontend emitted an edge, the finding SHALL say **that**,
naming the language and the frontend, and SHALL NOT report the repository as having a flat graph. This is
P30's defect stated as a requirement so it cannot recur in a new surface.

### 6.3 Inference

**FR8** — Where structural extraction is insufficient, HEROS may infer, and every inference runs **once**
per `(source_revision, agent config_hash)`, content-addressed and pinned — the P30 rule, unchanged.
**FR9** — Re-inference is an explicit act whose output is shown as a diff against the pin. **FR10** — An
inference that cannot reach a conclusion returns `not_measured` with a named missing input; it does not
return a low-confidence conclusion.

### 6.4 Eval-set generation and measurement

**FR11** — Where the platform can execute the workflow in the sandbox, it generates an eval set with P4
unchanged and reports the result with its confidence interval, multi-seed. **FR12** — Every reported eval
result carries its **decisiveness**: `n_cases`, oracle coverage, and the count of cases carrying an oracle
that can never fail (`NIndecisive`, which `evalboard.CoverageView` already computes). **FR13** — The cases
themselves SHALL be enumerable. A count is not a case list, and P30 named this absence explicitly: *"a
reader cannot answer the only question that matters: 8 cases of what?"*. **FR14** — Where execution is
impossible, the axis reports `not_measured` naming which of the four it was: no runnable entry point,
missing credential, sandbox refusal, or unsupported language.

### 6.5 Reproducibility

**FR15** — Two assessments of the same `(source_revision, agent config_hash)` produce identical findings,
including identical evidence references. **FR16** — An assessment records the agent `config_hash` that
produced it, so a finding can be attributed to the configuration that made it.

---

## 7. Non-functional requirements

**7.1 Statistical honesty.** Every measured number carries its interval. Ties are declared when intervals
overlap. No number is reported without the size of the set behind it.

**7.2 Determinism.** FR8's pinning is the mechanism. A graph that changes under a customer between two
page loads is worse than no graph, and an assessment that changes is worse than no assessment.

**7.3 Cost.** An assessment spends provider tokens. Spend is bounded per assessment, attributed to the
tenant, and visible before it is spent for anything above a threshold. A budget refusal is `not_measured`
with `budget exhausted` as the named missing input — a first-class outcome, not an error page.

**7.4 Privacy.** Prompt text and source are inputs to a computation on the platform's side of the
boundary; they are not transmitted anywhere and are not stored as text by this phase. Evidence references
point at artifacts the platform already holds.

**7.5 Language coverage.** The coverage contract is a total function over every registered language.
`refused` names which of three things is missing — the frontend, the analysis, or the language support —
never a generic "unsupported".

---

## 8. System design summary

### 8.1 Shape

```
source snapshot ──▶ discovery ──▶ Workflow IR ──┐
                                                 ├──▶ structural extractors (9 axes) ──▶ observed findings
      pattern classifier ◀───────────────────────┘                                          │
                                                                                            │
      HEROS inference (pinned per source_revision × config_hash) ──▶ inferred findings ──────┤
                                                                                            │
      evalgen ──▶ evalharness (multi-seed, CIs) ──▶ measured findings + decisiveness ────────┤
                                                                                            ▼
                                                                                     Assessment
                                                                          (9 axes × {measured | observed
                                                                           | not_measured | refused})
```

### 8.2 Decisions

**D1 — Four states, and `not_measured` carries its cause.** The state set is the phase. Three states would
collapse "we looked and there is nothing" into "we could not look", and those lead to different actions by
different people.

**D2 — Structural before inferred, always.** An axis is extracted deterministically first; inference runs
only on the residue. This is not a performance decision — it is what keeps the *proportion* of the report
that rests on a model as small as the repository allows, and it makes that proportion visible.

**D3 — No composite (R4).** The alternative was offered and refused. The reasoning is recorded in the
program document so that re-opening it is a decision rather than a drift.

**D4 — Decisiveness travels with every score.** `evalboard.CoverageView` already computes `NIndecisive`
and *"none of it reaches a screen that shows which cases"*. A score without it is the aggregate hiding the
broken part — §2.2(2) — and this phase is where that gets fixed rather than repeated.

**D5 — Evidence links into the existing surfaces.** A finding's evidence reference resolves to the graph,
the board or the scorecard. The assessment is a new **index** over existing evidence, never a second place
the number is computed.

**D6 — Empty topology is a finding about the frontend, not about the repository** (FR7). P30's most
misleading surface was a header asserting full coverage over a graph with zero labels. That is a
copy-shaped bug with a design-shaped cause, and the cause is stating a property of the *tool* as a
property of the *subject*.

---

## 9. Design by role lens

### 9.1 Senior Product Designer — *reduce the input, never the truth*

One question replaces nine investigations. The truth that must not reduce is the shape of the report:
nine axes, always all nine, each in one of four states. A report that silently omits the axes it could not
assess is shorter, prettier, and lies by construction.

The hardest copy in the phase is `not_measured`. It must read as *"here is what is missing and what you
could do about it"*, not as *"we failed"*. That is the difference between a report someone acts on and one
they discount.

### 9.2 Senior System Designer — *arbitrate by level; do not open a one-way door*

The one-way door is **the composite score**. Once a number is on a screen it is quoted in a deck, written
into a contract, and used to compare teams — and withdrawing it later is a product regression no matter
how well the reasoning is written. Refusing it now costs level-3 legibility; shipping it risks level-1
correctness of a claim the platform cannot defend. The ladder decides this without argument.

The second door is quieter: **the shape of a finding**. Every consumer downstream — the conversation, the
proposal engine, the delivery record — will key on `axis`, `state` and `origin`. Getting the state set
wrong is expensive to change once three consumers read it, which is why D1 is stated before any of them
are built.

### 9.3 Senior Backend Dev — *a 200 is not evidence of a write*

An assessment that returns 200 with nine findings has not necessarily persisted nine findings, and the
schema-code coherence rule applies directly: adding a column to the finding record without the baseline
and the migration means `INSERT OR IGNORE` swallows rows while the endpoint keeps reporting success.
Acceptance is the live four-step: run → `SELECT` the findings → assert nine axes present → assert each
carries a resolvable evidence reference.

Event names: `assessment.run.started`, `assessment.axis.not_measured`, `assessment.inference.pinned`,
`assessment.inference.replayed` — central enum, no literals. Every `not_measured` emits a WARN carrying
the named missing input, because a silent fallback to a default is precisely the logging rule's target.

### 9.4 Senior Frontend Dev — *four states stay four*

Nine axes × four states is the render matrix, and every cell must have a design. The temptation is to
render `not_measured` as a greyed-out version of `observed`; it is a different message, not a dimmer one.

`origin: inferred` needs a persistent, non-decorative marker — not a tooltip. A reader scanning the report
must be able to see, without hovering, how much of it a model wrote.

Hazard palette: `refused` may use it. `not_measured` may not — absence is not a hazard, and the palette is
only legible while it is rare.

### 9.5 Senior AI Engineer — *an aggregate hides the single-sample defect*

This is the lens that owns the phase, and it has three obligations.

**First, the inference needs a holdout.** Findings produced by a model reading source are a classifier's
output, and the metric is per-axis precision — with **abstention** counted as a success, not a miss.
FR10's "return `not_measured` rather than a low-confidence conclusion" is only real if abstention is
rewarded by the evaluation.

**Second, decisiveness is the anti-aggregate.** `NIndecisive` exists in `CoverageView` and reaches no
screen. A generated eval set whose oracles cannot fail scores 1.0, and a report that shows 1.0 without
showing that property is the exact failure this lens is defined by.

**Third, control variables.** An assessment's numbers move when the source moves, when the agent config
moves, and when the provider moves. Only the first is a fact about the customer. Pinning (FR8) holds the
second; the third needs the model version recorded on the finding, or a provider's silent upgrade is read
as the customer's repository getting worse.

### 9.6 Senior DevOps Engineer — *blast radius, reversible, observable, least privilege*

An assessment executes customer code in the sandbox and spends provider tokens. Both are bounded: sandbox
posture is P3's, unchanged, and spend is capped per assessment with the cap enforced before the call
(§7.3). A budget refusal must degrade to `not_measured`, never to a partially-scored report presented as
complete.

Observability: assessments started / completed / refused, per axis and per state, on a readable health
endpoint. "How many assessments produced nine `not_measured` findings" is the single best early signal
that a frontend or the sandbox broke, and it is invisible in an aggregate success rate.

### 9.7 Senior QA Engineer — *green is worth having only if green can be red*

1. A repository where an axis genuinely cannot be assessed → `not_measured` with a **named** missing
   input. Mutate the extractor to return a default and the test must fail.
2. A repository with zero edges → the graph finding names the **frontend**, not a flat graph (FR7).
3. An eval set with an oracle that can never fail → decisiveness reports it, beside the score.
4. Assessment run twice on an unchanged revision → byte-identical findings, and **no provider call** on
   the second run.
5. A composite score cannot be produced: assert no code path emits one, as a fence, because it is the
   thing most likely to be added later by request.
6. Nine axes present in every assessment, including a repository that fails at every axis.
7. Fixture repositories must map real schemas and real language frontends — an inline simplified fixture
   would test an extractor against a tree no customer has.

### 9.8 Senior Sales Operations — *only promise what shipped; state the boundary out loud*

The manager in §4 wants a number and will not get one. That has to be handled by saying it first and
saying why: *"We report what we measured and what we could not. We do not produce an overall score,
because there is no held-out set that would make one true — and a number you cannot defend is worse than
no number."* Said proactively that is a differentiator; discovered by a prospect in a demo it is a gap.

Do not say the platform "audits" or "grades" a repository. It assesses nine axes and reports evidence.
Noun dictionary: the axes are named exactly as the console and CLI name them, and `workflow` keeps meaning
the target program's call graph.

---

## 10. Dependencies

| Needs | From | Hard? |
|---|---|---|
| a source snapshot | [P32](P32-repo-intake.md) | hard |
| somewhere to report | [P31](P31-conversational-console.md) | hard |
| `loop` and `graph` as real axes | [P34](P34-harness-loop-graph-split.md) | **hard for those two axes only** — see §3 non-goals |
| pinned inference | P30 | hard |
| eval generation + multi-seed stats | P4 | hard |
| `CoverageView.NIndecisive` | `internal/evalboard` | hard — exists, unrendered |
| sandbox | P3 | hard |
| coverage/refusal contract | P13's `language-coverage` | hard |

---

## 11. Risks & mitigations

| Risk | Severity | Mitigation |
|---|---|---|
| A composite score is added later "just for the summary" | **high** | R4 recorded with reasoning; QA fence 5 asserts no path emits one. |
| `not_measured` rendered as zero or omitted | **high** | FR1/FR2 and QA fence 1; four states have four designs. |
| Inferred findings read as measured | **high** | FR3, a persistent non-decorative marker, and ordering by evidence strength (FR5). |
| A perfect score from an eval set that cannot fail | **high** | FR12/FR13 — decisiveness and the case list travel with the score. |
| Empty graph reported as a property of the repository | med | FR7, D6 — the P30 defect stated as a requirement. |
| Provider model upgrade read as the repository degrading | med | §9.5 — record the model version on the finding. |
| Assessment cost surprises a tenant | med | §7.3 — cap, attribute, and disclose above a threshold. |
| P33 reports on `loop`/`graph` before P34 lands | med | Named in §3 and §10; those two axes report `refused` until the axes exist. |

---

## 12. Rollout & test strategy

1. **Structural only.** Seven axes, `observed` and `not_measured` states, no inference, no eval. Fully
   deterministic, and every claim checkable against the tree.
2. **Add inference**, pinned, labelled, on internal repositories first. Per-axis precision and abstention
   measured against a holdout before any customer sees it.
3. **Add measurement** where a runnable baseline exists, with decisiveness reported from day one — not as
   a follow-up, because a score without it is the failure this phase exists to prevent.
4. **Add `loop` and `graph`** once [P34](P34-harness-loop-graph-split.md) has landed.

Rollback at every stage is disabling the newest source of findings; the report shrinks in *state*, never
in axis count.

---

## 13. Success metrics & acceptance criteria

| # | Criterion | How it is checked |
|---|---|---|
| A1 | Nine axes present in every assessment | including a fixture that fails at every axis |
| A2 | `not_measured` always names a missing input | mutation-verified fence |
| A3 | Inferred findings are distinguishable from measured, without hover | browser acceptance |
| A4 | Zero-edge repository names the frontend, not a flat graph | `openclaw`-shaped fixture |
| A5 | Decisiveness reported beside every score | eval set with a never-failing oracle |
| A6 | Eval cases are enumerable, not just counted | case-list assertion |
| A7 | Re-run on unchanged revision → identical findings, no provider call | content-address + call-count assertion |
| A8 | No code path emits a composite | fence over the emitters |
| A9 | Budget exhaustion degrades to `not_measured` | forced-budget test |
| A10 | Findings persisted, each with a resolvable evidence reference | live four-step: run → `SELECT` → resolve |
| A11 | Per-axis inference precision **and abstention rate** reported | holdout evaluation, never a single mean |

---

## 14. Open questions

| # | Question | Why it is open |
|---|---|---|
| **Q1** | Is `observed` genuinely distinct from `measured`, or will readers collapse them? | `observed` is true by construction (the code binds three tools) and `measured` is true by experiment (this variant scored 0.81 ± …). They are different epistemics. **Recommendation: keep four states**, and validate the copy with real readers before stage 3. |
| **Q2** | Who pays for an assessment's tokens — the tenant's plan allowance, or their own provider key? | P11's posture is that customers use their own keys and the platform never resells tokens. A platform-side inference (P30) is the platform's spend. An assessment does both, and the boundary between them needs stating. |
| **Q3** | Should the report include findings about the **absence** of a surface — "this repository has no memory strategy at all"? | That is a design opinion, not an observation, and it is exactly the kind that is useful and unfalsifiable. **Recommendation: state absence as `observed` (no memory reads or writes were found) and let P35 propose, so the opinion lives where verification can decide it.** |
| **Q4** | How long is an assessment retained, and is it exportable? | It is a durable artifact describing the customer's source. P23's data inventory would gain a row; P32 §14 Q4 already found that the snapshot retention rule is not written down anywhere. |
| **Q5** | Does the assessment run on a schedule, or only when asked? | Scheduled assessment is the input to any trend surface and is also unattended provider spend against an unattended clone (P32 §14 Q5). Both entitlements would need to compose. |
