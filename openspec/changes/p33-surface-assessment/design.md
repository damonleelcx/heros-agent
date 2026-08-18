# Design — P33: Surface Assessment

## Context

An assessment is mostly **absence** on first contact. A repository the platform has just met will have
several axes it cannot determine, one or two it cannot measure, and at least one it cannot assess at all
in the target language. So the design problem is not "how do we score" — it is "how do we report absence
in a way a reader acts on rather than discounts".

Three ways an assessment can lie, in order of how exposed this phase is to each:

1. **Render absence as zero.** A surface with no evidence showing `0` reads as measured-and-zero.
2. **Report an aggregate that hides a broken part.** On the production console today, `LLM FALLBACK
   CALLS · 0` renders as *"Fully rule-covered — no model was consulted"*; on `openclaw` it means the rules
   covered nothing and the fallback did not run. Two opposite states, one number.
3. **Assert what was not measured.** A model reading code, stated with a measurement's confidence.

## D1 — Four states, and `not_measured` carries its cause

**Decision.** `measured` | `observed` | `not_measured` | `refused`, and `not_measured` names the missing
input.

**Why four rather than three.** Collapsing `observed` into `measured` conflates *true by construction*
(the code binds three tools) with *true by experiment* (this variant scored 0.81 ± 0.05). They warrant
different confidence and support different actions. Collapsing `refused` into `not_measured` conflates
"we could not" with "this build cannot", and only the second is actionable by us rather than by the
customer.

**Why the cause is mandatory.** `not_measured` without a cause is a shrug. With one — *"the sandbox could
not run node X"* — it is a task. The whole difference between a report someone acts on and one they
discount is in that string, which is why it is a requirement and not a nicety.

**Open.** PRD §14 Q1 asks whether readers will actually hold `observed` and `measured` apart. Worth
validating with real readers before stage 3 rather than after.

## D2 — Structural before inferred, always

**Decision.** Deterministic extraction runs first; HEROS infers only on the residue.

**Why.** Not performance. It **minimises and exposes** the proportion of the report that rests on a model.
A reader can see at a glance how much of the assessment was read from their code and how much was guessed
about it, and that ratio is itself information about how well the platform understands their stack.

**Consequence.** Improving a language frontend directly shrinks the inferred portion of every future
assessment for that language. That is the right incentive, and it is the opposite of what happens if
inference runs first and structure is used only to check it.

## D3 — No composite (ruling R4)

**Decision.** No score, grade, maturity level, or ranking spanning axes. A fence asserts no path emits one.

**Why the fence and not just the absence.** A composite is the single most likely thing to be added later
by request, in a hurry, by someone who did not read this document. An absence is not self-defending; a
fence is. The fence is also the artifact that makes the refusal reviewable — someone can read it and see
that the decision was made rather than overlooked.

**Rejected.** *A composite marked "indicative".* The qualifier survives exactly as far as the first
screenshot.

## D4 — Decisiveness travels with every score

**Decision.** `n_cases`, oracle coverage and `NIndecisive` are rendered beside the score, and the cases
are enumerable.

**Why beside, not behind a link.** A property that changes how a number should be read must be visible at
the same time as the number. Behind a link, it is read by the people who already suspected the number,
which is the wrong half.

**Why it is cheap.** `CoverageView` already computes all three. This is a rendering decision that has been
available since P4 and was never taken, and P30 named the gap precisely: a reader sees `n=5 seeds ·
8 cases` and *"cannot answer the only question that matters: 8 cases of what?"*

## D5 — Evidence links into existing surfaces

**Decision.** A finding's evidence reference resolves to the graph, the board or the scorecard. The
assessment is an **index** over existing evidence.

**Why.** The alternative — recomputing a number for the assessment view — makes the report a second source
of truth for a statistical claim, which is the console's founding prohibition with an extra hop.

## D6 — An empty graph is a fact about the frontend

**Decision.** Zero edges from a language whose frontend emits none is reported as a missing input naming
the language and the frontend, never as the repository having a flat graph.

**Why.** P30's most misleading surface was a header asserting full coverage over a graph with zero labels.
The bug looked like copy; the cause was stating a property of the **tool** as a property of the
**subject**. Any new surface reporting on a repository is exposed to the same inversion at every axis, so
it is stated once here as a rule rather than fixed nine times later.

## D7 — Record what produced the finding, not just the finding

**Decision.** Each finding records the agent `config_hash` **and the provider model version**.

**Why the model version specifically.** An assessment's numbers move for three reasons: the source moved,
the agent configuration moved, or the provider silently changed the model. Only the first is a fact about
the customer. Pinning holds the second. Without the third recorded, a provider's routine upgrade is
rendered as the customer's repository getting worse, and nobody has any way to tell.

## Data-model sketch

```
Assessment
  assessment_id
  tenant_id, workflow_id, source_revision
  agent_config_hash          ← D7
  started_at, completed_at
  spend_usd, spend_cap_usd   ← §7.3

Finding                      ← exactly nine per assessment
  axis         ∈ {model, prompt, skills, tools, context, memory, harness, loop, graph}
  state        ∈ {measured, observed, not_measured, refused}
  origin       ∈ {structural, inferred}
  claim
  evidence_ref               ← resolves into an EXISTING surface (D5); required
  missing_input              ← required when state = not_measured
  refusal_cause              ← required when state = refused; names frontend | analysis | language
  provider_model_version     ← D7; required when origin = inferred
  inference_address          ← content address of the pin; required when origin = inferred

EvalSetReport                ← attached to a measured finding
  n_cases, n_seeds
  oracle_coverage
  n_indecisive               ← cases whose oracle can never fail
  vacuous_dimensions[]
  cases[]                    ← enumerable (D4); each with its oracle and whether it can fail
```

Note that four fields are conditionally required. That is deliberate: a `not_measured` finding with no
`missing_input` should be impossible to construct, not merely discouraged, which means the constraint
belongs in the type and in the schema rather than in a code review.

## Risks this design accepts

- **Nine axes × four states is thirty-six render cells**, and the temptation is to render `not_measured`
  as a dimmer `observed`. It is a different message, not a dimmer one. Frontend owns this and it is the
  most likely place for the design to erode quietly.
- **Inference quality is a classifier's quality**, with a per-axis precision and an abstention rate. An
  aggregate over axes hides the one axis that is broken — the standing warning of the AI lens, applying to
  this phase's central mechanism.
- **The manager wants a number** (PRD §4) and this design refuses to give one. That is a sales problem
  the sales lens owns, and it is solved by saying it first, not by softening the design.
- **Assessments are durable artifacts describing customer source.** Retention and export are PRD §14 Q4,
  and P32 already found that even the *snapshot* retention rule is not written down as a number anywhere
  in the tree. This design does not pretend to inherit a rule that does not exist.
