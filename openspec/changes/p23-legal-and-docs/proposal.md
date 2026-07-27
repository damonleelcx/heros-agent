## Why

The console has two surfaces it has never had, and both are now blocking.

**There is nothing legal.** A repository-wide search for "terms of service", "privacy notice" or "privacy
policy" across `.tsx`, `.ts`, `.md` and `.go` returns **zero hits**. The public layout
(`web/console/src/app/(public)/layout.tsx`) has a footer with a wordmark and one boundary statement;
[`signin/page.tsx`](../../../web/console/src/app/signin/page.tsx) has no legal line; `/app/account` renders plan and spend with nothing about the agreement governing them.
Meanwhile [P21](../../../docs/prd/P21-stripe-payments.md) puts a Stripe Checkout in front of customers and
[P22](../../../docs/prd/P22-sso-identity.md) lets an enterprise IdP create tenants — two moments where a
commitment is made against **no document, no record, and no way to answer "what exactly did this customer
accept, and when?"**, which is the first question in a billing dispute, a security review and a
data-protection audit.

**There is no documentation for the people the product is sold to.** The repository documents itself
unusually well *inward* — a 279-line README, 27 phase PRDs, 9 ADRs, a decisions log — and not at all
*outward*. A developer who installs the CLI ([P20](../../../docs/prd/P20-installable-packages.md)) or opens
the console ([P9](../../../docs/prd/P9-web-console.md)) has no quickstart, no task-shaped guide, and no CLI /
HTTP / schema reference; the product's own vocabulary (Variant Spec, `config_hash`, Dimension, verified
delta, refusal-as-`BuildStatus`) appears in the UI with nowhere to look it up. The current answer to "how do
I use this" is *read `internal/`*.

Both are **published-word** surfaces — read rather than computed — whose characteristic failure is not a
crash but **drift**: the written sentence and the running system diverging quietly, discovered by a
customer, an auditor or a regulator. They ship together because they are the same engineering problem: one
content pipeline inside the console's deploy unit, one reading composition that neither existing composition
provides, and one fence set that turns drift into a failed build. Product rationale:
[`../../../docs/prd/P23-legal-and-developer-docs.md`](../../../docs/prd/P23-legal-and-developer-docs.md).

**Boundary.** P23 owns the *engineering* of these surfaces — structure, versioning, acceptance,
availability, generation and the honesty fences. It does **not** own the legal words (authored with
counsel), a DPA / sub-processor portal / certification package, self-serve DSAR automation, localization, or
a docs platform beyond three tiers.

## What Changes

- **New capability `legal-documents`.** A **Terms of Service** and a **Privacy Notice** published as
  versioned artifacts at stable public routes, readable with **no session and no platform call**. Each
  declares `kind`, `version`, `effective_date`, `authoritative_language`, `supersedes` and `material` in
  machine-readable front matter — **a missing field fails the build**. A document's identity is
  `(kind, version, content_hash)` where the hash is computed at build over normalized source and shown on
  the page and in the print footer; **every superseded version stays permanently addressable** (deleting one
  orphans consent records, so deletion **fails the build**). A static `/legal/manifest.json` lists current
  and historical versions with hashes. The Privacy Notice is derived from a **data inventory** naming stores
  that actually exist, and asserts **only rights with an implemented request route**.
- **New capability `consent-records`.** An **append-only** record of `(tenant_id, principal_id,
  document_kind, document_version, content_hash, accepted_at, method)` on a new Postgres table, with
  **idempotency enforced by a unique constraint** rather than by application code, **persist-then-acknowledge**
  writes, and **server-side hash validation** against the manifest. A **material** publication requires
  re-acceptance; a non-material one requires nothing. The gate blocks **new commitments only** — first
  sign-in, checkout, plan change — and **never** blocks reading the console, an in-flight run, or a document
  itself. A failed write **never renders as acceptance**. Tenants read their own history; records survive
  tenant deletion and identity erasure in **pseudonymized** form (the record holds no email, name or free
  text). Acceptance binds to the **ADR-008 principal**, so P22 needs no migration here.
