# Error Monitoring — Spec (folded from P24)

Product rationale: [`../../../docs/prd/P24-analytics-and-error-monitoring.md`](../../../docs/prd/P24-analytics-and-error-monitoring.md)
§6 (FR25–FR33), §9.3 (Backend lens), §9.7 (QA lens). Technical decisions:
[`../../changes/archive/2026-08-01-p24-analytics-error-monitoring/design.md`](../../changes/archive/2026-08-01-p24-analytics-error-monitoring/design.md) D5, D6, D10.

Covers the pipeline that makes a production exception visible, and the construction discipline that keeps
the pipeline from becoming an exfiltration path.

> Two asymmetries carry this capability. **Construct, never filter** — a denylist means a newly added
> field is *sent*, silently, and is discovered externally by a customer; an allowlist means it is
> *absent*, visible as a missing feature, and discovered here. An error reporter receives every field any
> engineer ever attaches to an error, from anywhere in the codebase, forever, which makes it the strongest
> case for construction in the system. And **the message is the dangerous field** — `failed to resolve
> prompt "…"` is an ordinary error string and an exfiltration path, so a message body is dropped unless
> its value came from the central error-code enum.

## Requirements

### Requirement: Error reporting SHALL cover the Go services and the browser surfaces, and SHALL NOT cover the CLI

Error reporting SHALL be integrated into the platform service, the admin API, both BFF server halves, and
the browser on the customer console, the operator console and the public surface. It SHALL NOT be
integrated into the CLI running on a customer's machine.

#### Scenario: A server panic is reported
- **WHEN** an unrecovered panic occurs in a platform service
- **THEN** an event is transmitted carrying the exception type, platform frames, the error code, the trace
  identifier, the release and the surface.

#### Scenario: A browser exception is reported
- **WHEN** an unhandled error, an unhandled promise rejection, a chunk-load failure or a hydration
  failure occurs on any of the three web surfaces
- **THEN** an event is transmitted for it.

#### Scenario: The CLI reports nothing
- **WHEN** the CLI panics or exits with an error on a customer's machine
- **THEN** no event is transmitted
- **AND** no crash-reporting egress exists on any CLI path.

### Requirement: A transmitted event SHALL be constructed from a named allowlist

The event SHALL be built field by field from a checked-in list carrying a category and a one-line
justification per field. The system SHALL NOT serialize an error or request object and then remove
fields. The transmitted key set SHALL be a subset of the allowlist, and every allowlist entry SHALL be
populated by something.

#### Scenario: A new field on an internal error does not reach the wire
- **WHEN** a field is added to an internal error representation without being added to the allowlist
- **THEN** the transmitted event does not contain it.

#### Scenario: The allowlist is a reviewable artefact
- **WHEN** a security reviewer asks what an error event can contain
- **THEN** the complete answer is the allowlist, with a justification per field
- **AND** the published contract document is rendered from that list rather than maintained beside it.

#### Scenario: Both directions are asserted
- **WHEN** the allowlist assertion runs
- **THEN** no transmitted key falls outside the allowlist
- **AND** no allowlist entry exists that nothing populates.

### Requirement: The transmitted event SHALL contain no content, no request material and no identifying infrastructure detail

The following SHALL NOT appear in a transmitted event on any path, including diagnostics and local
development: error message bodies other than values drawn from the central error-code enum; request
bodies; request or response headers; query strings; breadcrumb, fetch, XHR or navigation URLs; console
output; DOM breadcrumbs and click-target text; local variables; source context lines for any frame that
is not platform code; environment-variable values; provider credentials; prompt, completion, source or
diff text; hostnames; server names; IP addresses; email addresses; tenant names.

#### Scenario: A forbidden-shape fixture produces no match on the wire
- **WHEN** an error carrying an API-key-shaped value, a cloud access-key id, an email address, a
  two-kilobyte prompt, a unified diff, a tenant-scoped URL, a hostname and a tenant name is reported —
  attached by message, by wrapped error, by context value and by struct field
- **THEN** the transmitted bytes contain none of those values
- **AND** the assertion is made on the transmitted bytes, not on the reporting call's return value.

#### Scenario: The message body is dropped unless it is an error code
- **WHEN** an error whose message contains a quoted value is reported
- **THEN** the transmitted event carries the error code and the exception type
- **AND** the message body is absent.

