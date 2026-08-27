# P36 The Agent Is a Graph — capability statement and claim discipline (Sales Operations)

- **Status:** Accepted (2026-08-27)
- **Audience:** anyone who describes P36 to a customer — a deck, a demo, a scoping call, an SoW, a
  security review, a renewal.
- **Rule:** this phase has **one claim worth making and one claim that must be refused out loud**. The
  refusal is the more valuable of the two, and it is only valuable if you say it first.

---

## 1. The capability, in one paragraph

The platform's own analysis agent is configured through **the same nine axes we expose to you** — prompt,
model, skills, tools, context, memory, harness, loop and **graph** — and it is now a **graph** rather
than a single call: it declares nodes, each with its own bindings, an ordering, edges, concurrency and a
merge. Its topology is validated by **the same code path** a customer's Variant Spec goes through. It is
**rehearsed** against a pinned calibration set before it can be activated, and every result it has ever
produced stays **pinned to the configuration that produced it**.

**The one-line version:** *we configure our own agent with the product we sell you, including its
topology — and we hold it to the same gates.*

---

## 2. ✅ Sayable on ship (task 10.1)

> **The platform's own agent is configured through the same nine axes we expose to you — including its
> topology — and it is rehearsed and version-pinned before activation.**

Every clause is load-bearing and every one is checkable:

| Claim | Where it is true |
|---|---|
| **The same nine axes** — not a parallel settings store | `AuthorableAxes()` DERIVES eight from `variantspec.Dimensions()` and appends `graph`; the operator console's build fails if any of the nine has no surface |
| **Including its topology**, and by the same validator | `variantspec.ValidateTopology` is one exported function that `Resolve` (customers) and `Publisher.Publish` (the agent) both call; the fence asserts the agent's refusal **contains the customer's verbatim** |
| Every axis is a **reference into a registry**, never an inlined value | a node has no field that can hold a strategy's params; `TestLoopAndGraphAreReferencesNeverInlined` |
| **Rehearsed** before activation, on every fixture individually | `Publisher.Activate` refuses any version whose state is not `passed`, and the store refuses independently |
| …and per **node**, not just as one agent | a definition whose second node contributes nothing FAILS, even when the merged numbers are good |
| …and refused outright when the calibration set **cannot exercise** what the definition declares | a warning beside a passing verdict is a warning that arms the gate |
| **Version-pinned**: every result names the configuration that produced it | the pin key is `(workflow, source_revision, agent config_hash)`, and a definition change never silently re-runs one |
| A single-node definition **keeps its identity** across this change | its bytes and its `config_hash` are reproduced from a fixture recorded before the change existed |
| Rollback is **one act** — activating a previous version, never re-authoring | `Publisher.Rollback` takes a hash and nothing else |
| No field anywhere can hold a **provider key** | a reflective fence walks every type in the package; adding a key-shaped field to the new node struct makes it fail |

**The demo that lands.** Open the operator console's Analysis Agent page. Show that the agent is three
nodes with per-node models — *a cheap one triages, an expensive one analyses, a critic runs only when the
analyst declined*. Then try to publish a fan-in with no merge, and let them read the refusal:

> *2 nodes converge on "merge" and no merge is declared. This is REFUSED rather than defaulted:
> first-result-wins, concatenate and last-writer are all semantic choices about YOUR program…*

Then say: **that is the same sentence your spec would get, from the same function.** The refusal is the
demo, not the graph.

---

## 3. 🚫 NOT sayable, and why you say the reason out loud (task 10.2)

> **Do not say the agent optimizes itself.**

Not "not yet". Not "on the roadmap". It is **refused by decision** ([`decisions.md`
D-36.8](../../openspec/changes/p36-agent-self-configuration/decisions.md)), and the decision has a reason
that is more persuasive than the feature would have been.

**Say this instead, unprompted, the first time anyone asks whether it tunes itself:**

> *It does not, and we refuse to build it. Diagnosis proposes; verification decides — and verification
> is performed by measurements this agent produces. An agent proposing a change to its own configuration
> is an evaluator grading itself, and no gate fixes that, because whatever gates it is running on the
> configuration being judged. An operator authors it, through the same nine axes, and it is rehearsed
> and pinned before it serves anything.*

