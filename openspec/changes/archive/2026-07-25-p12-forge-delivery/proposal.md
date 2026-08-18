## Why

[ADR-001](../../../docs/adr/ADR-001-source-transformation-apply-model.md) made the pull request the
product's entire output — *"the optimization ships as a pull request; git is the audit trail and
`git revert` is rollback."* P5.5's automation levels are specified almost entirely in PR verbs, and
P6's Autonomous level is auto-merge. **None of it can happen**, because there is no path from a
verified transform to a customer's repository:

- **No push route.** `worktree.NewPool` clones `--bare --local` from a filesystem path, so a variant
  branch exists only in a local mirror.
- **No forge credential kind.** `Secrets` is provider-scoped; a forge token is a different thing.
- **Nowhere to record the result.** `transform` is immutable by DB trigger (`0004:transform_immutable`)
  and the pull request would be opened *after* insert, because the build must pass first — so the
  `UPDATE` is rejected **by design**.

The P2 exit analysis ([`../p2-config-runtime/tasks.md`](../p2-config-runtime/tasks.md) §3.10)
inventoried all of this honestly, listed three options — push to the customer's remote, PR to a fork we
own, or defer — and parked at **defer**, because two of the gaps are one-way doors requiring sign-off.

**The commercial consequence is exact.** P7 computes billable savings *only* from **merged-PR deltas**
in the P5.5 verified-delta ledger. Every other link in that chain exists — P4 measures, P5.5 verifies,
P7 bills — and it terminates at a step that cannot occur. **Gainshare revenue is precisely zero**, no
matter how much value the platform demonstrably produces.

[ADR-005](../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md) resolves this with an
option the P2 analysis did not consider, because it framed the question as *"how do **we** get write
access"* rather than *"who should hold the credential."* The answer: **the customer's CI already holds
one** — repo-scoped, short-lived, issued and rotated by the forge, designed for exactly this. Using it
means the credential the analysis called *"the highest blast-radius action in the system"* is never
created on our side, which is consistent with ADR-002's refusal of the same class of reach in the
runtime dimension. A hosted Git App remains available for customers who cannot run delivery in CI,
contained per-repository and described honestly as what it is.

## What Changes

- **New capability `forge-delivery`.** Delivery in **two modes producing identical pull requests** —
  the mode is a credential decision, not a product difference. **CI-mediated is the default**: the
  customer's CI opens the pull request using the ephemeral, repo-scoped token it already holds, and the
  platform **does not receive, store, or request a forge credential**. The **hosted Git App** is opt-in:
  **per-repository** (never org-wide by default), a **least-privilege** permission set no broader than
  opening and updating pull requests on the selected repositories, **revocable by the customer without
  contacting the platform**, never logged, never leaving the platform. **Nothing unverified is
  delivered** — the P5.5 gate is upstream of delivery and is not a thing delivery can route around.
  Every pull request carries its **evidence**: the diff, the **verified delta with its confidence
  interval**, the eval evidence, `config_hash` lineage, and a reference that opens the full evidence in
  the console — which is also what makes a Team+ subscription visible to reviewers who never open the
  dashboard. Delivery is **idempotent**: re-delivering the same `(config_hash, source_revision)`
  **updates** the existing pull request rather than opening a second, because a duplicate-PR storm is
  the fastest way to lose repository access permanently. A **superseded** proposal's pull request is
  **closed with the reason stated**, open pull requests per repository are **bounded** with the bound
  being **reported** rather than silently dropping deliveries, and the platform **never merges below the
  Autonomous level** (Enterprise) — and under Autonomous only a gate-passed, verified change. An active
  fleet or per-tenant **halt stops delivery**, and a halt state that cannot be read **fails closed**.
  A repository with verified proposals and **no delivery route** surfaces a **reported state** rather
  than accumulating them invisibly, and losing delivery capability — App removed, CI credential rotated
  — is a **reported degraded state**, never a silent stop. The platform writes **only** pull requests
  and their branches: no direct pushes to protected branches, no tags, no releases, no issues.
- **New capability `delivery-record`.** An **append-only** `delivery` record keyed by
  `(config_hash, source_revision, forge_ref)` capturing every delivery and every subsequent state
  change, including **which mode** was used so a later audit can answer which credential opened a given
  pull request. **`transform` is not modified** — its immutability is what makes `config_hash`
  reproducibility checkable, and a delivery is a different kind of fact anyway: a transform is produced
  **once**, whereas a delivery has a lifecycle (opened → updated → superseded / closed / merged →
  possibly reverted) and the same transform may legitimately be delivered to more than one target.
  Append-only rather than mutable state means a merge that is later reverted reads as a **sequence**
  rather than as a field someone overwrote, which matters when a gainshare invoice is disputed. A
  **merge into the customer's target branch** is recorded against its delivery, giving P7's
  verified-savings computation an observable input, and the console shows each delivery's state —
  **open / merged / closed / superseded** — linked to the proposal that produced it.
- **Entitlement.** Delivery is gated **server-side** to **Team and above**; Autonomous auto-merge to
  **Enterprise** — consistent with P7's existing entitlements spec, which is not modified.
- **Not changed here.** Verification stays P5.5's (delivery is downstream of the gate, never a way
  around it); the autonomous loop and its kill switch stay P6's (delivery honours the halt, it does not
  implement the loop); the CLI and CI action stay [P11](../p11-cli-ci-integration/)'s (CI-mediated
  delivery *runs inside* P11's integration through the hook P11 exposes); billing computation stays
  P7's. No repository hosting, mirroring, or fork-we-own — rejected in ADR-005 on review quality, fork
  drift, and the fact that gainshare depends on observing a merge into the customer's default branch,
  which a fork cannot see. No code-review product: the pull request is reviewed in the customer's own
  tooling under their own branch protections.

## Impact

- **Affected capabilities:** `forge-delivery` (new), `delivery-record` (new). Consumed, not modified:
  `verification`/`proposal-engine` (P5.5), `autonomous-optimizer` (P6), `entitlements`/`metering` (P7),
  `config-layer`/`runtime` (P2), `eval-harness` (P4), `web-console` (P9), `ci-integration` (P11).
- **Affected code/systems:** a forge client and per-forge delivery recipes; the CI-mediated delivery
  step running inside P11's integration hook; a Git App installation flow and token custody for the
  opt-in mode; **one new table** (`delivery`), decided in ADR-005 against two alternatives per 🔴
  `careful-table-creation`; and console surfaces for delivery state. **`transform` and its immutability
  trigger are untouched.**
- **Dependencies:** requires **P2** (codemod, worktree, the immutable transform record), **P4** (eval
  evidence), **P5.5** (the gate and the verified-delta ledger), **P6** (automation levels + kill
  switch), **P7** (entitlement + gainshare), **P9** (delivery state in the console), **P11** (the CI
  integration hook), and **ADR-005** (credential posture + the delivery-record decision).
- **Unblocks:** **gainshare revenue becomes computable** — merged-PR deltas finally have merges to
  observe; **P5.5's Advisory and Assisted levels become real**, both being specified in PR verbs today
  with no mechanism; and **P6's Autonomous auto-merge** gains the mechanism it merges through.
- **Breaking:** none. Delivery is additive, entitlement-gated, and off until a repository has a
  configured route.
- **Sequencing:** **12a** (CI-mediated) is a complete phase on its own and holds no credential. **12b**
  (hosted Git App) is sequenced second **deliberately** — it is the mode that carries standing write
  access, so it is separable and independently cuttable.
