# PRD — P18: The Memory Runtime and the Call-Site Rewriter (lifting P17's refusal, per cell)

| | |
|---|---|
| **Phase** | P18 (M21) |
| **Depends on** | [P17](P17-memory-strategy-optimization.md) (the modeled axis), [P16](P16-context-strategy-optimization.md) (the region-rewrite precedent), P10 (bound-mode artifacts), P2 (transform dispatch), P1 (IR) |
| **Status** | specification |
| **One-way doors** | [`decisions.md`](../../openspec/changes/archive/2026-07-31-p18-memory-runtime/decisions.md) D1–D7 |
| **Capabilities** | `memory-runtime` (new), `memory-materialization` (new) |

---

## 1. Summary

[P17](P17-memory-strategy-optimization.md) made memory a first-class Dimension: a strategy can be
referenced, resolved, hashed, proposed, and authored. Then the transform **refused**, and its refusal
named exactly what was missing:

> *a memory runtime (a store, a lifetime, and a key scheme) plus the call-site rewriter that reads and
> writes it*

P18 builds both, and lifts the refusal **per cell**.

The phase is governed by one decision, and every other choice follows from it: **both halves or refuse.**
A memory strategy is a *read* and a *write*. A call site that can carry the recall but not the record is
refused whole, because half a memory is not a weaker memory — it is a **different strategy**, one that no
`config_hash` names, running under a hash that claims another. That is P17's *"scored a configuration that
never ran"* failure re-introduced one layer down, and it is **harder to see**, because a diff genuinely was
emitted, the build passes, and a reviewer sees real memory code.

What the phase does **not** do is flip the axis to "supported". Coverage becomes a per-cell read of the
materializer table: a covered cell says so, and every uncovered cell keeps its typed refusal and its own
named cause. Gaining a capability must not cost the guarantee that an unmaterializable override is refused
rather than dropped.

## 2. Problem & context

Memory is the only optimization axis that is fully modeled and entirely unrealizable. Today a workflow
owner can pin `summary-buffer(max_tokens=2000)` on a node, see its `config_hash`, diff it against the
parent, and hand the id to a colleague — and none of it reaches their source. The `p17hermes` run states
the position exactly: **186 (node × strategy) combinations, 186 typed refusals, 0 diffs.**

That was the honest outcome for M20, and it is a poor one to stay at. An axis that can be described but
never applied cannot be *verified*, and an axis that cannot be verified cannot be **optimized** — which is
the entire reason memory became a Dimension. `OpMemoryPolicy` is catalogued and dormant for the same
reason: diagnosis proposes, verification decides, and verification has nothing to run.

Two things make this phase tractable now rather than earlier.

- **The region-rewrite precedent exists.** [P16](P16-context-strategy-optimization.md) faced the same
  shape — a dimension that is not an argument anywhere — and found the honest boundary: materialize what
  is a *transformation of an expression the author already wrote*, refuse what would be a construction.
  Memory's recall half is exactly that shape.
- **The artifact machinery exists.** P10's bound mode already generates a module plus a data document and
  ships them in the **same patch** as the call-site edit, so one revert restores everything. A memory
  runtime is another such artifact; it does not need a new delivery mechanism.

What is genuinely new is the **write** half. Recording a turn is not an expression replacement — it is a
statement that must run *after* the call. That is the edit class this phase adds, and it is why coverage
will be narrower than the recall half alone would suggest.

## 3. Goals & non-goals

### Goals

- **G1.** Ship a memory **runtime**: a store, a key scheme, a lifetime, and `Recall`/`Record` for all five
  builtin strategies, deterministic and bounded by construction.
- **G2.** Ship a **call-site rewriter** that materializes a complete memory (recall + record) where it can,
  in Python and Go, and refuses by name everywhere else.
- **G3.** Emit a **dependency-free, deterministic** generated artifact in the same patch as the call-site
  edit, carrying strategy and params as **data** so retuning is a document change.
- **G4.** Narrow the P17 refusal **per cell** without removing it; keep the totality canary green for every
  uncovered cell.
- **G5.** Wake `OpMemoryPolicy` where cells materialize — a memory proposal becomes verifiable — while
  keeping it refused-not-scored everywhere else.
- **G6.** Keep **every P17 `config_hash` bit-for-bit identical**, so a variant authored under P17
  materializes under P18 without being re-authored.

### Non-goals (explicitly deferred or owned elsewhere)

- **A durable/shared store.** The shipped store is in-process. The *semantics* live in the runtime, so
  swapping the store changes durability and nothing else — but a Postgres/Redis backing is a later phase,
  and the in-process choice is stated where a deployment sees it, never implied to be durable.
