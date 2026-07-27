# P23 — Legal Surface & Developer Documentation · Design

Product rationale: [`../../../docs/prd/P23-legal-and-developer-docs.md`](../../../docs/prd/P23-legal-and-developer-docs.md).

This document records the decisions with real trade-offs, the alternatives rejected and why, and the
interfaces that outlive the code. Decisions are arbitrated with the priority law — **安全 > 稳定 > UX > 运维
> 不可演进 > 不可扩展 > 维护 > 实现** — and where a decision costs something, the cost is named rather than
waved away.

---

## Context

Two surfaces, one shape. Legal documents and developer documentation are both **long-form text served from
the console to readers with no session**, both must **stay true as the system changes**, and both must
**keep serving when the platform does not**. Their characteristic failure is drift, not a crash — and drift
is found by customers, auditors and regulators rather than by tests.

What already exists and constrains the design:

- The console is a Next.js app with **two compositions**: `(public)` — dark-fixed, session-free, a poster —
  and `/app` — theme-following, session-bound, **viewport-first** (`md:overflow-hidden`, bounded `main`).
  The `(public)` layout's own header comment argues that a public surface must be a **separate composition**
  rather than the console shell with the tenant parts hidden, because a shared shell is how a public page
  acquires a session call.
- The console already has a **build-time fence idiom**: `scan-claims.mjs` gates every public capability claim
  against `src/content/capabilities.ts` and **fails the build** on an unlisted or unshipped claim. Alongside
  it: `scan-tokens`, `scan-strings`, `scan-markup`, `scan-bundle`.
- The test harness runs a **stub platform** and counts upstream requests, so "this surface fetched nothing"
  is an assertion the suite already knows how to make (`routes.test.mjs`).
- **ADR-006** ships the console as its own container in the platform's deployment unit. **ADR-007** governs
  console type generation. **ADR-008** defines the abstract tenant principal seam that P22 will make real.
- `schemas/` holds JSON Schemas for the IR, metric events, runtime invocations and the console view. There
  is **no OpenAPI document anywhere in the repository** (verified).
- Latest migration is `0015_p12_delivery`; P21 and P22 are docs-only and have claimed no number.

---

## Decision 1 — Content is code in the console image. No CMS, no runtime fetch, no external docs host.

**Alternatives considered.** (a) A headless CMS (Contentful/Sanity) with the console fetching at render or
build. (b) Platform-served content through the BFF. (c) An external docs host (Docusaurus/Mintlify) on its
own domain.

**Decision.** Markdown with YAML front matter under `web/console/content/{legal,docs}/en/**`, rendered at
build time into the console container.

**Why, by level.**

- **第1级 安全** — (a) and (c) put a **third party with write access on the highest-trust page the product
  has**: the page where a customer reads what they are agreeing to. That is an injection surface and an
  authorship-provenance hole in the same move.
- **第2级 稳定** — the legal surface must serve during a platform incident; (b) makes the terms unreadable
  exactly when the customer is most likely to go looking for them. An **air-gapped P19 deployment has no
  egress at all**, so (a) and (c) do not merely degrade there, they do not work.
- **第4级 运维** — (a)/(c) add a second system to operate, back up, patch and audit access to.
- **第5级 演进** — **git history *is* the legal change history** an auditor asks for, with authorship,
  review and timestamps attached. Reconstructing that from a CMS's revision list is possible but is not the
  same artifact.

**The cost, named.** A copy fix requires a console deploy. This is a real 第4级/第8级 cost and it is accepted,
not waved away: the deploy path is one P19 already makes routine, legal content changes slowly by nature,
and documentation changes ride the same pipeline every code change rides. **Mitigation:** content is plain
Markdown with no JSX, so a non-engineer can open a pull request against it.

**Recorded as ADR-010**, because the next person who wants a CMS deserves the reasoning rather than a
re-litigation.

---

## Decision 2 — A legal document's identity is `(kind, version, content_hash)`; consent points at that, never at a URL

"The customer agreed to the Terms" is meaningless if "the Terms" is a URL whose text has since changed.

- `content_hash` is computed at **build time over normalized source** (front matter excluded, line endings
  and trailing whitespace normalized — so a reformat that changes no words changes no hash, and a word change
  always changes it).
- It is published **on the page**, in the **print footer**, and in **`/legal/manifest.json`**.
- The **consent record stores the hash as accepted**, so a later republication that edits text under an
  unchanged version number is **detectable** rather than invisible.
