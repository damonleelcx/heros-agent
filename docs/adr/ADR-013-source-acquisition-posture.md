# ADR-013 — Source acquisition: the bundle stays the default; a per-repository read grant is the opt-in upgrade

- **Status:** Proposed (2026-08-18) — ruling R1 of the
  [GEHA program](../prd/P31-P38-graph-engineering-agent-program.md) is taken; this ADR records what it costs
- **Deciders:** System Design + Product (proposed) + User (ratified R1)
- **Resolves:** the implementation `internal/sourceingest` names and declines to build —
  [`source.go:32-34`](../../internal/sourceingest/source.go): *"A GitSource that clones from a registered
  remote can implement it later."*
- **Relates to:** [ADR-005](ADR-005-forge-delivery-and-credential-posture.md) — the same argument, in the
  opposite direction. ADR-005 kept a **write** credential out of the platform; this ADR admits a **read**
  one, under conditions, and must explain why that is not the first half of undoing ADR-005.
  [ADR-002](ADR-002-provider-gateway-serves-platform-callers.md) — the reach argument this extends.
- **Owns:** phase **P32 — Repo Intake** ([PRD](../prd/P32-repo-intake.md))

## Context — what problem this solves

The product's first touch is supposed to be a sentence: *point it at a repository and ask it something*.
What the platform can actually accept today is a **gzipped tar the customer uploads for one revision**
([`bundle.go`](../../internal/sourceingest/bundle.go)), reached through a CLI they must first install.

`sourceingest` did not arrive at the bundle by accident, and its reasoning is the thing this ADR has to
answer rather than skip past. Verbatim, from [`bundle.go:17-27`](../../internal/sourceingest/bundle.go):

> *Why a pushed bundle rather than a clone from their remote. Both were on the table. The bundle wins the
> first round for one reason that is not about effort: a clone needs a credential that can read the
> customer's repository, held by the platform, usable at any time, for any revision, without the customer
> present. **That is a standing capability. A bundle is an act** — the customer ran a command, for one
> revision, and can stop.*

It then names its own limit honestly: *"This is not an argument that clones are wrong; it is an argument
about which one to build first when only one of them can be reviewed carefully today."*

This ADR is that careful review. It is being written now because the GEHA program's entire premise is a
person who has installed nothing, and asking that person to install a CLI before the conversation can
start does not degrade the feature — it deletes it.

### What the surrounding posture already concedes

Two things make this a smaller step than it looks, and one thing makes it a real one.

**Smaller, 1:** the platform already holds source. The bundle is source. `sourceingest`'s package
comment says so at the top and refuses to disguise it: *"That is a real widening of what the platform
HOLDS, and it is not disguised as a wiring change. A customer who pushes source has handed over their
source."* This ADR is not the first to cross that line; it changes **who initiates** and **for how long**.

**Smaller, 2:** the platform already accepts a forge credential in one mode. ADR-005's Mode 2 hosted Git
App is a **write**-scoped installation, opt-in and per-repository. A read grant is strictly weaker than a
credential the same customer may already have installed.

**Real:** every previous grant expires with an act. A bundle covers one revision. A CI token lives for
one job. A read grant covers *every revision, at any time, without the customer present* — and it is the
first credential in this system whose whole purpose is to be used when nobody is watching.

## Decision

**Source acquisition has three modes behind one interface. The default is the pushed bundle. A
per-repository read grant is opt-in, is scoped to one repository, is revocable by the customer from the
console, and is never a precondition for any other feature. A local path is served by the existing local
bridge and never leaves the customer's machine.**

### Mode 1 — Bundle push (default, unchanged)

Exactly what ships today. `BundleSource` extracts a customer-pushed archive for one revision under the
hardened rules in [`bundle.go`](../../internal/sourceingest/bundle.go) — no absolute paths, no `..`
escape, no symlinks or hardlinks, no device nodes, and caps on entry count, per-file size and total
uncompressed bytes. **This ADR changes nothing about it**, including its retention rule.

### Mode 2 — Per-repository read grant (opt-in)

`GitSource` implements `sourceingest.Source` behind the seam that was left for it. The customer
authorizes the platform to read **one repository**. The grant is:

- **Per-repository.** Not per-organization, not per-account. A grant that covers repositories the
  customer did not name is the standing capability at its worst, and the forges all support the narrow
  form (GitHub App repository selection, GitLab project access token, Bitbucket repository access token).
- **Read-only.** No scope that can write a ref, open a pull request, or change a setting. When the same
  customer also runs ADR-005 Mode 2, that is a **second, separate** installation with its own scope — one
  credential that both reads source and writes branches is the thing neither ADR wants.
- **Revocable from the console, and revocation is a delete.** Revoking removes the stored grant and every
  cached tree derived from it. A revocation that leaves the platform able to answer from cache is not a
  revocation.
- **Recorded per use.** Every clone writes an append-only record of `(tenant, repository, revision,
  actor, reason)`. "Usable without the customer present" is acceptable only if the customer can afterwards
  read exactly when it was used and for what.
- **Never a precondition.** No feature is gated on Mode 2. A customer who declines it reaches every
  surface through Mode 1, and where a surface genuinely needs a revision that was never pushed, it says
  `not reported` — the P29 vocabulary — rather than prompting for a grant.

### Mode 3 — Local path (no transfer)

A local repository is read **on the customer's machine** by the existing bridge
(`cmd/heroslocallink`, `internal/clilink`) and never uploaded. The console's "select a local repo"
affordance is a pairing flow with a local agent, not a file picker that ships a tree.

