# Design — P10: Prompt & Model Studio

Product rationale:
[`../../../docs/prd/P10-prompt-model-studio.md`](../../../docs/prd/P10-prompt-model-studio.md).
Architecture decision: [`../../../docs/adr/ADR-004-runtime-config-binding.md`](../../../docs/adr/ADR-004-runtime-config-binding.md)
(amends [ADR-001](../../../docs/adr/ADR-001-source-transformation-apply-model.md); ADR-002 untouched).

## Context

The primitives are already right, and that is the constraint this design works under rather than
around. `internal/registry` gives content-addressed, structurally-immutable prompt versions — the
package offers **no** mutation API, and `db/migrations/postgres/0002_p2_registries.up.sql` adds a
`BEFORE UPDATE OR DELETE` trigger plus a `CHECK` that `version_id` equals the hash of the envelope,
with `verifyEnvelope` re-hashing on every read. `ParseTemplate`/`Render` give strict `{{name}}`
templating that fails on missing **and** unknown bindings before emitting output. `variantspec` gives
a per-node override map keyed to immutable registry ids with a typed `SpecError{NodeID, Dim, Ref}`
error channel. None of that is redesigned here; all of it is extended.

Three facts about the existing codemod shape the whole design, and each is a deliberate refusal worth
preserving:

```go
// internal/transform/rewrite.go — promptExprFor
"prompt %q's slot {{%s}} matches %d of this call site's runtime value(s) %s; a slot
 binds to the call-site expression spelled exactly like it, and this engine will not
 guess which value belongs in a slot"
```

1. A slot binds to a call-site expression **spelled identically** — never positionally, never inferred.
2. An **unclaimed operand** is a refusal, because rewriting past it silently drops a runtime value.
3. A slotless template over a call site with runtime operands is a refusal, for the same reason.

These are correct. The problem is not that they refuse; it is that **explicit binding is not
expressible**, so the only way to satisfy a slot is for the call site to already spell it. That is what
`variable-bindings` adds — a way to say what the engine refuses to guess.

## Decision 1 — Two-layer apply: `bound` is opt-in per node, `inline` stays the default

Per ADR-004, apply mode is per node. `inline` writes the value at the call site exactly as today.
`bound` writes an indirection plus a generated artifact plus the **resolved binding document**, all in
one diff, after which the model, its params, the prompt version, and `literal`/`env` bindings are data.

**Alternative rejected — inline-only (status quo).** Cheapest; preserves every ADR-001 property with
no new hazard surface. Rejected because it makes P10's central loop a build-and-review cycle per
attempt — an **L3 UX** cost paid to avoid a hazard set that is containable by ordinary engineering
(Decisions 4–6). Note it remains available *per node*, which is what makes the rejection cheap.

**Alternative rejected — make `bound` the default.** Simpler to reason about (one mode), and it is the
mode the new feature is built for. Rejected on **L2 稳定**: it would move every existing user onto a
new indirection and a new failure mode (degraded resolution) they did not ask for. Under the priority
ordering, a stability change cannot be bought with L3/L8 convenience. Opt-in pays neither price.

**Alternative rejected — true runtime resolution (the program fetches config from the platform).**
Rejected on ADR-001's original grounds, which have not expired: no interception seam in compiled Go
without generated code anyway, so the "no source change" benefit is illusory; and a run that resolves
from a live service is not the artifact that ships, which loses the measurement fidelity that caused
the shim to be rejected in the first place. It also maximizes the blast-radius hazard instead of
containing it.

## Decision 2 — The data/structure line is drawn on lexical scope, not on convenience

| Fact | `bound` location | Runtime-changeable |
|---|---|---|
| Model id + inference params | binding document | **yes** |
| Prompt template body / version | binding document | **yes** |
| `literal` binding | binding document | **yes** |
| `env` binding (the variable *name*) | binding document | **yes** |
| `expr` binding — a call-site expression | rewritten call site | **no** |
| `input` binding — a typed graph input | rewritten call site | **no** |
| Wiring, skills, context policy | the source | **no** |

The line is principled rather than pragmatic: an `expr` names a variable in the **user's lexical
scope**, and moving it into a data file requires either reflection Go does not offer or a string the
program evaluates. So it is structure and stays structure.

