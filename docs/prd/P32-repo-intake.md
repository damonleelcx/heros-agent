# PRD — P32: Repo Intake

| | |
|---|---|
| **Phase** | P32 |
| **Program** | [Graph Engineering Harness Agent (GEHA)](P31-P38-graph-engineering-agent-program.md) |
| **OpenSpec change** | [`p32-repo-intake`](../../openspec/changes/p32-repo-intake/) |
| **Lead roles** | Backend Dev + DevOps |
| **Support roles** | System Designer, Product Designer, Frontend Dev, QA, AI Engineer, Sales Operations |
| **Upstream** | P1 (discovery takes a checked-out tree) · P11 (CLI, local bridge) · P29 (source-push) · [ADR-013](../adr/ADR-013-source-acquisition-posture.md) |
| **Unblocks** | [P33](P33-surface-assessment.md) — there is nothing to assess without source |
| **Status** | Proposed — awaiting sign-off on §14 |

---

## 1. Summary

`discovery.Run` takes `Options.Repo`: a path to a checked-out tree. Everything the platform knows about a
customer's agentic system begins with somebody handing it one. Today the only ways to do that are to
install the CLI and push a bundle, or to be a developer with a local clone.

P32 adds the two paths a person who has installed nothing expects — **paste a repository URL**, or
**point at a folder on this machine** — and keeps the one that needs no standing credential as the
default. It implements [ADR-013](../adr/ADR-013-source-acquisition-posture.md), which is where the
reasoning lives; this PRD is what gets built.

The phase's centre is a distinction the console has to make legible rather than hide. Three modes produce
the same object — a source snapshot at one `(tenant, workflow, revision)` — and **nothing downstream can
tell which mode produced it, deliberately.** But the *customer* must be able to tell, because the three
differ in exactly one property that matters to them: what the platform can do when they are not there.

---

## 2. Problem & context

### 2.1 What exists

`internal/sourceingest` is one interface and one implementation:

- **`Source`** — *"give me a tree at this revision"*. `ErrNoSource` is a **first-class state**, not a
  failure: the overwhelmingly common case is a customer who has not opted in, and a caller must be able
  to tell that from a store it could not read.
- **`BundleSource`** — a gzipped tar the customer uploads for one revision, extracted under rules that
  treat the archive as hostile: no absolute paths, no `..` escape, no symlinks or hardlinks, no device
  nodes, caps on entry count, per-file size and total uncompressed bytes. Each rule has a test that
  constructs the malicious archive and asserts the refusal.

The package says why it stopped there, and the sentence is the one this phase has to answer:

> *A clone needs a credential that can read the customer's repository, held by the platform, usable at any
> time, for any revision, without the customer present. That is a standing capability. A bundle is an act.*

### 2.2 What a customer actually hits today

To be assessed, a person must: install a CLI, authenticate, run a command that produces a bundle, wait
for an upload, and only then open the console. Four of those five steps happen before anything about
their repository is on a screen. The drop-off is not measured because the funnel does not exist yet — but
the CLI demo's own framing ("no account and no network") tells you who the current path is for, and it is
not the person the GEHA program is built around.

### 2.3 The local path has a known, named gap

[`cmd/heroslocallink/main.go`](../../cmd/heroslocallink/main.go) states it plainly: `heros link`
transmits to `https://heros-agent.space` and nowhere else, *"enforced twice"*, and *"nothing overrides it:
not a flag, not an environment variable, not a config key."* The consequence is written in the file:

> 🔴 *The platform `make deploy-up` brings up on 127.0.0.1 **cannot be linked to** by the shipped CLI,
> even though `deploy/README.md` lists P11 run linking as served on it. That is a real product gap and
> this binary does not close it — closing it means deciding whether a self-hosted endpoint may be named,
> which is a boundary decision and not a deployment detail.*

**P32 does not get to skip this.** Mode 3 either ships for the hosted service only, or the boundary
decision is made. §14 Q1.

---

## 3. Goals & non-goals

### Goals

1. **G1** — A person can connect a repository from GitHub, GitLab or Bitbucket by authorizing access to
   **that one repository**, and reach an assessment without installing anything.
