# Workflows — Design Notes

## Responsibility

This package contains the Temporal workflow definition, activity implementations, and the worker binary. It has no dependency on Encore packages — all interaction with Encore services happens over HTTP.

The `workflows/` package is compiled separately from the Encore app and run as a standalone process (`workflows/worker/main.go`). It can be deployed, scaled, and restarted independently.

## Why Temporal

The contact workflow has several properties that make Temporal the right tool:

1. **Crash recovery.** If the worker restarts mid-workflow, Temporal replays the workflow history up to the last completed activity and resumes from there. No orphaned `pending` rows, no lost contacts.
2. **Automatic retry with backoff.** Each activity retries up to 3 times with 1-second initial backoff. HTTP calls to Encore APIs fail transiently; retries handle this without custom retry logic in the activity code.
3. **Visibility.** The Temporal UI shows every workflow execution — which step it's on, which activities failed, and the full event history. This replaces what would otherwise be a bespoke "where is this contact stuck?" dashboard.
4. **Long-running workflows.** `PaymentPlanWorkflow` uses durable timers (wait for acceptance signal, schedule installment reminders over weeks/months). That is only practical with Temporal.

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
│   compliance result is JSON-marshalled immediately; marshal failure returns workflow error (status="failed")
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
│   scoring service also re-scores async via interaction-created subscriber (Phase 4)
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

## `Activities` Struct and the `doRequest` Helper

All activities delegate to a shared `doRequest` method that:

1. Marshals the payload to JSON (if non-nil).
2. Creates an HTTP request with the specified method and `Content-Type: application/json`.
3. Executes the request with the activity's context (timeout + cancellation propagate correctly).
4. Returns a structured error on HTTP 4xx/5xx.
5. Decodes the response body into the provided result pointer (nil = discard body).

The convenience `post` method wraps `doRequest` with `http.MethodPost` for backward compatibility with existing ContactWorkflow activities. Payment activities call `doRequest` directly with `http.MethodPatch`.

This keeps each activity method small — they express _what_ to call and _what type to expect back_, not _how_ to make an HTTP call.

The `BaseURL` field (default: `http://localhost:4000`) is injected at worker startup via the `ENCORE_BASE_URL` environment variable. This makes the worker portable across local dev, staging, and production without code changes.

## Worker Binary

```
TEMPORAL_HOST_PORT=localhost:7233  (default)
ENCORE_BASE_URL=http://localhost:4000  (default)
```

The worker registers both workflow functions and the activities struct:

```go
w.RegisterWorkflow(workflows.ContactWorkflow)
w.RegisterWorkflow(workflows.PaymentPlanWorkflow)
w.RegisterActivity(activities)  // registers all methods on *Activities
```

Registering the struct registers all exported methods. Adding a new activity method (e.g., `MarkPlanDefaulted`, `MarkPlanCompleted`) automatically makes it available to the worker without a separate `RegisterActivity` call. Both workflows run on the same `contact-queue` task queue.

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

## `PaymentPlanWorkflow` — Signal-Driven State Machine

```
PaymentPlanWorkflow(input PaymentPlanInput)
│
├─ Step 1: Wait for "accept" signal (72h timeout via Selector)
│   └─ timeout → MarkPlanDefaulted activity → return
│
├─ Step 2: Loop NumInstallments times:
│   ├─ Sleep for frequency interval (weekly=7d, biweekly=14d, monthly=30d)
│   ├─ Wait for "payment_received" signal (3-day grace via Selector)
│   │   ├─ received → missedCount = 0, continue
│   │   └─ grace expired → missedCount++
│   │       └─ missedCount >= 3 → MarkPlanDefaulted → return
│   └─ next installment
│
└─ All installments complete → MarkPlanCompleted → return
```

**`missedCount` is consecutive, not cumulative.** A successful payment resets the counter to zero. A consumer who misses 2, pays 1, then misses 1 more has a `missedCount` of 1 — not 3. Default requires 3 consecutive missed installments.

### Key Differences from ContactWorkflow

1. **Long-running.** ContactWorkflow completes in seconds. PaymentPlanWorkflow can run for months (e.g., 12 monthly installments = ~360 days). Temporal handles this natively — the workflow history grows with each timer and signal event.

2. **Signal-driven.** The workflow uses `workflow.GetSignalChannel` to receive external signals ("accept" and "payment_received"). The Encore API or an external process sends these signals via the Temporal client when the consumer accepts a plan or makes a payment.

3. **Selector pattern for timeout-or-signal.** Both the acceptance wait and the installment grace period use `workflow.NewSelector` with a timer future and a signal channel. This is Temporal's idiomatic pattern for "wait for X or timeout after Y."

4. **No HTTP calls during wait.** The workflow sleeps between installments using `workflow.Sleep`. Temporal durable timers do not consume worker resources while sleeping — the worker can process other workflows.

### Activities

Two activities call Encore's private PATCH endpoints:

| Activity | Endpoint | Purpose |
|---|---|---|
| `MarkPlanDefaulted` | `PATCH /payment-plans/:id/default` | Sets status to `defaulted`, records event, publishes to Pub/Sub |
| `MarkPlanCompleted` | `PATCH /payment-plans/:id/complete` | Sets status to `completed`, records event, publishes to Pub/Sub |

Both use the `doRequest` helper (refactored from `post`) which supports arbitrary HTTP methods.

### Worker Registration

Both `ContactWorkflow` and `PaymentPlanWorkflow` are registered on the same `contact-queue` worker. Temporal dispatches by workflow type name, so both workflows run correctly on the same task queue. This simplifies development — a separate `payment-queue` can be introduced later if the workflows need independent scaling.

## Correlation ID

`ContactWorkflowInput.CorrelationID` is set to the Temporal workflow ID by the contact service at workflow start. All activities log it, enabling log correlation across Encore's structured logs and Temporal's worker logs without a trace collector:

```
grep "correlation_id=contact-42-1710000000000" <encore-logs> <temporal-worker-logs>
```

The workflow ID format is `contact-{attemptID}-{unixMilli}`, making it human-readable in the Temporal UI and unique across restarts.

`PaymentPlanInput.CorrelationID` flows through to `MarkPlanInput` and is logged by both `MarkPlanDefaulted` and `MarkPlanCompleted` activities, so payment plan terminal transitions are traceable in the same way.
