# Audit Service — Design Notes

## Responsibility

This service provides an immutable, append-only record of every state change across the platform. It serves two audiences:

1. **Compliance officers** — who need to pull a complete history of any entity (consumer, account, contact) for regulatory examiners.
2. **Ops team** — who need to trace what happened to a specific contact attempt or consent change.

The service owns the `audit_log` table and exposes two APIs: a private `RecordAudit` endpoint for direct writes and a public `GetAuditLog` endpoint for queries.

## Append-Only Design

The `audit_log` table is designed as append-only at the application layer. No `UPDATE` or `DELETE` operations exist in the codebase. This is a regulatory requirement — examiners must trust that the audit trail has not been tampered with.

**Database-level enforcement** is not yet implemented. A future migration should add a trigger or policy to prevent `UPDATE`/`DELETE` at the Postgres level, independent of the application code. Until then, the constraint is enforced by code review and the absence of any `UPDATE` or `DELETE` queries in the service.

## Schema

```sql
CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    entity_type TEXT NOT NULL,     -- 'consumer', 'account', 'contact', 'payment_plan'
    entity_id   BIGINT NOT NULL,
    action      TEXT NOT NULL,     -- 'created', 'updated', 'consent_revoked', 'contact_attempted'
    actor       TEXT NOT NULL,     -- 'system', 'api', 'system:pubsub', 'workflow:contact-123'
    old_value   JSONB,
    new_value   JSONB,
    metadata    JSONB,             -- correlation_id, workflow_id, etc.
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_entity ON audit_log(entity_type, entity_id);
CREATE INDEX idx_audit_time   ON audit_log(created_at);
```

**JSONB for old/new values.** Different entity types have different shapes. JSONB allows the audit log to store any entity's state without schema coupling. The audit service treats these blobs as opaque — it stores and returns them without interpretation.

**`metadata` for cross-service tracing.** Subscribers include `correlation_id` in metadata so audit entries can be correlated with Temporal workflow executions and Encore request traces.

## Shared Write Path — `recordAuditEntry`

Both the `RecordAudit` API handler and the Pub/Sub subscribers use the same internal `recordAuditEntry` function. This ensures:

1. **Consistent validation.** All writes go through the same validation (entity_type, entity_id, action, actor required).
2. **Consistent error codes.** Validation errors return `errs.InvalidArgument`, matching the pattern used by consumer, account, and contact services.
3. **Single code path.** No divergence between API-initiated and event-initiated audit entries.

The API handler is a thin wrapper that delegates to `recordAuditEntry`. Subscribers build a `RecordAuditReq` from the event payload and call the same function.

## Pub/Sub Subscribers

| Topic | Subscription Name | What is recorded |
|---|---|---|
| `contact-attempted` | `audit-contact-attempted` | Full event payload as `new_value`; action = `contact_attempted` |
| `interaction-created` | `audit-interaction-created` | Full event payload as `new_value`; action = `interaction_created` |

Both handlers are idempotent — duplicate events produce duplicate audit entries, which is acceptable for an append-only log (and preferred over losing entries). The `correlation_id` from each event is stored in `metadata` for traceability.

**Future subscribers** (Phase 4): `consent-changed` and `payment-updated` topics will also produce audit entries.

## Error Codes

| Condition | HTTP status | `errs.Code` |
|---|---|---|
| Missing `entity_type` | 400 | `InvalidArgument` |
| Missing `entity_id` | 400 | `InvalidArgument` |
| Missing `action` | 400 | `InvalidArgument` |
| Missing `actor` | 400 | `InvalidArgument` |

No 404 cases — `GetAuditLog` returns an empty list for entities with no audit history.

## Test Coverage

- `audit_test.go` — table-driven tests for `RecordAudit` (valid entries, minimal fields, missing required fields) and `GetAuditLog` (entities with entries, empty results, DESC ordering).
