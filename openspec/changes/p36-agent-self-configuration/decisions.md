# Decisions — P36: The Agent Is a Graph

Answers to PRD §14 Q1–Q5, plus the finding task 1.1 asks for, recorded where whoever proposes the
opposite will find it.

> **Task 1.1's finding is D-36.0 and it decides the size of the phase.** Read it first.

---

## D-36.0 — A nested `nodes` array cannot preserve the hash. A compatibility encoding is required.

**Task 1.1. Established before anything else was built, as the task requires.**

**The question.** Every pinned inference is keyed by `(workflow_id, source_revision,
agent_config_hash)`. Can a definition with one node, no `order`, no `edges`, no `graph_groups` and no
`loop_ref` marshal to byte-identical bytes and produce the same `config_hash` as its pre-P36 form —
with a nested `nodes` array, or does that need a compatibility encoding?

**The finding: a nested array cannot, and it is not close.** The pre-P36 document is a FLAT object whose
keys are the axes:

```json
{"prompt_ref":"prompt-v1","model_ref":"claude-opus-5","credential_ref":"anthropic",
 "context_ref":"ctx-v1","harness_ref":"harness-single-shot-v1"}
```

`confighash.SumBytes` canonicalises key ORDER and number spelling (RFC 8785). It does not canonicalise
STRUCTURE, and nothing could: `{"nodes":[{...}]}` and `{...}` are different documents with different
bytes and therefore different hashes. No arrangement of `omitempty` reaches across that — `omitempty`
removes keys, it does not un-nest them.

**The consequence, and why this is additive rather than a migration.** A compatibility encoding is
required, and the narrow one suffices:

| shape | wire document | hashed document |
|---|---|---|
| one node, id `heros_analyst`, no `loop_ref`, no `order`, no `edges`, no `graph_groups` | the pre-P36 flat object, byte for byte | the pre-P36 `canonicalDefinition`, byte for byte |
| anything else | `{"nodes":[…],"order":[…],…}` | `canonicalGraph` |

Both are pure functions of content, so identical content still yields an identical hash. A definition
that gains a second node changes its hash **because it is a different configuration** — which is what a
content address is for. What never happens is the thing D4 is about: a definition whose content did not
change acquiring a new hash because the CODE changed.

**So the phase is ADDITIVE.** No pin is migrated, no hash is rewritten, and `spec_json` rows written by
the previous binary decode unchanged — `Definition.UnmarshalJSON` discriminates on the PRESENCE of
`nodes`, not on a version field, because the rows that need reading were written before anybody could
have set one.

**The evidence.** `internal/herosagent/testdata/p36-pre-confighash.json`, recorded by the pre-P36 tree
with `P36_RECORD_PRE=1` before any P36 code existed (commit `proof(p36): record the pre-P36 definition
bytes`). It carries three artefacts per definition — the wire bytes, the hashed bytes and the hash —
because they fail differently: a wire mismatch means a stored row no longer reads back, a canonical
mismatch says WHICH key moved, and a hash mismatch is the orphaning itself.

**Mutation drill.** Forcing `legacyShaped()` to return false makes ten assertions in
`TestPreP36ConfigHashesAreReproducedExactly` and `TestAPreP36StoredDefinitionDecodesAndKeepsItsHash`
go red. The fence can fire.

🚫 **What must not happen to this decision.** The discontinuity between the two encodings looks like a
wart and the tempting cleanup is "just always use the new one, and re-key the pins". That cleanup is
the migration this finding says we do not need to do, and it costs one provider call per stored
inference to undo.

---

## D-36.1 — Q1: credentials are PER NODE

**Answer: per node.** `Node.CredentialRef`, alongside `Node.ModelRef`.

**Why.** A node binds a model and a model is served by exactly one vendor. A definition-level credential
would force every node onto one vendor — which is a routing decision made by a field that is not about
routing, and it removes the main reason to want a graph at all: a cheap model triaging for an expensive
one is usually two vendors.

