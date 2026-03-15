# Scoring Service — Design Notes

## Responsibility

The scoring service is an async QA subscriber. It listens to `interaction-created` events, evaluates interaction transcripts against a scorecard rubric, and writes results back to the contact attempt record via a private PATCH endpoint on the contact service.

## Why a Separate Service

The inline scoring path already exists inside the contact workflow (`ScoreInteraction` activity calls `POST /compliance/score`). That inline score runs synchronously and its result is stored on the `contact_attempts` row before the workflow completes.

The scoring service provides a second, async scoring pass. Use cases:

1. **Rubric updates.** When a client changes their rubric, previously scored interactions can be re-scored by replaying events or re-querying — without touching the workflow.
2. **Per-client rubrics.** The inline workflow uses a hardcoded default rubric. A future version of this service will look up the correct rubric for the client (account → client config → rubric JSON) and produce a client-specific score.
3. **Decoupled from delivery latency.** Scoring is advisory, not blocking. Async scoring means a slow rubric evaluation never delays the contact workflow's `contact_workflow_duration_ms` p99.

## Current Implementation (Phase 4)

### Handler Flow

```
handleInteractionCreated(event)
  ├─ Skip if SanitizedContent == "" (blocked contacts have no transcript)
  ├─ Call compliance.ScoreInteraction with defaultRubric()
  ├─ Marshal ScoreResponse to JSON
  └─ Call contact.UpdateScorecardResult (PATCH /contact/attempts/:id/scorecard)
```

### Default Rubric

```go
func defaultRubric() compliance.ScorecardRubric {
    return compliance.ScorecardRubric{
        Name: "default",
        Items: []compliance.ScorecardItem{
            {ID: "agent-id",      Required: true,  Keywords: []string{"this is", "my name is", "speaking with"}, Weight: 3},
            {ID: "mini-miranda",  Required: true,  Keywords: []string{"this is an attempt to collect a debt", "debt collector"}, Weight: 4},
            {ID: "payment-option", Required: false, Keywords: []string{"payment plan", "pay in full", "settlement"}, Weight: 3},
        },
    }
}
```

### Idempotency

The PATCH endpoint on the contact service is an overwrite (`UPDATE ... SET scorecard_result = $1 WHERE id = $2`). With the same rubric, the result is identical — the overwrite is a safe no-op. No upsert logic is needed in Phase 4.

When rubric versioning is added in a future phase, the PATCH should include a rubric version check to avoid overwriting a newer score with an older one.

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

`AtLeastOnce` delivery means the handler is designed to be idempotent (safe overwrite on the PATCH endpoint).

## Event Payload

`InteractionCreatedEvent` carries the sanitized content and the inline scorecard result:

```go
type InteractionCreatedEvent struct {
    ContactAttemptID int64
    ConsumerID       int64
    AccountID        int64
    Channel          domain.Channel
    SanitizedContent string          // PII-redacted transcript from the workflow
    ScorecardResult  json.RawMessage // inline score from the workflow (may differ from async score)
    CorrelationID    string
    Timestamp        time.Time
}
```

The sanitized content is embedded in the event — the scoring service does not call back to the contact service to retrieve it. This avoids a point-in-time consistency issue and removes a cross-service read dependency.

## What This Service Does Not Own

- **Scorecard rubric definitions.** Rubrics are configuration, not stored in this service's DB. Phase 5 will fetch them from client config.
- **The compliance/score logic.** The evaluator lives in the `compliance` service. This service calls it — it does not re-implement it.
- **The initial inline score.** The score stored in `contact_attempts.scorecard_result` by the workflow is the first write. This service overwrites it asynchronously with the same (or a client-specific) rubric result.
