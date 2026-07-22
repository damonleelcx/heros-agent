# ADR-005 — Forge delivery: the customer's CI opens the PR by default; a hosted Git App is the opt-in upgrade

- **Status:** Accepted (2026-07-22)
- **Deciders:** System Design + Product (proposed) + User (ratified)
- **Resolves:** the two blockers left open in
  [`openspec/changes/p2-config-runtime/tasks.md`](../../openspec/changes/p2-config-runtime/tasks.md) §3.10
  ("Two blockers require sign-off"), which parked forge delivery at **Option C — defer, seam only**.
- **Relates to:** [ADR-001](ADR-001-source-transformation-apply-model.md) — the pull request **is** the
  delivery mechanism; this ADR decides how it reaches the customer's repository, and changes nothing
  about what the transformation produces. [ADR-002](ADR-002-provider-gateway-serves-platform-callers.md)
  — this ADR **extends its reasoning** into a second dimension rather than overturning it.
- **Owns:** phase **P12 — Forge Delivery** ([PRD](../prd/P12-forge-delivery.md)), and the delivery half
  of the loop **P11 — CLI & CI Integration** ([PRD](../prd/P11-cli-ci-integration.md)) carries.

## Context — what problem this solves

ADR-001 made the pull request the product's entire output: *"the optimization ships as a pull request;
git is the audit trail and `git revert` is rollback."* P5.5's automation levels are specified almost
entirely in PR verbs — Advisory opens a draft PR, Assisted opens a verified one, the human merges. P6's
Autonomous level is *auto-merge*. And P7 makes it commercial:

> `p7/specs/metering/spec.md` — *"Billable savings SHALL be computed only from **merged-PR** deltas in
> the P5.5 verified-delta ledger."*

So the platform's primary output channel, three phases' automation model, and **one of its two revenue
models** all rest on opening pull requests. There is no code that opens one. The P2 exit analysis
(§3.10) inventoried the gap honestly — no push path (`worktree.NewPool` clones `--bare --local`, so the
variant branch exists only in a local mirror), no forge credential kind (`Secrets` is provider-scoped),
and nowhere to record the result — and then stopped, because two of the gaps are one-way doors:

1. **Pushing to the customer's repository needs a write-scoped forge token** — described there as *"the
   highest-blast-radius action in the system,"* and noted as contradicting the reasoning ADR-002 spent
   its entire argument on.
2. **Recording the PR URL is structurally blocked** — `transform` is immutable by DB trigger
   (`0004:transform_immutable`), and the PR is opened *after* insert because the build must pass first,
   so the `UPDATE` is rejected **by design**.

The three options it listed were **A** (push to the customer's real remote), **B** (PR to a fork we
own), **C** (defer). It recommended C, "then B or A behind a new ADR." This is that ADR — and it
selects a fourth option that was not on the list.

## Decision

**Forge delivery has two modes, producing identical pull requests. The default is CI-mediated, in which
the customer's own CI opens the PR using the ephemeral forge token it already holds. A hosted Git App
installation is the opt-in upgrade for customers who cannot or will not run it in CI.**

### Mode 1 — CI-mediated (default)

The customer's CI job authenticates to the platform, fetches the verified proposal for that repository
and revision, applies the diff on a branch, and opens the pull request **using the forge credential the
CI environment already provides** — `GITHUB_TOKEN` and its equivalents. That credential is scoped to
the one repository, short-lived, issued and rotated by the forge, and never leaves the customer's CI
runner.

**The platform never receives, stores, or requests a forge credential in this mode.** It hands out a
diff and evidence; the customer's own infrastructure does the writing.

### Mode 2 — Hosted Git App (opt-in)

For customers without CI, or who prefer not to run it there, a Git App installation lets the platform
open the PR directly. This **is** standing write access and the ADR does not pretend otherwise. It is
contained rather than denied:

- **Per-repository installation, never org-wide by default.** The customer selects the repositories.
- **Least-privilege permission set**, documented, minimal, and no broader than opening and updating
  pull requests on the selected repositories.
- **Customer-revocable from their side at any time**, without contacting us.
- **The installation token never leaves the platform and is never logged**, in any field, on any path.
- **Every delivery is recorded** (see below), so the write is never invisible.
- **Never merges** below the Autonomous automation level, and under Autonomous only a gate-passed,
  verified change.

