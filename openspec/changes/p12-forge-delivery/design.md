# Design — P12: Forge Delivery

Product rationale: [`../../../docs/prd/P12-forge-delivery.md`](../../../docs/prd/P12-forge-delivery.md).
Architecture decision: [`../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md`](../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md).
CI counterpart: [`../p11-cli-ci-integration/`](../p11-cli-ci-integration/).

## Context

Everything upstream of delivery works. P2 produces a build-verified transform, P4 measures it, P5.5
proves it on held-out data. The chain then stops at a step that does not exist, and the missing step is
the one that reaches outside our boundary into another company's source of record — which is why it was
deferred rather than improvised.

The P2 exit analysis (§3.10) is worth reading before this document. It inventoried the gaps precisely
and stopped for the right reason: two of them are one-way doors. What it did not do — and this is the
lesson worth keeping — is question its own framing. It asked *"how do **we** get write access to the
customer's repository,"* and all three of its options (push with our token, PR to a fork we own, defer)
follow from that framing. Re-asking it as *"**who** should hold the credential"* makes a fourth option
available, and it is strictly better on the dimension that mattered most.

## Decision 1 — CI-mediated delivery is the default; the platform holds no forge credential

The customer's CI job fetches the verified proposal, applies the diff on a branch, and opens the pull
request **using the forge credential the CI environment already provides**. That credential is scoped
to the one repository, short-lived, and rotated by the forge. The platform never receives it.

**Alternative rejected — a platform-held write token as the only mode.** It is the straightforward
implementation and gives the cleanest product experience with no customer setup. Rejected on **L1
安全**: it means standing write access to every customer repository, forever, and a compromise of the
aggregated key store is a supply-chain event across the entire customer base. ADR-002 spent its whole
argument refusing the same class of reach in the runtime dimension; holding a permanent write key to
their source would be the larger version of that mistake.

**Alternative rejected — a pull request to a fork or mirror we own.** Avoids customer write scope.
Rejected on **L3 UX** and **L5**: a bot-fork PR is a visibly second-class artifact — it does not run
their branch protections or required checks, and it does not merge the way their team merges. Fork
drift is a permanent tax. And decisively, **gainshare depends on observing a merge into their default
branch**, which a fork cannot see, so this option would break the revenue model it was meant to enable.

**Why this is a default rather than one option among equals:** the credential already exists on the
customer's side, correctly scoped, issued by the forge for exactly this purpose. Using it is not a
workaround. It also collapses an entire operational surface — no issuance, storage, rotation,
revocation, scope audit, or breach playbook for a class of secret we do not hold (**L4**).

## Decision 2 — The hosted Git App is retained, contained, and described honestly

Customers who cannot or will not run delivery in CI can install a Git App: per-repository, never
org-wide by default, least-privilege, customer-revocable without contacting us, token never logged and
never leaving the platform, and every delivery recorded.

**Alternative rejected — refuse platform-held credentials entirely.** The purest security posture, and
the easiest thing to say in a review. Rejected on **L3 UX and commercial reach**: a Team+ surface that
*requires* CI excludes every customer without it, and that is a real cost paid for a purity we can
otherwise contain.

The containment is the point, and so is the honesty. This mode **is** standing write access. It should
be sold as such, with its scoping and revocability as mitigations — not presented as equivalent to the
default. A customer who chooses it should be choosing it knowingly.

## Decision 3 — Identical pull-request content across modes

Both modes render the same PR for the same proposal, verified by comparison rather than by review.

**Alternative rejected — let each mode render what is convenient.** Simpler per-mode implementation.
Rejected on **L5/L7**: two renderings drift, and the drift surfaces to customers as *"why does the PR
look different in CI?"* The PR's content is the thing that earns the merge, so it is one contract with
two credential paths behind it.

## Decision 4 — An append-only `delivery` record; `transform` stays immutable

Every delivery and state change appends to a `delivery` record keyed by
`(config_hash, source_revision, forge_ref)`. `transform` is untouched.

**Alternative rejected — relax `transform` immutability and add a PR-URL column.** The obvious fix for
the blocker. Rejected on **L2/L5**: immutability there is what makes `config_hash` reproducibility
checkable, and `TestPG_Immutability_*` asserts it — trading an invariant for a field that is not part
of the transform's identity is a bad exchange.

**Alternative rejected — derive delivery state from the P2.5 event stream.** No new table, and the
events would exist. Rejected because a **business** fact — did the customer merge, and is this
therefore billable — would depend on **observability** retention and sampling policy. A gainshare
invoice must not become unanswerable because a metrics retention window expired.

