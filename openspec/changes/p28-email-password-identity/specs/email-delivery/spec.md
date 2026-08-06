# email-delivery

## ADDED Requirements

### Requirement: The platform SHALL send transactional mail through one seam with an SMTP implementation, and SHALL NOT require SMTP to be configured

#### Scenario: A configured deployment sends
- **WHEN** SMTP host, port and sender are configured and a confirmation or reset message is produced
- **THEN** it is delivered over SMTP with transport security
- **AND** the health surface reports mail as configured

#### Scenario: An unconfigured deployment still works
- **WHEN** no SMTP configuration is present and a message is produced
- **THEN** the act that produced it still succeeds
- **AND** the deployment is not required to configure SMTP in order to run

### Requirement: A deployment that cannot send SHALL make every undelivered message visible to its operator, and SHALL NOT discard silently

#### Scenario: An undelivered message is surfaced, not dropped
- **WHEN** mail is unconfigured and a message is produced
- **THEN** the message, including the link a person needs, is written to an operator-readable record
- **AND** a warning is logged naming the recipient and the purpose
- **AND** the health surface reports mail as not configured

#### Scenario: The log does not become a second copy of the secret
- **WHEN** the warning is written
- **THEN** it does not contain the token that the link carries

### Requirement: Mail failure SHALL NOT fail the act that produced the message

#### Scenario: A send failure leaves the account intact
- **WHEN** a sign-up, reset request or invitation produces a message and delivery fails
- **THEN** the sign-up, reset request or invitation still stands
- **AND** the failure is reported to the caller and logged, and the person can request the message again

### Requirement: A message SHALL carry a link and no other secret, and the link SHALL be single-use and purpose-bound

#### Scenario: What a message contains
- **WHEN** a confirmation or reset message is produced
- **THEN** its body carries one link containing one single-use, purpose-bound, expiring token
- **AND** it contains no password, no API credential and no session token

### Requirement: Outbound mail SHALL declare its egress lane explicitly

#### Scenario: No undeclared client reaches the network
- **WHEN** the SMTP implementation opens a connection
- **THEN** it does so through a transport that names its egress lane
- **AND** no bare, undeclared network client is constructed for mail
