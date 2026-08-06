# password-identity

## ADDED Requirements

### Requirement: The platform SHALL offer an email-and-password sign-in method as a seam kind, without altering the seam contract or any other kind

`CONSOLE_TENANT_IDENTITY=password` selects it. The ADR-008 contract `verify → { tenantId, userId? }` is
unchanged; a second entry point taking an address and a password is added beside it. `configured`, `oidc`,
`saml`, `platform` and `dev` behave exactly as before.

#### Scenario: A password deployment signs a person in
- **WHEN** the seam kind is `password` and a person submits a registered address with the matching password
- **THEN** a session is issued whose principal carries the organization **and** the person
- **AND** the session record, cookie flags, revocation semantics and scope derivation are unchanged from before
  this change, asserted by regression test

#### Scenario: The other kinds are unaffected
- **WHEN** the seam kind is `configured`, `oidc`, `saml`, `platform` or `dev`
- **THEN** sign-in behaves exactly as it did before this change
- **AND** no password field is presented on any of them

### Requirement: The platform SHALL store a password only as a memory-hard, per-row-salted, algorithm-tagged hash

The stored form is `$argon2id$v=<v>$m=<m>,t=<t>,p=<p>$<salt>$<hash>`.

#### Scenario: A stored password is tagged and salted
- **WHEN** a password is set
- **THEN** the stored value begins `$argon2id$`, carries the parameters it was produced with, and carries a salt
  drawn from a cryptographic source
- **AND** two people who choose the same password have different stored values

#### Scenario: A password is never stored by the minted-secret hash
- **WHEN** any code path stores or compares a password
- **THEN** it does not use the SHA-256 helper that hashes platform-minted secrets
- **AND** a test asserts this, and the database refuses a stored value that is not argon2id-tagged

#### Scenario: Cost parameters are raised without a migration
- **WHEN** the configured parameters differ from those tagged in a person's stored value and that person signs
  in successfully
- **THEN** the password is re-hashed with the current parameters and the new value is stored
- **AND** the sign-in succeeds normally

### Requirement: A password SHALL never appear in a response, a log, a URL, or a stored session

#### Scenario: A submitted password leaves no trace
- **WHEN** a person signs in, signs up, resets or changes a password
- **THEN** the submitted value appears in no log line, no response body, no query string, no session record and
  no telemetry attribute
- **AND** a test asserts the absence across those surfaces

### Requirement: The platform SHALL enforce a password floor and a common-password blocklist, and SHALL state the rule before submission

#### Scenario: A short password is refused
- **WHEN** a password shorter than twelve characters is submitted
- **THEN** it is refused with a message naming the minimum
- **AND** the same rule is rendered beside the field before submission

#### Scenario: A breached or self-referential password is refused
- **WHEN** the submitted password appears in the bundled common-password list, or contains the person's own
  address
- **THEN** it is refused with a message saying why, and no account or password change is written

### Requirement: Sign-in SHALL refuse an unknown address and a wrong password identically, in body and in timing

#### Scenario: The refusal does not disclose whether an address is registered
- **WHEN** sign-in is attempted with an unregistered address, and separately with a registered address and a
  wrong password
- **THEN** both produce the same status and the same message
- **AND** both perform a full password verification, so the two do not differ measurably in time

#### Scenario: The operator can still tell them apart
- **WHEN** either refusal occurs
- **THEN** the platform's own log records the distinct cause

### Requirement: The platform SHALL lock an account after repeated failures and SHALL say so

#### Scenario: Consecutive failures lock the account
- **WHEN** ten consecutive sign-in failures occur for one person within fifteen minutes
- **THEN** further attempts are refused for fifteen minutes
- **AND** the refusal names the lock and its remaining duration and offers a password reset

#### Scenario: A success clears the counter
- **WHEN** a sign-in succeeds, or a password reset completes
- **THEN** the failure count is cleared and any lock is released

#### Scenario: Locking is per person, not per address of origin
- **WHEN** many people sign in from one network address
- **THEN** one person's failures do not lock another's account

