# Design — P18: The Memory Runtime and the Call-Site Rewriter

Product rationale: [`../../../docs/prd/P18-memory-runtime.md`](../../../docs/prd/P18-memory-runtime.md).
Pre-code one-way-door contracts: [`decisions.md`](./decisions.md) (D1–D7).
The phase this completes: [`../p17-memory-strategy-optimization/`](../p17-memory-strategy-optimization/).
The precedent it follows: [`../p16-context-strategy-optimization/`](../p16-context-strategy-optimization/).

## Context

P17 left a refusal that named its own remedy — *a memory runtime (a store, a lifetime, and a key scheme)
plus the call-site rewriter that reads and writes it*. That sentence is the specification for this phase,
and it is worth noticing what it does **not** say: it does not say "a Python rewriter". The gap was never
per-language, which is why P17's coverage was uniform and why its console copy said the limit was not
about your language.

That uniformity is what ends here, and it ends **per cell**, not per axis.

Two pieces of the problem are already solved elsewhere in the repository and are reused rather than
reinvented:

- **The rewrite shape.** [P16](../p16-context-strategy-optimization/) faced a dimension that is not an
  argument in any language and found the honest boundary: materialize what is a *transformation of an
  expression the author already wrote*; refuse what would be a *construction*. Memory's **recall** is
  exactly that — the message list goes in, a message list comes out.
- **The delivery.** P10's bound mode already emits a generated module plus a data document **in the same
  patch** as a call-site edit, so one revert restores everything. A memory artifact is another such
  artifact.

What is genuinely new is the **record** half, and it is new in a way that matters: recording a turn is not
an expression replacement, it is a *statement that must run after the call*. That is a second edit class,
with its own admission rule, and it is the reason coverage is narrower than the recall half alone would
suggest.

## Decision 1 — Both halves or refuse

A cell materializes **only** when it can emit recall **and** record. A call site that admits one and not
the other is refused whole, naming which half is missing.

This is the decision every other one bends around, and it is the one most exposed to shipping pressure,
because "we can at least do the read" is always available and always sounds like progress. It is not
progress. Half a memory is not a weaker memory — it is a **different strategy**:

| Emitted | What the node does | What the hash claims |
|---|---|---|
| recall only | recalls from a store nothing ever fills — **behaves as `none`** | `summary-buffer` |
| record only | fills a store nothing ever reads — **behaves as `none`**, and grows unboundedly | `summary-buffer` |
| both | the strategy the hash names | the same |

**Alternative rejected — emit the half that works, warn about the other.** Rejected on **L1**: a warning
is not a gate. The wrong behaviour still ships, still builds, and is still scored. And this failure is
*worse* than P17's silent drop, not milder: a silent drop leaves an empty diff, which is at least
suspicious, while a half-materialization leaves **real memory code** a reviewer will read and approve.

**Alternative rejected — make the record best-effort at run time** (record if the shape allows, skip
otherwise). Rejected on **L1/L2**: it moves a compile-time decision to run time, where nobody sees it, and
makes the node's behaviour depend on a code shape the `config_hash` does not record.

🔴 The corollary that keeps this honest: a cell **cannot report `materializes` without emitting both**. It
is a property of the construction, not a rule to remember — the coverage read is derived from the same
table the materializer dispatches on (Decision 4).

(Full record: [decisions.md D2](./decisions.md).)

## Decision 2 — The key scheme is `{node_id, session_id}`, both required

Once entries are stored under a key, changing the key **orphans every stored memory** — the customer's
agent silently forgets everything. So the scheme is fixed before any code, and both parts are required:
an empty part is a typed error, never a default.

| Scheme | What breaks | Level |
|---|---|---|
| `node_id` only | every conversation shares one memory — a **cross-user leak**, visible only under real traffic | rejected **L1** |
| `session_id` only | nodes read memory they never wrote; a node runs a strategy it did not configure | rejected **L1/L2** |
| add `tenant_id` | a **second** isolation boundary beside the process — and when two exist, only one ends up enforced, never the stronger | rejected **L1** |
| default an empty session | silently merges conversations that should be separate; undetectable from inside the process | rejected **L1** |
| **`{node_id, session_id}`, both required (chosen)** | the caller must supply a session id — stated, and typed | — |

