# Krew Backend Platform — Project Spec

## Overview

A Go backend platform that powers AI credit-servicing agents (like Krew's "Daniel"), built with **Encore** (API framework + infrastructure) and **Temporal** (durable workflow orchestration). You are building the **rails** — the data layer, compliance engine, workflow orchestration, and observability — not the AI agent itself.

**Mental model**: Daniel is a black box. He receives consumer context and returns a message string. Your job is everything around him — making sure the right consumer gets contacted at the right time, through the right channel, within legal bounds, with a full audit trail.

---

## Tech Stack

| Layer          | Tool                    | Why                                                                                   |
|----------------|-------------------------|---------------------------------------------------------------------------------------|
| API Framework  | Encore.go               | Type-safe APIs, built-in tracing/metrics, auto-provisioned local infra (Postgres, Pub/Sub) |
| Workflow Engine | Temporal               | Durable execution for multi-step contact workflows, built-in retry/timeout, workflow visibility |
| Database       | PostgreSQL (via Encore)  | Encore manages DB provisioning + migrations                                           |
| Async Events   | Encore Pub/Sub          | Decouple services; compliance auditing, QA scoring, analytics as subscribers          |
| Language       | Go                      | Matches Krew's stack (JD lists Python/Go)                                             |

---

## Architecture (Encore Services)

```
┌─────────────────────────────────────────────────────────────────────┐
│                            Encore App                               │
│                                                                     │
│  ┌───────────────┐  ┌───────────────┐  ┌──────────────────────┐     │
│  │   consumer    │  │    account    │  │       contact        │     │
│  │   service     │  │    service    │  │       service        │     │
│  │               │  │               │  │                      │     │
│  │  CRUD         │  │  CRUD +       │  │  API + Temporal      │     │
│  │  consent      │  │  status       │  │  workflow trigger    │     │
│  └───────────────┘  └───────────────┘  └──────────┬───────────┘     │
│                                                    │                │
│                                                    ▼                │
│                                        ┌───────────────────────┐    │
│                                        │     compliance        │    │
│                                        │     service           │    │
│                                        │                       │    │
│                                        │  Pre-check rules      │    │
│                                        │  PII sanitizer        │    │
│                                        │  Scorecard eval       │    │
│                                        └───────────────────────┘    │
│                                                                     │
│  ┌───────────────┐  ┌───────────────┐  ┌──────────────────────┐     │
│  │    audit      │  │    payment    │  │      scoring         │     │
│  │   service     │  │    service    │  │      service         │     │
│  │               │  │               │  │                      │     │
│  │  Append-only  │  │  Plans +      │  │  QA scorecard        │     │
│  │  log          │  │  status       │  │  subscriber          │     │
│  └───────────────┘  └───────────────┘  └──────────────────────┘     │
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐    │
│  │                    Encore Pub/Sub Topics                    │    │
│  │                                                             │    │
│  │   contact-attempted    │   interaction-created              │    │
│  │   consent-changed      │   consumer-lifecycle               │    │
│  │   account-lifecycle    │   payment-updated                  │    │
│  └─────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────┘
                               │
                               │  Temporal SDK
                               ▼
               ┌────────────────────────────────────┐
               │          Temporal Server           │
               │                                    │
               │  ContactWorkflow                   │
               │    ├─ CheckCompliance              │
               │    ├─ SanitizePII                  │
               │    ├─ SimulateDelivery             │
               │    ├─ ScoreInteraction             │
               │    ├─ RecordContactResult          │
               │    ├─ PublishContactAttempted      │
               │    └─ PublishInteractionCreated    │
               │                                    │
               │  PaymentPlanWorkflow               │
               │    ├─ Wait "accept" signal (72h)   │
               │    ├─ Track installments + grace   │
               │    ├─ MarkPlanDefaulted            │
               │    └─ MarkPlanCompleted            │
               └────────────────────────────────────┘
```

---

## Service Breakdown

### 1. `consumer` service

**Database: `consumer`**

```sql
-- 1_create_tables.up.sql
CREATE TABLE consumers (
    id               BIGSERIAL PRIMARY KEY,
    external_id      TEXT UNIQUE NOT NULL,       -- client's consumer ID
    first_name       TEXT NOT NULL,
    last_name        TEXT NOT NULL,
    phone            TEXT,
    email            TEXT,
    timezone         TEXT NOT NULL DEFAULT 'America/New_York',
    consent_status   TEXT NOT NULL DEFAULT 'granted'
                     CHECK (consent_status IN ('granted','revoked')),
    do_not_contact   BOOLEAN NOT NULL DEFAULT false,
    attorney_on_file BOOLEAN NOT NULL DEFAULT false,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_consumers_external_id ON consumers(external_id);
```

**APIs:**

```go
//encore:api public method=POST path=/consumers
func (s *Service) CreateConsumer(ctx context.Context, req *CreateConsumerReq) (*Consumer, error)

//encore:api public method=GET path=/consumers/:id
func (s *Service) GetConsumer(ctx context.Context, id int64) (*Consumer, error)

//encore:api public method=PUT path=/consumers/:id/consent
func (s *Service) UpdateConsent(ctx context.Context, id int64, req *UpdateConsentReq) (*Consumer, error)
```

When consent changes (grant **or** revoke), publish to `consent-changed` topic. On creation, publish to `consumer-lifecycle` topic.

---

### 2. `account` service

**Database: `account`**

```sql
CREATE TYPE account_status AS ENUM ('current','delinquent','charged_off','settled','closed');

CREATE TABLE accounts (
    id                BIGSERIAL PRIMARY KEY,
    consumer_id       BIGINT NOT NULL,
    original_creditor TEXT NOT NULL,
    account_number    TEXT NOT NULL,           -- stored encrypted at rest
    balance_due       NUMERIC(12,2) NOT NULL,
    days_past_due     INT NOT NULL DEFAULT 0,
    status            account_status NOT NULL DEFAULT 'current',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**APIs:**

```go
//encore:api public method=POST path=/accounts
func (s *Service) CreateAccount(ctx context.Context, req *CreateAccountReq) (*Account, error)

//encore:api public method=GET path=/accounts/:id
func (s *Service) GetAccount(ctx context.Context, id int64) (*Account, error)

//encore:api public method=GET path=/consumers/:consumerId/accounts
func (s *Service) ListAccountsByConsumer(ctx context.Context, consumerId int64) (*AccountList, error)

//encore:api public method=PATCH path=/accounts/:id/status
func (s *Service) UpdateAccountStatus(ctx context.Context, id int64, req *UpdateStatusReq) (*Account, error)
```

`CreateAccount` publishes to the `account-lifecycle` topic (`action: "created"`). `UpdateAccountStatus` publishes `action: "status_updated"` with old and new status values.

---

### 3. `compliance` service (THE CENTERPIECE)

No database. Pure logic. This is the module you'll talk about most in the interview.

**Pre-Contact Check:**

```go
type ContactCheckRequest struct {
    ConsumerID int64
    Channel    string // "sms", "email", "voice"
    Timezone   string
}

type ContactCheckResult struct {
    Allowed    bool        `json:"allowed"`
    Violations []Violation `json:"violations"`
}

type Violation struct {
    Rule    string `json:"rule"`
    Details string `json:"details"`
}
```

**Rules (based on FDCPA Reg F + TCPA):**

| Rule               | Logic                                                                         |
|--------------------|-------------------------------------------------------------------------------|
| Time Window        | No contact before 8am / after 9pm in consumer's local timezone                |
| Frequency Cap      | Max 7 contact attempts per rolling 7-day window per consumer                  |
| Attorney Block     | If `attorney_on_file = true`, block all contact                               |
| Consent Check      | If `consent_status = 'revoked'` or `do_not_contact = true`, block             |
| Channel Validation | SMS/email outbound must include opt-out instructions (validated on payload)   |

**PII Sanitizer:**

```go
// Redact SSNs, credit card numbers, phone numbers from log text
func SanitizePII(text string) string
```

Patterns to redact:

| Pattern      | Regex                                              | Replacement       |
|--------------|----------------------------------------------------|-------------------|
| SSN          | `\d{3}-\d{2}-\d{4}`                                | `[SSN_REDACTED]`  |
| Credit card  | `\d{4}[\s-]?\d{4}[\s-]?\d{4}[\s-]?\d{4}`           | `[CC_REDACTED]`   |
| Phone        | various formats                                    | `[PHONE_REDACTED]`|

**Scorecard Evaluator:**

```go
type ScorecardRubric struct {
    Name  string          `json:"name"`
    Items []ScorecardItem `json:"items"`
}

type ScorecardItem struct {
    ID          string   `json:"id"`
    Description string   `json:"description"`
    Required    bool     `json:"required"`
    Keywords    []string `json:"keywords"` // simple keyword match
    Weight      int      `json:"weight"`
}
```

Example rubric (JSON config, not hardcoded):
- Did the agent identify themselves? (keywords: "my name is", "this is", "calling from")
- Mini-Miranda disclosure? (keywords: "attempt to collect a debt", "information will be used")
- Payment option offered? (keywords: "payment plan", "settle", "arrangement")

**APIs:**

```go
//encore:api public method=POST path=/compliance/check
func (s *Service) CheckContact(ctx context.Context, req *ContactCheckRequest) (*ContactCheckResult, error)

//encore:api public method=POST path=/compliance/sanitize
func (s *Service) SanitizeText(ctx context.Context, req *SanitizeRequest) (*SanitizeResponse, error)

//encore:api public method=POST path=/compliance/score
func (s *Service) ScoreInteraction(ctx context.Context, req *ScoreRequest) (*ScoreResponse, error)
```

---

### 4. `contact` service (Temporal Workflow Trigger)

This service is the bridge between Encore's API layer and Temporal's workflow engine.

**Database: `contact`**

```sql
CREATE TABLE contact_attempts (
    id                BIGSERIAL PRIMARY KEY,
    consumer_id       BIGINT NOT NULL,
    account_id        BIGINT NOT NULL,
    channel           TEXT NOT NULL CHECK (channel IN ('sms','email','voice')),
    status            TEXT NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending','blocked','sent','delivered','failed')),
    block_reason      TEXT,
    workflow_id       TEXT,               -- Temporal workflow ID
    message_content   TEXT,              -- the agent's message (PII-scrubbed)
    compliance_result JSONB,
    scorecard_result  JSONB,
    attempted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at      TIMESTAMPTZ
);

CREATE INDEX idx_contact_consumer_time ON contact_attempts(consumer_id, attempted_at);
```

**API:**

```go
// Initiates a contact attempt — starts a Temporal workflow
//encore:api public method=POST path=/contact/initiate
func (s *Service) InitiateContact(ctx context.Context, req *InitiateContactReq) (*InitiateContactResp, error)

// Query contact history for a consumer
//encore:api public method=GET path=/consumers/:consumerId/contacts
func (s *Service) ListContacts(ctx context.Context, consumerId int64) (*ContactList, error)
```

**Temporal Workflow: `ContactWorkflow`**

```
 ContactWorkflow(input ContactWorkflowInput)
 │
 ├─ Step 1: CheckCompliance
 │           └─ blocked? ──► RecordContactResult(blocked) + PublishContactAttempted → return early
 │
 ├─ Step 2: SanitizePII
 │           └─ redact SSN/CC/phone from input.MessageContent before any storage
 │
 ├─ Step 3: SimulateDelivery
 │           └─ deterministic stub: attemptID % 10 == 0 → failed, else → delivered
 │
 ├─ Step 4: ScoreInteraction
 │           └─ keyword-based default rubric (agent-id, mini-miranda, payment-option)
 │
 ├─ Step 5: RecordContactResult
 │           └─ write sanitized content + compliance + scorecard blobs to DB
 │
 ├─ Step 6: PublishContactAttempted
 │           └─ bridges worker → Encore Pub/Sub (contact-attempted topic)
 │
 └─ Step 7: PublishInteractionCreated
             └─ carries sanitized content + scorecard result (interaction-created topic)
```

**Note on message generation**: The AI agent (Daniel) assembles `MessageContent` before calling `POST /contact/initiate`. The platform receives it as part of `ContactWorkflowInput` and sanitizes it in Step 2. There is no `GenerateMessageStub` activity — the generation stub lives in the contact service before the workflow starts.

```go
func ContactWorkflow(ctx workflow.Context, input ContactWorkflowInput) (ContactWorkflowResult, error) {
    // Step 1: Pre-contact compliance check
    checkResult, err := workflow.ExecuteActivity(ctx, activities.CheckCompliance, checkInput).Get(ctx, &checkResult)
    if !checkResult.Allowed {
        // Record blocked result + publish contact-attempted, then return early
    }

    // Step 2: Sanitize PII from message content
    sanitizeResult, err := workflow.ExecuteActivity(ctx, activities.SanitizePII, ...).Get(ctx, &sanitizeResult)

    // Step 3: Simulate delivery (deterministic: attemptID % 10 == 0 → failed)
    deliveryResult, err := workflow.ExecuteActivity(ctx, activities.SimulateDelivery, ...).Get(ctx, &deliveryResult)

    // Step 4: Score interaction against default rubric
    scoreResult, err := workflow.ExecuteActivity(ctx, activities.ScoreInteraction, ...).Get(ctx, &scoreResult)

    // Step 5: Record contact result (sanitized content + compliance + scorecard blobs)
    workflow.ExecuteActivity(ctx, activities.RecordContactResult, ...)

    // Step 6: Publish contact-attempted event
    workflow.ExecuteActivity(ctx, activities.PublishContactAttempted, ...)

    // Step 7: Publish interaction-created event
    workflow.ExecuteActivity(ctx, activities.PublishInteractionCreated, ...)

    return result, nil
}
```

**Why Temporal here (not just a function call):**
- If the delivery step fails, Temporal retries with backoff automatically
- If the worker crashes mid-workflow, Temporal resumes from the last completed activity
- You get full workflow visibility in Temporal's UI — you can see exactly which step a contact is stuck on
- Payment plan workflows (below) involve waiting for consumer response — Temporal handles durable timers and signals natively

---

### 5. `payment` service

**Database: `payment`**

```sql
CREATE TYPE plan_status AS ENUM ('proposed','accepted','active','completed','defaulted');

CREATE TABLE payment_plans (
    id               BIGSERIAL PRIMARY KEY,
    account_id       BIGINT NOT NULL,
    total_amount     NUMERIC(12,2) NOT NULL,
    num_installments INT NOT NULL,
    installment_amt  NUMERIC(12,2) NOT NULL,
    frequency        TEXT NOT NULL DEFAULT 'monthly'
                     CHECK (frequency IN ('weekly','biweekly','monthly')),
    status           plan_status NOT NULL DEFAULT 'proposed',
    proposed_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at      TIMESTAMPTZ,
    completed_at     TIMESTAMPTZ
);

CREATE TABLE payment_events (
    id          BIGSERIAL PRIMARY KEY,
    plan_id     BIGINT NOT NULL REFERENCES payment_plans(id),
    event_type  TEXT NOT NULL CHECK (event_type IN ('proposed','accepted','payment_received','missed','defaulted','completed')),
    amount      NUMERIC(12,2),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    metadata    JSONB
);
```

**APIs:**

```go
//encore:api public method=POST path=/payment-plans
func (s *Service) ProposePlan(ctx context.Context, req *ProposePlanReq) (*PaymentPlan, error)

//encore:api public method=PATCH path=/payment-plans/:id/accept
func (s *Service) AcceptPlan(ctx context.Context, id int64) (*PaymentPlan, error)

//encore:api public method=POST path=/payment-plans/:id/payments
func (s *Service) RecordPayment(ctx context.Context, id int64, req *RecordPaymentReq) (*PaymentEvent, error)

//encore:api public method=GET path=/payment-plans/:id
func (s *Service) GetPlan(ctx context.Context, id int64) (*PaymentPlan, error)
```

**Private endpoints (for Temporal workflow callbacks):**

```go
//encore:api private method=PATCH path=/payment-plans/:id/default
func (s *Service) MarkDefaulted(ctx context.Context, id int64) (*PaymentPlan, error)

//encore:api private method=PATCH path=/payment-plans/:id/complete
func (s *Service) MarkCompleted(ctx context.Context, id int64) (*PaymentPlan, error)
```

**Temporal Workflow: `PaymentPlanWorkflow`**

```
 PaymentPlanWorkflow(input PaymentPlanInput)
 │
 ├─ Step 1: Wait for "accept" signal (72h timeout)
 │   └─ timeout? ──► MarkPlanDefaulted activity → return
 │
 ├─ Step 2: Loop NumInstallments times:
 │   ├─ Sleep for interval (weekly=7d, biweekly=14d, monthly=30d)
 │   ├─ Wait for "payment_received" signal (3-day grace)
 │   │   ├─ received? ──► continue to next installment
 │   │   └─ grace expired? ──► missedCount++
 │   │       └─ missedCount >= 3? ──► MarkPlanDefaulted activity → return
 │   └─ next installment
 │
 └─ All installments tracked ──► MarkPlanCompleted activity → return
```

```go
func PaymentPlanWorkflow(ctx workflow.Context, input PaymentPlanInput) error {
    // Wait for "accept" signal with 72-hour timeout
    // On timeout → MarkPlanDefaulted → return
    // On accept → loop NumInstallments times:
    //   Sleep for frequency interval
    //   Wait for "payment_received" signal with 3-day grace
    //   If grace expires → missedCount++; if >= 3 → MarkPlanDefaulted → return
    // All installments tracked → MarkPlanCompleted → return
}
```

Activities (`MarkPlanDefaulted`, `MarkPlanCompleted`) call Encore's private PATCH endpoints via HTTP, following the same pattern as `ContactWorkflow` activities. The `Activities` struct's `doRequest` helper (refactored from the original `post` method) supports both POST and PATCH methods.

This demonstrates Temporal's long-running workflow + signal capabilities, durable timers, and the selector pattern for timeout-or-signal branching.

---

### 6. `audit` service

**Database: `audit`**

```sql
CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    entity_type TEXT NOT NULL,   -- 'consumer', 'account', 'contact', 'payment_plan'
    entity_id   BIGINT NOT NULL,
    action      TEXT NOT NULL,   -- 'created', 'updated', 'consent_revoked', 'contact_blocked'
    actor       TEXT NOT NULL,   -- 'system', 'api', 'workflow:contact-123'
    old_value   JSONB,
    new_value   JSONB,
    metadata    JSONB,           -- correlation_id, workflow_id, etc.
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- This table is APPEND-ONLY. No UPDATE or DELETE operations.
CREATE INDEX idx_audit_entity ON audit_log(entity_type, entity_id);
CREATE INDEX idx_audit_time   ON audit_log(created_at);
```

**APIs:**

```go
// Returns all audit entries for an entity (DESC by time).
//encore:api public method=GET path=/audit/:entityType/:entityId
func (s *Service) GetAuditLog(ctx context.Context, entityType string, entityId int64) (*AuditLogList, error)

// Filtered query — supports action, since, until (RFC3339) filters.
//encore:api public method=POST path=/audit/search
func (s *Service) SearchAuditLog(ctx context.Context, params *GetAuditLogParams) (*AuditLogList, error)

// Internal — called by other services
//encore:api private method=POST path=/audit/record
func (s *Service) RecordAudit(ctx context.Context, req *RecordAuditReq) (*AuditEntry, error)
```

**Note on `GetAuditLog` vs `SearchAuditLog`**: Encore requires path parameters to be individual function parameters, not embedded in a struct. This precludes adding optional query params to the path-based `GET` endpoint without changing its signature. `SearchAuditLog` is a POST-based search endpoint that accepts the full `GetAuditLogParams` struct (entity_type, entity_id, action, since, until) as a JSON body. Both call the same `queryAuditLog` internal implementation.

**Subscribers: listens to all 6 Pub/Sub topics and writes audit entries with idempotency.**

| Topic | Subscription | Action | Entity |
|---|---|---|---|
| `contact-attempted` | `audit-contact-attempted` | `contact_attempted` | `contact` |
| `interaction-created` | `audit-interaction-created` | `interaction_created` | `contact` |
| `consent-changed` | `audit-consent-changed` | `consent_revoked` or `consent_granted` | `consumer` |
| `consumer-lifecycle` | `audit-consumer-lifecycle` | event.Action (e.g. `created`) | `consumer` |
| `account-lifecycle` | `audit-account-lifecycle` | event.Action (e.g. `created`, `status_updated`) | `account` |
| `payment-updated` | `audit-payment-updated` | event.EventType | `payment_plan` |

**Idempotency**: Each handler computes a deterministic dedup key before inserting. `isDuplicate(ctx, key)` queries `metadata->>'dedup_key'` and skips on match. Expected under at-least-once delivery; logged at Debug level.

**Append-only enforcement**: Migration `2_enforce_append_only.up.sql` installs a `BEFORE UPDATE OR DELETE` trigger that raises an exception. Belt-and-suspenders with code-level convention.

---

### 7. `scoring` service

Subscribes to `interaction-created` events. Runs the scorecard evaluator from the compliance package and writes results back to `contact_attempts.scorecard_result` via a private PATCH endpoint on the contact service.

```go
var _ = pubsub.NewSubscription(
    contact.InteractionCreated,
    "scoring-interaction-created",
    pubsub.SubscriptionConfig[*contact.InteractionCreatedEvent]{
        Handler: handleInteractionCreated,
    },
)
```

Handler logic:
1. Skip if `SanitizedContent` is empty (blocked contacts have no transcript).
2. Score using `defaultRubric()` — 3-item rubric: agent-id (required, weight 3), mini-miranda (required, weight 4), payment-option (optional, weight 3).
3. Call `compliance.ScoreInteraction(ctx, &ScoreRequest{...})`.
4. Marshal result and call `contact.UpdateScorecardResult(ctx, id, &UpdateScorecardReq{...})`.

**Why async scoring exists alongside in-workflow scoring**: enables re-scoring if the rubric is updated later; decouples QA from the contact flow so scoring failures never block delivery.

---

## Pub/Sub Topics (Encore)

```go
// contact service
var ContactAttempted = pubsub.NewTopic[*ContactAttemptedEvent]("contact-attempted", pubsub.TopicConfig{
    DeliveryGuarantee: pubsub.AtLeastOnce,
})
var InteractionCreated = pubsub.NewTopic[*InteractionCreatedEvent]("interaction-created", pubsub.TopicConfig{
    DeliveryGuarantee: pubsub.AtLeastOnce,
})

// consumer service
var ConsentChanged = pubsub.NewTopic[*ConsentChangedEvent]("consent-changed", pubsub.TopicConfig{
    DeliveryGuarantee: pubsub.AtLeastOnce,
})
var ConsumerLifecycle = pubsub.NewTopic[*ConsumerLifecycleEvent]("consumer-lifecycle", pubsub.TopicConfig{
    DeliveryGuarantee: pubsub.AtLeastOnce,
})

// account service
var AccountLifecycle = pubsub.NewTopic[*AccountLifecycleEvent]("account-lifecycle", pubsub.TopicConfig{
    DeliveryGuarantee: pubsub.AtLeastOnce,
})

// payment service
var PaymentUpdated = pubsub.NewTopic[*PaymentUpdatedEvent]("payment-updated", pubsub.TopicConfig{
    DeliveryGuarantee: pubsub.AtLeastOnce,
})
```

**Subscriber mapping:**

| Topic | Subscribers |
|---|---|
| `contact-attempted` | `audit` |
| `interaction-created` | `audit`, `scoring` |
| `consent-changed` | `audit`, `contact` (cancel pending outbound for that consumer) |
| `consumer-lifecycle` | `audit` |
| `account-lifecycle` | `audit` |
| `payment-updated` | `audit` |

---

## Encore Project Structure

Files marked with ✅ are implemented; unmarked files are planned for future phases.

```
compliance-platform/
├── encore.app
├── go.mod
├── go.sum
├── CLAUDE.md                         # ✅ Project instructions for Claude Code
│
├── consumer/
│   ├── consumer.go                   # ✅ Service + API handlers; publishes consumer-lifecycle on create, consent-changed on grant/revoke
│   ├── models.go                     # ✅ Consumer, CreateConsumerReq, UpdateConsentReq
│   ├── events.go                     # ✅ ConsentChangedEvent + ConsumerLifecycleEvent + both topics
│   ├── consumer_test.go              # ✅ Table-driven tests
│   ├── DESIGN.md                     # ✅ Design notes
│   └── migrations/
│       └── 1_create_tables.up.sql    # ✅
│
├── account/
│   ├── account.go                    # ✅ Service + API handlers; publishes account-lifecycle on create and status update
│   ├── models.go                     # ✅ Account, validStatuses map
│   ├── events.go                     # ✅ AccountLifecycleEvent + account-lifecycle topic
│   ├── account_test.go              # ✅ Table-driven tests including status transitions
│   ├── DESIGN.md                     # ✅ Design notes
│   └── migrations/
│       └── 1_create_tables.up.sql    # ✅
│
├── compliance/
│   ├── compliance.go                 # ✅ Service + API handlers (CheckContact, SanitizeText, ScoreInteraction)
│   ├── models.go                     # ✅ Rule interface, request/response types, scorecard types
│   ├── rules.go                      # ✅ 5 rules: TimeWindow, FrequencyCap, AttorneyBlock, ConsentCheck, OptOut
│   ├── sanitizer.go                  # ✅ PII redaction (SSN, CC, phone)
│   ├── scorecard.go                  # ✅ Keyword-based scorecard evaluator
│   ├── rules_test.go                 # ✅ 30+ table-driven cases
│   ├── sanitizer_test.go             # ✅
│   ├── scorecard_test.go             # ✅
│   ├── compliance_test.go            # ✅ API handler tests
│   └── DESIGN.md                     # ✅ Design notes
│
├── contact/
│   ├── contact.go                    # ✅ Service + API handlers + Temporal trigger + PATCH /attempts/:id/scorecard
│   ├── models.go                     # ✅ ContactAttempt, request/response types, UpdateScorecardReq
│   ├── events.go                     # ✅ ContactAttempted + InteractionCreated topics
│   ├── subscribers.go                # ✅ consent-changed subscriber (blocks pending contacts)
│   ├── contact_test.go              # ✅ ListContacts, UpdateContactResult, validation, consent revocation
│   ├── DESIGN.md                     # ✅ Design notes
│   └── migrations/
│       └── 1_create_tables.up.sql    # ✅
│
├── audit/
│   ├── audit.go                      # ✅ RecordAudit (private) + GetAuditLog (public) + SearchAuditLog (filtered)
│   ├── models.go                     # ✅ AuditEntry, RecordAuditReq, GetAuditLogParams
│   ├── subscribers.go                # ✅ 6 subscribers + isDuplicate/buildMetadata idempotency helpers
│   ├── audit_test.go                # ✅ RecordAudit, GetAuditLog, action filter, time range, idempotency, append-only, subscriber tests
│   ├── integration_test.go          # ✅ Full lifecycle pipeline: consumer create → account create → status update → consent grant/revoke → filtered queries
│   ├── DESIGN.md                     # ✅ Design notes
│   └── migrations/
│       ├── 1_create_tables.up.sql    # ✅
│       └── 2_enforce_append_only.up.sql  # ✅ BEFORE UPDATE OR DELETE trigger
│
├── scoring/
│   ├── subscribers.go                # ✅ Full implementation: score via compliance.ScoreInteraction, update via contact.UpdateScorecardResult
│   ├── scoring_test.go              # ✅ Full score, partial score, empty content, idempotency tests
│   └── DESIGN.md                     # ✅ Design notes
│
├── workflows/
│   ├── contact_workflow.go           # ✅ ContactWorkflow (7 steps)
│   ├── activities.go                 # ✅ HTTP-based activities (no Encore imports)
│   ├── models.go                     # ✅ Workflow input/output types, activity I/O mirrors
│   ├── payment_models.go             # ✅ PaymentPlanInput, MarkPlanInput (no Encore imports)
│   ├── payment_workflow.go           # ✅ PaymentPlanWorkflow (signal-driven state machine)
│   ├── payment_activities.go         # ✅ MarkPlanDefaulted, MarkPlanCompleted (HTTP PATCH)
│   ├── contact_workflow_test.go      # ✅ 5 test cases using Temporal test suite
│   ├── DESIGN.md                     # ✅ Design notes
│   └── worker/
│       └── main.go                   # ✅ Temporal worker binary (registers both ContactWorkflow and PaymentPlanWorkflow)
│
├── payment/
│   ├── payment.go                    # ✅ Service + 4 public handlers (ProposePlan, AcceptPlan, RecordPayment, GetPlan) + 2 private (MarkDefaulted, MarkCompleted)
│   ├── models.go                     # ✅ PaymentPlan, PaymentEvent, ProposePlanReq, RecordPaymentReq
│   ├── events.go                     # ✅ PaymentUpdatedEvent (with OccurredAt) + payment-updated topic
│   ├── payment_test.go              # ✅ Table-driven tests: propose, accept, record payment lifecycle, get plan
│   ├── DESIGN.md                     # ✅ Design notes
│   └── migrations/
│       └── 1_create_tables.up.sql    # ✅ plan_status enum, payment_plans, payment_events tables
│
└── docs/
    ├── PRD.md                        # ✅ Product requirements
    └── TD.md                         # ✅ Technical design (this file)
```

---

## Testing Strategy

### Unit Tests (compliance module — aim for >90% coverage)

```go
// rules_test.go — parametrized timezone tests
func TestTimeWindowCheck(t *testing.T) {
    tests := []struct {
        name      string
        timezone  string
        checkTime time.Time // UTC
        wantAllow bool
    }{
        {"NYC 10am OK",             "America/New_York",  utc(14, 0), true},  // 10am ET
        {"NYC 7am blocked",         "America/New_York",  utc(11, 0), false}, // 7am ET
        {"NYC 9:01pm blocked",      "America/New_York",  utc(1, 1),  false}, // 9:01pm ET
        {"Hawaii 8pm OK",           "Pacific/Honolulu",  utc(6, 0),  true},
        {"edge: exactly 8am OK",    "America/New_York",  utc(12, 0), true},
        {"edge: exactly 9pm OK",    "America/New_York",  utc(1, 0),  true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ...
        })
    }
}