**Why the precedent settles it rather than merely suggesting it.** `CriticModelRef` /
`CriticCredentialRef` already put a SECOND model and a SECOND credential on ONE definition, and the
comment beside them gives the reason: *"a second model is a SECOND COST and a SECOND CREDENTIAL … so the
spend meter can attribute them separately."* Per-node credentials are that same fact generalised. A
definition-level credential would be a NARROWING of what P30 already shipped.

**What it costs, stated rather than glossed.**
- The credential surface multiplies by the node count. Mitigated structurally, not by care: the
  reflective fence walks every exported type in the package, so `Node` is covered the same way
  `Definition` was, and task 3.3's drill adds a key-shaped field to `Node` and requires the fence to go
  red.
- `Readiness` must resolve EVERY node's credential, not the first one's. Done —
  `credentialRefsOf(Definition)` returns the distinct set including critic credentials, because a critic
  call is a real call against a real provider.
- `Publish` resolves every node's credential and every node's critic credential, and the refusal names
  the node.

**It does NOT complicate the ceiling.** See D-36.6: the ceiling is per assessment, so the number of
credentials has no bearing on it.

---

## D-36.2 — Q2: the producing node is OPERATOR-SIDE ONLY

**Answer: operator-side only.** A customer sees evidence; they do not see our topology.

**Why.** Two reasons, and the second is the load-bearing one.

The obvious one is that the platform's internal architecture is not part of what a customer bought, and
publishing it makes it a thing we cannot change without a change note.

The one that decides it: a node id is meaningless to a customer and *actionable-looking*. `heros_critic`
next to a finding invites "your critic is wrong", which is a conversation about our implementation
instead of about their code. The evidence — the call sites, the edges, the confidence — is what they can
act on, and it is complete without us.

**Where the line is drawn in code.** `ProvenancedEdge.ProducedByNode` is stored and is on the OPERATOR
read model. It is not on the customer console's projection, and `p36_customer_projection_test.go` asserts
the customer-facing shape carries no node attribution.

---

## D-36.3 — Q3: `placement` is NOT per node

**Answer: no. Placement stays per tenant.**

**Why.** `placement.go` says what the gate is: *"which host may run a tenant's inference, answered in ONE
function that both runners call and neither can skip … there is no path to a provider that does not pass
through it."*

A per-node placement makes that gate a per-node decision. The function survives, but the property does
not: today "may this run here" is answered once, before anything, for the whole assessment. Per node it
is answered N times, and a definition where node 3 is platform-placed and node 4 is customer-placed is
an assessment whose data crosses the boundary mid-run — which is not a placement any more, it is a
distributed execution with a security boundary inside it.

The task list names the hazard exactly: *"this would turn a gate both runners call and neither can skip
into a per-node decision."* That is the shape of a check acquiring a way around it.