### Delivery is recorded in its own append-only record

A `delivery` record — keyed by `(config_hash, source_revision, forge_ref)` — records what was delivered
where, in which mode, and its current state. `transform` is **not** touched.

## Why this design — the arbitration

Applying [八级法则](../../../aikeylabs-skills/shared/00-核心法则.md) (安全 > 稳定 > UX > 运维 >
不可演进 > 不可扩展 > 维护 > 实现). Per **L2**, the comparison is settled at the highest level where the
options differ, and we do not fall back to a lower-level convenience.

### ADR-002 refused one dimension of customer-side reach; this is the second

ADR-002's argument was that our component must not become part of the customer's **runtime** — because
either it merges and their production traffic flows through us (L1/L2), or it is stripped before merge
and we measured code that does not ship (L2). A platform-held forge token is the same class of reach in
a different dimension: not their runtime, but their **repository**.

| | ADR-002 (runtime) | ADR-005 (repository) |
|---|---|---|
| The reach refused | Our gateway in their production request path | A standing write credential to their source of record |
| Blast radius if compromised | Their live traffic | **Their source code, across every customer at once** — a supply-chain event |
| Chosen posture | Platform callers only; their program calls its own SDKs | CI-mediated by default; their CI writes with its own credential |

The consistency matters. Having argued that we must not hold their production traffic, holding a
permanent write key to their source would be the larger version of the same mistake.

### Why CI-mediated is the default rather than merely an option

The decisive fact is that **the credential already exists on the customer's side, correctly scoped.**
Every CI system issues a repo-scoped, short-lived, automatically-rotated token to its own jobs. Using
it is not a workaround; it is the credential the forge designed for exactly this purpose.

- **L1 安全** — the highest-blast-radius credential in the system is never created. There is no
  aggregated write-key store to compromise, no token rotation programme, and no answer needed to
  "what happens if you're breached?" beyond *we never had write access*.
- **L2 稳定** — delivery does not depend on our uptime at the moment of writing. The CI job holds the
  diff; if we are down it retries on its own schedule.
- **L3 UX** — the customer gets the real Dependabot experience: a PR on *their* repository, from *their*
  automation, reviewable normally. Option B's bot-fork PR is a visibly second-class artifact.
- **L4 运维** — no forge-credential lifecycle to operate: no issuance, storage, rotation, revocation,
  scope audit, or breach playbook for a class of secret we do not hold.
- **L5 可演进** — the platform's side of the contract is "hand out a verified diff plus evidence,"
  which is forge-agnostic. GitHub, GitLab and Bitbucket differ in their CI syntax, not in our API.
- **L8 实现** — also cheaper, but per **L3** that is not why. The decision is made at L1 and holds
  regardless of cost.

The hosted App is offered because a Team+ surface that *requires* CI would exclude every customer
without it — a real commercial cost. It is opt-in, per-repo and revocable precisely because its risk is
genuine, and customers who choose it are choosing it knowingly.

### Alternatives considered

| Option | Verdict |
|---|---|
| **A. Push + PR to the customer's real remote with a platform-held token** (as the only mode) | ❌ Rejected as the default on L1. This is the credential §3.10 called the highest-blast-radius action in the system, and a compromise is a supply-chain event across the entire customer base. **Retained as opt-in Mode 2**, contained per-repo and revocable, because refusing it outright costs customers who have no CI. |
| **B. PR to a fork or mirror we own** | ❌ Rejected on L3 and L5. A bot-fork PR is a weaker review artifact — it is not on the customer's repo, it does not run their branch protections or required checks, and it does not merge the way their team merges. Fork drift is a permanent maintenance tax, and gainshare depends on detecting a **merge into their default branch**, which a fork cannot observe. |
| **C. Defer; seam only** *(the current state)* | ❌ No longer viable. It was the right call at P2 exit, but gainshare revenue is entirely blocked on merged-PR deltas, and P5.5/P6 are already specified in PR verbs. Deferring further means shipping automation levels whose delivery mechanism does not exist. |
| **D. CI-mediated with the customer's own ephemeral token** (chosen default) | ✅ The credential is never created on our side; delivery is forge-native; the artifact is a first-class PR on the customer's repository; and it ties the CLI/CI surface to the delivery surface, which is what makes the commercial loop close. |

