# Design — P36: The Agent Is a Graph

## Context

P30 left the platform's own agent as one node and said why: *"there is no second node to order it
against."* That was accurate and it was also the shape choosing for everyone — the critic became a loop
strategy, the nine-axis assessment became nine sequential inferences, and the wiring axis became a
read-only box with an explanation.

P36 removes the constraint. The whole difficulty is that the agent's *shape* is hashed, and the hash keys
every pinned result the platform has ever produced.

## D1 — One validator, shared with customers

**Decision.** The agent's topology is validated by the same typed-contract path a customer's Variant Spec
uses.

**Why.** A parallel validator for our own configuration is where a rule gets quietly weaker — nobody
intends it, and it happens because the internal path has different pressures (an operator is trusted, the
surface is smaller, the deadline is closer). The failure mode is that a customer discovers the platform
does not hold itself to a rule it enforces on them.

There is a second, more practical reason: HEROS is the only agentic workflow the platform can change and
measure freely. If it goes through the customer path, then every fan-in, conditional edge and merge in the
graph axis is exercised by our own agent **before** a customer's repository reaches it.

## D2 — `NodeID` becomes data; single node stays the default

**Decision.** Node identity is data. A definition may declare one node — and that remains what an operator
gets without asking for more.

**Why not make multi-node mandatory.** An operator who wants what they have today should not have to author
a graph to keep it. More importantly, the single-node case is what must stay hash-identical (D4), and it
cannot be if it stops being expressible.

**Scope of the change.** The constant is a shape assumption threaded through `definition.go`,
`axiseditor.go`, `inferencestore.go`, `placement.go` and the package's fences. Those move together or the
package is inconsistent in a way the type system will not catch — in particular the **reflective
credential fence**, which asserts no field can hold a key: a fence that enumerates the old shape passes
*vacuously* on the new one, and a vacuous pass looks exactly like a real one.

## D3 — The wiring refusal narrows; it is not deleted

**Decision.** `ErrWiringOverride` becomes conditional. A single-node definition still refuses an ordering.

**Why.** The refusal's stated reason was *"there is no second node to order it against."* For a single-node
definition that is still true. Deleting the rule because a new case appeared would discard a correct check
for the old case — and the old case is still the default (D2), so it would be discarded for the majority of
definitions.

This is the small version of the discipline ADR-014 applies at scale: when a rule's premise stops holding
in *some* cases, narrow the rule to the cases where it still holds. Do not remove it.

## D4 — 🔴 A configuration change is a pinning event, exactly like a source change

**Decision.** A single-node definition with no loop ref and no graph declaration hashes byte-identically to
its pre-P36 form. A definition change never silently re-runs pinned inferences; re-inference is explicit
and shown as a diff. A pin whose shape is no longer authorable renders **stale with its producing
configuration named**.

**Why this is the phase's hardest requirement.** Every pinned inference is keyed by `(source_revision,
agent config_hash)`. Change the definition's shape and every hash moves; every pin is orphaned; every
assessment re-runs at provider cost the next time somebody asks — and until they ask, the console shows
results computed by a configuration that no longer exists, with nothing on screen saying so.

None of that fails a test. It shows up weeks later as a bill and as a support question.

**Why P30's rule extends here without modification.** P30 wrote the pinning rule about *source* revisions:
*"a graph that changes under a customer between two page loads is worse than no graph."* The argument
never depended on which input moved. A configuration change moves the result the same way, so it gets the
same treatment.

## D5 — HEROS does not propose changes to itself

**Decision.** The agent never targets its own definition with a proposal. The operator authors it.

**Why.** *Diagnosis proposes, verification decides* — and verification is performed by measurements the
agent produces. An agent proposing a change to itself is an evaluator grading its own configuration, and
the circularity is not fixed by adding a gate: whatever gates it is running on the configuration being
judged.

**Why state it now.** It is the obvious next step, it sounds like the natural culmination of the program,
and it will be proposed. Stating the circularity here makes that a decision to overturn rather than a gap
to fill.

## D6 — The spend ceiling is per assessment, not per node

**Decision.** `CapChecker`'s ceiling is scoped to the assessment. Adding a node does not raise it.

**Why.** A per-node ceiling means a topology change silently changes a budget. That is the least visible
way for a system to start spending more money — nobody edited a budget, and the number that moved is one
nobody was looking at.

**Consequence.** A definition with many nodes may exhaust its ceiling mid-assessment. That is correct, and
it degrades to `not_measured` with `budget exhausted` — the state [P33](../../../docs/prd/P33-surface-assessment.md)
already defines.

## Data-model sketch

```go
type Definition struct {
    Nodes []Node   `json:"nodes"`             // was: implicit, one, named by a package constant
    Order []string `json:"order,omitempty"`   // meaningful only when len(Nodes) > 1  (D3)
    Edges []Edge   `json:"edges,omitempty"`
    Graph []Group  `json:"graph_groups,omitempty"`  // concurrency + merge (P34)
}

type Node struct {
    NodeID        string   `json:"node_id"`
    PromptRef     string   `json:"prompt_ref"`
    ModelRef      string   `json:"model_ref"`
    CredentialRef string   `json:"credential_ref"`   // a provider NAME, never a value
    SkillRefs     []string `json:"skill_refs,omitempty"`
    ToolNames     []string `json:"tool_names,omitempty"`
    ContextRef    string   `json:"context_ref"`
    MemoryRef     string   `json:"memory_ref,omitempty"`
    HarnessRef    string   `json:"harness_ref"`      // envelope only, after P34
    LoopRef       string   `json:"loop_ref,omitempty"`  // NEW
}
```

The hash-compatibility requirement (D4) constrains the serialisation, not just the fields: a definition
with exactly one node, no `Order`, no `Edges`, no `Graph` and no `LoopRef` must marshal to the bytes the
pre-P36 shape produced. Whether that is achievable with a nested `Nodes` array or needs a compatibility
encoding is the first thing to establish, before anything else in this phase is built — it is the
difference between an additive change and a migration of every pin.

## Risks this design accepts

- **D4 is a silent failure if wrong.** No test goes red; a bill goes up. Which is why the fence for it is
  task 1.1 of the phase and not a QA item at the end.
- **The reflective credential fence can pass vacuously** on the new shape. Adding a key-shaped field to the
  new struct must make it fail, and that must be asserted, not assumed.
- **Non-determinism from internal concurrency** is intermittent by nature. The fence must run the same
  pinned inference repeatedly, not once.
- **Blast radius is every tenant at once** — this is the platform's own agent, not a per-tenant
  configuration. Rehearsal, staged rollout and the kill switch already exist in `internal/herosagent`;
  none of them is new work, and all of them are now load-bearing for a larger surface.
- **A calibration set sized for one node may not exercise a fan-in at all**, in which case rehearsal passes
  without testing the new capability — a fence that cannot go red. PRD §14 Q5.
