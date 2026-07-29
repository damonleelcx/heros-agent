# P18 — Recorded decisions (System Designer, §0)

Seven contracts fixed **before any code ships**, because each is a one-way door.

[P17](../p17-memory-strategy-optimization/) modeled memory and refused at transform, naming exactly two
missing artifacts: *a memory runtime (a store, a lifetime, and a key scheme) plus the call-site rewriter
that reads and writes it.* This phase builds both. That makes the doors here of a different kind from
P17's: P17's were about **identity** (what a configuration IS), and every one of them is now frozen and
must not move. P18's are about **behaviour** (what a configuration DOES) and about **what ships into the
customer's repository** — a generated file, keyed data, and a rewritten call site, none of which can be
un-shipped once a customer has run them.

Three create a storage or execution contract that cannot be changed without orphaning stored data (D1,
D3, D4). Two define a behavioural boundary that cannot be cleanly un-drawn (D2, D5). Two bind this phase
to P17's frozen guarantees so gaining a capability does not cost an honesty property (D6, D7).

Each walks: **Problem → Decision → Why appropriate → Alternatives + decision point (rejected on L-level) →
Effect**, and carries its governing 八级法则 level.

---

## D1 — The key scheme is **(node_id, session_id)**, and both parts are required

**Problem.** A memory store has to answer "whose memory is this?". P17's refusal named the key scheme as
one of the three missing pieces precisely because it is not derivable: the same workflow serves many
conversations, and the same conversation passes through many nodes. Once entries are stored under a key,
changing the key **orphans every stored memory** — a customer's agent silently forgets everything it
learned. This is the most literal one-way door in the phase.

**Decision.** A memory entry is scoped by **`Key{NodeID, SessionID}`**, and **both parts are required** —
an empty part is a typed `ErrInvalidKey`, never a default. The caller supplies the session id; the runtime
never invents one.

**Why this is the appropriate design.** The two failures the two halves prevent are different in kind, and
each is invisible in single-user testing:

- **Node-only** would make every conversation in a workflow share one memory. Two users' facts land in one
  scratchpad, and one user's entity memory is recalled into another's prompt. That is a **data-leak
  shape**, not a correctness bug, and it appears only under concurrent real traffic.
- **Session-only** would make two different nodes in one workflow share a store, so a summarizer's rolling
  summary is recalled by a classifier that never wrote it. The node runs a memory it did not configure.

Refusing an empty part rather than defaulting it is the same fail-closed reflex the tool selection uses
against an unrecorded tool set: a defaulted session id **merges conversations that should be separate**,
and the merge is undetectable from inside the process.

**Alternatives + decision point.**

| Option | What breaks | Level |
|---|---|---|
| `node_id` only | every conversation shares one memory — a cross-user leak, visible only under real traffic | rejected **L1 安全** |
| `session_id` only | nodes read memory they never wrote; a node runs a strategy it did not configure | rejected **L1/L2** |
| add a `tenant_id` part | a SECOND isolation boundary beside the process, and when two mechanisms exist only one ends up enforced — never the stronger | rejected **L1** |
| default an empty session id | silently merges conversations; undetectable from inside the process | rejected **L1** |
| **`{node_id, session_id}`, both required (chosen)** | caller must supply a session id — stated, and typed | — |

**Effect.** Memory is scoped per (call site, conversation); a missing scope fails closed with a named
error. No tenant field exists, so nobody can mistake it for the isolation boundary (task 1.1).

---

## D2 — **Both halves or refuse**: a cell materializes iff it can emit recall AND record

**Problem.** A memory strategy is a **read and a write**. The read (recall prior turns into this call) and
the write (record this turn for later) are materialized by *different edits* at a call site — an expression
replacement and a statement insertion — and a given call site may admit one and not the other. What
happens then? This is the phase's central decision and the one most exposed to shipping pressure, because
"we can at least do the read" is always available and always sounds like progress.

**Decision.** A cell materializes **only when it can emit both halves**. A call site that can carry the
recall but not the record is **REFUSED WHOLE**, with the refusal naming which half is missing.

**Why this is the appropriate design.** Half a memory is not a weaker memory — it is a **different
strategy**, and one that no `config_hash` names:

- **Recall without record** reads from a store nothing ever fills. Every call recalls nothing. The node
  behaves exactly like `none` while its `config_hash` claims `summary-buffer`.
- **Record without recall** fills a store nothing ever reads. The node behaves like `none` while paying
  the write cost, and the store grows unboundedly in the customer's process.