This matters beyond correctness. Because there is a principled place where `bound` mode stops, it
cannot gradually become a general runtime-reconfiguration surface under feature pressure — the next
request ("let me change the wiring at runtime too") has a defensible answer rather than a reluctant
one. It is also what lets the product make a **precise** promise: *model and prompt become data; the
wiring between the prompt's holes and the program's values stays code.* The console states this per
node rather than leaving users to infer it (PRD FR33).

## Decision 3 — All binding validation happens at spec-resolve, none at codemod time

Every binding failure class — unsatisfied slot, `expr` not in scope, undeclared `env`, `input`
violating the typed contract, unclaimed operand — is decided when the Variant Spec is resolved, before
any AST work begins, and reported through the existing `variantspec.SpecError{NodeID, Dim, Ref}`.

**Alternative rejected — validate during transformation.** The AST is right there, and in-scope
symbols could be computed exactly rather than read from the IR. Rejected on **L3 UX**: a failure at
codemod time arrives after the user has published a prompt version, built a spec, and submitted it —
the discovery is late and the work is already done. Resolve-time failure names node, dimension and slot
while the user is still in the editor. It is also rejected on **L7 维护**: a second error channel
alongside `SpecError` would mean two shapes reaching the console for the same class of user mistake.

The cost is real and accepted: the IR must record in-scope symbols per call site (an **additive**
extension to an `x-frozen: additive-only` object), and that record can be more conservative than a
full scope analysis. A conservative record produces false rejections, never false acceptances — the
safe direction, and the transform's own checks remain as the backstop.

## Decision 4 — An indirection without its resolved values is rejected, not merely discouraged

A `bound` transformation must emit the resolved values in the same change. One that does not is
**rejected before it is proposed or run**, on the same footing as a transformation that fails to build.

**Alternative rejected — let the indirection ship and keep values platform-side, referenced by id.**
Smaller diffs, and it centralizes configuration. Rejected on **L1/L5**: review is ADR-001's central
property, and a diff showing `agentcfg.Node("n").Model()` with the value elsewhere makes review
theatre — the reviewer approves a pointer. It also breaks `git revert` as rollback and git as the audit
trail, because a configuration outside the repository cannot be reverted by the change that introduced
it. Making this a **hard gate** rather than a lint follows the codebase's own lesson that a rule which
only turns a light red is the kind that holds.

## Decision 5 — Reproducibility is reconciled per invocation, not assumed

The resolver emits the `config_hash` of the document it **actually resolved** on every invocation into
the P2.5 tag set. The eval harness asserts observed equals requested and **fails the run** on mismatch.
During eval and verification the resolver is **pinned**: override sources are disabled in the sandbox.

**Alternative rejected — trust that the resolver read what it was told to.** Rejected under
`event-write-reconcile-read`: an invariant that depends on ordering rather than on an **idempotent
reconcile point on the data's necessary path** is not enforced, it is hoped for. Telemetry is a path
every invocation already travels, so the check cannot be skipped by forgetting to call it.

Worth stating plainly: this **adds** a guarantee that inline mode never had. Today nothing verifies
that a run recorded under a `config_hash` executed that configuration — it is inferred from the build.
The hazard `bound` mode introduces is what forced the check, and the check is broader than the hazard.

## Decision 6 — Fail-static resolution; remote opt-in; never a startup dependency

Resolution order is embedded → local override → remote-if-enabled. On an unreachable, unparseable or
invalid override source the resolver keeps the **last known-good** document and reports **degraded**.

**Alternative rejected — fetch at startup, fail if unavailable.** Simple, always fresh, and the
failure is loud. Rejected on **L2 稳定 over L3/L4**: it makes a platform outage a customer outage and
puts us on their critical boot path. **Fail-open to an empty or default configuration is rejected
outright** — silently changing which model a production node uses is worse than any outage.

**Degraded is a reported state, not a silent fallback.** A resolver quietly serving stale
configuration would be the worst of the three outcomes, because the operator would have no signal at
all. This follows the health-signal rule: the condition is exposed on a readable endpoint, not left to
be inferred.

## Decision 7 — Unverified resolution is visible and refusable, not forbidden

The binding document records the `config_hash` that carried a verified delta. Resolving to a
configuration without one is **permitted**, **marked unverified at every invocation** and in the
console, and **refusable by automation level** (under Autonomous it does not run).

