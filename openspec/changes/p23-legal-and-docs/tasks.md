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
- [x] 1.1 Ratify **Decision 2** — a legal document's identity is `(kind, version, content_hash)` and consent
      points at the triple, never at a URL. This is the contract that outlives every other artifact in the
      phase; everything else here is replaceable.
- [x] 1.2 Ratify **Decision 1** as **ADR-010** (`docs/adr/ADR-010-legal-and-docs-content-as-code.md`):
      content in the console image, no CMS / no runtime fetch / no external docs host — with the accepted
      cost (a deploy per copy fix) written down, not implied.
- [x] 1.3 Ratify **Decision 3** (materiality is a **declared** field, never a diff heuristic) and **Decision 4**
      (the gate blocks commitments, never reading) — the two decisions that determine whether a legal update
      can take the console down.
- [x] 1.4 **Escalate, do not self-decide:** governing law / contracting entity / jurisdiction (PRD OQ1) and the
      consent-record **retention period** (OQ2, proposed 7 years). Both are business commitments; record the
      answers, and note that retention is configuration so code need not wait.
- [x] 1.5 Decide **OQ3** explicitly: emit the HTTP API artifact in this phase, or ship the API reference tier
      **absent with the reason** (Decision 6). Hand-writing it is forbidden either way.
- [x] 1.6 Reserve the URL shape for future versioned docs (**OQ7**) before any external link exists — cheap
      now, a redirect table later.

## 2. Backend + System Designer — The data inventory (counsel's input, produced by engineering)
- [x] 2.1 Enumerate every store that holds customer-derived data — eval results and registries (Postgres),
      telemetry and spans (P2.5), delivery records (P12), session state (P9), billing objects (P7/P21) —
      with **data categories, retention, processor and transfer basis** per store.
- [x] 2.2 Mark each entry **checkable against `db/migrations/postgres/`** or explicitly **external**; an entry
      that is neither is a gap, not a rounding error.
- [x] 2.3 Commit the inventory to `docs/decisions/p23-data-inventory.md` as the Privacy Notice's source of
      truth, and hand it to counsel as **input** (Decision 10).

## 3. Frontend — The reading surface (machinery first, with placeholder content)
- [x] 3.1 Add the `(reading)` route group and layout: **no session, no fetch**, theme-following, bounded
      measure, document scroll. Put the **viewport-first exemption** and its reasoning in the layout's own
      header comment, in the codebase's existing idiom — so the next reader sees a decision, not an omission.
- [x] 3.2 Build the Markdown render pipeline over `web/console/content/{legal,docs}/en/**`: front-matter
      parse, heading-slug emission, code-block rendering, and the `content_hash` computation over
      **normalized** source (front matter excluded; line endings and trailing whitespace normalized).
- [x] 3.3 TOC component as a `nav` landmark with scroll-spy: current section marked by **`aria-current` and a
      word**, never by colour alone (the console's existing rule, applied to a table of contents).
- [x] 3.4 Print stylesheet: paginated, chrome dropped, measure expanded, **kind + version + effective date +
      content hash in the running footer**.
- [x] 3.5 Multi-language code samples via the **existing `<Tabs>` component**; do not add a second one.
- [x] 3.6 Verify the two client islands (TOC, search) are the only client components and that the
      `scan-bundle` budget is unchanged; confirm the surface is readable with JavaScript disabled.

## 4. Frontend + QA — The fences and the generators (before any real prose lands)
- [x] 4.1 `scripts/gen-slug-manifest.mjs` — emit `docs/slug-manifest.json` from the **same render pass** that
      produces the pages, so the manifest cannot drift from the page.
- [x] 4.2 `scripts/gen-cli-reference.mjs` — generate the CLI reference from the `internal/cli` command
      registry (**EXISTS**).
- [x] 4.3 `scripts/gen-schema-reference.mjs` — generate the schema reference from `schemas/*.json`
      (**EXISTS**).
- [x] 4.4 `scripts/gen-search-index.mjs` — build-time static index over titles, headings and lead paragraphs;
      **state the ranking limit in the file header** (Decision 9).
- [x] 4.5 `scripts/gen-release-assets.mjs` — generate the install page's asset table (filenames, targets,
      versions, checksums) **from the published release**. Until a release exists it emits nothing and the
      install page renders the not-yet-available statement (Decision 12).
- [x] 4.6 `scripts/scan-docs-claims.mjs` — extend the `scan-claims` rule to `/docs/**`: no page may describe a
      capability that is not `shipped: true` with a named owning phase in `CAPABILITIES`.