- **A provider call from generated code.** `summary-buffer` and `vector-recall` need host services, which
  are **injected**. The artifact calls nothing (D3).
- **Memory for languages beyond Python and Go.** Each additional language is a materializer plus a
  coverage row; the table states the gap rather than hiding it.
- **A new metric.** The improvement signal remains the classifier's existing `MemoryManagement` set. P18
  makes it *measurable*; it does not invent a number.
- **Any change to what a configuration IS.** No new hashed field, no change to the P17 projection (D7).
- **Context assembly.** Still [P16](P16-context-strategy-optimization.md)'s within-call axis; memory
  remains across-invocation.

## 4. Users & personas

| Persona | What P18 changes for them |
|---|---|
| **Workflow owner** | A memory strategy they author can now reach their source — where their call site admits it, stated per cell before they choose. |
| **Platform engineer** | `OpMemoryPolicy` stops being dormant on covered cells; memory variants enter the verified-delta ledger like any other. |
| **Reviewer** | A memory change arrives as a reviewable diff: a generated module plus a bounded call-site edit, revertible in one step. |
| **Security reviewer** | The generated artifact is dependency-free and makes no provider call; host services are injected, and a missing one refuses rather than substitutes. |

## 5. User stories / jobs-to-be-done

- *As a workflow owner*, I set `scratchpad(max_entries=5)` on a node that writes its messages out, and I
  get a diff that recalls prior turns and records this one — not a refusal.
- *As a workflow owner whose call site passes `**kwargs`*, I am told **that** is the reason, not that my
  language is pending — and the sentence stays true after every rewriter lands.
- *As a reviewer*, I can see the whole memory change in one patch and revert it in one step.
- *As a platform engineer*, I can finally ask the harness whether `summary-buffer` beats `scratchpad` on
  this node, because both now produce runnable variants.
- *As anyone reading a result*, a memory variant that was refused is still never scored.

## 6. Functional requirements

Each maps 1:1 to an OpenSpec requirement under
`openspec/changes/archive/2026-07-31-p18-memory-runtime/specs/`.

### The memory runtime (capability `memory-runtime`)

- **FR1.** A memory entry SHALL be scoped by a key of **`node_id` and `session_id`**, both required. An
  empty part SHALL be a typed error, never a default (D1).
- **FR2.** The runtime SHALL implement `Recall` and `Record` for all five builtin strategies through **one
  dispatch** over the closed set, so a strategy without an implementation fails loudly rather than
  silently no-op'ing.
- **FR3.** Recall SHALL be **deterministic**: identical store state and params SHALL produce byte-identical
  output, on any machine and on any re-run.
- **FR4.** Entry ordering SHALL be a store-assigned **monotonic sequence**, never a wall-clock timestamp.
- **FR5.** The lifetime SHALL be **count-based**. No wall-clock TTL SHALL affect what a recall returns (D4).
- **FR6.** Every strategy SHALL be **bounded by construction**: `scratchpad` never exceeds `max_entries`,
  `summary-buffer` never exceeds `max_tokens`, `vector-recall` never returns more than `top_k`.
- **FR7.** 🚫 The runtime SHALL make **no provider call**. Summarization and embedding SHALL be injected
  host services; a strategy needing one it was not given SHALL return a **typed refusal** and SHALL NOT
  fall back to a different behaviour (D3).
- **FR8.** A strategy's behaviour SHALL have **one definition**, read by the runtime and by the
  materializer alike; the generated artifact SHALL **call** it rather than re-implement it (D5).

### Materialization (capability `memory-materialization`)

- **FR9.** The transform SHALL emit a **generated memory module** alongside the call-site edit, in the
  **same patch**, so a single revert restores both.
- **FR10.** The artifact SHALL regenerate **byte-identically** for the same resolved configuration.
- **FR11.** The artifact SHALL be **dependency-free** — nothing outside the language's standard library.
- **FR12.** The artifact SHALL carry strategy and params **as data** read from the binding document, so
  retuning a parameter is a document change rather than a code change.
- **FR13.** **Recall** SHALL be materialized by replacing the written message-list argument with a call
  into the generated module — an expression replacement of something the author already wrote.
- **FR14.** **Record** SHALL be materialized as a statement following the call, gated on the call being a
  simple assignment at statement level.
- **FR15.** 🔴 **Both halves or refuse.** A cell SHALL materialize **only** when it can emit both recall
  and record. A call site admitting one half SHALL be **refused whole**, naming which half is missing
  (D2).
- **FR16.** The rewritten source SHALL **reparse**, and the edit SHALL be **minimal** — no untargeted line
  is touched.