### Requirement: Self-serve sign-up SHALL create the person, the organization, the owner membership, the free account and the password atomically, and SHALL remain governed by the deployment posture

#### Scenario: One act or none
- **WHEN** sign-up is submitted with an organization name, an address and a password on a deployment with
  self-serve enabled
- **THEN** the person, the organization, an owner membership, a free account and the stored password all exist
- **AND** if any one of those writes fails, none of them exists

#### Scenario: Sign-up stays off where it is declared off
- **WHEN** self-serve sign-up is not enabled
- **THEN** no sign-up form is offered and a submitted sign-up is refused, naming the deployment as the decider

#### Scenario: Sign-up is not an account-existence oracle
- **WHEN** sign-up is submitted for an address that already has a password
- **THEN** the response is indistinguishable from a successful sign-up
- **AND** a message is sent to that address reporting the attempt
- **AND** no second organization, membership or account is created

### Requirement: An unverified address SHALL limit exactly two actions and SHALL block nothing else

#### Scenario: An unverified person uses the product
- **WHEN** a person whose address is unverified signs in
- **THEN** they reach the console and may use every surface except the two below
- **AND** a persistent notice names their address and offers to resend the confirmation

#### Scenario: The two limited actions
- **WHEN** an unverified person attempts to invite a member, or to move to a plan that charges
- **THEN** the action is refused with a message naming confirmation as the prerequisite and offering a resend
- **AND** nothing is written and no mail is sent to any third party

### Requirement: Email confirmation and password reset SHALL use single-use, expiring, purpose-bound tokens spent at the store

#### Scenario: A link works once
- **WHEN** a confirmation or reset link is used twice
- **THEN** the second use is refused
- **AND** the refusal is the same message for spent, expired and unknown tokens

#### Scenario: Concurrency cannot double-spend
- **WHEN** two requests present the same unspent token simultaneously
- **THEN** exactly one succeeds, decided by the store rather than by application logic

#### Scenario: A token is bound to one purpose
- **WHEN** a token minted for confirmation is presented to the reset endpoint, or the reverse
- **THEN** it is refused

#### Scenario: A token is not a platform credential
- **WHEN** a confirmation or reset token is presented as an API credential
- **THEN** it is refused as unrecognised, and the refusal is enforced by an allowlist of accepted session
  purposes rather than by a list of rejected ones

### Requirement: Requesting a password reset SHALL answer identically for every address

#### Scenario: Known and unknown addresses are indistinguishable
- **WHEN** a reset is requested for a registered address, and separately for an unregistered one
- **THEN** both produce the same status and the same message
- **AND** only the registered one causes a message to be sent

### Requirement: Completing a password reset SHALL revoke every session and every personal credential that person holds, and SHALL disclose what it did not revoke

#### Scenario: A reset ends everything the person held
- **WHEN** a reset completes
- **THEN** every session that person holds is revoked and refused at the next request, with no grace period
- **AND** every personal credential that person holds is revoked

#### Scenario: Machine credentials survive and are named
- **WHEN** a reset completes and that person's organization holds machine credentials
- **THEN** those credentials are untouched
- **AND** the completion screen lists them, so the person knows what is still running

### Requirement: Changing a password while signed in SHALL require the current password and SHALL end every other session

#### Scenario: The current password is required
- **WHEN** a password change is submitted without the correct current password
- **THEN** it is refused and nothing is written

#### Scenario: Other sessions end, this one does not
- **WHEN** a password change succeeds
- **THEN** every other session that person holds is revoked
- **AND** the session that made the change remains usable

### Requirement: A deployment without self-serve sign-up SHALL be able to establish its first owner without an operator handling a password

#### Scenario: The first owner is bootstrapped
- **WHEN** a deployment declares a bootstrap owner address and that address has no password
- **THEN** boot mints a single-use password-set token for it and hands it to the mail seam
- **AND** the fact is logged

#### Scenario: Bootstrapping is idempotent
- **WHEN** the deployment restarts and the bootstrap address already has a password
- **THEN** no token is minted and nothing is sent
