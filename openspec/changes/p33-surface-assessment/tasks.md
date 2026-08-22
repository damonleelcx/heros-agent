# Tasks — P33: Surface Assessment

> **Implementation in progress.** Sections are completed in order; a section is checked only when every
> task in it is done and its tests are green.

## 1. System Designer — the shape before the consumers

- [x] 1.1 Freeze the finding shape: `axis`, `state`, `origin`, `claim`, `evidence_ref`, `missing_input`, `refusal_cause`, `provider_model_version`, `inference_address`. Three consumers will key on it (conversation, proposal engine, delivery record), so it is expensive to change afterwards.
- [x] 1.2 Make the conditional requirements structural: a `not_measured` finding with no `missing_input`, or an `inferred` finding with no `inference_address`, must be **unconstructable** — enforced in the type and in the schema, not in review.
- [x] 1.3 Answer PRD §14 Q1: are `observed` and `measured` genuinely distinct to a reader? Validate with real readers before stage 3.
- [x] 1.4 Answer PRD §14 Q2: who pays for assessment tokens — the tenant's plan allowance or their own provider key. P11's posture is that the platform never resells tokens; a platform-side inference is the platform's spend. The boundary needs stating.
- [x] 1.5 Answer PRD §14 Q3: is "this repository has no memory strategy" an `observed` finding or a P35 proposal.
- [x] 1.6 Answer PRD §14 Q4 (retention/export) and Q5 (scheduled assessment) with DevOps and Product.

## 2. Backend Dev — structural extractors

- [x] 2.1 One extractor per axis over the IR and the tree, each returning `observed` or `not_measured` with a named missing input. No extractor returns a default.
- [x] 2.2 Graph extractor attributes zero edges to the **frontend**, naming the language — never to the repository as a flat graph.
- [x] 2.3 Assessment persisted with exactly nine findings; schema, baseline migration and insert columns aligned in all three places, or `INSERT OR IGNORE` swallows rows while the endpoint reports success.
- [x] 2.4 Evidence references resolve into existing surfaces (graph / board / scorecard); a reference that does not resolve fails the write.
- [x] 2.5 Spend cap enforced **before** each provider call; exhaustion degrades remaining axes to `not_measured` with `budget exhausted`.
- [x] 2.6 Central event names — `assessment.run.started`, `assessment.axis.not_measured`, `assessment.inference.pinned`, `assessment.inference.replayed`, `assessment.budget.exhausted` — no literals. Every `not_measured` emits a WARN carrying its missing input.
- [x] 2.7 Assessment routes added to the P19 ingress as `Exact` paths.

## 3. AI Engineer — inference and its evaluation

- [x] 3.1 Inference runs **only on the residue** left by structural extraction (design D2).
- [x] 3.2 Pin per `(source_revision, agent config_hash)`, content-addressed; explicit re-inference renders as a diff.
- [x] 3.3 An inference that cannot conclude returns `not_measured` with a named missing input — never a low-confidence conclusion.
- [x] 3.4 Holdout set of repositories with known ground truth per axis, including repositories where the correct answer is "cannot be determined".
- [x] 3.5 Report **per-axis** precision and abstention rate. Abstention counts as a success, not a miss — otherwise 3.3 is unrewarded and will erode.
- [x] 3.6 Record `provider_model_version` on every inferred finding, so a provider upgrade is distinguishable from the repository changing (design D7).
- [x] 3.7 Control-variable run: same repository, vary only the agent config; then vary only the provider model. Both deltas must be attributable.

> ⚠️ **!!! NOT DONE, and it is a real gap: no holdout run against a real provider has happened.**
> The scorer, the holdout set, the abstention-as-success rule and the per-axis report are built,
> fenced and mutation-tested — but the suite that runs in `make go` drives a **scripted** analyst, so
> it measures the *harness* and says nothing about any model. `make assessment-holdout` is the real
> run; it needs `HEROS_HOLDOUT_PROVIDER`, `HEROS_HOLDOUT_MODEL` and a provider credential, none of
> which exists in a checkout. **Until it is run, the inference's per-axis precision and abstention
> rate are UNMEASURED**, and `internal/launch/capabilities.go` therefore wires `inference: nil` —
> PRD §12 stage 2's precondition is exactly this measurement.

## 4. Eval generation and decisiveness

- [x] 4.1 Generate an eval set where a runnable baseline exists; use P4 unchanged — no bespoke oracle, no new scorer.
- [x] 4.2 Surface `CoverageView`'s `OracleCoverage`, `NIndecisive` and vacuous-dimension list **beside** every score, not behind a link.
- [x] 4.3 Enumerate cases: each with its oracle and whether that oracle can fail. A count is not a case list.
- [x] 4.4 Four named reasons a workflow cannot be measured — no runnable entry point, missing credential, sandbox refusal, unsupported language — four distinct messages.
- [x] 4.5 A set in which every oracle can never fail is reported as unable to fail, and its score is not presented as evidence of quality.

