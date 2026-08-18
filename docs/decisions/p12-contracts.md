# P12 Contracts — PR-body format, the `delivery` key, merge observation, route keying, branch naming, forge

This document is the **referenceable contract** for P12 §1 (tasks 1.1–1.6) and is folded in by §11.2. It
is the same kind of artifact as [`p11-contracts.md`](p11-contracts.md): each contract below has exactly
one machine source of truth in Go or SQL, and this file explains and renders it. The moment a customer
builds automation on a pull-request body, or a security reviewer reads the credential posture, or P7's
gainshare join reads the `delivery` key, these are **public contracts** — decided here, versioned, and
changed in one place or not at all.

Architecture decision: [ADR-005](../adr/ADR-005-forge-delivery-and-credential-posture.md). Product
rationale: [PRD P12](../prd/P12-forge-delivery.md). Design reasoning:
[`../../openspec/changes/archive/2026-07-25-p12-forge-delivery/design.md`](../../openspec/changes/archive/2026-07-25-p12-forge-delivery/design.md).

| Contract | Machine source of truth (single) | Consumed by |
|---|---|---|
| PR-body format + version (§1) | [`internal/forgedelivery/prbody.go`](../../internal/forgedelivery/prbody.go) `PRBodyContractVersion`, `RenderPRBody` | every delivered PR, a customer's PR-parsing automation, byte-parity test |
| `delivery` schema + key (§2) | [`db/migrations/postgres/0015_p12_delivery.up.sql`](../../db/migrations/postgres/0015_p12_delivery.up.sql) | `internal/deliveryrecord`, P7 gainshare join |
| Merge-observation mechanism (§3) | [`internal/forgedelivery/observe.go`](../../internal/forgedelivery/observe.go) `MergeObserver` | gainshare timeliness, delivery record `merged` state |
| `delivery_route` keying (§4) | [`internal/forgedelivery/route.go`](../../internal/forgedelivery/route.go) `Route`, `Target` | `Deliver`, idempotency key |
| Branch naming + stale policy (§5) | [`internal/forgedelivery/branch.go`](../../internal/forgedelivery/branch.go) `BranchName`, `StaleBranchPolicy` | the forge writer, idempotency |
| First-class forge (§6) | [`internal/forgedelivery/forge.go`](../../internal/forgedelivery/forge.go) `ForgeKind`, `ForgeGitHub` | forge client, CI recipe |

---

## 1. The pull-request body format (task 1.1)

**Decision: the PR body carries an explicit version marker, exactly as P11's CLI output does.** The
moment a customer's automation greps the body for the verified delta, the layout is a de-facto contract
(PRD §14 Q3); a body that changed shape silently would break that automation with no signal. So the
first line of every delivered body is a machine-readable marker, and the version is bumped whenever the
layout changes in a way a parser could notice.

```
<!-- heros-delivery: pr-body/v1 -->
```

`PRBodyContractVersion = "pr-body/v1"` in Go is the single source of truth; `RenderPRBody` emits it and
the parity test (§9 of the QA gate) byte-compares two renderings of the same proposal. The body's
**sections are fixed and ordered** so a parser can anchor on headings:

1. **Summary** — one line: the proposal title and its automation level.
2. **Verified delta** — the held-out metric delta **with its 95% confidence interval**, rendered
   **as computed**: an interval whose low bound is at or below the baseline reads as a **tie**, never as
   an improvement (task 3.2). This is `verification.Verdict` narrated, never re-derived.
