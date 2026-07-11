## 1. System Designer — contracts, estimation, storage decision (lead)

- [ ] 1.1 Record explicit assumptions + scope boundaries (repos/day, nodes/repo, variants×cases×seeds,
      invocations/case, reproducibility definition) in the storage decision record.
- [ ] 1.2 Produce the back-of-envelope estimate (metric events, spans, eval rows, blob volume per
      optimization run and per day) and the cardinality budget (which tags are TSDB series labels vs.
      span/Postgres attributes).
- [ ] 1.3 Author `workflow-ir.schema.json` (versioned): nodes, edges, static-definition vs
      runtime-invocation, per-definition node count, `variable_at_runtime` flag, reserved `pattern_labels`.
- [ ] 1.4 Add the first-class typed I/O contract (`input_schema` + `output_schema`, JSON Schema
      2020-12) as a required field on every node.
- [ ] 1.5 Author `metric-event.schema.json` (versioned): seven required non-null tags + typed payload
      + optional extensible dimensions.
- [ ] 1.6 Write the `config_hash` spec: canonicalization rules, the include set, the excluded run-time
      values (`run_id`, `seed`, `timestamp`), and hash function/length decision.
- [ ] 1.7 Write the storage decision record: three stores by shape + content-hashed blobs, each choice
      justified against 1.2; state the trade-offs rejected.
- [ ] 1.8 Draft the end-to-end architecture + lineage diagram (Mermaid) for the PRD.

## 2. Backend — invariants, constraints, migration strategy (support)

- [ ] 2.1 Model the Postgres eval-results / lineage tables: non-null tag columns, `config_hash`
      uniqueness where a row is a configuration, FKs eval-result → variant / node / case.
- [ ] 2.2 Specify the emission-boundary rejection rule for any event missing a tag (defense in depth
      with the DB constraints).
- [ ] 2.3 Document the expand-migrate-contract procedure for evolving the IR + registry schemas, with
      a worked example (adding an optional IR field) proving older samples still validate.
- [ ] 2.4 Define golden `config_hash` vectors: determinism, seed-invariance, registry-version
      sensitivity; wire them as tests.

## 3. AI Engineer — downstream-slice sufficiency (support)

- [ ] 3.1 Cross-check the tag set against every P4/P4.5 slice (per-variant, per-node attribution,
      per-case, per-failure-cluster, per-seed CIs); record any gaps and close them additively.
- [ ] 3.2 Confirm the typed I/O contract is sufficient to later drive schema-driven eval-set synthesis
      (P4) and output-contract-adherence metrics; note the permissive-schema allowance for P1.
- [ ] 3.3 Confirm `config_hash + seed` reproducibility satisfies "verification decides" (every result
      replayable); reserve `pattern_labels` for the P3.5 dispatcher.

## 4. DevOps — scaffold, CI, OTel conventions, secrets baseline (support)

- [ ] 4.1 Scaffold the repo (build/test/lint) with a green CI pipeline.
- [ ] 4.2 Add the CI schema-validation job: validate the valid IR + metric-event samples; assert the
      negative fixtures (missing tag / missing `io_contract`) FAIL the build.
- [ ] 4.3 Author the OTel GenAI-semantic-conventions doc: attribute→field mapping, and the rule that
      prompts/PII/secrets never appear in span attributes.
- [ ] 4.4 Establish the secrets-management baseline (provider keys from a secrets manager; never in
      repo/logs/CI echo/traces).

## 5. Product — north-star journey + automation-level model (support)

- [ ] 5.1 Draft the top-level user journey (import → inspect → configure → run → compare → diagnose →
      apply) as the UI north star.
- [ ] 5.2 Define the automation-level model (Advisory / Assisted / Autonomous) and note what each trust
      level requires from lineage/reproducibility (auditability, reversibility).

## 6. Samples, review & M0 freeze

- [ ] 6.1 Hand-write a valid IR sample and a valid metric-event sample; confirm they validate.
- [ ] 6.2 Hand-write invalid samples (missing tag, missing `io_contract`); confirm CI rejects them.
- [ ] 6.3 Review both schemas + the decision record with System Designer + Backend + AI + DevOps;
      resolve open questions or log them.
- [ ] 6.4 **Freeze** both schemas at M0; confirm CI green.
