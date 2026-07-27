# Tasks — P23: Legal Surface & Developer Documentation

Ordered by workstream, and by the rollout sequence in PRD §12: **machinery before prose, documents before
the gate, the gate last**. Each task is independently verifiable. Two standing rules for every PR in this
phase:

1. **A fence without a failing fixture is not delivered.** Every scan lands with a fixture that proves it
   goes red, in the same PR.
2. **Every content PR states which fences ran.** A content change that passes because a fence was not wired
   in is a content change nobody checked.

---

## 1. System Designer + Sales Ops — Decide the one-way doors first (blocks everything else)
- [ ] 1.1 Ratify **Decision 2** — a legal document's identity is `(kind, version, content_hash)` and consent
      points at the triple, never at a URL. This is the contract that outlives every other artifact in the
      phase; everything else here is replaceable.
- [ ] 1.2 Ratify **Decision 1** as **ADR-010** (`docs/adr/ADR-010-legal-and-docs-content-as-code.md`):
      content in the console image, no CMS / no runtime fetch / no external docs host — with the accepted
      cost (a deploy per copy fix) written down, not implied.
- [ ] 1.3 Ratify **Decision 3** (materiality is a **declared** field, never a diff heuristic) and **Decision 4**
      (the gate blocks commitments, never reading) — the two decisions that determine whether a legal update
      can take the console down.
- [ ] 1.4 **Escalate, do not self-decide:** governing law / contracting entity / jurisdiction (PRD OQ1) and the
      consent-record **retention period** (OQ2, proposed 7 years). Both are business commitments; record the
      answers, and note that retention is configuration so code need not wait.
- [ ] 1.5 Decide **OQ3** explicitly: emit the HTTP API artifact in this phase, or ship the API reference tier
      **absent with the reason** (Decision 6). Hand-writing it is forbidden either way.
- [ ] 1.6 Reserve the URL shape for future versioned docs (**OQ7**) before any external link exists — cheap
      now, a redirect table later.

## 2. Backend + System Designer — The data inventory (counsel's input, produced by engineering)
- [ ] 2.1 Enumerate every store that holds customer-derived data — eval results and registries (Postgres),
      telemetry and spans (P2.5), delivery records (P12), session state (P9), billing objects (P7/P21) —
      with **data categories, retention, processor and transfer basis** per store.
- [ ] 2.2 Mark each entry **checkable against `db/migrations/postgres/`** or explicitly **external**; an entry
      that is neither is a gap, not a rounding error.
- [ ] 2.3 Commit the inventory to `docs/decisions/p23-data-inventory.md` as the Privacy Notice's source of
      truth, and hand it to counsel as **input** (Decision 10).

## 3. Frontend — The reading surface (machinery first, with placeholder content)
- [ ] 3.1 Add the `(reading)` route group and layout: **no session, no fetch**, theme-following, bounded
      measure, document scroll. Put the **viewport-first exemption** and its reasoning in the layout's own
      header comment, in the codebase's existing idiom — so the next reader sees a decision, not an omission.
- [ ] 3.2 Build the Markdown render pipeline over `web/console/content/{legal,docs}/en/**`: front-matter
      parse, heading-slug emission, code-block rendering, and the `content_hash` computation over
      **normalized** source (front matter excluded; line endings and trailing whitespace normalized).
