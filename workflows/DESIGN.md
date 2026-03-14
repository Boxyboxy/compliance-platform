# Workflows — Design Notes

## Responsibility

This package contains the Temporal workflow definition, activity implementations, and the worker binary. It has no dependency on Encore packages — all interaction with Encore services happens over HTTP.

The `workflows/` package is compiled separately from the Encore app and run as a standalone process (`workflows/worker/main.go`). It can be deployed, scaled, and restarted independently.

## Why Temporal

The contact workflow has several properties that make Temporal the right tool:

1. **Crash recovery.** If the worker restarts mid-workflow, Temporal replays the workflow history up to the last completed activity and resumes from there. No orphaned `pending` rows, no lost contacts.
2. **Automatic retry with backoff.** Each activity retries up to 3 times with 1-second initial backoff. HTTP calls to Encore APIs fail transiently; retries handle this without custom retry logic in the activity code.
3. **Visibility.** The Temporal UI shows every workflow execution — which step it's on, which activities failed, and the full event history. This replaces what would otherwise be a bespoke "where is this contact stuck?" dashboard.
4. **Future workflows.** `PaymentPlanWorkflow` (Phase 4) requires durable timers (wait for acceptance signal, schedule installment reminders). That is only practical with Temporal.

## The Worker Is HTTP-Only

The `workflows/` package imports only the Temporal SDK and standard library. It never imports `encore.dev/*`. This is a hard architectural constraint, not an oversight.

Encore generates a custom Go build that wires service calls, database connections, and Pub/Sub through its own runtime. A Temporal worker is a separate OS process that cannot participate in that runtime. Attempting to import `encore.dev/storage/sqldb` or `encore.dev/pubsub` from the worker would compile but fail at runtime — Encore's infra would not be initialized.

The consequence: every interaction with the platform goes through Encore's HTTP API. Activities call `POST /compliance/check`, `POST /compliance/sanitize`, etc. via the `Activities.post` helper. Two private Encore endpoints (`/contact/internal/publish-attempted`, `/contact/internal/publish-interaction`) exist specifically to let the worker publish Pub/Sub events it cannot publish directly.

## `ContactWorkflow` — Step by Step

All activities share options: `StartToCloseTimeout: 10s`, `MaximumAttempts: 3`, `InitialInterval: 1s`.

```
ContactWorkflow(input ContactWorkflowInput)
│
├─ Step 1: CheckCompliance
│   POST /compliance/check
│   input carries: consumer state + recent timestamps (assembled by contact service before workflow start)
│   if !allowed → RecordContactResult(blocked) + PublishContactAttempted, return early
│
├─ Step 2: SanitizePII
│   POST /compliance/sanitize
│   redacts SSN, credit card, phone from MessageContent before any storage or event emission
│
├─ Step 3: SimulateDelivery
│   deterministic stub: attemptID % 10 == 0 → failed, else → delivered
│   no math/rand — workflow code must be deterministic across replays
│
├─ Step 4: ScoreInteraction
│   POST /compliance/score
│   uses hardcoded default rubric (3 items: agent-id, mini-miranda, payment-option)
│   Phase 4: scoring service will re-score async with per-client rubric
│
├─ Step 5: RecordContactResult
│   POST /contact/attempts/:id/result (private)
│   writes: status, sanitized content, compliance blob, scorecard blob
│
├─ Step 6: PublishContactAttempted
│   POST /contact/internal/publish-attempted (private)
│   bridges worker → Encore Pub/Sub → audit, scoring subscribers
│
└─ Step 7: PublishInteractionCreated
    POST /contact/internal/publish-interaction (private)
    carries: sanitized content + scorecard result embedded in event payload
```

### Blocked Path (early exit after Step 1)

When compliance blocks the contact, the workflow runs only `RecordContactResult` and `PublishContactAttempted`, then returns. Steps 2–7 do not execute. This is intentional: there is no content to sanitize, no delivery to simulate, no interaction to score. The `contact-attempted` event is published so the audit log has a record.

`InteractionCreated` is **not** published on the blocked path. That topic's semantic is "an interaction occurred" — a blocked contact is not an interaction.

## Deterministic Delivery Simulation

```go
if input.AttemptID%10 == 0 {
    return &DeliveryResult{Delivered: false, Status: "failed"}, nil
}
return &DeliveryResult{Delivered: true, Status: "delivered"}, nil
```

