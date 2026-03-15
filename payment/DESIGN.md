# Payment Service — Design Notes

## Responsibility

This service manages payment plan lifecycle for delinquent accounts. It is the source of truth for plan status, installment tracking, and payment history. The AI agents (Daniel) call `POST /payment-plans` to propose a plan when a consumer agrees to a payment arrangement, and the platform tracks payments through completion or default.

## Schema Decisions

### `plan_status` as a PostgreSQL ENUM
Using a native ENUM (`proposed`, `accepted`, `active`, `completed`, `defaulted`) rather than a TEXT CHECK constraint, matching the pattern established by the account service's `account_status` enum.

### `total_amount` and `installment_amt` as NUMERIC(12, 2)
Financial amounts use exact decimal precision, not floating-point. Same rationale as `accounts.balance_due` — IEEE 754 rounding errors cause cent-level discrepancies. In Go, these scan into `float64` which is safe for display and comparison but should be converted to a `decimal` type before arithmetic in production.

### `payment_events` as a separate table
Payment events form an append-only timeline for each plan. Using a separate table (rather than a JSONB array on the plan) enables:
1. Efficient SUM queries for completion detection (`COALESCE(SUM(amount), 0)`).
2. Individual event timestamps and metadata without array manipulation.
3. Foreign key integrity via `REFERENCES payment_plans(id)`.

### No foreign key to `accounts`
`account_id BIGINT NOT NULL` references the account service's `accounts.id` by convention. Cross-service foreign keys are avoided in the Encore multi-service architecture — same pattern as account → consumer.

### `frequency` as TEXT (not ENUM)
Frequency values (`weekly`, `biweekly`, `monthly`) are validated at the application layer. Using TEXT keeps the schema flexible if new frequencies are added without a migration.

## Payment Plan State Machine

```
proposed ──► accepted ──► active ──► completed
    │            │           │
    │            │           └──► defaulted (3+ missed payments via workflow)
    │            └──► defaulted (via workflow timeout — no acceptance within 72h)
    └──► defaulted (via workflow timeout)
```

### Transition Rules

| From | To | Trigger |
|---|---|---|
| `proposed` | `accepted` | `AcceptPlan` API call |
| `proposed` | `defaulted` | `PaymentPlanWorkflow` 72h acceptance timeout |
| `accepted` | `active` | First `RecordPayment` call |
| `active` | `completed` | `RecordPayment` when sum ≥ `total_amount` |
| `active` | `defaulted` | `PaymentPlanWorkflow` — 3 missed installments |
| `accepted`/`active` | `completed` | `MarkCompleted` private endpoint (workflow callback) |
| `accepted`/`active` | `defaulted` | `MarkDefaulted` private endpoint (workflow callback) |

### Dual Completion Detection

Completion can be triggered in two ways:
1. **Synchronous** — `RecordPayment` detects `SUM(amount) >= total_amount` within the same transaction and updates status to `completed`. This is the primary path.
2. **Workflow** — `PaymentPlanWorkflow` calls `MarkPlanCompleted` after tracking all installments. This is a fallback/confirmation path.

The first path to fire wins. Both `MarkDefaulted` and `MarkCompleted` are idempotent at the code level: they read the current status before the transaction and return early (without re-inserting events or re-publishing) if the plan is already in a terminal state (`completed` or `defaulted`). This prevents Temporal's retry policy from emitting duplicate audit events.

## Transactional Consistency

All state-changing endpoints (`ProposePlan`, `AcceptPlan`, `RecordPayment`, `MarkDefaulted`, `MarkCompleted`) use database transactions. Each transaction:
1. Updates the `payment_plans` row.
2. Inserts a `payment_events` row.
3. Commits atomically.

Pub/Sub events are published **after** the transaction commits. This is best-effort — a publish failure is logged but does not roll back the DB change. The DB is the source of truth; audit entries are supplementary.

## Event Publishing

Every state change publishes a `PaymentUpdatedEvent` to the `payment-updated` Pub/Sub topic:

| Trigger | `EventType` value |
|---|---|
| `ProposePlan` | `proposed` |
| `AcceptPlan` | `accepted` |
| `RecordPayment` (first) | `active` + `payment_received` |
| `RecordPayment` (subsequent) | `payment_received` |
| `RecordPayment` (final) | `payment_received` + `completed` |
| `MarkDefaulted` | `defaulted` |
| `MarkCompleted` | `completed` |

The `OccurredAt` field is passed explicitly to `publishPaymentEvent`. For `payment_received` events, the DB-generated `occurred_at` from the `payment_events` INSERT is used rather than `time.Now()`. This ensures the audit dedup key (`payment-updated:{PlanID}:payment_received:{occurred_at}`) is stable across Temporal activity retries — if an activity is retried after the DB write succeeds but before the publish returns, the same message will carry the same timestamp and be correctly deduplicated by the audit subscriber. All other event types use `time.Now()` since their dedup keys do not include a timestamp.

## Private Endpoints

`MarkDefaulted` and `MarkCompleted` are `//encore:api private` — they are not exposed to external callers. They exist as HTTP endpoints because the Temporal worker process cannot import Encore packages and must interact via HTTP (same pattern as `contact/internal/publish-*`).

## PII Considerations

Financial amounts (`total_amount`, `installment_amt`, payment `amount`) are business data, not PII. However, they are associated with a consumer's debt situation and should not appear in unstructured log messages. The service logs plan ID and account ID but not financial amounts on error paths.

## Audit Integration

The audit subscriber maps payment events as:
- `EntityType` → `"payment_plan"`
- `EntityID` → `event.PlanID`
- `Action` → `event.EventType`
- Dedup key: `payment-updated:{PlanID}:{EventType}` for all event types, except `payment_received` which uses `payment-updated:{PlanID}:payment_received:{occurred_at}` to distinguish individual installment payments.

Each installment payment now produces its own audit entry because the dedup key includes the DB-generated `occurred_at` timestamp. For a full payment history, either the audit log or `GET /payment-plans/:id` (which returns the `payment_events` table) can be used.

## Error Codes

| Condition | HTTP status | `errs.Code` |
|---|---|---|
| Missing or invalid required field | 400 | `InvalidArgument` |
| Invalid frequency value | 400 | `InvalidArgument` |
| Plan not found | 404 | `NotFound` |
| Accept non-proposed plan | 400 | `InvalidArgument` |
| Payment on non-accepted/active plan | 400 | `InvalidArgument` |

## Test Coverage

`payment_test.go` — 4 test functions with ~13 table-driven cases:
- `TestProposePlan` — valid proposal, weekly frequency, missing account_id, zero total_amount, invalid frequency
- `TestAcceptPlan` — accept proposed plan, accept already-accepted plan (err), accept non-existent plan (err)
- `TestRecordPayment` — first payment (accepted→active), second payment (stays active), final payment (triggers completed), payment on proposed plan (err)
- `TestGetPlan` — existing plan, non-existent plan (NotFound)