- [ ] 3.3 TOC component as a `nav` landmark with scroll-spy: current section marked by **`aria-current` and a
      word**, never by colour alone (the console's existing rule, applied to a table of contents).
- [ ] 3.4 Print stylesheet: paginated, chrome dropped, measure expanded, **kind + version + effective date +
      content hash in the running footer**.
- [ ] 3.5 Multi-language code samples via the **existing `<Tabs>` component**; do not add a second one.
- [ ] 3.6 Verify the two client islands (TOC, search) are the only client components and that the
      `scan-bundle` budget is unchanged; confirm the surface is readable with JavaScript disabled.

## 4. Frontend + QA — The fences and the generators (before any real prose lands)
- [ ] 4.1 `scripts/gen-slug-manifest.mjs` — emit `docs/slug-manifest.json` from the **same render pass** that
      produces the pages, so the manifest cannot drift from the page.
- [ ] 4.2 `scripts/gen-cli-reference.mjs` — generate the CLI reference from the `internal/cli` command
      registry (**EXISTS**).
- [ ] 4.3 `scripts/gen-schema-reference.mjs` — generate the schema reference from `schemas/*.json`
      (**EXISTS**).
- [ ] 4.4 `scripts/gen-search-index.mjs` — build-time static index over titles, headings and lead paragraphs;
      **state the ranking limit in the file header** (Decision 9).
- [ ] 4.5 `scripts/scan-docs-claims.mjs` — extend the `scan-claims` rule to `/docs/**`: no page may describe a
      capability that is not `shipped: true` with a named owning phase in `CAPABILITIES`.
- [ ] 4.6 `scripts/scan-cli.mjs` — every `heros …` invocation in content resolves to a real subcommand **and
      real flags**.
- [ ] 4.7 `scripts/scan-api.mjs` — every documented endpoint, method and field resolves against the
      machine-readable API artifact; **refuse the page** when the artifact is absent rather than passing
      vacuously.
- [ ] 4.8 `scripts/scan-metric.mjs` — every documented metric matches the harness on **name, unit and
      computation** and **cites where it is computed**.
- [ ] 4.9 `scripts/scan-links.mjs` — internal links and anchors resolve; a removed/renamed slug fails unless
      the same change adds a redirect; external links allow-listed and visibly marked.
- [ ] 4.10 `scripts/scan-secrets.mjs` — credential-shaped content (provider key prefixes, PEM blocks, bearer
      tokens) fails the build.
- [ ] 4.11 `scripts/scan-content.mjs` — Markdown only: **no raw HTML, no inline handlers, no external
      script/font/stylesheet reference** (this is what makes air-gapped parity a machine check rather than a
      policy).
- [ ] 4.12 Wire all seven scans + four generators into `npm run build`, and add a **failing fixture per
      fence** under `tests/support/` — a fake `heros frobnicate`, an unshipped claim, a dead anchor, a fake
      API key, a raw `<script>`, a mismatched metric unit, an undocumented endpoint. Each must fail
      **individually**.
- [ ] 4.13 Give every fence a header that states **what it does not check** (NFR12), in the idiom
      `scan-claims.mjs` already uses.

## 5. Product Designer + Frontend — Documentation content (tiers 1–2)
- [ ] 5.1 **Quickstart**: install → a discovery graph **on the reader's own repository**, with **no config
      file edit** (matching the P20 first-run contract). One page, one path, **no options** — choices move to
      Guides.
- [ ] 5.2 **Guides**, task-shaped, one job each: configure a variant and read the diff; run an eval and read
      the scorecard; wire CI (P11); take delivery as a pull request (P12); use the console's Studio (P10);
      bring your own provider keys.
- [ ] 5.3 **Glossary** for the product's own nouns — Variant Spec, `config_hash`, Dimension, verified delta,
      refusal-as-`BuildStatus`, unclassified region — so the vocabulary in the UI has somewhere to resolve.
- [ ] 5.4 Document **refusals as first-class outcomes**, not as errors or omissions (AI-engineer lens): a
      developer who first meets a refusal in production reads it as a bug; one who met it in the quickstart
      reads it as the design.
- [ ] 5.5 Every page carries the **platform version it documents** and the **boundary** — what the capability
      deliberately does not do — reusing the `boundary` field the capability manifest already requires.
- [ ] 5.6 Sample outputs are either **captured from a real run and labelled with the version that produced
      them**, or clearly marked illustrative. No model-generated example may be presented as a real run.
- [ ] 5.7 Design the unhappy paths as pages, not defaults: docs 404 offering the section index and search; a
      zero-result search that **says what it searched**.

## 6. Sales Ops + Frontend — Legal content (published read-only, no gate yet)
- [ ] 6.1 Author the **Terms of Service** with counsel; engineering supplies structure, front matter and the
      commercial facts.
- [ ] 6.2 Author the **Privacy Notice** from the §2 data inventory; assert **only rights with an implemented
      route**, name the route, and state the response commitment operators have actually agreed to.
- [ ] 6.3 Front matter on both: `kind`, `version`, `effective_date`, `authoritative_language`, `supersedes`,
      `material` — and make the build fail when any is missing.
- [ ] 6.4 Version-history page + permanent per-version routes `/legal/{kind}/v/{version}`; a superseded page
      **says so, names the current version and links to it — without redirecting**.
- [ ] 6.5 Static `/legal/manifest.json` (kind → versions → `{effective_date, hash, route, material}`),
      resolvable with no session.
- [ ] 6.6 Add a fence: a manifest entry whose document no longer resolves **fails the build** (the
      orphaned-consent one-way door, Decision 2).
- [ ] 6.7 Link legal from the **public footer, sign-in, console shell, account surface and checkout** — every
      place a commitment is made or reviewed.
- [ ] 6.8 **Reconcile the Terms line-by-line against P7 entitlements and (when present) P21 Stripe
      configuration** — plans, metering basis (SUM), gainshare/verified-savings basis, cancellation, refunds,
      any SLA language. Record it in `docs/sales/P23-terms-reconciliation.md`. A refund term Stripe cannot
      execute is a promise software will break.
- [ ] 6.9 Assert **no** SLA, certification or sub-processor claim appears anywhere until it exists.

## 7. Backend — Consent records
- [ ] 7.1 Migration `00NN_p23_legal_acceptance.{up,down}.sql` (next free number — `0016` at time of writing):
      the table in `design.md` Decision 5, **expand-only**, with `unique (tenant_id, principal_id,
      document_kind, document_version)` — **idempotency in the schema, not in application code**.
- [ ] 7.2 `internal/legal`: the manifest reader and **server-side `content_hash` validation**. A client that
      submits a hash for a version it was not shown is rejected; without this the record says whatever the
      browser said.
- [ ] 7.3 `POST /v1/legal/acceptances` — **persist-then-acknowledge**; the 201 is written after commit, never
      before. A repeat of the same triple returns success and creates no second row.
- [ ] 7.4 `GET /v1/legal/acceptances` — the caller's **own tenant only**, plus `pending[]` (the kinds needing
      acceptance). No cross-tenant read exists on this path at all.