**Why naming the circularity beats marketing past it.** The customer is buying an evaluator. If we are
willing to let our own evaluator grade its own configuration, they have no reason to believe our
evaluation of *theirs* — the credibility we spend defending self-optimization is the exact credibility
the product is sold on. A vendor who says "we deliberately did not build the impressive-sounding thing,
and here is the reasoning" is making the strongest available argument that the measurements are real.

🔴 **It is enforced, not merely written.** `proposalgen` refuses any pass whose subject is the platform's
own agent — **before it reads any store**, so the path whose purpose is to propose changes never even
loads the platform's own graph. `TestNoProposalTargetsTheAgentsOwnDefinition` proves the ordering by
running it on a Generator with no stores wired at all.

### Also not sayable

| Do not say | Because | Say instead |
|---|---|---|
| "It learns from every analysis" | Memory is scoped to ONE inference; a repository analysed twice starts cold both times | "It is deterministic per revision: the same revision always shows you the same graph, and it costs nothing to ask again" |
| "It picks the best model for each node automatically" | An operator binds every model, deliberately, and a second model is a second cost made visible | "You can see exactly which model each node uses and what each one costs" |
| "Adding nodes makes it more thorough" | The spend ceiling is per **assessment**; adding a node does not raise the budget | "Adding a node changes the shape, not the budget — the ceiling is per assessment on purpose" |
| "Our agent runs on your infrastructure as a graph" | The customer-side link contract is single-node and a multi-node definition is REFUSED over it | "Graph-shaped analysis runs platform-side; customer-placed analysis is single-node today" |

---

## 4. The three questions a technical buyer will ask

**"How do I know your agent is really configured the way you say?"**
Its topology goes through the same validator yours does — one exported function, called from both paths,
with a fence asserting the two produce the same sentence. If we had written a second validator for
ourselves, that is exactly where our rule would have become quietly weaker than yours.

**"What happens when you change it?"**
Nothing, to your stored results. A configuration change is a **pinning event**: results produced under
the previous definition stay readable and stay attributed to it. Re-inference is an explicit act shown as
a diff. Activating a new definition makes **zero** provider calls — asserted by counting them, not by
the absence of an error.

**"What if a change is bad?"**
Rolling back is one act: we activate the previous version. We do not re-author it — retyping a
configuration under pressure produces a different `config_hash` on any transcription error, which is a
third configuration nobody has measured, activated in place of the one known to work.

---

## 5. Noun dictionary (task 10.3)

The nine axes are named **identically** on the operator console, the customer console, the CLI and the
docs. Use these words and no synonyms:

| Axis | What it is |
|---|---|
| `model` | which model a node calls |
| `prompt` | the instruction it is given |
| `skills` | the platform capabilities bound at the call site |
| `tools` | which of the tools already offered to the model it keeps |
| `context` | how a single call builds its message list |
| `memory` | what persists **between** turns and sessions |
| `harness` | the execution **envelope** — the imposed policy: ceilings, host services, sandbox posture |
| `loop` | the **iteration policy** — which control loop runs, what stops it, how many turns |
| `graph` | the **topology** — nodes, edges, concurrency, conditional routing, merge |

🔴 **`wiring` is retired.** It was this platform's word for the topology axis while that axis was
vacuous. The product's word is `graph`, and an API request naming `wiring` is refused **by name with the
new name stated** rather than silently accepted — a rename that quietly accepts the old spelling never
finishes, and the dictionary ends up with two entries for one thing.

🔴 **`harness` and `loop` are two axes, not one.** The line is *imposed vs chosen*: a ceiling is a policy
about blast radius imposed on an author; a turn count is a value chosen by one. Saying "harness" when you
mean "loop" tells a customer that tightening a spend ceiling and changing a reflection prompt are the
same kind of edit, and they are reviewed by different people.

The dictionary is enforced by `TestTheNineAxesAreNamedIdenticallyOnEverySurface`, which reads the
operator vocabulary, the shared assessment enum the customer console and CLI are generated from, and this
document — and fails if any of the three disagrees.
