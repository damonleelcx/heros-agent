# User Identity — the person, their memberships, and the session that names them

Product rationale: [`docs/prd/P27-account-system.md`](../../../../../docs/prd/P27-account-system.md) §6
(FR6–FR13). Design: [`design.md`](../../design.md) §3.2–§3.4, §3.6, §5.3, §5.4.

[ADR-008](../../../../../docs/adr/ADR-008-console-tenant-identity-seam.md) recorded that the console session holds
a tenant and not a user *"because the platform cannot currently prove one"*, and
[P22](../../../../../docs/prd/P22-sso-identity.md) repeated the deferral. P22 then shipped, so a verified assertion
now yields `sub@issuer` — the proof whose absence was the reason. This capability promotes that subject into a
first-class record, and everything that could not be expressed without one: per-user revocation, per-user audit
attribution, invitations, and a countable seat.

**Nothing above the seam changes.** ADR-008 Rule 3 holds: the cookie, the TTL, revocation semantics, the
fail-closed middleware and `scope.ts`'s no-tenant-parameter rule are unchanged. The session record gains one
optional field and moves to a durable store.

## ADDED Requirements

### Requirement: The platform SHALL store a person as a durable record keyed internally and identified by the verified federated pair

A user record is `{user_id, issuer, subject, email, created_at}` with `UNIQUE(issuer, subject)`. `email` is a
**display attribute**, never the identity.

#### Scenario: The federated pair is the identity, the email is not
- **WHEN** two sign-ins present the same `(issuer, subject)` with different email addresses
- **THEN** both resolve to the same user record, whose email is updated
- **AND WHEN** two sign-ins present the same email with different `(issuer, subject)` pairs
- **THEN** they resolve to two different users, because an address can be reassigned to a different person and
  a subject cannot

#### Scenario: The internal key is stable across an identity migration
- **WHEN** a customer changes identity provider and the same person arrives with a new `(issuer, subject)`
- **THEN** rebinding that person updates the identity pair on the existing user record
- **AND** no row referencing `user_id` is rewritten

### Requirement: The platform SHALL model membership as its own record supporting one person in several organizations

A membership is `{user_id, tenant_id, role, status, invited_by, joined_at}` with `role ∈ {owner, admin,
member}` and `status ∈ {active, removed}`.

#### Scenario: One person, two organizations
- **WHEN** a person holds active memberships in two tenants
- **THEN** both memberships exist independently, each with its own role
- **AND** each tenant counts that person against its own seat allowance

#### Scenario: Scope comes from the session, never from a selector
- **WHEN** a request arrives for a user holding several memberships
- **THEN** the acting tenant is the session's tenant
- **AND** a tenant or organization identifier supplied by the client in any position is ignored

#### Scenario: Removal is a state, not a deletion
- **WHEN** a membership is removed
- **THEN** the membership record persists with status `removed` and the user record persists
- **AND** every audit entry attributed to that person still resolves to a name

### Requirement: The platform SHALL refuse any operation that would leave a tenant with no owner

#### Scenario: The last owner cannot be removed
- **WHEN** removing a membership would leave the tenant with zero active `owner` memberships
- **THEN** the operation is refused with a named *last owner* error code
- **AND** the refusal is distinguishable from a generic permission denial

#### Scenario: The last owner cannot be demoted
- **WHEN** changing a role would leave the tenant with zero active `owner` memberships
- **THEN** the operation is refused with the same named error code

### Requirement: The platform SHALL create a membership from an invitation only when a verified identity matches it

An invitation is `{invitation_id, tenant_id, email, role, invited_by, expires_at, accepted_at?}`. The link
pre-fills the organization and the address; it grants nothing.

#### Scenario: The invited person joins
- **WHEN** an invitation is opened, SSO is completed, and the **verified** address in the assertion equals the
  invitation's address, the invitation has not expired and has not been accepted
- **THEN** a membership with the invitation's role is created and `accepted_at` is stamped

#### Scenario: A forwarded link creates nothing
- **WHEN** an invitation is opened and the verified address in the assertion differs from the invitation's
- **THEN** no user, membership or tenant is created
- **AND** the refusal is recorded as a security event

#### Scenario: An invitation is single-use
- **WHEN** an already-accepted invitation is presented again
- **THEN** it is refused and no second membership is created

#### Scenario: An invitation expires
- **WHEN** an invitation is presented after `expires_at`
- **THEN** it is refused, and the refusal names expiry rather than an identity problem

#### Scenario: The address in the request is never the address that matters
- **WHEN** an acceptance request carries an email address in its body or query
- **THEN** that value is ignored; only the assertion's verified address is compared

### Requirement: The console session SHALL name the acting person where a person is provable, and SHALL persist across restarts and replicas

The session record becomes `{id, tenantId, userId?, issuedAt, expiresAt, revokedAt?}` and lives in a durable,
shared store.

#### Scenario: An interactive session names a person
- **WHEN** a session is issued from a completed interactive sign-in
- **THEN** it carries the acting user's identifier

#### Scenario: A machine principal names none
- **WHEN** a principal is authenticated by a machine credential
- **THEN** the user field is **absent** — not an empty string, not a placeholder

#### Scenario: A restart does not end a session
- **WHEN** the console process is restarted while a session is live and unexpired
- **THEN** the next request with that session's token is served

#### Scenario: A second replica serves the same session
- **WHEN** a session issued by one console replica is presented to another
- **THEN** it is served identically

#### Scenario: Everything above the seam is unchanged
- **WHEN** this capability lands
- **THEN** the cookie name and flags, the session TTL, revocation-effective-at-the-next-request-with-no-grace,
  the fail-closed middleware and `scope.ts`'s rule that no call site may pass a tenant are byte-for-byte what
  they were
- **AND** this is asserted by a pinned regression suite, not by review

### Requirement: Removing a member SHALL end that person's access at their next request, and SHALL disclose what it does not revoke

#### Scenario: Sessions and personal credentials stop working
- **WHEN** a membership is set to removed
- **THEN** that user's sessions for that tenant are revoked and every credential carrying their user reference
  for that tenant is revoked
- **AND** their next console request redirects to sign-in and their next API request is refused, with no
  restart and no grace period

#### Scenario: Machine credentials are untouched and named
- **WHEN** a removal is previewed
- **THEN** the response lists, by label, every organization-scoped machine credential that removal will **not**
  revoke
- **AND** the confirmation surface renders that list before the removal can be confirmed

#### Scenario: Removal is atomic
- **WHEN** a removal is applied
- **THEN** the membership change, the session revocations, the credential revocations, the seat observation and
  the audit entry commit together
- **AND** there is no window in which the person is absent from the member list while still holding a working
  credential

### Requirement: An audited action SHALL attribute to the acting person when there is one, and SHALL NOT invent one when there is not

#### Scenario: A session-borne action names the person
- **WHEN** an audited action is taken through an interactive session
- **THEN** the audit entry records the acting user

#### Scenario: A machine-borne action names the credential
- **WHEN** an audited action is taken with a machine credential
- **THEN** the audit entry records the credential identifier
- **AND** it names no person

### Requirement: A user SHALL NOT be an operator, and an operator SHALL NOT hold a membership

The customer identity domain and the operator identity domain stay disjoint — P8 FR1, unchanged.

#### Scenario: The domains do not join
- **WHEN** any query resolves an operator principal
- **THEN** no tenant, membership or user record participates in it
- **AND** no schema path exists by which an operator principal acquires a membership
