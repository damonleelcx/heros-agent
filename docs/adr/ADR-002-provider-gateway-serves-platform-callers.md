# ADR-002 — The provider gateway serves platform callers; the transformed program calls its own SDKs

- **Status:** Accepted (2026-07-17)
- **Deciders:** System Design (proposed) + User (ratified)
- **Amends:** `docs/prd/P2-config-runtime.md` FR12; `openspec/changes/p2-config-runtime/specs/runtime/spec.md`
  ("Models SHALL be invoked through a unified provider gateway so that provider swaps are transparent")
- **Relates to:** [ADR-001](ADR-001-source-transformation-apply-model.md) (source-transformation apply model)
- **Extended by:** [ADR-005](ADR-005-forge-delivery-and-credential-posture.md) — applies this ADR's
  refusal of customer-side reach to a second dimension. Here the platform declines a place in the
  customer's **runtime**; there it declines a standing write credential to their **repository**, by
  having the customer's own CI open the pull request with the token it already holds.
- **Closes:** the open question raised in [`docs/decisions/m2-exit-review.md`](../decisions/m2-exit-review.md) §4

## Context — what problem this solves

P2's exit review could not be signed off because **two accepted requirements contradict each other**,
and no amount of implementation work could satisfy both:

- **FR12** (written before ADR-001): models *"SHALL be invoked through a unified provider gateway
  such that swapping a node's provider makes the transform rewrite only its `model_ref` at the call
  site."*
- **ADR-001**: the system applies configuration by **transforming source code**; the Runtime
  **executes the transformed copy**, and *"measurements are faithful — the eval harness runs the real,
  transformed code."*

Both hold only if the codemod rewrites the user's call sites to route through our gateway. Three P2
tasks are blocked on which reading wins: 2.2 (how many dimensions the codemod rewrites), 3.2 (skill
binding at the call site), and 4.2 (provider swap as a `model_ref` rewrite).

The code already made a de-facto choice — `internal/transform/rewrite.go:59` refuses a cross-provider
swap with the comment *"a provider swap is a gateway concern (FR12), not a call-site argument
rewrite"* — but a comment in a rewriter is not a ratified decision, and it points at FR12 while doing
the opposite of what FR12 asks. That drift is the thing this ADR ends.

## Decision

**The unified provider gateway is the call path for models invoked *by the platform*. The transformed
program under measurement calls its own provider SDKs directly, exactly as it does in production.**

- **Platform callers** — the P4 eval harness, the P5.5 verifier, the P6 optimizer, and any model call
  *we* make on our own behalf — go through `internal/providergateway`. Normalization, timeout,
  bounded backoff, idempotency keys, secret injection and scrubbing all apply there.
- **The user's transformed program** keeps its own SDK calls. We rewrite *arguments* at its call
  sites (per ADR-001); we do not insert ourselves into its runtime.
- **Within-provider model swap** (e.g. Sonnet → Opus on an Anthropic call site) is a `model_ref`
  rewrite and works today.
- **Cross-provider swap** (Anthropic → OpenAI at a *user* call site) means rewriting the SDK call
  itself — a different client, a different request shape, a different response type. That is a real
  codemod, it is **not implemented, and it is out of P2 scope.** The transform refuses it with a
  typed error rather than emitting a diff it cannot guarantee. See "Consequences" for where it lives.

## Why this design — the arbitration

Applying [八级法则](../../../aikeylabs-skills/shared/00-核心法则.md) (安全 > 稳定 > UX > 运维 >
不可演进 > 不可扩展 > 维护 > 实现). Per **L2**, the comparison is settled at the highest level where
the options differ, and we do not fall back to a lower-level convenience:

### The FR12 reading fails at L1 and L2 — and it fails on a dilemma, not a cost

If the codemod routes the user's call sites through our gateway, the resulting PR either merges with
that dependency or it does not. **Both branches are fatal:**

