# Decision Records

Design decisions with stated trade-offs. Distinct from `docs/adr/` (architecture decision records for
single reversible-door choices) — these are the P0 System-Designer deliverables that the PRD and
OpenSpec specs reference.

| Record | Covers | P0 tasks |
|---|---|---|
| [`storage-decision-record.md`](storage-decision-record.md) | Assumptions & scope; back-of-envelope estimate; cardinality budget; three stores by shape + content-hashed blobs; trade-offs rejected; relational model. | 1.1, 1.2, 1.7 |
| [`config-hash-spec.md`](config-hash-spec.md) | `config_hash` definition; include/exclude sets; RFC 8785 canonicalization; lineage resolution; golden vectors; alternatives rejected. | 1.6 |
| [`architecture-and-lineage.md`](architecture-and-lineage.md) | End-to-end architecture, lineage-of-a-hash, and static-vs-runtime Mermaid diagrams. | 1.8 |
| [`backend-invariants-and-migrations.md`](backend-invariants-and-migrations.md) | Postgres relational model (DDL); emission-boundary rejection rule; expand-migrate-contract procedure; golden `config_hash` vectors wired as tests. | 2.1–2.4 |
| [`ai-slice-sufficiency.md`](ai-slice-sufficiency.md) | Tag-set slice sufficiency vs every P4/P4.5 slice (live proof); typed I/O-contract sufficiency for synthesis + adherence; reproducibility; `pattern_labels` reserved for nodes **and subgraphs**. | 3.1–3.3 |
| [`otel-genai-conventions.md`](otel-genai-conventions.md) | OTel GenAI attribute↔field mapping for the seven tags + payload; label-vs-attribute cardinality placement; the no-prompts/PII/secrets-in-spans rule. | 4.3 |
| [`secrets-baseline.md`](secrets-baseline.md) | Provider keys from a secrets manager (env-wins-over-file); never in repo/logs/CI-echo/traces; gitleaks CI scan; `config.example.json`. | 4.4 |
| [`product-north-star.md`](product-north-star.md) | North-star user journey (import→inspect→configure→run→compare→diagnose→apply) with unhappy paths; automation-level model (Advisory/Assisted/Autonomous) and what each requires from lineage/reproducibility. | 5.1–5.2 |
| [`m0-review-and-freeze.md`](m0-review-and-freeze.md) | The M0 gate: sample fixtures (valid pass / negatives fail), cross-role review sign-off, open-questions register, M0 exit-checklist evidence, and the schema **freeze** declaration. | 6.1–6.4 |

Product rationale: [`docs/prd/P0-foundations.md`](../prd/P0-foundations.md). Behavioral specs:
[`openspec/changes/archive/2026-07-15-p0-foundations/`](../../openspec/changes/archive/2026-07-15-p0-foundations/).
