# P16 — Recorded decisions (System Designer, §1)

Contracts that must be fixed **before any rewriter or admissibility-gate code ships, or any further
language's list splitter lands**, because each is a one-way door: a `config_hash`-participating field and the shape of the dimension enum are things
future readers depend on and the platform can never quietly retract. The rewriter design itself is in
[`design.md`](design.md); this file records the irreversible shape decisions, plus D-3 — the one open
question (PRD §14 Q4) whose answer decides whether the D-2 gate can ever see the policy it was built for.

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

---

## D-3 — `structured-extraction` is **lossy**, and its drop is **measured** (PRD §14 Q4)

**Problem.** PRD §14 Q4 left one flag undecided: is extracting a structured summary from a conversation
a *lossy* assembly, or a *representation change* like `full-history`'s identity? The flag is small and
the consequence is not — `AssembledContext.Lossy` is what decides whether a drop-ratio event is emitted
at all ([`internal/telemetry/context_assembly.go:78`](../../../internal/telemetry/context_assembly.go)),
and therefore whether the drop-tolerance gate (D-2) can ever see this policy.

**Decision.** **Lossy.** `StructuredExtractionPolicy` sets `Lossy: true`, and its `DropRatio` is
**measured** from the assembled-vs-source token counts on every run rather than asserted as a constant.

**Why appropriate.** Extraction is a **projection**, not a re-encoding. `full-history` is lossless
because every message survives — the information is all still there, differently named. Extraction
discards everything the declared field set does not name, and nothing downstream can recover it: a node
that needed a detail outside the schema has lost it. The two cases look alike ("the same conversation,
tidier") and behave in opposite ways, which is exactly why the flag had to be decided rather than
defaulted.

**Alternatives + decision point.** (A) Lossless, on the grounds that the extraction is "the same
information in a schema" — rejected on **L1 安全 + L2 稳定**: a lossless flag suppresses the
`context_drop_ratio` event, so the drop-tolerance gate would never fire on the one policy whose entire
mechanism is discarding what the schema did not name. The gate would be decoration on precisely the case
it was built for, and a variant that dropped the answer would reach eval spend to discover it.
(B) Lossy with a *fixed* drop constant — rejected on **L1 honesty**: two extractions of the same schema
over a terse and a verbose conversation drop wildly different amounts, and a constant would report a
number nobody measured. The gate reads the measurement.

**Effect.** `structured-extraction` emits `context_drop_ratio` per node per run like any other lossy
policy, participates in the drop-tolerance gate, and needs no new metric family. Its call-site behavior
follows from the same fact: it rewrites message *content*, so the Go materializer refuses it by name
rather than constructing message values the author never wrote
([`internal/transform/contextmaterialize.go`](../../../internal/transform/contextmaterialize.go)).


---

## D-4 — A list splitter is the **only** per-language part of a selection; retention and the drop record stay shared

**Problem.** `spanContextMaterializers` carries Python and wave 16d adds the remaining five languages.
That looks like ordinary per-language work and contains a one-way door: whichever shape the *second*
syntactic language arrives in becomes the template for every language afterwards. The tempting shape —
have each language's rewriter implement the policy directly, since it already has the list in front of it
— is faster per language and permanently wrong, and it is wrong in a way that produces no error anywhere.

**Decision.** Two parts, both pre-code.

1. **Retention is decided by the shared selection code in every language.** A splitter answers exactly one
   question: what are the written elements of this list, as spans. It does not decide which elements are
   retained, does not interpret the policy, and does not read policy parameters. `SelectionPolicy.Retain`
   is the single implementation, so the codemod and the runtime cannot disagree and neither can two
   languages.
2. **The drop record is produced by the shared path, and is language-free.** Materializing a selection
   records which turns were not retained through the same code in every language, and the record carries
   no language field — so a record produced in a newly covered language is **byte-comparable** with one
   produced in an existing one, and that comparison is the test. No code path emits the deletion without
   producing the record.

**Why this is the appropriate design.** Part 1 is **L1**, and it is a measurement argument rather than a
correctness-of-code argument: retention *is* the policy, so a per-language retention decision means one
`config_hash` describes two different configurations. The harness reads only the hash and the trace, so it
would compare them as one and every number downstream would be quietly wrong — the same class of failure
as scoring a variant against unchanged source, which is what Decision 1's refusal exists to prevent. Part 2
is **L1 honesty**: `context_drop_ratio` is the number this axis exists to keep honest, and a language that
could delete turns without recording them would not fail anything — it would simply make the ratio
incomplete for some workflows and not others, which is undetectable from the outside. Making the record a
property of the shared path rather than a responsibility of each rewriter is what makes "unskippable"
structural instead of remembered.

**Alternatives.** (a) **Let each language's rewriter implement the policy** — rejected under part 1.
(b) **Let a splitter return elements plus a hint about which are retainable** — rejected as part 1 with an
extra step: a hint that influences retention *is* a retention decision, and it would be argued about per
language. (c) **Add a language field to the drop record** — rejected under part 2: it makes the
cross-language comparison non-trivial precisely where the comparison is the assertion; the language belongs
on the transform record instead.

**Effect.** Each remaining language is one splitter plus one coverage row. What does **not** change: a
policy whose content is produced at run time refuses identically in every language, before and after any
splitter lands; a call site that wrote no message list refuses for its own shape; and the drop-tolerance
gate keeps running first, and keeps returning `not-yet-measurable` rather than refusing on ignorance.