3. **Held-out status** — `held-out` or `not held-out`, verbatim from the verdict.
4. **Eval evidence** — cases fixed / broken counts, cost and latency deltas.
5. **Lineage** — `config_hash` and `source_revision`, the determinism anchors.
6. **Evidence in the console** — a single canonical console reference (§3 of P9's rules) that opens the
   full evidence. It resolves from anywhere it is pasted.

**Both modes produce byte-identical bodies** (ADR-005 Decision 3). The body never contains a credential,
on the success path or the failure path.

## 2. The `delivery` record schema and its key (task 1.2)

**One new table**, `delivery`, decided in ADR-005 (blocker #2) against two alternatives per 🔴
`careful-table-creation` — do **not** add a second. It is **append-only**: every delivery and every
state change is a new row; the transform record is not touched.

- **Lifecycle key (P7's join):** `(config_hash, source_revision, forge_ref)`. This is the join P7's
  gainshare computation depends on — a `merged` row for this triple is the observable input to billable
  savings.
- **Idempotency key (one PR per target):** `(config_hash, source_revision, target)`, materialized as the
  deterministic `delivery_id`. A **partial unique index** `WHERE state = 'opened'` makes a second open
  pull request for one delivery **physically impossible**, which is what makes idempotency hold under
  concurrency rather than under careful code (task 2.3 / 7.1).
- **Mode** (`ci` | `app`) is recorded on every row so an audit can answer which credential path opened a
  given pull request (task 4.3).
- **State** ∈ `opened | updated | superseded | closed | merged | reverted`.

Append-only is enforced by a trigger (`delivery_reject_mutation`), not by convention, mirroring
`billing_event`.

## 3. The merge-observation mechanism (task 1.3)

**Decision: merge is recorded from an explicit observation, primary source CI-reported, with the hosted
App's webhook as the second source when installed.** (PRD §14 Q4.)

A merge is **never inferred from a pull request closing** (task 4.4): a close-without-merge records
`closed`, a merge records `merged`, and the two are distinct observations. The mechanism trade-off:

| Mechanism | Failure mode | Role |
|---|---|---|
| **CI-reported** (default) | a merge without a CI run on the target branch is not observed | primary — consistent with the credential posture; needs no platform-held credential |
| **App webhook** | webhook delivery lost | present only in hosted-App mode; deduped by delivery id |
| Polling | latency + forge rate limits | not implemented; recorded here as the rejected option |

Gainshare timeliness therefore tracks the customer's own CI/merge signal. `MergeObserver.Observe`
appends a `merged` state from an observation carrying the merge commit; a later `revert` appends a
further state and the `merged` row stays (task 4.5).

## 4. `delivery_route` keying (task 1.4)

**Decision: `delivery_route` keys on `(repository, workflow)`, with `workflow` optional.** (PRD §14 Q6.)
A monorepo may host several workflows with different reviewers; keying on repository alone would force
one route — and one reviewer set — on all of them. `Target` renders to the canonical string
`owner/repo` or `owner/repo#workflow`, and that string is the `target` column and the idempotency key's
third component. A route absent for a repository with verified proposals is a **reported state**
(task 6.1), not silence.

## 5. Branch naming and stale-branch policy (task 1.5)

**Decision: a predictable, deterministic branch name; branches are never auto-deleted.** (PRD §14 Q2.)

```
heros/opt/<config_hash[:12]>-<source_revision[:12]>
```

`BranchName(configHash, sourceRevision)` is the single source of truth. Determinism is what makes the
branch itself an idempotency anchor: two concurrent opens target the **same** head branch, and a forge
refuses a second open pull request for one head→base pair, so the branch name backs the DB partial
unique index at the forge layer.

**Stale-branch policy:** the platform **never deletes** a branch. Supersession closes the pull request
(task 2.4) and leaves the branch, because deletion could remove something a customer built on — a
one-way door the spec forbids. Removing an abandoned branch is the customer's action in their own tooling.

## 6b. The hosted Git App — permission set and token custody (tasks 8.1–8.4)

The opt-in hosted App mode carries standing write access. It is **contained**, and the containment is a
contract, not a runtime choice:

- **Per-repository, never org-wide by default (8.1).** `Installation.Repositories` must select at least
  one repository; there is no "all repositories" flag — `Installation` cannot express org-wide, so
  adding it would be a visible spec change. Source: `internal/forgedelivery/hostedapp.go` `Installation`.
- **Least-privilege permission set (8.2).** The documented, minimal grant is exactly:

  | Permission | Level | Why |
  |---|---|---|
  | `pull_requests` | `write` | open and update pull requests |
  | `contents` | `write` | push the platform's own `heros/opt/*` head branch (never a protected branch) |

  Nothing broader. `PermissionSet.WithinLeastPrivilege()` refuses any extra scope (`administration`,
  `actions`, …) or any higher level. **Broadening this set is a spec change**, not a configuration
  choice — the enforcement point is that method. Source: `LeastPrivilegePermissions()`.
- **Token custody (8.3).** The installation token lives in a `SecretsManager` and is accessed **only**
  through `UseToken(id, func(token) …)` — a closure. There is deliberately no `GetToken(id) string`, so
  the token cannot be returned, logged, embedded in a pull-request body, put in a telemetry attribute,
  or written to an artifact. It never leaves the platform.
- **Customer-revocable without contacting us (8.4).** A customer revokes from their own forge settings;
  the platform learns via webhook or a failed token exchange and records it (`InstallationStore.Revoke`).
  Delivery then stops and the state is **reported** — `InstallationStore.Capability` returns
  `revoked`, which the console renders as a condition with a next action.

## 6. The first-class forge (task 1.6)

**Decision: GitHub is the first-class forge, and it matches P11's.** (PRD §14 Q1.) P11's CI integration
already targets GitHub Actions and `GITHUB_TOKEN`; CI-mediated delivery runs inside that hook, so a
different first-class forge would split the surface. `ForgeKind` is an open vocabulary (`github`,
`gitlab`, `bitbucket`) so a second forge is a new recipe, not a core change (L6 扩展性) — but only
`github` is first-class in P12, and the others are declared-not-implemented rather than pretended.
