# Scoring Service — Design Notes

## Responsibility

The scoring service is an async QA subscriber. It listens to `interaction-created` events and (in Phase 4) will re-evaluate interaction transcripts against per-client scorecard rubrics, writing results back for ops-team review.

Phase 3 delivers the service skeleton: the subscription is wired, the service struct is registered, and the handler logs receipt. The scoring logic lives in Phase 4.

## Why a Separate Service

The inline scoring path already exists inside the contact workflow (`ScoreInteraction` activity calls `POST /compliance/score`). That inline score runs synchronously and its result is stored on the `contact_attempts` row before the workflow completes.

The scoring service provides a second, async scoring pass. Use cases:

1. **Rubric updates.** When a client changes their rubric, previously scored interactions need to be re-evaluated. The scoring service can replay events or re-query the DB without touching the workflow.
2. **Per-client rubrics.** The inline workflow uses a hardcoded default rubric. Phase 4 will look up the correct rubric for the client (account → client config) and produce a client-specific score.
3. **Decoupled from delivery latency.** Scoring is advisory, not blocking. Async scoring means a slow rubric evaluation or a client config lookup never delays the contact workflow's `contact_workflow_duration_ms` p99.

## Subscription

```go
var _ = pubsub.NewSubscription(
    contact.InteractionCreated,
    "scoring-interaction-created",
    pubsub.SubscriptionConfig[*contact.InteractionCreatedEvent]{
        Handler: handleInteractionCreated,
    },
)
```

The subscription name `"scoring-interaction-created"` is stable — Encore uses it to identify this consumer's offset in the topic. Renaming it would reset the offset and reprocess all historical events.

`AtLeastOnce` delivery means the handler must be idempotent. A future implementation writing a score back to the DB should use an `INSERT ... ON CONFLICT DO UPDATE` (upsert) rather than a plain INSERT.

## Event Payload

`InteractionCreatedEvent` carries the sanitized content and the inline scorecard result:

```go
type InteractionCreatedEvent struct {
    ContactAttemptID int64
    ConsumerID       int64
    AccountID        int64
    Channel          string
    SanitizedContent string          // PII-redacted transcript from the workflow
    ScorecardResult  json.RawMessage // inline score from the workflow
    CorrelationID    string
    Timestamp        time.Time
}
```

The sanitized content is embedded in the event so the scoring service does not need to call back to the contact service to retrieve it. This avoids a point-in-time consistency issue (the content is guaranteed to be the same version the workflow scored against) and removes the cross-service dependency on the read path.

## Phase 4 Implementation Plan

The handler stub will grow to:

1. Look up the per-client rubric from a config store (account → client ID → rubric JSON).
2. Call `POST /compliance/score` with the event's `SanitizedContent` and the client-specific rubric.
3. Write the result back to `contact_attempts.scorecard_result` (upsert) or a dedicated `qa_scores` table.
4. Publish a `qa-score-updated` event if downstream analytics subscribers are needed.

The `CorrelationID` from the event should be included in all log calls to enable cross-service log correlation with the originating workflow execution.

## What This Service Does Not Own

- **Scorecard rubric definitions.** Rubrics are JSON config, not stored in this service's DB. Phase 4 will fetch them from wherever the client config lives (likely a `client_config` table in the account service or a dedicated config service).
- **The compliance/score logic.** The evaluator lives in the `compliance` service. This service calls it — it does not re-implement it.
- **The inline score.** The score stored in `contact_attempts.scorecard_result` by the workflow is written by the contact service. This service may update it or write to a separate table, but it does not own the initial write.