- **FR17.** A call site passing `**kwargs` SHALL be refused with the **call-site** cause, not the platform
  cause; that sentence SHALL remain true after every rewriter lands.
- **FR18.** Coverage SHALL become a **per-cell** read of the materializer table: a covered cell reports
  `materializes`, every other cell reports `refuses` with its own cause (D6).
- **FR19.** 🔴 The typed refusal SHALL be **narrowed, never removed**. Every uncovered cell SHALL still
  return an `unsafeRewrite`, and the P17 totality canary SHALL still pass for those cells.
- **FR20.** 🔴 **No hash moves.** Every P17 `config_hash` SHALL reproduce bit-for-bit, `none` SHALL still
  hash as absent, and no field added by this phase SHALL enter `config_hash` (D7).
- **FR21.** `OpMemoryPolicy` SHALL produce a compilable, verifiable candidate where the cell materializes,
  and SHALL remain **refused-not-scored** where it does not.
- **FR22.** The authoring surface SHALL report **per-cell** applicability, and its P17 statement that the
  limit is language-independent SHALL be **changed**, because it is no longer true.

## 7. Non-functional requirements

| # | Requirement | Target |
|---|---|---|
| **NFR1** | **Determinism** | Identical store state + params ⇒ byte-identical recall, across machines and re-runs. Asserted, not inspected. |
| **NFR2** | **Boundedness** | No strategy can grow a store without bound; each bound is asserted per strategy, because an unbounded one is a memory leak in the customer's process. |
| **NFR3** | **Hash stability** | Every P17 `config_hash` reproduces bit-for-bit; the P0 golden vectors are unchanged. Machine-asserted. |
| **NFR4** | **Refusal survival** | The P17 totality canary passes for every cell without a materializer; a sabotaged refusal still turns cells red. |
| **NFR5** | **Credential isolation** | The generated artifact imports nothing outside the standard library and makes no provider call; asserted over the emitted bytes, not by review. |
| **NFR6** | **Regeneration** | The same resolved config regenerates the artifact byte-identically, so a re-apply produces no spurious diff. |
| **NFR7** | **Minimality** | A memory edit touches only its own call site's lines; the existing minimality gate admits no widened window. |
| **NFR8** | **One definition** | Retention semantics exist in exactly one place; a static check keeps the generated artifact from re-implementing them. |
| **NFR9** | **Completeness** | No cell reports `materializes` unless both halves are emitted; asserted by construction rather than by convention. |

## 8. System design summary

### 8.1 What moves, and what deliberately does not

```
P17 (unchanged, frozen)                      P18 (new)
────────────────────────                     ─────────
DimMemory ∈ closed enum                      internal/memoryruntime
NodeOverride.MemoryRef  ─┐                     Store · Key{node,session} · count lifetime
ResolvedNode.Memory     ─┼─► config_hash       Recall/Record × 5 strategies  ◄── ONE definition
memory_entry registry   ─┘   🔴 UNCHANGED      Host{Summarizer,Embedder} injected, never called
                                             internal/transform/memoryartifact.go
refuseMemory (both engines) ──► NARROWED       generated module + params as DATA, same patch
                                on covered    internal/transform/memorymaterialize{,_span}.go
                                cells only      recall  = expression replacement  (P16's shape)
OpMemoryPolicy (dormant) ─────► WAKES on        record  = statement insertion     (new edit class)
                                covered cells   🔴 BOTH OR REFUSE
```

### 8.2 Why "both halves or refuse" is the whole design

