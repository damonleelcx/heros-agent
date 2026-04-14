# Tool Response Contract

This document defines the strict response contract for all extension tools.

## Required Top-Level Fields

Every tool response must include:

- `tool_id` (string): canonical tool id.
- `status` (string): one of `ok` or `error`.
- `action` (string): resolved action for the call.
- `audit` (object): required audit payload described below.

## Required `audit` Schema

The `audit` object must always include string values for:

- `timestamp` (RFC3339 UTC recommended)
- `session_id`
- `workdir`
- `action`

## Error Contract

When `status == "error"`, responses must also include:

- `error_code` (string, must be in whitelist)
- `error` (string, human-readable)

Allowed `error_code` values are defined in:

- `internal/toolcontract/errors.go` (shared source of truth)
- `internal/cliagent/tool_response_contract.go` (cliagent-facing aliases/wrappers)

Current whitelist:

- `INVALID_ACTION`
- `VALIDATION_ERROR`
- `PERMISSION_DENIED`
- `POLICY_BLOCKED`
- `IO_ERROR`
- `NOT_FOUND`
- `COMMAND_FAILED`
- `NETWORK_ERROR`
- `UNKNOWN_ERROR`

## Enforcement Points

Runtime normalization and enforcement happen in:

- `internal/cliagent/tools_runtime_registry.go` via `normalizeToolResponse()`

Contract regression tests are in:

- `internal/cliagent/tools_contract_all_test.go`

## Guidance for New Tools

When adding a new tool:

1. Implement tool behavior in that tool's own `tool.go`.
2. Return `tool_id`, `status`, `action`, and `audit`.
3. Use only whitelisted `error_code` values.
4. Wire the tool through the extension module registry.
5. Run `go test ./internal/cliagent/...` to validate contract compliance.