- **Every superseded version stays served forever** at `/legal/{kind}/v/{version}`. Deleting one orphans
  every consent record referencing it — a **one-way door** — so a fence fails the build on a manifest entry
  whose document no longer resolves. This is prevented by a machine, not by care.

**Rejected:** storing the rendered HTML in the consent record (couples the evidence to a renderer version and
bloats the row), and storing only a URL (the failure mode above).

---

## Decision 3 — Materiality is a declared field, not a diff heuristic

Front matter carries `material: true|false`. The build **fails if a new version omits it**.

- A typo fix must not push a consent interstitial at every customer — **第3级 UX**.
- A rights-changing amendment must not slip through silently — **第1级 / compliance**.
- No machine can judge materiality. So the fence does not decide; it **forces the decision to exist and to
  be attributable** — set in a reviewed pull request, visible in the manifest and on the version-history
  page.

**Rejected:** inferring materiality from diff size or from which sections changed. Both are plausible and
both are wrong in the cases that matter (a five-word change to a liability clause; a wholesale reformat that
changes nothing).

---

## Decision 4 — The gate blocks commitments; it never walls the console

Re-acceptance is demanded at: **first sign-in for a principal with no acceptance**, **checkout** (P21), and
**plan change**. It is *not* demanded from a session already in flight, which instead gets a persistent,
dismissible notice naming the document and its effective date.

The failure this avoids is specific and self-inflicted: a consent modal keyed to a deployment can block
**every customer simultaneously**, and it will do so on release day — converting a legal update into an
outage (**第2级**) while interrupting work the customer is in the middle of (**第3级**).

**Failure behavior is asymmetric on purpose:** *fail-closed on the commitment* (if acceptance cannot be
recorded, checkout does not proceed) and *fail-open on reading* (the console and the documents stay
available). Under no circumstance is an unrecorded acceptance rendered as recorded — see Decision 5.

---

## Decision 5 — Persist-then-acknowledge, with idempotency in the schema

```
legal_acceptance
  id                 uuid        primary key
  tenant_id          text        not null
  principal_id       text        not null   -- opaque (ADR-008); never an email
  document_kind      text        not null   -- 'terms' | 'privacy'
  document_version   text        not null
  content_hash       text        not null
  accepted_at        timestamptz not null
  method             text        not null   -- 'signin' | 'checkout' | 'plan_change' | 'api'
  superseded_by      uuid        null       -- set when a MATERIAL later version is published
  unique (tenant_id, principal_id, document_kind, document_version)
```

- **Append-only.** A withdrawal or a re-acceptance is a new row; nothing is updated in place except
  `superseded_by`, which is bookkeeping and not a rewrite of what was agreed.
- **Idempotency is the unique constraint.** A double-clicked button, a retried request and a back-button
  resubmit collapse to one row. "Check then insert" in application code is a race with a customer's
  double-click.
- **Persist-then-acknowledge.** The 201 is written after commit, never before — the mirror of the P21
  webhook rule, for the same reason: an acknowledged consent with no row is indistinguishable from consent
  that never happened, and the *direction* of the error is what matters.
- **Server-side hash validation.** The submitted `content_hash` is checked against the manifest the server
  knows. Without it the record says whatever the browser said, and its audit value is zero.
- **Data minimization (NFR9).** No email, no name, no free text. This is what makes erasure a **tombstone of
  the subject** rather than a rewrite of the evidence, and it is decided now rather than during the first
  erasure request.
- **Migration is expand-only**: one new table, no alteration of existing ones, a `.down.sql` that drops only
  what it created.

**Rejected:** a `legal_acceptance_current` materialized view or a "latest acceptance" column on the tenant —
both are derivable, and both create a second truth that drifts.

---

## Decision 6 — Reference is generated; where the artifact does not exist, the tier is marked absent

Honest status at authoring time:

| Tier | Status | Source artifact |
|---|---|---|
| CLI reference | **EXISTS** | the `internal/cli` command registry |
| Schema reference | **EXISTS** | `schemas/workflow-ir.schema.json`, `metric-event.schema.json`, `runtime-invocation.schema.json`, `console-view.schema.json` |
| HTTP API reference | **ABSENT** | no OpenAPI document exists in the repository (verified) |