> ⚠️ **Mode 3 is the one with a known product gap, and it is not this ADR's to close.**
> [`cmd/heroslocallink/main.go`](../../cmd/heroslocallink/main.go) states it: `heros link` is pinned to
> `https://heros-agent.space` and *"nothing overrides it: not a flag, not an environment variable, not a
> config key"*, so a self-hosted deployment cannot be linked to by the shipped CLI. That pin is a
> boundary decision. P32 must either name a self-hosted endpoint (a separate ADR) or ship Mode 3 for the
> hosted service only, and say which.

## Why this design — the arbitration

Under the eight-level rule (`安全 > 稳定 > UX > 运维 > 不可演进 > 不可扩展 > 维护 > 实现`) this is decided
at **level 1 against level 3**, which is the hardest pairing in the ladder because level 1 wins by
construction and the temptation is to pretend the conflict is elsewhere.

It is not resolved by declaring the UX loss acceptable. It is resolved by observing that **the security
cost is not a property of cloning — it is a property of the grant's scope and lifetime.** A grant that is
one repository, read-only, revocable, and logged per use is a materially different object from "a
credential that can read the customer's repository." `sourceingest`'s argument was written against the
broad form, and it is correct against the broad form. Narrowing the grant is what makes level 3 reachable
without trading level 1 away, and L1 of the rule — *a higher level's degradation may not be bought with a
lower level's convenience* — is satisfied because nothing was bought: the scope is smaller, not the
scrutiny.

The default stays Mode 1 for a reason that is about level 2, not level 1. **The bundle cannot fail in a
way the customer does not see.** A clone can — a rotated token, a renamed default branch, a repository
made private — and each of those failures happens while nobody is present, which is exactly when a
degraded answer is most likely to be believed.

### Alternatives considered

| Option | Why not |
|---|---|
| **A — clone only, no bundle** | Deletes the mode that needs no standing credential. A customer whose policy forbids third-party repository grants loses the product entirely, and that customer is the one this codebase's posture was written for. |
| **B — org-wide grant** | One authorization, best UX, and the exact shape `sourceingest` refused. An org grant reads repositories nobody named, including ones created after the grant. |
| **C — bundle only (status quo)** | Preserves level 1 perfectly and makes the GEHA program's first touch impossible. Chosen against by ruling R1. |
| **D — shallow ephemeral clone with a customer-supplied token per run** | Attractive: no stored credential. But the customer must be present for every run, which forbids scheduled and autonomous operation — P6's entire automation ladder. It is Mode 1 with extra steps. |

## What this does not change

- **The wire contract in `internal/runlink` is untouched, byte for byte.** Egress stays an allowlist,
  constructed. Metrics and IR structure cross the boundary; prompt text, source, diffs, environment
  values and credentials do not, on any path including diagnostics.
- **The CLI stays offline-first and free on every plan.** Mode 2 is a hosted-console affordance; it is not
  a dependency of `discover`, `apply` or `eval`.
- **ADR-005's default is not touched by this ADR.** Ruling R3 changes the *console's* delivery default and
  is recorded in P35; a read grant neither implies nor enables a write one.
- **Retention.** Mode 2 inherits Mode 1's retention rule for extracted trees. A clone is not a licence to
  keep a tree longer than a push would have been.

## Consequences

**Accepted:**
- The platform can be asked to read a customer's repository while they are asleep. This is new, it is the
  point, and it is why the per-use record is a requirement and not a nicety.
- A second credential kind enters the secret store, with its own rotation and revocation lifecycle.
  `internal/broker`'s `Secrets` source is provider-scoped today and must grow a forge-read kind — the same
  gap ADR-005 §Blocker noted for the write kind.
- A revoked grant must cascade to derived trees. Whoever implements the delete path owns a correctness
  requirement that is easy to satisfy incompletely and impossible to detect afterwards.

**Rejected consequence — worth stating so it is not assumed:**
- This does **not** put the platform on a path to holding organization-wide forge access. Option B is
  refused here on the record, so a later phase proposing it is proposing a change to this ADR rather than
  an extension of it.

### 🔴 Option B — the refusal, and where it is enforced (P32 §1.5)

The refusal is not a default that a configuration key can move. It is enforced in three places, and all
three have to be edited by anyone who wants to change it — which is the point:

| Where | What it does | Fence |
|---|---|---|
| `sourceingest.Connection` | Has **no field** that can express a scope wider than one repository. Not "defaults to one" — there is nothing to widen. | `TestConnectionHasNoFieldThatCanExpressWriteOrBreadth` reads the struct by reflection, so a field added later is caught by the fence and not by review |
| `ConnectionStore.Create` | Refuses at connect any authorization whose **resulting grant** would cover a repository the customer did not name, per forge | `TestConnectRefusesAGrantBroaderThanOneRepository` — one case per forge |
| This ADR | Records the refusal so a later phase has an address to amend | — |

**A later phase that wants organization-wide scope must amend this ADR.** It cannot arrive as a
`scope: "org"` string, because there is no field it could arrive in.

## Terminology

| Term | Meaning here |
|---|---|
| **grant** | A customer authorization for the platform to read one repository. Not a token the customer pastes; an installation or access token the forge issues and the customer can revoke on their side too. |
| **act** vs **standing capability** | `sourceingest`'s distinction, kept: an act is bounded by what the customer did, a standing capability by what a credential's scope allows. |
| **source snapshot** | A tree at one `(tenant, workflow, revision)` — `sourceingest.Ref`. Both modes produce one; nothing downstream can tell which mode produced it, and that is deliberate. |
