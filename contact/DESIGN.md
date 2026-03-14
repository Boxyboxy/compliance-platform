# Contact Service — Design Notes

## Responsibility

The contact service is the API entry point for all outbound contact attempts. It owns three things:

1. **Workflow trigger** — validates the request, inserts a `pending` row, and fires a Temporal workflow to handle the actual work.
2. **Contact history** — stores the outcome of every attempt (status, sanitized content, compliance result, scorecard) as written back by the workflow.
3. **Consent propagation** — subscribes to `consent-changed` and immediately cancels any pending workflows for that consumer.

The service deliberately does not run compliance logic itself. All rules, PII sanitization, and scoring happen inside the Temporal workflow. This keeps the API handler fast and keeps compliance logic in one place.

## API Surface

| Method | Path | Visibility | Purpose |
|---|---|---|---|
| `POST` | `/contact/initiate` | public | Validate, insert pending row, start workflow |
| `GET` | `/consumers/:consumerId/contacts` | public | Contact history for a consumer |
| `POST` | `/contact/attempts/:id/result` | private | Workflow callback — write final status and results |
| `POST` | `/contact/internal/publish-attempted` | private | Workflow callback — publish `contact-attempted` event |
| `POST` | `/contact/internal/publish-interaction` | private | Workflow callback — publish `interaction-created` event |

The two `internal/publish-*` endpoints exist because the Temporal worker process cannot import Encore packages and therefore cannot call `pubsub.Publish` directly. The worker completes its work, then calls these private Encore endpoints to emit events. Encore handles deduplication and at-least-once delivery guarantees on its side.

## `InitiateContact` Sequence

```
POST /contact/initiate
  │
  ├─ validate channel, IDs, message_content
  ├─ consumer.GetConsumer(consumerID)      ← cross-service call (Encore generated client)
  ├─ account.GetAccount(accountID)        ← cross-service call
  ├─ assert account.ConsumerID == consumerID
  ├─ INSERT contact_attempts (status='pending')
  ├─ SELECT attempted_at WHERE consumer_id AND attempted_at >= now()-7d
  │   └─ passes timestamps to workflow (compliance check is stateless)
  ├─ build workflows.ContactWorkflowInput (typed struct, not map[string]interface{})
  ├─ temporalClient.ExecuteWorkflow(workflows.ContactWorkflow, input)
  └─ UPDATE contact_attempts SET workflow_id = run.GetID()
```

The workflow is started using the typed `workflows.ContactWorkflowInput` struct and the `workflows.ContactWorkflow` function reference. This provides compile-time type safety — a missing or mistyped field is caught at build time rather than at Temporal deserialization time.

The pending row is inserted _before_ the workflow starts. If the workflow fails to start (Temporal unavailable), the row remains in `pending`. A future retry from the caller will insert a second row with a new workflow. This is safe: the frequency cap check inside the workflow will observe both rows' timestamps and may block the retry if the cap is reached.

## Why Recent Timestamps Are Passed in Workflow Input

The frequency cap rule in the compliance service is stateless — it operates on a list of timestamps provided by the caller. The contact service queries its own database for the 7-day window and passes the results into `ContactWorkflowInput.RecentContactTimestamps`.

Alternatives considered:

**Option A: Compliance service queries the contact DB directly.** Rejected. Creates a cross-service database dependency and a circular import (contact calls compliance; compliance would call contact).

**Option B: Compliance service calls the contact HTTP API.** Rejected. Adds a network round-trip inside the compliance hot path, which has a < 50ms p99 target.

**Option C (chosen): Contact service assembles timestamps and passes them in.** The contact service already owns this data. Passing it into the workflow input keeps compliance stateless, avoids circular calls, and adds no latency to the compliance check itself.

## Lazy Temporal Client

The Temporal client is initialized via `sync.Once` rather than at service startup. This has one practical consequence: tests that only exercise `ListContacts` or `UpdateContactResult` never attempt to connect to Temporal. A test environment without a running Temporal server can still run those tests without connection errors.