// Rolling window frequency tests
// PII sanitizer: SSNs, credit cards, phone numbers, edge cases
// Scorecard evaluator: rubric matching, partial matches, missing keywords
```

### Integration Tests (API lifecycle)

```go
func TestContactLifecycle(t *testing.T) {
    // 1. Create consumer
    // 2. Create account
    // 3. Initiate contact → verify compliance pre-check passes
    // 4. Verify contact_attempts row written
    // 5. Verify audit_log entry exists
    // 6. Revoke consent
    // 7. Initiate contact again → verify blocked with violation details
}
```

### Temporal Workflow Tests

Use Temporal's test framework (`go.temporal.io/sdk/testsuite`):

```go
func TestContactWorkflow_Blocked(t *testing.T) {
    suite := testsuite.WorkflowTestSuite{}
    env   := suite.NewTestWorkflowEnvironment()

    // Mock compliance check to return blocked
    env.OnActivity(activities.CheckCompliance, mock.Anything, mock.Anything).
        Return(&ContactCheckResult{Allowed: false, Violations: []Violation{{Rule: "attorney_block"}}}, nil)

    env.ExecuteWorkflow(ContactWorkflow, input)

    require.True(t, env.IsWorkflowCompleted())
    // Verify workflow returned early, no delivery attempted
}
```

---

## Observability

### Technology Stack

**Recommendation: Prometheus + Grafana over Encore's built-in metrics.**

Encore has its own metrics API (`encore.dev/metrics`) and ships a local dashboard. It is fine for a single-service demo. For this platform it is the wrong default because:

1. **Temporal already speaks Prometheus.** Temporal Server exposes a `/metrics` endpoint natively. The Go SDK has a first-class Prometheus client. Using Encore's metrics would give you two separate metric stores — one for Encore services, one for Temporal — with no unified dashboard.
2. **Encore metrics export to Prometheus anyway.** Encore's `encore.dev/metrics` package has a Prometheus exporter. There is no benefit to the abstraction layer; you pay the indirection without gaining portability.
3. **Grafana Mimir / Thanos at scale.** If this platform reaches multi-region, Prometheus federation and Mimir are standard. Encore's metrics don't participate in that ecosystem.

The resulting stack:

| Concern | Tool | Why |
|---|---|---|
| Metrics scrape + storage | Prometheus | Native Temporal support, standard Go ecosystem |
| Dashboards + alerting | Grafana | Works with Prometheus, Temporal has official dashboards |
| Distributed tracing | OpenTelemetry → Jaeger (dev) / Datadog (prod) | OTel is vendor-neutral; Encore exports OTel traces |
| Structured logging | `encore.dev/rlog` → stdout → Loki (or CloudWatch) | rlog emits JSON; ship to any log aggregator |

Encore's built-in local dashboard is still useful during development — it visualises traces and API calls without any setup. Use it locally; use the Prometheus/Grafana stack in staging and production.

---

### Logging Standards

#### What Encore provides automatically

- Every API request gets a trace ID injected into the context.
- `rlog` emits structured JSON lines to stdout; Encore's dashboard parses them locally.
- Request duration and status code are logged at the API boundary without any code.

#### Log levels

| Level | When to use |
|---|---|
| `rlog.Debug` | Read path lookups (`GetConsumer`, `GetAccount`, `ListAccountsByConsumer`). High-frequency, disabled in production unless debugging. |
| `rlog.Info` | State changes that succeeded: consumer created, status updated, consent revoked, event published. One log per mutating operation. |
| `rlog.Warn` | Recoverable unexpected conditions: a retry succeeded, a non-critical feature is degraded. Currently unused; will be used for Temporal retry events. |
| `rlog.Error` | Failures that are returned to the caller or that skip a side-effect (e.g., event publish failed). Always include the causal error. |

#### Required fields on every structured log call

```go
rlog.Info("consumer created",
    "service",     "consumer",        // always — identifies the emitting service
    "id",          c.ID,              // entity ID where applicable
    "trace_id",    ...,               // Encore injects this automatically
    "external_id", c.ExternalID,      // domain key for cross-system correlation
)
```

Encore injects `trace_id` automatically from the context — you do not need to pass it explicitly to `rlog`. All other fields must be added manually at the call site.

#### What must never appear in logs

- `account_number` — GLBA-regulated PII
- Raw SSN, credit card, phone number — any field the PII sanitizer would redact
- Full `message_content` from contact attempts — always log the sanitized version
- JWT tokens, API keys, DB connection strings

The compliance PII sanitizer (`SanitizePII`) operates on free-text interaction content before storage, not on log messages. Log discipline (not logging sensitive fields in the first place) is the control for structured fields.

#### Temporal worker logs

Temporal activities should log at the same levels as API handlers. Add `workflow_id` and `run_id` as standard fields in every activity log call:

```go
rlog.Info("compliance check completed",
    "workflow_id", workflowInfo.WorkflowExecution.ID,
    "run_id",      workflowInfo.WorkflowExecution.RunID,
    "consumer_id", input.ConsumerID,
    "allowed",     result.Allowed,
)
```

---

### Metric Definitions

All metrics are defined using `encore.dev/metrics` (which exports to Prometheus) or registered directly with the Prometheus client if the indirection is not wanted.

#### Compliance service

```go
// compliance_check_duration_ms — histogram, p99 target < 50ms
var ComplianceCheckDuration = metrics.NewHistogram[int64]("compliance_check_duration_ms", metrics.HistogramConfig{
    Buckets: []float64{5, 10, 25, 50, 100, 250},
})