The refusal to invent a session id is the same fail-closed reflex the tool selection uses against an
unrecorded tool set: a defaulted scope is a **false acceptance**, and false acceptance is the failure mode
fail-closed exists to prevent.

(Full record: [decisions.md D1](./decisions.md).)

## Decision 3 — Count-based lifetime, sequence ordering, no clock

Expiry is `Expire(key, keepLast)`. Ordering is a store-assigned monotonic `Seq`. Nothing reads a
wall-clock.

A TTL is what every cache does, and it is wrong here for one reason: it makes recall depend on **when it
runs**. The same configuration, over the same conversation, returns different memory on a slow machine
than on a fast one, and different memory on a re-run than on the original. That breaks the single property
the entire eval path rests on — a `config_hash` denotes **one reproducible computation** — and an
unscorable axis cannot be optimized, which is the whole reason memory became a Dimension.

The same argument rules out timestamps as the ordering key: two writes in one millisecond are unordered,
and clock skew makes "oldest first" machine-dependent.

**Alternative rejected — a TTL, with determinism recovered by freezing the clock during evaluation.**
Rejected on **L2**: it makes the eval path correct and the production path different from it, so what is
scored is not what runs.

(Full record: [decisions.md D4](./decisions.md).)

## Decision 4 — One definition of a strategy's behaviour; the artifact calls it

Retention and recall semantics live in **one dispatch** (`internal/memoryruntime`). The generated artifact
**calls** it; it does not re-implement it.

This is P16's `SelectionPolicy.Retain` argument applied to the second axis that needs it, and its failure
mode is stated there in one sentence: **a diff that behaves differently from the strategy the
`config_hash` names, scored as that strategy.**

**Alternative rejected — emit self-contained retention logic per language**, so the artifact needs no
runtime dependency. Rejected on **L2 稳定 + 禁止分裂**: seven languages' worth of hand-written retention,
each a place `max_entries` can be off by one, each scored as though it were the sealed strategy. And the
copy that drifts is always the generated one, because it is the one nobody reads after it is written.

The same single-definition rule is what makes the **coverage read** trustworthy: `memoryCoverage` derives
from the materializer table the rewriter dispatches on, so the table cannot claim a cell the engine
refuses.

(Full record: [decisions.md D5](./decisions.md).)

## Decision 5 — The artifact calls nothing; host services are injected or the strategy refuses

The generated module **ships into the customer's repository and runs in their process**. It imports
nothing outside the standard library and makes no provider call. `Summarizer` and `Embedder` are
interfaces the caller supplies; a strategy needing one it was not given returns a typed refusal.

Two properties, and the second is the one that would be lost quietly:

1. **Credential isolation.** Every provider call on this platform is host-side through a trusted gateway.
   A generated file in a customer's tree is the furthest thing from a trusted host, and a credential
   reachable from code nobody reviewed line by line is a posture that cannot be walked back.
2. **No silent substitution.** The tempting fallback for a missing summarizer is "drop the oldest turns
   instead". That is not a degraded `summary-buffer` — it **is `scratchpad`**, running under a hash that
   says otherwise. The refusal is what keeps the strategy name meaningful.

(Full record: [decisions.md D3](./decisions.md).)

## Decision 6 — Coverage narrows per cell; the refusal is never removed

`memoryCoverage` stops being uniform and becomes a per-cell read. A covered (language, strategy,
call-shape) cell reports `materializes`; every other cell keeps its typed `unsafeRewrite` **and its own
cause**.

🔴 The P17 **totality canary must still pass for every uncovered cell**. Gaining a capability must not cost
the guarantee that an unmaterializable override is refused rather than dropped — and the canary is what
proves it, because it turns red when the refusal is sabotaged.

**Alternative rejected — flip the axis to "supported" and let each cell explain itself on failure.**
Rejected on **L1**: a reader told the axis works, who then meets a refusal at apply, has been handed
exactly the bait-and-switch P17's authoring decision (D7) rejected — one phase later and with more
credibility behind it.

The refusal ladder keeps P16's ordering, most-specific first, because the same failure applies:

