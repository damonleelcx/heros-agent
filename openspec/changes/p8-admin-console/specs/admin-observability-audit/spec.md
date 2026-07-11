# Admin Observability & Audit — Spec Delta (P8)

Product rationale: [`../../../../docs/prd/P8-admin-console.md`](../../../../docs/prd/P8-admin-console.md)
§6 (FR14–FR18) and §7.

Covers the operator console's cross-tenant read models and its record of truth: **permission-gated**
cross-tenant read models (usage/SUM, COGS/provider spend, revenue/ops aggregates — mechanism, not
numbers; top consumers; anomalies) where **every cross-tenant view is logged**; an **append-only,
tamper-evident audit log** that captures **every admin action AND every P6 autonomous merge**;
**compliance / data-deletion (GDPR)** handling that is actionable and verifiable from the console; and
**admin actions being observable** on the P2.5 substrate. These read models and aggregates derive from
the existing P2.5 telemetry — the console re-collects nothing.

> No dollar amounts, percentages, or price bands appear in this spec. Cross-tenant read models expose
> **mechanism, not numbers**. Plans are named (Free / Team / Business / Enterprise).

## ADDED Requirements

### Requirement: Cross-tenant read models SHALL be permission-gated and every authorized view SHALL be logged

Cross-tenant read models — aggregate usage/SUM, COGS/provider spend, revenue/ops aggregates, top
consumers, and anomalies — SHALL be **read-only** and **permission-gated**. An **unauthorized** admin
SHALL be **denied** a cross-tenant view, and **every authorized cross-tenant view SHALL be logged** (the
viewing admin, the read model, and the timestamp), because any cross-tenant access is a privacy event.

#### Scenario: An unauthorized admin is denied a cross-tenant view

- **WHEN** an admin who lacks the cross-tenant read permission requests a cross-tenant read model
- **THEN** the request is denied
- **AND** the denial is logged.

#### Scenario: An authorized cross-tenant view is logged

- **WHEN** an authorized admin views a cross-tenant read model
- **THEN** the read model is returned
- **AND** the view is logged with the viewing admin, the read model, and the timestamp.

### Requirement: The audit log SHALL be append-only and tamper-evident

The audit log SHALL be **append-only** and **tamper-evident**: a mutation or deletion of an existing
entry SHALL be **prevented and detectable** (e.g., via a hash chain / write-once store), and **no role,
including Superadmin**, SHALL be able to silently alter or erase an entry.

#### Scenario: A Superadmin cannot silently delete an audit entry

- **WHEN** any admin, including a Superadmin, attempts to mutate or delete an existing audit entry
- **THEN** the mutation/deletion is prevented
- **AND** an integrity verification detects any break in the audit chain.

#### Scenario: Tampering is detectable

- **WHEN** the audit log's integrity is verified after an out-of-band attempt to alter a stored entry
- **THEN** the verification reports the entry at which the chain is broken
- **AND** it does not report the log as intact.

### Requirement: The audit log SHALL capture every admin action and every autonomous merge

The audit log SHALL record **every admin action** (keyed to reconstruct **actor, target, action, reason,
timestamp**) **and every P6 autonomous merge** (with the merge's motivating diagnosis, verified delta,
and merge commit, mirroring the P6 change ledger). An admin action or an autonomous merge that is not
recorded SHALL be treated as a failure of the action, not a silent omission.

#### Scenario: Every autonomous merge appears in the audit log

- **WHEN** a tenant's P6 autonomous optimizer merges a pull request
- **THEN** the audit log contains an entry for that merge with its motivating diagnosis, verified delta,
  and merge commit
- **AND** the entry is reconstructable to the tenant and timestamp.

#### Scenario: An admin action that cannot be audited does not take effect

- **WHEN** a privileged admin action is attempted while the audit store is unavailable
- **THEN** the action fails closed and does not take effect
- **AND** no unaudited state change occurs.

### Requirement: A data-deletion (GDPR) request SHALL be actionable from the console and verifiable

A **data-deletion (GDPR) request** SHALL be **actionable** from the console and its completion
**verifiable**: the subject's data SHALL be removed (or tombstoned), a **verifiable completion record**
SHALL be produced, and the action SHALL be audited. To keep the append-only audit chain intact, the
audit log SHALL retain a **non-PII tombstone reference** for the deletion rather than the deleted
content.

#### Scenario: A deletion request is executed and verifiable

- **WHEN** a Superadmin actions a GDPR data-deletion request for a subject (with the required second
  confirmation)
- **THEN** the subject's data is removed or tombstoned
- **AND** a verifiable completion record is produced showing the deletion completed
- **AND** the action is recorded in the audit log.

#### Scenario: The audit chain stays intact after a deletion

- **WHEN** a GDPR deletion completes
- **THEN** the audit log retains a non-PII tombstone reference for the deletion
- **AND** no audit entry is removed, so the tamper-evident chain remains verifiable.

### Requirement: Admin actions SHALL be observable on the P2.5 telemetry substrate

Admin activity — logins, MFA failures, privileged actions, kill-switch state, active impersonation
sessions, and cross-tenant views — SHALL emit metrics and audit events on the **P2.5 telemetry
substrate** (not a new pipeline), so operator behavior is a live operational signal with anomaly
alerting.

#### Scenario: Privileged-action volume is an observable signal

- **WHEN** admins perform logins, privileged actions, impersonations, and cross-tenant views
- **THEN** corresponding metrics/audit events are emitted on the P2.5 substrate
- **AND** an anomaly (e.g., a spike in privileged actions or a kill switch left armed) can raise an
  alert.

#### Scenario: No secret appears in admin telemetry

- **WHEN** admin activity is emitted to the telemetry substrate
- **THEN** no SSO/MFA secret, session signing key, or provider handle appears in any span, metric label,
  or log.
