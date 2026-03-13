# Compliance Service — Design Notes

## Responsibility

Pure-logic service with no database. Provides three capabilities:

1. **Pre-contact compliance check** — evaluates five TCPA/FDCPA rules against a contact request and returns all violations (non-short-circuit).
2. **PII sanitizer** — redacts SSN, credit card, and phone patterns from free text before storage.
3. **Scorecard evaluator** — keyword-based quality scoring of interaction transcripts against JSON-configured rubrics.

This service is the centerpiece of the platform. Every outbound contact must pass through the compliance check before delivery, and every interaction transcript must pass through the sanitizer before storage.

## Stateless Design — Why the Caller Provides All Data

The compliance service has no database and makes no cross-service API calls. The `ContactCheckRequest` carries all the data the rules need: consumer state (consent, attorney flag, timezone) and contact history (recent timestamps).

Why not have the compliance service fetch this data from the consumer and contact services?

1. **Testability.** Every rule is a pure function of its input. Tests inject exact data without mocking service calls or seeding databases.
2. **No circular dependencies.** The contact service will call compliance for pre-checks in Phase 3. If compliance also called contact (for frequency cap data), the dependency would be circular.
3. **Performance.** A single request carries everything; no fan-out to other services. The p99 target is < 50ms — there is no latency budget for additional network calls.
4. **Deployment independence.** The compliance service can be deployed, scaled, or restarted without any dependency on other services being available.

The tradeoff: the caller is responsible for assembling the request correctly. This is acceptable because there will be a single orchestration point (the contact workflow in Phase 3) that gathers consumer data and contact history before calling compliance.

## Rule Engine Design

### `Rule` Interface

Each compliance rule implements a single-method interface:

```go
type Rule interface {
    Evaluate(req *ContactCheckRequest) *Violation
}
```

Returning `*Violation` (nil = pass) rather than `(bool, string)` keeps the interface minimal and makes the rule name and details inherent to the violation, not the caller.

### Non-Short-Circuit Evaluation

`RunAllRules` runs every rule regardless of prior failures. This is a regulatory requirement: compliance officers need to see the _full_ violation set, not just the first failure. A contact blocked for both a time window violation and a consent violation must report both in the audit trail.

### Five Rules

| Rule | Regulation | Logic |
|---|---|---|
| `TimeWindowRule` | TCPA | Block before 8am / after 9pm in consumer's local timezone. Uses `time.LoadLocation` for correct DST handling. Exactly 8am and exactly 9pm are both permitted. |
| `FrequencyCapRule` | FDCPA Reg F | Block if ≥ 7 contacts in a rolling 7-day window. Boundary (exactly 7 days ago) counts — conservative for compliance. |
| `AttorneyBlockRule` | FDCPA § 805(a)(2) | Block if `attorney_on_file` is true. |
| `ConsentCheckRule` | TCPA/FDCPA | Block if `consent_status == "revoked"` or `do_not_contact == true`. These are separate checks because they have different legal bases (voluntary revocation vs. regulatory cease-and-desist). |
| `OptOutValidationRule` | CAN-SPAM / TCPA | SMS and email payloads must contain opt-out keywords ("opt out", "unsubscribe", "stop", "reply stop"). Voice is exempt. Empty `MessageContent` passes — the message may not be generated yet at pre-check time. |

### `CheckTime` Override

The `ContactCheckRequest.CheckTime` field allows tests to inject a fixed timestamp instead of using `time.Now()`. This avoids a clock interface or package-level variable — the function remains pure.

## PII Sanitizer

### Pattern Compilation

All three regex patterns (`ssnPattern`, `ccPattern`, `phonePattern`) are compiled once at package level via `regexp.MustCompile`. Per-call compilation would violate the < 50ms latency target.

### Application Order

Patterns are applied in order: credit card → SSN → phone. Credit card is applied first because its 16-digit pattern is the longest; applying phone first could match substrings of a CC number (e.g., 3-3-4 digit groups within a 4-4-4-4 CC).

### What Is and Is Not Matched

| Pattern | Matched | Not matched |
|---|---|---|
| SSN | `123-45-6789` (dashed) | `123456789` (no dashes — too many false positives with arbitrary 9-digit numbers) |
| Credit card | `4111 1111 1111 1111`, `4111-1111-1111-1111`, `4111111111111111` | Fewer than 16 digits, non-standard groupings |
| Phone | `(555) 123-4567`, `555-123-4567`, `555.123.4567`, `+15551234567` | 7-digit local numbers (no area code), international formats other than +1 |

### Scope of Sanitization

The sanitizer operates on free-text interaction content (message bodies, transcripts) before storage. It is _not_ applied to structured log fields. Log discipline (never logging `account_number`, raw SSN, etc.) is a separate control enforced at the call site per the logging standards in TD.md.

## Scorecard Evaluator

### Keyword Matching, Not NLP

The evaluator uses `strings.Contains` on lowercased text. This is intentional:

1. **Deterministic.** The same transcript always produces the same score. No model drift, no temperature sensitivity, no API latency.
2. **Auditable.** A compliance officer can read the rubric JSON and verify why a score was assigned. The `matched_keyword` field in each `ItemResult` shows exactly which keyword triggered the match.
3. **Configurable.** Rubrics are JSON — different clients can define different keyword sets and weights without code changes.

The tradeoff: keyword matching produces false positives (the agent said "my name is" in a different context) and false negatives (the agent identified themselves using phrasing not in the keyword list). This is acceptable because scoring is advisory (not blocking) and ops teams can adjust rubrics.

### Required vs. Optional Items

Each rubric item has a `Required` flag. The `RequiredPassed` field in the response is true only if _every_ required item matched. This supports the common use case: Mini-Miranda disclosure is mandatory (FDCPA), but offering a payment plan is nice-to-have.

### Percentage Calculation

`Percentage = TotalScore / MaxScore * 100`. If `MaxScore` is 0 (empty rubric), `Percentage` is 0 to avoid division by zero.

## Metrics

| Metric | Type | Purpose |
|---|---|---|
| `compliance_check_duration_ms` | Gauge | Tracks latest check duration. Target: p99 < 50ms. Using Gauge because Encore v1.52 does not expose a Histogram type; switch to Histogram when available. |
| `compliance_violation_total` | CounterGroup (by `rule`) | Counts violations by rule name. Spikes in a specific rule indicate a data or configuration problem upstream. |

## Error Codes

| Condition | HTTP status | `errs.Code` |
|---|---|---|
| Missing `consumer_id` | 400 | `InvalidArgument` |
| Invalid `channel` | 400 | `InvalidArgument` |
| Missing `timezone` | 400 | `InvalidArgument` |
| Empty `text` (sanitizer) | 400 | `InvalidArgument` |
| Empty `transcript` (scorer) | 400 | `InvalidArgument` |
| Empty rubric items (scorer) | 400 | `InvalidArgument` |

No 404 or 500 cases — the service has no database and no external dependencies.

## Test Coverage

All application code is at 100% coverage. Test files:

- `rules_test.go` — 30+ table-driven cases covering each rule individually plus combined multi-violation scenarios.
- `sanitizer_test.go` — SSN, CC, phone, mixed PII, clean text, edge cases.
- `scorecard_test.go` — full match, required missing, optional missing, case insensitive, empty rubric, partial match.
- `compliance_test.go` — API handler validation errors and integration through the handler layer.
