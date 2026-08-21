# P33 Surface Assessment — capability statement and claim discipline (Sales Operations)

- **Status:** Accepted (2026-08-21)
- **Audience:** anyone who describes P33 to a customer — a deck, a demo, a scoping call, an SoW, a
  security review.
- **Rule:** this feature's honest description contains a **refusal**, and the refusal is the strongest
  thing about it. Say it first. Discovered by a prospect in a demo it is a gap; said proactively it is
  the differentiator.

---

## 1. The capability, in one paragraph

A customer points us at one revision of one repository. We report, for **each of nine surfaces** —
model, prompt, skills, context, tools, memory, harness, loop, graph — what their code does, what
evidence supports the claim, and **where there is no evidence, that there is none**.

Nine findings. Always nine. Each in exactly one of four states.

| State | What it means | Who acts |
|---|---|---|
| **measured** | a number from an eval run, with its confidence interval and the size of the set behind it | they read it, with the decisiveness beside it |
| **observed** | read deterministically from their code — true by construction | they can check it in their editor in thirty seconds |
| **not measured** | we looked and could not establish it, **and the finding names what was missing** | usually them: supply a credential, declare an entry point |
| **refused** | this build cannot assess that surface for that target, naming which of our parts is missing | **us** |

---

## 2. 🔴 THE REFUSAL — say this before they ask

> **There is no overall score. There is no grade, no maturity level, and no ranking against other
> customers. We report what we measured and what we could not.**

### Why, in the customer's terms

Every score this platform produces anywhere else is **comparative and verified** — variant against
variant, multi-seed, and a tie declared when the confidence intervals overlap. That discipline is the
product. An absolute *"your repository is 62 out of 100"* is a model's judgement rendered in a
metric's typeface, and **no held-out set exists that would make it true**.

A number you cannot defend is worse than no number, because the first time somebody asks *how was 62
computed* the answer is "we made it up" — and that answer discredits the nine findings that were real.

### What the manager gets instead (this is §8.2, written down)

They get a **distribution**: nine numbers that sum to nine. *"Four read from your code, one measured,
two we could not determine, two this build cannot assess."*

That shape answers the question they were actually asking — *how much of this do you actually know?* —
and it does something a single number cannot: it says **where the uncertainty is**. A repository with
four absences on `memory`, `harness`, `loop` and `graph` and five solid observations is in a
completely different position from one with four absences spread across `model`, `prompt`, `tools` and
`context`, and any composite would render them identically.

### If they push

Three sentences, in this order:

1. *"We can give you a number today. We cannot give you one you could defend in a design review, and
   the second is what you are buying."*
2. *"Here is what we can defend: every finding on this page links to the evidence behind it, and the
   ones with no evidence say so and say what is missing."*
3. *"If your board needs a trend, the honest trend is the count of surfaces we can establish going up
   as you give us more to read. That number moves for reasons you can point at."*

🚫 **Do not offer a composite "just for the summary", and do not offer one marked "indicative".** The
qualifier survives exactly as far as the first screenshot.

---

## 3. 🚫 Words this product does not use about a customer's repository

| Do not say | Say | Why |
|---|---|---|
| we **grade** your repository | we **assess nine surfaces and report evidence** | a grade is a composite; we do not produce one |
| we **audit** your repository | we assess it | audit implies a standard we are certifying against, and a completeness we do not claim |
| a **score** for your repo | the nine findings, and the tally | a score for a repository is the thing §2 refuses |
| **health**, **maturity**, **rating** | the state of each surface | all three are composites with extra steps |
| our analysis **covers** your repository | we read what your language's frontend can give us, and we say where it could not | the coverage claim is exactly the inversion §5 is about |

`web/console/scripts/scan-claims.mjs` fails the build on the first four. That gate covers the console;
your deck is a review responsibility, and saying so plainly is better than implying the gate covers
more than it does.

---

## 4. 🔴 The most misleading true sentence we could say

> *"We assessed your repository and found no memory strategy."*

