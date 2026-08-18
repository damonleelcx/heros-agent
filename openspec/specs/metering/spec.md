# Metering — Spec (folded from P7)

Product rationale: [`../../../docs/prd/P7-billing-metering.md`](../../../docs/prd/P7-billing-metering.md)
§6 (FR1–FR4) and §7.

Covers the value metric — **LLM spend under management (SUM)** derived idempotently from the P2.5
cost events (never a second pipeline); idempotent usage records for SUM, seats, retention, and cloud
eval compute keyed `{customer, period, metric}` (same period usage never double-counted); verified
billable savings computed **only** from merged-PR deltas in the P5.5 verified-delta ledger (estimated
/ unverified savings excluded, baseline + holdout methodology fixed and auditable); and usage
reconcilable against the billing provider. Builds on P2.5's `metrics-observability` cost events and
P5.5's `verification` verified-delta ledger.

> No dollar amounts, percentages, or price bands appear in this spec. Plans are named
> (Free / Team / Business / Enterprise); limits + price references are configuration, not code, not
> in git.

## Requirements

### Requirement: SUM SHALL be derived from the P2.5 cost events, not collected by a second pipeline

Spend under management (SUM) for a customer over a billing period SHALL be computed as an
**aggregation of the P2.5 cost events** (tokens × the model's price) attributed to that customer,
**reusing the telemetry substrate** — the platform SHALL NOT stand up a second usage-collection
pipeline. Re-deriving SUM for a **closed** period from the same events SHALL yield the same figure
(deterministic).

#### Scenario: SUM aggregates the existing cost events

- **WHEN** a customer's workflows emit P2.5 cost events (tokens × price) across a billing period
- **THEN** `DeriveSUM(customer, period)` returns the aggregate of exactly those cost events attributed
  to the customer
- **AND** no separate usage-metering pipeline collects a second, independent record of the same spend.

#### Scenario: Re-deriving a closed period is deterministic

- **WHEN** `DeriveSUM` is run twice for the same closed period over the same cost events
- **THEN** it returns the identical SUM figure both times.

### Requirement: Every meter SHALL be an idempotent usage record keyed by customer, period, and metric

Each meter — **SUM, seats, retention, and cloud eval compute** — SHALL be recorded as a usage record
keyed `{customer, period, metric}`, written by an **upsert**. Re-reporting or re-deriving the same
period's usage for a metric SHALL update the one record in place and SHALL NOT append a second
charge-bearing record. The **same period's usage SHALL NOT be double-counted** under any number of
replays, re-derivations, or reconciliations.

#### Scenario: A replayed period is counted exactly once

- **WHEN** a period's usage for a metric is reported, and the same events/records are then replayed
  any number of times
- **THEN** exactly one `{customer, period, metric}` record exists for that metric
- **AND** its quantity reflects the period once, not multiplied by the number of replays.

#### Scenario: All four meters are recorded per period

- **WHEN** a customer accrues SUM, holds dashboard seats, retains traces/metrics, and runs cloud eval
  compute in a period
- **THEN** a usage record exists for each of `sum`, `seats`, `retention`, and `eval_compute`, each
  keyed by `{customer, period, metric}`.

#### Scenario: A redelivered cost event does not double-count SUM

- **WHEN** a P2.5 cost event is redelivered under P2/P2.5 retry idempotency
- **THEN** it contributes to SUM exactly once
- **AND** the period's `sum` usage record is not increased by the redelivery.

### Requirement: Billable savings SHALL be computed only from merged-PR deltas in the P5.5 verified-delta ledger

Billable savings for a customer over a period SHALL be `baseline SUM − optimized SUM`, attributable
**only** to **merged** optimization pull requests, computed **only** from the **P5.5 verified-delta
ledger**. A saving that is estimated, unverified, or attributable to an un-merged proposal SHALL NOT
contribute to billable savings. Each billable-savings record SHALL carry references to the verifying
ledger entries and the merge commits that produced it, and the **baseline + holdout methodology SHALL
be fixed and auditable** (reconstructable from those references).

#### Scenario: A merged, verified saving is billable and traces to its evidence

- **WHEN** the P5.5 verified-delta ledger contains a held-out, statistically-significant delta for a
  **merged** optimization PR that reduced the customer's SUM in the period
- **THEN** `ComputeBillableSavings` includes `baseline SUM − optimized SUM` for that delta as billable
  savings
- **AND** the billable-savings record references the verifying ledger entries and the merge commits.

#### Scenario: An estimated or un-merged saving is not billable

- **WHEN** an estimated saving, or a saving from a proposal that was verified but never merged, is
  present as a projection
- **THEN** `ComputeBillableSavings` contributes **zero** billable savings for it
- **AND** the saving appears in no billable-savings record.

#### Scenario: The baseline and holdout methodology is auditable

- **WHEN** an auditor inspects a billable-savings figure
- **THEN** the fixed baseline + holdout methodology behind it is reconstructable from the stored
  verified-delta references.

### Requirement: Reported usage SHALL be reconcilable against the billing provider

The usage the platform reports SHALL be **reconcilable** against the billing provider: a
reconciliation over a period SHALL compare the platform's usage records to the provider's recorded
usage and invoices and **surface any drift** — usage the provider is missing, or provider-recorded
usage the platform did not report — rather than silently accepting divergence. Reconciliation SHALL
be idempotent and SHALL NOT reconcile by overwriting either side's records.

#### Scenario: Drift is surfaced, not silently accepted

- **WHEN** the platform holds a usage record for a period that the provider did not record
- **THEN** `Reconcile(customer, period)` reports that record as drift
- **AND** it does not silently discard, overwrite, or accept the divergence.

#### Scenario: A matching period reconciles clean

- **WHEN** the platform's usage records for a period match the provider's recorded usage
- **THEN** `Reconcile` reports the period as matched with no drift
- **AND** running it again yields the same result without mutating either side's records.
