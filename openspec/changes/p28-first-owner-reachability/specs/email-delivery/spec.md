# email-delivery

## ADDED Requirements

### Requirement: A mail proof SHALL execute from the environment the platform runs in

A send proven from a build host establishes that the credential, the relay and the mailer code work. It
establishes nothing about whether the deployed workload can reach the relay, because the build host is
subject to none of the workload's network policy. The hosted deployment held a green proof of this shape
while every send from the workload failed with `connection refused`.

#### Scenario: The proof runs where the product runs
- **WHEN** a deployment's mail configuration is declared proven
- **THEN** the sending attempt that proves it originated from the deployed workload
- **AND** a proof executed anywhere else is not accepted as evidence for that deployment

#### Scenario: A build-host proof is reported as what it is
- **WHEN** the mail proof target is run outside the deployed workload
- **THEN** its output states that it proves the relay and the credential and not the deployment's
  reachability

### Requirement: A workload's outbound dependency SHALL be reachable from that workload's own network policy

The production overlay configured SMTP on port 587 while its egress allowlist opened 443 only. Both
statements are in the same file, roughly eighty lines apart, and each is individually correct.

#### Scenario: A configured outbound port is permitted by the same manifest that configures it
- **WHEN** a workload manifest sets a destination port for an outbound dependency
- **THEN** that workload's egress policy in the same overlay permits that port
- **AND** a mismatch fails a check rather than a customer's password reset

#### Scenario: The failure is attributable when it happens anyway
- **WHEN** a send fails because the destination is unreachable
- **THEN** the workload logs a WARN naming the host and port
- **AND** the act that triggered the send is not failed by it