**It is also simply the right model.** A transform is produced **once** and is immutable by nature. A
delivery has a **lifecycle** — opened, updated, superseded, closed, merged, possibly reverted — and the
same transform may legitimately be delivered to more than one target. Forcing a lifecycle-bearing fact
into an immutable row was the wrong shape before the blocker existed. Append-only rather than mutable
state means a merge later reverted reads as a **sequence** rather than as a field someone overwrote,
which is what makes a disputed gainshare invoice answerable.

## Decision 5 — Idempotency keyed by `(config_hash, source_revision, target)`

Re-delivery updates the existing pull request. This must hold under retries, restarts, and
**concurrent** attempts.

**Alternative rejected — open a pull request per delivery attempt.** Trivially simple. Rejected on
**L2/L3**: a duplicate-PR storm is the fastest route to losing repository access permanently, and the
recovery is a conversation with an annoyed platform team rather than a patch. The concurrency case is
the one that actually produces duplicates in practice, which is why a sequential-only test is
insufficient.

## Decision 6 — No delivery route is a reported state

A repository with verified proposals and no configured route surfaces that condition.

**Alternative rejected — let proposals accumulate until someone configures delivery.** No work.
Rejected on **L3**: silence is indistinguishable from *"the product found nothing to improve,"* and the
confusion lands hardest during evaluation — exactly when the customer is deciding whether it works.
The same reasoning covers revocation: an App uninstalled or a CI credential rotated must not read as a
quiet week.

## Decision 7 — Halt reads fail closed

If the kill-switch state cannot be read, delivery does not happen.

**Alternative rejected — deliver when the halt state is unreadable.** Keeps delivery flowing through a
partial outage. Rejected on **L1/L2**: the kill switch exists for the case where delivery is causing
harm across tenants. Failing open in exactly the circumstance it was built for defeats its purpose, and
the cost of the safe choice is a delayed pull request.

## Decision 8 — The platform writes only pull requests and their branches

No direct pushes to protected branches, no tags, no releases, no issues.

**Alternative rejected — also write tags/releases/issues where useful.** Each has a plausible use.
Rejected on **L1**: every additional write scope widens the permission set the hosted App must request,
and the narrowest credible ask is what gets an installation approved. Scope is easy to add later and
impossible to take back from an already-approved installation.

## Interfaces sketch

```
Deliver(proposal, route) → delivery_id
  precondition: proposal passed the P5.5 gate            // FR5 — never a way around it
  precondition: entitlement(tenant) ≥ Team               // server-side
  precondition: halt_state readable AND not armed        // fail closed (D7)
  idempotency key: (config_hash, source_revision, target)

route = { mode: ci | app, target }                       // absent → reported state (D6)

PR content (identical across modes, D3):
  diff · verified delta + CI · held-out status · eval evidence · config_hash lineage
  · console evidence reference

delivery (append-only, D4):
  { delivery_id, config_hash, source_revision, forge_ref, mode, state, pr_ref, actor, reason?, at }
  state ∈ opened | updated | superseded | closed | merged | reverted
```

## Risks

| Risk | Mitigation |
|---|---|
| A platform-held write credential is compromised | Decision 1 — the default mode never creates one; there is no store to compromise. Decision 2 contains the opt-in mode per-repo, least-privilege, revocable. |
| A duplicate-PR storm loses repository access | Decision 5, tested under **concurrency and retry** — the sequential case is not the one that produces duplicates. |
| An unverified change is delivered | Delivery is downstream of the P5.5 gate and asserted undeliverable from every entry point. A delivery path that could bypass the gate would dissolve "verification decides." |
| The kill switch halts the loop but not its output | Decision 7 — halt scopes to delivery, and an unreadable halt state fails closed. Tested by making it unreadable and asserting delivery does **not** occur. |
| Proposals accumulate invisibly against a repo with no route | Decision 6 — a reported state rendered as a condition with a next action, not an empty list. |
| The two modes' pull requests drift | Decision 3 — parity verified by byte-comparison, not review. |
| Gainshare billed on a merge that did not happen | A merge is recorded from an **observation** of a merge into the target branch; a closed-without-merge PR is not inferred to be one. A later revert appears as a further state. |
| `transform` immutability gets relaxed under delivery pressure | Decision 4 — forbidden, with the alternatives and their costs recorded in ADR-005. |
| The App's permission set grows quietly | The permission set is a spec item; broadening it is a spec change, not a configuration choice. |
| One customer's forge outage stalls the queue for everyone | Per-repository failure isolation; a failed delivery does not lose the proposal. |