#### Scenario: Breadcrumbs are absent rather than filtered
- **WHEN** a browser event is transmitted
- **THEN** it carries no breadcrumb collection at all
- **AND** no navigation, fetch, console or DOM breadcrumb is present to be filtered.

#### Scenario: Non-platform frames carry no source context
- **WHEN** a stack contains frames from a dependency or the runtime
- **THEN** those frames carry no source context lines
- **AND** their in-app marker distinguishes them from platform frames.

### Requirement: The event SHALL carry the existing trace identifier and SHALL mint no second correlation identity

Every event SHALL carry the trace identifier already present on the span, in the structured log, and in
the trace-id response header of an internal-error response. No new correlation identifier SHALL be
introduced.

#### Scenario: One string resolves the whole incident
- **WHEN** an operator takes the trace identifier from a transmitted event
- **THEN** it resolves the corresponding span in the span store and the corresponding structured log line
- **AND** it is the same value a customer would have received in the response header.

#### Scenario: No second identity is created
- **WHEN** the transmitted event is inspected
- **THEN** it contains no correlation identifier minted by the reporting integration for cross-system use.

### Requirement: The existing scrubbing chokepoint SHALL run over the constructed event as an independent second guard

After construction and before transmission, the platform's existing scrubber SHALL process the event.
The two guards SHALL be independent and of different kinds, so a defect in one is caught by the other.

#### Scenario: A value that slipped construction is still caught
- **WHEN** a constructed event is deliberately seeded with a secret-shaped value in a permitted field
- **THEN** the scrubber replaces it before transmission
- **AND** the transmitted bytes contain no match.

### Requirement: Reporting SHALL be fail-static, non-blocking, and out of band

A transmit failure SHALL never fail, delay, retry into an unbounded queue, or panic a served request. It
SHALL be reported as degraded on the readiness surface and logged at most once per interval.

#### Scenario: An unreachable target changes nothing a customer sees
- **WHEN** the transmit target is unreachable during a load run
- **THEN** no served request fails and no served route's p99 latency changes measurably
- **AND** readiness reports the integration degraded.

#### Scenario: A failure is loud once, not loud always
- **WHEN** transmission fails repeatedly
- **THEN** at most one log line per interval is emitted, naming the failure class
- **AND** no per-event log line is produced.

### Requirement: Sampling, rate limits and the transmit budget SHALL be explicit; performance tracing and profiling SHALL be off

The sample rate, the per-issue rate limit and the transmit budget SHALL be stated values with a recorded
basis, not inherited defaults. Performance tracing and profiling SHALL be disabled explicitly.

#### Scenario: No transaction or profile payload is constructed
- **WHEN** the reporting integration is exercised under load
- **THEN** no performance-transaction and no profile payload is transmitted
- **AND** latency continues to be measured by the telemetry substrate.

#### Scenario: The rates are stated, not defaulted
- **WHEN** the configuration is inspected
- **THEN** the sample rate, per-issue rate limit and transmit budget are explicit values
- **AND** each carries a recorded basis.

### Requirement: A browser event SHALL be sent directly to the named reporting origin, not tunnelled through the BFF

The browser SHALL transmit to the reporting origin named in that surface's policy. The BFF SHALL NOT
accept, forward or relay browser error payloads.

#### Scenario: No unauthenticated ingest path is added to the BFF
- **WHEN** the BFF's routes are enumerated
- **THEN** no route accepts a client-supplied error payload for forwarding.

#### Scenario: The policy tells the truth about where data goes
- **WHEN** a reader inspects the content-security policy of any prefix
- **THEN** every party that receives anything from that page is enumerable from the policy
- **AND** the reporting origin appears under the connect directive rather than being hidden behind a
  first-party path.

### Requirement: Source maps SHALL be uploaded for the platform's own hosted deployment only

Source maps for platform code MAY be uploaded so frames are readable for the platform's own hosted
deployment. They SHALL NOT be included in any customer-installable package and SHALL NOT be served from
a customer-facing origin.

#### Scenario: An installable package carries no source map
- **WHEN** any installable package is built
- **THEN** the build asserts that no source map is present
- **AND** a package containing one fails the build.

#### Scenario: The upload credential never reaches runtime
- **WHEN** the release pipeline uploads source maps
- **THEN** the credential used is scoped to release creation, present only in the pipeline
- **AND** it appears in no image, no manifest and no runtime environment.
