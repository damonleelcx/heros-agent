# P16 — Recorded decisions (System Designer, §1)

Two contracts that must be fixed **before any rewriter or admissibility-gate code ships**, because each
is a one-way door: a `config_hash`-participating field and the shape of the dimension enum are things
future readers depend on and the platform can never quietly retract. The rewriter design itself is in
[`design.md`](design.md); this file records the two irreversible shape decisions.

---

## D-1 — Context stays **one axis**; no `DimRetrieval`, no new `Dimension`

**Problem.** P16 makes context applicable and adds retrieval tuning (top-k, chunk size, rerank,
embedding). Retrieval could be modeled as its own dimension — a `DimRetrieval` alongside `DimContext`
— which reads cleanly ("retrieval is its own thing"). But the `Dimension` enum is closed and tiny by
design ([`internal/variantspec/spec.go:42-47`](../../../internal/variantspec/spec.go)), and adding a
member is a one-way door: every `Dimensions()` caller, every `resolveNode` block, every rewriter
dispatch, and every `config_hash` reader would thereafter assume it exists.

**Decision.** **No new `Dimension`.** Retrieval tuning is the existing `rag-retrieval` context policy
([`internal/registry/context_policies.go:231`](../../../internal/registry/context_policies.go)) with
params; it lives under `DimContext`. The enum stays `{DimModel, DimPrompt, DimSkills, DimContext}`.

**Why appropriate.** Retrieval already *is* a context policy in the registry — the platform models it
that way today. A second dimension would split one axis's identity across two enum members, double the
`config_hash` surface for a node that does both context assembly and retrieval, and force a second
`resolveNode` block and a second rewriter for behavior the `rag-retrieval` policy already expresses.
The `Policy` interface + `ParamsSchema` were built precisely so a new context strategy is a policy, not
a dimension.

**Alternatives + decision point.** `DimRetrieval` as a first-class axis. Rejected on **L5 不可演进**:
opening the closed enum for behavior an existing dimension already carries is an evolvability cost with
no behavioral gain — the enum is closed on purpose, and this is exactly the pressure it is closed
against. Retrieval as a policy is the narrower, reversible choice.

**Effect.** Retrieval tuning ships with zero change to the dimension enum, the resolve dispatch, or the
hash algorithm; it is new `OpRAGTune` proposal breadth and one rewriter, both additive.

---

## D-2 — Drop tolerance is an **additive, omit-when-absent** per-node attribute

**Problem.** The drop-tolerance admissibility gate needs a per-node number: the drop ratio a node's job
can tolerate. That number has to be readable at proposal admissibility and it has to be part of the
configuration's identity when set (two variants that differ only in tolerance are different
configurations). But `ResolvedNode` feeds `config_hash`, and P0/P10 froze golden hash vectors —
[`internal/variantspec/resolved.go:46`](../../../internal/variantspec/resolved.go) — so any new field is
a one-way door on the hash: get its emptiness encoding wrong and every pre-P16 spec's hash moves.

**Decision.** Add `context_drop_tolerance` as an **additive, omit-when-absent** attribute on
`NodeOverride` and `ResolvedNode` — a pointer/optional that emits **no key** when unset, exactly the
encoding P10's `Bindings` field used ([`resolved.go:62-69`](../../../internal/variantspec/resolved.go)).
A node that declares no tolerance serialises byte-identically to a pre-P16 node, so its `config_hash` is
unchanged. When set, it participates in the hash additively (JCS includes the present key). Tolerance is
modeled as a **per-node admissibility input**, not a policy fact.

**Why appropriate.** Whether a given drop is acceptable is a property of the node's *job* — a Retrieval
node tolerates augmentation a Summarization node does not — so tolerance belongs on the node, not on the
policy (a policy is shared across nodes with different tolerances). Omit-when-absent is the only encoding
that keeps the frozen golden vectors reproducing: the field's **absence** is what must stay
byte-compatible, and a pointer field with no key when nil achieves that, whereas an always-present
`0.0` default would both change the bytes and mean "tolerate no drop", the wrong default.

**Alternatives + decision point.** (A) An always-present `context_drop_tolerance` with a `0.0` default —
rejected on **L2 稳定**: it changes the frozen `config_hash` bytes of every existing node and encodes a
hostile default (zero tolerance rejects every lossy policy). (B) Tolerance as a policy param on the
context entry — rejected on **L6 不可扩展 + L7 维护**: it duplicates the number across every node using
the policy and makes "this node tolerates more drop" inexpressible without a bespoke per-node policy
entry. The additive per-node attribute is the narrow, reversible choice.

**Effect.** The gate reads `node.context_drop_tolerance` (or a pattern-derived default when absent, PRD
§14 Q1) at admissibility; a node with no tolerance hashes byte-identically to pre-P16; removing the
field later is a clean revert because nothing that omitted it depended on its presence.
