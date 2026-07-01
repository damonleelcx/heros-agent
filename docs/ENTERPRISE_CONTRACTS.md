# Enterprise Contracts

This repo currently treats the following as the public boundary for fleet features.

## Proposal vs approved artifact

- `proposal` means a mutation is pending human review.
- `approved artifact` means a mutation is safe to apply and sync.
- Producers must not blur these channels.
- Consumers must preserve both identifiers and decision history.

## Inbox message schema

```json
{
  "message_id": "uuid",
  "tenant_id": "tenant-a",
  "payload_type": "skill|tool|memory-link-batch|memory-entity",
  "payload_version": 1,
  "signature": "base64",
  "created_at": "2026-06-25T00:00:00Z",
  "expire_at": "2026-07-25T00:00:00Z"
}
```

Lifecycle:

1. `received`
2. `verified`
3. `applied`
4. `acked`

Failures move to `retry` and then `dead-letter`.

## Memory versioning

Authoritative on-disk memory should carry schema versions, TTLs, and provenance for every link. Quarantined or corrupt files should be isolated rather than silently repaired in place.

## Observability minimums

- structured logs with request, session, tenant, node, and trace IDs
- request latency and error counters
- trace propagation across HTTP and internal events
- a runbook covering upgrade, rollback, and sync failures