Every word is defensible and the sentence is wrong. Static call-site extraction reads **one call at a
time**, and a memory strategy is a store read and written **between turns** — so on that axis the
platform is not reporting the absence of a strategy, it is reporting the absence of a way to see one.

The product says this instead, and you should too:

> *"A memory strategy lives between turns, and static extraction sees one call at a time. Nothing here
> says this repository has no memory — only that we have not looked between its turns yet."*

The same inversion is available on every axis, and the sharpest case is topology: a repository whose
graph shows **zero edges** has not been shown to have independent calls. It has been read by a
syntactic frontend that **emits no edges at all**. The product names the language and the frontend.
Never present an empty graph as a finding about their architecture.

---

## 5. What is shipped, and what is not — as of 2026-08-21

**Shipped and demonstrable:**

- All nine axes, four states, `not_measured` naming its missing input, the no-composite refusal, the
  evidence links, the ordering by evidence strength, the console surface, and reproducibility (the
  same revision and configuration return the same nine findings and cost nothing to re-run).

**Built, wired, and NOT switched on:**

- **Inference.** HEROS reading source to answer the axes structural extraction cannot. The code is
  complete; the gate is a holdout measurement of its per-axis precision and abstention rate, which has
  not been run. Until it is, `memory` and `harness` report `not_measured` and say why.
- **Measurement.** Generating an eval set and reporting a number needs the sandbox to execute customer
  code; no deployment does that yet, so **no assessment reports a `measured` finding today**.

**Deliberately absent:**

- `loop` and `graph` report `refused` until [P34](../prd/P34-harness-loop-graph-split.md) lands. That
  is stated on the page rather than discovered.
- Scheduled assessment. It runs when a person asks. See PRD §14 A5.

🔴 **Do not demo an inferred finding or a measured one.** Both render correctly against a fixture and
neither is produced by any deployment. A demo that shows one is a demo of a screenshot.

---

## 6. Cost, and who pays — the boundary, stated

| What runs | Whose provider account | Whose money |
|---|---|---|
| structural extraction | none — no provider call happens | nobody's |
| an inference (`origin: inferred`) | **ours** | **ours** |
| an eval run (`state: measured`) | **theirs**, exactly as every other eval in the product | theirs |

P7's commitment is unchanged and unaffected: **the platform never resells provider tokens**, and no
invoice line represents one. An assessment is bounded by a per-assessment spend cap enforced *before*
each provider call, and by the tenant's own ceiling on top. Exhausting either degrades the remaining
axes to `not_measured — budget` and marks the report partial; it never returns an error page and never
returns a shorter report presented as a complete one.

---

## 7. Noun dictionary (task 8.4)

| Word | Means | Does not mean |
|---|---|---|
| **axis** | one of the nine surfaces | a chart axis |
| **workflow** | the **target program's LLM call graph** — the thing being assessed | a CI pipeline, a process, a Heros feature |
| **finding** | one axis's result in one assessment | a defect, an issue, a violation |
| **assessment** | one run over one revision, producing nine findings | an audit, a scan, a grade |
| **evidence** | a reference into a surface the platform already holds | a report we generated for this |
| **not measured** | we looked and could not establish it | zero, none, absent, failed |

The axes are named **exactly** as the console rail and the CLI name them, and that equality is a test:
the seven that are also configuration dimensions are read from `variantspec.Dimensions()` rather than
retyped, so a rename in one place cannot leave the other behind.

---

## 8. The two questions a security review will ask

**"Does this send our source to a model?"** — Structural extraction sends nothing anywhere; it runs on
our side of the boundary. An inference sends **facts about** the code — node identifiers, file paths
and line numbers, whether a field resolved, which analyser ran — and **never prompt text or source
lines**. There is no field on the request shape that could carry one.

**"What do you keep?"** — The assessment: nine claims and their evidence references. No source text, no
prompt text. It is retained for the life of the workflow and exported as the JSON the schema describes,
whenever the customer asks. Revoking a repository connection deletes every tree we derived from it and
**does not** delete the assessments — a customer who wants those gone deletes the workflow, and the
revocation copy says so.
