# Admin RBAC — Spec Delta (P8)

Product rationale: [`../../../../docs/prd/P8-admin-console.md`](../../../../docs/prd/P8-admin-console.md)
§6 (FR1–FR5) and §7.

Covers the operator-console identity and access model: admin identity **separate from customer auth**,
mandatory **SSO + MFA**, **short-lived, revocable** sessions, a **deny-by-default** per-capability
permission gate over four roles (**Support / Billing-Ops / Platform-SRE / Superadmin**), **least
privilege** (a Support role can neither bill nor perform destructive ops), and **audited, Superadmin-
only role grants**. This is the highest-blast-radius surface in the platform — it crosses tenant
boundaries and can halt the autonomous fleet — so access control is a correctness invariant, not a
convenience.

> No dollar amounts, percentages, or price bands appear in this spec. Plans are named
> (Free / Team / Business / Enterprise). Admin identity is distinct from the P7 customer/account
> identity.

## ADDED Requirements

### Requirement: Admin access SHALL require SSO + MFA through an identity provider separate from customer auth

Admin access to the operator console SHALL be authenticated through a **dedicated admin identity
provider** that is **separate from the customer authentication path**, and SHALL require both **SSO**
and a verified **MFA** factor. An admin principal SHALL never be a tenant principal, and no admin
capability SHALL be reachable without an authenticated, MFA-verified admin session.

#### Scenario: A session without MFA is denied

- **WHEN** a user presents a valid SSO assertion but no verified MFA factor
- **THEN** no admin session is issued
- **AND** no admin capability is reachable
- **AND** the failed attempt is logged.

#### Scenario: SSO + MFA issues an admin session

- **WHEN** a user authenticates through the admin identity provider with SSO and a verified MFA factor
- **THEN** an admin session is issued for the corresponding admin principal
- **AND** that principal is an admin principal, never a tenant principal.

#### Scenario: The customer auth path cannot reach admin capabilities

- **WHEN** a principal authenticated via the customer (tenant) auth path attempts any admin capability
- **THEN** the attempt is denied
- **AND** it is not treated as an admin session regardless of the tenant's plan or role.

### Requirement: Admin sessions SHALL be short-lived and immediately revocable

An admin session SHALL have a **short time-to-live** and SHALL be **immediately revocable**. A session
that is expired or that has been revoked SHALL be denied at the next request, with no grace period, so
a lost or compromised session has a bounded blast radius.

#### Scenario: An expired session is denied at the next request

- **WHEN** an admin session's time-to-live has elapsed and the holder makes a request
- **THEN** the request is denied
- **AND** re-authentication (SSO + MFA) is required to obtain a new session.

#### Scenario: A revoked session is denied immediately

- **WHEN** an admin session is revoked and the holder makes a request after the revocation
- **THEN** the request is denied at the next request with no grace period
- **AND** the denial is logged.

### Requirement: Every admin capability SHALL be permission-gated and deny by default

Access to **every** admin capability SHALL be gated by an explicit permission held via a **role**
(Support / Billing-Ops / Platform-SRE / Superadmin), resolved against the caller's **live** role grants.
The gate SHALL **deny by default**: a capability for which the caller holds no granting permission SHALL
be denied, and the denial SHALL be logged.

#### Scenario: A capability with no granting permission is denied

- **WHEN** an authenticated admin invokes a capability that none of their live roles grant
- **THEN** the capability is denied
- **AND** the denial is logged with the actor, the attempted capability, and the timestamp.

#### Scenario: A granted capability is allowed

- **WHEN** an authenticated admin invokes a capability that one of their live roles grants
- **THEN** the capability is allowed
- **AND** the action proceeds through its confirmation/reason/audit path.

### Requirement: Roles SHALL enforce least privilege so a Support role can neither bill nor perform destructive ops

The role model SHALL enforce **least privilege**: a **Support** role SHALL **not** be able to perform
**billing** operations (credits/refunds) or **destructive/privileged** operations (tenant suspend/
reactivate, job cancel, kill-switch arm/disarm, entitlement/plan override). Those capabilities SHALL be
reachable only by the roles that hold them (Billing-Ops, Platform-SRE, or Superadmin as applicable).

#### Scenario: Support cannot issue a refund

- **WHEN** an admin whose only role is Support attempts to issue a credit or refund
- **THEN** the action is denied
- **AND** the denial is logged
- **AND** the same action attempted by a Billing-Ops admin is allowed.

#### Scenario: Support cannot perform a destructive operation

- **WHEN** a Support admin attempts to suspend a tenant, cancel a job, arm the kill switch, or override
  an entitlement
- **THEN** each attempt is denied and logged
- **AND** the same actions are allowed for the role that holds them (Platform-SRE or Billing-Ops as
  applicable).

### Requirement: Role and permission grants SHALL be permission-gated to Superadmin and audited

Granting or revoking an admin **role** SHALL itself be a **permission-gated** action reachable **only**
by a **Superadmin**, and every grant/revoke SHALL be **audited** with actor, subject, role, and
timestamp. A non-Superadmin SHALL NOT be able to grant or revoke a role.

#### Scenario: A non-Superadmin cannot grant a role

- **WHEN** an admin who is not a Superadmin attempts to grant or revoke an admin role
- **THEN** the action is denied
- **AND** the denial is logged.

#### Scenario: A Superadmin role grant is audited

- **WHEN** a Superadmin grants an admin role to another principal
- **THEN** the grant takes effect
- **AND** an audit entry records the actor, the subject, the role granted, and the timestamp.