// compliance_violation_total — counter, labelled by rule name
// Labels: rule = time_window | frequency_cap | attorney_block | consent_check | opt_out_validation
var ComplianceViolations = metrics.NewCounterGroup[complianceLabels, uint64]("compliance_violation_total", metrics.CounterConfig{})
type complianceLabels struct {
    Rule string
}
```

#### Contact service / Temporal workflow

```go
// contact_attempt_total — counter, labelled by channel and outcome
// Labels: channel = sms|email|voice, outcome = allowed|blocked|failed
var ContactAttempts = metrics.NewCounterGroup[contactLabels, uint64]("contact_attempt_total", metrics.CounterConfig{})

// contact_workflow_duration_ms — histogram, p99 target < 2000ms
var ContactWorkflowDuration = metrics.NewHistogram[int64]("contact_workflow_duration_ms", metrics.HistogramConfig{
    Buckets: []float64{100, 250, 500, 1000, 2000, 5000},
})
```

#### Consumer service

```go
// consent_revocation_total — counter; spikes here are a leading indicator of compliance problems
var ConsentRevocations = metrics.NewCounter[uint64]("consent_revocation_total", metrics.CounterConfig{})

// consent_event_publish_error_total — counter; any value > 0 requires immediate investigation
var ConsentPublishErrors = metrics.NewCounter[uint64]("consent_event_publish_error_total", metrics.CounterConfig{})
```

#### Account service

```go
// account_status_transition_total — counter, labelled by from and to status
var AccountStatusTransitions = metrics.NewCounterGroup[statusLabels, uint64]("account_status_transition_total", metrics.CounterConfig{})
type statusLabels struct {
    From string
    To   string
}
```

#### Temporal (provided by the SDK, no code needed)

The Temporal Go SDK reports these to Prometheus automatically when a `prometheus.Reporter` is registered on the worker:

| Metric | What it measures |
|---|---|
| `temporal_workflow_completed` | Completed workflows by type and status |
| `temporal_workflow_execution_latency` | End-to-end workflow duration |
| `temporal_activity_execution_latency` | Per-activity duration |
| `temporal_task_queue_poll_empty` | Worker starvation (queue empty on poll) |
| `temporal_schedule_to_start_latency` | Time between scheduling and starting an activity — spikes here mean worker capacity issues |

Register the Prometheus reporter in the worker:

```go
// workflows/worker/main.go
import (
    "github.com/uber-go/tally/v4/prometheus"
    "go.temporal.io/sdk/client"
)