So this phase either **emits** the HTTP artifact from the existing route table or **renders the tier as
absent with the reason**. What it must not do is hand-write an endpoint list: that is a copy of the truth
that begins drifting the day it is written, and it defeats the API fence, which can only check documentation
against an artifact.

This is the same **EXISTS / PARTIAL / ABSENT** posture P13–P18 use for optimization axes, applied to
documentation: an absent tier that says why is honest; a hand-written one is a fiction with a table of
contents.

---

## Decision 7 — A third composition: the reading surface

| | `(public)` | `/app` | `(reading)` — new |
|---|---|---|---|
| Session | none | required | none |
| Theme | dark-fixed (`--marketing-*`) | follows the reader | **follows the reader** |
| Scroll | page | **viewport-first, bounded `main`** | **document scroll** |
| Data | none | tenant read-model | none |
| Read for | seconds | an hour, interactively | **an hour, linearly; and printed** |

**The viewport-first exemption is a decision, not an oversight.** A 9,000-word Terms of Service inside a
bounded inner scroll region is hostile to read, breaks the browser's own find-and-scroll behavior, and
prints as one page of clipped text. This is stated here so that the next reader of `layout.tsx` sees a
deliberate exemption rather than a page somebody forgot to bound.

**Islands only.** TOC scroll-spy and search are the only client components. Everything else is server-rendered
Markdown, which is what makes "no client JS beyond two islands" and the unchanged bundle budget hold, and
what makes the surface readable with JavaScript disabled.

**Reuse over invention:** multi-language code samples use the console's **existing `<Tabs>` component**. A
second tab implementation is a second set of keyboard-navigation and focus bugs.

---

## Decision 8 — Anchors are a published contract

CLI error messages, console empty states and API error bodies will deep-link into documentation. A renamed
heading therefore breaks a link that ships **inside a binary the customer already installed**.

- Every heading gets a stable slug, emitted into `docs/slug-manifest.json` by the same render pass that
  produces the pages — so the manifest cannot drift from the page.
- Removing or renaming a slug **fails the build** unless the same change adds a redirect.
- This is the discipline the console already applies to its legacy routes, which resolve by **permanent
  redirect** rather than 404 (`routes.test.mjs`).

---

## Decision 9 — Search is a build-time static index

No hosted search service, for Decision 1's reasons (第1级 third party on a trust surface; 第2级 air-gapped
deployments have no egress).

**Disclosed limit:** the first cut ranks over **titles, headings and lead paragraphs**, not full text, and
the index grows with the corpus. Stated in the fence/generator header and in the PRD's open questions rather
than discovered later by a reader who assumed otherwise.

**Degradation:** with JavaScript disabled the surface is a browsable table of contents — not a blank page and
not a spinner.

---

## Decision 10 — The Privacy Notice is generated against a data inventory and asserts only executable rights

The inventory is produced by **engineering first** and is counsel's *input*, so the notice describes the real
system rather than a plausible one. It names the actual stores — eval results and registries (Postgres),
telemetry and spans (P2.5), delivery records (P12), session state (P9), billing objects (P7/P21) — with
categories, retention and processor, each **checkable against `db/migrations/postgres/`** or explicitly
marked external.

Rights are asserted **only where a route exists**. At P23 the route is a documented request address plus the
P8 operator runbook, and the notice says so plainly rather than implying a self-serve button. This is the
sales-operations rule — *only promise what has been delivered* — applied where over-promising is a legal
liability rather than a support ticket.

---

## Decision 11 — Eight fences, each with a failing fixture, each stating what it does not check

| Fence | Fails the build when |
|---|---|
| `scan-docs-claims` | a docs page describes a capability not `shipped: true` with a named owning phase in `CAPABILITIES`, **or an install channel the release pipeline does not publish** |
| `scan-cli` | **(docs → code)** a `heros …` invocation in content names a subcommand or flag the registry does not have; **(code → docs)** a subcommand in the registry has no reference entry; an exit code's documented meaning disagrees with `internal/cli` |
| `scan-api` | a documented endpoint, method or field does not resolve against the machine-readable API artifact |
| `scan-metric` | a metric definition disagrees with the harness on name, unit or computation, or cites no computation site |
| `scan-links` | an internal link or anchor does not resolve, or an external link is not allow-listed and marked |
| `scan-secrets` | content matches a credential pattern (provider key prefixes, PEM blocks, bearer tokens) |
| `scan-content` | content contains raw HTML, an inline event handler, or an external script/font/stylesheet reference |
| `scan-install` | an asset filename, version or **checksum is hand-typed rather than generated** from the release; a documented install path places the binary on `PATH` **before** verification; a signing/notarization claim names a step the pipeline does not perform |