2. **G2** — The pushed bundle stays the default and loses no capability.
3. **G3** — A local repository can be assessed without its contents leaving the machine.
4. **G4** — A connection is revocable from the console, and revocation deletes derived trees.
5. **G5** — Every clone is recorded: `(tenant, repository, revision, actor, reason)`, append-only.
6. **G6** — No feature is gated on a connection. A customer who declines it reaches every surface through
   the bundle, and a surface with no snapshot says `not reported` rather than prompting for a grant.
7. **G7** — The three modes are legible to the customer and invisible to everything downstream.

### Non-goals (with the phase that owns them)

- **Assessing what was ingested** — [P33](P33-surface-assessment.md).
- **Writing to the repository** — [P35](P35-autonomous-improvement-run.md) and ADR-005; a read grant
  neither implies nor enables a write one.
- **Organization-wide access** — refused on the record in ADR-013, option B.
- **Mirroring or long-term storage of customer repositories.** A snapshot is a working input under the
  existing retention rule, not an archive.
- **Monorepo sub-path selection, submodules, LFS.** Named in §14 Q3 rather than assumed away.

---

## 4. Users & personas

| Persona | Mode they will use | What decides it |
|---|---|---|
| Engineer evaluating the product for the first time | **connection** (Mode 2) | they have not installed anything |
| Engineer at a company whose policy forbids third-party repository grants | **bundle** (Mode 1) | policy; this customer is who the current posture was written for |
| Developer iterating locally on an unpushed branch | **local** (Mode 3) | the revision does not exist on a remote |
| Security reviewer | — | reads the per-use record and the revocation behaviour |

---

## 5. User stories

- **US1** As an engineer I paste a repository URL, authorize one repository, and get an assessment,
  so that evaluating the product costs one authorization instead of an install.
- **US2** As a security reviewer I see exactly which repositories are connected, when each was last read,
  and by what, so that "usable without the customer present" is auditable after the fact.
- **US3** As an admin I revoke a connection and the platform can no longer answer from it — including
  from cache — so that revocation means what the word means.
- **US4** As an engineer whose policy forbids grants I push a bundle and lose no capability, so that the
  safe path is not the degraded one.
- **US5** As a developer I assess a local repository without uploading it, so that unpushed work is
  assessable.
- **US6** As an engineer I connect a repository whose default branch was renamed, and I am told **that**,
  so that a stale connection fails loudly rather than answering about the wrong revision.
- **US7** As a customer I see what the platform will be able to do while I am not present **before** I
  authorize, so that the standing capability is a choice I made rather than one I discovered.

---

## 6. Functional requirements

### 6.1 The seam (capability `source-acquisition`)

**FR1** — All three modes implement `sourceingest.Source`. A caller receives a snapshot and **cannot
determine which mode produced it**; there is no branch threaded through the pipeline and no field
downstream reads.

**FR2** — `ErrNoSource` remains a first-class state in all three modes and stays distinguishable from a
store that could not be read.

**FR3** — A snapshot is identified by `(tenant_id, workflow_id, revision)`. A snapshot with no revision is
refused: "the latest source" is a moving target that makes a stored graph a picture of nothing in
particular.

### 6.2 Mode 1 — bundle push (default, unchanged)

**FR4** — Everything `BundleSource` does today, unchanged, including every extraction refusal and the
compressed-size ceiling at the HTTP boundary. **FR5** — The console shows the exact command, with the
tenant and workflow already filled in.

### 6.3 Mode 2 — per-repository read grant

**FR6** — A connection authorizes **one repository**. An authorization flow that would grant access to
repositories the customer did not name is refused, on every forge. The grant kind is per forge (§14 A2):
a GitHub App installation with one selected repository; a GitLab project access token; a Bitbucket
repository access token. A workflow may declare a **sub-path** within the connected repository, and the
snapshot is rooted there (§14 A3) — the grant stays repository-scoped because no forge issues a narrower
one. Organization-wide scope is refused on the record (§14 A5, ADR-013 Option B).

**FR7** — The grant is **read-only**. No scope that can write a ref, open a pull request, or change a
setting. Where the same customer also runs the ADR-005 hosted Git App, that is a **second, separate**
installation with its own scope.