- **New capability `developer-docs`.** Three tiers — **Quickstart** (install → a discovery graph on the
  reader's own repository, **without editing a config file**), **Guides** (task-shaped), **Reference**
  (CLI, HTTP API, schemas, metrics, glossary). **Reference is generated** from shipped artifacts (the
  `internal/cli` command registry; `schemas/*.json`); where the artifact does not exist the tier is
  **rendered as absent with the reason, never hand-written** — the HTTP API reference is ABSENT today
  because the repository contains **no OpenAPI document** (verified). **Anchors are a published contract**:
  a slug manifest, and a rename or removal fails the build unless the same change adds a redirect. Search is
  a **build-time static index served by the console** — no third-party service — degrading to a browsable
  table of contents with JavaScript off. Every page states the platform version it documents and the
  **boundary** of what the capability does not do. Everything renders in an **air-gapped** deploy.
- **New capability `docs-accuracy-fence`.** Seven build-time gates in the idiom of the existing
  `scan-claims.mjs`: **claims** (a docs page may not describe a capability that is not `shipped: true` in
  `CAPABILITIES`), **CLI** (every `heros …` invocation resolves to a real subcommand and flags), **API**
  (every documented endpoint/field resolves against the machine-readable artifact), **metric** (every metric
  definition matches the harness's name, unit and computation, and cites where it is computed), **link**
  (internal links and anchors resolve; external links allow-listed and marked), **secret** (credential-shaped
  content fails the build), **content** (Markdown with no raw HTML, no inline handlers, no external script).
  **Each fence ships with a fixture proving it can fail**, and each states in its own header what it does
  **not** check.
- **New capability `reading-surface`.** A **third composition** beside the dark-fixed marketing poster and
  the console shell: public (no session, no fetch), **theme-following**, bounded measure, TOC as a `nav`
  landmark whose current section is marked by a **word** and not by colour alone. It **scrolls as a
  document** — an explicit, stated exemption from the console's viewport-first rule (NFR17), because a long
  legal text in a bounded inner scroll region is hostile to read and prints as one page of clipped text. It
  ships **no client JavaScript beyond the TOC and search islands**, prints paginated with the document
  identity in the running footer, and meets WCAG 2.2 AA in **both** themes.

**No breaking changes.** No existing route, component, scan script, table or endpoint changes behavior. P23
adds a route group beside the two that exist, one table beside the existing schema, two endpoints, and
fences that run inside the console's existing `npm run build`.

## Impact

- **Affected capabilities:** new — `legal-documents`, `consent-records`, `developer-docs`,
  `docs-accuracy-fence`, `reading-surface`. Consumes (does not modify) the P9 console shell + BFF boundary +
  `CAPABILITIES` manifest, the P11/P20 CLI, the P7 entitlement facts, and ADR-006 / ADR-007 / ADR-008.
- **Affected code / systems:** `web/console/src/app/(reading)/` route group with `/legal/**` and `/docs/**`
  (new); `web/console/content/{legal,docs}/en/**` (new, locale-segmented from day one);
  `web/console/scripts/scan-{docs-claims,cli,api,metric,links,secrets,content}.mjs` (new, wired into
  `npm run build`); `web/console/scripts/gen-{cli-reference,schema-reference,search-index,slug-manifest}.mjs`
  (new); `web/console/tests/{legal,docs}.test.mjs` (new) and extensions to `link-coverage.test.mjs`;
  `internal/api` consent endpoints + `internal/legal` (new); `db/migrations/postgres/00NN_p23_legal_acceptance.{up,down}.sql`
  (next free number — `0016` at time of writing, P21/P22 being docs-only); `docs/adr/ADR-010-*` (new);
  `docs/sales/P23-terms-reconciliation.md` (new).
- **Dependencies:** upstream — P9 (shell, `(public)` composition, BFF credential boundary, scan idiom, stub
  platform harness), P11/P20 (the CLI the quickstart drives and the registry the CLI fence reads), P7 (the
  plan/metering facts the Terms must match), ADR-006/007/008. Soft — P21 (checkout is one commitment moment;
  until it ships the gate covers sign-in and plan change) and P22 (makes the principal real). Unblocks —
  legally complete self-serve signup, enterprise security review without a bespoke questionnaire round-trip,
  deep-linkable CLI/console error messages, and the compliance package (which needs the data inventory this
  phase produces).
- **Explicitly out of scope (owned elsewhere):** the legal text itself (counsel); DPA, sub-processor portal,
  SOC 2 / ISO reports, trust center; self-serve data export/erasure (the notice names a request route and the
  P8 operator runbook); localization (content path is locale-segmented, English is authoritative);
  a blog / changelog / status page; hosted search, analytics or a headless CMS (ADR-010); cookie-consent UI
  (the one session cookie is strictly necessary; a banner arrives with a change, not before it).
