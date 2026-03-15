# Compliance Platform

A Go backend platform powering AI credit-servicing agents with TCPA/FDCPA compliance enforcement, durable workflow orchestration, PII sanitization, QA scoring, payment plan lifecycle management, and an immutable audit trail. Built with [Encore.go](https://encore.dev) for type-safe APIs and auto-provisioned infrastructure, and [Temporal](https://temporal.io) for crash-recoverable multi-step workflows.

## Architecture

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

## Prerequisites

- **Go 1.22+**
- **Encore CLI** — `curl -L https://encore.dev/install.sh | bash`
- **Temporal CLI** — `brew install temporal` or see [Temporal docs](https://docs.temporal.io/cli)

## Local Setup

Start three processes in separate terminals:

```bash
# Terminal 1: Encore app (API + PostgreSQL + Pub/Sub)
encore run

# Terminal 2: Temporal server
temporal server start-dev

# Terminal 3: Temporal worker
go run ./workflows/worker/main.go
```

## Smoke Test

```bash
# 1. Create a consumer
curl -s -X POST http://localhost:4000/consumers -d '{
  "external_id": "TEST-001",
  "first_name": "Jane",
  "last_name": "Doe",
  "timezone": "America/New_York",
  "consent_status": "granted"
}' | jq .

# 2. Create an account (use the consumer ID from step 1)
curl -s -X POST http://localhost:4000/accounts -d '{
  "consumer_id": 1,
  "external_account_id": "ACCT-001",
  "account_number": "1234567890",
  "balance": 5000.00
}' | jq .

# 3. Initiate a contact workflow
curl -s -X POST http://localhost:4000/contact/initiate -d '{
  "consumer_id": 1,
  "account_id": 1,
  "channel": "sms",
  "message_content": "Hello Jane, this is regarding your account."
}' | jq .
```

## Service Map

| Service | Responsibility | Key APIs |
|---------|---------------|----------|
| `consumer/` | Consumer CRUD, consent management, attorney flag | `POST /consumers`, `GET /consumers/:id`, `PUT /consumers/:id/consent` |
| `account/` | Account CRUD, status transitions | `POST /accounts`, `GET /accounts/:id`, `PUT /accounts/:id/status` |
| `compliance/` | Rules engine (5 TCPA/FDCPA rules), PII sanitizer, QA scorecard | `POST /compliance/check`, `POST /compliance/sanitize`, `POST /compliance/score` |
| `contact/` | Contact workflow orchestration via Temporal | `POST /contact/initiate`, `GET /consumers/:id/contacts` |
| `payment/` | Payment plan lifecycle (proposed → accepted → active → completed/defaulted) | `POST /payments/propose`, `POST /payments/:id/accept`, `POST /payments/:id/record` |
| `audit/` | Append-only audit log, subscribes to all Pub/Sub topics | `GET /audit/:entityType/:entityId`, `POST /audit/search` |
| `scoring/` | Async QA scoring, subscribes to `interaction-created` | (subscriber only) |
| `workflows/` | Temporal workflow + activity definitions | (worker binary: `go run ./workflows/worker/main.go`) |

## Links

- [Technical Design](docs/TD.md)
- [Product Requirements](docs/PRD.md)
- [Architecture Decision Records](docs/adr/001-architecture-decisions.md)
- [Test Coverage](docs/coverage.md)
- [Observability](docs/observability.md)