**What is genuinely lost.** Cheap extraction customer-side and expensive analysis platform-side. That is
a real capability and it is deferred rather than refused — it needs a data-crossing story (what leaves
the customer's machine, under whose consent, recorded where), and inventing one as a side effect of a
topology change is how a boundary gets moved by accident.

**Consequence for the customer-side link.** `runlink.AgentDefinition` carries one prompt and one model.
A multi-node definition is REFUSED over it, naming the reason, rather than flattened to its first node —
flattening would run a configuration the `config_hash` does not name and submit the answer back under
that hash.

---

## D-36.4 — Q4: an in-flight assessment finishes under the definition it started with

**Answer: it finishes under the definition it started with, and the report records which.**

**Why.** The alternative produces a report with two configurations in it, and no honest way to label it.
Half its findings came from one agent and half from another, and `config_hash` — the field that exists
to say which agent produced a result — would be a lie whichever value it took.

**How it is made structural rather than intended.** The definition is RESOLVED ONCE at the start of an
assessment and travels with it as a value (`AssessmentBinding`), so there is no read of "the active
definition" mid-run that could return a different answer. A run cannot pick up a new definition because
it never asks again.

**What an operator sees.** Activation succeeds immediately — it is not blocked on in-flight work, which
would make the kill switch's neighbour a control that sometimes does not respond. The assessments
already running finish under the old definition and say so; the next one to start uses the new one.

---

## D-36.5 — Q5: the calibration set MUST grow, and the fence is that it cannot pass without it

**Answer: yes, it must grow, and the requirement is expressed as a refusal rather than as a fixture
count.**

**Why a count would not work.** "Add two fixtures" is satisfiable by two fixtures that exercise neither a
fan-in nor a conditional edge, and then rehearsal passes without testing the new capability — a fence
that cannot go red, which is exactly the risk the design already names.

**So the rule is:** rehearsing a definition that declares a fan-in requires at least one calibration
fixture whose expected graph CONTAINS a fan-in, and a definition declaring a conditional edge requires
one whose predicate is exercised in both directions. A rehearsal that cannot exercise the capability is
REFUSED, naming the capability and the missing fixture kind — it does not pass quietly.

**Why refusing beats passing-with-a-warning.** `RehearsalPassed` arms the activation gate. A warning
next to a `passed` verdict is a warning that arms the gate, and the gate is the only thing standing
between an unmeasured configuration and every tenant at once.

**Fixtures added.** `testdata/fixtures/py_fanout_no_merge` already exists for the fan-out shape;
`py_conditional_route` is added for the predicate. Both are exercised per node (D-36.7).

---

## D-36.6 — the spend ceiling is per ASSESSMENT (restated, because a graph is where it would move)

Design D6, unchanged, restated here because a multi-node definition is the first thing that makes a
per-node ceiling look reasonable.

`CapChecker`'s ceiling is scoped to the assessment. **Adding a node does not raise the budget.** A
per-node ceiling means a topology change silently changes a budget — the least visible way for a system
to start spending more, because nobody edited a budget and the number that moved is one nobody was
watching.

**Consequence, accepted:** a definition with many nodes may exhaust its ceiling mid-assessment. That is
correct. It degrades to `not_measured` with `budget exhausted`, the state P33 already defines, and the
report names the node it stopped at.

---

## D-36.7 — evaluation is PER NODE, not per agent

Task 7.1, recorded as a decision because the aggregate is the natural thing to build.

A definition whose critic never disagrees scores well as a whole and is broken in the half that matters:
the analyst carries the score, the critic contributes nothing, and one number cannot tell you. So
rehearsal reports per node and per axis, and the gate reads the minimum across BOTH — the same argument
D7 makes about per-fixture minima, applied one dimension further in.

---

## D-36.8 — 🚫 D5: HEROS DOES NOT PROPOSE CHANGES TO ITSELF

**Recorded here, in the decisions file, because this is where whoever proposes it will look.**

The agent never targets its own definition with a proposal. An operator authors it.

**Why no gate fixes this.** *Diagnosis proposes, verification decides* — and verification is performed by
measurements the agent produces. An agent proposing a change to itself is an evaluator grading its own
configuration. Adding a gate does not help: whatever gates it is running on the configuration being
judged.

**Why it is stated now.** It is the obvious next step, it sounds like the natural culmination of the
program, and it will be proposed. Stating the circularity here makes that a decision to OVERTURN rather
than a gap to fill.

**Enforced, not merely written.** `proposalgen` refuses a proposal whose subject is the platform's own
workflow id, and `TestNoProposalTargetsTheAgentsOwnDefinition` asserts it — including the case where the
subject arrives through a tenant-scoped path with `workflow_id: "heros"`.

**What is sayable instead** (task 10.2): *the platform's own agent is configured through the same nine
axes we expose to you, including its topology, and it is rehearsed and version-pinned before
activation.* Not: that it optimizes itself. Naming the circularity out loud is more credible than
marketing past it.