**FR8** — A connection is revocable from the console. Revocation deletes the stored grant **and every
derived tree**; a revoked connection that can still be answered from cache is not revoked.

**FR9** — Every clone appends `(tenant, repository, revision, actor, reason)` to a record the customer can
read. `actor` distinguishes a person-initiated read from a scheduled or autonomous one.

**FR10** — Before authorizing, the customer is shown what the grant permits, that it is usable when they
are not present, and how to revoke it. **FR11** — A failed clone reports which of these it was: credential
rejected, repository not found, revision not found, or network — four causes, four messages, never one.

**FR12** — No feature is gated on Mode 2. A surface with no snapshot renders `not reported`.

### 6.4 Mode 3 — local path

**FR13** — A local repository is read on the customer's machine by the existing bridge and **its contents
are not transmitted**. **FR14** — The console pairs with a local agent; the affordance is a pairing flow,
not a file picker that ships a tree. **FR15** — Mode 3 ships for the **hosted service only** (§14 A1), and the console SHALL state which
deployments it works against **before** the pairing flow starts, rather than failing at the end of it. A
self-hosted endpoint stays unnameable; naming one is a separate ADR.

### 6.5 Retention

**FR16** — A snapshot obtained by clone is retained for **72 hours** and then removed; a pushed bundle is
retained until the customer deletes it. Both figures, and why they differ, are §14 A4 — a clone is not a
licence to keep a tree longer than a push would have been, and 72 h is shorter than unbounded in the
direction this requirement was written to protect. The **extracted tree** is released at the end of the
operation in both modes and has no window at all. **FR17** — Retention is enforced on a schedule that runs
whether or not anything else does, and its last successful run is readable from a health endpoint.

---

## 7. Non-functional requirements

### 7.1 Security

- The forge credential is stored in the deployment's secret store, never in a request body, a config file,
  or an audit trail. `internal/broker`'s `Secrets` source is provider-scoped today and grows a **forge-read
  kind** — the same gap ADR-005 noted for the write kind.
- A cloned tree is hostile input in exactly the way an uploaded archive is. **The clone path SHALL reach
  the same extraction and traversal defences**, not a weaker set justified by the source being "a real
  repository." A repository can contain a symlink to `/etc`; git will happily deliver it.
- Rotation and revocation are lifecycle operations with tests, not a runbook step.
- Cross-tenant: a connection belongs to one tenant and a snapshot is keyed by tenant. The scope comes
  from the credential (P27), never from a request field.

### 7.2 Stability

A clone can fail while nobody is present — rotated token, renamed default branch, repository made
private — and each failure is most likely to be believed when unattended. So: a failed clone **never**
degrades to an older snapshot silently. It reports the cause (FR11) and the surface says so.

### 7.3 Operability

Clone duration, bytes, and failure cause by class are metrics on a readable health endpoint, not log
lines. A connection that has failed N consecutive times escalates rather than staying at WARN forever.

### 7.4 Scale

A 30,500-file repository is the demo's own benchmark, so it is the floor, not the ceiling. Shallow clone
at the requested revision; no full history unless a later phase needs one and says why.

---

## 8. System design summary

### 8.1 Shape

```
                        ┌───────────── sourceingest.Source ─────────────┐
console / conversation  │                                               │
        │               │  BundleSource   GitSource     LocalBridge     │
        └──────────────▶│   (default)     (opt-in)      (no transfer)   │
                        └───────┬───────────┬───────────────┬───────────┘
                                │           │               │
                             extract     clone@rev      read in place
                                │           │               │
                                └───────────┴───────────────┘
                                            │
                                    source snapshot
                                  (tenant, workflow, rev)
                                            │
                                     discovery.Run
```

### 8.2 Decisions

**D1 — The mode is invisible downstream and visible upstream.** `sourceingest`'s design already says the
choice between implementations *"stays one constructor call in `internal/launch` rather than a branch
threaded through the pipeline"*. P32 keeps that. What it adds is that the **customer** can always see
which mode a workflow uses, because the difference — what happens when they are absent — is theirs.

**D2 — A clone gets the bundle's hostile-input defences, not fewer.** The tempting reasoning is that a
git clone is trustworthy because git produced it. Git produces what the repository contains, including
symlinks pointing outside the tree. The defences are about the *tree*, not about the transport.