- [x] 4.7 `scripts/scan-cli.mjs` — **both directions**: every `heros …` invocation in content resolves to a
      real subcommand **and real flags**; **and** every subcommand in the registry has a reference entry. Plus
      exit-code parity — a documented code whose meaning disagrees with `internal/cli` fails (Decision 14).
- [x] 4.8 `scripts/scan-api.mjs` — every documented endpoint, method and field resolves against the
      machine-readable API artifact; **refuse the page** when the artifact is absent rather than passing
      vacuously.
- [x] 4.9 `scripts/scan-metric.mjs` — every documented metric matches the harness on **name, unit and
      computation** and **cites where it is computed**.
- [x] 4.10 `scripts/scan-links.mjs` — internal links and anchors resolve; a removed/renamed slug fails unless
      the same change adds a redirect; external links allow-listed and visibly marked.
- [x] 4.11 `scripts/scan-secrets.mjs` — credential-shaped content (provider key prefixes, PEM blocks, bearer
      tokens) fails the build.
- [x] 4.12 `scripts/scan-content.mjs` — Markdown only: **no raw HTML, no inline handlers, no external
      script/font/stylesheet reference**; and **no third-party origin in public-surface markup** (badge,
      widget, hosted font, cross-origin fetch). The runtime CSP in `middleware.ts` refuses these anyway —
      this makes the refusal visible at review rather than at deploy, and keeps air-gapped parity a machine
      check rather than a policy.
- [x] 4.13 `scripts/scan-install.mjs` — a hand-typed asset filename, version or **checksum** fails; a
      documented install path that reaches `PATH` **before** verification fails; a signing/notarization claim
      naming a step the pipeline does not perform fails; an install channel absent from the published release
      fails (Decisions 12 + 13).
- [x] 4.14 Wire all eight scans + five generators into `npm run build`, and add a **failing fixture per
      fence** under `tests/support/` — a fake `heros frobnicate`, a registry subcommand with no reference
      entry, an unshipped claim, a dead anchor, a fake API key, a raw `<script>`, a mismatched metric unit, an
      undocumented endpoint, a hand-typed checksum, and an install path that reaches `PATH` before verifying.
      Each must fail **individually**.
- [x] 4.15 Give every fence a header that states **what it does not check** (NFR12), in the idiom
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

## 6. Backend + DevOps + Product — CLI reference and installation content
- [ ] 6.1 Generate the **CLI reference** for every subcommand in the registry — today `help`, `version`,
      `discover`, `apply`, `eval`, `status`, `login`, `link` — and make a registry entry with no reference
      entry **fail the build** (§4.7). Adding a subcommand is a normal Tuesday; remembering the docs is not.
- [ ] 6.2 Document the **exit-code contract as a contract**: `0` success, `1` a gate the customer configured
      failed, `2` the tool broke, `3` invalid invocation — each with its **remedy**, sourced from
      `internal/cli/exit.go` and `docs/decisions/p11-contracts.md`. The 1-vs-2 gap is load-bearing: opposite
      remedies, and a CI step that fails for an unclear reason gets disabled.
- [ ] 6.3 State per command whether it runs **offline with no account**; for `login` and `link`, document the
      **"unavailable in this build"** outcome rather than leaving it to be met at the terminal.
- [ ] 6.4 Document every flag with type, default, environment equivalent and **which wins** when both are
      set; mark deprecations with their replacement and expected removal release, **before** removal.
- [ ] 6.5 Give each command entry a **runnable invocation**, what success looks like, and the success exit
      code.
- [ ] 6.6 Author the **install page** for macOS, Linux and Windows. **The shortest path on the page is the
      verified path**: checksum **and** signature checked before the binary reaches `PATH`. Do not publish
      any path that installs first and verifies later (Decision 13) — that is the path everyone copies.
- [ ] 6.7 Generate the **release-asset table** (§4.5) from the published release. **No hand-typed checksum,
      filename or version** — a routinely-wrong checksum is how readers learn to skip verification.
- [ ] 6.8 Gate channels on existence (§4.13): document a channel **only once the pipeline publishes it**.
      Until then, document what does exist — build from source, `scripts/release-cli.sh`, `SHA256SUMS`,
      `herossign`, `docs/release/cli-verification.md` — and **say packaged channels are not yet available**.
- [ ] 6.9 State the **OS-trust posture per platform**: signed/notarized only where the pipeline does it;
      otherwise "unsigned", with the exact quarantine-clear command and what accepting it means. Warn the
      reader about the Gatekeeper/SmartScreen dialog **before** they meet it.
- [ ] 6.10 Document **pinned-version install** on every channel (an unpinnable install is an unreproducible
      build image), and **upgrade + uninstall in each channel's own idiom**, including deferring to the
      package manager and naming any configuration or cache left behind.