> ⚠️ **!!! The eval RUNNER is a seam with no implementation in this repository.** `EvalRun` is
> declared, `Measurement` consumes it, and every decisiveness path above it is built and fenced — but
> generating and executing a real eval set needs the P3 sandbox to run customer code and a provider
> credential, which is PRD §12 **stage 3**. `MeasurableAxes()` returns empty on every deployment
> today, so no assessment reports a `measured` finding. That is the honest stage-1 report, and it is
> stated here rather than implied by a green suite. The seam refuses a stub deliberately: *"a stub
> returning plausible scores is indistinguishable from a working harness."*

## 5. Frontend Dev

- [x] 5.1 Nine axes × four states — thirty-six render cells, each with a design. `not_measured` is a different message, not a dimmer `observed`.
- [x] 5.2 `origin: inferred` marked persistently and non-decoratively; visible without hover.
- [x] 5.3 Findings ordered by evidence strength, not by guessed severity.
- [x] 5.4 Decisiveness rendered beside the score.
- [x] 5.5 Evidence links navigate into the existing surfaces; nothing is recomputed in this view.
- [x] 5.6 Hazard palette on `refused` only; `not_measured` is not a hazard.
- [x] 5.7 No colour / spacing / type / radius literals; `scan:tokens` stays green.

## 6. DevOps

- [x] 6.1 Assessments started / completed / refused, broken out **per axis and per state**, on a readable health endpoint.
- [x] 6.2 Alert on the rate of assessments returning nine `not_measured` findings — the earliest signal that a frontend or the sandbox broke, and invisible in an aggregate success rate.
- [x] 6.3 Sandbox posture unchanged from P3; assessment executes customer code under it, not beside it.
- [x] 6.4 Provider spend per assessment attributed to the tenant and exported.

## 7. QA — fences that can go red

- [x] 7.1 An axis that genuinely cannot be assessed → `not_measured` with a **named** missing input. Mutate the extractor to return a default; the test must fail.
- [x] 7.2 Zero-edge repository (an `openclaw`-shaped fixture) → the graph finding names the frontend, not a flat graph.
- [x] 7.3 An eval set whose oracle can never fail → decisiveness reports it beside the score.
- [x] 7.4 Cases enumerable, not only counted.
- [x] 7.5 Assessment run twice on an unchanged revision → byte-identical findings **and no provider call** on the second run.
- [x] 7.6 Fence: no code path emits a composite score, grade or level. This is the fence most likely to be needed later.
- [x] 7.7 Forced budget exhaustion → `not_measured`, and the partial report is not presented as complete.
- [x] 7.8 Nine axes present in an assessment of a repository that fails at every axis.
- [x] 7.9 A `not_measured` finding with no missing input cannot be constructed (1.2).
- [x] 7.10 Live four-step: run → `SELECT` the findings → assert nine axes → assert each evidence reference resolves.
- [x] 7.11 Fixtures map real schemas and real language frontends. An inline simplified fixture tests an extractor against a tree no customer has.

## 8. Product Designer + Sales Operations

- [x] 8.1 `not_measured` copy reads as "here is what is missing and what you could do", not as "we failed".
- [x] 8.2 Answer for the manager who asks for one number, written down: we report what we measured and what we could not; there is no held-out set that would make an overall score true.
- [x] 8.3 Never describe the product as grading or auditing a repository. It assesses nine axes and reports evidence.
- [x] 8.4 Noun dictionary: axes named exactly as the console and CLI name them; `workflow` keeps meaning the target program's call graph.

## 9. Sign-off

- [x] 9.1 PRD §14 Q1–Q5 answered and folded in.
- [x] 9.2 Confirm the `loop` and `graph` axes report `refused` until [P34](../p34-harness-loop-graph-split/) lands, and that this is stated rather than discovered.
- [ ] 9.3 Ruling R4 re-confirmed with the user before stage 3, since stage 3 is where the pressure for a composite arrives.

> ⚠️ **!!! 9.3 IS THE ONE TASK NOBODY BUT THE USER CAN CLOSE, and it is deliberately left open.**
> It asks for a *re-confirmation from a person*, not for an artifact. What is built is everything that
> makes the confirmation meaningful when it happens:
>
> - the refusal is **enforced structurally** — no column, no field, no view key and no function can
>   carry a composite, and `TestNoPathInTheProductEmitsAComposite` scans the domain, the wire and the
>   screen for both the vocabulary and the arithmetic;
> - the refusal is **drilled** — `make p33-fence-redcheck` ADDS a composite and asserts the fence goes
>   red, in both Go and TypeScript;
> - the answer for the manager who asks for one number is **written down** in
>   [`docs/sales/P33-surface-assessment-claims.md`](../../../docs/sales/P33-surface-assessment-claims.md) §2,
>   including what to say when they push;
> - the surface **states it in the first screen** rather than leaving its absence to be discovered.
>
> 🔴 Stage 3 (measurement) is where the pressure arrives, and stage 3 is blocked on its own
> precondition anyway — no deployment executes customer code in the sandbox, so `MeasurableAxes()`
> returns empty everywhere. **Do not check this box on the user's behalf.**
