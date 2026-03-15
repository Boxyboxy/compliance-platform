# Audit Service — Design Notes

## Responsibility

This service provides an immutable, append-only record of every state change across the platform. It serves two audiences:

1. **Compliance officers** — who need to pull a complete history of any entity (consumer, account, contact, payment_plan) for regulatory examiners.
2. **Ops team** — who need to trace what happened to a specific contact attempt or consent change.

The service owns the `audit_log` table and exposes three APIs: a private `RecordAudit` endpoint for direct writes, a public `GetAuditLog` endpoint for basic entity queries, and a public `SearchAuditLog` endpoint for filtered queries.

## Append-Only Design

The `audit_log` table is designed as append-only. No `UPDATE` or `DELETE` operations exist in the codebase, and the constraint is now enforced at the database level:

- **Migration `2_enforce_append_only.up.sql`** installs a `BEFORE UPDATE OR DELETE` trigger (`audit_log_immutable`) that raises an exception for any mutation attempt. This is belt-and-suspenders with code-level convention — the trigger catches application bugs and rogue admin queries alike.

## Schema

```sql
CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    entity_type TEXT NOT NULL,     -- 'consumer', 'account', 'contact', 'payment_plan'
    entity_id   BIGINT NOT NULL,
    action      TEXT NOT NULL,     -- 'created', 'consent_revoked', 'consent_granted', 'status_updated', 'contact_attempted', ...
    actor       TEXT NOT NULL,     -- 'system:pubsub', 'api'
    old_value   JSONB,
    new_value   JSONB,
    metadata    JSONB,             -- {"correlation_id": "...", "dedup_key": "..."}
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_entity ON audit_log(entity_type, entity_id);
CREATE INDEX idx_audit_time   ON audit_log(created_at);
```

**JSONB for old/new values.** Different entity types have different shapes. JSONB allows the audit log to store any entity's state without schema coupling. The audit service treats these blobs as opaque — it stores and returns them without interpretation.

**`metadata` for cross-service tracing and idempotency.** Subscribers store both `correlation_id` (for tracing) and `dedup_key` (for duplicate detection) in the same metadata blob. The query `metadata->>'dedup_key' = $1` is used by the idempotency check.

## Shared Write Path — `recordAuditEntry`

Both the `RecordAudit` API handler and all Pub/Sub subscribers use the same internal `recordAuditEntry` function. This ensures:

1. **Consistent validation.** All writes go through the same validation (entity_type, entity_id, action, actor required).
2. **Consistent error codes.** Validation errors return `errs.InvalidArgument`, matching the pattern used by other services.
3. **Single code path.** No divergence between API-initiated and event-initiated audit entries.

## API Design: GetAuditLog vs SearchAuditLog

Encore requires path parameters to be individual function parameters, not embedded in a struct. This prevents adding optional query params (action, since, until) to a GET endpoint with path params without changing the function signature in a way Encore's code generator doesn't support.

**Solution**: two endpoints sharing a single implementation (`queryAuditLog`):

```
GET /audit/:entityType/:entityId     → GetAuditLog  (no filters; backward compatible)
POST /audit/search                   → SearchAuditLog (full filter support via JSON body)
```

`GetAuditLogParams` carries all fields (entity_type, entity_id, action, since, until). The GET endpoint fills the first two; the POST endpoint accepts the full struct.

## Pub/Sub Subscribers

All 6 subscribers are wired in `subscribers.go`:

| Topic | Subscription | Action recorded | Entity type |
|---|---|---|---|
| `contact-attempted` | `audit-contact-attempted` | `contact_attempted` | `contact` |
| `interaction-created` | `audit-interaction-created` | `interaction_created` | `contact` |
| `consent-changed` | `audit-consent-changed` | `consent_revoked` or `consent_granted` | `consumer` |
| `consumer-lifecycle` | `audit-consumer-lifecycle` | event.Action (e.g. `created`) | `consumer` |
| `account-lifecycle` | `audit-account-lifecycle` | event.Action (e.g. `created`, `status_updated`) | `account` |
| `payment-updated` | `audit-payment-updated` | event.EventType (e.g. `proposed`, `accepted`, `active`, `payment_received`, `completed`, `defaulted`) | `payment_plan` |

The consent-changed subscriber derives the action from `event.ConsentStatus`: `"revoked"` → `"consent_revoked"`, anything else → `"consent_granted"`.

## Idempotency

Encore Pub/Sub delivers events at-least-once. Without dedup, a retried delivery would produce a duplicate audit entry. For an append-only log this is generally harmless (a duplicate row is not a compliance violation), but it produces confusing history for ops.

**Implementation**: each handler computes a deterministic dedup key before inserting, then calls `isDuplicate(ctx, key)`:

```go
// isDuplicate queries the existing audit log.
// On error, returns false so we never silently drop events.
func isDuplicate(ctx context.Context, dedupKey string) bool {
    var exists bool
    db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audit_log WHERE metadata->>'dedup_key' = $1)`, dedupKey).Scan(&exists)
    return exists
}
```

Dedup key formats:
- `contact-attempted:<ContactAttemptID>`
- `interaction-created:<ContactAttemptID>`
- `consent-changed:<ConsumerID>:<ConsentStatus>:<ChangedAt>` (includes status so revoke and grant at the same second are distinct)
- `consumer-lifecycle:<ConsumerID>:<Action>`
- `account-lifecycle:<AccountID>:<Action>`
- `payment-updated:<PlanID>:<EventType>` for all payment event types except `payment_received`
- `payment-updated:<PlanID>:payment_received:<occurred_at>` (RFC3339) — includes the DB-generated timestamp so each installment payment gets a distinct audit entry while still deduplicating genuine Pub/Sub redeliveries of the same message

Duplicates are logged at Debug level and return nil (not an error — the handler completed successfully).

## Error Codes

| Condition | HTTP status | `errs.Code` |
|---|---|---|
| Missing `entity_type` | 400 | `InvalidArgument` |
| Missing `entity_id` | 400 | `InvalidArgument` |
| Missing `action` | 400 | `InvalidArgument` |
| Missing `actor` | 400 | `InvalidArgument` |
| Invalid `since`/`until` format | 400 | `InvalidArgument` |

No 404 cases — `GetAuditLog` and `SearchAuditLog` return an empty list for entities with no matching history.

## Test Coverage

- `audit_test.go` — RecordAudit (valid/minimal/validation failures), GetAuditLog (entries/empty/DESC order), action filter, time range filter, idempotency (isDuplicate), append-only enforcement (trigger), and direct handler tests for all 4 new subscriber types.
- `integration_test.go` — End-to-end pipeline: create consumer + account, update status, revoke/grant consent, verify filtered queries by action and time range.