- [ ] 6.11 Document the **offline / air-gapped install**: transferred asset, checksum manifest, signature and
      public key, with verification performed on the disconnected machine and **no step needing the internet
      or an account**.
- [ ] 6.12 End the install page by **naming the quickstart's first command**, with no config-file edit
      between installing and a first discovery graph.

## 7. Frontend + Product + Sales Ops — The GitHub link on the home page
- [ ] 7.1 Add the **repository link** to the public header (beside "Sign in") and the footer, marked as an
      external destination. It is an anchor: **no request until clicked** — no client component, no effect,
      no loading state (Decision 15).
- [ ] 7.2 Fence the link target: a **private or non-existent** repository **fails the build**, under the
      same rule that forbids an install command that 404s. (Verified today: the repository is **public**.)
- [ ] 7.3 If a star count is wanted, capture it **during the build** and render it as a server-side string
      **with its measurement date**. Do **not** add a shields.io image, a GitHub buttons widget, or a
      browser-side `api.github.com` call — the CSP (`default-src 'self'`, `connect-src 'self'`,
      `img-src 'self' data:`) refuses all three, and this is not the feature that gets an exception.
- [ ] 7.4 Make an **unavailable measurement degrade to the plain link** — never `0`, never a placeholder,
      never a broken badge. An unavailable measurement rendered as zero is a false statement, and the one a
      reader will believe.
- [ ] 7.5 Make the count **opt-in configuration, default off**; keep the link unconditional. **Escalate the
      display decision** rather than defaulting it: the repository has **0 stars** today, and "★ 0" on the
      marketing home page is worse than nothing.
- [ ] 7.6 Assert the page's existing posture is unchanged: no cookie, no third-party request at render, and
      the CSP untouched.

## 8. Sales Ops + Frontend — Legal content (published read-only, no gate yet)
- [ ] 8.1 Author the **Terms of Service** with counsel; engineering supplies structure, front matter and the
      commercial facts.
- [ ] 8.2 Author the **Privacy Notice** from the §2 data inventory; assert **only rights with an implemented
      route**, name the route, and state the response commitment operators have actually agreed to.
- [ ] 8.3 Front matter on both: `kind`, `version`, `effective_date`, `authoritative_language`, `supersedes`,
      `material` — and make the build fail when any is missing.
- [ ] 8.4 Version-history page + permanent per-version routes `/legal/{kind}/v/{version}`; a superseded page
      **says so, names the current version and links to it — without redirecting**.
- [ ] 8.5 Static `/legal/manifest.json` (kind → versions → `{effective_date, hash, route, material}`),
      resolvable with no session.
- [ ] 8.6 Add a fence: a manifest entry whose document no longer resolves **fails the build** (the
      orphaned-consent one-way door, Decision 2).
- [ ] 8.7 Link legal from the **public footer, sign-in, console shell, account surface and checkout** — every
      place a commitment is made or reviewed.
- [ ] 8.8 **Reconcile the Terms line-by-line against P7 entitlements and (when present) P21 Stripe
      configuration** — plans, metering basis (SUM), gainshare/verified-savings basis, cancellation, refunds,
      any SLA language. Record it in `docs/sales/P23-terms-reconciliation.md`. A refund term Stripe cannot
      execute is a promise software will break.
- [ ] 8.9 Assert **no** SLA, certification or sub-processor claim appears anywhere until it exists.

## 9. Backend — Consent records
- [ ] 9.1 Migration `00NN_p23_legal_acceptance.{up,down}.sql` (next free number — `0016` at time of writing):
      the table in `design.md` Decision 5, **expand-only**, with `unique (tenant_id, principal_id,
      document_kind, document_version)` — **idempotency in the schema, not in application code**.
- [ ] 9.2 `internal/legal`: the manifest reader and **server-side `content_hash` validation**. A client that
      submits a hash for a version it was not shown is rejected; without this the record says whatever the
      browser said.
- [ ] 9.3 `POST /v1/legal/acceptances` — **persist-then-acknowledge**; the 201 is written after commit, never
      before. A repeat of the same triple returns success and creates no second row.
- [ ] 9.4 `GET /v1/legal/acceptances` — the caller's **own tenant only**, plus `pending[]` (the kinds needing
      acceptance). No cross-tenant read exists on this path at all.
- [ ] 9.5 Bind the record to the **ADR-008 principal**, not to an email or an IdP subject, so P22 requires no
      migration here.
- [ ] 9.6 Supersession: publishing a **material** version sets `superseded_by` on prior acceptances; a
      **non-material** publication changes nothing.