```
is the strategy `none`?            → no edit, no refusal (identity)
does the call write a message list? → if not: the CALL is the reason (**kwargs) — permanent, actionable
can the record half land here?      → if not: name WHICH half — actionable
does this language have one yet?    → OURS, temporary  ← asked LAST
```

Only the last is a promise about future work. A refusal naming a cause the reader cannot act on is barely
better than one naming nothing.

(Full record: [decisions.md D6](./decisions.md).)

## Decision 7 — `config_hash` is untouched

Nothing this phase adds enters `config_hash`. The hashed projection stays exactly what P17 froze: strategy
+ params.

The session id is **run-time data**, in the same relationship `seed` has to a configuration: pinned per
run, deliberately excluded so results roll up under one config. Materialization status is a property of
the **engine's current capability**, not of the configuration — hashing it would mean the same variant
hashed differently before and after a rewriter landed, so a stored result could never be compared across
releases.

**Alternative rejected — hash the key scheme, since it changes what a node recalls.** Rejected on **L2**:
it is run-time scoping, not configuration, and including it would fragment one configuration across every
session that ever ran it, making the axis unaggregatable — the opposite of why it was modeled.

🔴 The concrete obligation: **every P17 `config_hash` reproduces bit-for-bit**, and `none` still hashes as
absent. This is what makes P17's promise to its users — *it materializes unchanged once the rewriter
lands* — literally true rather than aspirational.

(Full record: [decisions.md D7](./decisions.md).)

## Interfaces sketch

```
internal/memoryruntime                       ← the first missing artifact
  Key{NodeID, SessionID}                       both required; empty ⇒ ErrInvalidKey        (D2)
  Store{Append, Entries, Expire}               Seq-ordered; Expire is COUNT-based          (D3)
  Recall(strategy, params, store, key, msgs)   ONE dispatch over the closed set            (D4)
  Record(strategy, params, store, key, turn)   same dispatch — read and write are one unit
  Host{Summarizer, Embedder}                   INJECTED; missing ⇒ typed refusal, no fallback (D5)

internal/transform/memoryartifact.go         ← the generated module
  agentmem/agentmem.{py,go}                    dependency-free, byte-identical regeneration
  agentcfg/bindings.json                       strategy + params as DATA (ADR-004 line)

internal/transform/memorymaterialize{,_span}.go
  recall  : messages=[…]  →  messages=agentmem.recall("<node>", [...])   expression replacement (P16 shape)
  record  : resp = call(…) →  + agentmem.record("<node>", resp)          statement insertion (NEW class)
  🔴 both emit or neither does                                            (D1)

internal/transform/coverage.go
  memoryCoverage(lang)                         per-cell read of the materializer table      (D6)
  🔴 uncovered cells keep refuseMemory + the P17 totality canary

config_hash                                    🔴 UNCHANGED — strategy + params only        (D7)
```

## Risks

| Risk | Mitigation |
|---|---|
| A half-materialized memory ships and is scored | **D1** — both halves or refuse, by construction; a cell cannot report `materializes` without emitting both, because coverage derives from the materializer table |
| The generated artifact drifts from the runtime's semantics | **D4** — one dispatch, called rather than re-implemented; a static check forbids re-implementation in the emitted bytes |
| A strategy grows a customer's process without bound | Every bound asserted per strategy — an unbounded strategy is a memory leak in code we generated |
| Recall becomes machine-dependent | **D3** — count-based lifetime, sequence ordering, no clock read anywhere |
| A credential reaches generated code | **D5** — injected host services; dependency-freedom asserted over the emitted bytes, not by review |
| Gaining materialization silently weakens the refusal elsewhere | **D6** — the P17 totality canary must still turn uncovered cells red under sabotage |
| A P17 hash moves and orphans every recorded result | **D7** — the golden vectors and `none ≡ absent` are re-asserted in this phase, not assumed |
| The console over-claims the moment one cell materializes | **D6** — per-cell boundary read from the engine; P17's "not about your language" sentence is **changed**, because keeping it would be the opposite lie |
| A session id gets defaulted somewhere downstream to make integration easier | **D2** — the runtime refuses an empty scope; there is no code path that supplies one |