**D3 — Revocation cascades to derived trees.** The alternative — revoke the grant, keep the cache — is
the version everyone builds by accident, because deleting the grant is one line and finding the derived
artifacts is not. It is called out here so it is a requirement rather than a discovery.

**D4 — Four failure causes, four messages.** The P9 rule that failure classes stay distinguishable,
applied to intake. "Could not connect" collapses a rotated credential and a renamed branch into one
message, and they need different actions from different people.

**D5 — Mode 3 is a pairing flow, not an upload.** A file picker that reads a folder and ships it is
Mode 1 wearing Mode 3's clothes, and the customer would reasonably believe nothing left their machine.

**D6 — Shallow, at the revision.** History is not an input to discovery. Cloning it costs time and disk
and widens what the platform holds for no current consumer.

---

## 9. Design by role lens

### 9.1 Senior Product Designer — *reduce the input, never the truth*

The input reduction is one authorization instead of an install. The truth that must not reduce is FR10:
the customer sees what the grant permits **before** authorizing, including the sentence that matters —
*this can be used when you are not here*. A consent screen that lists scopes without saying that has
technically disclosed and practically not.

**The default must not be the degraded path.** If Mode 1 loses capability, every safety-conscious customer
is punished for their policy, and the posture becomes a tax rather than a choice.

### 9.2 Senior System Designer — *arbitrate by level; do not open a one-way door*

The one-way door is the grant itself, and ADR-013 documents it — including refusing organization-wide
scope **on the record**, so a later phase proposing it is proposing a change to that ADR rather than an
extension of this one.

The subtler door is **retention**. A snapshot kept "while the assessment is warm" becomes a mirror of the
customer's repository by accretion, and no single commit does it. FR16/FR17 make retention a scheduled,
observable behaviour rather than an intention.

### 9.3 Senior Backend Dev — *a 200 is not evidence of a write*

A connection endpoint returning 200 has not necessarily stored a usable grant, and a clone returning
success has not necessarily produced a tree discovery can walk. Acceptance is the live-event four-step:
connect → `SELECT` the connection row → clone → assert files on disk at the expected revision → run
discovery and assert nodes.

The same applies in reverse to FR8: revoke → `SELECT` returns nothing → assert the derived tree is gone
from disk → assert a subsequent read returns `ErrNoSource` and not a cached answer.

### 9.4 Senior Frontend Dev — *four states stay four*

FR11's four causes are four messages. The connection list shows, per repository: mode, last successful
read, last failure and its cause, and a revoke control in the hazard palette (revocation is destructive
and irreversible for derived trees). `not reported` is a rendered state, not an empty div.

### 9.5 Senior AI Engineer — *an aggregate hides the single-sample defect*

Ingest is not an ML path, but one thing here is measured and will be reported as an aggregate: **ingest
success rate**. A 97% success rate that is 100% on GitHub and 40% on Bitbucket is a broken forge adapter
hiding inside a good number. Report per-forge, per-failure-cause, never a mean.

### 9.6 Senior DevOps Engineer — *blast radius, reversible, observable, least privilege*

Least privilege is the phase. Concretely: one repository, read-only, separate from any write installation,
revocable, logged per use, and never a precondition. Blast radius of a leaked read grant is bounded to the
repositories explicitly named — which is the entire argument for refusing the org-wide form.

Egress: cloning means the platform makes outbound connections to forge hosts. That is a new egress class
and belongs on the constructed allowlist, not implicitly permitted because "it's git".

### 9.7 Senior QA Engineer — *green is worth having only if green can be red*

Fences that must be able to go red:
1. A repository containing a symlink escaping the tree → refused on the **clone** path, not only on the
   bundle path. **This is the fence most likely to be written only for bundles.**
2. Revoke → subsequent read returns `ErrNoSource`, and the derived tree is absent from disk.
3. A grant scoped beyond one repository → refused at connect.
4. A rotated credential → cause `credential rejected`, and **no** fallback to an older snapshot.
5. Retention job → a snapshot past its window is gone, and the job's last success is readable from the
   health endpoint.
6. Ingest metrics broken out per forge; assert the breakdown exists, because the aggregate is what will be
   built if nobody checks.

