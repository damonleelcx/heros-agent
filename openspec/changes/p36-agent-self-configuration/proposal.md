## Why

P30 built HEROS and configured it through the same vocabulary the product sells — *"The platform optimizes
agentic workflows for a living; its own agent is one of them."* It is one of them with one exception, and
the exception is a constant:

```go
const NodeID = "heros_analyst"
```

with the comment: *"One node, and that is what makes the wiring axis vacuous rather than merely unused:
there is no second node to order it against."*

So the platform's own agent is a single call site. It cannot fan out, cannot route conditionally, cannot
run a critic beside an analyst, and cannot be re-ordered — not because those were refused but because there
is nothing there to arrange. **An agent that cannot be a graph cannot do graph engineering**, which is what
this program is named after.

The cost is not only rhetorical. P30 exists because seven of eight pattern detectors are topology
predicates that cannot fire on a graph with no edges — and HEROS *is* a graph with no edges. Its critic is
a loop strategy standing in for a topology it cannot express. Its nine-axis assessment
([P33](../../../docs/prd/P33-surface-assessment.md)) is nine sequential inferences or one very large prompt,
because the shape leaves no third option. And HEROS is the only agentic workflow the platform can change
and measure freely, so a multi-node HEROS is the first real fan-out, conditional edge and merge that
[P34](../../../docs/prd/P34-harness-loop-graph-split.md)'s graph axis will ever be exercised against — before
a customer's repository is.

## What Changes

- **MODIFIED** `AuthorableAxes()` from seven to **nine**: `loop` and `graph` join prompt, model, skills,
  tools, context, memory and harness. Both are registry references like every other axis.
- **ADDED** multi-node definitions: `NodeID` stops being a package constant and becomes data. A definition
  declares nodes, per-node axis bindings, an ordering, edges, and (per P34) concurrency and merge.
- **MODIFIED** the wiring refusal: `ErrWiringOverride` narrows from unconditional to conditional. A
  single-node definition **still** refuses an ordering, because the reason it always gave — *"there is no
  second node to order it against"* — is still true in that case. The blanket rule is retired because the
  axis became real, not because the rule was relaxed.
- **ADDED** the requirement that the agent's topology is validated by the **same** typed-contract path a
  customer's Variant Spec uses. One validator, not a lookalike.
- **ADDED** hash compatibility: a single-node definition with no loop ref and no graph declaration
  serialises **byte-identically** to its pre-P36 form. Every pinned inference is keyed by
  `(source_revision, agent config_hash)`, so a shape change that moved the hash would orphan every pin —
  silently re-running every assessment at provider cost, weeks later.
- **ADDED** per-node attribution, per-node observability, and rollback as a single act (activating a prior
  version, never re-authoring the older shape).
- **PRESERVED and restated as requirements**, because a configuration surface that grows is where a
  property gets left behind: no field may hold a credential value (with the reflective fence extended to
  the new fields); rehearsal before activation, including multi-node; the spend ceiling enforced before
  every provider call and scoped **per assessment, not per node**; and `ErrNoChange` still refusing to mint
  a duplicate version.
- **REFUSED, deliberately:** the agent proposing changes to its own definition. An evaluator that grades
  its own configuration is not an evaluator, and no gating fixes the circularity. It is stated as a
  decision now because it is the obvious next request.

## Impact

- **Affected capabilities:** `heros-agent-definition` (modified — the node list, the nine axes, the
  narrowed wiring refusal, hash compatibility), `operator-agent-authoring` (modified — nine axes edited
  per node, each still bound to its vocabulary), `agent-graph-composition` (new), and by reference
  `operator-agent-control`, `operator-axis-oversight`, `inference-provenance`
- **Affected code/systems:** `internal/herosagent` (`definition.go`, `axiseditor.go`, `inferencestore.go`,
  `placement.go`, `caps.go`, `rehearsal.go`, `rollout.go` and the fences), `internal/typedcontract`
  (shared validator), `web/admin-console` (`/agent`, `/axes`), P26's operator build fence
- **Dependencies:** upstream — [P34](../../../docs/prd/P34-harness-loop-graph-split.md) is a **hard** blocker;
  P36 cannot start before the axes it configures exist. Also P30 (the agent, its pinning, rehearsal,
  rollout and caps), P2 (registries), P8/P26 (operator console and its build fence).
- **Unblocks:** nothing. This is the program's terminus.
- **Documents only in this program.** Every task is unchecked.