| Emitted | What the node actually does | What the hash claims | Verdict |
|---|---|---|---|
| recall only | recalls from a store nothing fills ⇒ behaves as `none` | `summary-buffer` | **refused** |
| record only | fills a store nothing reads ⇒ behaves as `none`, unbounded | `summary-buffer` | **refused** |
| both | the strategy the hash names | the same | **materializes** |
| neither | nothing | — | **refused** (P17's behaviour, unchanged) |

The two rejected rows are not degraded modes. They run **`none`** under another strategy's hash — and
unlike P17's silent drop, they leave a real diff behind, so nothing looks wrong.

### 8.3 The refusal ladder, most-specific first

Inherited from P16's ordering fix, because the same failure applies:

1. is the strategy `none`? → nothing to do, no edit, no refusal
2. does this call site write a message list? → if not, the **call** is the reason (`**kwargs`) — permanent, actionable
3. can the record half land here? → if not, name **which half** — actionable
4. does this language have a materializer yet? → **ours**, temporary

Only the last is a promise about future work. A refusal naming a cause the reader cannot act on is barely
better than one naming nothing.

## 9. Design by role lens

| Role | The question they ask | The answer |
|---|---|---|
| **System designer** | which doors close here? | D1 key scheme, D3 no-provider-call, D4 count lifetime — all storage/execution contracts over customer data |
| **Backend** | where does behaviour live? | one dispatch in `memoryruntime`; the artifact calls it (D5) |
| **AI engineer** | can the operator finally be scored? | on covered cells, yes; elsewhere still refused-not-scored (FR21) |
| **QA** | what must go red? | half-materialization, an unbounded strategy, a moved hash, a sabotaged refusal |
| **Security** | what ships into the customer's tree? | a dependency-free module that calls nothing; host services injected (FR7, FR11) |
| **Product** | what changes on the surface? | per-cell applicability, and the P17 "not your language" sentence must go (FR22) |

## 10. Dependencies

Requires **P17** (the modeled axis and its frozen hash contract), **P16** (the region-rewrite precedent and
the list splitters), **P10** (bound-mode artifact emission), **P2** (transform dispatch, `config_hash`),
**P1** (IR and call-site indexing), **P4/P4.5** (the harness and catalog that consume the now-verifiable
variants).

## 11. Risks & mitigations

| Risk | Mitigation |
|---|---|
| A half-materialized memory ships and is scored | **D2/FR15** — both halves or refuse, asserted by construction; a cell cannot report `materializes` without emitting both |
| The generated artifact drifts from the runtime's semantics | **D5/FR8** — one dispatch, called rather than re-implemented; a static check forbids re-implementation |
| A strategy grows a customer's process without bound | **FR6** — every bound asserted per strategy |
| Recall becomes machine-dependent | **D4/FR3–FR5** — count-based lifetime, sequence ordering, no clock |
| A credential reaches generated code | **D3/FR7/FR11** — injected host services; dependency-freedom asserted over emitted bytes |
| Gaining materialization silently drops the refusal elsewhere | **D6/FR19** — the P17 canary must still pass for uncovered cells |
| A P17 hash moves and orphans recorded results | **D7/FR20** — golden vectors and `none ≡ absent` re-asserted in this phase |
| The console over-claims once one cell materializes | **FR18/FR22** — per-cell boundary read from the engine; the old copy is *changed*, not kept |

## 12. Rollout & test strategy

**18a** — the runtime (§1) and the artifact (§2): complete, testable, and shipping nothing to a call site.
**18b** — the Python and Go materializers (§3, §4) and the per-cell coverage narrowing (§5).
**18c** — the operator waking (§6) and the surfaces (§7), then verification on a real repository (§8).

Must-fail tests: half-materialization refused whole; an unbounded strategy; a moved `config_hash`; a
sabotaged refusal that no longer turns uncovered cells red; a generated artifact that imports anything.

## 13. Success metrics & acceptance criteria (M21 exit checklist)

- [ ] **A1.** All five strategies implement `Recall`/`Record` through one dispatch; a sixth without one
      fails loudly.
- [ ] **A2.** Recall is deterministic and every strategy is bounded, asserted per strategy.
- [ ] **A3.** The artifact regenerates byte-identically, imports nothing, and calls no provider.
- [ ] **A4.** A complete memory materializes at a Python call site that writes its messages and assigns
      its result; the diff carries both halves and the source reparses.
- [ ] **A5.** 🔴 A call site admitting only the recall is **refused whole**, naming the missing half.
- [ ] **A6.** A `**kwargs` call site is refused with the call-site cause.
- [ ] **A7.** 🔴 Every P17 `config_hash` reproduces bit-for-bit; `none` still hashes as absent.
- [ ] **A8.** 🔴 The P17 totality canary still turns uncovered cells red when the refusal is sabotaged.
- [ ] **A9.** `OpMemoryPolicy` compiles a candidate on a covered cell and stays refused-not-scored
      elsewhere.
- [ ] **A10.** The console states per-cell applicability; the "not about your language" sentence is gone.
- [ ] **A11.** A real-repository run reports what moved and what did not, with counts rather than claims.

## 14. Open questions

1. **Where does the session id come from** in a workflow the platform did not write? The runtime requires
   one and refuses to invent it (D1); the *plumbing* — a parameter, a context value, a framework hook —
   is per-integration, and the first materializer should state which it assumes rather than generalize
   early.
2. **Should a durable store ship in this phase or the next?** The semantics are store-agnostic by
   construction, so this is a delivery question rather than a design one — but an in-process store must
   never be *implied* to be durable on any surface.
3. **Does the record half deserve a second shape** (a `with` block, a decorator) for call sites that are
   not simple assignments? Each is a new edit class with its own admission rule, and D2's answer is that a
   shape without a safe record is refused until it has one — not approximated.
