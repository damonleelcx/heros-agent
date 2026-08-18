# M0 Review & Freeze Record (P0)

| Field | Value |
|---|---|
| Phase / Milestone | P0 / M0 |
| Owner | QA (gate) + all P0 roles (review) |
| Status | **Schemas FROZEN at M0** |
| Tasks | 6.1 (valid samples), 6.2 (invalid samples), 6.3 (review + open questions), 6.4 (freeze + CI green) |
| Cross-refs | PRD `P0-foundations.md` §13 (M0 exit checklist), §14 (open questions); all `docs/decisions/*.md`; `openspec/changes/archive/2026-07-15-p0-foundations/` |

The M0 gate: both schemas and the decision records are reviewed by every P0 role, open questions are
resolved or logged with an owner, and the two schemas are **frozen** — additive-only evolution
afterward, enforced by CI. QA lens applied throughout: *happy-path green ≠ invariant holds; the fence
must be able to go red; test the real path, not a mock.*

---

## 1. Samples & the "can-go-red" gate (tasks 6.1, 6.2)

Valid fixtures MUST validate; negative fixtures MUST be rejected — and for the *right* reason. Each
negative isolates one defect so the gate is proven to fail for a specific cause, not incidentally.

| Fixture | Kind | Expected | Actual (CI: `schemas/validate.py`) |
|---|---|---|---|
| `workflow-ir.valid.json` | valid IR | validates | ✅ validates |
| `workflow-ir.with-subgraphs.valid.json` | valid IR + subgraph labels | validates | ✅ validates |
| `metric-event.valid.json` | valid event | validates | ✅ validates |
| `runtime-invocation.valid.json` | valid invocation | validates | ✅ validates |
| `workflow-ir.invalid-missing-io-contract.json` | negative | reject | ✅ rejected — `'io_contract' is a required property` |
| `workflow-ir.invalid-missing-model.json` | negative | reject | ✅ rejected — `'model' is a required property` |
| `metric-event.invalid-missing-tag.json` | negative | reject | ✅ rejected — `'run_id' is a required property` |
| `metric-event.invalid-missing-unit.json` | negative | reject | ✅ rejected — `'unit' is a required property` |

The gate is proven to go red for **four distinct defects** across the two schemas — a missing tag, a
missing payload unit, a missing `io_contract`, and a missing node override dimension. A schema change
that stopped rejecting any of these would fail CI.

## 2. Cross-role review & sign-off (task 6.3)

Each role reviewed the artifacts it owns/depends on, and the review is backed by a runnable proof, not
an assertion (QA discipline). "Verified by" is the exact check that must stay green.

| Role | Reviewed | Verdict | Verified by |
|---|---|---|---|
| **System Designer** (lead) | `workflow-ir.schema.json`, `metric-event.schema.json`, `config-hash-spec.md`, `storage-decision-record.md`, `architecture-and-lineage.md` | ✅ contracts coherent; numbers justify the three stores; cardinality budget stated | `schemas/validate.py`, `schemas/test_config_hash.py` |
| **Backend** | Postgres DDL + constraints, emission boundary, expand-migrate-contract, golden vectors | ✅ invariants enforced by the DB, not convention; migration reversible | live PG `prove_constraints.py` (15 checks); `internal/confighash`, `internal/metricevent` tests; `test_schema_evolution.py` |
| **AI Engineer** | tag-set slice sufficiency, typed I/O contract, `pattern_labels` (nodes + subgraphs), reproducibility | ✅ every P4/P4.5 slice answerable; synthesis/adherence sufficient; one gap found & closed additively | live PG `prove_slices.py` (6/6); `schemas/spike_io_contract.py` |
| **DevOps** | Makefile + CI (5 jobs), schema gate, OTel conventions, secrets baseline | ✅ local == CI; gate can go red; no secrets in repo/logs/traces | `make go/lint/schema/tidy-check`; `secretenv_test.go`; gitleaks job |
| **Product** | north-star journey, automation-level model | ✅ lineage/reproducibility requirements traced to the trust contracts | `product-north-star.md` (link-integrity check) |
| **QA** (gate) | all fixtures + the negative-fixture discipline | ✅ valid pass, negatives fail for the right reason; real-path (live PG) proofs, no mocks | this record + the full suite |

**Review outcome:** no blocking objections. The two schemas and the config_hash/lineage scheme are
approved for freeze.

## 3. Open-questions register (task 6.3 — resolve or log)

Every open question from the PRD (§14) and the decision docs, with a disposition. **None blocks the M0
freeze** — each is either resolved here or deferred to a named phase without precluding the design.