### Why the P2 analysis did not reach D

Not an oversight worth glossing over: §3.10 framed the question as *"how do **we** get write access,"*
and every option followed from that framing. Re-framing it as *"who should hold the credential"* makes
the answer available — the party that already holds it, correctly scoped, for exactly this purpose. The
lesson is worth keeping: three of the options were about **containing** a credential we had assumed we
needed.

## Blocker #2 — recording the delivery

Recording a PR URL is blocked because `transform` is immutable by trigger and the PR is opened after
insert. Per 🔴 `careful-table-creation`, creating a table is a one-way door requiring ≥2 alternatives:

| Option | Assessment |
|---|---|
| **(i) A separate append-only `delivery` record** keyed by `(config_hash, source_revision, forge_ref)` — **chosen** | Leaves `transform` immutability untouched. And it is the *correct* model rather than a workaround: a transform is produced **once** and is immutable by nature, whereas a delivery has a **lifecycle** — opened, updated, superseded, closed, merged — and the same transform can legitimately be delivered to more than one ref. Forcing a lifecycle-bearing fact into an immutable row was always the wrong shape. |
| (ii) Relax `transform` immutability and add a PR-URL column | ❌ Rejected. Breaks `TestPG_Immutability_*` and a stated invariant, in order to store a field that is not part of the transform's identity — and immutability there is what makes `config_hash` reproducibility checkable. |
| (iii) Derive delivery state from the P2.5 event stream | Runner-up: no new table, and the events exist. Rejected because a **business** fact — did the customer merge, and is this therefore billable — would depend on **observability** retention and sampling policy. A gainshare invoice must not become unanswerable because a metrics retention window expired. |

The record is **append-only**: state changes append a new row rather than mutating, so the delivery
history of a proposal is reconstructable and a merge that later gets reverted is visible as a sequence
rather than as a silently overwritten field.

## What this does not change

- **ADR-001** — the transformation is unchanged: AST-level, deterministic, build-preserving,
  behavior-preserving-except-intended, worktree-isolated, reviewable, revertible by one `git revert`.
  This ADR decides only how the resulting diff reaches the repository.
- **ADR-002** — the transformed program still calls its own SDKs; we still hold no place in the
  customer's runtime. This ADR extends that posture to their repository.
- **The platform never merges** below the Autonomous automation level (Enterprise, per P7
  entitlements), and under Autonomous only a gate-passed, verified change.
- **Nothing unverified is delivered.** P5.5's gate is upstream of delivery, not replaced by it.

## Consequences

**Positive**
- The highest-blast-radius credential in the system is **not created** in the default posture — which
  is also the strongest possible answer in a customer's security review.
- Gainshare revenue becomes computable, because merges into the customer's default branch are
  observable.
- The delivery model is forge-agnostic on our side; per-forge work lives in CI recipes, which are
  small, inspectable, and can be contributed.
- The delivery record models a lifecycle honestly instead of overloading an immutable row.

**Negative / new risk surface**
- **Two delivery modes to specify, build, and support**, with a shared content contract that must not
  drift between them. Mitigated by requiring both modes to produce **identical PR content**.
- **The default depends on the customer's CI being configured.** A customer with verified proposals and
  no delivery route configured must be told so plainly — proposals silently accumulating with nowhere
  to go is a failure that looks like the product having no suggestions. P12 makes that a reported state.
- **Mode 2 is real standing write access** for the customers who choose it. Contained, revocable,
  per-repo, and recorded — but not eliminated, and it should be described that way when it is sold.

## Terminology

| Term | Meaning |
|---|---|
| **CI-mediated delivery** | The customer's CI opens the PR with its own ephemeral, repo-scoped forge token. The default. |
| **Hosted Git App delivery** | The platform opens the PR through a per-repo, least-privilege, customer-revocable App installation. Opt-in. |
| **Delivery record** | The append-only record of what was delivered where, in which mode, and its lifecycle state. |
| **Delivery route** | A repository's configured means of receiving PRs. A repository with none has no route, which is a reported state, not silence. |
