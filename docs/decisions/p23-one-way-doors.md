# P23 — The one-way doors, ratified

Product rationale: [`docs/prd/P23-legal-and-developer-docs.md`](../prd/P23-legal-and-developer-docs.md).
Engineering design: [`openspec/changes/archive/2026-08-01-p23-legal-and-docs/design.md`](../../openspec/changes/archive/2026-08-01-p23-legal-and-docs/design.md).

This record closes **tasks 1.1–1.6**. It exists because P23's failure mode is not a crash — it is a
commitment that cannot be evidenced years after it was made. The decisions below are the ones that cannot
be taken back cheaply; everything else in the phase is replaceable.

**Arbitration law:** `安全 > 稳定 > UX > 运维 > 不可演进 > 不可扩展 > 维护 > 实现`. Where a decision costs
something, the cost is named.

---

## 1.1 · Decision 2 — a legal document's identity is `(kind, version, content_hash)` — **RATIFIED**

**Status:** Accepted (2026-07-31). This is the contract that outlives every other artifact in the phase.

Consent points at the triple. **Never at a URL.**

| | What is stored | What it survives |
|---|---|---|
| A URL | `https://…/legal/terms` | nothing — the text behind it can change under the record |
| Rendered HTML | the page as it looked | a renderer upgrade, badly; the row bloats and the evidence couples to a React version |
| **`(kind, version, content_hash)`** | three short strings | a redeploy, a reformat, a renderer change, a re-publication under an unchanged version |

Hash rule, fixed here so a later implementation cannot quietly widen it: **normalized source, front matter
excluded, line endings normalized to `\n`, trailing whitespace stripped per line, trailing blank lines
collapsed.** A reformat that changes no words changes no hash; a word change always changes it.

**The one-way door this closes:** deleting an archived version orphans every consent record referencing it,
and there is no recovery — the row says "they agreed to v1.0.0" and nothing can any longer say what v1.0.0
said. So **every superseded version stays served forever** at `/legal/{kind}/v/{version}`, and a fence fails
the build on a manifest entry whose document no longer resolves (§8.6). This is enforced by a machine
because it will otherwise be violated by a routine cleanup, years from now, by someone who never read this
file.

Recorded in full as [ADR-011](../adr/ADR-011-legal-and-docs-content-as-code.md).

---

## 1.2 · Decision 1 — content is code in the console image — **RATIFIED as ADR-011**

[ADR-011 — Legal and documentation content is code in the console image](../adr/ADR-011-legal-and-docs-content-as-code.md).

No CMS, no runtime content fetch, no external docs host. **The accepted cost is written down there rather
than implied:** a copy fix requires a console deploy.

> **Numbering.** The design document says "ADR-010". That number was taken by
> [`ADR-010-runtime-gradual-rollout.md`](../adr/ADR-010-runtime-gradual-rollout.md) between the design being
> written and this being ratified. The decision lands as **ADR-011**. The design doc's pointer is stale by
> a digit, and this note is why a reader who follows it does not conclude the ADR was never written.

---

## 1.3 · Decisions 3 and 4 — the two that decide whether a legal update can take the console down — **RATIFIED**

### Decision 3 — materiality is a **declared** field, never a diff heuristic

Front matter carries `material: true|false`. **The build fails if a new version omits it.**

The fence does not decide materiality — no machine can. It forces the decision to **exist** and to be
**attributable**: set in a reviewed pull request, visible in `/legal/manifest.json` and on the
version-history page.

- A typo fix must not push a consent interstitial at every customer — **第3级 UX**.
- A rights-changing amendment must not slip through silently — **第1级 / compliance**.

**Rejected:** inferring materiality from diff size, or from which sections changed. Both are plausible and
both are wrong in exactly the cases that matter — a five-word change to a liability clause, and a wholesale
reformat that changes nothing.

### Decision 4 — the gate blocks commitments; it never walls the console

Re-acceptance is demanded at **first sign-in for a principal with no acceptance**, **checkout** (P21), and
**plan change**. It is *not* demanded from a session already in flight, which gets a persistent, dismissible
notice naming the document and its effective date.

The failure avoided is specific and self-inflicted: a consent modal keyed to a deployment blocks **every
customer simultaneously**, and it does so on release day — converting a legal update into an outage
(**第2级**) while interrupting work the customer is in the middle of (**第3级**).

**Failure behaviour is asymmetric on purpose:**

| Path | Behaviour when acceptance cannot be recorded |
|---|---|
| A commitment (checkout, plan change, first sign-in) | **fail-closed** — the commitment does not proceed |
| Reading (the console, an in-flight run, a legal document) | **fail-open** — it stays available |

And under no circumstance is an unrecorded acceptance rendered as recorded. **No optimistic checkmark,
ever.**

---

## 1.4 · Escalated, not self-decided — OQ1 and OQ2