| ID | Question | Disposition | Owner / phase |
|---|---|---|---|
| **OQ1** | Concrete span store / TSDB product (Tempo vs Jaeger; Prometheus vs ClickHouse) | **Deferred.** P0 freezes the *shape* (OTel-compatible); the product pick has no P0 information to decide it well and the OTel constraint keeps the door open. | DevOps / P2.5 |
| **OQ2** | Hash function & length for `config_hash` | **Resolved.** Full SHA-256 stored (64 hex); UI displays a 12-char prefix; truncation never stored. | System Designer (done) |
| **OQ3** | JSON Schema dialect for `io_contract` + strictness of partial inference | **Resolved (dialect) / deferred (strictness).** Dialect = draft 2020-12; permissive early schemas allowed, precision refines additively — proven graceful in `spike_io_contract.py`. | AI / P1 |
| **OQ4** | Does `variant_id` live in the IR? | **Resolved.** IR is variant-agnostic; `variant_id` enters with the P2 Variant Spec. The metric-event schema (which carries `variant_id`) is defined now; its producer arrives in P2. | System Designer (done) |
| **OQ5** | Blob GC for content-hashed blobs no longer referenced | **Deferred.** Reference-by-hash design does not preclude a GC; flagged so lineage doesn't block it. | Backend / P2.5+ |
| **OQ6** | Per-store retention/sampling policy (spans especially) | **Deferred.** Mechanism expectation set in the storage decision record; numbers tuned against real volume. | DevOps / P2.5 |
| **OQ-Prod-1** | Default automation level for a new workflow | **Logged (leaning Advisory).** Earn trust before automating; confirmed with P6. | Product / P6 |
| **OQ-Prod-2** | Granularity of Autonomous bounds (per-workflow vs per-node budgets; hard vs advisory guardrails) | **Logged.** Defined with the Autonomous loop. | Product / P6 |
| **OQ-Sec-1** | Concrete secrets-manager product + per-edition injection | **Deferred.** Baseline (env-wins-over-file) set; product wired when the service deploys. | DevOps / P2.5 |
| **OQ-Otel-1** | Opt-in prompt/response capture (to object storage under access control, never spans) | **Deferred.** Trust/consent model is a Product/P2.5 decision; the no-content-in-spans rule stands now. | Product / P2.5 |

## 4. M0 exit checklist (PRD §13) → evidence

| M0 exit criterion | Artifact | Status |
|---|---|---|
| `workflow-ir.schema.json` versioned; models call-site/model/prompt/tools/context, static-vs-runtime, per-definition count, typed I/O contract, typed edges, reserved `pattern_labels` | `schemas/workflow-ir.schema.json` (+ `runtime-invocation.schema.json`) | ✅ |
| `metric-event.schema.json` versioned; seven non-null tags + typed payload; OTel-aligned | `schemas/metric-event.schema.json`; `otel-genai-conventions.md` | ✅ |
| `config_hash`/lineage spec; canonicalization; run-time values excluded; golden vectors pass (deterministic, seed-invariant, version-sensitive) | `config-hash-spec.md`; `config-hash.golden.json`; Go + Python golden tests | ✅ |
| Storage decision record: three stores + content-hashed blobs, justified by the estimate; cardinality budget | `storage-decision-record.md` | ✅ |
| A hand-written IR sample validates; an invalid sample fails CI | `validate.py` (4 valid, 2 IR negatives) | ✅ |
| A hand-written metric-event sample validates; a missing-tag sample fails CI | `validate.py` (1 valid, 2 event negatives) | ✅ |
| Repo scaffold merged; CI (build/test/lint) green; OTel conventions + secrets baseline documented | `Makefile`, `.github/workflows/ci.yml`, `otel-genai-conventions.md`, `secrets-baseline.md` | ✅ |
| Both schemas reviewed and frozen by SD + Backend + AI + DevOps reviewers | §2 (this record) + §5 | ✅ |

## 5. Freeze declaration (task 6.4)

**`workflow-ir.schema.json`, `runtime-invocation.schema.json`, and `metric-event.schema.json` are
FROZEN at M0.** Each schema carries a `x-frozen` marker (`{ "milestone": "M0", "policy":
"additive-only" }`) pointing at this record. From M0 onward:

- **Additive only within a MAJOR:** new fields are optional (not in `required`); adding one bumps MINOR.
- **A breaking change bumps MAJOR** and follows expand-migrate-contract (`backend-invariants-and-migrations.md` §2.3).
- **CI is the enforcement mechanism:** the `schema` job (valid pass + negatives fail) and
  `test_schema_evolution.py` (backward compat) catch any drift; a change that breaks a consumer fails
  the build.

**Reversibility:** everything P0 ships is versioned text; rollback is a `git revert`. The one thing
*not* cheaply reversible is a schema already emitted-against in production — which is exactly why this
freeze gate and the additive-only rule exist.

## 6. CI-green confirmation (task 6.4)

`make go` (build/vet/gofmt/test) · `make lint` (0 issues) · `make schema` (8 fixtures + 3 contract
proofs) · `make tidy-check` · live-PG `prove_constraints.py` (15 checks) · `prove_slices.py` (6/6) —
all green at freeze. See the session's full-suite run; CI (`.github/workflows/ci.yml`) runs the same
targets on every push/PR.

**M0 is complete.**