### 9.8 Senior Sales Operations — *only promise what shipped; state the boundary out loud*

Sayable: *"Connect one repository, read-only, revocable — or push a bundle and connect nothing at all."*
The second half is the differentiator for regulated buyers and it is only credible while Mode 1 is
genuinely equal (FR12).

Not sayable: anything implying the platform "watches" a repository. It reads a revision when asked.
Boundary to state out loud: **a connection is usable when you are not present** — said by us first, in the
consent screen, not discovered by a reviewer later.

---

## 10. Dependencies

| Needs | From | Hard? |
|---|---|---|
| `sourceingest.Source` seam | P29 | hard — exists |
| forge-read credential kind in `Secrets` | `internal/broker` | **hard — does not exist** |
| local bridge | P11 (`heroslocallink`, `clilink`) | hard — exists, with the §2.3 gap |
| a place to show connections | [P31](P31-conversational-console.md) or `/app/settings` | soft |
| retention scheduler | P19 | hard |
| egress allowlist entry for forge hosts | P11 posture | hard |

---

## 11. Risks & mitigations

| Risk | Severity | Mitigation |
|---|---|---|
| Clone path gets weaker extraction defences than bundle | **high** | D2; QA fence 1 explicitly targets the clone path. |
| Revocation leaves derived trees readable | **high** | D3 as a requirement; QA fence 2 asserts absence on disk. |
| Snapshots accrete into a mirror of the customer's repo | **high** | FR16/FR17: retention is scheduled and its last run is observable. |
| Silent fallback to a stale snapshot on clone failure | med | §7.2 forbids it; QA fence 4. |
| Mode 1 quietly becomes second-class | med | FR12 plus §9.1; a capability that works only under Mode 2 is a defect, not a tier. |
| Org-wide scope reintroduced later as a convenience | med | Refused on the record in ADR-013. |
| Mode 3 ships against hosted only and the self-hosted gap is rediscovered as a bug | med | §14 Q1 forces the decision; FR15 forbids failing at the end of the flow. |

---

## 12. Rollout & test strategy

1. **Mode 1 unchanged**, regression-fenced. Nothing about the existing path may move.
2. **Mode 2 against one forge** (GitHub first), entitlement-gated, internal tenants only. All six QA
   fences green before a second forge is started.
3. **Mode 2 across GitLab and Bitbucket**, with per-forge metrics from the first day (§9.5).
4. **Mode 3**, scoped by the §14 Q1 answer.

Rollback for each stage is disabling the mode; Mode 1 remains, so no stage's rollback removes the
customer's ability to be assessed.

---

## 13. Success metrics & acceptance criteria

| # | Criterion | How it is checked |
|---|---|---|
| A1 | A repository is assessable with nothing installed | browser acceptance, connect → discovery emits nodes |
| A2 | Downstream cannot distinguish the mode | one discovery run per mode over the same tree → identical IR |
| A3 | Escaping symlink refused on the clone path | adversarial repository fixture |
| A4 | Revoke → `ErrNoSource` and no derived tree on disk | live event, four-step |
| A5 | Grant broader than one repository refused | per-forge authorization test |
| A6 | Four failure causes → four distinct messages | one case each |
| A7 | No stale-snapshot fallback on clone failure | rotated-credential test |
| A8 | Retention removes an expired snapshot; last run readable | scheduler test + health endpoint assertion |
| A9 | Ingest metrics broken out per forge and per cause | assert the breakdown, not the aggregate |
| A10 | Local mode transmits no repository content | egress capture during a Mode 3 assessment |

---

## 14. Open questions — ANSWERED

**Status: closed 2026-08-21 (P32 §1, System Designer).** All five are answered below and folded into the
requirements they touch. The table of questions is kept underneath the answers rather than deleted,
because a phase that later wants a different answer needs to read the question that was asked.

### A1 — Mode 3 ships for the **hosted service only**, and the console says so before the flow starts

**Answer.** Mode 3 is offered against `https://heros-agent.space` and no other endpoint. A self-hosted
deployment is **out of scope for this phase** and the console states that as a precondition of the
pairing flow, not as its final error.

