# Admin Operations — Spec Delta (P8)

Product rationale: [`../../../../docs/prd/P8-admin-console.md`](../../../../docs/prd/P8-admin-console.md)
§6 (FR6–FR13) and §7.

Covers the operator console's privileged **command** surface over the existing platform: the
**confirmation + recorded reason + audit** discipline on every destructive/privileged action; **tenant
lifecycle** (search/view, suspend/reactivate, quota); **entitlement/plan override** (plans-as-config,
effective with **no code deploy**, audited); **billing operations oversight** (invoices, dunning,
reconciliation, additive credits/refunds, gainshare oversight — Billing-Ops, not Support); **model-
registry administration** (models + the price references used for SUM, deprecations — audited, non-
retroactive); **job/queue operations** (view/retry/cancel, worker-fleet health); the **global and
per-tenant kill switch** for the autonomous optimizer (halts further PR merges immediately, wired to
P6); and **support impersonation** (reason-required, time-bounded, read-scoped, fully audited). P8 adds
no new pipeline — it issues commands to P4/P6 (jobs/fleet), P6 (kill switch), and P7 (tenants/billing/
entitlements).

> No dollar amounts, percentages, or price bands appear in this spec. Plans are named
> (Free / Team / Business / Enterprise); the model-registry **price references** the console repoints
> are configuration, not code, not in git.

## ADDED Requirements

### Requirement: Every destructive or privileged action SHALL require confirmation, a recorded reason, and an audit entry

Every admin action that changes tenant state, money, entitlements, jobs, the fleet, or the model
registry SHALL require an explicit **confirmation** and a **recorded reason**, and on execution SHALL
write an audit entry capturing **actor, target, action, reason, and timestamp**. An action that is
**irreversible** SHALL additionally require an explicit **second confirmation** before it proceeds.

#### Scenario: A destructive action records actor, target, reason, and timestamp

- **WHEN** an authorized admin executes a destructive action (e.g., suspends a tenant) with a supplied
  reason
- **THEN** the action proceeds only after confirmation
- **AND** an audit entry is written recording the actor, the target, the action, the reason, and the
  timestamp.

#### Scenario: An action without a reason does not proceed

- **WHEN** an admin attempts a destructive/privileged action without supplying a reason
- **THEN** the action does not proceed
- **AND** no state change occurs.

#### Scenario: An irreversible action requires a second confirmation

- **WHEN** an admin invokes an irreversible action (e.g., a GDPR data deletion)
- **THEN** the action requires an explicit second confirmation before it proceeds
- **AND** without the second confirmation no deletion occurs.

### Requirement: Operators SHALL be able to run tenant lifecycle and a suspension SHALL halt the tenant's autonomous merges

An authorized operator SHALL be able to **search/view** tenants and **suspend/reactivate** them and
**adjust quotas** — each permission-gated, reason-required, and audited. A **suspension** SHALL halt the
suspended tenant's autonomous optimizer merges as part of the suspension; a **reactivation** SHALL
restore the prior state.

#### Scenario: Suspending a tenant halts its autonomous merges

- **WHEN** an authorized operator suspends a tenant with a reason
- **THEN** the tenant is suspended and an audit entry is written
- **AND** the tenant's autonomous optimizer merges no further while suspended.

#### Scenario: Reactivation restores the tenant

- **WHEN** an authorized operator reactivates a previously suspended tenant with a reason
- **THEN** the tenant returns to active state
- **AND** the reactivation is audited.

### Requirement: An entitlement or plan override SHALL take effect without a code deploy and SHALL be audited

An authorized operator SHALL be able to **override a tenant's plan/entitlement**. Because plans are
configuration (P7), the override SHALL take effect **without a code deploy** (no code change and no
migration) and SHALL be **audited**. No price value SHALL be written to a git-tracked file as part of an
override.

#### Scenario: An override takes effect with no code deploy and is audited

- **WHEN** an authorized operator overrides a tenant's plan/entitlement with a reason
- **THEN** the new entitlement takes effect for that tenant without any code change or deploy
- **AND** the override is recorded in the audit log with actor, target, the entitlement changed, the
  reason, and the timestamp.

#### Scenario: No price value is committed to git by an override

- **WHEN** an entitlement/plan override is applied
- **THEN** the change is resolved from the config store
- **AND** no plan definition or price value is written to any git-tracked file.

### Requirement: Billing oversight and credits/refunds SHALL be gated to Billing-Ops as additive, audited corrections

Viewing invoices, dunning, and reconciliation status, and issuing **credits/refunds**, and overseeing
**verified-savings / gainshare**, SHALL be reachable by **Billing-Ops** (and higher), not Support. A
credit/refund SHALL be an **additive, audited** correction (per P7) — a new billing event with the
underlying usage/invoice records left intact — never a destructive edit.

#### Scenario: Billing-Ops issues an additive, audited credit