reporter := prometheus.NewReporter(prometheus.Configuration{
    ListenAddress: "0.0.0.0:9090",
    TimerType:     "histogram",
})
c, err := client.Dial(client.Options{
    MetricsHandler: sdktally.NewMetricsHandler(reporter.UserScope()),
})
```

---

### Trace Propagation

#### Encore → Encore (automatic)

Encore injects OpenTelemetry trace context into all HTTP calls between services. No code required. The local dashboard visualises these spans automatically.

#### Encore → Pub/Sub → Subscriber (automatic)

Encore propagates trace context in Pub/Sub message metadata. The subscriber's handler receives a context with the original trace ID already set. The trace appears as a single span tree from the original API call through the subscriber execution.

#### Encore API → Temporal Worker (manual — requires interceptor)

This is the only gap Encore doesn't fill. When `contact.InitiateContact` starts a Temporal workflow, the OTel trace context from the HTTP request must cross the Temporal execution boundary.

**Solution: Temporal's OpenTelemetry interceptor.**

```go
// In the Temporal worker setup (workflows/worker/main.go):
import (
    "go.temporal.io/sdk/contrib/opentelemetry"
    "go.temporal.io/sdk/interceptor"
)

tracingInterceptor, err := opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{
    Tracer: otel.Tracer("temporal-worker"),
})