Two constraints drive this design:

1. **Workflow determinism.** Temporal replays the workflow history on retries and crash recovery. Any non-deterministic call (`math/rand`, `time.Now()`) would produce different results on replay, causing a `NonDeterministicWorkflowError`. The modulo check is a pure function of the attempt ID.
2. **Testability.** Tests can predict the outcome by controlling the attempt ID. `TestContactWorkflow_DeliveryFailure` uses `ContactAttemptID: 10` (10 % 10 == 0) to force a failure without mocking time or random state.

## `SimulateDeliveryInput` — Why a Struct, Not `int64`

The activity signature wraps the attempt ID in a struct:

```go
type SimulateDeliveryInput struct {
    AttemptID int64 `json:"attempt_id"`
}
```

The Temporal Go SDK's test suite serializes and deserializes activity arguments via JSON. For method-based activities (defined on a struct receiver), the test env includes the receiver type in the argument list when decoding. Decoding a bare `int64` JSON value into the receiver type (`*Activities`) panics with a decode error. Wrapping in a struct makes the JSON decode succeed regardless of what the test env expects at position zero.

This is a test-infrastructure constraint, not a domain modeling choice.

## `Activities` Struct and the `post` Helper

All seven activities delegate to a single private `post` method that:

1. Marshals the payload to JSON.
2. Creates an HTTP POST with `Content-Type: application/json`.
3. Executes the request with the activity's context (timeout + cancellation propagate correctly).
4. Returns a structured error on HTTP 4xx/5xx.
5. Decodes the response body into the provided result pointer (nil = discard body).

This keeps each activity method small — they express _what_ to call and _what type to expect back_, not _how_ to make an HTTP call.

The `BaseURL` field (default: `http://localhost:4000`) is injected at worker startup via the `ENCORE_BASE_URL` environment variable. This makes the worker portable across local dev, staging, and production without code changes.

## Worker Binary

```
TEMPORAL_HOST_PORT=localhost:7233  (default)
ENCORE_BASE_URL=http://localhost:4000  (default)
```

The worker registers both the workflow function and the activities struct:

```go
w.RegisterWorkflow(workflows.ContactWorkflow)
w.RegisterActivity(activities)  // registers all methods on *Activities
```

Registering the struct registers all exported methods. Adding a new activity method automatically makes it available to the worker without a separate `RegisterActivity` call.

`worker.InterruptCh()` returns a channel that closes on `SIGINT`/`SIGTERM`. The worker drains in-flight activities before exiting — no activity is abandoned mid-execution on a graceful shutdown.

## Testing Strategy

Tests use `go.temporal.io/sdk/testsuite`, which runs the workflow in-process without a Temporal server. Activities are mocked via `env.OnActivity("ActivityName", ...)`.

String-based activity names (e.g., `"CheckCompliance"`) are used instead of method references because the Temporal test suite deserialization has the same struct-receiver argument-position issue as noted above for `SimulateDeliveryInput` — mock matching fails for struct-typed inputs when using method references. String names bypass the deserializer entirely and match by name.

Each test registers the `Activities` struct on the env (`env.RegisterActivity(&Activities{})`) so the test framework knows the function signatures without actually executing the HTTP calls.

| Test | What it verifies |
|---|---|
| `TestContactWorkflow_HappyPath` | All 7 activities run; result is `delivered`, `Allowed: true` |
| `TestContactWorkflow_ComplianceBlocked` | Steps 2–7 skipped; result is `blocked` with correct `BlockReason` |
| `TestContactWorkflow_DeliveryFailure` | All steps run; result is `failed`, `Allowed: true` (compliance passed, delivery failed) |
| `TestContactWorkflow_ComplianceActivityError` | Activity error after 3 retries → workflow errors |
| `TestContactWorkflow_ScorecardIncludedInResult` | Sanitized content propagates into workflow result |

## Correlation ID

`ContactWorkflowInput.CorrelationID` is set to the Temporal workflow ID by the contact service at workflow start. Every activity logs it (Phase 4: switch to `rlog` once the worker is updated). This enables log correlation across Encore's structured logs and Temporal's worker logs without a trace collector:

```
grep "correlation_id=contact-42-1710000000000" <encore-logs> <temporal-worker-logs>
```

The workflow ID format is `contact-{attemptID}-{unixMilli}`, making it human-readable in the Temporal UI and unique across restarts.