**Why.** `heros link` is pinned to one host and the pin is enforced twice
([`runlink.IsLinkTarget`](../../internal/runlink), and `transport.assertLinkTarget` re-checking
immediately before the request goes out). Naming a self-hosted endpoint means deciding what makes a
destination nameable — which is a boundary decision about where a customer's run payload may be
carried, and it is a **one-way door**: once an endpoint can be named by a flag, an environment variable
or a config key, every future release has to keep it nameable. That decision earns its own ADR and its
own review; smuggling it in as a P32 wiring detail would be the exact shape of change this repository's
posture exists to refuse.

**What is built instead.** FR15 becomes a *rendered precondition*: the console names the deployments
Mode 3 works against **before** authorization can begin (§3, task 4.3). A customer on a self-hosted
deployment is told at step zero, which is the difference between a limitation and a broken feature.

**Carried, not closed.** A self-hosted Mode 3 remains a real product gap. It is recorded here and owned
by a future ADR — *not* by a `TODO` in the pairing code.

### A2 — **App where the forge issues one; repository access token where it does not** — per forge, stated

| Forge | Grant kind | The narrowest form that forge actually supports | Why not the other |
|---|---|---|---|
| **GitHub** | `app_installation` | A GitHub App installation with **selected repositories** (exactly one), permissions `contents: read` + `metadata: read` | A PAT — even fine-grained — is a secret the customer pastes to us in plaintext, and it is revocable on their side only by deleting it |
| **GitLab** | `access_token` | A **project access token** scoped to one project with `read_repository` | GitLab's App/OAuth equivalents are *user*- or *instance*-scoped. An instance-wide grant is Option B of ADR-013 wearing a different name, and it is refused (A5) |
| **Bitbucket** | `access_token` | A **repository access token** with `repository:read` | Bitbucket's OAuth consumer is workspace-scoped — same objection as GitLab's |

**Why the split rather than one rule.** The rule "always an App" is unimplementable on two of three
forges, and a rule that cannot be implemented gets satisfied by pretending — a workspace grant recorded
as if it were repository-scoped. Naming the grant kind **per forge, in the store**, makes the difference
visible in the data rather than lost in an adapter.

**What must be true of both kinds.** The value never appears in a request body, a config file, a log
line, or an audit record (§7.1, task 3.3). `GrantKind` carries no scope field capable of expressing
write (§6.3 FR7). Rotation and revocation are lifecycle operations with tests (task 3.2).

### A3 — A connection covers the **whole repository**; a **workflow** names a sub-path

**Answer.** The *grant* is repository-scoped. The *ingest* is sub-path-scoped: a workflow may declare a
sub-path within a connected repository, and the snapshot materialized for that workflow is rooted there.
A monorepo with forty services is one connection and forty workflows.

**Why not a sub-path-scoped grant.** No forge issues one. GitHub scopes an installation to a repository,
GitLab to a project, Bitbucket to a repository — none of them to a directory. A `Connection` row
claiming sub-path scope would be a claim about a credential's reach that the credential does not honour,
and the first person to read that row would believe it.

**Why not "one connection per workflow, whole repository each".** Cloning 30,500 files to assess one
directory is disproportionate, and it makes the entry ceilings (`MaxBundleEntries`) fire on repositories
that are not attacking anything.

**Consequence, stated.** What the platform *could* read (the repository) is wider than what it *does*
read (the sub-path). That gap is honest and it is recorded per use: `CloneRecord` names the revision and
the run that caused the read, so "what did you actually take" is answerable from the ledger rather than
inferred from the grant.

### A4 — **72 hours** for a cloned snapshot; a pushed bundle is held **until the customer deletes it**

**The number: 72 hours (259 200 s) from the clone.** After it, the retention job removes the stored tree
regardless of whether anything referenced it.

**The bundle's rule, written down for the first time.** It was genuinely unwritten, and this question was
right to say so. Two rules were conflated under one phrase:

1. The **extracted tree** — released at the end of the operation that needed it, both modes. Already
   enforced (`Materialized.Release`, deferred by `hostdiscovery`, asserted by
   `TestRunReleasesTheSourceTree`). No window; it does not outlive the request.