w := worker.New(temporalClient, "contact-workflow", worker.Options{
    Interceptors: []interceptor.WorkerInterceptor{tracingInterceptor},
})
```

```go
// In the Encore contact service, when starting the workflow:
import "go.temporal.io/sdk/contrib/opentelemetry"

startOptions := client.StartWorkflowOptions{
    ID:        workflowID,
    TaskQueue: "contact-workflow",
}
// The interceptor on the Temporal client side propagates the OTel context
// from ctx into the workflow's first activity automatically.
run, err := temporalClient.ExecuteWorkflow(ctx, startOptions, workflows.ContactWorkflow, input)
```

The interceptor extracts the active OTel span from `ctx` and injects it as a Temporal header. When the worker picks up the workflow, the interceptor reconstructs the span and makes it the parent for all activities in that workflow execution. The result: a single Jaeger/Datadog trace from `POST /contact/initiate` through every Temporal activity.

#### Correlation ID (belt-and-suspenders for log correlation)

Even with OTel traces, add a `correlation_id` to Temporal workflow inputs as a string. It makes log correlation possible even in environments where a trace collector is not configured:

```go
type ContactWorkflowInput struct {
    ConsumerID    int64  `json:"consumer_id"`
    AccountID     int64  `json:"account_id"`
    Channel       string `json:"channel"`
    CorrelationID string `json:"correlation_id"` // pass encore.CurrentRequest().TraceID
}
```

Every activity logs `"correlation_id", input.CorrelationID` so you can grep across Encore logs and Temporal worker logs with a single ID.

---

### SLIs, SLOs, and Alerting Thresholds

These map directly from the PRD's performance targets to Prometheus alerting rules.

| SLI | SLO | Prometheus alert |
|---|---|---|
| `compliance_check_duration_ms` p99 | < 50ms | Fire if p99 > 50ms for 5 consecutive minutes |
| `contact_workflow_duration_ms` p99 | < 2000ms | Fire if p99 > 2000ms for 5 consecutive minutes |
| `contact_attempt_total{outcome="blocked"}` / total | Monitor (no hard SLO — block rate is a business signal, not an error) | Alert if block rate > 80% over 1 hour (indicates a data or config problem) |
| Temporal workflow completion rate | 99.9% | Alert if `temporal_workflow_completed{status="failed"}` / total > 0.1% over 1 hour |
| `consent_event_publish_error_total` | 0 | Alert immediately on any increment — this is a compliance risk |
| `temporal_schedule_to_start_latency` p99 | < 500ms | Alert if p99 > 500ms — indicates worker starvation |

Example Prometheus alerting rule for the compliance SLO:

```yaml
# prometheus/alerts.yml
groups:
  - name: compliance-platform
    rules:
      - alert: ComplianceCheckTooSlow
        expr: histogram_quantile(0.99, rate(compliance_check_duration_ms_bucket[5m])) > 50
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Compliance pre-check p99 exceeds 50ms SLO"

      - alert: ConsentEventPublishFailed
        expr: increase(consent_event_publish_error_total[5m]) > 0
        for: 0m
        labels:
          severity: critical
        annotations:
          summary: "consent-changed event failed to publish — pending contacts may not be cancelled"