Both are business commitments. Both were escalated and answered on **2026-07-31**.

### OQ1 — governing law, contracting entity, jurisdiction — **ANSWERED**

| Field | Value |
|---|---|
| Contracting entity | **PLUTUX TECHNOLOGY LLC** |
| Entity type | Limited liability company |
| Formation state | **Nevada, United States** |
| Registered address | 930 S 4th St, Ste 209 #281, Las Vegas, Nevada 89101, US |
| Entity status at time of answer | Active / Good Standing |
| Governing law | **The laws of the State of Nevada, United States**, without regard to conflict-of-law rules |
| Venue | The state and federal courts located in **Clark County, Nevada** |

These are the facts the Terms are written against, and the entity named on both documents. They are
**commercial facts, not legal advice** — the words that carry them are authored with counsel (§8.1), and
this record fixes only the facts counsel is drafting around.

### OQ2 — consent-record retention period — **ANSWERED: 7 years**

The window is **configuration**, so the code did not wait on it and does not hard-code it. The retention
job reads a single configured duration, is **runnable dry**, and refuses to delete anything when the window
is unset rather than falling back to a default — a deletion job whose first production run is also its
first run ever is a defect waiting for a quiet weekend (§9.7).

---

## 1.5 · OQ3 — the HTTP API reference tier — **DECIDED: ships ABSENT with the reason**

Hand-writing the tier is **forbidden either way** (Decision 6). The choice was emit-or-absent, and it is
made explicitly rather than by whichever was easier in the week.

**Decision: the HTTP API reference tier ships ABSENT, and the page says why.**

| Tier | Status | Source artifact |
|---|---|---|
| CLI reference | **EXISTS** | the `internal/cli` command registry |
| Schema reference | **EXISTS** | `schemas/*.schema.json` |
| HTTP API reference | **ABSENT** | there is no OpenAPI document in the repository (verified 2026-07-31) |

**Why absent rather than emitted.** Generating OpenAPI from the route table is not free, and a generator
that is subtly wrong is worse than no artifact at all: `scan-api` would then be checking documentation
against a **fiction**, and would pass. An absent tier that names its reason is honest and costs a reader
one paragraph. A wrong artifact costs them a debugging session and costs us the fence.

**The fence's obligation under this decision is sharper, not weaker:** `scan-api` **refuses any documented
endpoint, method or field while the artifact is absent**, rather than passing vacuously. A vacuous pass is
how "we have an API fence" becomes true and useless in the same sentence (§4.8).

This is the same **EXISTS / PARTIAL / ABSENT** posture P13–P18 use for optimization axes, applied to
documentation.

---

## 1.6 · OQ7 — the URL shape, reserved before any external link exists — **DECIDED**

Cheap now; a redirect table later. Anchors are a published contract (Decision 8) and URLs are the coarsest
anchor there is.

**Legal — versioning is live today:**

```
/legal/{kind}                 the current version           kind ∈ {terms, privacy}
/legal/{kind}/v/{version}     a permanent, immutable route  every version, forever
/legal/manifest.json          kind → versions → {effective_date, hash, route, material}
```

**Docs — unversioned today, with the version segment reserved:**

```
/docs                         the section index
/docs/{section}/{page}        current documentation — ALWAYS means "the version that is deployed"
/docs/v/{platform_version}/…  RESERVED. Nothing serves here yet.
```

**The reservation is a rule with a machine behind it:** `v` is a **reserved first segment** under `/docs`.
No documentation section may be named `v`, so the day versioned docs ship, `/docs/v/0.20.0/cli/apply`
cannot collide with an existing page and no published URL has to move. `scan-links` enforces the
reservation; the alternative is discovering the collision after the URLs are in a customer's bookmarks and
inside a shipped binary's error messages.

**Locale is already segmented** on the content path (`content/{legal,docs}/en/**`) for the same reason,
one level earlier. English is authoritative; localization is out of scope for this phase, and the path does
not have to move when it stops being.

---

## What this record does **not** decide

Named, so nobody reads a silence as a decision:

- **The legal words.** Authored with counsel (§8.1–8.2). Engineering supplies structure, front matter and
  the commercial facts above.
- **OQ4** — whether the P8 operator console needs a consent read surface this phase. Deferred: tenant-facing
  history (§10.1) plus a database query is the answer until an auditor asks. Operator-side access stays
  behind P8's existing RBAC and append-only audit (§11.4).
- **OQ5** — whether a separate "acceptable use" section is needed inside the Terms for the P3/P12 sandboxed
  execution and forge-delivery paths. Counsel's call, at authoring time.
- **OQ6** — the corpus size at which titles/headings/lead-paragraph search ranking stops being adequate.
  The limit is stated in the generator's own header (Decision 9) so a reader meets it as a disclosure
  rather than as a surprise; the revisit trigger is not fixed here.