- **WHEN** a Billing-Ops admin issues a credit for a billing error with a reason
- **THEN** the credit is recorded as a new, audited billing event
- **AND** the original charge and its usage/invoice records remain intact
- **AND** the action is recorded in the admin audit log.

#### Scenario: Gainshare oversight shows verified, merged evidence

- **WHEN** a Billing-Ops admin inspects a gainshare charge
- **THEN** the console shows that the charge traces to a verified, merged saving (the P5.5 verified-delta
  ledger entries and merge commits behind it)
- **AND** a charge with no such verified evidence is surfaced as an exception rather than shown as valid.

### Requirement: Model-registry administration SHALL be audited and SHALL NOT retroactively alter closed periods

An authorized operator SHALL be able to administer the **model registry** — add or **deprecate** models
and **repoint the per-model price references** used to derive SUM (configuration, not git). Every such
change SHALL be **audited**, and it SHALL **NOT** retroactively alter already-closed metering or billing
periods.

#### Scenario: Repointing a price reference is audited and non-retroactive

- **WHEN** an authorized operator repoints a model's price reference
- **THEN** the change is audited with actor, the model, the reason, and the timestamp
- **AND** already-closed metering/billing periods retain the price reference that was in effect when they
  closed
- **AND** only open/future periods use the new reference.

#### Scenario: Deprecating a model is recorded

- **WHEN** an operator deprecates a model in the registry
- **THEN** the deprecation is recorded and audited
- **AND** SUM derived for closed periods that used the model is unchanged.

### Requirement: Operators SHALL be able to view, retry, and cancel jobs and read worker-fleet health

An authorized operator SHALL be able to **view**, **retry**, and **cancel** discovery, eval, and
optimization **jobs** on the existing P4/P6 queue (not a second queue), and read **worker-fleet health**.
A **cancel** is a destructive action and SHALL require confirmation, a recorded reason, and an audit
entry.

#### Scenario: Cancelling a job requires reason and is audited

- **WHEN** an authorized operator cancels a running optimization job
- **THEN** the cancel proceeds only after confirmation with a reason
- **AND** an audit entry records the actor, the job, the reason, and the timestamp.

#### Scenario: Fleet health is readable

- **WHEN** an authorized operator opens worker-fleet health
- **THEN** the console shows the health of the existing P4/P6 worker fleet
- **AND** it reads the existing queue/fleet, not a second execution pipeline.

### Requirement: A global and a per-tenant kill switch SHALL immediately halt further autonomous merges

An authorized operator SHALL be able to arm a **global** kill switch (no tenant's autonomous optimizer
merges further) and a **per-tenant** kill switch (only the named tenant halts). Each SHALL take effect
**immediately** and **without a deploy**, wired to the **P6** kill switch the loop consults before every
merge, with policy defaults settable. Every arm/disarm SHALL be audited. The kill-switch state SHALL be
read **fail-closed to halt** — if the state cannot be determined, no merge proceeds.

#### Scenario: The global kill switch halts every tenant's merges immediately

- **WHEN** an authorized operator arms the global kill switch with a reason
- **THEN** no tenant's autonomous optimizer merges any further pull request, effective immediately and
  with no deploy
- **AND** the arming is audited.

#### Scenario: The per-tenant kill switch halts only that tenant

- **WHEN** an authorized operator arms the kill switch for a single tenant
- **THEN** that tenant's autonomous optimizer merges no further pull request
- **AND** other tenants' loops continue to operate
- **AND** the arming is audited.

#### Scenario: Indeterminate kill-switch state fails closed to halt

- **WHEN** the P6 merge path cannot determine the kill-switch state (the state store is unreachable)
- **THEN** the merge does not proceed
- **AND** the last-good Variant Spec remains live.

### Requirement: Support impersonation SHALL be reason-required, time-bounded, read-scoped by default, and fully audited

Support **impersonation** of a tenant SHALL require a **reason**, SHALL be **time-bounded** (auto-
expiring), SHALL be **read-scoped by default**, and SHALL be **fully audited** — every impersonated
action logged as impersonation (with the acting admin and the impersonated tenant), not as the tenant.
A **write** while impersonating SHALL require explicit **elevation** and a **second confirmation**; when
the session expires, impersonation SHALL end automatically.

#### Scenario: Impersonation without a reason is denied

- **WHEN** an admin attempts to start impersonating a tenant without supplying a reason
- **THEN** the impersonation session is not started.

#### Scenario: A read-scoped impersonation session is time-bounded and audited

- **WHEN** an admin starts impersonating a tenant with a reason
- **THEN** a read-scoped, time-bounded session is started
- **AND** every action taken during the session is logged as impersonation with the acting admin, the
  impersonated tenant, and the reason
- **AND** the session ends automatically when its time bound elapses.

#### Scenario: A write during read-scoped impersonation is denied until elevated

- **WHEN** an admin attempts a write while in a read-scoped impersonation session
- **THEN** the write is denied
- **AND** it is permitted only after explicit elevation and a second confirmation, which is itself
  audited.
