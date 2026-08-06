# password-identity

## ADDED Requirements

### Requirement: Spending a valid identity token SHALL NOT depend on the deployment's sign-in seam

Consuming a single-use identity token is a different act from offering password sign-in. A deployment
that does not yet offer password sign-in is the **normal** state of every deployment before its first
owner exists, so gating token consumption on that setting makes the first owner unreachable on every
install.

#### Scenario: The first owner sets a password before the seam is flipped
- **WHEN** a deployment runs a tenant-identity seam other than `password`, a bootstrap owner has been
  adopted, and that person opens the emailed password-set link
- **THEN** the page serves the password-set form
- **AND** completing it stores the password and signs them in

#### Scenario: A bad token is still refused, ungated
- **WHEN** the same deployment is opened at the password-set path with a token that is expired, already
  spent, or unknown
- **THEN** the response is the single "this link is no longer usable" outcome
- **AND** no password is stored and no session is issued

#### Scenario: Ungating recovery does not ungate sign-in
- **WHEN** a deployment runs a seam other than `password`
- **THEN** password sign-in is still refused at `/signin` and at the session endpoint
- **AND** the only paths reachable without the seam are those that require a platform-minted token

### Requirement: An identity page that cannot serve the reader SHALL say why and what to do next

A redirect discards the reader's context. Somebody who followed a link from their inbox and lands on an
unexplained sign-in form has been told, in effect, that the link was a lie.

#### Scenario: A reader arrives with a token at a page this deployment cannot serve
- **WHEN** a reader opens an identity path carrying a token on a deployment that cannot serve it
- **THEN** the page states that this install does not offer password sign-in and names contacting
  whoever runs it as the next action
- **AND** the message names no deployment variable, seam name, or internal mechanism

#### Scenario: The reason is not carried by a failed sign-in attempt
- **WHEN** that reader is shown the message
- **THEN** they are not presented with a credential form that would reject whatever they type
- **AND** no message asserts that a credential they did not present failed to verify

### Requirement: The first-owner path SHALL be accepted end to end from the pre-flip state

#### Scenario: The acceptance run starts where a real deployment starts
- **WHEN** the first-owner acceptance test runs
- **THEN** its precondition is a deployment whose seam is **not** `password`
- **AND** it asserts the mailed link is consumable and the resulting credential signs in

#### Scenario: The fence is observed red before it is trusted
- **WHEN** the test is run against the code as it stood before this change
- **THEN** it fails
- **AND** the failure names the redirect rather than a generic assertion error
