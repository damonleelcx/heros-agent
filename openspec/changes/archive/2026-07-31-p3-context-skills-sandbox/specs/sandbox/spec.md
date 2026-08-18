# Sandbox — Spec Delta (P3)

Product rationale: [`../../../../docs/prd/P3-context-skills-sandbox.md`](../../../../docs/prd/P3-context-skills-sandbox.md) §6 (FR12–FR19), §7 (threat model).

Skills execute arbitrary tool code from the target repo — treat it as untrusted. This is the
project's sharpest security boundary: every node running repo tool code is isolated, holds no
ambient credentials, and operates under least-privilege network + filesystem with hard resource
bounds. All requirements below are first-class security requirements.

## ADDED Requirements

### Requirement: Repo tool code SHALL execute only inside a per-node isolate, never on the host

Each node whose execution involves skill/tool code from the target repo SHALL run inside an isolate
(subprocess or container) separate from the host process. Discovered tool code SHALL run only inside
the isolate; there SHALL be no configuration that runs it on the host.

#### Scenario: A node using a repo tool runs in the isolate
- **WHEN** a node bound to a target-repo tool executes
- **THEN** the tool code runs inside a subprocess/container isolate, not in the host process
- **AND** the isolate is separate from the control plane and from other runs

### Requirement: The isolate SHALL start with no ambient credentials

The isolate SHALL start with a scrubbed environment: no provider API keys, no cloud credential
files, no secrets-manager access, and no inherited environment secrets. A tool that reads the
environment, credential files, or the cloud metadata endpoint SHALL find no usable credential.

#### Scenario: A credential-reading tool finds nothing usable
- **WHEN** a repo tool inside the isolate reads environment variables, common credential file paths,
  and the cloud metadata endpoint
- **THEN** it obtains no usable provider API key or cloud credential
- **AND** the metadata endpoint is unreachable from the isolate

### Requirement: The isolate SHALL enforce least-privilege network egress with default-deny

The isolate SHALL default-deny all outbound network egress; only an explicit allowlist SHALL be
permitted (default empty). Any un-allowlisted outbound connection attempt SHALL be blocked and
recorded as a denial event.

#### Scenario: A repo tool attempting egress is denied
- **WHEN** a repo tool inside the isolate attempts an outbound network connection to a host not on
  the allowlist
- **THEN** the connection is blocked
- **AND** a denial event is recorded with no secret values leaked

#### Scenario: Only allowlisted hosts are reachable
- **WHEN** an egress allowlist naming a single host is configured for a skill and the tool connects
  to that host
- **THEN** the connection is permitted, and connections to all other hosts remain denied

### Requirement: The isolate SHALL enforce a least-privilege read-only filesystem scoped to the node's working set

The isolate SHALL expose a minimal, read-only-by-default filesystem view scoped to the node's
declared working set. It SHALL NOT expose the host filesystem, other runs' data, the repo's `.git`
credentials, or the secrets store. Writes SHALL be confined to an ephemeral scratch area destroyed
with the isolate.

#### Scenario: Filesystem-scope violation denied
- **WHEN** a repo tool attempts to read a host path, another run's data, or the secrets store outside
  its declared working set
- **THEN** the access is denied
- **AND** a denial event is recorded

### Requirement: The isolate SHALL enforce resource bounds and fail the node closed on breach

The isolate SHALL enforce per-node bounds on CPU, memory, wall-clock time, process/thread count, and
captured output size. On breach the isolate SHALL be terminated and the node SHALL fail closed with a
typed resource error, without affecting other runs or the host.

#### Scenario: A resource-exhausting tool is contained
- **WHEN** a repo tool inside the isolate forks unboundedly, allocates beyond the memory limit, or
  runs past the wall-clock timeout
- **THEN** the isolate is terminated and the node fails closed with a typed resource error
- **AND** a second concurrent run on the host is unaffected

### Requirement: A sandboxed node's credentialed calls SHALL be brokered by the trusted host

Provider/model and retrieval calls a sandboxed node needs SHALL be brokered by the trusted host over
a narrow, audited channel; the host performs the call via the provider gateway and returns only the
result. The isolate SHALL NOT hold or receive the provider credential, and the broker SHALL apply
the same allowlist and validation rules as direct egress.

#### Scenario: Brokered call succeeds without the isolate holding a credential
- **WHEN** a sandboxed tool requests an LLM call through the broker
- **THEN** the trusted host performs the call via the gateway and returns only the result
- **AND** the isolate never receives the provider credential

#### Scenario: Broker cannot be used to bypass egress
- **WHEN** a sandboxed tool requests, through the broker, a call to a host not on the allowlist
- **THEN** the broker denies the request
- **AND** a denial event is recorded

### Requirement: Sandbox isolation SHALL fail closed

If an isolate cannot be created with all required restrictions (no ambient credentials, default-deny
egress, scoped read-only filesystem, resource bounds), the node SHALL NOT execute the untrusted tool
code. There SHALL be no fallback to executing the tool on the host.

#### Scenario: Un-creatable isolate does not fall back to the host
- **WHEN** the sandbox runner cannot create an isolate with the required restrictions
- **THEN** the node fails closed with a typed error
- **AND** the untrusted tool code is not executed on the host

### Requirement: The sandbox SHALL emit tagged audit events for lifecycle and every denied action without leaking secrets

Sandbox isolate lifecycle transitions and every denied action (egress block, credential-read
attempt, resource breach, filesystem-scope violation) SHALL emit a telemetry event tagged with the
P0 tag set for audit. No secret value SHALL appear in any event.

#### Scenario: Denials are auditable and secret-free
- **WHEN** any denied action or a resource breach occurs in the isolate
- **THEN** a tagged audit event is emitted identifying the node, the action, and the reason
- **AND** the event contains no provider key, credential, or other secret value