| Branch | Consequence |
|---|---|
| **The gateway dependency merges** | We have rewritten the customer's production architecture — their live traffic now flows through our component. That is not "behavior-preserving except for the intended change" (ADR-001 requirement 3). It is the single largest change we could possibly make to their system, delivered as a side effect of a prompt tweak. **L1 (new credential/traffic path through us) and L2 (our uptime becomes their uptime).** |
| **The dependency is stripped before merge** | We measured code that is not the code that ships — **precisely the flaw that killed the shim model** in ADR-001 ("a shimmed run does not exercise the code that will actually ship"). **L2 (the measurement no longer supports the decision it is used to justify).** |

There is no third branch. FR12-as-written is not merely more expensive than the alternative; it is
**incoherent with the purpose of ADR-001**, which is why no implementation could have satisfied both.

### Why the chosen design is the right one

- **L1 安全**: our gateway never holds, sees, or proxies the customer's production model traffic. The
  blast radius of a gateway compromise stops at our own eval calls.
- **L2 稳定**: the customer's program has no runtime dependency on us. We cannot take their
  production down. And the measured artifact *is* the shipped artifact, so the numbers we bill and
  propose against (P7 gainshare) mean what they claim.
- **L5 可演进**: the gateway stays a *platform-internal* seam. We can swap its implementation, add
  providers, or replace it wholesale without touching a single customer's code — because no customer
  code ever depended on it. The FR12 reading would have made our gateway's wire shape a **published
  contract inside customer repositories** — a one-way door slammed shut in every repo we ever
  touched.
- **L8 实现**: cheaper too — but this is the *lowest* priority item and is **not** why we chose it.
  Per **L3**, it could not be. The decision is made at L1/L2 and holds regardless of cost.

### Alternatives considered

| Option | Verdict |
|---|---|
| **A. Codemod routes call sites through our gateway** (FR12 literal) | ❌ Rejected on the dilemma above — L1/L2 violation on either branch. |
| **B. Gateway serves platform callers only** (chosen) | ✅ No customer runtime dependency; measurement fidelity preserved; gateway stays evolvable. |
| **C. Keep both requirements, decide per-node at runtime** | ❌ Rejected. This is the "average code that satisfies both rules" that [沟通规范 §4](../../../aikeylabs-skills/shared/02-沟通与协作规范.md) forbids. It inherits A's blast radius on any node that opts in, and adds a config axis that changes what a measurement *means* — two runs of the same `config_hash` would no longer be comparable. |
| **D. Drop the gateway entirely; every caller uses SDKs directly** | ❌ Rejected at L6/L5. P4 fan-out, P6's cost-aware tiering, and P7's metering all need one normalized call path with one idempotency-key derivation point. Removing it re-scatters that across every caller. |

## Consequences

**Positive**
- P2's blocking question is closed; tasks 2.2, 3.2 and 4.2 have a decidable answer.
- `internal/transform/rewrite.go:59`'s refusal is now **correct and ratified**, not a drift from spec.
  Its comment must stop citing FR12 as the reason and cite this ADR.
- `executor.CallProvider` remains the single idempotency-key derivation point — correct under this
  reading, as the exit review already observed.

**Negative / accepted cost**
- 🔴 **Cross-provider swap at a user call site is a capability we do not have.** Sales-operations must
  not promise it (only-promise-what-shipped). The honest statement is: *"we swap models within a
  provider today; cross-provider swap requires rewriting the SDK call and is not shipped."*
- A future cross-provider codemod is a **per-(source-SDK, target-SDK) pair** of rewriters — real work,
  correctly sized, and now explicitly *not* hiding inside "the gateway makes swaps transparent."
  It belongs to a later phase with its own PRD, not to P2.

## Follow-through (required by this ADR)

1. `docs/prd/P2-config-runtime.md` FR12 — reword to scope the gateway to platform callers.
2. `openspec/changes/p2-config-runtime/specs/runtime/spec.md` — reword the gateway requirement and
   its "Provider swap rewrites only the model_ref" scenario.
3. `openspec/changes/p2-config-runtime/tasks.md` 4.2 — reword; it currently claims the opposite.
4. `internal/transform/rewrite.go:59` — cite ADR-002, not FR12.
5. `docs/decisions/m2-exit-review.md` §4 — mark the open question resolved, linking here.