- [ ] 7.5 Bind the record to the **ADR-008 principal**, not to an email or an IdP subject, so P22 requires no
      migration here.
- [ ] 7.6 Supersession: publishing a **material** version sets `superseded_by` on prior acceptances; a
      **non-material** publication changes nothing.
- [ ] 7.7 Retention job for the configured statutory window, **runnable dry**. A deletion job whose first
      production run is also its first run ever is a defect waiting for a quiet weekend.
- [ ] 7.8 Erasure path: tombstone the subject, **keep the evidentiary row** (document version, hash,
      timestamp). Assert the row holds no email, no name and no free text.

## 8. Frontend + Product — The gate and the account surface (last, smallest, most reversible)
- [ ] 8.1 Acceptance history on `/app/account`: document, version, date, principal — each entry linking to
      **the exact archived text that was accepted**.
- [ ] 8.2 The commitment gate at **first sign-in / checkout / plan change**, behind a flag, **new principals
      first**.
- [ ] 8.3 The non-blocking notice for existing sessions: names the document and the effective date, offers
      "Read it" and "Accept", and **the console keeps working**.
- [ ] 8.4 Failed-write behavior: the button returns to rest with a plain sentence — *the acceptance was not
      recorded; nothing has been agreed* — and a retry. **No optimistic checkmark, ever.**
- [ ] 8.5 Assert by test that consent **never** blocks reading the console, an in-flight run, or a legal
      document itself.

## 9. DevOps — Deploy, observability, air-gapped parity
- [ ] 9.1 Content ships in the **console container** (ADR-006); confirm a bad copy change is reverted by
      redeploying the previous console image — no migration, no platform restart.
- [ ] 9.2 Expose the live document versions and hashes from the running deployment (the static manifest plus
      the console's version surface) so "which text is live on this cluster" is a `curl`, not an
      investigation.
- [ ] 9.3 Verify **air-gapped parity**: docs and legal byte-identical in the P19 air-gapped package and the
      hosted deploy; zero external requests, enforced by `scan-content` rather than by policy.
- [ ] 9.4 Confirm the consent endpoints are the **only** new authenticated surface, accept exactly three
      fields, and read only the caller's own tenant; operator-side access to consent records stays in the P8
      console behind its existing RBAC + append-only audit.

## 10. QA — The acceptance gate that can actually fail
- [ ] 10.1 **Availability (NFR1):** stop the platform stub; every legal and docs route still returns 200 and
      the harness's upstream-request counter **does not move** (the assertion `routes.test.mjs` already knows
      how to make).
- [ ] 10.2 **Legal identity:** changing a document body without bumping the version **fails the build**;
      deleting an archived version **fails the build**.
- [ ] 10.3 **Consent behavior:** double-submit → one row (proven against **real Postgres**, `pgproof`-style —
      an idempotency guarantee asserted only against an in-memory fake is not asserted); **material**
      publication → an existing principal is asked again; **non-material** → they are not; a forced write
      failure → no acceptance rendered and the commitment does not proceed.
- [ ] 10.4 **Fences:** each of the seven fixtures fails the build individually (§4.12).
- [ ] 10.5 **Reachability (FR21/FR22):** extend `link-coverage.test.mjs` — every docs page reachable by
      navigation, every anchor referenced from CLI or console output resolves.
- [ ] 10.6 **A11y and print:** WCAG 2.2 AA in **both** themes via the existing design-system test; the print
      stylesheet asserted to emit the document identity.
- [ ] 10.7 **The human end-to-end, recorded as evidence:** one reviewer on a clean machine follows the
      published quickstart start to finish **without reading source or asking a question**; one reviewer reads
      and **prints** both legal documents as a buyer would. A green suite over documentation nobody has read
      end to end is the exact failure this phase exists to prevent.

## 11. Close-out
- [ ] 11.1 Add the P23 row to `docs/prd/README.md` and to the ownership matrix in
      `docs/implementation-timeline/roles-and-ownership.md`.
- [ ] 11.2 Record the §13 acceptance checklist outcomes; fold these delta specs into `openspec/specs/`.
- [ ] 11.3 File the residue: anything a fence cannot check (tone, emphasis, omission) becomes a named review
      responsibility in the docs contributor guide — not an implied guarantee.