Two rules carried over from `scan-claims.mjs`, which already gets both right:

1. **Each fence states in its own header what it does *not* check.** A fence that implies broader coverage
   than it has is worse than no fence, because it stops the human review that would have caught the rest.
   Tone, emphasis and omission are **not** machine-checkable and stay a named review responsibility.
2. **Each fence ships with a fixture proving it fails.** A fence with no failing fixture is not tested, and
   an untested fence is a green light with no bulb.

---

## Decision 12 — The install page is generated from the release, and documents only channels that exist

Asset filenames, target platforms, version strings and checksums come from the published release, never from
a hand-typed line. The generation rule is the same as Decision 6's, but the reason is sharper: **a stale
checksum on an install page teaches readers that verification fails routinely**, which is precisely how a
security step becomes a step people skip.

A channel is documented **only once it is published**, under the claims fence. An install command that 404s
is the worst possible first sentence of a product.

**Honest status.** `.github/workflows/` holds only `ci.yml` and `heros-eval.yml` — **there is no release
pipeline and therefore no published GitHub Release**. What exists is the P11 supply-chain floor:
`scripts/release-cli.sh` (reproducible build, sorted `SHA256SUMS`, ed25519 signature via `cmd/herossign`)
and `docs/release/cli-verification.md`. So the install page ships describing **what exists** — build from
source, and the verification runbook — and states plainly that packaged channels are not yet available.
Each P20 channel's documentation becomes publishable **as that channel becomes real**.

This is the same EXISTS / PARTIAL / ABSENT posture as Decision 6, applied to distribution: P23 is therefore
**not blocked by P20**, and its install content grows with P20 rather than waiting for it or preceding it.

---

## Decision 13 — Verification is a step of the install, never an appendix

The CLI *"runs inside your CI with access to your repository, so a compromised release is a compromise of
every build it runs in."* Two consequences, both structural rather than editorial:

1. **The shortest documented path is the verified path.** Readers follow the line that fits on one line. If
   the one-liner installs and a later section explains verification, the one-liner is what ships to
   production — and it has silently removed the control the threat model rests on.
2. **A documented path that reaches `PATH` before verifying is not published at all.** This is a
   publication rule with a reviewer-citable name, not a preference to be argued per pull request.

**Priority-law reading:** 第1级 安全 over 第3级 UX over 第8级 实现. An unverified one-liner is easier to
write, easier to copy and shorter on the page. It is still refused.

**Corollary — trust claims may not outrun the pipeline.** "Signed", "notarized" and "Authenticode" are
claims about steps the pipeline performs; where an artifact is unsigned the page says unsigned and documents
the exact quarantine-clear command and what accepting it means. The customer meets the truth at the
Gatekeeper dialog either way; the only question is whether we told them first.

---

## Decision 14 — The CLI fence runs in both directions

As first specified, the CLI fence catches documentation naming a command that does not exist. The failure
that actually accumulates is the inverse: **a command that exists and is undocumented**, because adding a
subcommand is a normal Tuesday and remembering the reference is not.

So coverage is asserted **against the registry**: a subcommand present with no reference entry fails the
build. The exit-code contract gets the same treatment — `internal/cli/exit.go` already says the codes are
public "the moment a customer's pipeline branches on them", and a contract nobody can look up is a contract
nobody can rely on.

---

## Risks carried by this design

| Risk | Why we accept it | What limits the damage |
|---|---|---|
| Content-in-image friction pushes someone toward a CMS later | The alternative costs 第1/2/4/5级 | ADR-010 records the reasoning and the accepted price |
| Materiality mis-declared | No machine can judge it | Mandatory, attributable, visible in the manifest and history |
| Fences imply more coverage than they have | Partial coverage still beats none | Each header states its limits (NFR12); tone/emphasis explicitly a review job |
| API reference ships absent | An absent tier is honest | Decision 6 forbids the hand-written alternative outright |
| Archived version deleted in a cleanup | Human error is certain over years | A fence fails the build on an unresolvable manifest entry |
| Consent records complicate erasure | Evidence must outlive the identity | NFR9: the row holds no personal data beyond an opaque id |