**Alternative rejected — hard-forbid resolving to an unverified configuration.** It sounds stricter
and protects P5.5's guarantee absolutely. Rejected because an operator sometimes must — an incident, a
provider outage, a rollback to a known-good configuration that predates the verified-delta ledger —
and forbidding it pushes people to route **around** the mechanism, which loses exactly the telemetry
that made the risk visible. A state that is visible everywhere it matters is stronger than a
prohibition people evade.

This is also why "someone selected this" and "this was proven better" must render differently: two
states with different meanings collapsing into one rendering would destroy the distinction P5.5 exists
to create.

## Decision 8 — The studio is deliberately not an evaluator

A studio test or side-by-side shows the output, cost, latency and tokens. It shows **no score, rank,
winner or confidence interval**, and no configuration can be promoted from its result.

**Alternative rejected — let a side-by-side declare a winner.** It is the obvious feature, users will
ask for it, and it demos beautifully. Rejected because a two-sample comparison presented as a finding
**is** the amateur loop (*change → eyeball → ship*) that P4 exists to replace — and a UI that implies
it manufactures false confidence at the exact moment a user is deciding. The studio's real value is
discarding the obviously-bad cheaply, which does not require ranking; the moment it ranks, it competes
with the instrument that is actually honest, and it wins on convenience.

The separate spend kind (reusing P4's existing `SpendReport.by_kind`) follows the same reasoning:
exploratory traffic folded into eval cost would corrupt the numbers optimization decisions are made on.

## Data model sketch

```
NodeOverride           += Bindings  map[string]BindingSource   // FR7; part of config_hash (FR13)
                       += ApplyMode "inline" | "bound"         // FR15; default "inline"

BindingSource          =  { Kind: "literal"|"expr"|"env"|"input", Value string }
                          // literal, env  -> binding document (runtime-changeable)
                          // expr, input   -> rewritten call site (needs a new diff)

IR node.call_site      += in_scope []string    // ADDITIVE to an x-frozen: additive-only object

BindingDocument        =  { config_hash, verified_config_hash?,
                            nodes: { node_id -> { model_id, params, prompt_template,
                                                  literal_bindings, env_bindings } } }

PromptTimeline         =  read model over existing registry rows — no new table
PromptVersionDiff      =  { body_diff, slots_added[], slots_removed[] }   // slot change reported separately
PromptImpactAnalysis   =  { blocked: [{node_id, reason}], unanalyzed: [{node_id, why}] }
```

## Key interfaces

```
PublishPrompt(name, body)            -> version_id           // idempotent on identical content
PromptTimeline(name)                 -> []{version_id, slots, created}
DiffPromptVersions(a, b)             -> PromptVersionDiff
AnalyzeImpact(name, proposed_body)   -> PromptImpactAnalysis // BEFORE publish; names what it could not analyze
ResolveSpec(spec)                    -> ResolvedConfig | SpecError{NodeID, Dim, Ref}  // all binding validation here
Resolve(pinned bool)                 -> (BindingDocument, resolved_config_hash, degraded?)
StudioRender(version_id, bindings)   -> string               // byte-identical to what a run sends
StudioRun(prompt_version, model_version, bindings) -> {output, cost_usd, latency_ms, tokens}  // spend kind: studio
```

## Risks

| Risk | Mitigation |
|---|---|
| The indirection hollows out review | Decision 4 — a hard rejection, plus the PR rendering effective values. |
| A run is scored against a configuration it did not execute | Decision 5 — per-invocation reconciliation, run failed on mismatch, resolver pinned during measurement. QA asserts the check can go red. |
| A platform outage changes or halts customer production | Decision 6 — embedded-first, remote opt-in, fail-static, never fail-open, never a startup dependency. |
| Production drifts onto an unverified configuration | Decision 7 — marked at every invocation, surfaced in the console, refusable by automation level. |
| Binding inference creeps back under delivery pressure | Decision 3 — bindings are explicit and validated; `promptExprFor`'s existing refusal text is the precedent. |
| A prompt edit un-applies nodes and the user finds out late | Impact analysis **before publish**, and resolve-time rejection naming node/dimension/slot. |
| A studio comparison is read as a result | Decision 8 — enforced as a failing test, not a review note. |
| `bound` becomes the default by drift | `inline` fixed as the default; mode visible in the diff and the console. |
| Conservative `in_scope` produces false rejections | Accepted direction: false rejections, never false acceptances. The transform's own checks remain the backstop. |
| The generated artifact rots in customer repositories | Dependency-free, deterministic, byte-identically regenerable, small enough to review, revertible with the change that introduced it. |
