# Console BFF — Spec Delta (P9)

Product rationale: [`../../../../../docs/prd/P9-web-console.md`](../../../../../docs/prd/P9-web-console.md)
§6 (FR1–FR8, FR25–FR26) and §7. Architecture decisions:
[`../../design.md`](../../design.md) Decisions 1, 3, 4, 6.

Covers the credential and session boundary between the browser and the platform API: the platform API
key held **server-side only**, an `HttpOnly` browser **session** bound server-side to a tenant, routes
that **fail closed**, a **pass-through** contract that forbids the BFF from computing or reinterpreting
read models, preservation of the three-way failure taxonomy (**503 not-mounted / 404 not-found /
transport failure**), SSE proxying with flush semantics intact, explicit upstream timeouts, and
readiness that **aggregates** the console rather than reporting only the platform service.

> This capability exists because the current pages cannot authenticate: the page routes are public
> while every `/api/*` call they make requires `X-API-Key`, so under `auth_mode=required` the UI loads
> and then fails every request. A static bundle cannot hold a secret, so a server-side credential
> holder is the only correct answer.

## ADDED Requirements

### Requirement: The platform API key SHALL be held server-side only and SHALL never reach the browser

The platform API key (or any equivalent platform credential) SHALL exist only in the BFF process
environment. It SHALL NOT appear in the client bundle, in a script-readable cookie, in `localStorage`
or `sessionStorage`, in any URL or query string, in any log line, or in any telemetry attribute.

#### Scenario: No credential in the shipped client bundle

- **WHEN** the console's client bundle is built and scanned for platform key material
- **THEN** no key material is found
- **AND** the scan is a build-time gate, so a bundle containing key material fails the build rather
  than being caught in review.

#### Scenario: No credential in logs or telemetry

- **WHEN** the BFF logs a request or emits a trace for an upstream call
- **THEN** no credential value appears in any log field, span attribute, or error message
- **AND** this holds on the failure path as well as the success path.

#### Scenario: The browser cannot read its own session token

- **WHEN** page script attempts to read the session cookie
- **THEN** the cookie is not readable, because it is set `HttpOnly` and `SameSite`
- **AND** no platform credential is obtainable from the client at all.

### Requirement: Console data routes SHALL require a session and SHALL fail closed

Every console route that renders tenant data SHALL require a valid session. An unauthenticated request
SHALL be redirected to sign-in and SHALL NOT be served a rendered shell that subsequently fails its
data requests.

#### Scenario: An unauthenticated request is redirected, not rendered

- **WHEN** a request without a valid session arrives at a console route that renders tenant data
- **THEN** the request is redirected to sign-in
- **AND** no tenant data is returned
- **AND** no shell is rendered that would then fail every data request with an authorization error.

#### Scenario: A public route does not become a data route

- **WHEN** a route is not gated because it renders no tenant data (for example sign-in itself)
- **THEN** it returns no tenant data under any parameters
- **AND** it does not issue upstream calls using the server-held credential.

### Requirement: Sessions SHALL be bounded in lifetime and revocable with effect at the next request

A session SHALL carry a bounded lifetime and SHALL be revocable. A request presenting an expired or
revoked session SHALL be denied at the **next** request with no grace period, and SHALL NOT be silently
retried using the server-held credential.

#### Scenario: An expired session is denied at the next request

- **WHEN** a session's lifetime has elapsed and its holder makes a request
- **THEN** the request is denied and re-authentication is required
- **AND** the BFF does not fulfil the request with the server-held credential on the holder's behalf.

#### Scenario: A revoked session is denied immediately

- **WHEN** a session is revoked and its holder makes a request after the revocation
- **THEN** the request is denied at the next request with no grace period
- **AND** the denial is logged without recording any credential value.

### Requirement: The BFF SHALL be the only origin the browser calls for tenant data

The browser SHALL obtain tenant data exclusively through the BFF origin. The console SHALL NOT issue a
direct browser-to-platform-API request for tenant data.

#### Scenario: No direct browser call to the platform API

- **WHEN** any console view loads and its network traffic is inspected
- **THEN** every tenant-data request targets the BFF origin
- **AND** no request targets the platform API origin directly.

### Requirement: Request scope SHALL be derived from the session's tenant, never from client input

Every upstream call the BFF makes SHALL be scoped to the tenant bound to the session server-side. A
tenant identifier supplied by the client SHALL NOT widen, change, or override that scope.

#### Scenario: A client-supplied tenant identifier cannot widen scope

- **WHEN** a request carries a tenant identifier in its path, query, body, or headers that differs from
  the session's tenant
- **THEN** the request is scoped to the session's tenant or rejected
- **AND** no data belonging to another tenant is returned.

### Requirement: The BFF SHALL pass platform read models through unmodified