2. The **stored snapshot** — a pushed bundle is retained **until the customer deletes it** through
   `DELETE /api/v1/workflow-source`. Unbounded, and correctly so: it exists because the customer ran a
   command naming a revision, and expiring it would delete an artifact they chose to hand over and would
   have to hand over again.

**Why the clone is finite when the bundle is not — and why this is not a violation of FR16.** FR16's
sentence is *"a clone is not a licence to keep a tree longer than a push would have been."* 72 h is
shorter than unbounded, so the requirement is satisfied in the direction it was written to protect.
Reading it as *"exactly the same number"* would mean holding a cloned tree forever, which inverts the
requirement: the bundle's unbounded hold is backed by a customer **act** per revision, and a clone has
no act behind it at all. An unbounded hold with no act is the standing capability ADR-013 spent its
argument bounding.

**Why 72 and not 24 or 168.** 24 h expires a snapshot across a weekend, so a Monday re-run silently
re-clones — which spends the grant, and spending a grant unnecessarily is the cost this phase is
measuring. 168 h means a connection revoked and forgotten on a Friday can leave a tree on our disk into
the following week. 72 h spans a weekend, does not span a holiday, and is short enough that the worst
case is bounded in days rather than in "until somebody notices."

**Independent of retention: revocation deletes immediately** (D3, FR8). Retention is the floor, not the
mechanism — a customer revoking after an incident is not asking for a faster cache expiry.

### A5 — Organization-wide scope is **refused on the record** (ADR-013 Option B)

No grant that covers a repository the customer did not name may be created, on any forge. This is not a
default; there is no field in `Connection` that can express it and the connect path refuses an
authorization whose resulting grant is broader than the single named repository (task 2.6), per forge,
with a fence that can go red (task 7.4).

**A later phase proposing organization-wide scope is amending [ADR-013](../adr/ADR-013-source-acquisition-posture.md),
not extending P32.** That is the whole point of writing it here: the refusal has an address.

### A6 — An unattended clone needs **no separate entitlement** in this phase

**Answer.** `CloneRecord.Actor` distinguishes a person-initiated read from a scheduled or autonomous one
(FR9), and that distinction is a **record**, not a gate. There is no plan tier, no toggle and no
entitlement check keyed on it in P32.

**Why not.** Two reasons at different levels of the ladder. *Level 5/6:* an entitlement axis is a pricing
and packaging surface, and a pricing surface is a one-way door — it is far easier to add a tier later
than to remove one customers have bought. *Level 3:* the control the customer actually asked for already
exists and is simpler — revoking the connection stops every read, attended or not, and it is one button.

**What would change the answer.** A customer who wants attended-only reads has a real requirement, and it
is not served by revocation (they would have to re-authorize per use, which is Option D of ADR-013 and
was rejected for forbidding automation). If that demand appears, the next phase adds a per-connection
`unattended: allow|deny` flag — which is a field on an existing row, not a new table and not a new tier.
Recorded so the cheap version is the one that gets built.

---

### The questions, as they were asked

| # | Question | Why it is open |
|---|---|---|
| **Q1** | Does Mode 3 work against a self-hosted deployment? | Requires deciding whether a self-hosted endpoint may be named — the boundary decision `heroslocallink` explicitly declines to make. **Recommendation: ship Mode 3 for the hosted service, state it in the UI (FR15), and open a separate ADR for endpoint naming.** |
| **Q2** | Is the read grant a forge **App installation** or a **repository access token** the customer pastes? | An App is revocable on both sides and auditable on theirs; a pasted token is faster to build and is a secret the customer hands us in plaintext. **Recommendation: App where the forge supports it, token only where it does not.** |
| **Q3** | Monorepos: does a connection cover the whole repository, or a sub-path? | A monorepo with forty services is not one workflow, and cloning it to assess one directory is disproportionate. Not assumed away here. |
| **Q4** | What is the retention window for a cloned snapshot, in hours? | FR16 says "the bundle's rule"; I could not find that rule stated as a number anywhere in the tree. If it is not written down, it is not a rule. |
| **Q5** | Does an autonomous (unattended) clone need a separate entitlement from a person-initiated one? | FR9 distinguishes them in the record. Whether the customer may permit one and forbid the other is a product decision with a plan-tier consequence. |