The tradeoff: the first `InitiateContact` call pays the connection cost. This is acceptable — Temporal connection is fast and the alternative (failing all tests that don't need Temporal) would require more invasive test infrastructure.

## Consent Revocation Subscriber

`subscribers.go` subscribes to `consumer.ConsentChanged`. On a `revoked` event, it runs a single `UPDATE`:

```sql
UPDATE contact_attempts
SET status = 'blocked', block_reason = 'consent_revoked', completed_at = now()
WHERE consumer_id = $1 AND status = 'pending'
```

This is intentionally a bulk update rather than per-row. Pending contacts for a consumer are typically zero or one; in exceptional cases (burst) there may be a handful. The single statement handles all of them atomically.

The handler returns the error on DB failure, which causes Encore's Pub/Sub to redeliver. This is the correct behavior: if the DB is unavailable, we must not silently drop the revocation.

`granted` events are ignored — granting consent does not unblock contacts that were already blocked for other reasons (e.g., frequency cap, attorney flag).

## Data Model

```sql
CREATE TABLE contact_attempts (
    id                BIGSERIAL PRIMARY KEY,
    consumer_id       BIGINT NOT NULL,
    account_id        BIGINT NOT NULL,
    channel           TEXT NOT NULL CHECK (channel IN ('sms','email','voice')),
    status            TEXT NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending','blocked','sent','delivered','failed')),
    block_reason      TEXT,
    workflow_id       TEXT,
    message_content   TEXT,        -- always the PII-sanitized version after workflow completes
    compliance_result JSONB,       -- full ContactCheckResult blob
    scorecard_result  JSONB,       -- full ScoreResponse blob
    attempted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at      TIMESTAMPTZ
);
```

**JSONB for compliance and scorecard results.** These blobs are opaque to the contact service — it stores and returns them verbatim. Using JSONB rather than TEXT enables future Postgres queries against the JSON fields without a schema migration (e.g., `WHERE compliance_result->>'allowed' = 'false'`).

**`message_content` is always sanitized.** The raw message from the caller is stored in the pending row at insert time, but `UpdateContactResult` overwrites it with the sanitized version from the workflow. After the workflow completes, the DB never holds unsanitized content.

**`status` lifecycle:**

```
pending → blocked    (compliance check failed, or consent revoked via event)
pending → delivered  (workflow completed, channel delivery succeeded)
pending → failed     (workflow completed, channel delivery failed)
```

`sent` is reserved for future use (async delivery confirmation). Currently all delivery simulation resolves to `delivered` or `failed` synchronously within the workflow.

## Pub/Sub Topics

Both topics use `AtLeastOnce` delivery. Subscribers must be idempotent.

| Topic | Published when | Subscribers |
|---|---|---|
| `contact-attempted` | Every attempt concludes (blocked or delivered/failed) | `audit`, `scoring` |
| `interaction-created` | Attempt delivered or failed — includes sanitized content and scorecard | `audit`, `scoring` |

`contact-attempted` fires for both blocked and non-blocked attempts so the audit log has a complete record of every contact attempt, not just successful ones.

`interaction-created` fires only after the full workflow completes (sanitize + deliver + score). Subscribers receive sanitized content and the scorecard result embedded in the event so they don't need to query the contact service.

## Metrics

| Metric | Type | Labels |
|---|---|---|
| `contact_attempt_total` | CounterGroup | `channel`, `outcome` |
| `contact_workflow_duration_ms` | Gauge | — |

`contact_workflow_duration_ms` uses a Gauge (not a Histogram) because Encore v1.52 does not expose a Histogram type. Switch to Histogram once available to support p99 alerting.

## Error Codes

| Condition | Code |
|---|---|
| Missing `consumer_id`, `account_id`, `message_content` | `InvalidArgument` |
| Invalid `channel` | `InvalidArgument` |
| Account does not belong to consumer | `InvalidArgument` |
| Consumer or account not found | `NotFound` (propagated from upstream service) |
| Temporal unavailable | `Internal` (wrapped error) |
