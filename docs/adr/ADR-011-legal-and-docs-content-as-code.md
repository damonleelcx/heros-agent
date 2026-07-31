# ADR-011 — Legal and documentation content is code in the console image

- **Status:** Accepted (2026-07-31)
- **Deciders:** System Design (proposed) + User (ratified)
- **Scope:** the P23 reading surface — `web/console/content/{legal,docs}/en/**` and everything rendered
  from it at `/legal/**` and `/docs/**`.
- **Constrained by:** [ADR-006](ADR-006-console-deploy-packaging.md) (the console is its own container in
  the platform's deployment unit) and the P19 air-gapped package (a deployment with **no egress at all**).
- **Relates to:** [ADR-008](ADR-008-console-tenant-identity-seam.md) — the consent record binds to the
  abstract principal defined there, so P22 requires no migration on this path.

> **Numbering note.** P23's design document calls this "ADR-010". `ADR-010` was taken by
> [runtime-gradual-rollout](ADR-010-runtime-gradual-rollout.md) between the design being written and this
> being ratified, so the decision lands as **ADR-011** and the design document's reference is stale by a
> digit, not by a decision. Recorded here rather than silently renumbered, because a reader who follows
> the design doc's pointer deserves to find out why it misses.

## Context — what problem this fixes

Two surfaces the console has never had are now blocking: a **Terms of Service and Privacy Notice**, and
**developer documentation**. They are the same engineering problem wearing different words. Both are
long-form text served to readers with **no session**, both must stay true as the system changes, and both
must keep serving **when the platform does not**.

The question this ADR settles is where that text lives. Four candidates were real:

| | Where the words live | Who can write them |
|---|---|---|
| (a) Headless CMS | Contentful / Sanity, fetched at build or render | anyone with a CMS login |
| (b) Platform-served | rows behind the BFF | anyone with database write |
| (c) External docs host | Docusaurus / Mintlify on its own domain | anyone with that host's login |
| (d) **Content as code** | Markdown in the console image | anyone who can land a reviewed pull request |

## Decision

**Markdown with YAML front matter under `web/console/content/{legal,docs}/en/**`, rendered at build time
into the console container. No CMS, no runtime content fetch, no external docs host.**

The path is **locale-segmented from day one** (`/en/`) even though English is the only language and the
only authoritative one — because adding a locale segment later means rewriting every published URL, and
published URLs are a one-way door (see [Decision 2](#the-identity-decision-this-adr-carries), below).

## Why, by the priority law

The arbitration is `安全 > 稳定 > UX > 运维 > 不可演进 > 不可扩展 > 维护 > 实现`, and this one separates at
the first level rather than the last.

**第1级 安全.** (a) and (c) put **a third party with write access on the highest-trust page the product
has** — the page where a customer reads what they are agreeing to. That is an injection surface and an
authorship-provenance hole in one move: "who changed the liability clause, and when" becomes a question
answered by a vendor's audit log rather than by our own history. It also contradicts, on the page itself,
the console's own `default-src 'self'` posture.

**第2级 稳定.** The legal surface must serve **during a platform incident** — which is exactly when a
customer goes looking for it. (b) makes the Terms unreadable at that moment. And a **P19 air-gapped
deployment has no egress**: (a) and (c) do not degrade there, they do not function.

**第4级 运维.** (a)/(c) add a second system to operate, back up, patch, and audit access to — for text
that changes a few times a year.

**第5级 演进.** **Git history *is* the legal change history** an auditor asks for, with authorship, review
and timestamps already attached to it. That artifact can be reconstructed from a CMS revision list, but the
reconstruction is not the same evidence, and the difference shows up in the one conversation where it
matters.

## The cost, named rather than waved away

**A copy fix requires a console deploy.** That is a real 第4级/第8级 cost and it is *accepted*, not
denied:

- the deploy path is one P19 already makes routine, and content ships in the console container, so a bad
  copy change is reverted by redeploying the previous console image — **no migration, no platform
  restart**;
- legal content changes slowly by nature, and documentation changes ride the pipeline every code change
  already rides;
- **mitigation:** the content is plain Markdown with **no JSX and no components**, so a non-engineer can
  open a pull request against it. The `scan-content` fence enforces that plainness rather than trusting it.

The person who wants a CMS in eighteen months gets this paragraph rather than a re-litigation.

## The identity decision this ADR carries

Content-as-code is only half the answer; the other half is what a consent record points at.

**A legal document's identity is `(kind, version, content_hash)`. Consent points at that triple — never at
a URL.** "The customer agreed to the Terms" is meaningless if "the Terms" is a URL whose text has since
changed.

- `content_hash` is computed at **build time over normalized source**: front matter excluded, line endings
  and trailing whitespace normalized — so a reformat that changes no words changes no hash, and a word
  change always changes it.
- It is published **on the page**, in the **print footer**, and in **`/legal/manifest.json`**.
- The consent record stores the hash **as accepted**, so a later republication that edits text under an
  unchanged version number is **detectable** rather than invisible.
- **Every superseded version stays served forever** at `/legal/{kind}/v/{version}`. Deleting one orphans
  every consent record referencing it — a one-way door — so a **fence fails the build** on a manifest entry
  whose document no longer resolves. Prevented by a machine, not by care.

**Rejected:** storing rendered HTML in the consent record (couples the evidence to a renderer version and
bloats the row), and storing only a URL (the failure above, which is the whole reason this ADR has a second
half).

## Consequences

- Every copy change is a reviewed pull request against the console, and ships on a console deploy.
- The reading surface makes **no upstream request**, so its availability is the console's availability —
  asserted by the existing harness's upstream-request counter, not by a claim.
- Docs and legal are **byte-identical** in the air-gapped package and the hosted deploy, because there is
  only one artifact.
- Publishing a new legal version is an append: a new file, a new manifest entry, and the previous route
  keeps resolving forever.
- The fences (`scan-content`, `scan-links`, `scan-secrets`, `scan-docs-claims`, `scan-cli`, `scan-api`,
  `scan-metric`, `scan-install`) are what keep "content is code" from meaning "content nobody checks".