```

---

### Health Check Endpoint

Add to the `contact` service (the entrypoint for agents):

```go
//encore:api public method=GET path=/health
func (s *Service) Health(ctx context.Context) (*HealthResponse, error) {
    // 1. Verify DB connectivity
    if err := db.QueryRow(ctx, "SELECT 1").Scan(new(int)); err != nil {
        return nil, &errs.Error{Code: errs.Unavailable, Message: "db unhealthy"}
    }
    // 2. Verify Temporal connectivity
    if _, err := temporalClient.DescribeNamespace(ctx, "default"); err != nil {
        return nil, &errs.Error{Code: errs.Unavailable, Message: "temporal unhealthy"}
    }
    return &HealthResponse{Status: "ok"}, nil
}
```

This endpoint is what load balancers and uptime monitors call. It is distinct from Prometheus metrics scraping, which measures ongoing performance rather than binary up/down.

---

## Build Order

### Phase 1: Foundation ✅
1. `consumer` service — DB migration + CRUD APIs + table-driven tests
2. `account` service — DB migration + CRUD APIs + table-driven tests

### Phase 2: Compliance Engine ✅
3. `compliance` service — rules engine (pre-contact check) with `Rule` interface
4. PII sanitizer — regex-based redaction (CC → SSN → phone ordering)
5. Scorecard evaluator — keyword-based, JSON-configurable rubrics
6. **Heavy unit tests** for all three (30+ table-driven cases for rules)

### Phase 3: Contact Orchestration + Audit + Scoring ✅
7. `contact` service — DB + API + Temporal workflow trigger
8. `workflows/` package — ContactWorkflow with 7 activities, HTTP-only (no Encore imports)
9. `audit` service — append-only log + Pub/Sub subscribers for contact events
10. `scoring` service — subscriber skeleton wired to `interaction-created`
11. Pub/Sub topics: `contact-attempted`, `interaction-created`, `consent-changed`
12. Consent revocation subscriber cancels pending contacts
13. Workflow tests using `go.temporal.io/sdk/testsuite`

### Phase 4: Audit Pipeline + Scoring Implementation ✅
14. `payment/events.go` — `PaymentUpdated` topic stub (no CRUD yet)
15. `consumer/events.go` — Added `ConsumerLifecycle` topic; `consumer.go` publishes on create and on both consent grant/revoke
16. `account/events.go` — Added `AccountLifecycle` topic; `account.go` publishes on create and status update
17. `contact/contact.go` — Added `PATCH /contact/attempts/:id/scorecard` private endpoint
18. `audit/migrations/2_enforce_append_only.up.sql` — DB trigger enforcing append-only
19. `audit/audit.go` — Added `SearchAuditLog` (filtered POST endpoint); refactored to shared `queryAuditLog` helper
20. `audit/subscribers.go` — All 6 subscribers wired; idempotency via `isDuplicate`/`buildMetadata`/dedup keys
21. `scoring/subscribers.go` — Full implementation: score via `compliance.ScoreInteraction`, write back via `contact.UpdateScorecardResult`
22. Tests: audit filter/time-range/idempotency/append-only/subscriber tests; scoring full/partial/empty/idempotency tests; integration test

### Phase 5: Payment Plans ✅
23. `payment/migrations/1_create_tables.up.sql` — `plan_status` enum, `payment_plans`, `payment_events` tables with indexes
24. `payment/models.go` — `PaymentPlan`, `PaymentEvent`, request/response structs
25. `payment/payment.go` — Service + 4 public handlers (`ProposePlan`, `AcceptPlan`, `RecordPayment`, `GetPlan`) + 2 private (`MarkDefaulted`, `MarkCompleted`) + scan helpers
26. `payment/events.go` — Added `OccurredAt time.Time` to `PaymentUpdatedEvent`; moved `//encore:service` to `payment.go`
27. `payment/payment_test.go` — 4 table-driven test functions (~13 cases) covering full lifecycle
28. `workflows/payment_models.go` — `PaymentPlanInput`, `MarkPlanInput` (pure Go, no Encore imports)
29. `workflows/payment_workflow.go` — Signal-driven `PaymentPlanWorkflow` (72h accept timeout, installment tracking, 3-day grace, 3-miss default)
30. `workflows/payment_activities.go` — `MarkPlanDefaulted`, `MarkPlanCompleted` (HTTP PATCH via `doRequest`)
31. `workflows/activities.go` — Refactored `post` to use new `doRequest` helper (supports POST + PATCH)
32. `workflows/worker/main.go` — Registers `PaymentPlanWorkflow` alongside `ContactWorkflow`