The BFF SHALL forward platform read models to the browser **unmodified**. It SHALL NOT compute,
re-rank, re-aggregate, merge multiple upstream responses into one, reformat values, translate statuses,
or apply any business rule about what a value means.

#### Scenario: A read model is forwarded byte-equivalently

- **WHEN** the BFF forwards a platform read model response to the browser
- **THEN** the payload the browser receives is semantically identical to the platform's response
- **AND** no field has been added, removed, renamed, reordered by rank, rounded, or reformatted.

#### Scenario: The BFF does not merge two upstream responses

- **WHEN** a console view needs data from two platform endpoints
- **THEN** the BFF forwards each response separately
- **AND** it does not synthesize a combined response, because doing so would place a business rule in
  the BFF.

### Requirement: The BFF SHALL preserve the three-way upstream failure taxonomy

**503 subsystem-not-mounted**, **404 not-found**, and **transport failure** SHALL remain three
distinguishable outcomes at the browser, each carrying the upstream error body where one exists. The
BFF SHALL NOT normalize them to a single shape, and SHALL NOT map a 404 to an empty successful result.

#### Scenario: A 503 not-mounted is distinguishable from a 404

- **WHEN** the platform returns 503 with a not-mounted error body for a subsystem
- **THEN** the browser receives an outcome distinguishable from not-found
- **AND** the upstream error body is preserved.

#### Scenario: A 404 is not converted into an empty result

- **WHEN** the platform returns 404 for a subject that does not exist
- **THEN** the browser receives a not-found outcome
- **AND** it does not receive a successful response with an empty collection.

#### Scenario: A transport failure is its own outcome

- **WHEN** the BFF cannot reach the platform API at all
- **THEN** the browser receives a transport-failure outcome distinct from both 503 and 404
- **AND** it is not represented as an empty or successful result.

### Requirement: The BFF SHALL proxy the run-monitor SSE stream without altering its delivery semantics

The BFF SHALL proxy the platform's server-sent-events run-monitor stream, preserving per-event flush
semantics, closing the client stream when the upstream closes, and SHALL NOT buffer or batch events.

#### Scenario: Events are delivered as they arrive

- **WHEN** the platform emits an SSE event on the run-monitor stream
- **THEN** the browser receives that event without waiting for a subsequent event
- **AND** events are not coalesced into batches.

#### Scenario: Upstream close closes the client stream

- **WHEN** the platform closes the SSE stream because the run reached a terminal state
- **THEN** the BFF closes the client stream
- **AND** the client is able to distinguish a completed stream from a failed one.

#### Scenario: Stream failure leaves the polling fallback available

- **WHEN** the SSE stream fails before any event has been delivered
- **THEN** the client is able to fall back to polling the snapshot endpoint through the BFF
- **AND** the fallback is not prevented by any BFF-side state.

### Requirement: Every upstream call SHALL carry an explicit timeout

Every BFF call to the platform API SHALL carry an explicit timeout. A hung upstream SHALL surface to
the browser as a transport failure with actionable copy, and SHALL NOT present as an unbounded loading
state.

#### Scenario: A hung upstream becomes a transport failure

- **WHEN** the platform API accepts a connection but does not respond within the configured timeout
- **THEN** the BFF aborts the call and returns a transport-failure outcome
- **AND** the view renders an error state rather than remaining in a loading state indefinitely.

### Requirement: Platform readiness SHALL account for the console component

The platform readiness signal SHALL aggregate the console component. A healthy platform service with an
unreachable console SHALL NOT report ready, and the degraded component SHALL be named on a
machine-readable endpoint.

#### Scenario: An unreachable console makes readiness report not-ready

- **WHEN** the platform service is healthy but the console component is unreachable
- **THEN** readiness reports not-ready
- **AND** the response names the console as the degraded component.

#### Scenario: Health is readable without a UI

- **WHEN** an operator or a probe needs the console's health
- **THEN** it is obtainable from a machine-readable endpoint
- **AND** the console's own rendered interface is not required as, and is not treated as, the health
  judgement.

### Requirement: BFF telemetry SHALL correlate with platform traces and SHALL exclude sensitive content

BFF logs and traces SHALL correlate with the platform's `trace_id`. They SHALL NOT record prompt text,
diff content, or any credential.

#### Scenario: A BFF trace correlates with the platform trace

- **WHEN** the BFF makes an upstream call as part of handling a browser request
- **THEN** the emitted trace carries the platform's `trace_id`
- **AND** a single request can be followed across the browser, the BFF, and the platform service.

#### Scenario: Sensitive request content is not logged

- **WHEN** the BFF handles a request whose body or upstream response contains prompt text or diff
  content
- **THEN** that content does not appear in any log line, span attribute, or error report
- **AND** this holds on the error path as well as the success path.
