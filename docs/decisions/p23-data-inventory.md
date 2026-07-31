# P23 — Data inventory

**Status:** produced by engineering, 2026-07-31. **This is counsel's *input*, not counsel's output.**

Closes **tasks 2.1–2.3**. It is the Privacy Notice's source of truth (Decision 10): the notice describes
**this** system rather than a plausible one, and every claim in it traces to a row here.

**The rule this document is written under:** an entry is either **checkable** — you can open
`db/migrations/postgres/` (or a named source file) and see the store — or explicitly **external**. An
entry that is neither is a **gap**, and gaps are listed in [§6](#6-gaps--named-not-rounded-off) with a
name, not smoothed into a paragraph. A privacy notice built on a rounded-off inventory is a document that
describes a system nobody has.

**Contracting entity:** PLUTUX TECHNOLOGY LLC (Nevada, US) — see
[`p23-one-way-doors.md` §1.4](p23-one-way-doors.md#oq1--governing-law-contracting-entity-jurisdiction--answered).

---

## 0. The fact that shapes every row below

**One deployment serves one tenant boundary.** Isolation between customers is *deployment-level*, not
shared multi-tenancy — this is the shipped `deploy` capability's own stated boundary
(`web/console/src/content/capabilities.ts`, id `deploy`), and it is why most rows below have no
`tenant_id` column: the deployment *is* the tenant scope.

**Verified 2026-07-31: there is no vendor-operated hosted production deployment.** `deploy/` ships
substrate — Docker Compose files, Kustomize overlays, `images.env` — and names no region, no vendor
account and no hosted environment. Every deployment that exists today is **customer- or operator-run**,
including the P19 air-gapped package.

Two consequences, and they are the load-bearing ones for the Privacy Notice:

1. For a customer-run deployment, **PLUTUX TECHNOLOGY LLC is not a processor of the data in §1–§3** — the
   data never reaches it. The processor is the customer's own infrastructure.
2. The **transfer basis** column is therefore mostly *"no transfer occurs"* today. The day a hosted
   deployment exists, **that is the row that changes**, and it changes into a live legal obligation rather
   than a configuration change. It is called out here so the change is noticed when it happens instead of
   being discovered afterwards.

---

## 1. Postgres — the platform database

**Checkable:** `db/migrations/postgres/0001…0018` — **61 tables**. Everything in this section can be read
off the migration chain; nothing here is asserted from memory.

**Processor:** the operator of the deployment. **Transfer basis:** none — data does not leave the
deployment.

### 1.1 Lineage, discovery and configuration — `0001`, `0002`, `0003`, `0004`

| Table | Data category | Customer-derived? |
|---|---|---|
| `workflow` | repository URL, commit SHA, language, IR version | **yes** — names the customer's repository |
| `variant`, `config`, `node` | configuration identity, lineage JSON, node kinds | **yes** — structure of the customer's code |
| `eval_case` | evaluation case identifiers and suite names | **yes** |
| `blob` | content-hash, size, media type — **the bytes live in the object store, §2** | pointer only |
| `model_entry`, `prompt_entry`, `skill_entry`, `context_entry` | registry envelopes; prompt **bodies** are `body_blob_hash` references | **yes** — prompt text is customer content |
| `variant_spec`, `transform` | the Variant Spec JSON, build status, worktree ref, branch/commit, **`build_log`** | **yes** |
| `schema_migrations` | migration bookkeeping | no |

🔴 **`transform.build_log` is free text produced by the customer's own toolchain.** It is the one column in
this group that can carry arbitrary content — a compiler error quoting a source line, a stack trace. It is
not scrubbed. Named in [§6](#6-gaps--named-not-rounded-off).

**Retention:** none — no job deletes from any of these tables. See §5.

### 1.2 Runs and execution — `0005`, `0006`, `0011`

| Table | Data category |
|---|---|
| `run`, `run_queue`, `node_execution` | run lifecycle, per-node execution records, timings, costs |
| `reconciliation`, `recon_node`, `recon_call`, `recon_edge` | static-vs-runtime reconciliation; **`inputs_blob_hash`, `stack_blob_hash`** point at the object store |
| `inserted_adapter` | adapter nodes and their I/O-contract hashes |
| `behavioral_label`, `anti_pattern` | pattern labels and `evidence_blob_hash` references |

**Customer-derived: yes.** Execution records describe what the customer's workflow did.
**Retention:** none enforced. See §5.

### 1.3 Evaluation, attribution and proposals — `0008`–`0012`

`eval_set`, `eval_result`, `eval_run`, `metric_stat`, `metric_seed_value`, `score_cache`,
`coverage_item`, `weight_profile`, `gate_set`, `judge_calibration`, `judge_human_label`, `attribution`,
`failure_cluster`, `ablation_result`, `bottleneck_flag`, `analyst_cal`, `diagnosis`, `proposal`,
`proposal_evidence`, `verdict`, `rank_entry`.

| Data category | Note |
|---|---|
| Scores, confidence intervals, gate verdicts, cost/latency deltas | **customer-derived** — measurements of the customer's workflow |
| `judge_human_label.labeler` | **an identifier of a person who labelled a case.** The one field in this group that is plausibly personal data |
| `failure_cluster.embedding_ref`, `*_blob_hash` | pointers into the object store, §2 |

**Retention:** none enforced. See §5.

### 1.4 Billing and metering — `0013` (P7 / P21)

| Table | Data category | Note |
|---|---|---|
| `account` | `customer_id`, **`provider_customer_handle`** (opaque Stripe handle), active plan, `gainshare_consent`, `consented_at` | 🔒 A `CHECK` constraint **rejects the PAN family outright** — a 12–19 digit run cannot be stored, so a mis-wired integration fails at the database rather than putting the platform in PCI scope. **No card data exists in this system by construction.** |
| `usage_record` | `{customer, period, metric}` quantities | the primary key *is* the never-double-count guarantee |
| `billable_savings` | gainshare lines with verified-delta refs and merge commits | |
| `billing_event`, `webhook_delivery` | provider event ids and processing timestamps | idempotency ledger for Stripe webhooks |

**Customer-derived: yes** (commercial, not behavioural). **Processor for the payment leg: Stripe — see §4.**
**Retention:** none enforced locally; Stripe's own retention governs the objects it holds.

### 1.5 Operator console — `0014` (P8)

| Table | Data category | Note |
|---|---|---|
| `admin_principal` | `admin_id`, **`sso_subject`**, MFA enrolment, status | `sso_subject` is the customer IdP's opaque subject — **not an email**. See §4 |
| `admin_session` | session id, issue/expiry/revocation, MFA factor | operator sessions, not customer sessions |
| `admin_role_grant`, `permission` | RBAC grants and the deny-by-default permission map | `admin_role_grant` is **append-only, enforced by a trigger** |
| `audit_entry` | the append-only audit chain | **write-once at the store**: a trigger refuses `UPDATE`/`DELETE` **for every role, including Superadmin** |
| `impersonation_session` | actor admin, `tenant_id`, reason, scope, window | operator access to a tenant, recorded |
| `kill_switch_state` | armed/disarmed, who, why | |
| `gdpr_request` | `subject_ref`, status, actor, reason, `verification_ref`, **`tombstone_ref`**, `removed_count` | **the erasure path that already exists.** `tombstone_ref` is a non-PII reference kept in the audit chain so **no audit entry is removed on erasure** |

🔴 **`gdpr_request.reason` is free text written by an operator.** Named in
[§6](#6-gaps--named-not-rounded-off).

**Retention:** the audit chain is **deliberately permanent** — that is its purpose, and it is why erasure
tombstones rather than deletes.

### 1.6 Delivery, authored change, and the axis registries — `0015`–`0018`

| Table | Data category | Note |
|---|---|---|
| `delivery` (P12) | `tenant_id`, `config_hash`, `source_revision`, `target` (`owner/repo`), **`forge_ref`** (`owner/repo#42`), mode, state | **customer-derived and externally correlatable** — it names a public or private repository and a specific pull request |
| `authored_change` (P13) | `tenant_id`, **`actor_id`**, workflow, axis, `diff_ref`, origin | `actor_id` identifies **who authored a change** |
| `memory_entry` (P17), `harness_entry` (P18) | registry envelopes | |

**Retention:** none enforced. See §5.

### 1.7 New in this phase — `legal_acceptance` (§9.1)

| Column | Data category |
|---|---|
| `tenant_id`, `principal_id` | **opaque** (ADR-008). `principal_id` is **never an email, never a name** |
| `document_kind`, `document_version`, `content_hash` | the document identity triple |
| `accepted_at`, `method`, `superseded_by` | when, how, and whether superseded |

**Data minimisation is the design (NFR9):** no email, no name, no free text, no IP address, no user agent.
This is what makes erasure a **tombstone of the subject** rather than a rewrite of the evidence — decided
now rather than during the first erasure request.

**Retention: 7 years** (OQ2, answered). ⚠️ **This is the only store in this document with a configured
retention window and a job to execute it** (§9.7). Everything else in §1 has neither.

---

## 2. The object store — content-hashed blobs

**Checkable:** referenced from `blob.content_hash` (`0001`) and from every `*_blob_hash` column above;
written through `internal/db`.

| | |
|---|---|
| **Data categories** | 🔴 **prompt text, completion text, diffs, discovery reports, reconciliation reports, failure-cluster evidence, stack traces.** This is the highest-sensitivity store the platform has — it holds the customer's actual model inputs and outputs |
| **Why it is separate** | telemetry carries only a `blobref:<hash>` pointer (`internal/telemetry/scrub.go`), so prompt and completion text **never reach a span, a label or a log** |
| **Processor** | the operator of the deployment |
| **Transfer basis** | none — the object store is deployment-local |
| **Retention** | **none enforced.** See §5 and §6 |

---

## 3. Telemetry — spans and metrics (P2.5)

**Checkable:** `internal/telemetry/stores.go` declares the three-stores-by-shape contract; the Postgres
eval store is `internal/telemetry/evalstore_pg.go` and is proved against a live database.

| Store | Backend | Data categories | Retention |
|---|---|---|---|
| Span store | Tempo / Jaeger (**operator-run**) | OTel spans: run → node → tool hierarchy, timings, the seven tags, `blobref:` pointers | `MemSpanStore` takes a **retention duration and evicts**; a real backend's retention is the operator's configuration |
| TSDB | Prometheus / ClickHouse (**operator-run**) | metric series over low-cardinality labels | the operator's configuration |
| Eval store | Postgres — **§1.3 above** | per-variant/node/case rows | none enforced |

**What is structurally guaranteed:** every event and span passes a single scrubber
(`internal/telemetry/scrub.go`) that strips secrets, API keys, **prompt text, completion text and PII**,
replacing substantial content with a `blobref:` pointer. It runs at **one chokepoint**, on every event and
span — so "secrets never touch a span, a label or a log" is enforced rather than trusted to each emitter.

**Processor:** the operator. **Transfer basis:** none — unless the operator points an OTLP exporter at a
third party, which is their configuration and their disclosure to make. Named in §6.

---

## 4. External systems — explicitly external, none of them ours to inventory

Each of these is a **third party the customer or operator brings**. The rows state what leaves the
deployment, and nothing more.

| System | What leaves | What we store locally | Transfer basis |
|---|---|---|---|
| **Stripe** (P21) | customer/billing identifiers, subscription and usage quantities | an **opaque** `provider_customer_handle` and event ids only — **never card data** (enforced by a `CHECK`) | the operator's contract with Stripe. Card data is handled **entirely by Stripe**; it never enters this system |
| **The customer's IdP** (P22 — OIDC / SAML, e.g. Okta) | an authentication request | **`sso_subject`** — the IdP's opaque subject. **No email, no name** | the customer's own IdP; the customer is both source and controller |
| **The forge** (P12 — GitHub etc.) | in the **default CI-mediated path, nothing** — the customer's own CI opens the pull request, and **the platform holds no repository token** | `delivery.forge_ref` (`owner/repo#42`) — a citation of the customer's own pull request | the customer's forge account. The hosted Git App is **opt-in and off by default** |
| **LLM providers** | prompts and completions, **on the customer's own provider keys** | the object store's blobs, §2 | the customer's own provider contracts. Keys come from a secrets manager and are **never in repo, logs, CI echo or traces** (`docs/decisions/secrets-baseline.md`) |

**None of these are sub-processors of PLUTUX TECHNOLOGY LLC today**, because there is no hosted deployment
for them to be sub-processors *of* (§0). 🔴 **The Privacy Notice must not name a sub-processor list until
that is false** (§8.9), and the Terms must not name an SLA or a certification until one exists.

---

## 5. The session store (P9) — and the retention picture, stated plainly

**Console sessions** (`web/console/src/lib/session.ts`) live in a **process-local map**. They hold
`{ id, tenantId, issuedAt, expiresAt, revokedAt }` and **do not hold the assertion that produced them**,
and **do not hold a user** — P9 has no concept of one, because the platform could not prove one (ADR-008).

| | |
|---|---|
| **Retention** | **8-hour TTL, and a console restart ends every session.** This is the one store in this document whose retention is both short and actually enforced |
| **Browser side** | one `httpOnly`, `SameSite=Lax` session cookie holding an **opaque token the page script cannot read**. It is strictly necessary, which is why there is no cookie banner |
| **Transfer basis** | none — the session never leaves the console process |

### The retention finding, which the Privacy Notice must not soften

**`retention_days` is a declared plan allowance, not an enforced deletion.** It exists in
`internal/plancfg` (`LimitRetentionDays`, values 7 / 30 / 90 / 365 by plan) and is read by
`internal/entitlement` **only to produce a human label** — `limitLabel` returns the string `"retention"`.
**Verified 2026-07-31: no job anywhere in `internal/` deletes data on the basis of it.**

So the honest statement is:

> Apart from console sessions (8 hours), telemetry backends configured by the operator, and the new
> consent records (7 years), **no store in this system has an enforced retention period today.** Data
> persists until the operator removes it, or until an erasure request runs through the P8 path.

A Privacy Notice that states a retention period for eval results, telemetry blobs or delivery records
would be describing a job that does not exist. **It may not.**

---

## 6. Gaps — named, not rounded off

Task 2.2's rule: an entry that is neither checkable nor external is a gap. These are the gaps.

| # | Gap | Why it matters | Owner |
|---|---|---|---|
| G1 | **No enforced retention** on Postgres §1.1–§1.6 or the object store §2 | The Privacy Notice cannot state a retention period for them. Every entry that would carry one is `"until removed by the operator"` | Platform — out of P23's scope; **P23's obligation is to not claim otherwise** |
| G2 | **`transform.build_log`** is unscrubbed free text from the customer's toolchain | It can carry source fragments and stack traces. It is not covered by the telemetry scrubber, which sits on a different path | Backend |
| G3 | **`gdpr_request.reason`** is operator free text inside the erasure record itself | The record of an erasure request can contain more about the subject than the erasure removed | P8 |
| G4 | **`judge_human_label.labeler`** and **`authored_change.actor_id`** identify people | Both are plausibly personal data with no stated retention and no erasure route of their own | Backend |
| G5 | **OTLP export destination is operator configuration** | If an operator exports spans to a third party, that is a transfer this inventory cannot see | Operator — must be their disclosure |
| G6 | **Erasure is an operator runbook, not a self-serve route** | §8.2 may assert **only rights with an implemented route**: the implemented route is a documented request address plus the P8 `gdpr_request` path. **The notice must say that plainly rather than implying a button** | Sales Ops + P8 |
| G7 | **The transfer-basis row is empty because there is nothing to transfer** | It is empty for a reason that expires. The day a hosted deployment exists, §0 and §4 both become live obligations | System Design |

---

## 7. What the Privacy Notice may assert, derived from the above

Stated as a checklist so §8.2 has something to be checked *against* rather than reviewed by feel.

**May assert:**

- Card data never enters the system, enforced by a database constraint (§1.4).
- Prompt and completion text never reach a span, a label or a log — one scrubber, one chokepoint (§3).
- Consent records hold **no email, no name and no free text** (§1.7).
- Console sessions expire in 8 hours and hold no user identity (§5).
- In the default delivery path the platform holds **no repository token** (§4).
- Erasure **tombstones** the subject and keeps the evidentiary row; no audit entry is removed (§1.5, §1.7).
- Operator access to a tenant is recorded in `impersonation_session` and in an append-only audit chain that
  refuses `UPDATE` and `DELETE` for every role (§1.5).

**May NOT assert (today):**

- Any retention period for eval results, telemetry, blobs, delivery records or authored changes (G1).
- A sub-processor list (§4 — there is no hosted deployment to have sub-processors).
- An SLA, a certification, or a data-residency guarantee (§0, §8.9).
- A self-serve export or erasure button (G6).

---

**Hand-off:** this document goes to counsel as **input**. Counsel writes the words; §7's two lists are the
boundary the words must stay inside, and §6's gaps are the list that must shrink before any of them can be
asserted.