- [ ] 9.7 Retention job for the configured statutory window, **runnable dry**. A deletion job whose first
      production run is also its first run ever is a defect waiting for a quiet weekend.
- [ ] 9.8 Erasure path: tombstone the subject, **keep the evidentiary row** (document version, hash,
      timestamp). Assert the row holds no email, no name and no free text.

## 10. Frontend + Product — The gate and the account surface (last, smallest, most reversible)
- [ ] 10.1 Acceptance history on `/app/account`: document, version, date, principal — each entry linking to
      **the exact archived text that was accepted**.
- [ ] 10.2 The commitment gate at **first sign-in / checkout / plan change**, behind a flag, **new principals
      first**.
- [ ] 10.3 The non-blocking notice for existing sessions: names the document and the effective date, offers
      "Read it" and "Accept", and **the console keeps working**.
- [ ] 10.4 Failed-write behavior: the button returns to rest with a plain sentence — *the acceptance was not
      recorded; nothing has been agreed* — and a retry. **No optimistic checkmark, ever.**
- [ ] 10.5 Assert by test that consent **never** blocks reading the console, an in-flight run, or a legal
      document itself.

## 11. DevOps — Deploy, observability, air-gapped parity
- [ ] 11.1 Content ships in the **console container** (ADR-006); confirm a bad copy change is reverted by
      redeploying the previous console image — no migration, no platform restart.
- [ ] 11.2 Expose the live document versions and hashes from the running deployment (the static manifest plus
      the console's version surface) so "which text is live on this cluster" is a `curl`, not an
      investigation.
- [ ] 11.3 Verify **air-gapped parity**: docs and legal byte-identical in the P19 air-gapped package and the
      hosted deploy; zero external requests, enforced by `scan-content` rather than by policy.
- [ ] 11.4 Confirm the consent endpoints are the **only** new authenticated surface, accept exactly three
      fields, and read only the caller's own tenant; operator-side access to consent records stays in the P8
      console behind its existing RBAC + append-only audit.

## 12. QA — The acceptance gate that can actually fail
- [ ] 12.1 **Availability (NFR1):** stop the platform stub; every legal and docs route still returns 200 and
      the harness's upstream-request counter **does not move** (the assertion `routes.test.mjs` already knows
      how to make).
- [ ] 12.2 **Legal identity:** changing a document body without bumping the version **fails the build**;
      deleting an archived version **fails the build**.
- [ ] 12.3 **Consent behavior:** double-submit → one row (proven against **real Postgres**, `pgproof`-style —
      an idempotency guarantee asserted only against an in-memory fake is not asserted); **material**
      publication → an existing principal is asked again; **non-material** → they are not; a forced write
      failure → no acceptance rendered and the commitment does not proceed.
- [ ] 12.4 **Fences:** each of the eight fixtures fails the build individually (§4.14).
- [ ] 12.5 **CLI coverage:** add a subcommand to the registry with no reference entry → the build fails.
      Change an exit code's meaning without changing the reference → the build fails.
- [ ] 12.6 **Install honesty:** document a channel the pipeline does not publish → the build fails. Hand-type
      a checksum → the build fails. A documented path that reaches `PATH` before verifying → refused at
      review, with a named rule to cite rather than an opinion.
- [ ] 12.7 **Home-page social proof:** add a shields.io image or an `api.github.com` fetch → the build fails
      on the external-origin check (and the runtime CSP refuses it independently — assert both). Hand-type a
      count → the build fails. Force the measurement to fail → the page renders the plain link, never `0`.
      Point the link at a private repository → the build fails.
- [ ] 12.8 **Reachability (FR21/FR22):** extend `link-coverage.test.mjs` — every docs page reachable by
      navigation, every anchor referenced from CLI or console output resolves.
- [ ] 12.9 **A11y and print:** WCAG 2.2 AA in **both** themes via the existing design-system test; the print
      stylesheet asserted to emit the document identity.
- [ ] 12.10 **The human end-to-end, recorded as evidence:** one reviewer on a clean machine follows the
      published **install page and quickstart** start to finish **without reading source or asking a question**, verification included; one reviewer reads
      and **prints** both legal documents as a buyer would. A green suite over documentation nobody has read
      end to end is the exact failure this phase exists to prevent.

## 13. Close-out
- [ ] 13.1 Add the P23 row to `docs/prd/README.md` and to the ownership matrix in
      `docs/implementation-timeline/roles-and-ownership.md`.
- [ ] 13.2 Record the §13 acceptance checklist outcomes; fold these delta specs into `openspec/specs/`.
- [ ] 13.3 File the residue: anything a fence cannot check (tone, emphasis, omission) becomes a named review
      responsibility in the docs contributor guide — not an implied guarantee.