Both are P17's *"scored a configuration that never ran"* failure, re-introduced one layer down and
**harder to see** — because unlike a silent drop, something genuinely was emitted, so the diff is
non-empty, the build passes, and a reviewer sees real memory code. The eval would then attribute a result
to `summary-buffer` that `none` produced. An emitted-but-wrong diff is worse than no diff, for the same
reason ADR-001 names a plausible-but-wrong codemod as the top risk.

Refusing whole is also the only option that stays **honest under the P17 contract it inherits**: P17's
guarantee is that a memory override is applied or refused, never partially applied. A half-materialization
is exactly a partial application wearing a success badge.

**Alternatives + decision point.**

| Option | What "succeeds" | Failure mode | Level |
|---|---|---|---|
| Recall-only where the record cannot land | a diff | recalls from an empty store forever; behaves as `none` under another hash | rejected **L1** |
| Record-only | a diff | fills a store nothing reads; unbounded growth in the customer's process | rejected **L1/L2** |
| Emit both, warn when the record is best-effort | a diff + a warning | a warning is not a gate; the wrong behaviour still ships and is still scored | rejected **L1** |
| **Both halves or refuse (chosen)** | only a complete materialization | fewer cells covered — **stated per cell, with the missing half named** | — |

**Effect.** Every materialized memory cell is a *complete* memory. A call site that admits only one half
is refused with a sentence naming which half and why (tasks 3.3, 4.2). Coverage counts complete cells
only, so the table cannot over-claim.

---

## D3 — The generated artifact makes **no provider call**; host services are injected or the strategy refuses

**Problem.** `summary-buffer` needs a summarizer and `vector-recall` needs an embedder. The generated
memory module **ships into the customer's repository and runs in their process**. If it called a provider
itself it would need a credential there. Once a generated file has read a credential in a customer's
process, that is a security posture that cannot be walked back.

**Decision.** The runtime calls **nothing**. `Summarizer` and `Embedder` are interfaces the caller
supplies. A strategy that needs a service it was not given returns a **typed refusal**
(`ErrNoSummarizer` / `ErrNoEmbedder`) and **never falls back** to a cheaper behaviour.

**Why this is appropriate.** Two separate properties, and the second is the one that would be lost quietly:

1. **Credential isolation.** The platform's own rule is that every provider call is host-side through a
   trusted gateway; a generated artifact in the customer's tree is the furthest thing from a trusted host.
   This mirrors the context policies' `HostServices` seam exactly — same problem, same answer.
2. **No silent substitution.** The tempting fallback for a missing summarizer is "drop the oldest turns
   instead". That is not a degraded `summary-buffer`; it **is `scratchpad`**, running under a
   `config_hash` that says `summary-buffer`. The refusal is what keeps the strategy name meaningful.

**Alternatives + decision point.** Let the artifact call the provider directly — fewer moving parts, no
injection. Rejected on **L1 安全**: a credential in the customer's process, reachable from generated code
nobody reviewed line by line. And: fall back to truncation when no summarizer is present — rejected on
**L1 honesty**, because it runs a different strategy than the hash names, which is the exact defect the
whole memory axis was built to prevent.

**Effect.** The generated artifact is dependency-free and calls nothing (tasks 1.5, 2.4). A missing host
service is a loud, typed refusal at run time rather than a quiet change of strategy.

---

## D4 — The lifetime is **count-based**, never time-based

**Problem.** A store needs a lifetime or it grows without bound in a customer's process. The obvious
lifetime is a TTL. Once entries carry expiry semantics, changing them changes what every stored memory
recalls — a one-way door over customer data.

**Decision.** Expiry is **count-based** (`Expire(key, keepLast)`). No wall-clock TTL. Entry ordering is a
store-assigned monotonic `Seq`, not a timestamp.

**Why this is appropriate.** A time-based lifetime makes recall depend on **when it runs**. The same
configuration, over the same conversation, returns different memory on a slow machine than on a fast one,
and different memory on a re-run than on the original. That breaks the single property the entire eval
path rests on: a `config_hash` denotes **one reproducible computation**. A count is a property of the
conversation itself, so it is identical everywhere.

The same argument rules out timestamps as the ordering key: two writes in the same millisecond are
unordered, and clock skew across machines makes "oldest first" machine-dependent.

**Alternatives + decision point.** A TTL, which is what every cache does. Rejected on **L2 稳定 +
determinism**: it makes the axis unscorable, and an unscorable axis cannot be optimized — which is the
entire reason memory became a Dimension. The L2 loss buys an L8 familiarity.

