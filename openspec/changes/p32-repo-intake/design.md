# Design — P32: Repo Intake

## Context

`sourceingest` designed for this phase and then declined to build it, which means the shape is already
decided and the open work is the parts the original note deferred: what a grant may cover, what happens
when it is revoked, what a cloned tree is trusted to contain, and what the platform keeps afterwards.

The reasoning for admitting a clone at all is [ADR-013](../../../docs/adr/ADR-013-source-acquisition-posture.md).
This document is the mechanism.

## D1 — The mode is invisible downstream and visible upstream

**Decision.** Every consumer receives a source snapshot and cannot tell how it was obtained. The customer
can always tell, per workflow.

**Why.** `sourceingest` already argues the first half: the choice between implementations *"stays one
constructor call in `internal/launch` rather than a branch threaded through the pipeline."* A branch
would multiply every downstream code path by three and put mode-awareness in components that have no
business having an opinion about it.

The second half is new and is not symmetric with the first. The three modes differ in exactly one
property — **what the platform can do when the customer is absent** — and that property is the customer's,
not the pipeline's. Hiding it from them to match the pipeline's indifference would be hiding the only
thing they were asked to decide.

## D2 — A cloned tree gets the bundle's hostile-input defences, not fewer

**Decision.** The clone path passes through the same traversal and extraction refusals `BundleSource`
enforces: no absolute paths, no `..` escape, no symlinks or hardlinks, no device nodes, and the entry,
per-file and total-bytes ceilings.

**Why.** The defences exist because a *tree* can be malicious, not because an *upload* can. Git delivers
whatever the repository contains, and a repository can contain a symlink to `/etc/shadow` as easily as a
tar can. The reasoning that would weaken this — "it came from GitHub, so it is a real repository" —
confuses the transport's trustworthiness with the payload's.

**Consequence.** These defences currently live inside `bundle.go`, coupled to tar extraction. They have to
be factored out to a tree-validation step both implementations run, and that refactor needs the existing
`bundle_test.go` cases as its fence — they construct the malicious archives by hand and assert each
refusal, which is exactly the characterization suite a refactor of this kind requires.

## D3 — Revocation cascades to derived trees

**Decision.** Revoking a connection deletes the grant and every snapshot derived from it, and a subsequent
read returns `ErrNoSource`.

**Why.** The alternative gets built by accident. Deleting a grant row is one line; enumerating the
artifacts derived from it is not, and nothing fails when the second half is missing — the system keeps
answering, correctly, from data it is no longer authorized to hold. That failure is invisible from the
inside and indefensible from the outside.

**Rejected.** *Mark the grant revoked and let retention expire the trees.* Revocation then means "stops
being refreshed", and the customer's word for it means "stops being readable". A customer revoking access
after an incident is not asking for a slower cache.

## D4 — Four causes, four messages, and no fallback

**Decision.** A clone failure reports exactly one of: credential rejected, repository not found, revision
not found, network failure. A failure never serves an older snapshot.

**Why (the causes).** P9's rule that failure classes stay distinguishable, applied to intake. A rotated
token and a renamed default branch are different people's problems on different days, and "could not
connect to the repository" sends both of them to the same wrong place.

**Why (no fallback).** A clone fails most often while nobody is watching, which is when a plausible answer
is most likely to be believed. Serving yesterday's tree under today's question produces a finding about
source that no longer exists, and nothing about the finding says so.

## D5 — The local mode is a pairing flow, not an upload

**Decision.** The console pairs with a local agent that reads the repository in place. There is no
browser file picker.

**Why.** A picker that reads a folder and posts it is Mode 1 wearing Mode 3's clothes, and the customer
would reasonably believe nothing left their machine — the affordance says "select a local repo". A UI that
produces a materially different data-handling outcome than its label implies is a consent failure, not a
shortcut.

**Carried gap.** `heroslocallink` documents why this cannot yet work against a self-hosted deployment:
`heros link` is pinned to one host, *"enforced twice"*, and *"nothing overrides it: not a flag, not an
environment variable, not a config key."* Naming a self-hosted endpoint is a boundary decision. PRD §14
Q1 forces the answer rather than letting the flow fail at its last step.

## D6 — Shallow, at the requested revision

**Decision.** Clone shallowly at the revision. No full history.

**Why.** History is not an input to discovery, so cloning it spends time and disk to widen what the
platform holds for no consumer. If a later phase needs history it can say why and change this, which is a
smaller decision than un-holding it afterwards.

## Interface sketch

```go
// unchanged
type Source interface {
    Tree(ctx context.Context, ref Ref) (dir string, err error)   // ErrNoSource is a state, not a failure
}

// new, behind the same interface
type GitSource struct {
    conns   ConnectionStore   // per-repository grants, tenant-scoped
    secrets broker.Secrets    // forge-read kind — does not exist today
    guard   TreeGuard         // D2: factored out of bundle.go, run by BOTH implementations
    ret     RetentionPolicy   // D6/FR16: same window as a pushed bundle
}

type Connection struct {
    TenantID     string
    Forge        string   // github | gitlab | bitbucket
    Repository   string   // exactly one
    GrantKind    string   // app_installation | access_token  (PRD §14 Q2)
    // no scope field that can express write
}

type CloneRecord struct {   // append-only, customer-readable
    TenantID, Repository, Revision string
    Actor  string   // person id, or the scheduled/autonomous process
    Reason string   // the run or conversation that caused it
    At     int64
}
```

## Risks this design accepts

- **The `TreeGuard` refactor touches shipped, security-load-bearing code.** Mitigated by treating
  `bundle_test.go` as a characterization suite: it must pass unchanged before and after, and its cases
  must also run against the clone path.
- **A new credential kind is a new rotation and revocation lifecycle**, and `Secrets` is provider-scoped
  today. ADR-005 noted the same gap for the write kind and it was never closed; doing it for read means
  doing it properly enough that write can reuse it.
- **Per-forge behaviour will differ** — repository-scoped grants are expressed differently on each forge,
  and one of them will make the narrow form awkward. §9.5's per-forge metrics exist so that shows up as a
  number rather than as an anecdote.
- **Retention has no written window today.** PRD §14 Q4 says so plainly: FR16 defers to "the bundle's
  rule", and I could not find that rule stated as a number anywhere in the tree. An unwritten rule is not
  a rule, and this design does not pretend otherwise.
