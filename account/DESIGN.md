# Account Service — Design Notes

## Responsibility

This service manages delinquent account records linked to consumers. It is the source of truth for outstanding balance, days past due, and account lifecycle status. The AI agents (Daniel) read from this service to contextualize negotiation; the compliance engine reads it to enforce frequency caps per consumer (not per account — see below).

## Schema Decisions

### No foreign key to `consumers`
`consumer_id BIGINT NOT NULL` references the consumer service's `consumers.id` by convention, but there is no SQL-level `FOREIGN KEY` constraint. Cross-service foreign keys are deliberately avoided in an Encore multi-service architecture because each service owns its own database schema. Referential integrity is enforced at the application layer:

- The `contact` service validates `consumer_id` existence via `consumer.GetConsumer` before starting a workflow. The account service itself does not validate cross-service references at create time — this is accepted in the multi-service architecture where each service owns its own DB.
- The lack of a DB-level FK means a stale `consumer_id` is possible if a consumer record is deleted. Since consumer records are never deleted (only soft-state changes), this is acceptable in v1.

### `account_status` as a PostgreSQL ENUM
Using a native ENUM rather than a TEXT CHECK constraint provides:
1. A database-level catalog of valid states, visible to anyone inspecting the schema.
2. Slightly smaller storage per row (4 bytes vs variable TEXT).
3. Self-documenting in `psql \d accounts`.

The ENUM values mirror the business lifecycle: `current → delinquent → charged_off → settled → closed`. The service enforces valid values but does not enforce transition direction — a `closed` account can be moved back to `current` at the API layer if the business requires it. If strict state-machine enforcement becomes a requirement, add a transition table and validate against it in `UpdateAccountStatus`.

### `balance_due` as NUMERIC(12, 2)
Financial amounts must not use floating-point types (IEEE 754 rounding errors cause cent-level discrepancies in financial calculations). `NUMERIC(12,2)` stores up to $9,999,999,999.99 with exact decimal precision. In Go, this scans into `float64` which is safe for display but should be converted to a `decimal` library type before any arithmetic in future payment plan calculations.

### `account_number` (TEXT, plaintext in v1)
Account numbers are PII under GLBA. In v1, they are stored as plaintext TEXT because:
- The platform is a single-tenant v1 with no external access to the raw DB.
- Encryption at rest adds key management complexity that is out of scope for Phase 1.

**This is a known gap.** The PRD documents it explicitly. The path forward:
1. Write ADR-002 documenting the encryption approach (e.g., `pgcrypto` with AES-256, or application-layer encryption with a KMS-managed key).
2. Add a DB migration that re-encrypts the column using the chosen scheme.
3. Update `CreateAccount` and all `SELECT` paths to encrypt/decrypt transparently.

Until then, access to the `account` database must be restricted to the service process only. No direct DB access for ad hoc queries should be permitted in production.

### `days_past_due` (INT, not computed)
Days past due is denormalized rather than computed from a `due_date` column because:
- The source system (client's loan servicing platform) owns the authoritative DPD calculation.
- The platform receives DPD as a data point at onboarding/sync time, not a date to calculate from.
- Computed DPD would require knowing the payment due date, which varies by loan type and holiday schedules — logic that belongs to the originating system.

## Account Status State Machine

```
current ──► delinquent ──► charged_off ──► settled ──► closed
                │                                         ▲
                └─────────────────────────────────────────┘
                          (direct settle, less common)
```

The service validates that the requested status is a valid enum value but does not enforce transition direction. This keeps the API flexible for edge cases (e.g., a payment that cures a delinquency should go `delinquent → current`, which the state machine allows). If strict enforcement is added, use a transition allowlist:

```go
var allowedTransitions = map[string][]string{
    "current":     {"delinquent"},
    "delinquent":  {"current", "charged_off", "settled"},
    "charged_off": {"settled"},
    "settled":     {"closed"},
    "closed":      {},
}
```

## PII Considerations

### `account_number` — highest sensitivity field
- Contains the consumer's financial account identifier.
- Must not appear in log output. Use `rlog` only for IDs and status, never account numbers.
- Must not appear in error messages returned to API callers.
- Subject to GLBA Safeguards Rule: access must be logged, access control must be least-privilege.

### `balance_due` and `days_past_due`
These are financial attributes that identify the consumer's debt situation. They are less sensitive than account numbers but still regulated under GLBA. Do not include them in any unstructured log messages.

### What the compliance PII sanitizer covers
The compliance service's `SanitizePII` function redacts SSNs, credit cards, and phone numbers from free-text interaction logs. It does not redact account numbers (which are stored in structured DB columns, not free-text). Structured column data is protected by DB access controls, not regex sanitization.

## Frequency Cap Note (FDCPA Reg F)

The FDCPA 7-in-7 frequency cap (7 contact attempts per 7-day rolling window) applies **per consumer**, not per account. A consumer with three delinquent accounts still has a single 7-contact budget shared across all accounts. The compliance service queries `contact_attempts` by `consumer_id` (not `account_id`) when evaluating the frequency cap. This service does not enforce the cap — it just provides the account context; the compliance service enforces it.

## Lifecycle Events

`CreateAccount` and `UpdateAccountStatus` publish to the `account-lifecycle` Pub/Sub topic after each successful DB write. The audit service subscribes and records entries for the entity.

**`CreateAccount`** publishes `{action: "created", new_value: <full account JSON>}`. The full record is embedded so the audit entry captures the initial state without a second DB read.

**`UpdateAccountStatus`** publishes `{action: "status_updated", old_value: {"status": "<prev>"}, new_value: {"status": "<new>"}}`. The previous status is captured atomically in the same SQL `CTE` as the update, so the old/new pair in the event is guaranteed to match the actual transition.

Both publishes are best-effort: a publish failure is logged as an error but does not cause the API to return an error. The DB record is the source of truth; the audit entry is supplementary. Callers are not burdened with retrying a lifecycle event publish.

**Dedup keys** in the audit subscriber:
- Create: `account-lifecycle:<AccountID>:created`
- Status update: `account-lifecycle:<AccountID>:status_updated`

Note: if an account's status is updated twice in rapid succession, both events share the same dedup key format. In practice this is not a problem because the second status update has a different account state (a new `status_updated` event with different content) — but the dedup key does not encode the new value. If strict per-transition dedup is needed, include the new status in the key. For now the risk of a missed update due to dedup collision is accepted as a minor edge case in the audit log, not a compliance gap.

## Error Codes

| Condition | HTTP status | `errs.Code` |
|---|---|---|
| Missing required field | 400 | `InvalidArgument` |
| Negative balance_due | 400 | `InvalidArgument` |
| Invalid status value | 400 | `InvalidArgument` |
| Account not found | 404 | `NotFound` |