**Effect.** Recall is deterministic and reproducible across machines and re-runs (tasks 1.3, 1.4). Bounds
are enforced by construction per strategy, so no strategy can leak memory in a customer's process.

---

## D5 — A strategy's behaviour has **one definition**, read by the runtime and the materializer alike

**Problem.** The materializer must emit code that behaves exactly as the runtime says the strategy
behaves. If the emitted code decided retention itself, there would be **two definitions** of what
`scratchpad(max_entries=5)` retains.

**Decision.** The retention/recall semantics live in **one dispatch** (`internal/memoryruntime`), and the
generated artifact **calls it** rather than re-implementing it. The materializer emits a call, never a
policy.

**Why this is appropriate.** This is P16's `SelectionPolicy.Retain` argument, applied to the second axis
that needs it, and its failure mode is stated there in one sentence: a diff that behaves differently from
the strategy the `config_hash` names, **scored as that strategy**. Two implementations of one question is
禁止分裂 source-of-truth, and the copy that drifts is always the generated one — because it is the one
nobody reads after it is written.

**Alternatives + decision point.** Emit self-contained retention logic per language, so the artifact needs
no runtime. Rejected on **L2 稳定 + 禁止分裂**: seven languages' worth of hand-written retention, each a
place `max_entries` can be off by one, each scored as though it were the strategy the registry sealed.

**Effect.** One definition, two readers (task 1.6). A strategy added later cannot silently no-op in the
generated path, because there is no second path to add it to.

---

## D6 — Coverage **narrows per cell**; the refusal is never removed wholesale

**Problem.** P17's coverage says every non-identity strategy refuses in every language, uniformly, and its
console copy says *"this is not about your language."* P18 makes some cells materialize. The tempting move
is to flip the axis to "supported".

**Decision.** `memoryCoverage` becomes a **per-cell read of the materializer table**. A covered
(language, strategy, call-shape) cell reports `materializes`; every other cell still refuses **with its
own cause**. The typed refusal is narrowed, never deleted.

**Why this is appropriate.** The axis stops being uniform the moment one cell materializes, and a surface
that kept saying "no language has one" would then be lying in the *opposite* direction — over-refusing
instead of over-claiming. Both are the same defect: a coverage claim that does not match the engine. The
only stable answer is the one the other axes already use — read the table, state the cell.

🔴 And the P17 **totality canary must still pass for every uncovered cell**. Gaining a capability must not
cost the guarantee that an unmaterializable override is refused rather than dropped.

**Alternatives + decision point.** Flip the axis to "supported" and let each cell explain itself when it
fails. Rejected on **L1**: a reader who is told the axis works and then meets a refusal at apply has been
given the bait-and-switch P17 D7 rejected, one phase later.

**Effect.** Coverage is total and per-cell (task 5.1); the canary still fires for uncovered cells (5.2);
the console's boundary becomes per-cell and its "not about your language" copy **changes, because it is no
longer true** (7.2).

---

## D7 — `config_hash` is **untouched**: this phase changes what is EMITTED, never what a configuration IS

**Problem.** P18 adds a runtime, an artifact, a key scheme, and two new edit classes. Any of them could
plausibly be argued into the hashed shape — the key scheme especially, since it affects behaviour.

**Decision.** **Nothing in this phase enters `config_hash`.** The hashed projection stays exactly what P17
froze: strategy + params. The session id, the store, the lifetime bound, the artifact path, and the
materialization status are all outside it.

**Why this is appropriate.** `config_hash` denotes a **configuration**, not an execution. A session id is
run-time data (the same relationship `seed` has: pinned per run, deliberately excluded so multi-seed
results roll up under one config). Materialization status is a property of the ENGINE's current
capability, not of the configuration — hashing it would mean the same variant hashed differently before
and after a rewriter landed, so a stored result could never be compared across releases.

🔴 Concretely: **every P17 `config_hash` must reproduce bit-for-bit**, and `none` must still hash as
absent. If this phase moved a hash, every result P17 recorded would orphan, and the axis would have gained
a capability by breaking the ledger it exists to fill.

**Alternatives + decision point.** Hash the key scheme, since it changes what the node recalls. Rejected on
**L2 稳定**: it is run-time scoping, not configuration, and including it would fragment one configuration
across every session that ever ran it — making the axis unaggregatable, which is the opposite of why it
was modeled.

**Effect.** P17's golden vectors and every recorded hash reproduce unchanged (task 5.3). A variant authored
under P17 and materialized under P18 is **the same variant**, which is what makes P17's promise — *it
materializes unchanged once the rewriter lands* — literally true rather than aspirational.
