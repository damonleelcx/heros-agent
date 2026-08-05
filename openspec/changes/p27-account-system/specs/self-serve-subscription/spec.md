# Self-Serve Subscription — sign-up creates what the paid path already assumes

Product rationale: [`docs/prd/P27-account-system.md`](../../../../../docs/prd/P27-account-system.md) §6
(FR24–FR28). Design: [`design.md`](../../design.md) §3.7, §5.1, §5.5.

[`billing/collection.go`](../../../../../internal/billing/collection.go) opens `StartCheckout`, `ChangePlan` and
the payment read model with `s.accounts.Get(customerID)`, and
[`entitlement`](../../../../../internal/entitlement/entitlement.go) denies with `ReasonNoAccount` when that misses.
`account.Store.Create` is called by four demo/proof binaries and by test fixtures, and by **zero** lines of
non-test code under `internal/`. The P21 collection path is complete, correct, and reachable only for a
customer somebody inserted by hand.

This capability makes the account exist because a person signed up. It changes **no price, no plan and no
billing dimension** — [P7](../../../../../docs/prd/P7-billing-metering.md) and
[P21](../../../../../docs/prd/P21-stripe-payments.md) are consumed verbatim.

## ADDED Requirements

### Requirement: A verified identity that maps to no tenant SHALL be able to create one, atomically, where the deployment permits it

Sign-up creates `{tenant, user, membership(owner), account(Free)}` in a single transaction.

#### Scenario: Sign-up produces a working organization
- **WHEN** a verified identity that maps to no existing tenant completes sign-up on a deployment permitting
  self-serve
- **THEN** a tenant, a user, an owner membership and a Free account exist
- **AND** the person can immediately reach the console and the free tier's features

#### Scenario: A partial failure leaves nothing
- **WHEN** any part of the sign-up write fails
- **THEN** none of the four records exists
- **AND** there is no ownerless tenant, no tenantless account, and therefore no reconciliation job

#### Scenario: Only the name comes from the client
- **WHEN** sign-up is submitted
- **THEN** the identity, issuer and subject are taken from the verified assertion server-side
- **AND** the organization name is the only client-supplied field, treated as a display string

#### Scenario: The creator is the owner
- **WHEN** an organization is created
- **THEN** its creator holds the `owner` role
- **AND** no path creates an organization without one

### Requirement: Self-serve SHALL be a declared deployment posture that is off by default

#### Scenario: A fresh install does not offer sign-up
- **WHEN** a deployment has not declared the self-serve posture
- **THEN** self-serve is off
- **AND** no organization-creation surface is reachable

#### Scenario: An unmapped identity is refused exactly as before
- **WHEN** self-serve is off and a verified identity maps to no tenant
- **THEN** the refusal is byte-identical to the pre-existing `not_provisioned` refusal
- **AND** no new code path or message is introduced for this case

#### Scenario: The effective posture is reported, not inferred
- **WHEN** the platform becomes ready
- **THEN** the readiness surface reports whether self-serve is on
- **AND** it is not deduced from whether an identity provider is configured

#### Scenario: An air-gapped package asserts it off
- **WHEN** the air-gapped package is built
- **THEN** the build asserts the self-serve posture is off, beside the existing zero-external-origin assertion
- **AND** upgrading an air-gapped deployment does not publish a registration form on a customer's network

### Requirement: The platform SHALL permit an account to hold no billing-provider handle only while its plan charges nothing

`provider_customer_handle` becomes nullable. The original guarantee — a customer who cannot be billed must not
look billable — is preserved by stating the condition it actually was.

#### Scenario: A Free account with no handle is legal
- **WHEN** an account is created on a plan that charges nothing
- **THEN** it may be stored with no provider handle
- **AND** the entitlement gate grants that plan's features rather than denying for a missing account

#### Scenario: A paid plan without a handle is refused by the database
- **WHEN** a write would leave an account on a charging plan with no provider handle
- **THEN** the database refuses it
- **AND** the state is impossible to hold, rather than being a condition something must detect

#### Scenario: Card-shaped values are still refused
- **WHEN** a non-null provider handle is written
- **THEN** the existing card-data check still refuses any Luhn-valid 12–19 digit run, with or without
  separators
- **AND** the platform remains out of PCI scope

### Requirement: The first upgrade SHALL mint and persist the provider customer idempotently

#### Scenario: Checkout finds an account
- **WHEN** an owner starts checkout for a tenant created by sign-up
- **THEN** the account is found
- **AND** the flow proceeds to collection rather than failing for a missing account

#### Scenario: The handle is created on first need
- **WHEN** checkout starts on an account with no provider handle
- **THEN** the provider customer is created and the returned handle is persisted before collection proceeds

#### Scenario: A retried checkout does not create a second customer
- **WHEN** checkout is started twice for the same tenant, concurrently or after a timeout
- **THEN** exactly one provider customer exists for that tenant
- **AND** the idempotency key is derived from the tenant so a retry resolves to the same customer

#### Scenario: No card data reaches the platform
- **WHEN** a payment method is collected
- **THEN** it is collected by the provider's hosted surface, unchanged from P21
- **AND** no card data passes through, or is stored by, the platform

### Requirement: Closing an account SHALL stop billing and SHALL state what it does not erase

#### Scenario: Closure suspends and stops accrual
- **WHEN** an account is closed
- **THEN** the tenant is suspended and metered accrual stops

#### Scenario: Closure does not erase, and says so
- **WHEN** an account is closed
- **THEN** history is retained
- **AND** the response and the surface name the existing erasure request mechanism rather than implying
  deletion has occurred

### Requirement: This capability SHALL NOT change any price, plan or billing dimension

#### Scenario: The plan catalogue is untouched
- **WHEN** this capability lands
- **THEN** the set of plans, their prices and the set of billing dimensions are unchanged
- **AND** plan definitions remain configuration with no price in the repository