### Phase 6: Polish (next)
33. ADR document — key design decisions: Encore over custom framework, Temporal over cron jobs, HTTP-only worker pattern, append-only audit, dedup key design
34. Test coverage report — target > 90% on `compliance/`; measure all services
35. OpenTelemetry integration — Temporal OTel interceptor on worker + contact service for end-to-end trace from `POST /contact/initiate` through every activity
36. README with architecture diagram
37. Fix `payment_received` audit dedup key — include `occurred_at` so each installment payment produces a distinct audit entry (Phase 5 known limitation)

---

## Interview Talking Points (mapped to your 7 requirements)

| # | Requirement                      | What you demo                                                                                                                                                               |
|---|----------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 1 | Data pipelines / event-driven    | Encore Pub/Sub topics + subscribers, Temporal workflow orchestration, transactional outbox pattern discussion                                                               |
| 2 | Product mindset                  | "When compliance blocks a contact, I don't just return 403 — I record *why*, publish an event for analytics, and make it queryable for the ops team. The audit trail is regulatory-ready." |
| 3 | Security / privacy / auditing    | PII sanitizer, append-only audit log, consent propagation via events, encrypted-at-rest account numbers, TCPA/FDCPA rule encoding                                           |
| 4 | Code quality                     | Table-driven tests, clean service boundaries, type-safe APIs via Encore, Go idioms (errors as values, interfaces for testability)                                            |
| 5 | Testing & design reviews         | Parametrized compliance tests, Temporal workflow test suite, integration tests, ADR as design review artifact                                                               |
| 6 | Reliability / observability      | Temporal's durable execution (crash recovery), Encore's built-in tracing, correlation IDs through workflows, structured logging                                              |
| 7 | End-to-end feature shipping      | Full flow: DB migration → API → Temporal workflow → Pub/Sub event → audit log → queryable via API                                                                           |